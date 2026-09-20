package pricing

import "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"

// CostForFlatPrice prices tokens with a plain five-rate price book entry. It
// applies no model-name behavior (long-context premiums, priority multipliers,
// GPT fallbacks): provider price rules are explicit and complete on their own.
func CostForFlatPrice(tokens ModelTokens, price model.ModelPrice) float64 {
	return costForSegment(
		maxInt64(tokens.InputTokens, 0),
		maxInt64(tokens.OutputTokens, 0),
		maxInt64(tokens.CachedTokens, 0),
		maxInt64(tokens.CacheReadTokens, 0),
		maxInt64(tokens.CacheCreationTokens, 0),
		price,
		1,
		1,
	)
}
