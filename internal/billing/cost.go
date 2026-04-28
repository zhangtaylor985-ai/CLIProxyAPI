package billing

import (
	"math"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/policy"
)

const tokensPerMillion = int64(1_000_000)

const (
	// Long-context premium starts at the input-token threshold, not the full
	// context window. GPT-5.5 Pro/Codex exposes 400k as 272k input + 128k output.
	fallbackGPT54StandardContextTokens = int64(272_000)
	fallbackGPT55StandardContextTokens = int64(272_000)
)

func usdPer1MToMicroUSDPer1M(v float64) int64 {
	if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	// v USD => v * 1e6 micro-USD
	return int64(math.Round(v * 1_000_000))
}

func USDPer1MToMicroUSDPer1M(v float64) int64 { return usdPer1MToMicroUSDPer1M(v) }

func microUSDPer1MToUSDPer1M(v int64) float64 {
	if v <= 0 {
		return 0
	}
	return float64(v) / 1_000_000
}

func MicroUSDPer1MToUSDPer1M(v int64) float64 { return microUSDPer1MToUSDPer1M(v) }

func microUSDToUSD(v int64) float64 {
	if v == 0 {
		return 0
	}
	return float64(v) / 1_000_000
}

func MicroUSDToUSD(v int64) float64 { return microUSDToUSD(v) }

func costMicroUSD(tokens int64, microUSDPer1M int64) int64 {
	return costMicroUSDRatio(tokens, microUSDPer1M, 1, 1)
}

func costMicroUSDRatio(tokens int64, microUSDPer1M int64, numerator int64, denominator int64) int64 {
	if tokens <= 0 || microUSDPer1M <= 0 {
		return 0
	}
	if numerator <= 0 || denominator <= 0 {
		return 0
	}
	// Round to nearest micro-USD at the end.
	divisor := tokensPerMillion * denominator
	return (tokens*microUSDPer1M*numerator + divisor/2) / divisor
}

func calculateUsageCostMicro(inputTokens, outputTokens, reasoningTokens, cachedTokens int64, price PriceMicroUSDPer1M) int64 {
	promptTokens := inputTokens - cachedTokens
	if promptTokens < 0 {
		promptTokens = 0
	}
	completionTokens := outputTokens + reasoningTokens
	if completionTokens < 0 {
		completionTokens = 0
	}

	cost := int64(0)
	cost += costMicroUSD(promptTokens, price.Prompt)
	cost += costMicroUSD(cachedTokens, price.Cached)
	cost += costMicroUSD(completionTokens, price.Completion)
	return cost
}

func calculateUsageCostMicroForModel(model string, inputTokens, outputTokens, reasoningTokens, cachedTokens int64, price PriceMicroUSDPer1M) int64 {
	cost := calculateUsageCostMicro(inputTokens, outputTokens, reasoningTokens, cachedTokens, price)
	threshold := longContextPremiumInputThresholdTokens(model)
	if threshold <= 0 || inputTokens <= threshold {
		return cost
	}
	promptTokens := inputTokens - cachedTokens
	if promptTokens < 0 {
		promptTokens = 0
	}
	completionTokens := outputTokens + reasoningTokens
	if completionTokens < 0 {
		completionTokens = 0
	}

	// OpenAI long-context pricing applies to the full session once the prompt
	// crosses the input threshold: 2x input and 1.5x output.
	cost += costMicroUSD(promptTokens, price.Prompt)
	cost += costMicroUSD(cachedTokens, price.Cached)
	cost += costMicroUSDRatio(completionTokens, price.Completion, 1, 2)
	return cost
}

func CalculateUsageCostMicro(inputTokens, outputTokens, reasoningTokens, cachedTokens int64, price PriceMicroUSDPer1M) int64 {
	return calculateUsageCostMicro(inputTokens, outputTokens, reasoningTokens, cachedTokens, price)
}

func longContextPremiumInputThresholdTokens(model string) int64 {
	key := policy.StripThinkingVariant(policy.NormaliseModelKey(model))
	switch key {
	case policy.ClaudeGPTTargetFamilyGPT54:
		return fallbackGPT54StandardContextTokens
	case policy.ClaudeGPTTargetFamilyGPT55:
		return fallbackGPT55StandardContextTokens
	default:
		return 0
	}
}
