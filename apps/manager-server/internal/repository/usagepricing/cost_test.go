package usagepricing_test

import (
	"context"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

func costPricingEvent(hash string, timestampMS int64, provider, modelID string, inputTokens, outputTokens int64) usage.Event {
	return usage.Event{
		EventHash:            hash,
		TimestampMS:          timestampMS,
		Timestamp:            "1970-01-01T01:00:00Z",
		Provider:             provider,
		AuthProviderSnapshot: provider,
		Model:                modelID,
		RequestedModel:       modelID,
		ResolvedModel:        modelID,
		AccountSnapshot:      "team-a",
		AuthFileSnapshot:     "team-a.json",
		AuthIndex:            "auth-team-a",
		InputTokens:          inputTokens,
		OutputTokens:         outputTokens,
		TotalTokens:          inputTokens + outputTokens,
		CreatedAtMS:          timestampMS,
	}
}

func TestPricingRollupSumsStoredEventCost(t *testing.T) {
	ctx := context.Background()
	cfg := testutil.NewConfig(t)
	st := testutil.NewStore(t, cfg)
	if _, err := st.SaveProviderPrices(ctx, []store.ProviderModelPrice{{
		Provider: "openai-compatible-deepseek", Model: "deepseek-flash", Prompt: 2, Completion: 8,
	}}); err != nil {
		t.Fatalf("save provider prices: %v", err)
	}
	if err := st.SaveModelPrices(ctx, map[string]store.ModelPrice{
		"gpt-x": {Prompt: 1, Completion: 2, PromptConfigured: true, CompletionConfigured: true},
	}); err != nil {
		t.Fatalf("save model prices: %v", err)
	}
	if _, err := st.UsageEvents.InsertBatch(ctx, []usage.Event{
		costPricingEvent("deepseek-1", 3_600_001, "openai-compatible-deepseek", "deepseek-flash", 1_000_000, 100_000),
		costPricingEvent("deepseek-2", 3_600_002, "openai-compatible-deepseek", "deepseek-flash", 500_000, 0),
		costPricingEvent("codex-1", 3_600_003, "codex", "gpt-x", 1_000_000, 1_000_000),
		costPricingEvent("unknown-1", 3_600_004, "unknown", "mystery", 10, 10),
	}); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	// Price the first three events, then aggregate only up to that coverage.
	if _, err := st.CatchUpUsageEventCost(ctx, 3, 10_000); err != nil {
		t.Fatalf("cost catch-up: %v", err)
	}
	cap, err := st.UsageEventCostCoverage(ctx)
	if err != nil || cap != 3 {
		t.Fatalf("coverage = %d, %v", cap, err)
	}
	if _, err := st.CatchUpUsagePricingUpTo(ctx, 10, 11_000, cap); err != nil {
		t.Fatalf("pricing catch-up: %v", err)
	}
	filter := store.UsagePricingHourlyFilter{FromMS: 3_600_000, ToMS: 7_200_000, IncludeFailed: true, CollapseBuckets: true}
	rows, state, available, err := st.UsagePricingHourlyRows(ctx, filter)
	if err != nil || !available {
		t.Fatalf("hourly rows: available=%v err=%v", available, err)
	}
	if state.CoverageEventID != 3 {
		t.Fatalf("pricing coverage = %#v", state)
	}
	byModel := map[string]store.UsagePricingHourlyRow{}
	for _, row := range rows {
		byModel[row.Model] = row
	}
	// 1.0M*2 + 100k*8 = 2.8 CNY plus 500k*2 = 1.0 CNY.
	if byModel["deepseek-flash"].CostCNYNanos != 3_800_000_000 || byModel["deepseek-flash"].CostUSDNanos != 0 || byModel["deepseek-flash"].UnpricedCalls != 0 {
		t.Fatalf("deepseek row = %#v", byModel["deepseek-flash"])
	}
	if byModel["gpt-x"].CostUSDNanos != 3_000_000_000 || byModel["gpt-x"].CostCNYNanos != 0 {
		t.Fatalf("codex row = %#v", byModel["gpt-x"])
	}
	// The fourth event is past the rollup coverage and not priced yet, so the raw
	// tail reports it as unpriced rather than inventing a cost.
	if byModel["mystery"].Calls != 1 || byModel["mystery"].UnpricedCalls != 1 || byModel["mystery"].CostCNYNanos != 0 {
		t.Fatalf("unpriced tail row = %#v", byModel["mystery"])
	}

	accountRows, _, available, err := st.UsagePricingAccountRows(ctx, []string{pricingAccountKey("team-a.json", "auth-team-a")})
	if err != nil || !available {
		t.Fatalf("account rows: available=%v err=%v", available, err)
	}
	var totals usage.CostTotals
	for _, row := range accountRows {
		totals.AddCost(row.CostTotals)
	}
	if totals.CostCNYNanos != 3_800_000_000 || totals.CostUSDNanos != 3_000_000_000 || totals.UnpricedCalls != 1 {
		t.Fatalf("account totals = %#v rows=%#v", totals, accountRows)
	}

	// Once the last event is priced and rolled up, the stored rows carry it too.
	if _, err := st.CatchUpUsageEventCost(ctx, 10, 12_000); err != nil {
		t.Fatalf("second cost catch-up: %v", err)
	}
	if _, err := st.CatchUpUsagePricingUpTo(ctx, 10, 13_000, 4); err != nil {
		t.Fatalf("second pricing catch-up: %v", err)
	}
	rows, state, _, err = st.UsagePricingHourlyRows(ctx, filter)
	if err != nil || state.CoverageEventID != 4 {
		t.Fatalf("rows after full catch-up: state=%#v err=%v", state, err)
	}
	for _, row := range rows {
		if row.Model == "mystery" && (row.UnpricedCalls != 1 || row.CostCNYNanos != 0) {
			t.Fatalf("stored unpriced row = %#v", row)
		}
	}
}
