package apikeyconfig

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestStateFromConfigAndApplyToConfig(t *testing.T) {
	source := &config.Config{
		SDKConfig: config.SDKConfig{
			APIKeys: []string{" key-1 ", "key-1", "", "key-2"},
		},
		APIKeyPolicies: []config.APIKeyPolicy{
			{
				APIKey:         "key-1",
				ExcludedModels: []string{"claude-*"},
				DailyLimits:    map[string]int{"claude-opus-4-6": 5},
			},
		},
	}

	state := StateFromConfig(source)
	if len(state.APIKeys) != 2 || state.APIKeys[0] != "key-1" || state.APIKeys[1] != "key-2" {
		t.Fatalf("unexpected api keys: %#v", state.APIKeys)
	}

	target := &config.Config{
		SDKConfig: config.SDKConfig{
			APIKeys: []string{"old"},
		},
		APIKeyPolicies: []config.APIKeyPolicy{
			{APIKey: "old"},
		},
	}
	state.ApplyToConfig(target)

	if len(target.APIKeys) != 2 || target.APIKeys[0] != "key-1" || target.APIKeys[1] != "key-2" {
		t.Fatalf("unexpected target api keys: %#v", target.APIKeys)
	}
	if len(target.APIKeyPolicies) != 1 || target.APIKeyPolicies[0].APIKey != "key-1" {
		t.Fatalf("unexpected target policies: %#v", target.APIKeyPolicies)
	}

	state.APIKeyPolicies[0].ExcludedModels[0] = "changed"
	if target.APIKeyPolicies[0].ExcludedModels[0] != "claude-*" {
		t.Fatalf("target policy should be isolated from state mutation: %#v", target.APIKeyPolicies[0].ExcludedModels)
	}
}
