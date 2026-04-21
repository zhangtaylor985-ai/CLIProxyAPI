package config

import (
	"testing"
	"time"
)

func boolPtr(v bool) *bool { return &v }

func TestConfig_SanitizeAPIKeyPolicies_WeeklyBudgetDisabledWhenNonPositive(t *testing.T) {
	cfg := &Config{
		APIKeyPolicies: []APIKeyPolicy{
			{APIKey: "k1", WeeklyBudgetUSD: -1},
			{APIKey: "k2", WeeklyBudgetUSD: 400},
		},
	}

	cfg.SanitizeAPIKeyPolicies()

	if got := cfg.FindAPIKeyPolicy("k1"); got == nil || got.WeeklyBudgetUSD != 0 {
		t.Fatalf("k1 weekly budget = %v", got)
	}
	if got := cfg.FindAPIKeyPolicy("k2"); got == nil || got.WeeklyBudgetUSD != 400 {
		t.Fatalf("k2 weekly budget = %v", got)
	}
}

func TestConfig_SanitizeAPIKeyPolicies_NormalizesWeeklyBudgetAnchor(t *testing.T) {
	cfg := &Config{
		APIKeyPolicies: []APIKeyPolicy{
			{APIKey: "k1", WeeklyBudgetUSD: 400, WeeklyBudgetAnchorAt: "2026-03-15T10:37:12+08:00"},
			{APIKey: "k2", WeeklyBudgetUSD: 400, WeeklyBudgetAnchorAt: "invalid"},
		},
	}

	cfg.SanitizeAPIKeyPolicies()

	if got := cfg.FindAPIKeyPolicy("k1"); got == nil || got.WeeklyBudgetAnchorAt != "2026-03-15T10:00:00+08:00" {
		t.Fatalf("k1 anchor = %v", got)
	}
	if got := cfg.FindAPIKeyPolicy("k2"); got == nil || got.WeeklyBudgetAnchorAt != "" {
		t.Fatalf("k2 anchor = %v", got)
	}
}

func TestAPIKeyPolicy_WeeklyBudgetBoundsUsesCreatedAtWhenAnchorUnset(t *testing.T) {
	p := &APIKeyPolicy{
		APIKey:          "k1",
		WeeklyBudgetUSD: 400,
		CreatedAt:       "2026-04-09T14:14:57Z",
	}
	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)

	start, end, err := p.WeeklyBudgetBounds(now)
	if err != nil {
		t.Fatalf("WeeklyBudgetBounds: %v", err)
	}
	wantStart := time.Date(2026, 4, 16, 14, 14, 57, 0, time.UTC)
	if !start.Equal(wantStart) {
		t.Fatalf("start = %s, want %s", start.Format(time.RFC3339), wantStart.Format(time.RFC3339))
	}
	if !end.Equal(wantStart.Add(7 * 24 * time.Hour)) {
		t.Fatalf("end = %s, want %s", end.Format(time.RFC3339), wantStart.Add(7*24*time.Hour).Format(time.RFC3339))
	}
}

func TestAPIKeyPolicy_WeeklyBudgetBoundsErrorsWithoutAnchorOrCreatedAt(t *testing.T) {
	p := &APIKeyPolicy{APIKey: "k1", WeeklyBudgetUSD: 400}

	_, _, err := p.WeeklyBudgetBounds(time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("expected missing created_at error")
	}
}

func TestConfig_SanitizeAPIKeyPolicies_ClearsBaseBudgetsButPreservesAnchorWhenGroupBound(t *testing.T) {
	cfg := &Config{
		APIKeyPolicies: []APIKeyPolicy{
			{
				APIKey:               "k1",
				GroupID:              "triple",
				DailyBudgetUSD:       100,
				WeeklyBudgetUSD:      300,
				WeeklyBudgetAnchorAt: "2026-03-15T10:37:12+08:00",
			},
		},
	}

	cfg.SanitizeAPIKeyPolicies()

	got := cfg.FindAPIKeyPolicy("k1")
	if got == nil {
		t.Fatal("expected k1 policy")
	}
	if got.DailyBudgetUSD != 0 || got.WeeklyBudgetUSD != 0 {
		t.Fatalf("expected group-bound base budgets cleared, got %+v", got)
	}
	if got.WeeklyBudgetAnchorAt != "2026-03-15T10:00:00+08:00" {
		t.Fatalf("expected normalized anchor to be preserved, got %+v", got)
	}
}

func TestConfig_SanitizeAPIKeyPolicies_NormalizesTokenPackageStart(t *testing.T) {
	cfg := &Config{
		APIKeyPolicies: []APIKeyPolicy{
			{
				APIKey:                "k1",
				TokenPackageUSD:       1000,
				TokenPackageStartedAt: "2026-03-15T10:37:12+08:00",
			},
		},
	}

	cfg.SanitizeAPIKeyPolicies()

	got := cfg.FindAPIKeyPolicy("k1")
	if got == nil {
		t.Fatal("expected k1 policy")
	}
	if got.TokenPackageUSD != 1000 {
		t.Fatalf("token package usd=%v", got.TokenPackageUSD)
	}
	if got.TokenPackageStartedAt != "2026-03-15T10:37:12+08:00" {
		t.Fatalf("token package startedAt=%q", got.TokenPackageStartedAt)
	}
}

func TestConfig_SanitizeAPIKeyPolicies_DisablesTokenPackageWhenStartInvalid(t *testing.T) {
	cfg := &Config{
		APIKeyPolicies: []APIKeyPolicy{
			{
				APIKey:                "k1",
				TokenPackageUSD:       1000,
				TokenPackageStartedAt: "invalid",
			},
		},
	}

	cfg.SanitizeAPIKeyPolicies()

	got := cfg.FindAPIKeyPolicy("k1")
	if got == nil {
		t.Fatal("expected k1 policy")
	}
	if got.TokenPackageUSD != 0 || got.TokenPackageStartedAt != "" {
		t.Fatalf("token package=%+v", got)
	}
}

func TestConfig_SanitizeAPIKeyPolicies_DisablesClaudeUsageLimitWhenNonPositive(t *testing.T) {
	cfg := &Config{
		APIKeyPolicies: []APIKeyPolicy{
			{APIKey: "k1", ClaudeUsageLimitUSD: -1},
			{APIKey: "k2", ClaudeUsageLimitUSD: 25},
		},
	}

	cfg.SanitizeAPIKeyPolicies()

	if got := cfg.FindAPIKeyPolicy("k1"); got == nil || got.ClaudeUsageLimitUSD != 0 {
		t.Fatalf("k1 claude usage limit = %v", got)
	}
	if got := cfg.FindAPIKeyPolicy("k2"); got == nil || got.ClaudeUsageLimitUSD != 25 {
		t.Fatalf("k2 claude usage limit = %v", got)
	}
}

func TestConfig_SanitizeAPIKeyPolicies_NormalizesCodexChannelMode(t *testing.T) {
	cfg := &Config{
		APIKeyPolicies: []APIKeyPolicy{
			{APIKey: "k1", CodexChannelMode: " Provider "},
			{APIKey: "k2", CodexChannelMode: "AUTH_FILE"},
			{APIKey: "k3", CodexChannelMode: "invalid"},
			{APIKey: "k4"},
		},
	}

	cfg.SanitizeAPIKeyPolicies()

	if got := cfg.FindAPIKeyPolicy("k1"); got == nil || got.CodexChannelMode != "provider" {
		t.Fatalf("k1 codex channel mode = %v", got)
	}
	if got := cfg.FindAPIKeyPolicy("k2"); got == nil || got.CodexChannelMode != "auth_file" {
		t.Fatalf("k2 codex channel mode = %v", got)
	}
	if got := cfg.FindAPIKeyPolicy("k3"); got == nil || got.CodexChannelMode != "auto" {
		t.Fatalf("k3 codex channel mode = %v", got)
	}
	if got := cfg.FindAPIKeyPolicy("k4"); got == nil || got.CodexChannelMode != "auto" {
		t.Fatalf("k4 codex channel mode = %v", got)
	}
}

func TestConfig_ShouldRouteClaudeToGPT_DefaultsToAllKeysWhenGlobalEnabled(t *testing.T) {
	cfg := &Config{SDKConfig: SDKConfig{ClaudeToGPTRoutingEnabled: true}}

	if !cfg.ShouldRouteClaudeToGPT("k1") {
		t.Fatal("expected global Claude -> GPT routing to apply to keys without explicit policy")
	}
}

func TestConfig_ShouldRouteClaudeToGPT_RespectsPerKeyClaudeEnable(t *testing.T) {
	cfg := &Config{
		SDKConfig: SDKConfig{ClaudeToGPTRoutingEnabled: true},
		APIKeyPolicies: []APIKeyPolicy{
			{APIKey: "k1", EnableClaudeModels: boolPtr(true)},
			{APIKey: "k2"},
		},
	}

	if cfg.ShouldRouteClaudeToGPT("k1") {
		t.Fatal("expected k1 to bypass global Claude -> GPT routing")
	}
	if !cfg.ShouldRouteClaudeToGPT("k2") {
		t.Fatal("expected k2 to inherit global Claude -> GPT routing")
	}
}

func TestConfig_AllowsClaudeOpus1M_DefaultsEnabledWhenGlobalDisabled(t *testing.T) {
	cfg := &Config{}

	if !cfg.AllowsClaudeOpus1M("k1") {
		t.Fatal("expected Opus 1M to remain enabled when the global switch is off")
	}
}

func TestConfig_AllowsClaudeOpus1M_RespectsPerKeyOverride(t *testing.T) {
	cfg := &Config{
		SDKConfig: SDKConfig{DisableClaudeOpus1M: true},
		APIKeyPolicies: []APIKeyPolicy{
			{APIKey: "k1", EnableClaudeOpus1M: boolPtr(true)},
			{APIKey: "k2"},
		},
	}

	if !cfg.AllowsClaudeOpus1M("k1") {
		t.Fatal("expected k1 to override the global Opus 1M disable switch")
	}
	if cfg.AllowsClaudeOpus1M("k2") {
		t.Fatal("expected k2 to inherit the global Opus 1M disable switch")
	}
	if cfg.AllowsClaudeOpus1M("k3") {
		t.Fatal("expected unknown keys to inherit the global Opus 1M disable switch")
	}
}

func TestConfig_EffectiveAPIKeyPolicy_AddsGlobalClaudeRoutingRules(t *testing.T) {
	cfg := &Config{SDKConfig: SDKConfig{ClaudeToGPTRoutingEnabled: true}}

	policy := cfg.EffectiveAPIKeyPolicy("k1")
	if policy == nil {
		t.Fatal("expected synthesized policy")
	}
	if len(policy.ModelRouting.Rules) < 2 {
		t.Fatalf("expected synthesized routing rules, got %+v", policy.ModelRouting.Rules)
	}

	target, decision := policy.RoutedModelFor("k1", "claude-opus-4-6", time.Unix(0, 0))
	if decision == nil || target != "gpt-5.4(high)" {
		t.Fatalf("expected opus routing to high, got target=%q decision=%+v", target, decision)
	}

	target, decision = policy.RoutedModelFor("k1", "claude-sonnet-4-6", time.Unix(0, 0))
	if decision == nil || target != "gpt-5.3-codex(high)" {
		t.Fatalf("expected sonnet routing to gpt-5.3-codex(high), got target=%q decision=%+v", target, decision)
	}
}

func TestConfig_EffectiveAPIKeyPolicy_UsesGlobalClaudeGPTReasoningEffort(t *testing.T) {
	cfg := &Config{
		SDKConfig: SDKConfig{
			ClaudeToGPTRoutingEnabled:  true,
			ClaudeToGPTReasoningEffort: "low",
		},
	}

	policy := cfg.EffectiveAPIKeyPolicy("k1")
	if policy == nil {
		t.Fatal("expected synthesized policy")
	}

	target, decision := policy.RoutedModelFor("k1", "claude-opus-4-6", time.Unix(0, 0))
	if decision == nil || target != "gpt-5.4(low)" {
		t.Fatalf("expected opus routing to gpt-5.4(low), got target=%q decision=%+v", target, decision)
	}

	target, decision = policy.RoutedModelFor("k1", "claude-sonnet-4-6", time.Unix(0, 0))
	if decision == nil || target != "gpt-5.3-codex(low)" {
		t.Fatalf("expected sonnet routing to gpt-5.3-codex(low), got target=%q decision=%+v", target, decision)
	}
}

func TestConfig_EffectiveAPIKeyPolicy_UsesPerKeyClaudeGPTTargetFamily(t *testing.T) {
	cfg := &Config{
		SDKConfig: SDKConfig{
			ClaudeToGPTRoutingEnabled: true,
		},
		APIKeyPolicies: []APIKeyPolicy{
			{APIKey: "k1", ClaudeGPTTargetFamily: "gpt-5.2"},
		},
	}

	policy := cfg.EffectiveAPIKeyPolicy("k1")
	if policy == nil {
		t.Fatal("expected synthesized policy")
	}

	target, decision := policy.RoutedModelFor("k1", "claude-opus-4-6", time.Unix(0, 0))
	if decision == nil || target != "gpt-5.2(high)" {
		t.Fatalf("expected opus routing to gpt-5.2(high), got target=%q decision=%+v", target, decision)
	}

	target, decision = policy.RoutedModelFor("k1", "claude-sonnet-4-6", time.Unix(0, 0))
	if decision == nil || target != "gpt-5.2(medium)" {
		t.Fatalf("expected sonnet routing to gpt-5.2(medium), got target=%q decision=%+v", target, decision)
	}
}

func TestConfig_EffectiveAPIKeyPolicyWithOptions_ForcesGlobalClaudeRouting(t *testing.T) {
	cfg := &Config{
		SDKConfig: SDKConfig{
			ClaudeToGPTRoutingEnabled: true,
		},
		APIKeyPolicies: []APIKeyPolicy{
			{
				APIKey:                "k1",
				EnableClaudeModels:    boolPtr(true),
				ClaudeUsageLimitUSD:   10,
				ClaudeGPTTargetFamily: "gpt-5.2",
			},
		},
	}

	policy := cfg.EffectiveAPIKeyPolicyWithOptions("k1", APIKeyPolicyEffectiveOptions{
		ForceGlobalClaudeRouting: true,
	})
	if policy == nil {
		t.Fatal("expected synthesized policy")
	}
	if policy.ClaudeModelsEnabled() {
		t.Fatal("expected forced policy to stop opting into direct Claude execution")
	}

	target, decision := policy.RoutedModelFor("k1", "claude-opus-4-6", time.Unix(0, 0))
	if decision == nil || target != "gpt-5.4(high)" {
		t.Fatalf("expected forced opus routing to gpt-5.4(high), got target=%q decision=%+v", target, decision)
	}

	target, decision = policy.RoutedModelFor("k1", "claude-sonnet-4-6", time.Unix(0, 0))
	if decision == nil || target != "gpt-5.3-codex(high)" {
		t.Fatalf("expected forced sonnet routing to gpt-5.3-codex(high), got target=%q decision=%+v", target, decision)
	}
}

func TestConfig_EffectiveAPIKeyPolicy_UsesPerKeyClaudeGPT53CodexTarget(t *testing.T) {
	cfg := &Config{
		SDKConfig: SDKConfig{
			ClaudeToGPTRoutingEnabled: true,
		},
		APIKeyPolicies: []APIKeyPolicy{
			{APIKey: "k1", ClaudeGPTTargetFamily: "gpt-5.3-codex"},
		},
	}

	policy := cfg.EffectiveAPIKeyPolicy("k1")
	if policy == nil {
		t.Fatal("expected synthesized policy")
	}

	target, decision := policy.RoutedModelFor("k1", "claude-opus-4-6", time.Unix(0, 0))
	if decision == nil || target != "gpt-5.3-codex(high)" {
		t.Fatalf("expected opus routing to gpt-5.3-codex(high), got target=%q decision=%+v", target, decision)
	}

	target, decision = policy.RoutedModelFor("k1", "claude-sonnet-4-6", time.Unix(0, 0))
	if decision == nil || target != "gpt-5.3-codex(medium)" {
		t.Fatalf("expected sonnet routing to gpt-5.3-codex(medium), got target=%q decision=%+v", target, decision)
	}
}

func TestConfig_EffectiveAPIKeyPolicy_SynthesizesClaudeFailoverForOptInKeys(t *testing.T) {
	cfg := &Config{
		SDKConfig: SDKConfig{ClaudeToGPTRoutingEnabled: true},
		APIKeyPolicies: []APIKeyPolicy{
			{APIKey: "k1", EnableClaudeModels: boolPtr(true)},
		},
	}

	policy := cfg.EffectiveAPIKeyPolicy("k1")
	if policy == nil {
		t.Fatal("expected synthesized policy")
	}
	if routed, decision := policy.RoutedModelFor("k1", "claude-opus-4-6", time.Unix(0, 0)); decision != nil || routed != "" {
		t.Fatalf("expected opted-in key to keep Claude routing, got target=%q decision=%+v", routed, decision)
	}

	target, ok := policy.ClaudeFailoverTargetModelFor("claude-opus-4-6")
	if !ok || target != "gpt-5.4(high)" {
		t.Fatalf("expected opus failover target gpt-5.4(high), got target=%q enabled=%v", target, ok)
	}

	target, ok = policy.ClaudeFailoverTargetModelFor("claude-sonnet-4-6")
	if !ok || target != "gpt-5.3-codex(high)" {
		t.Fatalf("expected sonnet failover target gpt-5.3-codex(high), got target=%q enabled=%v", target, ok)
	}
}

func TestConfig_EffectiveAPIKeyPolicy_SynthesizesClaudeFailoverUsingGlobalReasoningEffort(t *testing.T) {
	cfg := &Config{
		SDKConfig: SDKConfig{
			ClaudeToGPTRoutingEnabled:  true,
			ClaudeToGPTReasoningEffort: "medium",
		},
		APIKeyPolicies: []APIKeyPolicy{
			{APIKey: "k1", EnableClaudeModels: boolPtr(true)},
		},
	}

	policy := cfg.EffectiveAPIKeyPolicy("k1")
	if policy == nil {
		t.Fatal("expected synthesized policy")
	}

	target, ok := policy.ClaudeFailoverTargetModelFor("claude-opus-4-6")
	if !ok || target != "gpt-5.4(medium)" {
		t.Fatalf("expected opus failover target gpt-5.4(medium), got target=%q enabled=%v", target, ok)
	}

	target, ok = policy.ClaudeFailoverTargetModelFor("claude-sonnet-4-6")
	if !ok || target != "gpt-5.3-codex(medium)" {
		t.Fatalf("expected sonnet failover target gpt-5.3-codex(medium), got target=%q enabled=%v", target, ok)
	}
}

func TestConfig_EffectiveAPIKeyPolicy_SynthesizesClaudeFailoverUsingPerKeyGPT53CodexTarget(t *testing.T) {
	cfg := &Config{
		SDKConfig: SDKConfig{ClaudeToGPTRoutingEnabled: true},
		APIKeyPolicies: []APIKeyPolicy{
			{
				APIKey:                "k1",
				EnableClaudeModels:    boolPtr(true),
				ClaudeGPTTargetFamily: "gpt-5.3-codex",
			},
		},
	}

	policy := cfg.EffectiveAPIKeyPolicy("k1")
	if policy == nil {
		t.Fatal("expected synthesized policy")
	}

	target, ok := policy.ClaudeFailoverTargetModelFor("claude-opus-4-6")
	if !ok || target != "gpt-5.3-codex(high)" {
		t.Fatalf("expected opus failover target gpt-5.3-codex(high), got target=%q enabled=%v", target, ok)
	}

	target, ok = policy.ClaudeFailoverTargetModelFor("claude-sonnet-4-6")
	if !ok || target != "gpt-5.3-codex(medium)" {
		t.Fatalf("expected sonnet failover target gpt-5.3-codex(medium), got target=%q enabled=%v", target, ok)
	}
}

func TestConfig_EffectiveAPIKeyPolicy_SynthesizesClaudeFailoverUsingPerKeyFamily(t *testing.T) {
	cfg := &Config{
		SDKConfig: SDKConfig{ClaudeToGPTRoutingEnabled: true},
		APIKeyPolicies: []APIKeyPolicy{
			{
				APIKey:                "k1",
				EnableClaudeModels:    boolPtr(true),
				ClaudeGPTTargetFamily: "gpt-5.2",
			},
		},
	}

	policy := cfg.EffectiveAPIKeyPolicy("k1")
	if policy == nil {
		t.Fatal("expected synthesized policy")
	}

	target, ok := policy.ClaudeFailoverTargetModelFor("claude-opus-4-6")
	if !ok || target != "gpt-5.2(high)" {
		t.Fatalf("expected opus failover target gpt-5.2(high), got target=%q enabled=%v", target, ok)
	}

	target, ok = policy.ClaudeFailoverTargetModelFor("claude-sonnet-4-6")
	if !ok || target != "gpt-5.2(medium)" {
		t.Fatalf("expected sonnet failover target gpt-5.2(medium), got target=%q enabled=%v", target, ok)
	}
}

func TestConfig_EffectiveAPIKeyPolicy_DefaultsUnknownKeysToClaudeOnlyClientAccess(t *testing.T) {
	cfg := &Config{}

	policy := cfg.EffectiveAPIKeyPolicy("k1")
	if policy == nil {
		t.Fatal("expected synthesized policy")
	}

	for _, pattern := range []string{"gpt-*", "chatgpt-*", "o1*", "o3*", "o4*"} {
		found := false
		for _, candidate := range policy.ExcludedModels {
			if candidate == pattern {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected default excluded pattern %q in %+v", pattern, policy.ExcludedModels)
		}
	}
}

func TestConfig_EffectiveAPIKeyPolicy_ExplicitPolicyCanAllowGPTAccess(t *testing.T) {
	cfg := &Config{
		APIKeyPolicies: []APIKeyPolicy{
			{APIKey: "k1"},
		},
	}

	policy := cfg.EffectiveAPIKeyPolicy("k1")
	if policy == nil {
		t.Fatal("expected explicit policy")
	}
	if len(policy.ExcludedModels) != 0 {
		t.Fatalf("expected explicit policy to preserve excluded models, got %+v", policy.ExcludedModels)
	}
}

func TestAPIKeyPolicy_ClaudeFailoverTargetModel_DefaultsToMedium(t *testing.T) {
	policy := &APIKeyPolicy{
		Failover: APIKeyFailoverPolicy{
			Claude: ProviderFailoverPolicy{Enabled: true},
		},
	}

	target, ok := policy.ClaudeFailoverTargetModel()
	if !ok {
		t.Fatal("expected failover target to be enabled")
	}
	if target != "gpt-5.4(medium)" {
		t.Fatalf("expected default failover target gpt-5.4(medium), got %q", target)
	}
}

func TestAPIKeyPolicy_ClaudeFailoverTargetModel_AllowsConfiguredGPT52Targets(t *testing.T) {
	policy := &APIKeyPolicy{
		Failover: APIKeyFailoverPolicy{
			Claude: ProviderFailoverPolicy{
				Enabled:     true,
				TargetModel: "gpt-5.2(medium)",
				Rules: []ModelFailoverRule{
					{FromModel: "claude-opus-*", TargetModel: "gpt-5.2(high)"},
				},
			},
		},
	}

	target, ok := policy.ClaudeFailoverTargetModelFor("claude-opus-4-6")
	if !ok || target != "gpt-5.2(high)" {
		t.Fatalf("expected opus failover target gpt-5.2(high), got target=%q enabled=%v", target, ok)
	}

	target, ok = policy.ClaudeFailoverTargetModelFor("claude-sonnet-4-6")
	if !ok || target != "gpt-5.2(medium)" {
		t.Fatalf("expected default failover target gpt-5.2(medium), got target=%q enabled=%v", target, ok)
	}
}

func TestConfig_SanitizeAPIKeyPolicies_NormalizesClaudeGPTTargetFamily(t *testing.T) {
	cfg := &Config{
		APIKeyPolicies: []APIKeyPolicy{
			{APIKey: "k1", ClaudeGPTTargetFamily: " GPT-5.2 "},
			{APIKey: "k2", ClaudeGPTTargetFamily: " GPT-5.3-CODEX "},
			{APIKey: "k3", ClaudeGPTTargetFamily: "unknown"},
		},
	}

	cfg.SanitizeAPIKeyPolicies()

	if got := cfg.FindAPIKeyPolicy("k1"); got == nil || got.ClaudeGPTTargetFamily != "gpt-5.2" {
		t.Fatalf("k1 family = %v", got)
	}
	if got := cfg.FindAPIKeyPolicy("k2"); got == nil || got.ClaudeGPTTargetFamily != "gpt-5.3-codex" {
		t.Fatalf("k2 family = %v", got)
	}
	if got := cfg.FindAPIKeyPolicy("k3"); got == nil || got.ClaudeGPTTargetFamily != "" {
		t.Fatalf("k2 family = %v", got)
	}
}

func TestConfig_ClaudeGPTTargetFamilyOrDefault(t *testing.T) {
	cfg := &Config{}
	if got := cfg.ClaudeGPTTargetFamilyOrDefault(); got != "gpt-5.4" {
		t.Fatalf("expected default family gpt-5.4, got %q", got)
	}

	cfg.ClaudeToGPTTargetFamily = "gpt-5.2"
	if got := cfg.ClaudeGPTTargetFamilyOrDefault(); got != "gpt-5.2" {
		t.Fatalf("expected configured family gpt-5.2, got %q", got)
	}

	cfg.ClaudeToGPTTargetFamily = "gpt-5.3-codex"
	if got := cfg.ClaudeGPTTargetFamilyOrDefault(); got != "gpt-5.3-codex" {
		t.Fatalf("expected configured family gpt-5.3-codex, got %q", got)
	}
}
