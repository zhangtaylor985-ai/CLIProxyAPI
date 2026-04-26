package management

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestBuildAuthFileEntryIncludesHealthSummaryAndSwitchIdentity(t *testing.T) {
	t.Parallel()

	manager := coreauth.NewManager(nil, nil, nil)
	now := time.Date(2026, 4, 10, 18, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "claude-primary.json")
	if err := os.WriteFile(path, []byte(`{"type":"claude"}`), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	target := &coreauth.Auth{
		ID:       "auth-target",
		Provider: "claude",
		Label:    "backup",
		Attributes: map[string]string{
			"api_key":  "sk-target-key-1234567890",
			"base_url": "https://backup.claudepool.com/",
		},
	}
	if _, err := manager.Register(context.Background(), target); err != nil {
		t.Fatalf("register target auth: %v", err)
	}

	source := &coreauth.Auth{
		ID:       "auth-source",
		Provider: "claude",
		FileName: "claude-primary.json",
		Label:    "primary",
		Attributes: map[string]string{
			"api_key":  "sk-source-key-1234567890",
			"base_url": "https://cc.claudepool.com/",
			"path":     path,
		},
		ModelStates: map[string]*coreauth.ModelState{
			"claude-sonnet-4-5": {
				Status:         coreauth.StatusError,
				StatusMessage:  "provider degraded",
				Unavailable:    true,
				NextRetryAfter: now.Add(5 * time.Minute),
				UpdatedAt:      now,
				Health: coreauth.ProviderHealthState{
					LastFirstActivityMs:   31_000,
					LastCompletedMs:       92_000,
					BackoffLevel:          3,
					LastSwitchAt:          now.Add(-1 * time.Minute),
					LastSwitchToProvider:  "claude",
					LastSwitchToAuthID:    "auth-target",
					LastSwitchToAuthIndex: target.EnsureIndex(),
				},
			},
		},
	}
	if _, err := manager.Register(context.Background(), source); err != nil {
		t.Fatalf("register source auth: %v", err)
	}

	h := &Handler{authManager: manager}
	entry := h.buildAuthFileEntry(source)

	if got := entry["masked_api_key"]; got != "sk-s...7890" {
		t.Fatalf("masked_api_key = %v, want sk-s...7890", got)
	}
	if got := entry["base_url"]; got != "https://cc.claudepool.com/" {
		t.Fatalf("base_url = %v", got)
	}

	summary, ok := entry["health_summary"].(gin.H)
	if !ok {
		t.Fatalf("health_summary type = %T, want gin.H", entry["health_summary"])
	}
	if got := summary["model"]; got != "claude-sonnet-4-5" {
		t.Fatalf("summary.model = %v", got)
	}
	if got := summary["degraded"]; got != true {
		t.Fatalf("summary.degraded = %v, want true", got)
	}
	if got := summary["last_switch_to_auth_index"]; got != target.EnsureIndex() {
		t.Fatalf("summary.last_switch_to_auth_index = %v", got)
	}
	if got := summary["last_switch_to_masked_api_key"]; got != "sk-t...7890" {
		t.Fatalf("summary.last_switch_to_masked_api_key = %v, want sk-t...7890", got)
	}
	if got := summary["last_switch_to_base_url"]; got != "https://backup.claudepool.com/" {
		t.Fatalf("summary.last_switch_to_base_url = %v", got)
	}
}
