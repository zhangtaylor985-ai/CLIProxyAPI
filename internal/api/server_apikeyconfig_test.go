package api

import (
	"context"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/apikeyconfig"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

type stubAPIKeyStateStore struct {
	state apikeyconfig.State
	found bool
	err   error
}

func (s *stubAPIKeyStateStore) Close() error { return nil }

func (s *stubAPIKeyStateStore) LoadState(context.Context) (apikeyconfig.State, bool, error) {
	return s.state, s.found, s.err
}

func (s *stubAPIKeyStateStore) SaveState(context.Context, apikeyconfig.State) error { return nil }
func (s *stubAPIKeyStateStore) SaveRecord(context.Context, string, apikeyconfig.Record) error {
	return nil
}
func (s *stubAPIKeyStateStore) DeleteRecord(context.Context, string) error { return nil }

func TestApplyAPIKeyConfigOverlay(t *testing.T) {
	cfg := &config.Config{
		SDKConfig: config.SDKConfig{
			APIKeys: []string{"yaml-key"},
		},
		APIKeyPolicies: []config.APIKeyPolicy{
			{APIKey: "yaml-key"},
		},
	}
	store := &stubAPIKeyStateStore{
		found: true,
		state: apikeyconfig.State{
			APIKeys: []string{"pg-key"},
			APIKeyPolicies: []config.APIKeyPolicy{
				{APIKey: "pg-key", WeeklyBudgetUSD: 10},
			},
		},
	}

	applied, err := applyAPIKeyConfigOverlay(context.Background(), store, cfg)
	if err != nil {
		t.Fatalf("applyAPIKeyConfigOverlay: %v", err)
	}
	if !applied {
		t.Fatal("expected overlay to apply")
	}
	if len(cfg.APIKeys) != 1 || cfg.APIKeys[0] != "pg-key" {
		t.Fatalf("unexpected cfg api keys: %#v", cfg.APIKeys)
	}
	if len(cfg.APIKeyPolicies) != 1 || cfg.APIKeyPolicies[0].APIKey != "pg-key" {
		t.Fatalf("unexpected cfg policies: %#v", cfg.APIKeyPolicies)
	}
}
