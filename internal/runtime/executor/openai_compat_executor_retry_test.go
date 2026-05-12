package executor

import (
	"net/http"
	"testing"
	"time"
)

func TestParseOpenAICompatRetryAfterFromHeaderSeconds(t *testing.T) {
	headers := http.Header{"Retry-After": []string{"42"}}
	got := parseOpenAICompatRetryAfter(http.StatusTooManyRequests, headers, nil, time.Now())
	if got == nil {
		t.Fatal("expected retryAfter, got nil")
	}
	if *got != 42*time.Second {
		t.Fatalf("retryAfter = %v, want 42s", *got)
	}
}

func TestParseOpenAICompatRetryAfterFromModelCooldownBody(t *testing.T) {
	body := []byte(`{"error":{"code":"model_cooldown","message":"All credentials are cooling down","reset_seconds":298294}}`)
	got := parseOpenAICompatRetryAfter(http.StatusTooManyRequests, nil, body, time.Now())
	if got == nil {
		t.Fatal("expected retryAfter, got nil")
	}
	if *got != 298294*time.Second {
		t.Fatalf("retryAfter = %v, want 298294s", *got)
	}
}

func TestParseOpenAICompatRetryAfterFromResetTimestamp(t *testing.T) {
	now := time.Unix(1000, 0)
	body := []byte(`{"error":{"resets_at":1123}}`)
	got := parseOpenAICompatRetryAfter(http.StatusTooManyRequests, nil, body, now)
	if got == nil {
		t.Fatal("expected retryAfter, got nil")
	}
	if *got != 123*time.Second {
		t.Fatalf("retryAfter = %v, want 123s", *got)
	}
}

func TestParseOpenAICompatRetryAfterIgnoresNonCooldownStatus(t *testing.T) {
	headers := http.Header{"Retry-After": []string{"42"}}
	if got := parseOpenAICompatRetryAfter(http.StatusInternalServerError, headers, nil, time.Now()); got != nil {
		t.Fatalf("retryAfter = %v, want nil", *got)
	}
}
