package management

import (
	"net/http"
	"net/http/httptest"
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
