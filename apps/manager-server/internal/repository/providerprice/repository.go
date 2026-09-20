// Package providerprice stores the real CNY prices providers charge per model,
// including daily time windows that scale the rate.
package providerprice

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

type Repository interface {
	LoadAll(ctx context.Context) ([]model.ProviderModelPrice, error)
	LoadAllTx(ctx context.Context, tx *sql.Tx) ([]model.ProviderModelPrice, error)
	ReplaceAll(ctx context.Context, prices []model.ProviderModelPrice) ([]model.ProviderModelPrice, error)
}

type repository struct {
	db *sql.DB
}

func New(db *sql.DB) Repository {
	return &repository{db: db}
}

func (r *repository) LoadAll(ctx context.Context) ([]model.ProviderModelPrice, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	prices, err := r.LoadAllTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return prices, nil
}

func (r *repository) LoadAllTx(ctx context.Context, tx *sql.Tx) ([]model.ProviderModelPrice, error) {
	rows, err := tx.QueryContext(ctx, `select
		id, provider, model, prompt_per_1m, completion_per_1m,
		cache_read_per_1m, cache_creation_per_1m, cache_read_configured, cache_creation_configured,
		timezone, coalesce(note, ''), coalesce(off_days, ''), updated_at_ms
		from provider_model_prices order by provider, model`)
	if err != nil {
		return nil, err
	}
	prices := make([]model.ProviderModelPrice, 0)
	index := map[int64]int{}
	for rows.Next() {
		var price model.ProviderModelPrice
		var cacheReadConfigured, cacheCreationConfigured int
		var offDays string
		if err := rows.Scan(
			&price.ID,
			&price.Provider,
			&price.Model,
			&price.Prompt,
			&price.Completion,
			&price.CacheRead,
			&price.CacheCreation,
			&cacheReadConfigured,
			&cacheCreationConfigured,
			&price.Timezone,
			&price.Note,
			&offDays,
			&price.UpdatedAtMS,
		); err != nil {
			_ = rows.Close()
			return nil, err
		}
		price.CacheReadConfigured = cacheReadConfigured != 0
		price.CacheCreationConfigured = cacheCreationConfigured != 0
		price.OffDays = splitList(offDays)
		index[price.ID] = len(prices)
		prices = append(prices, price)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	windowRows, err := tx.QueryContext(ctx, `select
		id, price_id, start_minute, end_minute, multiplier, coalesce(label, ''), coalesce(weekdays, '')
		from provider_model_price_windows order by price_id, start_minute, id`)
	if err != nil {
		return nil, err
	}
	defer windowRows.Close()
	for windowRows.Next() {
		var priceID int64
		var window model.ProviderPriceWindow
		var weekdays string
		if err := windowRows.Scan(&window.ID, &priceID, &window.StartMinute, &window.EndMinute, &window.Multiplier, &window.Label, &weekdays); err != nil {
			return nil, err
		}
		window.Weekdays = parseWeekdays(weekdays)
		position, ok := index[priceID]
		if !ok {
			continue
		}
		prices[position].Windows = append(prices[position].Windows, window)
	}
	if err := windowRows.Err(); err != nil {
		return nil, err
	}
	return prices, nil
}

// ReplaceAll makes the stored rule set equal to the supplied one. Existing
// (provider, model) rows keep their ids so event cost audit references stay
// meaningful; windows are rewritten. The normalized, persisted rules are
// returned.
func (r *repository) ReplaceAll(ctx context.Context, prices []model.ProviderModelPrice) ([]model.ProviderModelPrice, error) {
	normalized := make([]model.ProviderModelPrice, 0, len(prices))
	seen := map[[2]string]bool{}
	for _, price := range prices {
		entry, err := model.NormalizeProviderModelPrice(price)
		if err != nil {
			return nil, err
		}
		key := [2]string{entry.Provider, entry.Model}
		if seen[key] {
			return nil, fmt.Errorf("duplicate provider price for %s/%s", entry.Provider, entry.Model)
		}
		seen[key] = true
		normalized = append(normalized, entry)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UnixMilli()
	upsert, err := tx.PrepareContext(ctx, `insert into provider_model_prices (
		provider, model, prompt_per_1m, completion_per_1m,
		cache_read_per_1m, cache_creation_per_1m, cache_read_configured, cache_creation_configured,
		timezone, note, off_days, updated_at_ms
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	on conflict(provider, model) do update set
		prompt_per_1m = excluded.prompt_per_1m,
		completion_per_1m = excluded.completion_per_1m,
		cache_read_per_1m = excluded.cache_read_per_1m,
		cache_creation_per_1m = excluded.cache_creation_per_1m,
		cache_read_configured = excluded.cache_read_configured,
		cache_creation_configured = excluded.cache_creation_configured,
		timezone = excluded.timezone,
		note = excluded.note,
		off_days = excluded.off_days,
		updated_at_ms = excluded.updated_at_ms
	returning id`)
	if err != nil {
		return nil, err
	}
	defer upsert.Close()
	insertWindow, err := tx.PrepareContext(ctx, `insert into provider_model_price_windows (
		price_id, start_minute, end_minute, multiplier, label, weekdays
	) values (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return nil, err
	}
	defer insertWindow.Close()

	keptIDs := make([]any, 0, len(normalized))
	for position := range normalized {
		price := &normalized[position]
		price.UpdatedAtMS = now
		if err := upsert.QueryRowContext(
			ctx,
			price.Provider,
			price.Model,
			price.Prompt,
			price.Completion,
			price.CacheRead,
			price.CacheCreation,
			price.CacheReadConfigured,
			price.CacheCreationConfigured,
			price.Timezone,
			nullString(price.Note),
			strings.Join(price.OffDays, ","),
			now,
		).Scan(&price.ID); err != nil {
			return nil, err
		}
		keptIDs = append(keptIDs, price.ID)
		if _, err := tx.ExecContext(ctx, `delete from provider_model_price_windows where price_id = ?`, price.ID); err != nil {
			return nil, err
		}
		for windowPosition := range price.Windows {
			window := &price.Windows[windowPosition]
			result, err := insertWindow.ExecContext(ctx, price.ID, window.StartMinute, window.EndMinute, window.Multiplier, nullString(window.Label), joinWeekdays(window.Weekdays))
			if err != nil {
				return nil, err
			}
			window.ID, err = result.LastInsertId()
			if err != nil {
				return nil, err
			}
		}
	}

	deleteQuery := `delete from provider_model_prices`
	if len(keptIDs) > 0 {
		placeholders := make([]byte, 0, len(keptIDs)*2)
		for index := range keptIDs {
			if index > 0 {
				placeholders = append(placeholders, ',')
			}
			placeholders = append(placeholders, '?')
		}
		deleteQuery += ` where id not in (` + string(placeholders) + `)`
	}
	if _, err := tx.ExecContext(ctx, deleteQuery, keptIDs...); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return normalized, nil
}

func splitList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func joinWeekdays(weekdays []int) string {
	parts := make([]string, 0, len(weekdays))
	for _, weekday := range weekdays {
		parts = append(parts, strconv.Itoa(weekday))
	}
	return strings.Join(parts, ",")
}

func parseWeekdays(value string) []int {
	parts := splitList(value)
	if len(parts) == 0 {
		return nil
	}
	result := make([]int, 0, len(parts))
	for _, part := range parts {
		weekday, err := strconv.Atoi(part)
		if err != nil {
			continue
		}
		result = append(result, weekday)
	}
	return result
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
