package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestCodexExecuteProviderFastModeSetsFastServiceTier(t *testing.T) {
	t.Parallel()

	var seenBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		seenBody = append([]byte(nil), body...)

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\"}\n\n"))
	}))
	defer srv.Close()

	cfg := &config.Config{
		CodexKey: []config.CodexKey{
			{
				APIKey:   "test-key",
				BaseURL:  srv.URL,
				FastMode: true,
			},
		},
	}
	exec := NewCodexExecutor(cfg)
	auth := &cliproxyauth.Auth{
		Attributes: map[string]string{
			"api_key":  "test-key",
			"base_url": srv.URL,
		},
	}
	req := cliproxyexecutor.Request{
		Model:   "gpt-5.4",
		Payload: []byte(`{"input":"hi","service_tier":"priority"}`),
	}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("codex")}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := exec.Execute(ctx, auth, req, opts); err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if got := gjson.GetBytes(seenBody, "service_tier").String(); got != "fast" {
		t.Fatalf("service_tier = %q, want %q (payload=%s)", got, "fast", string(seenBody))
	}
}

func TestApplyConfiguredCodexServiceTierLeavesBodyUnchangedWhenFastModeDisabled(t *testing.T) {
	t.Parallel()

	exec := NewCodexExecutor(&config.Config{
		CodexKey: []config.CodexKey{
			{
				APIKey:   "test-key",
				BaseURL:  "https://example.com",
				FastMode: false,
			},
		},
	})
	auth := &cliproxyauth.Auth{
		Attributes: map[string]string{
			"api_key":  "test-key",
			"base_url": "https://example.com",
		},
	}

	body := []byte(`{"service_tier":"priority"}`)
	got := exec.applyConfiguredCodexServiceTier(body, auth)

	if serviceTier := gjson.GetBytes(got, "service_tier").String(); serviceTier != "priority" {
		t.Fatalf("service_tier = %q, want priority", serviceTier)
	}
}

func TestCodexExecuteStream_ReturnsErrorWhenStreamEndsBeforeCompleted(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_partial\",\"model\":\"gpt-5.4\",\"status\":\"in_progress\"}}\n\n"))
	}))
	defer srv.Close()

	cfg := &config.Config{
		CodexKey: []config.CodexKey{
			{
				APIKey:  "test-key",
				BaseURL: srv.URL,
			},
		},
	}
	exec := NewCodexExecutor(cfg)
	auth := &cliproxyauth.Auth{
		Attributes: map[string]string{
			"api_key":  "test-key",
			"base_url": srv.URL,
		},
	}
	req := cliproxyexecutor.Request{
		Model:   "gpt-5.4",
		Payload: []byte(`{"stream":true,"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}]}`),
	}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := exec.ExecuteStream(ctx, auth, req, opts)
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}

	var chunks []cliproxyexecutor.StreamChunk
	for chunk := range result.Chunks {
		chunks = append(chunks, chunk)
	}
	if len(chunks) == 0 {
		t.Fatal("expected at least one streamed chunk before terminal error")
	}
	if got := string(chunks[0].Payload); !strings.Contains(got, "response.created") {
		t.Fatalf("first chunk = %q, want initial streamed payload", got)
	}
	last := chunks[len(chunks)-1]
	if last.Err == nil {
		t.Fatalf("expected terminal error when response.completed is missing; chunks=%d", len(chunks))
	}
	if !strings.Contains(last.Err.Error(), "stream closed before response.completed") {
		t.Fatalf("terminal error = %v, want missing response.completed", last.Err)
	}
}
