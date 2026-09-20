package usageeventcost_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/modelprice"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/providerprice"
	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/usageevent"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/usageeventcost"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/usagepricing"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

type fixture struct {
	db     *sql.DB
	costs  usageeventcost.Repository
	events usageevent.Repository
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return fixture{db: db, costs: usageeventcost.New(db), events: usageevent.New(db)}
}

func costEvent(hash string, timestampMS int64, provider, modelID string, inputTokens, cacheReadTokens, outputTokens int64) usage.Event {
	return usage.Event{
		EventHash:       hash,
		TimestampMS:     timestampMS,
		Timestamp:       "2026-09-20T00:00:00Z",
		Provider:        provider,
		Model:           modelID,
		RequestedModel:  modelID,
		ResolvedModel:   modelID,
		ServiceTier:     "auto",
		InputTokens:     inputTokens,
		CachedTokens:    cacheReadTokens,
		CacheReadTokens: cacheReadTokens,
		OutputTokens:    outputTokens,
		TotalTokens:     inputTokens + outputTokens,
		CreatedAtMS:     timestampMS,
	}
}

type costRow struct {
	Provider     string
	PricingModel string
	Source       string
	Multiplier   float64
	CNYNanos     int64
	USDNanos     int64
}

func (f fixture) readCost(t *testing.T, eventID int64) costRow {
	t.Helper()
	var row costRow
	err := f.db.QueryRow(`select cost_provider, cost_pricing_model, cost_price_source, cost_multiplier, cost_cny_nanos, cost_usd_nanos
		from usage_event_costs_v1 where event_id = ?`, eventID).Scan(&row.Provider, &row.PricingModel, &row.Source, &row.Multiplier, &row.CNYNanos, &row.USDNanos)
	if err != nil {
		t.Fatalf("read cost for event %d: %v", eventID, err)
	}
	return row
}

func (f fixture) seedPrices(t *testing.T, deepseekPrompt float64) {
	t.Helper()
	ctx := context.Background()
	if _, err := providerprice.New(f.db).ReplaceAll(ctx, []model.ProviderModelPrice{{
		Provider: "openai-compatible-deepseek", Model: "deepseek-flash",
		Prompt: deepseekPrompt, Completion: 8, CacheRead: 0.2, CacheReadConfigured: true,
	}}); err != nil {
		t.Fatalf("save provider prices: %v", err)
	}
	if err := modelprice.New(f.db).ReplaceAll(ctx, map[string]model.ModelPrice{
		"gpt-x": {Prompt: 1, Completion: 2, PromptConfigured: true, CompletionConfigured: true},
	}); err != nil {
		t.Fatalf("save model prices: %v", err)
	}
}

func TestCatchUpPricesEveryEventOnce(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.seedPrices(t, 2)
	if _, err := f.events.InsertBatch(ctx, []usage.Event{
		costEvent("e1", 1_000, "openai-compatible-deepseek", "deepseek-flash", 1_000_000, 500_000, 100_000),
		costEvent("e2", 2_000, "codex", "gpt-x", 1_000_000, 0, 1_000_000),
		costEvent("e3", 3_000, "unknown", "mystery", 10, 0, 10),
	}); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	first, err := f.costs.CatchUp(ctx, 2, 10_000)
	if err != nil {
		t.Fatalf("first catch-up: %v", err)
	}
	if first.Processed != 2 || !first.Pending || first.CoverageEventID != 2 || first.TargetEventID != 3 {
		t.Fatalf("first result = %#v", first)
	}
	second, err := f.costs.CatchUp(ctx, 2, 11_000)
	if err != nil {
		t.Fatalf("second catch-up: %v", err)
	}
	if second.Processed != 1 || second.Pending || second.CoverageEventID != 3 {
		t.Fatalf("second result = %#v", second)
	}
	state, err := f.costs.State(ctx)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if state.Status != "ready" || state.PricedThroughEventID != 3 || state.ProcessedEvents != 3 || state.LatestEventID != 3 || state.Repricing() {
		t.Fatalf("state = %#v", state)
	}

	deepseek := f.readCost(t, 1)
	// prompt 500k*2 + cache read 500k*0.2 + output 100k*8 = 1.9 CNY
	if deepseek.Source != "provider" || deepseek.CNYNanos != 1_900_000_000 || deepseek.USDNanos != 0 || deepseek.Provider != "openai-compatible-deepseek" {
		t.Fatalf("deepseek cost = %#v", deepseek)
	}
	codex := f.readCost(t, 2)
	if codex.Source != "default" || codex.USDNanos != 3_000_000_000 || codex.CNYNanos != 0 || codex.PricingModel != "gpt-x" {
		t.Fatalf("codex cost = %#v", codex)
	}
	unknown := f.readCost(t, 3)
	if unknown.Source != "none" || unknown.CNYNanos != 0 || unknown.USDNanos != 0 {
		t.Fatalf("unknown cost = %#v", unknown)
	}

	idle, err := f.costs.CatchUp(ctx, 2, 12_000)
	if err != nil || idle.Processed != 0 || idle.Pending {
		t.Fatalf("idle catch-up = %#v, %v", idle, err)
	}
}

func TestStartRepriceRevisitsEventsFromDateAndCapsCoverage(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.seedPrices(t, 2)
	if _, err := f.events.InsertBatch(ctx, []usage.Event{
		costEvent("e1", 1_000, "openai-compatible-deepseek", "deepseek-flash", 1_000_000, 0, 0),
		costEvent("e2", 2_000, "openai-compatible-deepseek", "deepseek-flash", 1_000_000, 0, 0),
		costEvent("e3", 3_000, "openai-compatible-deepseek", "deepseek-flash", 1_000_000, 0, 0),
	}); err != nil {
		t.Fatalf("insert events: %v", err)
	}
	if _, err := f.costs.CatchUp(ctx, 10, 10_000); err != nil {
		t.Fatalf("initial catch-up: %v", err)
	}
	baseRevision, err := usagepricing.StructureRevision(ctx, f.db)
	if err != nil {
		t.Fatalf("structure revision: %v", err)
	}
	if strings.Contains(baseRevision, "reprice") {
		t.Fatalf("epoch zero must not touch the revision: %s", baseRevision)
	}

	// Price change alone does nothing to stored cost.
	f.seedPrices(t, 4)
	if _, err := f.costs.CatchUp(ctx, 10, 11_000); err != nil {
		t.Fatalf("catch-up after price change: %v", err)
	}
	if got := f.readCost(t, 2).CNYNanos; got != 2_000_000_000 {
		t.Fatalf("stored cost changed without reprice: %d", got)
	}

	state, err := f.costs.StartReprice(ctx, 2_000, 12_000)
	if err != nil {
		t.Fatalf("start reprice: %v", err)
	}
	if !state.Repricing() || state.RepriceEpoch != 1 || state.RepriceTargetEventID != 3 || state.CoverageEventID() != 0 {
		t.Fatalf("reprice state = %#v", state)
	}
	revision, err := usagepricing.StructureRevision(ctx, f.db)
	if err != nil {
		t.Fatalf("structure revision after reprice: %v", err)
	}
	if revision == baseRevision || !strings.HasSuffix(revision, ":reprice-1") {
		t.Fatalf("revision must fold the epoch in: %s", revision)
	}

	// A new event arriving mid-reprice is priced with the new book right away
	// and never blocks behind the reprice scan.
	if _, err := f.events.InsertBatch(ctx, []usage.Event{
		costEvent("e4", 4_000, "openai-compatible-deepseek", "deepseek-flash", 1_000_000, 0, 0),
	}); err != nil {
		t.Fatalf("insert late event: %v", err)
	}
	result, err := f.costs.CatchUp(ctx, 2, 13_000)
	if err != nil {
		t.Fatalf("reprice batch one: %v", err)
	}
	// Budget 2: the new event first, then one repriced event (id 2); event 1 is
	// below the start date so coverage jumps straight past it.
	if result.Processed != 2 || !result.Repricing || !result.Pending || result.CoverageEventID != 2 {
		t.Fatalf("batch one = %#v", result)
	}
	if got := f.readCost(t, 4).CNYNanos; got != 4_000_000_000 {
		t.Fatalf("late event priced with old book: %d", got)
	}
	if got := f.readCost(t, 1).CNYNanos; got != 2_000_000_000 {
		t.Fatalf("event before start date must stay frozen: %d", got)
	}
	if got := f.readCost(t, 2).CNYNanos; got != 4_000_000_000 {
		t.Fatalf("event at start date should be repriced: %d", got)
	}

	result, err = f.costs.CatchUp(ctx, 2, 14_000)
	if err != nil {
		t.Fatalf("reprice batch two: %v", err)
	}
	if result.Repricing || result.Pending || result.CoverageEventID != 4 {
		t.Fatalf("batch two = %#v", result)
	}
	if got := f.readCost(t, 3).CNYNanos; got != 4_000_000_000 {
		t.Fatalf("event 3 should be repriced: %d", got)
	}
	state, err = f.costs.State(ctx)
	if err != nil || state.Repricing() || state.Status != "ready" || state.CoverageEventID() != 4 {
		t.Fatalf("final state = %#v, %v", state, err)
	}
}

func TestStartRepriceWithNoMatchingEventsFinishesImmediately(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.seedPrices(t, 2)
	if _, err := f.events.InsertBatch(ctx, []usage.Event{
		costEvent("e1", 1_000, "openai-compatible-deepseek", "deepseek-flash", 10, 0, 0),
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := f.costs.CatchUp(ctx, 10, 10_000); err != nil {
		t.Fatalf("catch-up: %v", err)
	}
	if _, err := f.costs.StartReprice(ctx, 999_999, 11_000); err != nil {
		t.Fatalf("start reprice: %v", err)
	}
	result, err := f.costs.CatchUp(ctx, 10, 12_000)
	if err != nil || result.Repricing || result.Pending || result.CoverageEventID != 1 {
		t.Fatalf("result = %#v, %v", result, err)
	}
}

func TestCatchUpCapsRollupsBehindCostCoverage(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.seedPrices(t, 2)
	if _, err := f.events.InsertBatch(ctx, []usage.Event{
		costEvent("e1", 3_600_001, "openai-compatible-deepseek", "deepseek-flash", 10, 0, 0),
		costEvent("e2", 3_600_002, "openai-compatible-deepseek", "deepseek-flash", 10, 0, 0),
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	pricingRepo := usagepricing.New(f.db)
	capped, err := pricingRepo.CatchUpUpTo(ctx, 10, 10_000, 0)
	if err != nil {
		t.Fatalf("capped catch-up: %v", err)
	}
	if capped.Processed != 0 || capped.CoverageEventID != 0 {
		t.Fatalf("cap 0 must aggregate nothing: %#v", capped)
	}
	if _, err := f.costs.CatchUp(ctx, 1, 11_000); err != nil {
		t.Fatalf("cost catch-up: %v", err)
	}
	partial, err := pricingRepo.CatchUpUpTo(ctx, 10, 12_000, 1)
	if err != nil {
		t.Fatalf("partial catch-up: %v", err)
	}
	if partial.Processed != 1 || partial.CoverageEventID != 1 {
		t.Fatalf("cap 1 should aggregate one event: %#v", partial)
	}
	uncapped, err := pricingRepo.CatchUp(ctx, 10, 13_000)
	if err != nil {
		t.Fatalf("uncapped catch-up: %v", err)
	}
	if uncapped.Processed != 1 || uncapped.CoverageEventID != 2 {
		t.Fatalf("uncapped result = %#v", uncapped)
	}
}
