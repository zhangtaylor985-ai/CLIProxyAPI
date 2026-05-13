package billing

import "testing"

func TestCalculateUsageCostMicroForModel_AppliesGPT54LongContextSessionPricing(t *testing.T) {
	price := PriceMicroUSDPer1M{Prompt: 1_000_000, Completion: 10_000_000, Cached: 100_000}

	got := calculateUsageCostMicroForModel("gpt-5.4", 273_000, 10, 0, 0, price)
	want := int64(546_150) // Full session: 2x input + 1.5x output.

	if got != want {
		t.Fatalf("cost = %d, want %d", got, want)
	}
}

func TestCalculateUsageCostMicroForModel_DoublesGPT55InputAboveLongContextThreshold(t *testing.T) {
	price := PriceMicroUSDPer1M{Prompt: 2_000_000}

	got := calculateUsageCostMicroForModel("gpt-5.5(high)", 272_500, 0, 0, 0, price)
	want := int64(1_090_000) // Full input is billed at 2x once the long-context threshold is crossed.

	if got != want {
		t.Fatalf("cost = %d, want %d", got, want)
	}
}

func TestCalculateUsageCostMicroForModel_DoesNotSurchargeAtOrBelowWindow(t *testing.T) {
	price := PriceMicroUSDPer1M{Prompt: 1_000_000}

	got := calculateUsageCostMicroForModel("gpt-5.4", 272_000, 0, 0, 0, price)
	want := int64(272_000)

	if got != want {
		t.Fatalf("cost = %d, want %d", got, want)
	}
}

func TestCalculateUsageCostMicroForModel_DoesNotSurchargeOtherModels(t *testing.T) {
	price := PriceMicroUSDPer1M{Prompt: 1_000_000}

	got := calculateUsageCostMicroForModel("gpt-5.2", 800_000, 0, 0, 0, price)
	want := int64(800_000)

	if got != want {
		t.Fatalf("cost = %d, want %d", got, want)
	}
}

func TestCalculateUsageCostMicroForModel_LongContextDoublesCachedInputAndAddsHalfOutput(t *testing.T) {
	price := PriceMicroUSDPer1M{Prompt: 1_000_000, Completion: 10_000_000, Cached: 100_000}

	got := calculateUsageCostMicroForModel("gpt-5.5", 300_000, 1_000, 1_000, 50_000, price)
	want := int64(540_000) // Normal 275k + extra input 255k + extra half output 10k.

	if got != want {
		t.Fatalf("cost = %d, want %d", got, want)
	}
}

func TestLongContextPremiumInputThresholdTokens_UsesInputThreshold(t *testing.T) {
	if got := longContextPremiumInputThresholdTokens("gpt-5.4"); got != 272_000 {
		t.Fatalf("gpt-5.4 window = %d, want 272000", got)
	}
	if got := longContextPremiumInputThresholdTokens("gpt-5.5"); got != 272_000 {
		t.Fatalf("gpt-5.5 window = %d, want 272000", got)
	}
}

func TestResolveDefaultPrice_CoversCodexAliasesUsedByWorkers(t *testing.T) {
	for _, model := range []string{"codex-auto-review", "gpt-5.4-mini"} {
		price, ok := ResolveDefaultPrice(model)
		if !ok {
			t.Fatalf("expected default price for %s", model)
		}
		if price.Prompt == 0 || price.Completion == 0 || price.Cached == 0 {
			t.Fatalf("incomplete default price for %s: %+v", model, price)
		}
	}
}
