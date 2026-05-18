package executor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
	"github.com/tidwall/gjson"
)

func TestBuildCodexWebsocketRequestBodyPreservesPreviousResponseID(t *testing.T) {
	body := []byte(`{"model":"gpt-5-codex","previous_response_id":"resp-1","input":[{"type":"message","id":"msg-1"}]}`)

	wsReqBody := buildCodexWebsocketRequestBody(body)

	if got := gjson.GetBytes(wsReqBody, "type").String(); got != "response.create" {
		t.Fatalf("type = %s, want response.create", got)
	}
	if got := gjson.GetBytes(wsReqBody, "previous_response_id").String(); got != "resp-1" {
		t.Fatalf("previous_response_id = %s, want resp-1", got)
	}
	if gjson.GetBytes(wsReqBody, "input.0.id").String() != "msg-1" {
		t.Fatalf("input item id mismatch")
	}
	if got := gjson.GetBytes(wsReqBody, "type").String(); got == "response.append" {
		t.Fatalf("unexpected websocket request type: %s", got)
	}
}

func TestPatchCodexWebsocketCompletionOutputUsesOutputItemDone(t *testing.T) {
	outputItemsByIndex := make(map[int64][]byte)
	var outputItemsFallback [][]byte

	done := []byte(`{"type":"response.output_item.done","output_index":0,"item":{"id":"ig_1","type":"image_generation_call","output_format":"png","result":"aGVsbG8="}}`)
	gotDone := patchCodexWebsocketCompletionOutput(done, outputItemsByIndex, &outputItemsFallback)
	if got := gjson.GetBytes(gotDone, "type").String(); got != "response.output_item.done" {
		t.Fatalf("done type = %q, want response.output_item.done", got)
	}

	completed := []byte(`{"type":"response.completed","response":{"id":"resp_1","output":[]}}`)
	patched := patchCodexWebsocketCompletionOutput(completed, outputItemsByIndex, &outputItemsFallback)

	if got := gjson.GetBytes(patched, "response.output.0.type").String(); got != "image_generation_call" {
		t.Fatalf("patched output type = %q, want image_generation_call; payload=%s", got, string(patched))
	}
	if got := gjson.GetBytes(patched, "response.output.0.result").String(); got != "aGVsbG8=" {
		t.Fatalf("patched output result = %q, want image payload; payload=%s", got, string(patched))
	}
}

func TestApplyCodexWebsocketHeadersDefaultsToCurrentResponsesBeta(t *testing.T) {
	headers := applyCodexWebsocketHeaders(context.Background(), http.Header{}, nil, "", nil)

	if got := headers.Get("OpenAI-Beta"); got != codexResponsesWebsocketBetaHeaderValue {
		t.Fatalf("OpenAI-Beta = %s, want %s", got, codexResponsesWebsocketBetaHeaderValue)
	}
	if got := headers.Get("User-Agent"); got != codexUserAgent {
		t.Fatalf("User-Agent = %s, want %s", got, codexUserAgent)
	}
	if got := headers.Get("Version"); got != "" {
		t.Fatalf("Version = %s, want empty", got)
	}
	if got := headers.Get("x-codex-beta-features"); got != "" {
		t.Fatalf("x-codex-beta-features = %q, want empty", got)
	}
}

func TestApplyCodexWebsocketHeadersUsesConfigDefaultsForOAuth(t *testing.T) {
	cfg := &config.Config{
		CodexHeaderDefaults: config.CodexHeaderDefaults{
			UserAgent:    "my-codex-client/1.0",
			Version:      "0.126.0",
			BetaFeatures: "feature-a,feature-b",
		},
	}
	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{"email": "user@example.com"},
	}

	headers := applyCodexWebsocketHeaders(context.Background(), http.Header{}, auth, "", cfg)

	if got := headers.Get("User-Agent"); got != "my-codex-client/1.0" {
		t.Fatalf("User-Agent = %s, want %s", got, "my-codex-client/1.0")
	}
	if got := headers.Get("Version"); got != "" {
		t.Fatalf("Version = %s, want empty", got)
	}
	if got := headers.Get("x-codex-beta-features"); got != "feature-a,feature-b" {
		t.Fatalf("x-codex-beta-features = %s, want %s", got, "feature-a,feature-b")
	}
	if got := headers.Get("OpenAI-Beta"); got != codexResponsesWebsocketBetaHeaderValue {
		t.Fatalf("OpenAI-Beta = %s, want %s", got, codexResponsesWebsocketBetaHeaderValue)
	}
}

func TestApplyCodexWebsocketHeadersPrefersExistingHeadersOverClientAndConfig(t *testing.T) {
	cfg := &config.Config{
		CodexHeaderDefaults: config.CodexHeaderDefaults{
			UserAgent:    "config-ua",
			Version:      "config-version",
			BetaFeatures: "config-beta",
		},
	}
	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{"email": "user@example.com"},
	}
	ctx := contextWithGinHeaders(map[string]string{
		"User-Agent":            "client-ua",
		"Version":               "client-version",
		"X-Codex-Beta-Features": "client-beta",
	})
	headers := http.Header{}
	headers.Set("User-Agent", "existing-ua")
	headers.Set("Version", "existing-version")
	headers.Set("X-Codex-Beta-Features", "existing-beta")

	got := applyCodexWebsocketHeaders(ctx, headers, auth, "", cfg)

	if gotVal := got.Get("User-Agent"); gotVal != "existing-ua" {
		t.Fatalf("User-Agent = %s, want %s", gotVal, "existing-ua")
	}
	if gotVal := got.Get("Version"); gotVal != "client-version" {
		t.Fatalf("Version = %s, want %s", gotVal, "client-version")
	}
	if gotVal := got.Get("x-codex-beta-features"); gotVal != "existing-beta" {
		t.Fatalf("x-codex-beta-features = %s, want %s", gotVal, "existing-beta")
	}
}

func TestApplyCodexWebsocketHeadersConfigUserAgentOverridesClientHeader(t *testing.T) {
	cfg := &config.Config{
		CodexHeaderDefaults: config.CodexHeaderDefaults{
			UserAgent:    "config-ua",
			Version:      "config-version",
			BetaFeatures: "config-beta",
		},
	}
	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{"email": "user@example.com"},
	}
	ctx := contextWithGinHeaders(map[string]string{
		"User-Agent":            "client-ua",
		"Version":               "client-version",
		"X-Codex-Beta-Features": "client-beta",
	})

	headers := applyCodexWebsocketHeaders(ctx, http.Header{}, auth, "", cfg)

	if got := headers.Get("User-Agent"); got != "config-ua" {
		t.Fatalf("User-Agent = %s, want %s", got, "config-ua")
	}
	if got := headers.Get("Version"); got != "client-version" {
		t.Fatalf("Version = %s, want %s", got, "client-version")
	}
	if got := headers.Get("x-codex-beta-features"); got != "client-beta" {
		t.Fatalf("x-codex-beta-features = %s, want %s", got, "client-beta")
	}
}

func TestApplyCodexWebsocketHeadersIgnoresConfigForAPIKeyAuth(t *testing.T) {
	cfg := &config.Config{
		CodexHeaderDefaults: config.CodexHeaderDefaults{
			UserAgent:    "config-ua",
			Version:      "config-version",
			BetaFeatures: "config-beta",
		},
	}
	auth := &cliproxyauth.Auth{
		Provider:   "codex",
		Attributes: map[string]string{"api_key": "sk-test"},
	}

	headers := applyCodexWebsocketHeaders(context.Background(), http.Header{}, auth, "sk-test", cfg)

	if got := headers.Get("User-Agent"); got != "" {
		t.Fatalf("User-Agent = %s, want empty", got)
	}
	if got := headers.Get("Version"); got != "" {
		t.Fatalf("Version = %s, want empty", got)
	}
	if got := headers.Get("x-codex-beta-features"); got != "" {
		t.Fatalf("x-codex-beta-features = %q, want empty", got)
	}
}

func TestApplyCodexHeadersUsesConfigUserAgentForOAuth(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://example.com/responses", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	cfg := &config.Config{
		CodexHeaderDefaults: config.CodexHeaderDefaults{
			UserAgent:    "config-ua",
			Version:      "config-version",
			BetaFeatures: "config-beta",
		},
	}
	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{"email": "user@example.com"},
	}
	req = req.WithContext(contextWithGinHeaders(map[string]string{
		"User-Agent": "client-ua",
	}))

	applyCodexHeaders(req, auth, "oauth-token", true, cfg)

	if got := req.Header.Get("User-Agent"); got != "config-ua" {
		t.Fatalf("User-Agent = %s, want %s", got, "config-ua")
	}
	if got := req.Header.Get("Version"); got != "" {
		t.Fatalf("Version = %s, want empty", got)
	}
	if got := req.Header.Get("x-codex-beta-features"); got != "" {
		t.Fatalf("x-codex-beta-features = %q, want empty", got)
	}
}

func TestApplyCodexHeadersPassesClientTurnHeadersAndOriginatorForAPIKeyAuth(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://example.com/responses", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	auth := &cliproxyauth.Auth{
		Provider:   "codex",
		Attributes: map[string]string{"api_key": "sk-test"},
	}
	req = req.WithContext(contextWithGinHeaders(map[string]string{
		"Originator":            "codex-tui",
		"User-Agent":            "codex-tui/0.130.0 (Windows 10.0.0)",
		"Version":               "0.130.0",
		"X-Codex-Beta-Features": "responses-v2",
		"X-Codex-Turn-Metadata": "turn-meta",
		"X-Client-Request-Id":   "client-req-1",
	}))

	applyCodexHeaders(req, auth, "sk-test", true, nil)

	if got := req.Header.Get("Originator"); got != "codex-tui" {
		t.Fatalf("Originator = %s, want codex-tui", got)
	}
	if got := req.Header.Get("User-Agent"); got != "codex-tui/0.130.0 (Windows 10.0.0)" {
		t.Fatalf("User-Agent = %s, want client User-Agent", got)
	}
	if got := req.Header.Get("Version"); got != "0.130.0" {
		t.Fatalf("Version = %s, want 0.130.0", got)
	}
	if got := req.Header.Get("X-Codex-Beta-Features"); got != "responses-v2" {
		t.Fatalf("X-Codex-Beta-Features = %s, want responses-v2", got)
	}
	if got := req.Header.Get("X-Codex-Turn-Metadata"); got != "turn-meta" {
		t.Fatalf("X-Codex-Turn-Metadata = %s, want turn-meta", got)
	}
	if got := req.Header.Get("X-Client-Request-Id"); got != "client-req-1" {
		t.Fatalf("X-Client-Request-Id = %s, want client-req-1", got)
	}
	if got := headerValueCaseInsensitive(req.Header, "session_id"); got != "" {
		t.Fatalf("session_id = %s, want empty for non-Mac client", got)
	}
}

func TestApplyCodexHeadersAddsSessionIDOnlyForMacUserAgent(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://example.com/responses", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req = req.WithContext(contextWithGinHeaders(map[string]string{
		"User-Agent": "codex-tui/0.130.0 (Mac OS 15.5.0)",
	}))

	applyCodexHeaders(req, nil, "oauth-token", true, nil)

	if got := headerValueCaseInsensitive(req.Header, "session_id"); got == "" {
		t.Fatal("session_id is empty, want generated value for Mac client")
	}
}

func TestApplyCodexWebsocketHeadersPassesClientIdentityForAPIKeyAuth(t *testing.T) {
	auth := &cliproxyauth.Auth{
		Provider:   "codex",
		Attributes: map[string]string{"api_key": "sk-test"},
	}
	ctx := contextWithGinHeaders(map[string]string{
		"Originator":            "codex-tui",
		"User-Agent":            "codex-tui/0.130.0 (Windows 10.0.0)",
		"Version":               "0.130.0",
		"x-codex-turn-metadata": "turn-meta",
		"x-client-request-id":   "client-req-1",
		"x-codex-beta-features": "responses-v2",
	})

	headers := applyCodexWebsocketHeaders(ctx, http.Header{}, auth, "sk-test", nil)

	if got := headers.Get("Originator"); got != "codex-tui" {
		t.Fatalf("Originator = %s, want codex-tui", got)
	}
	if got := headers.Get("User-Agent"); got != "codex-tui/0.130.0 (Windows 10.0.0)" {
		t.Fatalf("User-Agent = %s, want client User-Agent", got)
	}
	if got := headers.Get("Version"); got != "0.130.0" {
		t.Fatalf("Version = %s, want 0.130.0", got)
	}
	if got := headers.Get("x-codex-turn-metadata"); got != "turn-meta" {
		t.Fatalf("x-codex-turn-metadata = %s, want turn-meta", got)
	}
	if got := headers.Get("x-client-request-id"); got != "client-req-1" {
		t.Fatalf("x-client-request-id = %s, want client-req-1", got)
	}
	if got := headers.Get("x-codex-beta-features"); got != "responses-v2" {
		t.Fatalf("x-codex-beta-features = %s, want responses-v2", got)
	}
	if got := headerValueCaseInsensitive(headers, "session_id"); got != "" {
		t.Fatalf("session_id = %s, want empty for non-Mac client", got)
	}
}

func contextWithGinHeaders(headers map[string]string) context.Context {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	ginCtx.Request.Header = make(http.Header, len(headers))
	for key, value := range headers {
		ginCtx.Request.Header.Set(key, value)
	}
	return context.WithValue(context.Background(), "gin", ginCtx)
}

func TestNewProxyAwareWebsocketDialerDirectDisablesProxy(t *testing.T) {
	t.Parallel()

	dialer := newProxyAwareWebsocketDialer(
		&config.Config{SDKConfig: sdkconfig.SDKConfig{ProxyURL: "http://global-proxy.example.com:8080"}},
		&cliproxyauth.Auth{ProxyURL: "direct"},
	)

	if dialer.Proxy != nil {
		t.Fatal("expected websocket proxy function to be nil for direct mode")
	}
}

func TestCodexWebsocketSessionIsScopedByAuth(t *testing.T) {
	executor := NewCodexWebsocketsExecutor(nil)
	authA := &cliproxyauth.Auth{ID: "codex-a", Provider: "codex", ProxyURL: "http://127.0.0.1:18081"}
	authB := &cliproxyauth.Auth{ID: "codex-b", Provider: "codex", ProxyURL: "http://127.0.0.1:18082"}

	sessA1 := executor.getOrCreateSession("shared-session", authA, "wss://chatgpt.com/backend-api/codex/responses")
	sessA2 := executor.getOrCreateSession("shared-session", authA, "wss://chatgpt.com/backend-api/codex/responses")
	sessB := executor.getOrCreateSession("shared-session", authB, "wss://chatgpt.com/backend-api/codex/responses")

	if sessA1 == nil || sessA2 == nil || sessB == nil {
		t.Fatal("expected non-nil sessions")
	}
	if sessA1 != sessA2 {
		t.Fatal("same auth should reuse the scoped websocket session")
	}
	if sessA1 == sessB {
		t.Fatal("different auths must not share the same websocket session")
	}
	if len(executor.sessions) != 2 {
		t.Fatalf("session map size = %d, want 2", len(executor.sessions))
	}

	executor.CloseExecutionSession("shared-session")
	if len(executor.sessions) != 0 {
		t.Fatalf("CloseExecutionSession left %d scoped sessions, want 0", len(executor.sessions))
	}
}
