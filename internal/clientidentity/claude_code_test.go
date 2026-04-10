package clientidentity

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsClaudeCodeRequest(t *testing.T) {
	tests := []struct {
		name string
		path string
		ua   string
		want bool
	}{
		{
			name: "claude messages request",
			path: "/v1/messages",
			ua:   "claude-cli/2.1.77 (external, cli)",
			want: true,
		},
		{
			name: "claude models request",
			path: "/v1/models",
			ua:   "claude-cli/2.1.77 (external, cli)",
			want: true,
		},
		{
			name: "generic curl request",
			path: "/v1/messages",
			ua:   "curl/8.7.1",
			want: false,
		},
		{
			name: "codex vscode request",
			path: "/v1/messages",
			ua:   "codex_exec/0.98.0 (Mac OS 26.3.0; arm64) vscode/1.112.0",
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, nil)
			req.Header.Set("User-Agent", tc.ua)
			if got := IsClaudeCodeRequest(req); got != tc.want {
				t.Fatalf("IsClaudeCodeRequest() = %v, want %v", got, tc.want)
			}
		})
	}
}
