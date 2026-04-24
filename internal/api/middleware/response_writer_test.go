package middleware

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/sessiontrajectory"
)

type responseWriterRecorderStub struct{}

func (responseWriterRecorderStub) Record(context.Context, *sessiontrajectory.CompletedRequest) error {
	return nil
}

func (responseWriterRecorderStub) Close() error { return nil }

type countingResponseWriterRecorder struct {
	count atomic.Int64
}

func (r *countingResponseWriterRecorder) Record(context.Context, *sessiontrajectory.CompletedRequest) error {
	r.count.Add(1)
	return nil
}

func (r *countingResponseWriterRecorder) Close() error { return nil }

func TestExtractRequestBodyPrefersOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	wrapper := &ResponseWriterWrapper{
		requestInfo: &RequestInfo{Body: []byte("original-body")},
	}

	body := wrapper.extractRequestBody(c)
	if string(body) != "original-body" {
		t.Fatalf("request body = %q, want %q", string(body), "original-body")
	}

	c.Set(requestBodyOverrideContextKey, []byte("override-body"))
	body = wrapper.extractRequestBody(c)
	if string(body) != "override-body" {
		t.Fatalf("request body = %q, want %q", string(body), "override-body")
	}
}

func TestExtractRequestBodySupportsStringOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	wrapper := &ResponseWriterWrapper{}
	c.Set(requestBodyOverrideContextKey, "override-as-string")

	body := wrapper.extractRequestBody(c)
	if string(body) != "override-as-string" {
		t.Fatalf("request body = %q, want %q", string(body), "override-as-string")
	}
}

func TestResponseWriterRecorderDisabledByAPIKeyPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	httpRecorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(httpRecorder)

	wrapper := NewResponseWriterWrapper(
		c.Writer,
		nil,
		responseWriterRecorderStub{},
		&RequestInfo{},
		c,
	)
	if !wrapper.isRecorderEnabled() {
		t.Fatal("recorder should be enabled before api key policy is attached")
	}

	c.Set(apiKeyPolicyContextKey, &config.APIKeyPolicy{SessionTrajectoryDisabled: true})
	if wrapper.isRecorderEnabled() {
		t.Fatal("recorder should be disabled when api key policy disables session trajectory")
	}
}

func TestResponseWriterFinalizeSkipsRecordWhenAPIKeyPolicyDisablesSessionTrajectory(t *testing.T) {
	gin.SetMode(gin.TestMode)

	disabledRecorder := &countingResponseWriterRecorder{}
	disabledHTTPRecorder := httptest.NewRecorder()
	disabledCtx, _ := gin.CreateTestContext(disabledHTTPRecorder)
	disabledCtx.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	disabledCtx.Set(apiKeyPolicyContextKey, &config.APIKeyPolicy{SessionTrajectoryDisabled: true})
	disabledWrapper := NewResponseWriterWrapper(
		disabledCtx.Writer,
		nil,
		disabledRecorder,
		&RequestInfo{Method: "POST", URL: "/v1/messages", RequestID: "req-disabled", Timestamp: time.Now()},
		disabledCtx,
	)
	if err := disabledWrapper.Finalize(disabledCtx); err != nil {
		t.Fatalf("Finalize(disabled) error = %v", err)
	}
	if got := disabledRecorder.count.Load(); got != 0 {
		t.Fatalf("disabled recorder calls = %d, want 0", got)
	}

	enabledRecorder := &countingResponseWriterRecorder{}
	enabledHTTPRecorder := httptest.NewRecorder()
	enabledCtx, _ := gin.CreateTestContext(enabledHTTPRecorder)
	enabledCtx.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	enabledCtx.Set(apiKeyPolicyContextKey, &config.APIKeyPolicy{})
	enabledWrapper := NewResponseWriterWrapper(
		enabledCtx.Writer,
		nil,
		enabledRecorder,
		&RequestInfo{Method: "POST", URL: "/v1/messages", RequestID: "req-enabled", Timestamp: time.Now()},
		enabledCtx,
	)
	if err := enabledWrapper.Finalize(enabledCtx); err != nil {
		t.Fatalf("Finalize(enabled) error = %v", err)
	}
	if got := enabledRecorder.count.Load(); got != 1 {
		t.Fatalf("enabled recorder calls = %d, want 1", got)
	}
}
