package usageevent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/modelprice"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/providerprice"
	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/usageeventcost"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

func newCostTestRepo(t *testing.T) *repository {
	t.Helper()
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &repository{db: db}
}

// costTestEvent shares one credential, account and API key across every event
// so each per-model row lands in the same identity group.
func costTestEvent(hash string, ts time.Time, provider, modelID string, inputTokens, outputTokens int64) usage.Event {
	return usage.Event{
		EventHash:        hash,
		TimestampMS:      ts.UnixMilli(),
		Timestamp:        ts.Format(time.RFC3339Nano),
		Provider:         provider,
		Model:            modelID,
		RequestedModel:   modelID,
		ResolvedModel:    modelID,
		APIKeyHash:       "key-a",
		AccountSnapshot:  "team-a",
		AuthFileSnapshot: "team-a.json",
		AuthIndex:        "auth-team-a",
		Source:           "team-a.json",
		SourceHash:       "hash-a",
		InputTokens:      inputTokens,
		OutputTokens:     outputTokens,
		TotalTokens:      inputTokens + outputTokens,
		CreatedAtMS:      ts.UnixMilli(),
	}
}

func totalCost[T any](rows []T, cost func(T) usage.CostTotals) usage.CostTotals {
	var total usage.CostTotals
	for _, row := range rows {
		total.AddCost(cost(row))
	}
	return total
}

func TestReadersSumStoredEventCost(t *testing.T) {
	ctx := context.Background()
	repo := newCostTestRepo(t)
	if _, err := providerprice.New(repo.db).ReplaceAll(ctx, []model.ProviderModelPrice{{
		Provider: "openai-compatible-deepseek", Model: "deepseek-flash", Prompt: 2, Completion: 8,
	}}); err != nil {
		t.Fatalf("save provider prices: %v", err)
	}
	if err := modelprice.New(repo.db).ReplaceAll(ctx, map[string]model.ModelPrice{
		"gpt-x": {Prompt: 1, Completion: 2, PromptConfigured: true, CompletionConfigured: true},
	}); err != nil {
		t.Fatalf("save model prices: %v", err)
	}
	base := time.Date(2026, time.September, 20, 10, 0, 0, 0, time.UTC)
	if _, err := repo.InsertBatch(ctx, []usage.Event{
		// 1.0M*2 + 100k*8 = 2.8 CNY from the provider rule.
		costTestEvent("deepseek-1", base.Add(1*time.Minute), "openai-compatible-deepseek", "deepseek-flash", 1_000_000, 100_000),
		// 1.0M*1 + 1.0M*2 = 3 USD from the default book.
		costTestEvent("codex-1", base.Add(2*time.Minute), "codex", "gpt-x", 1_000_000, 1_000_000),
		costTestEvent("unknown-1", base.Add(3*time.Minute), "unknown", "mystery", 10, 10),
	}); err != nil {
		t.Fatalf("insert events: %v", err)
	}
	if _, err := usageeventcost.New(repo.db).CatchUp(ctx, 100, base.Add(time.Hour).UnixMilli()); err != nil {
		t.Fatalf("cost catch-up: %v", err)
	}

	want := usage.CostTotals{CostCNYNanos: 2_800_000_000, CostUSDNanos: 3_000_000_000, UnpricedCalls: 1}
	check := func(name string, got usage.CostTotals, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != want {
			t.Errorf("%s cost = %#v, want %#v", name, got, want)
		}
	}
	fromMS, toMS := base.UnixMilli(), base.Add(time.Hour).UnixMilli()
	filter := AnalyticsFilter{FromMS: fromMS, ToMS: toMS, IncludeFailed: true}
	keyFilter := filter
	keyFilter.APIKeyHashes = []string{"key-a"}
	modelCost := func(s ModelStat) usage.CostTotals { return s.CostTotals }

	topModels, err := repo.TopModelsBetween(ctx, fromMS, toMS, 10)
	check("TopModelsBetween", totalCost(topModels, modelCost), err)
	modelStats, err := repo.ModelStatsBetween(ctx, fromMS, toMS)
	check("ModelStatsBetween", totalCost(modelStats, modelCost), err)
	for _, limit := range []int{0, 5} {
		stats, err := repo.ModelStatsWithFilter(ctx, filter, limit)
		check(fmt.Sprintf("ModelStatsWithFilter(limit=%d)", limit), totalCost(stats, modelCost), err)
	}
	timeline, err := repo.TimelineWithFilter(ctx, filter, "hour", time.UTC)
	check("TimelineWithFilter", totalCost(timeline, func(p TimelinePoint) usage.CostTotals { return p.CostTotals }), err)
	apiKeyTimeline, err := repo.APIKeyTimelineWithFilter(ctx, keyFilter, "hour", time.UTC)
	check("APIKeyTimelineWithFilter", totalCost(apiKeyTimeline, func(p APIKeyTimelinePoint) usage.CostTotals { return p.CostTotals }), err)
	heatmap, err := repo.HeatmapWithFilter(ctx, filter, time.UTC)
	check("HeatmapWithFilter", totalCost(heatmap, func(p HeatmapPoint) usage.CostTotals { return p.CostTotals }), err)
	channel, err := repo.ChannelModelStatsWithFilter(ctx, filter)
	check("ChannelModelStatsWithFilter", totalCost(channel, func(s ChannelModelStat) usage.CostTotals { return s.CostTotals }), err)
	account, err := repo.AccountModelStatsWithFilter(ctx, filter)
	check("AccountModelStatsWithFilter", totalCost(account, func(s AccountModelStat) usage.CostTotals { return s.CostTotals }), err)
	windows, err := repo.AccountWindowModelStats(ctx, []AccountWindowUsageQuery{{
		FromMS: fromMS, ToMS: toMS, AuthFileSnapshot: "team-a.json", AuthIndex: "auth-team-a", Source: "team-a.json", AccountSnapshot: "team-a",
	}})
	check("AccountWindowModelStats", totalCost(windows, func(s AccountWindowModelStat) usage.CostTotals { return s.CostTotals }), err)
	credential, err := repo.CredentialModelStatsWithFilter(ctx, filter)
	check("CredentialModelStatsWithFilter", totalCost(credential, func(s CredentialModelStat) usage.CostTotals { return s.CostTotals }), err)
	credentialCost := func(p CredentialTimelinePoint) usage.CostTotals { return p.CostTotals }
	credentialRaw, err := repo.credentialTimelineRawWithFilter(ctx, filter, "hour", time.UTC)
	check("credentialTimelineRawWithFilter", totalCost(credentialRaw, credentialCost), err)
	credentialHourly, err := repo.credentialTimelineHourlyWithFilter(ctx, filter, "hour", time.UTC)
	check("credentialTimelineHourlyWithFilter", totalCost(credentialHourly, credentialCost), err)
	credentialTimeline, err := repo.CredentialTimelineWithFilter(ctx, filter, "hour", time.UTC)
	check("CredentialTimelineWithFilter", totalCost(credentialTimeline, credentialCost), err)
	apiKeyStats, err := repo.APIKeyModelStatsWithFilter(ctx, filter)
	check("APIKeyModelStatsWithFilter", totalCost(apiKeyStats, func(s APIKeyModelStat) usage.CostTotals { return s.CostTotals }), err)

	// Merging two copies of the same edge part must add the cost, not keep the first.
	merged := totalCost(mergeCredentialTimelineParts([][]CredentialTimelinePoint{credentialRaw, credentialRaw}), credentialCost)
	if doubled := (usage.CostTotals{CostCNYNanos: 2 * want.CostCNYNanos, CostUSDNanos: 2 * want.CostUSDNanos, UnpricedCalls: 2 * want.UnpricedCalls}); merged != doubled {
		t.Errorf("mergeCredentialTimelineParts cost = %#v, want %#v", merged, doubled)
	}

	// Cost stays on the row of the model that incurred it.
	byModel := map[string]ModelStat{}
	for _, stat := range modelStats {
		byModel[stat.Model] = stat
	}
	if got := byModel["deepseek-flash"].CostTotals; got != (usage.CostTotals{CostCNYNanos: 2_800_000_000}) {
		t.Errorf("deepseek-flash cost = %#v", got)
	}
	if got := byModel["gpt-x"].CostTotals; got != (usage.CostTotals{CostUSDNanos: 3_000_000_000}) {
		t.Errorf("gpt-x cost = %#v", got)
	}
	if got := byModel["mystery"].CostTotals; got != (usage.CostTotals{UnpricedCalls: 1}) {
		t.Errorf("mystery cost = %#v", got)
	}

	page, err := repo.EventsPageWithFilter(ctx, filter, 0, 0, 10)
	if err != nil {
		t.Fatalf("events page: %v", err)
	}
	byHash := map[string]EventPageItem{}
	for _, item := range page.Items {
		byHash[item.EventHash] = item
	}
	if item := byHash["deepseek-1"]; item.CostCNYNanos != 2_800_000_000 || item.CostUSDNanos != 0 || item.PriceSource != "provider" || item.CostMultiplier != 1 {
		t.Errorf("deepseek-1 page item cost = cny:%d usd:%d source:%q multiplier:%v", item.CostCNYNanos, item.CostUSDNanos, item.PriceSource, item.CostMultiplier)
	}
	if item := byHash["codex-1"]; item.CostUSDNanos != 3_000_000_000 || item.CostCNYNanos != 0 || item.PriceSource != "default" || item.CostMultiplier != 1 {
		t.Errorf("codex-1 page item cost = cny:%d usd:%d source:%q multiplier:%v", item.CostCNYNanos, item.CostUSDNanos, item.PriceSource, item.CostMultiplier)
	}
	if item := byHash["unknown-1"]; item.CostCNYNanos != 0 || item.CostUSDNanos != 0 || item.PriceSource != "none" {
		t.Errorf("unknown-1 page item cost = cny:%d usd:%d source:%q", item.CostCNYNanos, item.CostUSDNanos, item.PriceSource)
	}

	// An event the cost task has not priced yet has no cost row: the page
	// reports an empty source and the aggregates count it as unpriced.
	if _, err := repo.InsertBatch(ctx, []usage.Event{
		costTestEvent("late-1", base.Add(4*time.Minute), "codex", "gpt-x", 10, 10),
	}); err != nil {
		t.Fatalf("insert late event: %v", err)
	}
	page, err = repo.EventsPageWithFilter(ctx, filter, 0, 0, 10)
	if err != nil {
		t.Fatalf("events page after late insert: %v", err)
	}
	var late *EventPageItem
	for index := range page.Items {
		if page.Items[index].EventHash == "late-1" {
			late = &page.Items[index]
		}
	}
	if late == nil {
		t.Fatalf("late event missing from page: %#v", page.Items)
	}
	if late.PriceSource != "" || late.CostCNYNanos != 0 || late.CostUSDNanos != 0 || late.CostMultiplier != 1 {
		t.Errorf("unpriced page item cost = cny:%d usd:%d source:%q multiplier:%v", late.CostCNYNanos, late.CostUSDNanos, late.PriceSource, late.CostMultiplier)
	}
	stats, err := repo.ModelStatsWithFilter(ctx, filter, 0)
	if err != nil {
		t.Fatalf("model stats after late insert: %v", err)
	}
	if got := totalCost(stats, modelCost); got != (usage.CostTotals{CostCNYNanos: want.CostCNYNanos, CostUSDNanos: want.CostUSDNanos, UnpricedCalls: 2}) {
		t.Errorf("model stats cost after late insert = %#v", got)
	}
}

// The cost join must resolve through the cost table's integer primary key
// and leave the usage_events index scans untouched.
func TestCostJoinUsesPrimaryKeyLookup(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliterepo.RunDerivedStartupMaintenance(context.Background(), db); err != nil {
		t.Fatalf("prepare post-listen indexes: %v", err)
	}

	where, args := analyticsWhere(AnalyticsFilter{FromMS: 1_000, ToMS: 2_000, IncludeFailed: true})
	queries := []struct {
		name  string
		query string
		args  []any
	}{
		{"top models", topModelsSQL, []any{int64(1_000), int64(2_000), 5}},
		{"filtered aggregate", pricingBandedUsageEventsCTE + `
select analytics_model_value, ` + usageeventcost.SumSQL + `
from banded_usage_events ` + where + `
group by analytics_model_value`, args},
		{"filtered rows", pricingBandedUsageEventsCTE + `
select timestamp_ms, ` + costRowExpr + `
from banded_usage_events ` + where + `
order by timestamp_ms`, args},
		{"events page", `select id, ` + costRowExpr + `
from usage_events
` + usageeventcost.JoinSQL("usage_events") + `
` + where + `
order by timestamp_ms desc, id desc
limit ?`, append(append([]any{}, args...), 10)},
	}
	for _, tc := range queries {
		rows, err := db.Query(`explain query plan `+tc.query, tc.args...)
		if err != nil {
			t.Fatalf("%s: explain: %v", tc.name, err)
		}
		details := make([]string, 0, 16)
		costLookup := false
		badScan := false
		usageIndexed := false
		for rows.Next() {
			var id, parent, notUsed int
			var detail string
			if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
				rows.Close()
				t.Fatalf("%s: scan query plan: %v", tc.name, err)
			}
			details = append(details, detail)
			costLookup = costLookup || strings.Contains(detail, "SEARCH usage_event_costs_v1 USING INTEGER PRIMARY KEY")
			badScan = badScan || strings.Contains(detail, "SCAN usage_event_costs_v1") || strings.Contains(detail, "SCAN usage_events")
			usageIndexed = usageIndexed || strings.Contains(detail, "SEARCH usage_events USING")
		}
		queryErr := rows.Err()
		rows.Close()
		if queryErr != nil {
			t.Fatalf("%s: query plan rows: %v", tc.name, queryErr)
		}
		if !costLookup || badScan || !usageIndexed {
			t.Fatalf("%s: cost join is not a primary key lookup over the indexed usage_events scan: %v", tc.name, details)
		}
	}
}
