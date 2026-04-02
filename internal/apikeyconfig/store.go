package apikeyconfig

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

// Store persists API key identities and API key policies outside config.yaml.
type Store interface {
	Close() error
	LoadState(ctx context.Context) (State, bool, error)
	SaveState(ctx context.Context, state State) error
}

// State captures API key identities and policy definitions.
type State struct {
	APIKeys        []string              `json:"api_keys"`
	APIKeyPolicies []config.APIKeyPolicy `json:"api_key_policies"`
}

// StateFromConfig extracts API key state from cfg.
func StateFromConfig(cfg *config.Config) State {
	if cfg == nil {
		return State{}
	}
	return State{
		APIKeys:        normalizeAPIKeys(cfg.APIKeys),
		APIKeyPolicies: clonePolicies(cfg.APIKeyPolicies),
	}
}

// ApplyToConfig overlays the state onto cfg.
func (s State) ApplyToConfig(cfg *config.Config) {
	if cfg == nil {
		return
	}
	cfg.APIKeys = normalizeAPIKeys(s.APIKeys)
	cfg.APIKeyPolicies = clonePolicies(s.APIKeyPolicies)
	cfg.SanitizeAPIKeyPolicies()
}

// ResolvePostgresConfigFromEnv resolves the policy store DSN and schema from env.
func ResolvePostgresConfigFromEnv() (dsn string, schema string) {
	for _, key := range []string{"APIKEY_POLICY_PG_DSN", "APIKEY_BILLING_PG_DSN", "PGSTORE_DSN"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			dsn = value
			break
		}
	}
	for _, key := range []string{"APIKEY_POLICY_PG_SCHEMA", "APIKEY_BILLING_PG_SCHEMA", "PGSTORE_SCHEMA"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			schema = value
			break
		}
	}
	return dsn, schema
}

func normalizeAPIKeys(keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(keys))
	out := make([]string, 0, len(keys))
	for _, raw := range keys {
		key := strings.TrimSpace(raw)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func clonePolicies(policies []config.APIKeyPolicy) []config.APIKeyPolicy {
	if len(policies) == 0 {
		return nil
	}
	raw, err := json.Marshal(policies)
	if err != nil {
		out := make([]config.APIKeyPolicy, len(policies))
		copy(out, policies)
		return out
	}
	var out []config.APIKeyPolicy
	if err := json.Unmarshal(raw, &out); err != nil {
		out = make([]config.APIKeyPolicy, len(policies))
		copy(out, policies)
		return out
	}
	return out
}
