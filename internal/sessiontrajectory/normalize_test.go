package sessiontrajectory

import (
	"encoding/json"
	"testing"
)

func TestMessagesPrefixMatch(t *testing.T) {
	previous := []byte(`[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"}]`)
	current := []byte(`[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"},{"role":"user","content":"next"}]`)
	if !messagesPrefixMatch(previous, current) {
		t.Fatal("expected previous messages to match current prefix")
	}
}

func TestMessagesPrefixMatchAnthropicStyle(t *testing.T) {
	previous := []byte(`[{"role":"user","content":[{"type":"text","text":"hello"}]}]`)
	current := []byte(`[{"role":"user","content":[{"text":"hello","type":"text"}]},{"role":"assistant","content":[{"type":"text","text":"hi"}]},{"role":"user","content":[{"type":"text","text":"continue"}]}]`)
	if !messagesPrefixMatch(previous, current) {
		t.Fatal("expected anthropic-style messages to match current prefix")
	}
}

func TestAppendAssistantResponseToMessages(t *testing.T) {
	previous := []byte(`[{"role":"user","content":[{"type":"text","text":"hello"}]}]`)
	response := []byte(`{"id":"msg_1","role":"assistant","content":[{"type":"text","text":"hi"}]}`)
	current := []byte(`[{"role":"user","content":[{"type":"text","text":"hello"}]},{"role":"assistant","content":[{"type":"text","text":"hi"}]},{"role":"user","content":[{"type":"text","text":"continue"}]}]`)

	augmented := appendAssistantResponseToMessages("anthropic_messages", previous, response)
	if len(augmented) == 0 {
		t.Fatal("expected augmented messages")
	}
	if !messagesPrefixMatch(augmented, current) {
		t.Fatalf("expected augmented messages to match current prefix: %s", string(augmented))
	}
}

func TestExtractProviderSessionIDFromMetadataUserID(t *testing.T) {
	request := []byte(`{"metadata":{"user_id":"user_x_session_abc123"},"messages":[{"role":"user","content":"hi"}]}`)
	normalized, _, _, _, err := normalizeCompletedRequest(&CompletedRequest{
		RequestURL: "/v1/messages",
		RequestHeaders: map[string][]string{
			"User-Agent": {"claude-cli/1.0"},
		},
		RequestBody: request,
	})
	if err != nil {
		t.Fatalf("normalizeCompletedRequest: %v", err)
	}
	if normalized == nil {
		t.Fatal("expected normalized conversation")
	}
	if normalized.ProviderSessionID != "abc123" {
		t.Fatalf("provider session id = %q, want %q", normalized.ProviderSessionID, "abc123")
	}
	if normalized.Source != "claude-cli" {
		t.Fatalf("source = %q, want %q", normalized.Source, "claude-cli")
	}
}

func TestExtractProviderSessionIDFromStructuredMetadataUserID(t *testing.T) {
	request := []byte(`{"metadata":{"user_id":"{\"device_id\":\"abc\",\"session_id\":\"a8407cb7-6080-49a1-89cb-0d590e0506bb\"}"},"messages":[{"role":"user","content":"hi"}]}`)
	normalized, _, _, _, err := normalizeCompletedRequest(&CompletedRequest{
		RequestURL:  "/v1/messages",
		RequestBody: request,
	})
	if err != nil {
		t.Fatalf("normalizeCompletedRequest: %v", err)
	}
	if normalized == nil {
		t.Fatal("expected normalized conversation")
	}
	if normalized.ProviderSessionID != "a8407cb7-6080-49a1-89cb-0d590e0506bb" {
		t.Fatalf("provider session id = %q, want structured session id", normalized.ProviderSessionID)
	}
}

func TestNormalizeCompletedRequestExtractsEmbeddedProviderRequestIDFromErrorMessage(t *testing.T) {
	normalized, _, _, _, err := normalizeCompletedRequest(&CompletedRequest{
		RequestURL:   "/v1/chat/completions",
		RequestBody:  []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}]}`),
		ResponseBody: []byte(`{"error":{"type":"invalid_request_error","message":"thinking is enabled but reasoning_content is missing in assistant tool call message at index 6 (request id: 20260426085751352687748KdidbVim)"},"type":"error"}`),
	})
	if err != nil {
		t.Fatalf("normalizeCompletedRequest: %v", err)
	}
	if normalized == nil {
		t.Fatal("expected normalized conversation")
	}
	if normalized.ProviderRequestID != "20260426085751352687748KdidbVim" {
		t.Fatalf("provider request id = %q", normalized.ProviderRequestID)
	}
}

func TestNormalizeCompletedRequestCompactsAnthropicStreamResponse(t *testing.T) {
	response := []byte("event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"gpt-5.4\",\"content\":[],\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":12,\"output_tokens\":0}}}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"FIRST_OK\"}}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":7}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n")
	normalized, _, responseJSON, _, err := normalizeCompletedRequest(&CompletedRequest{
		RequestURL:     "/v1/messages",
		RequestBody:    []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":[{"type":"text","text":"Reply with exactly FIRST_OK"}]}]}`),
		ResponseBody:   response,
		RequestHeaders: map[string][]string{"User-Agent": {"claude-cli/2.1.92 (external, sdk-cli)"}},
	})
	if err != nil {
		t.Fatalf("normalizeCompletedRequest: %v", err)
	}
	if normalized == nil {
		t.Fatal("expected normalized conversation")
	}
	if normalized.Usage.InputTokens != 12 || normalized.Usage.OutputTokens != 7 || normalized.Usage.TotalTokens != 19 {
		t.Fatalf("usage = %+v, want input=12 output=7 total=19", normalized.Usage)
	}

	var payload map[string]any
	if err := json.Unmarshal(responseJSON, &payload); err != nil {
		t.Fatalf("unmarshal compacted response: %v", err)
	}
	if payload["id"] != "msg_1" {
		t.Fatalf("response id = %v, want msg_1", payload["id"])
	}
	content, ok := payload["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("content = %#v, want single item", payload["content"])
	}
	block, ok := content[0].(map[string]any)
	if !ok || block["text"] != "FIRST_OK" {
		t.Fatalf("content block = %#v, want FIRST_OK text", content[0])
	}
}
