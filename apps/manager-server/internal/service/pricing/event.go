package pricing

import (
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usageidentity"
)

// Price sources recorded on every event cost row.
const (
	PriceSourceProvider = "provider"
	PriceSourceDefault  = "default"
	PriceSourceNone     = "none"
)

// EventInput is the subset of a usage event needed to price it once.
type EventInput struct {
	TimestampMS          int64
	Provider             string
	AuthProviderSnapshot string
	Model                string
	RequestedModel       string
	ResolvedModel        string
	ServiceTier          string
	InputTokens          int64
	// NormalizedTotalInputTokens is the persisted normalized input when the
	// cache accounting migration has filled it; nil falls back to InputTokens
	// exactly like the rollup SQL does.
	NormalizedTotalInputTokens *int64
	OutputTokens               int64
	CachedTokens               int64
	CacheTokens                int64
	CacheReadTokens            int64
	CacheCreationTokens        int64
}

// EventCost is the priced outcome for one event.
type EventCost struct {
	Provider     string
	PricingModel string
	PriceSource  string
	PriceID      int64
	WindowID     int64
	Multiplier   float64
	CostCNYNanos int64
	CostUSDNanos int64
}

type providerEntry struct {
	price    model.ProviderModelPrice
	flat     model.ModelPrice
	location *time.Location
}

// Book resolves per-event prices. Provider rules (CNY) win over the default
// model price book (USD); an event is priced by exactly one of them.
type Book struct {
	defaults  map[string]model.ModelPrice
	providers map[string]map[string]providerEntry
}

// NewBook indexes the default book and provider rules for repeated lookups.
// Provider rules whose timezone fails to load fall back to UTC rather than
// dropping the rule, because the rule already passed validation on save.
func NewBook(defaults map[string]model.ModelPrice, providerPrices []model.ProviderModelPrice) *Book {
	book := &Book{
		defaults:  defaults,
		providers: map[string]map[string]providerEntry{},
	}
	if book.defaults == nil {
		book.defaults = map[string]model.ModelPrice{}
	}
	for _, price := range providerPrices {
		provider := usageidentity.ProviderKey(price.Provider, "")
		if provider == "" || price.Model == "" {
			continue
		}
		location, err := time.LoadLocation(price.Timezone)
		if err != nil || location == nil {
			location = time.UTC
		}
		entries := book.providers[provider]
		if entries == nil {
			entries = map[string]providerEntry{}
			book.providers[provider] = entries
		}
		entries[price.Model] = providerEntry{price: price, flat: price.FlatPrice(), location: location}
	}
	return book
}

// HasProviderRules reports whether any provider rule is loaded.
func (b *Book) HasProviderRules() bool {
	return b != nil && len(b.providers) > 0
}

// PriceEvent prices one event. Model candidates follow the rollup SQL: the
// resolved (billing) model, then the analytics model, then the requested
// display model. Provider rules are matched first; otherwise the default book
// prices the event with the same context band and service tier rules the
// aggregate readers used to apply.
func (b *Book) PriceEvent(event EventInput) EventCost {
	requested := usageidentity.EffectiveRequestedModel(event.Model, event.RequestedModel)
	analytics := usageidentity.AnalyticsModel(requested)
	billing := event.ResolvedModel
	if billing == "" {
		billing = analytics
	}
	provider := usageidentity.ProviderKey(event.Provider, event.AuthProviderSnapshot)
	normalizedInput := event.InputTokens
	if event.NormalizedTotalInputTokens != nil {
		normalizedInput = *event.NormalizedTotalInputTokens
	}
	compatibleCached := usage.CompatibleCachedTokens(event.CachedTokens, event.CacheTokens, event.CacheReadTokens, event.CacheCreationTokens)
	tokens := ModelTokens{
		InputTokens:         normalizedInput,
		OutputTokens:        event.OutputTokens,
		CachedTokens:        compatibleCached,
		CacheReadTokens:     event.CacheReadTokens,
		CacheCreationTokens: event.CacheCreationTokens,
	}
	candidates := uniqueCandidates(billing, analytics, requested)

	if entries := b.providers[provider]; len(entries) > 0 {
		for _, candidate := range candidates {
			entry, ok := entries[candidate]
			if !ok {
				continue
			}
			multiplier := 1.0
			var windowID int64
			if window, matched := entry.price.MatchWindow(model.LocalMinute(event.TimestampMS, entry.location)); matched {
				multiplier = window.Multiplier
				windowID = window.ID
			}
			cost := CostForFlatPrice(tokens, entry.flat) * multiplier
			return EventCost{
				Provider:     provider,
				PricingModel: candidate,
				PriceSource:  PriceSourceProvider,
				PriceID:      entry.price.ID,
				WindowID:     windowID,
				Multiplier:   multiplier,
				CostCNYNanos: usage.CostNanos(cost),
			}
		}
	}

	pricingModel := billing
	for _, candidate := range candidates {
		if _, ok := b.defaults[candidate]; ok {
			pricingModel = candidate
			break
		}
	}
	tokens.PricingModel = pricingModel
	tokens.ContextThresholdTokens = model.ModelPriceBaseContextThreshold
	if price, ok := b.defaults[pricingModel]; ok {
		_, tokens.ContextThresholdTokens = model.ModelPriceForContext(price, normalizedInput)
	}
	if usage.IsLongContextInput(normalizedInput) {
		tokens.LongInputTokens = tokens.InputTokens
		tokens.LongOutputTokens = tokens.OutputTokens
		tokens.LongCachedTokens = tokens.CachedTokens
		tokens.LongCacheReadTokens = tokens.CacheReadTokens
		tokens.LongCacheCreationTokens = tokens.CacheCreationTokens
	}
	cost := CostForModelCandidatesWithServiceTier([]string{billing, analytics}, event.ServiceTier, tokens, b.defaults)
	result := EventCost{
		Provider:     provider,
		PricingModel: pricingModel,
		PriceSource:  PriceSourceNone,
		Multiplier:   1,
		CostUSDNanos: usage.CostNanos(cost),
	}
	if _, ok := b.defaults[pricingModel]; ok || cost > 0 {
		result.PriceSource = PriceSourceDefault
	}
	return result
}

func uniqueCandidates(values ...string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}
