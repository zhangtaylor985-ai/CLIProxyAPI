package executor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
)

func TestParseOpenAICompatRetryAfterFromHeaderSeconds(t *testing.T) {
	headers := http.Header{"Retry-After": []string{"42"}}
	got := parseOpenAICompatRetryAfter(http.StatusTooManyRequests, headers, nil, time.Now())
	if got == nil {
		t.Fatal("expected retryAfter, got nil")
	}
	if *got != 42*time.Second {
		t.Fatalf("retryAfter = %v, want 42s", *got)
	}
}

func TestParseOpenAICompatRetryAfterFromModelCooldownBody(t *testing.T) {
	body := []byte(`{"error":{"code":"model_cooldown","message":"All credentials are cooling down","reset_seconds":298294}}`)
	got := parseOpenAICompatRetryAfter(http.StatusTooManyRequests, nil, body, time.Now())
	if got == nil {
		t.Fatal("expected retryAfter, got nil")
	}
	if *got != 298294*time.Second {
		t.Fatalf("retryAfter = %v, want 298294s", *got)
	}
}

func TestParseOpenAICompatRetryAfterFromResetTimestamp(t *testing.T) {
	now := time.Unix(1000, 0)
	body := []byte(`{"error":{"resets_at":1123}}`)
	got := parseOpenAICompatRetryAfter(http.StatusTooManyRequests, nil, body, now)
	if got == nil {
		t.Fatal("expected retryAfter, got nil")
	}
	if *got != 123*time.Second {
		t.Fatalf("retryAfter = %v, want 123s", *got)
	}
}

func TestParseOpenAICompatRetryAfterIgnoresNonCooldownStatus(t *testing.T) {
	headers := http.Header{"Retry-After": []string{"42"}}
	if got := parseOpenAICompatRetryAfter(http.StatusInternalServerError, headers, nil, time.Now()); got != nil {
		t.Fatalf("retryAfter = %v, want nil", *got)
	}
}

func TestParseOpenAICompatStreamErrorFromErrorPayload(t *testing.T) {
	err, ok := parseOpenAICompatStreamError([]byte(`data: {"error":{"message":"empty_stream: upstream stream closed before first payload","type":"server_error","code":"internal_server_error"}}`))
	if !ok {
		t.Fatal("expected stream error")
	}
	var status statusErr
	if !errors.As(err, &status) {
		t.Fatalf("error type = %T, want statusErr", err)
	}
	if status.StatusCode() != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", status.StatusCode())
	}
	if !strings.Contains(err.Error(), "empty_stream") {
		t.Fatalf("error = %q, want empty_stream marker", err.Error())
	}
}

func TestParseOpenAICompatStreamErrorFromTypedErrorPayload(t *testing.T) {
	err, ok := parseOpenAICompatStreamError([]byte(`data: {"type":"error","status_code":429,"error":{"message":"service temporarily unavailable"}}`))
	if !ok {
		t.Fatal("expected stream error")
	}
	var status statusErr
	if !errors.As(err, &status) {
		t.Fatalf("error type = %T, want statusErr", err)
	}
	if status.StatusCode() != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", status.StatusCode())
	}
}

func TestParseOpenAICompatStreamErrorIgnoresNormalChunk(t *testing.T) {
	lines := [][]byte{
		[]byte(`data: {"id":"chatcmpl-test","object":"chat.completion.chunk","choices":[{"delta":{"content":"ok"}}]}`),
		[]byte(`data: [DONE]`),
		[]byte(`event: ping`),
	}
	for _, line := range lines {
		if err, ok := parseOpenAICompatStreamError(line); ok {
			t.Fatalf("line %q parsed as error %v", string(line), err)
		}
	}
}

func TestOpenAICompatExecuteStreamErrorFrameEmitsChunkError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"error\":{\"message\":\"empty_stream: upstream stream closed before first payload\",\"type\":\"server_error\"}}\n\n"))
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("codex-worker-test", &config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"base_url": server.URL + "/v1",
		"api_key":  "test-key",
	}}

	result, err := executor.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "gpt-5.4(medium)",
		Payload: []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}],"stream":true}`),
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai"), Stream: true})
	if err != nil {
		t.Fatalf("ExecuteStream returned setup error: %v", err)
	}

	chunk, ok := <-result.Chunks
	if !ok {
		t.Fatal("expected error chunk, got closed channel")
	}
	if chunk.Err == nil {
		t.Fatalf("chunk.Err = nil, payload = %s", string(chunk.Payload))
	}
	if !strings.Contains(chunk.Err.Error(), "empty_stream") {
		t.Fatalf("chunk.Err = %q, want empty_stream marker", chunk.Err.Error())
	}
}
