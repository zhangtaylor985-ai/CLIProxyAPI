package handlers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

func relayProbeTestContext(t *testing.T, path string, headers map[string]string) context.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest("POST", path, nil)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	c.Request = req
	return context.WithValue(context.Background(), "gin", c)
}

func relayProbePayload(t *testing.T, messages []map[string]any) []byte {
	t.Helper()
	payload := map[string]any{
		"model":    "claude-opus-4-6",
		"messages": messages,
		"metadata": map[string]any{
			"user_id": relayProbeFixedUserID,
		},
		"system": []map[string]any{
			{"type": "text", "text": relayProbeSystemPrompt},
		},
		"max_tokens": 32000,
		"stream":     true,
		"tools":      []any{},
		"thinking": map[string]any{
			"type":          "enabled",
			"budget_tokens": 31999,
		},
	}
	out, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal(payload) error = %v", err)
	}
	return out
}

func anthropicTextMessage(role string, texts ...string) map[string]any {
	content := make([]map[string]any, 0, len(texts))
	for _, text := range texts {
		content = append(content, map[string]any{"type": "text", "text": text})
	}
	return map[string]any{
		"role":    role,
		"content": content,
	}
}

func TestDetectRelayProbeKind(t *testing.T) {
	ctx := relayProbeTestContext(t, "/v1/messages", map[string]string{
		"User-Agent":     "claude-cli/2.1.76 (external, cli)",
		"Anthropic-Beta": "oauth-2025-04-20,interleaved-thinking-2025-05-14",
	})

	stage1 := relayProbePayload(t, []map[string]any{
		anthropicTextMessage("user", relayProbeStage1Prompt),
	})
	if got := detectRelayProbeKind(ctx, "claude", stage1); got != "relayapi_stage1" {
		t.Fatalf("detectRelayProbeKind(stage1) = %q, want %q", got, "relayapi_stage1")
	}

	stage2 := relayProbePayload(t, []map[string]any{
		anthropicTextMessage("user", relayProbeStage1Prompt),
		anthropicTextMessage("assistant", "dummy"),
		anthropicTextMessage("user", relayProbeStage2Prompt),
	})
	if got := detectRelayProbeKind(ctx, "claude", stage2); got != "relayapi_stage2" {
		t.Fatalf("detectRelayProbeKind(stage2) = %q, want %q", got, "relayapi_stage2")
	}

	detector := relayProbePayload(t, []map[string]any{
		anthropicTextMessage("user", "null", "null", relayProbeDetectorPrompt),
	})
	if got := detectRelayProbeKind(ctx, "claude", detector); got != "relayapi_detector" {
		t.Fatalf("detectRelayProbeKind(detector) = %q, want %q", got, "relayapi_detector")
	}

	normal := relayProbePayload(t, []map[string]any{
		anthropicTextMessage("user", "hello"),
	})
	if got := detectRelayProbeKind(ctx, "claude", normal); got != "" {
		t.Fatalf("detectRelayProbeKind(normal) = %q, want empty", got)
	}
}

func TestResolveClaudeProbeTargetAuthID(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	ctx := context.Background()

	if _, err := manager.Register(ctx, &coreauth.Auth{
		ID:       "claude-other",
		Provider: "claude",
		Attributes: map[string]string{
			"api_key":  "other-key",
			"base_url": "https://other.example.com",
		},
	}); err != nil {
		t.Fatalf("Register(other) error = %v", err)
	}
	if _, err := manager.Register(ctx, &coreauth.Auth{
		ID:       "claude-probe",
		Provider: "claude",
		Attributes: map[string]string{
			"api_key":      "probe-key",
			"base_url":     "https://boomai.cloud",
			"probe_target": "true",
		},
	}); err != nil {
		t.Fatalf("Register(probe) error = %v", err)
	}

	handler := &BaseAPIHandler{
		AuthManager: manager,
		Cfg:         &sdkconfig.SDKConfig{},
	}

	if got := handler.resolveClaudeProbeTargetAuthID("claude-opus-4-6"); got != "claude-probe" {
		t.Fatalf("resolveClaudeProbeTargetAuthID() = %q, want %q", got, "claude-probe")
	}
}

func TestApplyRelayProbePin(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "claude-probe",
		Provider: "claude",
		Attributes: map[string]string{
			"api_key":      "probe-key",
			"base_url":     "https://boomai.cloud",
			"probe_target": "true",
		},
	}); err != nil {
		t.Fatalf("Register(probe) error = %v", err)
	}

	handler := &BaseAPIHandler{
		AuthManager: manager,
		Cfg:         &sdkconfig.SDKConfig{},
	}

	ctx := relayProbeTestContext(t, "/v1/messages", map[string]string{
		"User-Agent":     "claude-cli/2.1.76 (external, cli)",
		"Anthropic-Beta": "oauth-2025-04-20,interleaved-thinking-2025-05-14",
	})
	raw := relayProbePayload(t, []map[string]any{
		anthropicTextMessage("user", relayProbeStage1Prompt),
	})
	meta := map[string]any{}

	got := handler.applyRelayProbePin(ctx, "claude", raw, []string{"claude"}, "claude-opus-4-6", meta)
	if got != "claude-probe" {
		t.Fatalf("applyRelayProbePin() = %q, want %q", got, "claude-probe")
	}
	if meta[coreexecutor.PinnedAuthMetadataKey] != "claude-probe" {
		t.Fatalf("PinnedAuthMetadataKey = %v, want %q", meta[coreexecutor.PinnedAuthMetadataKey], "claude-probe")
	}
	if meta[relayProbeMetadataLabelKey] != "relayapi_stage1" {
		t.Fatalf("relayProbeMetadataLabelKey = %v, want %q", meta[relayProbeMetadataLabelKey], "relayapi_stage1")
	}
	if meta[relayProbePinnedMetadataKey] != "claude-probe" {
		t.Fatalf("relayProbePinnedMetadataKey = %v, want %q", meta[relayProbePinnedMetadataKey], "claude-probe")
	}
}
