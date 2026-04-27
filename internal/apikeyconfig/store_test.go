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
				OwnerUsername:  "user_01",
				OwnerRole:      "staff",
			},
		},
	}

	state := StateFromConfig(source)
	if len(state.Records) != 2 || state.Records[0].APIKey != "key-1" || state.Records[1].APIKey != "key-2" {
		t.Fatalf("unexpected records: %#v", state.Records)
	}
	if got := state.Records[1].Policy.ExcludedModels; len(got) == 0 || got[0] != "gpt-*" {
		t.Fatalf("expected default GPT exclusions for key-2, got %#v", got)
	}
	if state.Records[0].OwnerUsername != "user_01" || state.Records[0].OwnerRole != "staff" {
		t.Fatalf("expected explicit owner to be preserved, got %#v", state.Records[0])
	}
	if state.Records[1].OwnerUsername != "admin" || state.Records[1].OwnerRole != "admin" {
		t.Fatalf("expected admin owner defaults for key-2, got %#v", state.Records[1])
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
	if len(target.APIKeyPolicies) != 2 || target.APIKeyPolicies[0].APIKey != "key-1" || target.APIKeyPolicies[1].APIKey != "key-2" {
		t.Fatalf("unexpected target policies: %#v", target.APIKeyPolicies)
	}
	if target.APIKeyPolicies[0].OwnerUsername != "user_01" || target.APIKeyPolicies[0].OwnerRole != "staff" {
		t.Fatalf("unexpected target owner: %#v", target.APIKeyPolicies[0])
	}

	state.Records[0].Policy.ExcludedModels[0] = "changed"
	if target.APIKeyPolicies[0].ExcludedModels[0] != "claude-*" {
		t.Fatalf("target policy should be isolated from state mutation: %#v", target.APIKeyPolicies[0].ExcludedModels)
	}
}
