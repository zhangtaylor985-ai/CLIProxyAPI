package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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
		"base_url": server.URL + "/v1",
		"api_key":  "worker-key",
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
		`{"prompt_tokens":20883,"completion_tokens":1,"total_tokens":39316,"prompt_tokens_details":{"cached_tokens":18432}}`,
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
		"base_url": server.URL + "/v1",
		"api_key":  "worker-key",
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
	if keys[0] != keys[1] {
		t.Fatalf("first cached observation should not affect the in-flight request key: %q != %q", keys[0], keys[1])
	}
	if keys[2] == keys[1] {
		t.Fatalf("expected third request to use rolled cache key after cached prefix growth, still %q", keys[2])
	}
	if keys[3] != keys[2] || keys[4] != keys[2] {
		t.Fatalf("cache key rolled too frequently after small growth: keys=%q", keys)
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
