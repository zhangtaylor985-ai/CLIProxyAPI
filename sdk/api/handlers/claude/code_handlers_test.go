package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	internallogging "github.com/router-for-me/CLIProxyAPI/v6/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
	log "github.com/sirupsen/logrus"
)

type claudeErrorPayload struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type slowFailingClaudeExecutor struct {
	delay time.Duration
}

func (e slowFailingClaudeExecutor) Identifier() string { return "claude" }

func (e slowFailingClaudeExecutor) Execute(ctx context.Context, _ *coreauth.Auth, _ coreexecutor.Request, _ coreexecutor.Options) (coreexecutor.Response, error) {
	select {
	case <-ctx.Done():
		return coreexecutor.Response{}, ctx.Err()
	case <-time.After(e.delay):
		return coreexecutor.Response{}, &coreauth.Error{
			HTTPStatus: http.StatusRequestTimeout,
			Message:    "stream closed before response.completed",
		}
	}
}

func (e slowFailingClaudeExecutor) ExecuteStream(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	return nil, &coreauth.Error{HTTPStatus: http.StatusInternalServerError, Message: "unexpected stream call"}
}

func (e slowFailingClaudeExecutor) CountTokens(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, &coreauth.Error{HTTPStatus: http.StatusInternalServerError, Message: "unexpected count call"}
}

func (e slowFailingClaudeExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (e slowFailingClaudeExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, nil
}

type emptyClaudeExecutor struct{}

func (e emptyClaudeExecutor) Identifier() string { return "claude" }

func (e emptyClaudeExecutor) Execute(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{Payload: []byte{}}, nil
}

func (e emptyClaudeExecutor) ExecuteStream(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	return nil, &coreauth.Error{HTTPStatus: http.StatusInternalServerError, Message: "unexpected stream call"}
}

func (e emptyClaudeExecutor) CountTokens(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, &coreauth.Error{HTTPStatus: http.StatusInternalServerError, Message: "unexpected count call"}
}

func (e emptyClaudeExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (e emptyClaudeExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, nil
}

type captureLogHook struct {
	entries chan *log.Entry
}

func (h captureLogHook) Levels() []log.Level {
	return []log.Level{log.ErrorLevel}
}

func (h captureLogHook) Fire(entry *log.Entry) error {
	h.entries <- entry
	return nil
}

func TestWriteClientError_UsesClaudeErrorBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	h := NewClaudeCodeAPIHandler(base)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	h.writeClientError(c, &interfaces.ErrorMessage{
		StatusCode: http.StatusTooManyRequests,
		Error:      errors.New("weekly budget exceeded"),
	})

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusTooManyRequests)
	}

	var payload claudeErrorPayload
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Type != "error" {
		t.Fatalf("type = %q, want %q", payload.Type, "error")
	}
	if payload.Error.Type != "api_error" {
		t.Fatalf("error.type = %q, want %q", payload.Error.Type, "api_error")
	}
	if payload.Error.Message != "weekly budget exceeded" {
		t.Fatalf("error.message = %q", payload.Error.Message)
	}
	if strings.Contains(recorder.Body.String(), `"rate_limit_error"`) {
		t.Fatalf("expected Claude error body, got OpenAI error body: %q", recorder.Body.String())
	}
}

func TestWriteClientError_SanitizesUnknownProviderModelLeak(t *testing.T) {
	gin.SetMode(gin.TestMode)
	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	h := NewClaudeCodeAPIHandler(base)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	h.writeClientError(c, &interfaces.ErrorMessage{
		StatusCode: http.StatusBadGateway,
		Error:      errors.New(`{"error":{"message":"unknown provider for model gpt-5.4(medium)"}}`),
	})

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}

	var payload claudeErrorPayload
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Error.Message != handlers.GenericSensitiveClientErrorMessage {
		t.Fatalf("error.message = %q", payload.Error.Message)
	}
	assertNoClaudeClientInternalLeak(t, recorder.Body.String())
}

func TestSanitizeClientErrorLogsRequestIDAndAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	h := NewClaudeCodeAPIHandler(base)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	internallogging.SetGinRequestID(c, "req-alert-1")
	c.Set("apiKey", "sk-user-alert-test-123456")
	setClaudeAlertContext(c, "non_stream", "claude-empty-response-test", "upstream_empty_response")
	c.Set("claude_alert_provider", "codex")

	logger := log.StandardLogger()
	previousHooks := logger.Hooks
	hook := captureLogHook{entries: make(chan *log.Entry, 1)}
	logger.ReplaceHooks(log.LevelHooks{})
	logger.AddHook(hook)
	t.Cleanup(func() {
		logger.ReplaceHooks(previousHooks)
	})

	sanitized := h.sanitizeClientError(c, &interfaces.ErrorMessage{
		StatusCode: http.StatusBadGateway,
		Error:      errors.New("empty upstream response"),
	})

	if sanitized == nil || sanitized.Error == nil || sanitized.Error.Error() != handlers.GenericSensitiveClientErrorMessage {
		t.Fatalf("unexpected sanitized error: %#v", sanitized)
	}
	select {
	case entry := <-hook.entries:
		if got := entry.Data["request_id"]; got != "req-alert-1" {
			t.Fatalf("request_id field = %#v, want req-alert-1", got)
		}
		if got := entry.Data["client_api_key"]; got != "sk-user-alert-test-123456" {
			t.Fatalf("client_api_key field = %#v, want sk-user-alert-test-123456", got)
		}
		if got := entry.Data["model"]; got != "claude-empty-response-test" {
			t.Fatalf("model field = %#v, want claude-empty-response-test", got)
		}
		if got := entry.Data["mode"]; got != "non_stream" {
			t.Fatalf("mode field = %#v, want non_stream", got)
		}
		if got := entry.Data["stage"]; got != "upstream_empty_response" {
			t.Fatalf("stage field = %#v, want upstream_empty_response", got)
		}
		if got := entry.Data["provider"]; got != "codex" {
			t.Fatalf("provider field = %#v, want codex", got)
		}
		if got := entry.Data["upstream_error"]; got != "empty upstream response" {
			t.Fatalf("upstream_error field = %#v, want empty upstream response", got)
		}
		if got := entry.Data["diagnosis"]; got != "Upstream execution completed but returned an empty non-streaming response body." {
			t.Fatalf("diagnosis field = %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("expected sanitize log entry")
	}
}

func TestWriteClientError_SanitizesCodexAuthLeak(t *testing.T) {
	gin.SetMode(gin.TestMode)
	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	h := NewClaudeCodeAPIHandler(base)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	h.writeClientError(c, &interfaces.ErrorMessage{
		StatusCode: http.StatusForbidden,
		Error:      errors.New(`{"error":{"message":"Codex Provider API rejected auth file for gpt-5.5","type":"authentication_error","code":"codex_auth_file_failed"}}`),
	})

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}

	var payload claudeErrorPayload
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Error.Message != handlers.GenericSensitiveClientErrorMessage {
		t.Fatalf("error.message = %q", payload.Error.Message)
	}
	assertNoClaudeClientInternalLeak(t, recorder.Body.String())
}

func TestClaudeNonStreamingResponse_DoesNotBodyKeepAliveBeforeError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(slowFailingClaudeExecutor{delay: 1100 * time.Millisecond})
	auth := &coreauth.Auth{
		ID:       "claude-nonstream-keepalive-test",
		Provider: "claude",
		Status:   coreauth.StatusActive,
	}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}
	registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: "claude-nonstream-test"}})
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient(auth.ID)
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{NonStreamKeepAliveInterval: 1}, manager)
	h := NewClaudeCodeAPIHandler(base)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	h.handleNonStreamingResponse(c, []byte(`{"model":"claude-nonstream-test","messages":[{"role":"user","content":"hi"}]}`))

	if recorder.Code != http.StatusRequestTimeout {
		t.Fatalf("status = %d, want %d; body=%q", recorder.Code, http.StatusRequestTimeout, recorder.Body.String())
	}
	if strings.HasPrefix(recorder.Body.String(), "\n") {
		t.Fatalf("non-streaming Claude response wrote keepalive before error body: %q", recorder.Body.String())
	}

	var payload claudeErrorPayload
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Error.Message != "stream closed before response.completed" {
		t.Fatalf("error.message = %q", payload.Error.Message)
	}
}

func TestClaudeNonStreamingResponse_RejectsEmptyUpstreamBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(emptyClaudeExecutor{})
	auth := &coreauth.Auth{
		ID:       "claude-empty-response-test",
		Provider: "claude",
		Status:   coreauth.StatusActive,
	}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}
	registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: "claude-empty-response-test"}})
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient(auth.ID)
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewClaudeCodeAPIHandler(base)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	h.handleNonStreamingResponse(c, []byte(`{"model":"claude-empty-response-test","max_tokens":1,"messages":[{"role":"user","content":"Hi"}]}`))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%q", recorder.Code, http.StatusServiceUnavailable, recorder.Body.String())
	}
	if strings.TrimSpace(recorder.Body.String()) == "" {
		t.Fatal("expected Claude error body, got empty response")
	}

	var payload claudeErrorPayload
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Error.Message != handlers.GenericSensitiveClientErrorMessage {
		t.Fatalf("error.message = %q", payload.Error.Message)
	}
	assertNoClaudeClientInternalLeak(t, recorder.Body.String())
}

func TestForwardClaudeStreamTerminalError_UsesClaudeErrorEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	h := NewClaudeCodeAPIHandler(base)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		t.Fatalf("expected gin writer to implement http.Flusher")
	}

	data := make(chan []byte)
	errs := make(chan *interfaces.ErrorMessage, 1)
	errs <- &interfaces.ErrorMessage{StatusCode: http.StatusInternalServerError, Error: errors.New("unexpected EOF")}
	close(errs)

	h.forwardClaudeStream(c, flusher, func(error) {}, data, errs)

	body := recorder.Body.String()
	if !strings.Contains(body, `event: error`) {
		t.Fatalf("expected Claude SSE error event, got: %q", body)
	}
	if !strings.Contains(body, `"type":"error"`) || !strings.Contains(body, `"type":"api_error"`) {
		t.Fatalf("expected Claude error payload, got: %q", body)
	}
	if strings.Contains(body, `"server_error"`) {
		t.Fatalf("expected Claude error payload, got OpenAI error body: %q", body)
	}

	recorded, ok := c.Get("API_RESPONSE_ERROR")
	if !ok {
		t.Fatal("expected terminal stream error to be recorded in gin context")
	}
	errors, ok := recorded.([]*interfaces.ErrorMessage)
	if !ok || len(errors) != 1 {
		t.Fatalf("recorded errors = %#v, want one ErrorMessage", recorded)
	}
	if errors[0].Error == nil || errors[0].Error.Error() != "unexpected EOF" {
		t.Fatalf("recorded error = %v, want unexpected EOF", errors[0].Error)
	}
}

func TestForwardClaudeStreamTerminalError_DoesNotRewriteCommittedStatus(t *testing.T) {
	gin.SetMode(gin.DebugMode)
	defer gin.SetMode(gin.TestMode)
	oldDebugPrintFunc := gin.DebugPrintFunc
	defer func() {
		gin.DebugPrintFunc = oldDebugPrintFunc
	}()

	var warnings []string
	gin.DebugPrintFunc = func(format string, values ...interface{}) {
		msg := fmt.Sprintf(format, values...)
		if strings.Contains(msg, "Headers were already written") {
			warnings = append(warnings, msg)
		}
	}

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	h := NewClaudeCodeAPIHandler(base)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		t.Fatalf("expected gin writer to implement http.Flusher")
	}

	data := make(chan []byte)
	errs := make(chan *interfaces.ErrorMessage)
	go func() {
		data <- []byte("event: message_start\ndata: {\"type\":\"message_start\"}\n\n")
		errs <- &interfaces.ErrorMessage{StatusCode: http.StatusInternalServerError, Error: errors.New("unexpected EOF")}
		close(data)
		close(errs)
	}()

	h.forwardClaudeStream(c, flusher, func(error) {}, data, errs)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want committed %d", recorder.Code, http.StatusOK)
	}
	if len(warnings) > 0 {
		t.Fatalf("terminal stream error rewrote committed status: %v", warnings)
	}

	body := recorder.Body.String()
	if !strings.Contains(body, `event: message_start`) || !strings.Contains(body, `event: error`) {
		t.Fatalf("expected initial chunk followed by Claude SSE error event, got: %q", body)
	}
}

func TestForwardClaudeStreamTerminalError_RecordsSanitizedError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	h := NewClaudeCodeAPIHandler(base)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		t.Fatalf("expected gin writer to implement http.Flusher")
	}

	data := make(chan []byte)
	errs := make(chan *interfaces.ErrorMessage, 1)
	errs <- &interfaces.ErrorMessage{
		StatusCode: http.StatusInternalServerError,
		Error:      errors.New("upstream service unavailable cf-ray abc123"),
	}
	close(errs)

	h.forwardClaudeStream(c, flusher, func(error) {}, data, errs)

	recorded, ok := c.Get("API_RESPONSE_ERROR")
	if !ok {
		t.Fatal("expected terminal stream error to be recorded in gin context")
	}
	errors, ok := recorded.([]*interfaces.ErrorMessage)
	if !ok || len(errors) != 1 {
		t.Fatalf("recorded errors = %#v, want one ErrorMessage", recorded)
	}
	if errors[0].StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("recorded status = %d, want %d", errors[0].StatusCode, http.StatusServiceUnavailable)
	}
	if got := errors[0].Error.Error(); got != handlers.GenericSensitiveClientErrorMessage {
		t.Fatalf("recorded error = %q", got)
	}
	if strings.Contains(fmt.Sprint(recorded), "cf-ray") {
		t.Fatalf("recorded error was not sanitized: %#v", recorded)
	}
}

func assertNoClaudeClientInternalLeak(t *testing.T, body string) {
	t.Helper()
	lower := strings.ToLower(body)
	for _, forbidden := range []string{
		"codex",
		"gpt",
		"chatgpt",
		"openai",
		"provider",
		"auth",
		"credential",
		"oauth",
		"id_token",
		"access token",
		"refresh token",
		"api_key",
		"sk-",
		"@gmail.com",
		"@outlook.com",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("Claude client error leaked %q: %s", forbidden, body)
		}
	}
}
