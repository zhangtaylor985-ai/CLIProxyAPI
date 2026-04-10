package clientidentity

import (
	"net/http"
	"strings"
)

// IsClaudeCodeRequest applies a conservative Claude Code fingerprint check.
// It is intentionally narrow for the current v1 scope: real Claude CLI traffic
// should pass, while generic API clients should not.
func IsClaudeCodeRequest(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	userAgent := strings.ToLower(strings.TrimSpace(r.Header.Get("User-Agent")))
	if !strings.HasPrefix(userAgent, "claude-cli/") {
		return false
	}

	switch strings.TrimSpace(r.URL.Path) {
	case "/v1/messages", "/v1/models":
		return true
	default:
		return false
	}
}
