package billing

import "github.com/router-for-me/CLIProxyAPI/v6/internal/policy"

// DefaultPrices provides a built-in fallback table when no saved override exists.
// Keys are normalised via policy.NormaliseModelKey.
var DefaultPrices = map[string]PriceMicroUSDPer1M{
	// Anthropic
	policy.NormaliseModelKey("claude-haiku-4-5"): {
		Prompt:     1_000_000, // $1.00 / 1M
		Completion: 5_000_000, // $5.00 / 1M
		Cached:     100_000,   // $0.10 / 1M
	},
	policy.NormaliseModelKey("claude-haiku-4-5-20251001"): {
		Prompt:     1_000_000, // $1.00 / 1M
		Completion: 5_000_000, // $5.00 / 1M
		Cached:     100_000,   // $0.10 / 1M
	},
	policy.NormaliseModelKey("claude-sonnet-4-5"): {
		Prompt:     3_000_000,  // $3.00 / 1M
		Completion: 15_000_000, // $15.00 / 1M
		Cached:     300_000,    // $0.30 / 1M
	},
	policy.NormaliseModelKey("claude-sonnet-4-5-20250929"): {
		Prompt:     3_000_000,  // $3.00 / 1M
		Completion: 15_000_000, // $15.00 / 1M
		Cached:     300_000,    // $0.30 / 1M
	},
	policy.NormaliseModelKey("claude-sonnet-4-6"): {
		Prompt:     3_000_000,  // $3.00 / 1M
		Completion: 15_000_000, // $15.00 / 1M
		Cached:     300_000,    // $0.30 / 1M
	},
	policy.NormaliseModelKey("claude-opus-4-6"): {
		Prompt:     5_000_000,  // $5.00 / 1M
		Completion: 25_000_000, // $25.00 / 1M
		Cached:     500_000,    // $0.50 / 1M
	},
	policy.NormaliseModelKey("claude-opus-4-5-20251101"): {
		Prompt:     5_000_000,  // $5.00 / 1M
		Completion: 25_000_000, // $25.00 / 1M
		Cached:     500_000,    // $0.50 / 1M
	},
	// OpenAI / Codex
	policy.NormaliseModelKey("gpt-5.2"): {
		Prompt:     1_750_000,  // $1.75 / 1M
		Completion: 14_000_000, // $14.00 / 1M
		Cached:     175_000,    // $0.175 / 1M
	},
	policy.NormaliseModelKey("gpt-5.3-codex"): {
		Prompt:     1_750_000,  // $1.75 / 1M
		Completion: 14_000_000, // $14.00 / 1M
		Cached:     175_000,    // $0.175 / 1M
	},
	policy.NormaliseModelKey("gpt-5.4"): {
		Prompt:     2_500_000,  // $2.50 / 1M
		Completion: 15_000_000, // $15.00 / 1M
		Cached:     250_000,    // $0.25 / 1M
	},
}

// ResolveDefaultPrice returns the built-in fallback price for a model,
// including support for "-thinking" variants mapping to their base model.
func ResolveDefaultPrice(model string) (PriceMicroUSDPer1M, bool) {
	modelKey := policy.NormaliseModelKey(model)
	if modelKey == "" {
		return PriceMicroUSDPer1M{}, false
	}
	if price, ok := DefaultPrices[modelKey]; ok {
		return price, true
	}
	baseKey := policy.StripThinkingVariant(modelKey)
	if baseKey != "" && baseKey != modelKey {
		price, ok := DefaultPrices[baseKey]
		return price, ok
	}
	return PriceMicroUSDPer1M{}, false
}
