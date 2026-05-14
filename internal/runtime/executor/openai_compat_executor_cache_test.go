package executor

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestOpenAICompatExecutorCodexWorkerInjectsSessionPromptCacheKey(t *testing.T) {
	resetCodexPromptCacheForTest(t)

	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("codex-worker99-test", &config.Config{})
	auth := &cliproxyauth.Auth{ID: "codex-worker99-test", Attributes: map[string]string{
		"base_url":                   server.URL + "/v1",
		"api_key":                    "worker-key",
		"codex_worker_claude_direct": "false",
	}}

	run := func(userID string) {
		t.Helper()
		payload := []byte(`{"model":"claude-opus-4-7","max_tokens":32,"metadata":{"user_id":"` + userID + `"},"messages":[{"role":"user","content":"hi"}]}`)
		_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
			Model:   "gpt-5.4(medium)",
			Payload: payload,
		}, cliproxyexecutor.Options{
			SourceFormat: sdktranslator.FromString("claude"),
			Stream:       false,
		})
		if err != nil {
			t.Fatalf("Execute error: %v", err)
		}
	}

	run("session-a")
	run("session-b")

	if len(bodies) != 2 {
		t.Fatalf("recorded bodies = %d, want 2", len(bodies))
	}
	keyA := gjson.GetBytes(bodies[0], "prompt_cache_key").String()
	keyB := gjson.GetBytes(bodies[1], "prompt_cache_key").String()
	if keyA == "" || keyB == "" {
		t.Fatalf("expected prompt_cache_key for codex worker, got %q and %q", keyA, keyB)
	}
	if keyA == keyB {
		t.Fatalf("different Claude sessions shared prompt_cache_key %q", keyA)
	}
}

func TestOpenAICompatExecutorCodexWorkerRollsSessionPromptCacheKeyAfterCachedGrowth(t *testing.T) {
	resetCodexPromptCacheForTest(t)

	var bodies [][]byte
	usages := []string{
		`{"prompt_tokens":30368,"completion_tokens":1,"total_tokens":30369,"prompt_tokens_details":{"cached_tokens":0}}`,
		`{"prompt_tokens":12917,"completion_tokens":1,"total_tokens":31350,"prompt_tokens_details":{"cached_tokens":18432}}`,
		`{"prompt_tokens":16001,"completion_tokens":1,"total_tokens":34434,"prompt_tokens_details":{"cached_tokens":18432}}`,
		`{"prompt_tokens":20022,"completion_tokens":1,"total_tokens":38455,"prompt_tokens_details":{"cached_tokens":18432}}`,
		`{"prompt_tokens":20883,"completion_tokens":1,"total_tokens":59796,"prompt_tokens_details":{"cached_tokens":38912}}`,
		`{"prompt_tokens":21000,"completion_tokens":1,"total_tokens":59913,"prompt_tokens_details":{"cached_tokens":38912}}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, body)
		idx := len(bodies) - 1
		if idx >= len(usages) {
			idx = len(usages) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":` + usages[idx] + `}`))
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("codex-worker99-test", &config.Config{})
	auth := &cliproxyauth.Auth{ID: "codex-worker99-test", Attributes: map[string]string{
		"base_url":                   server.URL + "/v1",
		"api_key":                    "worker-key",
		"codex_worker_claude_direct": "false",
	}}
	payload := []byte(`{"model":"claude-opus-4-7","max_tokens":32,"metadata":{"user_id":"session-a"},"messages":[{"role":"user","content":"hi"}]}`)

	for i := 0; i < len(usages); i++ {
		_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
			Model:   "gpt-5.4(medium)",
			Payload: payload,
		}, cliproxyexecutor.Options{
			SourceFormat: sdktranslator.FromString("claude"),
			Stream:       false,
		})
		if err != nil {
			t.Fatalf("Execute #%d error: %v", i+1, err)
		}
	}

	if len(bodies) != len(usages) {
		t.Fatalf("recorded bodies = %d, want %d", len(bodies), len(usages))
	}
	keys := make([]string, len(bodies))
	for i := range bodies {
		keys[i] = gjson.GetBytes(bodies[i], "prompt_cache_key").String()
		if keys[i] == "" {
			t.Fatalf("request #%d missing prompt_cache_key: %s", i+1, string(bodies[i]))
		}
	}
	for i := 1; i <= 4; i++ {
		if keys[i] != keys[0] {
			t.Fatalf("cache key rolled before cached prefix grew enough: keys=%q", keys)
		}
	}
	if keys[5] == keys[0] {
		t.Fatalf("expected request after cached prefix growth to use rolled cache key, keys=%q", keys)
	}
}

func TestOpenAICompatExecutorCodexWorkerUsesClaudeMessagesEndpointByDefault(t *testing.T) {
	resetCodexPromptCacheForTest(t)

	var gotPath string
	var gotAuth string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"gpt-5.4","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":128,"cache_read_input_tokens":64,"output_tokens":2}}`))
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("codex-worker99-test", &config.Config{})
	auth := &cliproxyauth.Auth{ID: "codex-worker99-test", Attributes: map[string]string{
		"base_url": server.URL + "/v1",
		"api_key":  "worker-key",
	}}
	payload := []byte(`{"model":"claude-opus-4-7","max_tokens":32,"metadata":{"user_id":"session-a"},"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
	resp, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "gpt-5.4(medium)",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
		Stream:       false,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if gotPath != "/v1/messages" {
		t.Fatalf("path = %q, want /v1/messages", gotPath)
	}
	if gotAuth != "Bearer worker-key" {
		t.Fatalf("Authorization = %q, want worker bearer", gotAuth)
	}
	if model := gjson.GetBytes(gotBody, "model").String(); model != "gpt-5.4(medium)" {
		t.Fatalf("worker Claude body model = %q, want routed model", model)
	}
	if stream := gjson.GetBytes(gotBody, "stream").Bool(); stream {
		t.Fatalf("non-stream direct request unexpectedly set stream=true: %s", string(gotBody))
	}
	if gjson.GetBytes(gotBody, "prompt_cache_key").Exists() {
		t.Fatalf("direct worker Claude request should let worker own prompt_cache_key, body=%s", string(gotBody))
	}
	if gjson.GetBytes(gotBody, "messages.0.content.0.text").String() != "hi" {
		t.Fatalf("direct worker Claude request did not preserve Claude message shape: %s", string(gotBody))
	}
	if got := gjson.GetBytes(resp.Payload, "usage.cache_read_input_tokens").Int(); got != 64 {
		t.Fatalf("response payload cache_read_input_tokens = %d, want 64; payload=%s", got, string(resp.Payload))
	}
}

func TestOpenAICompatExecutorCodexWorkerDirectClaudeStreamPassesThroughSSE(t *testing.T) {
	resetCodexPromptCacheForTest(t)

	var gotPath string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: message_start\n"))
		_, _ = w.Write([]byte(`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"gpt-5.4","content":[],"usage":{"input_tokens":0,"output_tokens":0},"stop_reason":null}}` + "\n\n"))
		_, _ = w.Write([]byte("event: content_block_delta\n"))
		_, _ = w.Write([]byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}` + "\n\n"))
		_, _ = w.Write([]byte("event: message_delta\n"))
		_, _ = w.Write([]byte(`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":256,"cache_read_input_tokens":128,"output_tokens":3}}` + "\n\n"))
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("codex-worker99-test", &config.Config{})
	auth := &cliproxyauth.Auth{ID: "codex-worker99-test", Attributes: map[string]string{
		"base_url": server.URL + "/v1",
		"api_key":  "worker-key",
	}}
	payload := []byte(`{"model":"claude-opus-4-7","max_tokens":32,"metadata":{"user_id":"session-a"},"messages":[{"role":"user","content":"hi"}]}`)
	streamResult, err := executor.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "gpt-5.4(high)",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
		Stream:       true,
	})
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	var gotStream bytes.Buffer
	for chunk := range streamResult.Chunks {
		if chunk.Err != nil {
			t.Fatalf("unexpected stream chunk error: %v", chunk.Err)
		}
		gotStream.Write(chunk.Payload)
	}

	if gotPath != "/v1/messages" {
		t.Fatalf("path = %q, want /v1/messages", gotPath)
	}
	if model := gjson.GetBytes(gotBody, "model").String(); model != "gpt-5.4(high)" {
		t.Fatalf("worker Claude stream body model = %q, want routed model", model)
	}
	if !gjson.GetBytes(gotBody, "stream").Bool() {
		t.Fatalf("stream direct request did not set stream=true: %s", string(gotBody))
	}
	if strings.Contains(gotStream.String(), "chat.completion") {
		t.Fatalf("direct worker Claude stream should not translate through OpenAI chat: %s", gotStream.String())
	}
	if !strings.Contains(gotStream.String(), "event: message_delta") || !strings.Contains(gotStream.String(), `"cache_read_input_tokens":128`) {
		t.Fatalf("direct worker Claude stream did not pass through Claude SSE usage: %s", gotStream.String())
	}
}

func TestOpenAICompatExecutorNonCodexWorkerDoesNotInjectPromptCacheKey(t *testing.T) {
	resetCodexPromptCacheForTest(t)

	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("generic-openai", &config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"base_url": server.URL + "/v1",
		"api_key":  "provider-key",
	}}
	payload := []byte(`{"model":"claude-opus-4-7","max_tokens":32,"metadata":{"user_id":"session-a"},"messages":[{"role":"user","content":"hi"}]}`)
	_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "gpt-5.4(medium)",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
		Stream:       false,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if gjson.GetBytes(gotBody, "prompt_cache_key").Exists() {
		t.Fatalf("generic provider should not receive prompt_cache_key, body=%s", string(gotBody))
	}
}

func resetCodexPromptCacheForTest(t *testing.T) {
	t.Helper()
	codexCacheMu.Lock()
	codexCacheMap = make(map[string]codexCache)
	codexCacheMu.Unlock()
}
