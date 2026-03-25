package policy

import "testing"

func TestDowngradeClaudeOpus46(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		changed bool
	}{
		{"claude-opus-4-6", "claude-opus-4-5-20251101", true},
		{"claude-opus-4-6-thinking", "claude-opus-4-5-20251101-thinking", true},
		{"claude-opus-4-6(8192)", "claude-opus-4-5-20251101(8192)", true},
		{"claude-opus-4-6-thinking(high)", "claude-opus-4-5-20251101-thinking(high)", true},
		{"claude-sonnet-4-5", "claude-sonnet-4-5", false},
	}
	for _, tt := range tests {
		got, changed := DowngradeClaudeOpus46(tt.in)
		if changed != tt.changed {
			t.Fatalf("DowngradeClaudeOpus46(%q) changed=%v, want %v", tt.in, changed, tt.changed)
		}
		if got != tt.want {
			t.Fatalf("DowngradeClaudeOpus46(%q)=%q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNormaliseModelKey_StripsSuffix(t *testing.T) {
	if got := NormaliseModelKey("claude-opus-4-6(8192)"); got != "claude-opus-4-6" {
		t.Fatalf("NormaliseModelKey got %q", got)
	}
}

func TestMatchWildcard(t *testing.T) {
	tests := []struct {
		pat  string
		val  string
		want bool
	}{
		{"claude-*", "claude-opus-4-6", true},
		{"*-thinking", "claude-opus-4-5-thinking", true},
		{"claude-opus-4-6", "claude-opus-4-6", true},
		{"claude-opus-4-6", "claude-opus-4-5", false},
	}
	for _, tt := range tests {
		if got := MatchWildcard(tt.pat, tt.val); got != tt.want {
			t.Fatalf("MatchWildcard(%q,%q)=%v, want %v", tt.pat, tt.val, got, tt.want)
		}
	}
}
