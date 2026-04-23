package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

type claudeErrorPayload struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
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
