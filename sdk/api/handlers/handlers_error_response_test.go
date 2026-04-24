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
	if payload.Error.Message != "upstream model temporarily unavailable, please retry later" {
		t.Fatalf("message = %q", payload.Error.Message)
	}
}

func TestBuildErrorResponseBody_SanitizesUnknownProviderLeakFromJSON(t *testing.T) {
	body := BuildErrorResponseBody(http.StatusBadGateway, `{"error":{"message":"unknown provider for model gpt-5.4(medium)","type":"server_error","code":"internal_server_error"}}`)
	var payload ErrorResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Error.Message != "upstream model temporarily unavailable, please retry later" {
		t.Fatalf("message = %q", payload.Error.Message)
	}
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
	if payload.Error.Message != "upstream model temporarily unavailable, please retry later" {
		t.Fatalf("message = %q", payload.Error.Message)
	}
	if strings.Contains(strings.ToLower(string(body)), "gpt") {
		t.Fatalf("Claude error body leaked internal model: %s", string(body))
	}
}
