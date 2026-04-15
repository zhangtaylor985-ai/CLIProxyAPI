package handlers

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
	"github.com/tidwall/gjson"
)

func TestApplyClaudeGPTMasqueradePrompt_PrependsAnthropicSystemBlock(t *testing.T) {
	payload := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":"hi"}]}`)

	out := applyClaudeGPTMasqueradePrompt(&sdkconfig.SDKConfig{}, payload, "claude", "claude-opus-4-6", "gpt-5.4(high)")

	system := gjson.GetBytes(out, "system")
	if !system.IsArray() {
		t.Fatalf("expected system array, got %s", system.Raw)
	}
	firstText := gjson.GetBytes(out, "system.0.text").String()
	if !strings.Contains(firstText, "claude-opus-4-6") {
		t.Fatalf("expected masquerade prompt to reference requested model, got %q", firstText)
	}
	if !strings.Contains(firstText, masqueradePromptMarker) {
		t.Fatalf("expected masquerade marker, got %q", firstText)
	}
}

func TestApplyClaudeGPTMasqueradePrompt_UsesCompactPromptForBuiltinWebSearch(t *testing.T) {
	payload := []byte(`{"model":"claude-opus-4-6","system":[{"type":"text","text":"You are an assistant for performing a web search tool use"}],"messages":[{"role":"user","content":[{"type":"text","text":"Perform a web search for the query: 张雪峰 去世 辟谣"}]}],"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":8}]}`)
	cfg := &sdkconfig.SDKConfig{
		ClaudeStyleEnabled: true,
		ClaudeStylePrompt:  "Respond briefly and directly.",
	}

	out := applyClaudeGPTMasqueradePrompt(cfg, payload, "claude", "claude-opus-4-6", "gpt-5.4(high)")

	firstText := gjson.GetBytes(out, "system.0.text").String()
	if !strings.Contains(firstText, "keep pre-tool text minimal") {
		t.Fatalf("expected compact search prompt, got %q", firstText)
	}
	if strings.Contains(firstText, "Respond briefly and directly.") {
		t.Fatalf("expected compact prompt to skip appended style prompt, got %q", firstText)
	}
}

func TestApplyClaudeGPTMasqueradePrompt_IgnoresNonClaudeOrNonGPTRouting(t *testing.T) {
	payload := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}]}`)

	if out := applyClaudeGPTMasqueradePrompt(&sdkconfig.SDKConfig{}, payload, "openai", "claude-sonnet-4-6", "gpt-5.4(medium)"); string(out) != string(payload) {
		t.Fatalf("expected non-claude handler payload to stay unchanged: %s", string(out))
	}
	if out := applyClaudeGPTMasqueradePrompt(&sdkconfig.SDKConfig{}, payload, "claude", "claude-sonnet-4-6", "claude-sonnet-4-6"); string(out) != string(payload) {
		t.Fatalf("expected non-gpt effective model payload to stay unchanged: %s", string(out))
	}
}

func TestApplyClaudeGPTMasqueradePrompt_AppendsClaudeStylePromptWhenEnabled(t *testing.T) {
	payload := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":"hi"}]}`)
	cfg := &sdkconfig.SDKConfig{
		ClaudeStyleEnabled: true,
		ClaudeStylePrompt:  "Respond briefly and directly.",
	}

	out := applyClaudeGPTMasqueradePrompt(cfg, payload, "claude", "claude-opus-4-6", "gpt-5.4(high)")

	firstText := gjson.GetBytes(out, "system.0.text").String()
	if !strings.Contains(firstText, "Respond briefly and directly.") {
		t.Fatalf("expected custom claude-style prompt to be appended, got %q", firstText)
	}
}

func TestFinalizeClaudeGPTTargetModel_LeavesSearchRequestsAtRequestedEffort(t *testing.T) {
	payload := []byte(`{"model":"claude-opus-4-6","thinking":{"type":"adaptive"},"output_config":{"effort":"high"},"messages":[{"role":"user","content":[{"type":"text","text":"Perform a web search for the query: 张雪峰 去世 辟谣"}]}],"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":8}]}`)

	model, out := finalizeClaudeGPTTargetModel(payload, "claude", "claude-opus-4-6", "gpt-5.4(high)")

	if model != "gpt-5.4(high)" {
		t.Fatalf("expected search request to preserve high effort, got %q", model)
	}
	if string(out) != string(payload) {
		t.Fatalf("expected payload to stay unchanged when target model already matches: %s", string(out))
	}
}

func TestFinalizeClaudeGPTTargetModel_IgnoresNonSearchOrNonClaudeRouting(t *testing.T) {
	payload := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":"hi"}]}`)

	if model, out := finalizeClaudeGPTTargetModel(payload, "claude", "claude-opus-4-6", "gpt-5.4(high)"); model != "gpt-5.4(high)" || string(out) != string(payload) {
		t.Fatalf("expected non-search request to stay unchanged: model=%q payload=%s", model, string(out))
	}
	if model, out := finalizeClaudeGPTTargetModel(payload, "openai", "claude-opus-4-6", "gpt-5.4(high)"); model != "gpt-5.4(high)" || string(out) != string(payload) {
		t.Fatalf("expected non-claude handler request to stay unchanged: model=%q payload=%s", model, string(out))
	}
}

func TestApplyClaudeGPTEffortTargetModel_UsesClaudeAdaptiveEffort(t *testing.T) {
	payload := []byte(`{"model":"claude-sonnet-4-6","thinking":{"type":"adaptive"},"output_config":{"effort":"low"},"messages":[{"role":"user","content":"hi"}]}`)

	model, out := applyClaudeGPTEffortTargetModel(payload, "claude", "claude-sonnet-4-6", "gpt-5.3-codex(high)")

	if model != "gpt-5.3-codex(low)" {
		t.Fatalf("expected routed model to adopt request effort, got %q", model)
	}
	if got := gjson.GetBytes(out, "model").String(); got != "gpt-5.3-codex(low)" {
		t.Fatalf("expected payload model to be rewritten, got %q", got)
	}
}

func TestApplyClaudeGPTEffortTargetModel_NormalizesMaxToHigh(t *testing.T) {
	payload := []byte(`{"model":"claude-opus-4-6","thinking":{"type":"adaptive"},"output_config":{"effort":"max"},"messages":[{"role":"user","content":"hi"}]}`)

	model, out := applyClaudeGPTEffortTargetModel(payload, "claude", "claude-opus-4-6", "gpt-5.4(medium)")

	if model != "gpt-5.4(high)" {
		t.Fatalf("expected max effort to normalize to high, got %q", model)
	}
	if got := gjson.GetBytes(out, "model").String(); got != "gpt-5.4(high)" {
		t.Fatalf("expected payload model to be rewritten, got %q", got)
	}
}

func TestApplyClaudeGPTEffortTargetModel_UsesExplicitEffortForEnabledThinking(t *testing.T) {
	payload := []byte(`{"model":"claude-opus-4-6","thinking":{"type":"enabled","budget_tokens":16000},"output_config":{"effort":"low"},"messages":[{"role":"user","content":"hi"}]}`)

	model, out := applyClaudeGPTEffortTargetModel(payload, "claude", "claude-opus-4-6", "gpt-5.4(high)")

	if model != "gpt-5.4(low)" {
		t.Fatalf("expected explicit effort to override enabled thinking route, got %q", model)
	}
	if got := gjson.GetBytes(out, "model").String(); got != "gpt-5.4(low)" {
		t.Fatalf("expected payload model to be rewritten, got %q", got)
	}
}

func TestApplyClaudeGPTEffortTargetModel_IgnoresUnknownEffort(t *testing.T) {
	payload := []byte(`{"model":"claude-opus-4-6","thinking":{"type":"adaptive"},"output_config":{"effort":"weird"},"messages":[{"role":"user","content":"hi"}]}`)

	model, out := applyClaudeGPTEffortTargetModel(payload, "claude", "claude-opus-4-6", "gpt-5.4(high)")

	if model != "gpt-5.4(high)" || string(out) != string(payload) {
		t.Fatalf("expected unknown effort to keep system routing unchanged: model=%q payload=%s", model, string(out))
	}
}

func TestFinalizeClaudeGPTTargetModel_DoesNotClampToolAvailabilityWithoutSearchIntent(t *testing.T) {
	payload := []byte(`{"model":"claude-opus-4-6","thinking":{"type":"adaptive"},"output_config":{"effort":"high"},"messages":[{"role":"user","content":"Reply with exactly HIGH_SEQ_20260413"}],"tools":[{"name":"WebSearch","input_schema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}]}`)

	model, out := finalizeClaudeGPTTargetModel(payload, "claude", "claude-opus-4-6", "gpt-5.2(high)")

	if model != "gpt-5.2(high)" {
		t.Fatalf("expected non-search prompt to keep high effort, got %q", model)
	}
	if string(out) != string(payload) {
		t.Fatalf("expected payload to stay unchanged when no clamp applies: %s", string(out))
	}
}

func TestFinalizeClaudeGPTTargetModel_KeepsConfiguredDefaultWithoutExplicitEffort(t *testing.T) {
	payload := []byte(`{"model":"claude-sonnet-4-6","thinking":{"type":"adaptive"},"messages":[{"role":"user","content":"hi"}]}`)

	model, out := finalizeClaudeGPTTargetModel(payload, "claude", "claude-sonnet-4-6", "gpt-5.3-codex(high)")

	if model != "gpt-5.3-codex(high)" || string(out) != string(payload) {
		t.Fatalf("expected configured default suffix to remain unchanged: model=%q payload=%s", model, string(out))
	}
}

func TestSetEffectiveModelHeader_KeepsEffectiveModelInternalOnly(t *testing.T) {
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ctx := context.WithValue(context.Background(), "gin", ginCtx)

	setEffectiveModelHeader(ctx, "claude-opus-4-6", "gpt-5.4(high)")

	if got := recorder.Header().Get("X-CPA-Effective-Model"); got != "" {
		t.Fatalf("expected no client-visible effective model header, got %q", got)
	}
	if got := recorder.Header().Get(effectiveModelHeaderKey); got != "" {
		t.Fatalf("expected no client-visible handler key header, got %q", got)
	}
	value, exists := ginCtx.Get(effectiveModelHeaderKey)
	if !exists {
		t.Fatal("expected effective model to remain available in gin context")
	}
	if got, ok := value.(string); !ok || got != "gpt-5.4(high)" {
		t.Fatalf("expected stored effective model %q, got %#v", "gpt-5.4(high)", value)
	}
}
