package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/logging"
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

func TestCodexExecute_ReturnsResponseIncompleteReason(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_incomplete\",\"model\":\"gpt-5.5\",\"status\":\"in_progress\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.incomplete\",\"response\":{\"id\":\"resp_incomplete\",\"model\":\"gpt-5.5\",\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n"))
	}))
	defer srv.Close()

	exec := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Attributes: map[string]string{
			"api_key":  "test-key",
			"base_url": srv.URL,
		},
	}
	req := cliproxyexecutor.Request{
		Model:   "gpt-5.5",
		Payload: []byte(`{"input":"hi"}`),
	}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("codex")}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := exec.Execute(ctx, auth, req, opts)
	if err == nil {
		t.Fatal("expected response.incomplete error")
	}
	for _, want := range []string{"response.incomplete", "max_output_tokens"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v, want %q", err, want)
		}
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

func TestCodexExecuteStream_ReturnsResponseIncompleteReason(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_incomplete\",\"model\":\"gpt-5.5\",\"status\":\"in_progress\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.incomplete\",\"response\":{\"id\":\"resp_incomplete\",\"model\":\"gpt-5.5\",\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n"))
	}))
	defer srv.Close()

	exec := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Attributes: map[string]string{
			"api_key":  "test-key",
			"base_url": srv.URL,
		},
	}
	req := cliproxyexecutor.Request{
		Model:   "gpt-5.5",
		Payload: []byte(`{"stream":true,"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}]}`),
	}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := exec.ExecuteStream(ctx, auth, req, opts)
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	var terminalErr error
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			terminalErr = chunk.Err
		}
	}
	if terminalErr == nil {
		t.Fatal("expected response.incomplete terminal error")
	}
	for _, want := range []string{"response.incomplete", "max_output_tokens"} {
		if !strings.Contains(terminalErr.Error(), want) {
			t.Fatalf("terminal error = %v, want %q", terminalErr, want)
		}
	}
}

func TestCodexExecuteStream_AcceptsResponseDoneAsCompleted(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_done\",\"model\":\"gpt-5.5\",\"status\":\"in_progress\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.content_part.added\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.content_part.done\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.done\",\"response\":{\"id\":\"resp_done\",\"model\":\"gpt-5.5\",\"status\":\"completed\",\"usage\":{\"input_tokens\":2,\"output_tokens\":3,\"total_tokens\":5}}}\n\n"))
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
		Model:   "gpt-5.5",
		Payload: []byte(`{"stream":true,"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}]}`),
	}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := exec.ExecuteStream(ctx, auth, req, opts)
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}

	var out strings.Builder
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("unexpected terminal error: %v", chunk.Err)
		}
		out.Write(chunk.Payload)
	}
	got := out.String()
	for _, want := range []string{
		"\"type\":\"response.created\"",
		"\"type\":\"response.output_text.delta\"",
		"\"delta\":\"ok\"",
		"\"type\":\"response.completed\"",
		"\"input_tokens\":2",
		"\"output_tokens\":3",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("translated stream missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\"type\":\"response.done\"") {
		t.Fatalf("response.done was not normalized:\n%s", got)
	}
}

func TestCodexExecuteStream_RawSSEDiagnosticsWritesUnnormalizedResponseDone(t *testing.T) {
	logDir := t.TempDir()
	t.Setenv(codexRawSSELogDirEnv, logDir)
	t.Setenv(codexRawSSEMaxBytesEnv, "1048576")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_done\",\"model\":\"gpt-5.5\",\"status\":\"in_progress\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.done\",\"response\":{\"id\":\"resp_done\",\"model\":\"gpt-5.5\",\"status\":\"completed\",\"usage\":{\"input_tokens\":2,\"output_tokens\":3,\"total_tokens\":5}}}\n\n"))
	}))
	defer srv.Close()

	exec := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Attributes: map[string]string{
			"api_key":  "test-key",
			"base_url": srv.URL,
		},
	}
	req := cliproxyexecutor.Request{
		Model:   "gpt-5.5",
		Payload: []byte(`{"stream":true,"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}]}`),
	}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")}

	ctx, cancel := context.WithTimeout(logging.WithRequestID(context.Background(), "raw-sse-test"), 5*time.Second)
	defer cancel()

	result, err := exec.ExecuteStream(ctx, auth, req, opts)
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("unexpected terminal error: %v", chunk.Err)
		}
	}

	content := readSingleRawSSELog(t, logDir)
	if !strings.Contains(content, "# request_id: raw-sse-test") {
		t.Fatalf("raw SSE log missing request id:\n%s", content)
	}
	if !strings.Contains(content, `"type":"response.done"`) {
		t.Fatalf("raw SSE log missing unnormalized response.done:\n%s", content)
	}
	if strings.Contains(content, `"type":"response.completed"`) {
		t.Fatalf("raw SSE log should not contain normalized response.completed:\n%s", content)
	}
	if !strings.Contains(content, "# saw_completion_event: true") {
		t.Fatalf("raw SSE log missing completion marker:\n%s", content)
	}
	if !strings.Contains(content, "# saw_terminal_event: true") || !strings.Contains(content, "# terminal_event: response.done") {
		t.Fatalf("raw SSE log missing terminal marker:\n%s", content)
	}
}

func TestCodexExecuteStream_RawSSEDiagnosticsMarksMissingCompletion(t *testing.T) {
	logDir := t.TempDir()
	t.Setenv(codexRawSSELogDirEnv, logDir)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_partial\",\"model\":\"gpt-5.5\",\"status\":\"in_progress\"}}\n\n"))
	}))
	defer srv.Close()

	exec := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Attributes: map[string]string{
			"api_key":  "test-key",
			"base_url": srv.URL,
		},
	}
	req := cliproxyexecutor.Request{
		Model:   "gpt-5.5",
		Payload: []byte(`{"stream":true,"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}]}`),
	}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")}

	ctx, cancel := context.WithTimeout(logging.WithRequestID(context.Background(), "raw-sse-incomplete"), 5*time.Second)
	defer cancel()

	result, err := exec.ExecuteStream(ctx, auth, req, opts)
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	var terminalErr error
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			terminalErr = chunk.Err
		}
	}
	if terminalErr == nil || !strings.Contains(terminalErr.Error(), "stream closed before response.completed") {
		t.Fatalf("terminal error = %v, want missing completion", terminalErr)
	}

	content := readSingleRawSSELog(t, logDir)
	if !strings.Contains(content, `"type":"response.created"`) {
		t.Fatalf("raw SSE log missing response.created:\n%s", content)
	}
	if !strings.Contains(content, "# eof: true") || !strings.Contains(content, "# saw_completion_event: false") {
		t.Fatalf("raw SSE log missing EOF diagnostic markers:\n%s", content)
	}
	if !strings.Contains(content, "# saw_terminal_event: false") {
		t.Fatalf("raw SSE log should mark missing terminal event:\n%s", content)
	}
}

func TestCodexExecuteStream_RawSSEDiagnosticsMarksResponseIncomplete(t *testing.T) {
	logDir := t.TempDir()
	t.Setenv(codexRawSSELogDirEnv, logDir)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_partial\",\"model\":\"gpt-5.5\",\"status\":\"in_progress\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.incomplete\",\"response\":{\"id\":\"resp_partial\",\"model\":\"gpt-5.5\",\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n"))
	}))
	defer srv.Close()

	exec := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Attributes: map[string]string{
			"api_key":  "test-key",
			"base_url": srv.URL,
		},
	}
	req := cliproxyexecutor.Request{
		Model:   "gpt-5.5",
		Payload: []byte(`{"stream":true,"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}]}`),
	}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")}

	ctx, cancel := context.WithTimeout(logging.WithRequestID(context.Background(), "raw-sse-response-incomplete"), 5*time.Second)
	defer cancel()

	result, err := exec.ExecuteStream(ctx, auth, req, opts)
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	var terminalErr error
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			terminalErr = chunk.Err
		}
	}
	if terminalErr == nil || !strings.Contains(terminalErr.Error(), "max_output_tokens") {
		t.Fatalf("terminal error = %v, want response.incomplete reason", terminalErr)
	}

	content := readSingleRawSSELog(t, logDir)
	for _, want := range []string{
		`"type":"response.incomplete"`,
		"# eof: true",
		"# saw_completion_event: false",
		"# saw_terminal_event: true",
		"# terminal_event: response.incomplete",
		"# incomplete_reason: max_output_tokens",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("raw SSE log missing %q:\n%s", want, content)
		}
	}
}

func readSingleRawSSELog(t *testing.T, dir string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "codex-raw-sse-*.log"))
	if err != nil {
		t.Fatalf("glob raw SSE logs: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("raw SSE log count = %d, want 1; matches=%v", len(matches), matches)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read raw SSE log: %v", err)
	}
	return string(data)
}
