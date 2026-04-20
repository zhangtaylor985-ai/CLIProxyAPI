package auth

import (
	"testing"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestResolveUpstreamModelForClaudeAPIKey_RewritesOpus1MToBase(t *testing.T) {
	cfg := &internalconfig.Config{
		ClaudeKey: []internalconfig.ClaudeKey{
			{
				APIKey:       "key-1",
				BaseURL:      "https://api.anthropic.com",
				OpusBaseOnly: true,
			},
		},
	}
	auth := &Auth{
		Provider: "claude",
		Attributes: map[string]string{
			"api_key":  "key-1",
			"base_url": "https://api.anthropic.com",
		},
	}

	got := resolveUpstreamModelForClaudeAPIKey(cfg, auth, "claude-opus-4-6[1m]")
	if got != "claude-opus-4-6" {
		t.Fatalf("resolveUpstreamModelForClaudeAPIKey() = %q, want %q", got, "claude-opus-4-6")
	}
}

func TestResolveUpstreamModelForClaudeAPIKey_RewritesBeforeAliasLookup(t *testing.T) {
	cfg := &internalconfig.Config{
		ClaudeKey: []internalconfig.ClaudeKey{
			{
				APIKey:       "key-1",
				BaseURL:      "https://api.anthropic.com",
				OpusBaseOnly: true,
				Models: []internalconfig.ClaudeModel{
					{Name: "upstream-opus", Alias: "claude-opus-4-6"},
				},
			},
		},
	}
	auth := &Auth{
		Provider: "claude",
		Attributes: map[string]string{
			"api_key":  "key-1",
			"base_url": "https://api.anthropic.com",
		},
	}

	got := resolveUpstreamModelForClaudeAPIKey(cfg, auth, "claude-opus-4-6[1m]")
	if got != "upstream-opus" {
		t.Fatalf("resolveUpstreamModelForClaudeAPIKey() = %q, want %q", got, "upstream-opus")
	}
}

func TestResolveUpstreamModelForClaudeAPIKey_RewritesOpus47To46(t *testing.T) {
	cfg := &internalconfig.Config{
		ClaudeKey: []internalconfig.ClaudeKey{
			{
				APIKey:       "key-1",
				BaseURL:      "https://api.anthropic.com",
				Opus47To46:   true,
				OpusBaseOnly: true,
			},
		},
	}
	auth := &Auth{
		Provider: "claude",
		Attributes: map[string]string{
			"api_key":  "key-1",
			"base_url": "https://api.anthropic.com",
		},
	}

	got := resolveUpstreamModelForClaudeAPIKey(cfg, auth, "claude-opus-4-7[1m](8192)")
	if got != "claude-opus-4-6(8192)" {
		t.Fatalf("resolveUpstreamModelForClaudeAPIKey() = %q, want %q", got, "claude-opus-4-6(8192)")
	}
}

func TestResolveUpstreamModelForClaudeAPIKey_RewritesOpus47BeforeAliasLookup(t *testing.T) {
	cfg := &internalconfig.Config{
		ClaudeKey: []internalconfig.ClaudeKey{
			{
				APIKey:     "key-1",
				BaseURL:    "https://api.anthropic.com",
				Opus47To46: true,
				Models: []internalconfig.ClaudeModel{
					{Name: "upstream-opus-46", Alias: "claude-opus-4-6"},
				},
			},
		},
	}
	auth := &Auth{
		Provider: "claude",
		Attributes: map[string]string{
			"api_key":  "key-1",
			"base_url": "https://api.anthropic.com",
		},
	}

	got := resolveUpstreamModelForClaudeAPIKey(cfg, auth, "claude-opus-4-7")
	if got != "upstream-opus-46" {
		t.Fatalf("resolveUpstreamModelForClaudeAPIKey() = %q, want %q", got, "upstream-opus-46")
	}
}
