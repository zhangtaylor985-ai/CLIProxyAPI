package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigOptional_SessionTrajectoryDefaultsEnabled(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("debug: false\n"), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}
	if !cfg.SessionTrajectoryEnabled {
		t.Fatal("SessionTrajectoryEnabled = false, want true by default")
	}
}

func TestLoadConfigOptional_SessionTrajectoryAllowsExplicitDisable(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("session-trajectory-enabled: false\n"), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}
	if cfg.SessionTrajectoryEnabled {
		t.Fatal("SessionTrajectoryEnabled = true, want explicit false")
	}
}
