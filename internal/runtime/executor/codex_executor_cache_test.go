package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestCodexExecutorCacheHelper_OpenAIChatCompletions_StablePromptCacheKeyFromAPIKey(t *testing.T) {
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Set("apiKey", "test-api-key")

	ctx := context.WithValue(context.Background(), "gin", ginCtx)
	executor := &CodexExecutor{}
	rawJSON := []byte(`{"model":"gpt-5.3-codex","stream":true}`)
	req := cliproxyexecutor.Request{
		Model:   "gpt-5.3-codex",
		Payload: []byte(`{"model":"gpt-5.3-codex"}`),
	}
	url := "https://example.com/responses"

	httpReq, err := executor.cacheHelper(ctx, nil, sdktranslator.FromString("openai"), url, req, rawJSON)
	if err != nil {
		t.Fatalf("cacheHelper error: %v", err)
	}

	body, errRead := io.ReadAll(httpReq.Body)
	if errRead != nil {
		t.Fatalf("read request body: %v", errRead)
	}

	expectedScope := codexScopedCacheKey(nil, "openai", "gpt-5.3-codex", "test-api-key")
	expectedKey := uuid.NewSHA1(uuid.NameSpaceOID, []byte("cli-proxy-api:codex:prompt-cache:"+expectedScope)).String()
	gotKey := gjson.GetBytes(body, "prompt_cache_key").String()
	if gotKey != expectedKey {
		t.Fatalf("prompt_cache_key = %q, want %q", gotKey, expectedKey)
	}
	if gotConversation := httpReq.Header.Get("Conversation_id"); gotConversation != expectedKey {
		t.Fatalf("Conversation_id = %q, want %q", gotConversation, expectedKey)
	}
	if gotSession := httpReq.Header.Get("Session_id"); gotSession != expectedKey {
		t.Fatalf("Session_id = %q, want %q", gotSession, expectedKey)
	}

	httpReq2, err := executor.cacheHelper(ctx, nil, sdktranslator.FromString("openai"), url, req, rawJSON)
	if err != nil {
		t.Fatalf("cacheHelper error (second call): %v", err)
	}
	body2, errRead2 := io.ReadAll(httpReq2.Body)
	if errRead2 != nil {
		t.Fatalf("read request body (second call): %v", errRead2)
	}
	gotKey2 := gjson.GetBytes(body2, "prompt_cache_key").String()
	if gotKey2 != expectedKey {
		t.Fatalf("prompt_cache_key (second call) = %q, want %q", gotKey2, expectedKey)
	}
}

func TestCodexExecutorCacheHelper_ClaudePromptCacheKeyIsScopedByAuth(t *testing.T) {
	executor := &CodexExecutor{}
	rawJSON := []byte(`{"model":"gpt-5-codex","stream":true}`)
	req := cliproxyexecutor.Request{
		Model:   "gpt-5-codex",
		Payload: []byte(`{"metadata":{"user_id":"shared-user-for-auth-scope-test"}}`),
	}
	url := "https://example.com/responses"
	authA := &cliproxyauth.Auth{ID: "codex-a", Provider: "codex", ProxyURL: "http://127.0.0.1:18081"}
	authB := &cliproxyauth.Auth{ID: "codex-b", Provider: "codex", ProxyURL: "http://127.0.0.1:18082"}

	reqA1, err := executor.cacheHelper(context.Background(), authA, sdktranslator.FromString("claude"), url, req, rawJSON)
	if err != nil {
		t.Fatalf("cacheHelper auth A first call error: %v", err)
	}
	reqA2, err := executor.cacheHelper(context.Background(), authA, sdktranslator.FromString("claude"), url, req, rawJSON)
	if err != nil {
		t.Fatalf("cacheHelper auth A second call error: %v", err)
	}
	reqB, err := executor.cacheHelper(context.Background(), authB, sdktranslator.FromString("claude"), url, req, rawJSON)
	if err != nil {
		t.Fatalf("cacheHelper auth B error: %v", err)
	}

	keyA1 := promptCacheKeyFromRequest(t, reqA1)
	keyA2 := promptCacheKeyFromRequest(t, reqA2)
	keyB := promptCacheKeyFromRequest(t, reqB)
	if keyA1 == "" || keyB == "" {
		t.Fatalf("expected non-empty cache keys, got authA=%q authB=%q", keyA1, keyB)
	}
	if keyA1 != keyA2 {
		t.Fatalf("same auth cache key changed: first=%q second=%q", keyA1, keyA2)
	}
	if keyA1 == keyB {
		t.Fatalf("different auths shared prompt_cache_key %q", keyA1)
	}
}

func TestCodexExecutorCacheHelper_ClaudePromptCacheKeyUsesBaseModel(t *testing.T) {
	executor := &CodexExecutor{}
	rawJSON := []byte(`{"model":"gpt-5.4","stream":true}`)
	url := "https://example.com/responses"
	auth := &cliproxyauth.Auth{ID: "codex-a", Provider: "codex", ProxyURL: "http://127.0.0.1:18081"}

	reqHigh := cliproxyexecutor.Request{
		Model:   "gpt-5.4(high)",
		Payload: []byte(`{"metadata":{"user_id":"same-user-base-model-cache"}}`),
	}
	reqMedium := cliproxyexecutor.Request{
		Model:   "gpt-5.4(medium)",
		Payload: []byte(`{"metadata":{"user_id":"same-user-base-model-cache"}}`),
	}
	reqDifferentBase := cliproxyexecutor.Request{
		Model:   "gpt-5.3-codex(high)",
		Payload: []byte(`{"metadata":{"user_id":"same-user-base-model-cache"}}`),
	}

	httpReqHigh, err := executor.cacheHelper(context.Background(), auth, sdktranslator.FromString("claude"), url, reqHigh, rawJSON)
	if err != nil {
		t.Fatalf("cacheHelper high error: %v", err)
	}
	httpReqMedium, err := executor.cacheHelper(context.Background(), auth, sdktranslator.FromString("claude"), url, reqMedium, rawJSON)
	if err != nil {
		t.Fatalf("cacheHelper medium error: %v", err)
	}
	httpReqDifferentBase, err := executor.cacheHelper(context.Background(), auth, sdktranslator.FromString("claude"), url, reqDifferentBase, rawJSON)
	if err != nil {
		t.Fatalf("cacheHelper different base error: %v", err)
	}

	keyHigh := promptCacheKeyFromRequest(t, httpReqHigh)
	keyMedium := promptCacheKeyFromRequest(t, httpReqMedium)
	keyDifferentBase := promptCacheKeyFromRequest(t, httpReqDifferentBase)
	if keyHigh == "" || keyDifferentBase == "" {
		t.Fatalf("expected non-empty cache keys, got high=%q different=%q", keyHigh, keyDifferentBase)
	}
	if keyHigh != keyMedium {
		t.Fatalf("same base model cache key changed: high=%q medium=%q", keyHigh, keyMedium)
	}
	if keyHigh == keyDifferentBase {
		t.Fatalf("different base models shared prompt_cache_key %q", keyHigh)
	}
}

func promptCacheKeyFromRequest(t *testing.T, req *http.Request) string {
	t.Helper()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	return gjson.GetBytes(body, "prompt_cache_key").String()
}
