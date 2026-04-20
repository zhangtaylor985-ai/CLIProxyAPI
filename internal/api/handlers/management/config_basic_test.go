package management

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/tidwall/gjson"
)

func TestGetClaudeCodeOnlyEnabled(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	handler := &Handler{
		cfg: &config.Config{
			SDKConfig: config.SDKConfig{
				ClaudeCodeOnlyEnabled: true,
			},
		},
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/claude-code-only-enabled", nil)

	handler.GetClaudeCodeOnlyEnabled(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := gjson.GetBytes(recorder.Body.Bytes(), "claude-code-only-enabled").Bool(); !got {
		t.Fatalf("claude-code-only-enabled = %v, want true", got)
	}
}

func TestGetClaudeToGPTReasoningEffort(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	handler := &Handler{
		cfg: &config.Config{
			SDKConfig: config.SDKConfig{
				ClaudeToGPTReasoningEffort: "high",
			},
		},
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/claude-to-gpt-reasoning-effort", nil)

	handler.GetClaudeToGPTReasoningEffort(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := gjson.GetBytes(recorder.Body.Bytes(), "claude-to-gpt-reasoning-effort").String(); got != "high" {
		t.Fatalf("claude-to-gpt-reasoning-effort = %q, want %q", got, "high")
	}
}

func TestPutClaudeToGPTReasoningEffort(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	tmpFile := t.TempDir() + "/config.yaml"
	if err := os.WriteFile(tmpFile, []byte("claude-to-gpt-reasoning-effort: high\n"), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	handler := &Handler{
		cfg: &config.Config{
			SDKConfig: config.SDKConfig{
				ClaudeToGPTReasoningEffort: "high",
			},
		},
		configFilePath: tmpFile,
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPut,
		"/v0/management/claude-to-gpt-reasoning-effort",
		bytes.NewBufferString(`{"value":"low"}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.PutClaudeToGPTReasoningEffort(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if handler.cfg.ClaudeToGPTReasoningEffort != "low" {
		t.Fatalf("handler cfg effort = %q, want %q", handler.cfg.ClaudeToGPTReasoningEffort, "low")
	}

	loaded, err := config.LoadConfigOptional(tmpFile, false)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if loaded.ClaudeToGPTReasoningEffort != "low" {
		t.Fatalf("persisted effort = %q, want %q", loaded.ClaudeToGPTReasoningEffort, "low")
	}
}

func TestPutClaudeToGPTReasoningEffort_RejectsInvalidValue(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	handler := &Handler{
		cfg: &config.Config{
			SDKConfig: config.SDKConfig{
				ClaudeToGPTReasoningEffort: "high",
			},
		},
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPut,
		"/v0/management/claude-to-gpt-reasoning-effort",
		bytes.NewBufferString(`{"value":"invalid-effort"}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.PutClaudeToGPTReasoningEffort(ctx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if handler.cfg.ClaudeToGPTReasoningEffort != "high" {
		t.Fatalf("handler cfg effort = %q, want unchanged %q", handler.cfg.ClaudeToGPTReasoningEffort, "high")
	}
}
