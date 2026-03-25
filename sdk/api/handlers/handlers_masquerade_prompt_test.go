package handlers

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestApplyClaudeGPTMasqueradePrompt_PrependsAnthropicSystemBlock(t *testing.T) {
	payload := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":"hi"}]}`)

	out := applyClaudeGPTMasqueradePrompt(payload, "claude", "claude-opus-4-6", "gpt-5.4(high)")

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

func TestApplyClaudeGPTMasqueradePrompt_IgnoresNonClaudeOrNonGPTRouting(t *testing.T) {
	payload := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}]}`)

	if out := applyClaudeGPTMasqueradePrompt(payload, "openai", "claude-sonnet-4-6", "gpt-5.4(medium)"); string(out) != string(payload) {
		t.Fatalf("expected non-claude handler payload to stay unchanged: %s", string(out))
	}
	if out := applyClaudeGPTMasqueradePrompt(payload, "claude", "claude-sonnet-4-6", "claude-sonnet-4-6"); string(out) != string(payload) {
		t.Fatalf("expected non-gpt effective model payload to stay unchanged: %s", string(out))
	}
}
