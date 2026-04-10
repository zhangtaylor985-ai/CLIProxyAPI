package management

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestPolicyViewRoundTrip_ClaudeCodeOnlyMode(t *testing.T) {
	enabledPolicy := config.APIKeyPolicy{
		APIKey:            "k-enabled",
		ClaudeCodeOnly:    boolPtr(true),
		AllowClaudeOpus46: boolPtr(true),
	}
	enabledView := policyToView("k-enabled", &enabledPolicy, nil)
	if enabledView.ClaudeCodeOnlyMode != "enabled" {
		t.Fatalf("enabled mode = %q, want enabled", enabledView.ClaudeCodeOnlyMode)
	}
	enabledRoundTrip := viewToPolicy("k-enabled", enabledView)
	if enabledRoundTrip.ClaudeCodeOnly == nil || !*enabledRoundTrip.ClaudeCodeOnly {
		t.Fatalf("enabled round trip = %+v", enabledRoundTrip.ClaudeCodeOnly)
	}

	disabledPolicy := config.APIKeyPolicy{
		APIKey:            "k-disabled",
		ClaudeCodeOnly:    boolPtr(false),
		AllowClaudeOpus46: boolPtr(true),
	}
	disabledView := policyToView("k-disabled", &disabledPolicy, nil)
	if disabledView.ClaudeCodeOnlyMode != "disabled" {
		t.Fatalf("disabled mode = %q, want disabled", disabledView.ClaudeCodeOnlyMode)
	}
	disabledRoundTrip := viewToPolicy("k-disabled", disabledView)
	if disabledRoundTrip.ClaudeCodeOnly == nil || *disabledRoundTrip.ClaudeCodeOnly {
		t.Fatalf("disabled round trip = %+v", disabledRoundTrip.ClaudeCodeOnly)
	}

	inheritView := defaultAPIKeyPolicyView("k-inherit")
	if inheritView.ClaudeCodeOnlyMode != "inherit" {
		t.Fatalf("inherit mode = %q, want inherit", inheritView.ClaudeCodeOnlyMode)
	}
	inheritRoundTrip := viewToPolicy("k-inherit", inheritView)
	if inheritRoundTrip.ClaudeCodeOnly != nil {
		t.Fatalf("inherit round trip = %+v, want nil", inheritRoundTrip.ClaudeCodeOnly)
	}
}

func boolPtr(v bool) *bool { return &v }
