package usageidentity

import "strings"

// ProviderKey returns the normalized provider identity shared by account keys,
// provider price rules, and event cost rows. The auth provider snapshot wins
// over the raw executor provider, matching SQLAccountKeyExpression.
func ProviderKey(provider, authProviderSnapshot string) string {
	if snapshot := strings.TrimSpace(authProviderSnapshot); snapshot != "" {
		return normalizeProvider(snapshot)
	}
	return normalizeProvider(provider)
}
