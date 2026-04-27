package apikeyconfig

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

// Store persists API key identities and API key policies outside config.yaml.
type Store interface {
	Close() error
	LoadState(ctx context.Context) (State, bool, error)
	SaveState(ctx context.Context, state State) error
	SaveRecord(ctx context.Context, previousAPIKey string, record Record) error
	DeleteRecord(ctx context.Context, apiKey string) error
}

// Record captures one API key row from the external store.
type Record struct {
	APIKey        string              `json:"api_key"`
	Policy        config.APIKeyPolicy `json:"policy"`
	CreatedAt     time.Time           `json:"created_at"`
	ExpiresAt     *time.Time          `json:"expires_at,omitempty"`
	Disabled      bool                `json:"disabled"`
	OwnerUsername string              `json:"owner_username"`
	OwnerRole     string              `json:"owner_role"`
}

// State captures API key records persisted outside config.yaml.
// APIKeys and APIKeyPolicies are legacy compatibility fields used only when decoding
// older single-blob payloads.
type State struct {
	Records        []Record              `json:"records,omitempty"`
	APIKeys        []string              `json:"api_keys,omitempty"`
	APIKeyPolicies []config.APIKeyPolicy `json:"api_key_policies,omitempty"`
}

// StateFromConfig extracts API key state from cfg.
func StateFromConfig(cfg *config.Config) State {
	if cfg == nil {
		return State{}
	}

	keys := normalizeAPIKeys(cfg.APIKeys)
	seen := make(map[string]struct{}, len(keys)+len(cfg.APIKeyPolicies))
	records := make([]Record, 0, len(keys)+len(cfg.APIKeyPolicies))

	appendRecord := func(key string, policy config.APIKeyPolicy) {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			return
		}
		if _, ok := seen[trimmedKey]; ok {
			return
		}
		seen[trimmedKey] = struct{}{}
		record := policyToRecord(policy)
		record.APIKey = trimmedKey
		record.Policy.APIKey = trimmedKey
		if record.Policy.CreatedAt == "" {
			record.Policy.CreatedAt = record.CreatedAt.Format(time.RFC3339)
		}
		records = append(records, record)
	}

	for _, key := range keys {
		if explicit := cfg.FindAPIKeyPolicy(key); explicit != nil {
			appendRecord(key, *explicit)
			continue
		}
		appendRecord(key, defaultStoredPolicy(key))
	}
	for _, entry := range cfg.APIKeyPolicies {
		appendRecord(entry.APIKey, entry)
	}

	return State{Records: normalizeRecords(records)}
}

// RecordFromConfig extracts one API key record from cfg.
func RecordFromConfig(cfg *config.Config, apiKey string) (Record, bool) {
	if cfg == nil {
		return Record{}, false
	}
	key := strings.TrimSpace(apiKey)
	if key == "" {
		return Record{}, false
	}

	if explicit := cfg.FindAPIKeyPolicy(key); explicit != nil {
		records := normalizeRecords([]Record{policyToRecord(*explicit)})
		if len(records) == 1 {
			return records[0], true
		}
		return Record{}, false
	}

	for _, existing := range normalizeAPIKeys(cfg.APIKeys) {
		if existing != key {
			continue
		}
		record := policyToRecord(defaultStoredPolicy(key))
		record.APIKey = key
		record.Policy.APIKey = key
		records := normalizeRecords([]Record{record})
		if len(records) == 1 {
			return records[0], true
		}
		return Record{}, false
	}

	return Record{}, false
}

// ConfigWithoutAPIKeyState returns a deep-copied config with API key store-backed fields removed.
func ConfigWithoutAPIKeyState(cfg *config.Config) *config.Config {
	if cfg == nil {
		return nil
	}
	copyCfg := *cfg
	copyCfg.APIKeys = nil
	copyCfg.APIKeyPolicies = nil
	return &copyCfg
}

// ApplyToConfig overlays the state onto cfg.
func (s State) ApplyToConfig(cfg *config.Config) {
	if cfg == nil {
		return
	}
	normalized := s.Normalized()
	cfg.APIKeys = stateAPIKeys(normalized.Records)
	cfg.APIKeyPolicies = statePolicies(normalized.Records)
	cfg.SanitizeAPIKeyPolicies()
}

// Normalized converts legacy payloads and sanitizes record order/content.
func (s State) Normalized() State {
	if len(s.Records) > 0 {
		return State{Records: normalizeRecords(s.Records)}
	}
	return State{Records: legacyStateToRecords(s.APIKeys, s.APIKeyPolicies)}
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
		out := make([]config.APIKeyPolicy, len(policies))
		copy(out, policies)
		return out
	}
	return out
}

func defaultStoredPolicy(apiKey string) config.APIKeyPolicy {
	return config.APIKeyPolicy{
		APIKey:         strings.TrimSpace(apiKey),
		OwnerUsername:  "admin",
		OwnerRole:      "admin",
		ExcludedModels: config.BuildExcludedModelFamilies(true, false, nil),
	}
}

func policyToRecord(policy config.APIKeyPolicy) Record {
	record := Record{
		APIKey:        strings.TrimSpace(policy.APIKey),
		Policy:        policy,
		Disabled:      policy.Disabled,
		OwnerUsername: strings.TrimSpace(policy.OwnerUsername),
		OwnerRole:     normalizeOwnerRole(policy.OwnerRole),
	}
	if createdAt, ok := policy.CreatedTime(); ok {
		record.CreatedAt = createdAt
	} else {
		record.CreatedAt = time.Now().UTC()
		record.Policy.CreatedAt = record.CreatedAt.Format(time.RFC3339)
	}
	if expiresAt, ok := policy.ExpiryTime(); ok {
		expiresAtCopy := expiresAt
		record.ExpiresAt = &expiresAtCopy
	}
	return record
}

func normalizeRecords(records []Record) []Record {
	if len(records) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(records))
	out := make([]Record, 0, len(records))
	for _, raw := range records {
		record := raw
		record.APIKey = strings.TrimSpace(record.APIKey)
		if record.APIKey == "" {
			record.APIKey = strings.TrimSpace(record.Policy.APIKey)
		}
		if record.APIKey == "" {
			continue
		}
		if _, ok := seen[record.APIKey]; ok {
			continue
		}
		seen[record.APIKey] = struct{}{}
		record.Policy.APIKey = record.APIKey
		record.Policy.Disabled = record.Disabled
		record.OwnerUsername = strings.TrimSpace(record.OwnerUsername)
		if record.OwnerUsername == "" {
			record.OwnerUsername = strings.TrimSpace(record.Policy.OwnerUsername)
		}
		record.OwnerRole = normalizeOwnerRole(record.OwnerRole)
		if record.OwnerRole == "" {
			record.OwnerRole = normalizeOwnerRole(record.Policy.OwnerRole)
		}
		if record.OwnerRole == "" {
			record.OwnerRole = "admin"
		}
		if record.OwnerRole == "admin" {
			record.OwnerUsername = "admin"
		} else if record.OwnerUsername == "" {
			record.OwnerUsername = "unknown"
		}
		record.Policy.OwnerUsername = record.OwnerUsername
		record.Policy.OwnerRole = record.OwnerRole
		if record.CreatedAt.IsZero() {
			if createdAt, ok := record.Policy.CreatedTime(); ok {
				record.CreatedAt = createdAt
			} else {
				record.CreatedAt = time.Now().UTC()
			}
		}
		record.Policy.CreatedAt = record.CreatedAt.UTC().Format(time.RFC3339)
		if record.ExpiresAt != nil {
			expiresAtCopy := record.ExpiresAt.UTC()
			record.ExpiresAt = &expiresAtCopy
			record.Policy.ExpiresAt = expiresAtCopy.Format(time.RFC3339)
		} else {
			record.Policy.ExpiresAt = ""
		}
		record.Policy.Disabled = record.Disabled
		cfg := &config.Config{APIKeyPolicies: []config.APIKeyPolicy{record.Policy}}
		cfg.SanitizeAPIKeyPolicies()
		if len(cfg.APIKeyPolicies) == 0 {
			record.Policy = defaultStoredPolicy(record.APIKey)
		} else {
			record.Policy = cfg.APIKeyPolicies[0]
		}
		record.Policy.APIKey = record.APIKey
		record.Policy.CreatedAt = record.CreatedAt.UTC().Format(time.RFC3339)
		record.Policy.Disabled = record.Disabled
		record.Policy.OwnerUsername = record.OwnerUsername
		record.Policy.OwnerRole = record.OwnerRole
		if record.ExpiresAt != nil {
			record.Policy.ExpiresAt = record.ExpiresAt.UTC().Format(time.RFC3339)
		} else {
			record.Policy.ExpiresAt = ""
		}
		out = append(out, record)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func stateAPIKeys(records []Record) []string {
	keys := make([]string, 0, len(records))
	for _, record := range records {
		if trimmed := strings.TrimSpace(record.APIKey); trimmed != "" {
			keys = append(keys, trimmed)
		}
	}
	return normalizeAPIKeys(keys)
}

func statePolicies(records []Record) []config.APIKeyPolicy {
	if len(records) == 0 {
		return nil
	}
	policies := make([]config.APIKeyPolicy, 0, len(records))
	for _, record := range records {
		entry := record.Policy
		entry.APIKey = record.APIKey
		entry.CreatedAt = record.CreatedAt.UTC().Format(time.RFC3339)
		entry.Disabled = record.Disabled
		entry.OwnerUsername = strings.TrimSpace(record.OwnerUsername)
		entry.OwnerRole = normalizeOwnerRole(record.OwnerRole)
		if record.ExpiresAt != nil {
			entry.ExpiresAt = record.ExpiresAt.UTC().Format(time.RFC3339)
		} else {
			entry.ExpiresAt = ""
		}
		policies = append(policies, entry)
	}
	return clonePolicies(policies)
}

func legacyStateToRecords(keys []string, policies []config.APIKeyPolicy) []Record {
	normalizedKeys := normalizeAPIKeys(keys)
	seen := make(map[string]struct{}, len(normalizedKeys)+len(policies))
	records := make([]Record, 0, len(normalizedKeys)+len(policies))

	for _, key := range normalizedKeys {
		seen[key] = struct{}{}
		explicit := (*config.APIKeyPolicy)(nil)
		for i := range policies {
			if strings.TrimSpace(policies[i].APIKey) == key {
				explicit = &policies[i]
				break
			}
		}
		if explicit != nil {
			records = append(records, policyToRecord(*explicit))
		} else {
			records = append(records, policyToRecord(defaultStoredPolicy(key)))
		}
	}
	for _, entry := range policies {
		key := strings.TrimSpace(entry.APIKey)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		records = append(records, policyToRecord(entry))
	}
	return normalizeRecords(records)
}

func normalizeOwnerRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "staff":
		return "staff"
	case "admin":
		return "admin"
	default:
		return ""
	}
}
