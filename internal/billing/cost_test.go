package billing

import "testing"

func TestCalculateUsageCostMicroForModel_DoublesGPT54InputAboveRegistryWindow(t *testing.T) {
	price := PriceMicroUSDPer1M{Prompt: 1_000_000, Completion: 10_000_000, Cached: 100_000}

	got := calculateUsageCostMicroForModel("gpt-5.4", 273_000, 10, 0, 0, price)
	want := int64(274_100) // 273k normal prompt + 1k long-context surcharge + 10 completion * $10/1M.

	if got != want {
		t.Fatalf("cost = %d, want %d", got, want)
	}
}

func TestCalculateUsageCostMicroForModel_UsesGPT55RegistryWindow(t *testing.T) {
	price := PriceMicroUSDPer1M{Prompt: 2_000_000}

	got := calculateUsageCostMicroForModel("gpt-5.5(high)", 400_500, 0, 0, 0, price)
	want := int64(802_000) // 400.5k normal prompt + 0.5k long-context surcharge at $2/1M.

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

func TestLongContextPremiumStandardWindowTokens_ComesFromRegistry(t *testing.T) {
	if got := longContextPremiumStandardWindowTokens("gpt-5.4"); got != 272_000 {
		t.Fatalf("gpt-5.4 window = %d, want 272000", got)
	}
	if got := longContextPremiumStandardWindowTokens("gpt-5.5"); got != 400_000 {
		t.Fatalf("gpt-5.5 window = %d, want 400000", got)
	}
}
