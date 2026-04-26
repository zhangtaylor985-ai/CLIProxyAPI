package executor

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestNormalizeOpenAICompatThinkingToolCallsAddsMissingReasoning(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-6",
		"reasoning_effort":"high",
		"messages":[
			{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"list","arguments":"{}"}}]}
		]
	}`)

	out, err := normalizeOpenAICompatThinkingToolCalls(body)
	if err != nil {
		t.Fatalf("normalizeOpenAICompatThinkingToolCalls() error = %v", err)
	}

	got := gjson.GetBytes(out, "messages.0.reasoning_content").String()
	if got != "[reasoning unavailable]" {
		t.Fatalf("reasoning_content = %q, want fallback", got)
	}
}

func TestNormalizeOpenAICompatThinkingToolCallsSkipsWhenReasoningDisabled(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-6",
		"reasoning_effort":"none",
		"messages":[
			{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"list","arguments":"{}"}}]}
		]
	}`)

	out, err := normalizeOpenAICompatThinkingToolCalls(body)
	if err != nil {
		t.Fatalf("normalizeOpenAICompatThinkingToolCalls() error = %v", err)
	}
	if gjson.GetBytes(out, "messages.0.reasoning_content").Exists() {
		t.Fatalf("reasoning_content should be absent when reasoning is disabled: %s", string(out))
	}
}

func TestNormalizeOpenAICompatThinkingToolCallsInheritsLatestReasoning(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-6",
		"reasoning":{"effort":"high"},
		"messages":[
			{"role":"assistant","content":"plan","reasoning_content":"previous reasoning"},
			{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"list","arguments":"{}"}}]}
		]
	}`)

	out, err := normalizeOpenAICompatThinkingToolCalls(body)
	if err != nil {
		t.Fatalf("normalizeOpenAICompatThinkingToolCalls() error = %v", err)
	}
	if got := gjson.GetBytes(out, "messages.1.reasoning_content").String(); got != "previous reasoning" {
		t.Fatalf("reasoning_content = %q, want inherited reasoning", got)
	}
}
