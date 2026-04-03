package apikeyconfig

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestReconcileStatesMergesYAMLIntoExistingAndPreservesTokenPackage(t *testing.T) {
	existing := State{
		APIKeys: []string{"pg-only", "shared"},
		APIKeyPolicies: []config.APIKeyPolicy{
			{APIKey: "pg-only", DailyBudgetUSD: 10},
			{APIKey: "shared", DailyBudgetUSD: 20, WeeklyBudgetUSD: 80},
		},
	}
	fromYAML := State{
		APIKeys: []string{"shared", "yaml-only"},
		APIKeyPolicies: []config.APIKeyPolicy{
			{
				APIKey:                "shared",
				DailyBudgetUSD:        150,
				WeeklyBudgetUSD:       500,
				TokenPackageUSD:       30,
				TokenPackageStartedAt: "2026-03-29T13:33:00+08:00",
			},
			{APIKey: "yaml-only", DailyBudgetUSD: 60, WeeklyBudgetUSD: 225},
		},
	}

	merged, stats := ReconcileStates(existing, fromYAML, ReconcileOptions{AssignBudgetGroups: true})

	if len(merged.APIKeys) != 3 {
		t.Fatalf("merged api keys len = %d, want 3", len(merged.APIKeys))
	}
	if merged.APIKeys[0] != "shared" || merged.APIKeys[1] != "yaml-only" || merged.APIKeys[2] != "pg-only" {
		t.Fatalf("merged api keys = %#v", merged.APIKeys)
	}
	if len(merged.APIKeyPolicies) != 3 {
		t.Fatalf("merged policies len = %d, want 3", len(merged.APIKeyPolicies))
	}

	shared := findPolicy(merged.APIKeyPolicies, "shared")
	if shared == nil {
		t.Fatal("shared policy missing")
	}
	if shared.GroupID != "double" {
		t.Fatalf("shared group = %q, want double", shared.GroupID)
	}
	if shared.DailyBudgetUSD != 150 || shared.WeeklyBudgetUSD != 500 {
		t.Fatalf("shared budgets = %v/%v, want 150/500", shared.DailyBudgetUSD, shared.WeeklyBudgetUSD)
	}
	if shared.TokenPackageUSD != 30 || shared.TokenPackageStartedAt != "2026-03-29T13:33:00+08:00" {
		t.Fatalf("shared token package = %v/%q", shared.TokenPackageUSD, shared.TokenPackageStartedAt)
	}

	yamlOnly := findPolicy(merged.APIKeyPolicies, "yaml-only")
	if yamlOnly == nil {
		t.Fatal("yaml-only policy missing")
	}
	if yamlOnly.GroupID != "quad" {
		t.Fatalf("yaml-only group = %q, want quad", yamlOnly.GroupID)
	}
	if yamlOnly.DailyBudgetUSD != 60 || yamlOnly.WeeklyBudgetUSD != 250 {
		t.Fatalf("yaml-only budgets = %v/%v, want 60/250", yamlOnly.DailyBudgetUSD, yamlOnly.WeeklyBudgetUSD)
	}

	pgOnly := findPolicy(merged.APIKeyPolicies, "pg-only")
	if pgOnly == nil {
		t.Fatal("pg-only policy missing")
	}
	if pgOnly.DailyBudgetUSD != 10 {
		t.Fatalf("pg-only daily budget = %v, want 10", pgOnly.DailyBudgetUSD)
	}

	if stats.TokenPackagePolicyCount != 1 {
		t.Fatalf("token package count = %d, want 1", stats.TokenPackagePolicyCount)
	}
	if stats.DoubleGroupPolicyCount != 1 || stats.QuadGroupPolicyCount != 1 {
		t.Fatalf("group stats = %+v", stats)
	}
	if stats.PreservedPGOnlyPolicyCount != 1 {
		t.Fatalf("preserved pg-only policy count = %d, want 1", stats.PreservedPGOnlyPolicyCount)
	}
}

func TestReconcileStatesKeepsCustomGroupAndSkipsDistantBudgets(t *testing.T) {
	fromYAML := State{
		APIKeyPolicies: []config.APIKeyPolicy{
			{APIKey: "custom-group", GroupID: "team-alpha", DailyBudgetUSD: 150, WeeklyBudgetUSD: 500},
			{APIKey: "distant", DailyBudgetUSD: 60, WeeklyBudgetUSD: 365},
			{APIKey: "triple-near", DailyBudgetUSD: 110, WeeklyBudgetUSD: 300},
		},
	}

	merged, _ := ReconcileStates(State{}, fromYAML, ReconcileOptions{AssignBudgetGroups: true})

	custom := findPolicy(merged.APIKeyPolicies, "custom-group")
	if custom == nil || custom.GroupID != "team-alpha" {
		t.Fatalf("custom-group = %+v, want team-alpha preserved", custom)
	}

	distant := findPolicy(merged.APIKeyPolicies, "distant")
	if distant == nil {
		t.Fatal("distant policy missing")
	}
	if distant.GroupID != "" {
		t.Fatalf("distant group = %q, want empty", distant.GroupID)
	}

	tripleNear := findPolicy(merged.APIKeyPolicies, "triple-near")
	if tripleNear == nil {
		t.Fatal("triple-near policy missing")
	}
	if tripleNear.GroupID != "triple" {
		t.Fatalf("triple-near group = %q, want triple", tripleNear.GroupID)
	}
}

func findPolicy(policies []config.APIKeyPolicy, apiKey string) *config.APIKeyPolicy {
	for i := range policies {
		if policies[i].APIKey == apiKey {
			return &policies[i]
		}
	}
	return nil
}
