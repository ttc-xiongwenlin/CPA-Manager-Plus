// Package usageeventcost prices every usage event exactly once and stores the
// result, so rollups and analytics sum stored cost instead of multiplying
// token totals by whatever the price book says at read time.
package usageeventcost

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/modelprice"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/providerprice"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/pricing"
)

const (
	RollupName        = "event_cost"
	Table             = "usage_event_costs_v1"
	defaultBatchLimit = 1000
)

// JoinSQL left-joins the cost row onto an event source aliased as eventAlias.
// Cost columns carry a cost_ prefix so unqualified event column references in
// existing queries stay unambiguous.
func JoinSQL(eventAlias string) string {
	return "left join " + Table + " on " + Table + ".event_id = " + eventAlias + ".id"
}

// SumSQL sums the joined cost columns for an aggregate select list, in the
// usage.CostTotals field order: CNY nanos, USD nanos, unpriced calls. Events
// without a cost row count as unpriced.
const SumSQL = `coalesce(sum(cost_cny_nanos), 0),
	coalesce(sum(cost_usd_nanos), 0),
	coalesce(sum(case when cost_price_source is null or cost_price_source = 'none' then 1 else 0 end), 0)`

// PassthroughColumnsSQL lists the joined cost columns for CTEs that select
// event rows and aggregate later.
const PassthroughColumnsSQL = Table + ".cost_cny_nanos, " + Table + ".cost_usd_nanos, " + Table + ".cost_price_source"

type State struct {
	Status                 string
	PricedThroughEventID   int64
	RepriceEpoch           int64
	RepriceFromMS          int64
	RepriceTargetEventID   int64
	RepricedThroughEventID int64
	ProcessedEvents        int64
	LastRunStartedAtMS     sql.NullInt64
	UpdatedAtMS            int64
	FinishedAtMS           sql.NullInt64
	LastError              string
	// LatestEventID is the newest usage event id at read time, for progress.
	LatestEventID int64
}

// Repricing reports whether a manual reprice still has events to revisit.
func (s State) Repricing() bool {
	return s.RepriceTargetEventID > 0 && s.RepricedThroughEventID < s.RepriceTargetEventID
}

// CoverageEventID is the highest event id whose stored cost is final. Rollups
// must not aggregate past it, because their incremental upserts never revisit
// an event.
func (s State) CoverageEventID() int64 {
	if s.Repricing() && s.RepricedThroughEventID < s.PricedThroughEventID {
		return s.RepricedThroughEventID
	}
	return s.PricedThroughEventID
}

type CatchUpResult struct {
	Processed       int
	CoverageEventID int64
	TargetEventID   int64
	Pending         bool
	Repricing       bool
}

type Repository interface {
	CatchUp(ctx context.Context, limit int, nowMS int64) (CatchUpResult, error)
	RecordFailure(ctx context.Context, taskErr error, nowMS int64) error
	State(ctx context.Context) (State, error)
	StartReprice(ctx context.Context, fromMS, nowMS int64) (State, error)
}

type repository struct {
	db             *sql.DB
	modelPrices    modelprice.Repository
	providerPrices providerprice.Repository
	catchUpGate    chan struct{}
}

func New(db *sql.DB) Repository {
	return &repository{
		db:             db,
		modelPrices:    modelprice.New(db),
		providerPrices: providerprice.New(db),
		catchUpGate:    make(chan struct{}, 1),
	}
}

type RowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// RepriceEpoch counts manual reprices. Derived rollups fold it into their
// structure revision so a reprice rebuilds their stored cost sums; epoch zero
// leaves the revision untouched so upgrades never force a rebuild.
func RepriceEpoch(ctx context.Context, db RowQuerier) (int64, error) {
	var epoch int64
	err := db.QueryRowContext(ctx, `select reprice_epoch from usage_event_cost_state where rollup_name = ?`, RollupName).Scan(&epoch)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return epoch, err
}

type eventRow struct {
	ID    int64
	Input pricing.EventInput
}

func (r *repository) CatchUp(ctx context.Context, limit int, nowMS int64) (CatchUpResult, error) {
	if limit <= 0 {
		limit = defaultBatchLimit
	}
	if nowMS <= 0 {
		return CatchUpResult{}, errors.New("nowMS must be greater than 0")
	}
	if err := r.acquireCatchUp(ctx); err != nil {
		return CatchUpResult{}, err
	}
	defer r.releaseCatchUp()

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return CatchUpResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `update usage_event_cost_state set last_run_started_at_ms = ? where rollup_name = ?`, nowMS, RollupName); err != nil {
		return CatchUpResult{}, err
	}
	state, err := stateQuery(ctx, tx)
	if err != nil {
		return CatchUpResult{}, err
	}
	latestID, err := latestEventID(ctx, tx)
	if err != nil {
		return CatchUpResult{}, err
	}
	defaults, err := r.modelPrices.LoadAllTx(ctx, tx)
	if err != nil {
		return CatchUpResult{}, err
	}
	providerRules, err := r.providerPrices.LoadAllTx(ctx, tx)
	if err != nil {
		return CatchUpResult{}, err
	}
	book := pricing.NewBook(defaults, providerRules)
	upsert, err := tx.PrepareContext(ctx, `insert into `+Table+` (
		event_id, cost_ts_ms, cost_provider, cost_pricing_model, cost_price_source,
		cost_price_id, cost_window_id, cost_multiplier, cost_cny_nanos, cost_usd_nanos, cost_updated_at_ms
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	on conflict(event_id) do update set
		cost_ts_ms = excluded.cost_ts_ms,
		cost_provider = excluded.cost_provider,
		cost_pricing_model = excluded.cost_pricing_model,
		cost_price_source = excluded.cost_price_source,
		cost_price_id = excluded.cost_price_id,
		cost_window_id = excluded.cost_window_id,
		cost_multiplier = excluded.cost_multiplier,
		cost_cny_nanos = excluded.cost_cny_nanos,
		cost_usd_nanos = excluded.cost_usd_nanos,
		cost_updated_at_ms = excluded.cost_updated_at_ms`)
	if err != nil {
		return CatchUpResult{}, err
	}
	defer upsert.Close()
	priceRows := func(rows []eventRow) error {
		for _, row := range rows {
			cost := book.PriceEvent(row.Input)
			if _, err := upsert.ExecContext(ctx,
				row.ID, row.Input.TimestampMS, cost.Provider, cost.PricingModel, cost.PriceSource,
				nullableID(cost.PriceID), nullableID(cost.WindowID), cost.Multiplier,
				cost.CostCNYNanos, cost.CostUSDNanos, nowMS,
			); err != nil {
				return err
			}
		}
		return nil
	}

	processed := 0
	// New events are priced first so live cost never waits behind a reprice.
	newRows, err := selectEvents(ctx, tx, `id > ?`, []any{state.PricedThroughEventID}, limit)
	if err != nil {
		return CatchUpResult{}, err
	}
	if err := priceRows(newRows); err != nil {
		return CatchUpResult{}, err
	}
	if len(newRows) > 0 {
		state.PricedThroughEventID = newRows[len(newRows)-1].ID
	}
	processed += len(newRows)

	if state.Repricing() && processed < limit {
		if state.RepricedThroughEventID == 0 {
			firstID, err := firstEventIDAtOrAfter(ctx, tx, state.RepriceFromMS)
			if err != nil {
				return CatchUpResult{}, err
			}
			switch {
			case firstID == 0 || firstID > state.RepriceTargetEventID:
				state.RepricedThroughEventID = state.RepriceTargetEventID
			case firstID > 1:
				state.RepricedThroughEventID = firstID - 1
			}
		}
		if state.Repricing() {
			budget := limit - processed
			rows, err := selectEvents(ctx, tx, `id > ? and id <= ?`, []any{state.RepricedThroughEventID, state.RepriceTargetEventID}, budget)
			if err != nil {
				return CatchUpResult{}, err
			}
			affected := rows[:0]
			for _, row := range rows {
				if row.Input.TimestampMS >= state.RepriceFromMS {
					affected = append(affected, row)
				}
			}
			if err := priceRows(affected); err != nil {
				return CatchUpResult{}, err
			}
			processed += len(affected)
			if len(rows) < budget {
				state.RepricedThroughEventID = state.RepriceTargetEventID
			} else {
				state.RepricedThroughEventID = rows[len(rows)-1].ID
			}
		}
	}

	repricing := state.Repricing()
	pending := repricing || latestID > state.PricedThroughEventID
	status := "ready"
	finishedAt := any(nowMS)
	switch {
	case repricing:
		status = "repricing"
		finishedAt = nil
	case pending:
		status = "catching_up"
		finishedAt = nil
	}
	if _, err := tx.ExecContext(ctx, `update usage_event_cost_state set
		status = ?, priced_through_event_id = ?, repriced_through_event_id = ?,
		processed_events = processed_events + ?, updated_at_ms = ?, finished_at_ms = ?, last_error = null
		where rollup_name = ?`,
		status, state.PricedThroughEventID, state.RepricedThroughEventID, processed, nowMS, finishedAt, RollupName,
	); err != nil {
		return CatchUpResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return CatchUpResult{}, err
	}
	return CatchUpResult{
		Processed:       processed,
		CoverageEventID: state.CoverageEventID(),
		TargetEventID:   latestID,
		Pending:         pending,
		Repricing:       repricing,
	}, nil
}

func (r *repository) RecordFailure(ctx context.Context, taskErr error, nowMS int64) error {
	if taskErr == nil || nowMS <= 0 {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `update usage_event_cost_state set
		status = 'failed', updated_at_ms = ?, finished_at_ms = ?, last_error = ?
		where rollup_name = ?`, nowMS, nowMS, taskErr.Error(), RollupName)
	return err
}

func (r *repository) State(ctx context.Context) (State, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return State{}, err
	}
	defer func() { _ = tx.Rollback() }()
	state, err := stateQuery(ctx, tx)
	if err != nil {
		return State{}, err
	}
	state.LatestEventID, err = latestEventID(ctx, tx)
	if err != nil {
		return State{}, err
	}
	return state, tx.Commit()
}

// StartReprice schedules every event at or after fromMS to be priced again
// with the current price book. A reprice already in flight widens to the
// earlier start and restarts its scan.
func (r *repository) StartReprice(ctx context.Context, fromMS, nowMS int64) (State, error) {
	if fromMS < 0 {
		fromMS = 0
	}
	if nowMS <= 0 {
		return State{}, errors.New("nowMS must be greater than 0")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return State{}, err
	}
	defer func() { _ = tx.Rollback() }()
	latestID, err := latestEventID(ctx, tx)
	if err != nil {
		return State{}, err
	}
	if _, err := tx.ExecContext(ctx, `update usage_event_cost_state set
		reprice_epoch = reprice_epoch + 1,
		reprice_from_ms = case
			when reprice_target_event_id > 0 and repriced_through_event_id < reprice_target_event_id then min(reprice_from_ms, ?)
			else ?
		end,
		reprice_target_event_id = ?,
		repriced_through_event_id = 0,
		status = 'repricing',
		updated_at_ms = ?,
		finished_at_ms = null,
		last_error = null
		where rollup_name = ?`, fromMS, fromMS, latestID, nowMS, RollupName); err != nil {
		return State{}, err
	}
	state, err := stateQuery(ctx, tx)
	if err != nil {
		return State{}, err
	}
	state.LatestEventID = latestID
	return state, tx.Commit()
}

func stateQuery(ctx context.Context, db RowQuerier) (State, error) {
	var state State
	var lastError sql.NullString
	err := db.QueryRowContext(ctx, `select
		status, priced_through_event_id, reprice_epoch, reprice_from_ms,
		reprice_target_event_id, repriced_through_event_id, processed_events,
		last_run_started_at_ms, updated_at_ms, finished_at_ms, last_error
		from usage_event_cost_state where rollup_name = ?`, RollupName).Scan(
		&state.Status,
		&state.PricedThroughEventID,
		&state.RepriceEpoch,
		&state.RepriceFromMS,
		&state.RepriceTargetEventID,
		&state.RepricedThroughEventID,
		&state.ProcessedEvents,
		&state.LastRunStartedAtMS,
		&state.UpdatedAtMS,
		&state.FinishedAtMS,
		&lastError,
	)
	if err != nil {
		return State{}, fmt.Errorf("read usage event cost state: %w", err)
	}
	state.LastError = lastError.String
	return state, nil
}

func latestEventID(ctx context.Context, db RowQuerier) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx, `select coalesce(max(id), 0) from usage_events`).Scan(&id)
	return id, err
}

func firstEventIDAtOrAfter(ctx context.Context, db RowQuerier, timestampMS int64) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx, `select coalesce(min(id), 0) from usage_events where timestamp_ms >= ?`, timestampMS).Scan(&id)
	return id, err
}

func selectEvents(ctx context.Context, tx *sql.Tx, condition string, args []any, limit int) ([]eventRow, error) {
	if limit <= 0 {
		return nil, nil
	}
	args = append(args, limit)
	rows, err := tx.QueryContext(ctx, `select
		id, timestamp_ms, coalesce(provider, ''), coalesce(auth_provider_snapshot, ''),
		coalesce(model, ''), coalesce(requested_model, ''), coalesce(resolved_model, ''), coalesce(service_tier, ''),
		coalesce(input_tokens, 0), normalized_total_input_tokens, coalesce(output_tokens, 0),
		coalesce(cached_tokens, 0), coalesce(cache_tokens, 0), coalesce(cache_read_tokens, 0), coalesce(cache_creation_tokens, 0)
		from usage_events where `+condition+` order by id limit ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]eventRow, 0, limit)
	for rows.Next() {
		var row eventRow
		var normalizedInput sql.NullInt64
		if err := rows.Scan(
			&row.ID,
			&row.Input.TimestampMS,
			&row.Input.Provider,
			&row.Input.AuthProviderSnapshot,
			&row.Input.Model,
			&row.Input.RequestedModel,
			&row.Input.ResolvedModel,
			&row.Input.ServiceTier,
			&row.Input.InputTokens,
			&normalizedInput,
			&row.Input.OutputTokens,
			&row.Input.CachedTokens,
			&row.Input.CacheTokens,
			&row.Input.CacheReadTokens,
			&row.Input.CacheCreationTokens,
		); err != nil {
			return nil, err
		}
		if normalizedInput.Valid {
			value := normalizedInput.Int64
			row.Input.NormalizedTotalInputTokens = &value
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func nullableID(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}

func (r *repository) acquireCatchUp(ctx context.Context) error {
	select {
	case r.catchUpGate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *repository) releaseCatchUp() {
	select {
	case <-r.catchUpGate:
	default:
	}
}
