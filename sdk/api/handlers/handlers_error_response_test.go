package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/logging"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

func TestWriteErrorResponse_AddonHeadersDisabledByDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	handler := NewBaseAPIHandlers(nil, nil)
	handler.WriteErrorResponse(c, &interfaces.ErrorMessage{
		StatusCode: http.StatusTooManyRequests,
		Error:      errors.New("rate limit"),
		Addon: http.Header{
			"Retry-After":  {"30"},
			"X-Request-Id": {"req-1"},
		},
	})

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusTooManyRequests)
	}
	if got := recorder.Header().Get("Retry-After"); got != "" {
		t.Fatalf("Retry-After should be empty when passthrough is disabled, got %q", got)
	}
	if got := recorder.Header().Get("X-Request-Id"); got != "" {
		t.Fatalf("X-Request-Id should be empty when passthrough is disabled, got %q", got)
	}
}

func TestWriteErrorResponse_AddonHeadersEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Writer.Header().Set("X-Request-Id", "old-value")

	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{PassthroughHeaders: true}, nil)
	handler.WriteErrorResponse(c, &interfaces.ErrorMessage{
		StatusCode: http.StatusTooManyRequests,
		Error:      errors.New("rate limit"),
		Addon: http.Header{
			"Retry-After":  {"30"},
			"X-Request-Id": {"new-1", "new-2"},
		},
	})

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusTooManyRequests)
	}
	if got := recorder.Header().Get("Retry-After"); got != "30" {
		t.Fatalf("Retry-After = %q, want %q", got, "30")
	}
	if got := recorder.Header().Values("X-Request-Id"); !reflect.DeepEqual(got, []string{"new-1", "new-2"}) {
		t.Fatalf("X-Request-Id = %#v, want %#v", got, []string{"new-1", "new-2"})
	}
}

func TestBuildErrorResponseBody_SanitizesUnknownProviderLeak(t *testing.T) {
	body := BuildErrorResponseBody(http.StatusBadGateway, "unknown provider for model gpt-5.4(medium)")
	var payload ErrorResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Error.Message != GenericSensitiveClientErrorMessage {
		t.Fatalf("message = %q", payload.Error.Message)
	}
	assertNoClientInternalLeak(t, body)
}

func TestBuildErrorResponseBody_SanitizesUnknownProviderLeakFromJSON(t *testing.T) {
	body := BuildErrorResponseBody(http.StatusBadGateway, `{"error":{"message":"unknown provider for model gpt-5.4(medium)","type":"server_error","code":"internal_server_error"}}`)
	var payload ErrorResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Error.Message != GenericSensitiveClientErrorMessage {
		t.Fatalf("message = %q", payload.Error.Message)
	}
	assertNoClientInternalLeak(t, body)
}

func TestBuildErrorResponseBody_SanitizesCodexAuthLeakFromJSON(t *testing.T) {
	body := BuildErrorResponseBody(
		http.StatusForbidden,
		`{"error":{"message":"Codex Provider API rejected auth file codex-account.json for gpt-5.5","type":"authentication_error","code":"codex_auth_file_failed"}}`,
	)
	var payload ErrorResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Error.Message != GenericSensitiveClientErrorMessage {
		t.Fatalf("message = %q", payload.Error.Message)
	}
	if payload.Error.Type != "server_error" || payload.Error.Code != "internal_server_error" {
		t.Fatalf("error shape = %#v, want generic server error", payload.Error)
	}
	assertNoClientInternalLeak(t, body)
}

func TestBuildErrorResponseBody_PreservesBenignBudgetError(t *testing.T) {
	body := BuildErrorResponseBody(http.StatusTooManyRequests, "daily budget exceeded")
	var payload ErrorResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Error.Message != "daily budget exceeded" {
		t.Fatalf("message = %q", payload.Error.Message)
	}
}

func TestAttachRequestIDToErrorBody(t *testing.T) {
	body := AttachRequestIDToErrorBody([]byte(`{"error":{"message":"bad","type":"invalid_request_error"}}`), "abc123ef")

	var payload ErrorResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.RequestID != "abc123ef" {
		t.Fatalf("request_id = %q, want abc123ef", payload.RequestID)
	}
	if payload.Error.Message != "bad" {
		t.Fatalf("message = %q, want bad", payload.Error.Message)
	}
}

func TestWriteErrorResponse_AttachesLocalRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	logging.SetGinRequestID(c, "deadbeef")

	handler := NewBaseAPIHandlers(nil, nil)
	handler.WriteErrorResponse(c, &interfaces.ErrorMessage{
		StatusCode: http.StatusBadRequest,
		Error:      errors.New("bad request"),
	})

	var payload ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.RequestID != "deadbeef" {
		t.Fatalf("request_id = %q, want deadbeef", payload.RequestID)
	}
}

func TestClientErrorStatusForResponse_SanitizesInternalAuthStatus(t *testing.T) {
	got := ClientErrorStatusForResponse(
		http.StatusForbidden,
		`{"error":{"message":"Codex Provider API rejected auth file for gpt-5.5"}}`,
	)
	if got != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", got, http.StatusServiceUnavailable)
	}
}

func TestWriteErrorResponse_SanitizesSensitiveStatusAndBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	handler := NewBaseAPIHandlers(nil, nil)
	handler.WriteErrorResponse(c, &interfaces.ErrorMessage{
		StatusCode: http.StatusForbidden,
		Error:      errors.New(`{"error":{"message":"Codex Provider API rejected auth file for gpt-5.5","type":"authentication_error","code":"codex_auth_file_failed"}}`),
	})

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	var payload ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Error.Message != GenericSensitiveClientErrorMessage {
		t.Fatalf("message = %q", payload.Error.Message)
	}
	assertNoClientInternalLeak(t, recorder.Body.Bytes())
}

func TestBuildClaudeErrorResponseBodyFromMessage_SanitizesUnknownProviderLeak(t *testing.T) {
	body := BuildClaudeErrorResponseBodyFromMessage(&interfaces.ErrorMessage{
		StatusCode: http.StatusBadGateway,
		Error:      errors.New(`{"error":{"message":"unknown provider for model gpt-5.4(medium)"}}`),
	})
	var payload claudeErrorResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Error.Message != GenericSensitiveClientErrorMessage {
		t.Fatalf("message = %q", payload.Error.Message)
	}
	assertNoClientInternalLeak(t, body)
}

func assertNoClientInternalLeak(t *testing.T, body []byte) {
	t.Helper()
	lower := strings.ToLower(string(body))
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
			t.Fatalf("client error body leaked %q: %s", forbidden, string(body))
		}
	}
}
