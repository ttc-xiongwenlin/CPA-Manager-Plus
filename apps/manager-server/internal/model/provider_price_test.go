package model

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizeProviderModelPriceNormalizesIdentityAndWindows(t *testing.T) {
	price, err := NormalizeProviderModelPrice(ProviderModelPrice{
		Provider:   " OpenAI_Compatible-DeepSeek ",
		Model:      " deepseek-flash ",
		Prompt:     2,
		Completion: 8,
		CacheRead:  0.2,
		Windows: []ProviderPriceWindow{
			{StartMinute: 990, EndMinute: 1440, Multiplier: 0.5},
			{StartMinute: 30, EndMinute: 510, Multiplier: 0.5},
		},
	})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if price.Provider != "openai-compatible-deepseek" || price.Model != "deepseek-flash" {
		t.Fatalf("identity = %q/%q", price.Provider, price.Model)
	}
	if price.Timezone != DefaultProviderPriceTimezone {
		t.Fatalf("timezone = %q", price.Timezone)
	}
	if price.CacheRead != 0 {
		t.Fatalf("unconfigured cache read should reset, got %v", price.CacheRead)
	}
	if len(price.Windows) != 2 || price.Windows[0].StartMinute != 30 || price.Windows[1].StartMinute != 990 {
		t.Fatalf("windows not sorted: %#v", price.Windows)
	}
	if price.Windows[1].EndMinute != 0 {
		t.Fatalf("end minute 1440 should normalize to 0, got %d", price.Windows[1].EndMinute)
	}
}

func TestNormalizeProviderModelPriceRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name  string
		price ProviderModelPrice
		want  string
	}{
		{"missing provider", ProviderModelPrice{Model: "m", Prompt: 1}, "provider is required"},
		{"missing model", ProviderModelPrice{Provider: "p", Prompt: 1}, "model is required"},
		{"bad timezone", ProviderModelPrice{Provider: "p", Model: "m", Timezone: "Mars/Olympus"}, "invalid timezone"},
		{"negative price", ProviderModelPrice{Provider: "p", Model: "m", Prompt: -1}, "invalid price"},
		{"zero multiplier", ProviderModelPrice{Provider: "p", Model: "m", Windows: []ProviderPriceWindow{{StartMinute: 0, EndMinute: 60, Multiplier: 0}}}, "multiplier"},
		{"empty window", ProviderModelPrice{Provider: "p", Model: "m", Windows: []ProviderPriceWindow{{StartMinute: 60, EndMinute: 60, Multiplier: 1}}}, "empty"},
		{"wrapped overlap", ProviderModelPrice{Provider: "p", Model: "m", Windows: []ProviderPriceWindow{
			{StartMinute: 990, EndMinute: 30, Multiplier: 0.5},
			{StartMinute: 0, EndMinute: 60, Multiplier: 0.8},
		}}, "overlap"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NormalizeProviderModelPrice(tc.price)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
	if _, err := NormalizeProviderModelPrice(ProviderModelPrice{Provider: "p", Model: "m", Windows: []ProviderPriceWindow{
		{StartMinute: 990, EndMinute: 30, Multiplier: 0.5},
		{StartMinute: 30, EndMinute: 510, Multiplier: 0.75},
	}}); err != nil {
		t.Fatalf("adjacent wrapped windows should not overlap: %v", err)
	}
}

func TestProviderPriceWindowContainsWrapsMidnight(t *testing.T) {
	window := ProviderPriceWindow{StartMinute: 990, EndMinute: 30, Multiplier: 0.5}
	for minute, want := range map[int]bool{989: false, 990: true, 1439: true, 0: true, 29: true, 30: false, 600: false} {
		if got := window.Contains(minute); got != want {
			t.Fatalf("Contains(%d) = %v, want %v", minute, got, want)
		}
	}
	plain := ProviderPriceWindow{StartMinute: 30, EndMinute: 510, Multiplier: 0.5}
	if plain.Contains(29) || !plain.Contains(30) || !plain.Contains(509) || plain.Contains(510) {
		t.Fatal("plain window bounds are half-open [start, end)")
	}
}

func TestLocalMinuteUsesLocation(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	// 2026-09-19T18:00:00Z is 02:00 in Shanghai.
	timestamp := time.Date(2026, 9, 19, 18, 0, 0, 0, time.UTC).UnixMilli()
	if got := LocalMinute(timestamp, shanghai); got != 120 {
		t.Fatalf("LocalMinute = %d, want 120", got)
	}
	if got := LocalMinute(timestamp, nil); got != 18*60 {
		t.Fatalf("LocalMinute(nil) = %d, want UTC minute", got)
	}
}

func TestFlatPriceFallsBackToPromptForUnconfiguredCache(t *testing.T) {
	flat := ProviderModelPrice{Prompt: 2, Completion: 8}.FlatPrice()
	if flat.CacheRead != 2 || flat.Cache != 2 || flat.CacheCreation != 2 || !flat.CacheReadConfigured {
		t.Fatalf("unconfigured cache rates should charge prompt: %#v", flat)
	}
	explicit := ProviderModelPrice{Prompt: 2, Completion: 8, CacheRead: 0, CacheReadConfigured: true}.FlatPrice()
	if explicit.CacheRead != 0 || explicit.Cache != 0 || !explicit.CacheReadConfigured {
		t.Fatalf("explicit zero cache read must stay zero: %#v", explicit)
	}
}
