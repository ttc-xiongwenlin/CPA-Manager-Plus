package pricing

import (
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

func deepseekRule() model.ProviderModelPrice {
	return model.ProviderModelPrice{
		ID:                  7,
		Provider:            "openai-compatible-deepseek",
		Model:               "deepseek-flash",
		Prompt:              2,
		Completion:          8,
		CacheRead:           0.2,
		CacheReadConfigured: true,
		Timezone:            "Asia/Shanghai",
		Windows: []model.ProviderPriceWindow{
			{ID: 3, StartMinute: 30, EndMinute: 510, Multiplier: 0.5, Label: "off-peak"},
		},
	}
}

func deepseekEvent(timestamp time.Time) EventInput {
	normalized := int64(1_000_000)
	return EventInput{
		TimestampMS:                timestamp.UnixMilli(),
		Provider:                   "openai-compatible-deepseek",
		Model:                      "deepseek-flash",
		RequestedModel:             "deepseek-flash",
		ResolvedModel:              "deepseek-flash",
		ServiceTier:                "auto",
		InputTokens:                1_000_000,
		NormalizedTotalInputTokens: &normalized,
		OutputTokens:               100_000,
		CachedTokens:               500_000,
		CacheReadTokens:            500_000,
	}
}

func TestPriceEventAppliesProviderRuleAndWindow(t *testing.T) {
	book := NewBook(map[string]model.ModelPrice{
		"deepseek-flash": {Prompt: 100, Completion: 100, PromptConfigured: true, CompletionConfigured: true},
	}, []model.ProviderModelPrice{deepseekRule()})

	// 02:00 Shanghai falls inside the 00:30-08:30 off-peak window.
	offPeak := book.PriceEvent(deepseekEvent(time.Date(2026, 9, 19, 18, 0, 0, 0, time.UTC)))
	if offPeak.PriceSource != PriceSourceProvider || offPeak.Provider != "openai-compatible-deepseek" || offPeak.PricingModel != "deepseek-flash" {
		t.Fatalf("provider rule should win over the default book: %#v", offPeak)
	}
	// prompt 500k*2 + cache read 500k*0.2 + output 100k*8 = 1.9 CNY, halved.
	if offPeak.CostCNYNanos != 950_000_000 || offPeak.CostUSDNanos != 0 {
		t.Fatalf("off-peak cost = %#v, want 0.95 CNY", offPeak)
	}
	if offPeak.PriceID != 7 || offPeak.WindowID != 3 || offPeak.Multiplier != 0.5 {
		t.Fatalf("audit fields = %#v", offPeak)
	}

	// 12:00 Shanghai is outside the window.
	peak := book.PriceEvent(deepseekEvent(time.Date(2026, 9, 20, 4, 0, 0, 0, time.UTC)))
	if peak.CostCNYNanos != 1_900_000_000 || peak.WindowID != 0 || peak.Multiplier != 1 {
		t.Fatalf("peak cost = %#v, want 1.9 CNY without window", peak)
	}
}

func TestPriceEventProviderRuleMatchesResolvedModelFirst(t *testing.T) {
	rule := deepseekRule()
	rule.Model = "deepseek-v4-pro"
	book := NewBook(nil, []model.ProviderModelPrice{rule})
	event := deepseekEvent(time.Date(2026, 9, 20, 4, 0, 0, 0, time.UTC))
	event.Model = "deepseek-flash"
	event.RequestedModel = "deepseek-flash"
	event.ResolvedModel = "deepseek-v4-pro"
	cost := book.PriceEvent(event)
	if cost.PriceSource != PriceSourceProvider || cost.PricingModel != "deepseek-v4-pro" {
		t.Fatalf("resolved model should be the first candidate: %#v", cost)
	}
	// Provider rules ignore service tier multipliers entirely.
	event.ServiceTier = "priority"
	if priority := book.PriceEvent(event); priority.CostCNYNanos != cost.CostCNYNanos {
		t.Fatalf("service tier must not change provider pricing: %d vs %d", priority.CostCNYNanos, cost.CostCNYNanos)
	}
}

func TestPriceEventFallsBackToDefaultBookInUSD(t *testing.T) {
	book := NewBook(map[string]model.ModelPrice{
		"gpt-x": {Prompt: 1, Completion: 2, PromptConfigured: true, CompletionConfigured: true},
	}, []model.ProviderModelPrice{deepseekRule()})
	cost := book.PriceEvent(EventInput{
		TimestampMS:    time.Now().UnixMilli(),
		Provider:       "codex",
		Model:          "gpt-x",
		RequestedModel: "gpt-x",
		InputTokens:    1_000_000,
		OutputTokens:   1_000_000,
	})
	if cost.PriceSource != PriceSourceDefault || cost.Provider != "codex" || cost.PricingModel != "gpt-x" {
		t.Fatalf("default book should price codex usage: %#v", cost)
	}
	if cost.CostUSDNanos != 3_000_000_000 || cost.CostCNYNanos != 0 {
		t.Fatalf("default cost = %#v, want 3 USD", cost)
	}
}

func TestPriceEventWithoutAnyRuleIsUnpriced(t *testing.T) {
	book := NewBook(nil, nil)
	cost := book.PriceEvent(EventInput{Provider: "unknown", Model: "mystery", InputTokens: 10})
	if cost.PriceSource != PriceSourceNone || cost.CostCNYNanos != 0 || cost.CostUSDNanos != 0 {
		t.Fatalf("unpriced event = %#v", cost)
	}
	if cost.PricingModel != "mystery" || cost.Multiplier != 1 {
		t.Fatalf("unpriced audit fields = %#v", cost)
	}
}

func TestPriceEventDefaultBookAppliesContextTiers(t *testing.T) {
	book := NewBook(map[string]model.ModelPrice{
		"tiered": {
			Prompt: 1, PromptConfigured: true,
			ContextTiers: []model.ModelPriceContextTier{{ThresholdTokens: 100, Prompt: 3, PromptConfigured: true}},
		},
	}, nil)
	small := book.PriceEvent(EventInput{Model: "tiered", InputTokens: 100})
	large := book.PriceEvent(EventInput{Model: "tiered", InputTokens: 101})
	if small.CostUSDNanos != 100_000 || large.CostUSDNanos != 303_000 {
		t.Fatalf("tier pricing = %d / %d, want 100000 / 303000 nanos", small.CostUSDNanos, large.CostUSDNanos)
	}
}

func TestPriceEventAppliesWeekdayOnlyWindows(t *testing.T) {
	rule := model.ProviderModelPrice{
		ID: 9, Provider: "openai-compatible-deepseek", Model: "deepseek-flash",
		Prompt: 1, Completion: 4, CacheRead: 0.02, CacheReadConfigured: true, Timezone: "Asia/Shanghai",
		Windows: []model.ProviderPriceWindow{
			{ID: 1, StartMinute: 540, EndMinute: 720, Multiplier: 2, Weekdays: []int{1, 2, 3, 4, 5}},
			{ID: 2, StartMinute: 840, EndMinute: 1080, Multiplier: 2, Weekdays: []int{1, 2, 3, 4, 5}},
		},
		OffDays: []string{"2026-10-01"},
	}
	book := NewBook(nil, []model.ProviderModelPrice{rule})
	event := deepseekEvent(time.Date(2026, 9, 21, 2, 0, 0, 0, time.UTC)) // Monday 10:00 Shanghai
	peak := book.PriceEvent(event)
	// 500k*1 + 500k*0.02 + 100k*4 = 0.91 CNY doubled during peak.
	if peak.CostCNYNanos != 1_820_000_000 || peak.WindowID != 1 || peak.Multiplier != 2 {
		t.Fatalf("weekday peak = %#v", peak)
	}
	weekend := book.PriceEvent(deepseekEvent(time.Date(2026, 9, 26, 2, 0, 0, 0, time.UTC))) // Saturday 10:00
	if weekend.CostCNYNanos != 910_000_000 || weekend.WindowID != 0 {
		t.Fatalf("weekend should stay off-peak = %#v", weekend)
	}
	holiday := book.PriceEvent(deepseekEvent(time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC))) // Thursday 10:00, off day
	if holiday.CostCNYNanos != 910_000_000 || holiday.WindowID != 0 {
		t.Fatalf("off day should stay off-peak = %#v", holiday)
	}
}
