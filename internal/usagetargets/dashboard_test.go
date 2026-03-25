package usagetargets

import (
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/billing"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestBuild(t *testing.T) {
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	cfg := &config.Config{
		GeminiKey: []config.GeminiKey{
			{APIKey: "AIzaGeminiKey12345678", Prefix: "gemini-prefix"},
		},
		CodexKey: []config.CodexKey{
			{APIKey: "sk-codexkey123456"},
		},
		ClaudeKey: []config.ClaudeKey{
			{APIKey: "sk-ant-claudekey123456"},
		},
		VertexCompatAPIKey: []config.VertexCompatKey{
			{APIKey: "AIzaVertexKey12345678", Prefix: "vertex-prefix"},
		},
		OpenAICompatibility: []config.OpenAICompatibility{
			{
				Name:    "provider-a",
				Prefix:  "openai-prefix",
				BaseURL: "https://example.com",
				APIKeyEntries: []config.OpenAICompatibilityAPIKey{
					{APIKey: "sk-openaikey123456"},
					{APIKey: "sk-openaiother123456"},
				},
			},
		},
	}

	dailyRows := []billing.DailyUsageRow{
		{APIKey: "AIzaGeminiKey12345678", Requests: 10, FailedRequests: 2},
		{APIKey: "sk-openaikey123456", Requests: 4, FailedRequests: 1},
		{APIKey: "sk-openaiother123456", Requests: 3, FailedRequests: 0},
	}

	aggregateRows := []billing.UsageEventAggregateRow{
		{Source: "auth-file.json", AuthIndex: "7", SuccessCount: 3, FailureCount: 1},
		{Source: "auth-file", SuccessCount: 1, FailureCount: 0},
	}

	recentRows := []billing.UsageEventRow{
		{RequestedAt: now.Add(-5 * time.Minute).Unix(), Source: "AIzaGeminiKey12345678", Model: "gpt-5.4"},
		{RequestedAt: now.Add(-15 * time.Minute).Unix(), Source: "auth-file.json", AuthIndex: "7", Model: "gpt-5.4", Failed: true},
		{RequestedAt: now.Add(-25 * time.Minute).Unix(), Source: "auth-file.json", AuthIndex: "7", Model: "gpt-5.4"},
	}

	dashboard := Build(cfg, dailyRows, aggregateRows, recentRows, now)

	if len(dashboard.Providers.Gemini) != 1 {
		t.Fatalf("gemini stats=%d", len(dashboard.Providers.Gemini))
	}
	if dashboard.Providers.Gemini[0].SuccessCount != 8 || dashboard.Providers.Gemini[0].FailureCount != 2 {
		t.Fatalf("gemini=%+v", dashboard.Providers.Gemini[0])
	}
	if dashboard.Providers.Gemini[0].StatusBar.TotalSuccess != 1 || dashboard.Providers.Gemini[0].StatusBar.TotalFailure != 0 {
		t.Fatalf("gemini status=%+v", dashboard.Providers.Gemini[0].StatusBar)
	}

	if len(dashboard.Providers.OpenAI) != 1 {
		t.Fatalf("openai stats=%d", len(dashboard.Providers.OpenAI))
	}
	if dashboard.Providers.OpenAI[0].SuccessCount != 6 || dashboard.Providers.OpenAI[0].FailureCount != 1 {
		t.Fatalf("openai=%+v", dashboard.Providers.OpenAI[0])
	}
	if len(dashboard.AuthFiles.ByAuthIndex) != 1 {
		t.Fatalf("auth by index=%d", len(dashboard.AuthFiles.ByAuthIndex))
	}

	authByIndex := dashboard.AuthFiles.ByAuthIndex["7"]
	if authByIndex.SuccessCount != 3 || authByIndex.FailureCount != 1 {
		t.Fatalf("authByIndex=%+v", authByIndex)
	}
	if authByIndex.StatusBar.TotalSuccess != 1 || authByIndex.StatusBar.TotalFailure != 1 {
		t.Fatalf("auth status=%+v", authByIndex.StatusBar)
	}

	authBySource := dashboard.AuthFiles.BySource["auth-file"]
	if authBySource.SuccessCount != 4 || authBySource.FailureCount != 1 {
		t.Fatalf("authBySource=%+v", authBySource)
	}
}
