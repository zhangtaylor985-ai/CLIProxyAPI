package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigOptional_ClaudeCodeOnlyDefaultsEnabled(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("debug: false\n"), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}
	if !cfg.ClaudeCodeOnlyEnabled {
		t.Fatal("ClaudeCodeOnlyEnabled = false, want true by default")
	}
}

func TestLoadConfigOptional_ClaudeCodeOnlyAllowsExplicitDisable(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("claude-code-only-enabled: false\n"), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}
	if cfg.ClaudeCodeOnlyEnabled {
		t.Fatal("ClaudeCodeOnlyEnabled = true, want explicit false")
	}
}

func TestLoadConfigOptional_DisableClaudeOpus1MDefaultsEnabled(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("debug: false\n"), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}
	if !cfg.DisableClaudeOpus1M {
		t.Fatal("DisableClaudeOpus1M = false, want true by default")
	}
}

func TestLoadConfigOptional_DisableClaudeOpus1MAllowsExplicitDisable(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("disable-claude-opus-1m: false\n"), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}
	if cfg.DisableClaudeOpus1M {
		t.Fatal("DisableClaudeOpus1M = true, want explicit false")
	}
}

func TestLoadConfigOptional_DisablePromptTokenLimitDefaultsDisabled(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("debug: false\n"), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}
	if cfg.DisablePromptTokenLimit {
		t.Fatal("DisablePromptTokenLimit = true, want false by default")
	}
}

func TestLoadConfigOptional_DisablePromptTokenLimitAllowsExplicitEnable(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("disable-prompt-token-limit: true\n"), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}
	if !cfg.DisablePromptTokenLimit {
		t.Fatal("DisablePromptTokenLimit = false, want explicit true")
	}
}

func TestConfig_EffectiveAPIKeyPolicy_UsesGlobalClaudeCodeOnlyByDefault(t *testing.T) {
	cfg := &Config{
		SDKConfig: SDKConfig{
			ClaudeCodeOnlyEnabled: true,
		},
		APIKeyPolicies: []APIKeyPolicy{
			{APIKey: "k1"},
		},
	}

	policy := cfg.EffectiveAPIKeyPolicy("k1")
	if policy == nil {
		t.Fatal("expected effective policy")
	}
	if !policy.ClaudeCodeOnlyEnabled() {
		t.Fatal("expected effective policy to inherit global Claude Code only flag")
	}
	if policy.ClaudeCodeOnly == nil || !*policy.ClaudeCodeOnly {
		t.Fatalf("ClaudeCodeOnly = %+v, want non-nil true", policy.ClaudeCodeOnly)
	}
}

func TestConfig_EffectiveAPIKeyPolicy_UsesPerKeyClaudeCodeOnlyOverride(t *testing.T) {
	cfg := &Config{
		SDKConfig: SDKConfig{
			ClaudeCodeOnlyEnabled: true,
		},
		APIKeyPolicies: []APIKeyPolicy{
			{APIKey: "k-enabled", ClaudeCodeOnly: boolPtr(true)},
			{APIKey: "k-disabled", ClaudeCodeOnly: boolPtr(false)},
		},
	}

	enabledPolicy := cfg.EffectiveAPIKeyPolicy("k-enabled")
	if enabledPolicy == nil {
		t.Fatal("expected enabled effective policy")
	}
	if !enabledPolicy.ClaudeCodeOnlyEnabled() {
		t.Fatal("expected enabled policy to remain true")
	}

	disabledPolicy := cfg.EffectiveAPIKeyPolicy("k-disabled")
	if disabledPolicy == nil {
		t.Fatal("expected disabled effective policy")
	}
	if disabledPolicy.ClaudeCodeOnlyEnabled() {
		t.Fatal("expected disabled policy to override global true")
	}
	if disabledPolicy.ClaudeCodeOnly == nil || *disabledPolicy.ClaudeCodeOnly {
		t.Fatalf("ClaudeCodeOnly = %+v, want non-nil false", disabledPolicy.ClaudeCodeOnly)
	}

	globalOffCfg := &Config{
		SDKConfig: SDKConfig{
			ClaudeCodeOnlyEnabled: false,
		},
		APIKeyPolicies: []APIKeyPolicy{
			{APIKey: "k-forced", ClaudeCodeOnly: boolPtr(true)},
		},
	}
	forcedPolicy := globalOffCfg.EffectiveAPIKeyPolicy("k-forced")
	if forcedPolicy == nil {
		t.Fatal("expected forced effective policy")
	}
	if !forcedPolicy.ClaudeCodeOnlyEnabled() {
		t.Fatal("expected per-key true to override global false")
	}
}

func TestSaveConfigPreserveComments_PersistsExplicitFalseClaudeCodeOnlyOverride(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	initial := []byte(`
host: "127.0.0.1"
port: 58080
api-keys:
  - sk-test
claude-code-only-enabled: true
api-key-policies:
  - api-key: sk-test
    enable-claude-models: true
    allow-claude-opus-4-6: true
`)
	if err := os.WriteFile(configPath, initial, 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg := &Config{
		Host:      "127.0.0.1",
		Port:      58080,
		SDKConfig: SDKConfig{APIKeys: []string{"sk-test"}, ClaudeCodeOnlyEnabled: true},
		APIKeyPolicies: []APIKeyPolicy{
			{
				APIKey:             "sk-test",
				EnableClaudeModels: boolPtr(true),
				AllowClaudeOpus46:  boolPtr(true),
				ClaudeCodeOnly:     boolPtr(false),
			},
		},
	}

	if err := SaveConfigPreserveComments(configPath, cfg); err != nil {
		t.Fatalf("SaveConfigPreserveComments() error = %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	if !strings.Contains(string(data), "claude-code-only: false") {
		t.Fatalf("saved config missing explicit false override:\n%s", string(data))
	}
}
