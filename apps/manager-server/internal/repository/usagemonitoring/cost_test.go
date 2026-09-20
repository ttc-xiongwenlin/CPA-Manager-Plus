package usagemonitoring_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

const costHourMS = int64(time.Hour / time.Millisecond)

// costMonitoringEvent shares one credential and API key across providers so
// every cost-bearing reader groups the same events by model only. Cache tokens
// stay zero so the expected cost is prompt and completion price alone.
func costMonitoringEvent(hash string, timestampMS int64, provider, modelID string, inputTokens, outputTokens int64) usage.Event {
	return usage.Event{
		EventHash:            hash,
		TimestampMS:          timestampMS,
		Timestamp:            time.UnixMilli(timestampMS).UTC().Format(time.RFC3339Nano),
		Provider:             provider,
		ExecutorType:         "codex",
		Model:                modelID,
		RequestedModel:       modelID,
		ResolvedModel:        modelID,
		AuthIndex:            "auth-cost",
		Source:               "cost.json",
		SourceHash:           "hash-cost",
		APIKeyHash:           "key-cost",
		AccountSnapshot:      "cost@example.com",
		AuthLabelSnapshot:    "label-cost",
		AuthFileSnapshot:     "cost.json",
		AuthProviderSnapshot: provider,
		ServiceTier:          "default",
		InputTokens:          inputTokens,
		OutputTokens:         outputTokens,
		TotalTokens:          inputTokens + outputTokens,
		CreatedAtMS:          timestampMS,
	}
}

// modelCost is what every cost-bearing reader must agree on per model.
type modelCost struct {
	usage.CostTotals
	Calls int64
}

type modelCosts map[string]modelCost

func (m modelCosts) add(model string, calls int64, cost usage.CostTotals) {
	entry := m[model]
	entry.Calls += calls
	entry.AddCost(cost)
	m[model] = entry
}

func collectModelCosts[T any](t *testing.T, name string, rows []T, available bool, err error, row func(T) (string, int64, usage.CostTotals)) modelCosts {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if !available {
		t.Fatalf("%s unavailable", name)
	}
	result := modelCosts{}
	for _, item := range rows {
		model, calls, cost := row(item)
		result.add(model, calls, cost)
	}
	return result
}

// readAllModelCosts runs every cost-bearing monitoring reader over the same
// window and returns each one's per-model totals keyed by reader name.
func readAllModelCosts(t *testing.T, ctx context.Context, db *store.Store, filter store.AnalyticsFilter, windows []store.AccountWindowUsageQuery) map[string]modelCosts {
	t.Helper()
	result := map[string]modelCosts{}

	models, _, available, err := db.UsageMonitoringModelStats(ctx, filter)
	result["model stats"] = collectModelCosts(t, "model stats", models, available, err, func(row store.ModelStat) (string, int64, usage.CostTotals) {
		return row.Model, row.Calls, row.CostTotals
	})
	accounts, _, available, err := db.UsageMonitoringAccountStats(ctx, filter)
	result["account stats"] = collectModelCosts(t, "account stats", accounts, available, err, func(row store.AccountModelStat) (string, int64, usage.CostTotals) {
		return row.Model, row.Calls, row.CostTotals
	})
	apiKeys, _, available, err := db.UsageMonitoringAPIKeyStats(ctx, filter)
	result["api key stats"] = collectModelCosts(t, "api key stats", apiKeys, available, err, func(row store.APIKeyModelStat) (string, int64, usage.CostTotals) {
		return row.Model, row.Calls, row.CostTotals
	})
	timeline, _, available, err := db.UsageMonitoringTimeline(ctx, filter)
	result["timeline"] = collectModelCosts(t, "timeline", timeline, available, err, func(row store.UsageMonitoringTimelineHourRow) (string, int64, usage.CostTotals) {
		return row.Model, row.Calls, row.CostTotals
	})
	apiKeyTimeline, _, available, err := db.UsageMonitoringAPIKeyTimeline(ctx, filter)
	result["api key timeline"] = collectModelCosts(t, "api key timeline", apiKeyTimeline, available, err, func(row store.UsageMonitoringAPIKeyTimelineHourRow) (string, int64, usage.CostTotals) {
		return row.Model, row.Calls, row.CostTotals
	})
	credentialTimeline, _, available, err := db.UsageMonitoringCredentialTimeline(ctx, filter)
	result["credential timeline"] = collectModelCosts(t, "credential timeline", credentialTimeline, available, err, func(row store.UsageMonitoringCredentialTimelineHourRow) (string, int64, usage.CostTotals) {
		return row.Model, row.Calls, row.CostTotals
	})
	windowStats, _, available, err := db.UsageMonitoringAccountWindowStats(ctx, windows)
	result["account window stats"] = collectModelCosts(t, "account window stats", windowStats, available, err, func(row store.AccountWindowModelStat) (string, int64, usage.CostTotals) {
		return row.Model, row.Calls, row.CostTotals
	})
	return result
}

func assertModelCosts(t *testing.T, phase string, got map[string]modelCosts, want modelCosts) {
	t.Helper()
	for name, costs := range got {
		if !reflect.DeepEqual(costs, want) {
			t.Fatalf("%s: %s cost by model = %#v, want %#v", phase, name, costs, want)
		}
	}
}

func costWindow(fromMS, toMS int64) []store.AccountWindowUsageQuery {
	return []store.AccountWindowUsageQuery{{
		RequestIndex:         0,
		FromMS:               fromMS,
		ToMS:                 toMS,
		AccountSnapshot:      "cost@example.com",
		AuthFileSnapshot:     "cost.json",
		AuthProviderSnapshot: "codex",
		AuthIndex:            "auth-cost",
		Source:               "cost.json",
	}}
}

func TestUsageMonitoringReadersSumStoredEventCost(t *testing.T) {
	sqlDB, db := newMonitoringRepositoryStore(t)
	ctx := context.Background()
	day0 := int64(1_800_057_600_000)
	if _, err := db.SaveProviderPrices(ctx, []store.ProviderModelPrice{{
		Provider: "openai-compatible-deepseek", Model: "deepseek-flash", Prompt: 2, Completion: 8,
	}}); err != nil {
		t.Fatalf("save provider prices: %v", err)
	}
	if err := db.SaveModelPrices(ctx, map[string]store.ModelPrice{
		"gpt-x": {Prompt: 1, Completion: 2, PromptConfigured: true, CompletionConfigured: true},
	}); err != nil {
		t.Fatalf("save model prices: %v", err)
	}
	if _, err := db.InsertEvents(ctx, []usage.Event{
		// 1.0M*2 + 100k*8 = 2.8 CNY from the provider rule.
		costMonitoringEvent("cost-deepseek-1", day0+costHourMS, "openai-compatible-deepseek", "deepseek-flash", 1_000_000, 100_000),
		// 1.0M*1 + 1.0M*2 = 3 USD from the default price book.
		costMonitoringEvent("cost-codex", day0+2*costHourMS, "codex", "gpt-x", 1_000_000, 1_000_000),
		costMonitoringEvent("cost-unknown", day0+3*costHourMS, "unknown", "mystery", 10, 10),
		// Second UTC day: 500k*2 = 1.0 CNY, so the stored path merges two daily rows.
		costMonitoringEvent("cost-deepseek-2", day0+testDayMS+costHourMS, "openai-compatible-deepseek", "deepseek-flash", 500_000, 0),
	}); err != nil {
		t.Fatalf("insert events: %v", err)
	}
	catchUpCostThenMonitoring := func() {
		t.Helper()
		if _, err := db.CatchUpUsageEventCost(ctx, 10, time.Now().UnixMilli()); err != nil {
			t.Fatalf("cost catch-up: %v", err)
		}
		for {
			result, err := db.CatchUpUsageMonitoringProjection(ctx, 2, time.Now().UnixMilli())
			if err != nil {
				t.Fatalf("projection catch-up: %v", err)
			}
			if !result.Pending {
				break
			}
		}
		coverage, err := db.UsageEventCostCoverage(ctx)
		if err != nil {
			t.Fatalf("cost coverage: %v", err)
		}
		for {
			result, err := db.CatchUpUsageMonitoringStatsUpTo(ctx, 2, time.Now().UnixMilli(), coverage)
			if err != nil {
				t.Fatalf("stats catch-up: %v", err)
			}
			if !result.Pending {
				break
			}
		}
	}
	catchUpCostThenMonitoring()

	// A priced event past both the projection and the stats coverage is served
	// by the raw usage_events branch of every reader: 250k*2 = 0.5 CNY.
	if _, err := db.InsertEvents(ctx, []usage.Event{
		costMonitoringEvent("cost-deepseek-tail", day0+4*costHourMS, "openai-compatible-deepseek", "deepseek-flash", 250_000, 0),
	}); err != nil {
		t.Fatalf("insert tail event: %v", err)
	}
	if _, err := db.CatchUpUsageEventCost(ctx, 10, time.Now().UnixMilli()); err != nil {
		t.Fatalf("tail cost catch-up: %v", err)
	}

	// Full UTC days with no extra filter: the daily rollups serve both days and
	// the tail comes from usage_events.
	storedFilter := store.AnalyticsFilter{FromMS: day0, ToMS: day0 + 2*testDayMS, IncludeFailed: true}
	storedWant := modelCosts{
		"deepseek-flash": {Calls: 3, CostTotals: usage.CostTotals{CostCNYNanos: 4_300_000_000}},
		"gpt-x":          {Calls: 1, CostTotals: usage.CostTotals{CostUSDNanos: 3_000_000_000}},
		"mystery":        {Calls: 1, CostTotals: usage.CostTotals{UnpricedCalls: 1}},
	}
	assertModelCosts(t, "stored days", readAllModelCosts(t, ctx, db, storedFilter, costWindow(storedFilter.FromMS, storedFilter.ToMS)), storedWant)

	// A partial day never touches the daily rollups; the projection serves the
	// covered events and usage_events serves the tail.
	partialFilter := store.AnalyticsFilter{FromMS: day0 + costHourMS/2, ToMS: day0 + 5*costHourMS, IncludeFailed: true}
	partialWant := modelCosts{
		"deepseek-flash": {Calls: 2, CostTotals: usage.CostTotals{CostCNYNanos: 3_300_000_000}},
		"gpt-x":          {Calls: 1, CostTotals: usage.CostTotals{CostUSDNanos: 3_000_000_000}},
		"mystery":        {Calls: 1, CostTotals: usage.CostTotals{UnpricedCalls: 1}},
	}
	assertModelCosts(t, "partial day", readAllModelCosts(t, ctx, db, partialFilter, costWindow(partialFilter.FromMS, partialFilter.ToMS)), partialWant)

	// A filter the daily rollups cannot serve falls back to the projection even
	// on whole days.
	cacheMissFilter := storedFilter
	cacheMissFilter.CacheStatus = "miss"
	models, _, available, err := db.UsageMonitoringModelStats(ctx, cacheMissFilter)
	projectedOnly := collectModelCosts(t, "cache-miss model stats", models, available, err, func(row store.ModelStat) (string, int64, usage.CostTotals) {
		return row.Model, row.Calls, row.CostTotals
	})
	if !reflect.DeepEqual(projectedOnly, storedWant) {
		t.Fatalf("cache-miss model stats = %#v, want %#v", projectedOnly, storedWant)
	}

	// Dropping the per-event cost rows proves which path each read used: the
	// daily rollups keep the cost they summed at catch-up time, while anything
	// read from the projection or usage_events is now unpriced.
	if _, err := sqlDB.ExecContext(ctx, `delete from usage_event_costs_v1`); err != nil {
		t.Fatalf("drop cost rows: %v", err)
	}
	storedAfterDrop := modelCosts{
		"deepseek-flash": {Calls: 3, CostTotals: usage.CostTotals{CostCNYNanos: 3_800_000_000, UnpricedCalls: 1}},
		"gpt-x":          {Calls: 1, CostTotals: usage.CostTotals{CostUSDNanos: 3_000_000_000}},
		"mystery":        {Calls: 1, CostTotals: usage.CostTotals{UnpricedCalls: 1}},
	}
	projectedAfterDrop := modelCosts{
		"deepseek-flash": {Calls: 3, CostTotals: usage.CostTotals{UnpricedCalls: 3}},
		"gpt-x":          {Calls: 1, CostTotals: usage.CostTotals{UnpricedCalls: 1}},
		"mystery":        {Calls: 1, CostTotals: usage.CostTotals{UnpricedCalls: 1}},
	}
	for name, costs := range readAllModelCosts(t, ctx, db, storedFilter, costWindow(storedFilter.FromMS, storedFilter.ToMS)) {
		want := projectedAfterDrop
		if strings.HasSuffix(name, "stats") {
			want = storedAfterDrop
		}
		if !reflect.DeepEqual(costs, want) {
			t.Fatalf("stored days without cost rows: %s cost by model = %#v, want %#v", name, costs, want)
		}
	}
	partialAfterDrop := modelCosts{
		"deepseek-flash": {Calls: 2, CostTotals: usage.CostTotals{UnpricedCalls: 2}},
		"gpt-x":          {Calls: 1, CostTotals: usage.CostTotals{UnpricedCalls: 1}},
		"mystery":        {Calls: 1, CostTotals: usage.CostTotals{UnpricedCalls: 1}},
	}
	assertModelCosts(t, "partial day without cost rows", readAllModelCosts(t, ctx, db, partialFilter, costWindow(partialFilter.FromMS, partialFilter.ToMS)), partialAfterDrop)
}

// TestUsageEventCostJoinSearchesByPrimaryKey pins that the cost join added to
// the projection and raw event sources is a rowid lookup, not a scan.
func TestUsageEventCostJoinSearchesByPrimaryKey(t *testing.T) {
	sqlDB, _ := newMonitoringRepositoryStore(t)
	for name, query := range map[string]string{
		"projection": `explain query plan select p.event_id, usage_event_costs_v1.cost_cny_nanos
			from usage_monitoring_event_projection_v1 p
			left join usage_event_costs_v1 on usage_event_costs_v1.event_id = p.event_id
			where p.event_id <= ? and p.timestamp_ms >= ? and p.timestamp_ms < ?`,
		"raw": `explain query plan select e.id, usage_event_costs_v1.cost_cny_nanos
			from usage_events e
			left join usage_event_costs_v1 on usage_event_costs_v1.event_id = e.id
			where e.id > ? and e.timestamp_ms >= ? and e.timestamp_ms < ?`,
	} {
		plan := strings.Join(explainMonitoringPlan(t, sqlDB, query, int64(1<<62), int64(1800000000000), int64(1800000200000)), "\n")
		if !strings.Contains(plan, "SEARCH usage_event_costs_v1 USING INTEGER PRIMARY KEY") {
			t.Fatalf("%s cost join plan = %q, want integer primary key search", name, plan)
		}
	}
}
