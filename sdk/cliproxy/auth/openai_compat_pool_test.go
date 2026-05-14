package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

type retryAfterTestError struct {
	statusCode int
	message    string
	retryAfter time.Duration
}

func (e retryAfterTestError) Error() string { return e.message }
func (e retryAfterTestError) StatusCode() int {
	return e.statusCode
}
func (e retryAfterTestError) RetryAfter() *time.Duration {
	return &e.retryAfter
}

type openAICompatPoolExecutor struct {
	id string

	mu                sync.Mutex
	executeModels     []string
	countModels       []string
	streamModels      []string
	executeErrors     map[string]error
	countErrors       map[string]error
	streamFirstErrors map[string]error
	streamPayloads    map[string][]cliproxyexecutor.StreamChunk
}

func (e *openAICompatPoolExecutor) Identifier() string { return e.id }

func (e *openAICompatPoolExecutor) Execute(ctx context.Context, auth *Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	_ = ctx
	_ = auth
	_ = opts
	e.mu.Lock()
	e.executeModels = append(e.executeModels, req.Model)
	err := e.executeErrors[req.Model]
	e.mu.Unlock()
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}
	return cliproxyexecutor.Response{Payload: []byte(req.Model)}, nil
}

func (e *openAICompatPoolExecutor) ExecuteStream(ctx context.Context, auth *Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	_ = ctx
	_ = auth
	_ = opts
	e.mu.Lock()
	e.streamModels = append(e.streamModels, req.Model)
	err := e.streamFirstErrors[req.Model]
	payloadChunks, hasCustomChunks := e.streamPayloads[req.Model]
	chunks := append([]cliproxyexecutor.StreamChunk(nil), payloadChunks...)
	e.mu.Unlock()
	ch := make(chan cliproxyexecutor.StreamChunk, max(1, len(chunks)))
	if err != nil {
		ch <- cliproxyexecutor.StreamChunk{Err: err}
		close(ch)
		return &cliproxyexecutor.StreamResult{Headers: http.Header{"X-Model": {req.Model}}, Chunks: ch}, nil
	}
	if !hasCustomChunks {
		ch <- cliproxyexecutor.StreamChunk{Payload: []byte(req.Model)}
	} else {
		for _, chunk := range chunks {
			ch <- chunk
		}
	}
	close(ch)
	return &cliproxyexecutor.StreamResult{Headers: http.Header{"X-Model": {req.Model}}, Chunks: ch}, nil
}

func (e *openAICompatPoolExecutor) Refresh(_ context.Context, auth *Auth) (*Auth, error) {
	return auth, nil
}

func (e *openAICompatPoolExecutor) CountTokens(ctx context.Context, auth *Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	_ = ctx
	_ = auth
	_ = opts
	e.mu.Lock()
	e.countModels = append(e.countModels, req.Model)
	err := e.countErrors[req.Model]
	e.mu.Unlock()
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}
	return cliproxyexecutor.Response{Payload: []byte(req.Model)}, nil
}

func (e *openAICompatPoolExecutor) HttpRequest(ctx context.Context, auth *Auth, req *http.Request) (*http.Response, error) {
	_ = ctx
	_ = auth
	_ = req
	return nil, &Error{HTTPStatus: http.StatusNotImplemented, Message: "HttpRequest not implemented"}
}

func (e *openAICompatPoolExecutor) ExecuteModels() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.executeModels))
	copy(out, e.executeModels)
	return out
}

func (e *openAICompatPoolExecutor) CountModels() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.countModels))
	copy(out, e.countModels)
	return out
}

func (e *openAICompatPoolExecutor) StreamModels() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.streamModels))
	copy(out, e.streamModels)
	return out
}

type authScopedOpenAICompatPoolExecutor struct {
	id string

	mu           sync.Mutex
	executeCalls []string
}

func (e *authScopedOpenAICompatPoolExecutor) Identifier() string { return e.id }

func (e *authScopedOpenAICompatPoolExecutor) Execute(_ context.Context, auth *Auth, req cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	call := auth.ID + "|" + req.Model
	e.mu.Lock()
	e.executeCalls = append(e.executeCalls, call)
	e.mu.Unlock()
	return cliproxyexecutor.Response{Payload: []byte(call)}, nil
}

func (e *authScopedOpenAICompatPoolExecutor) ExecuteStream(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	return nil, &Error{HTTPStatus: http.StatusNotImplemented, Message: "ExecuteStream not implemented"}
}

func (e *authScopedOpenAICompatPoolExecutor) Refresh(_ context.Context, auth *Auth) (*Auth, error) {
	return auth, nil
}

func (e *authScopedOpenAICompatPoolExecutor) CountTokens(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, &Error{HTTPStatus: http.StatusNotImplemented, Message: "CountTokens not implemented"}
}

func (e *authScopedOpenAICompatPoolExecutor) HttpRequest(context.Context, *Auth, *http.Request) (*http.Response, error) {
	return nil, &Error{HTTPStatus: http.StatusNotImplemented, Message: "HttpRequest not implemented"}
}

func (e *authScopedOpenAICompatPoolExecutor) ExecuteCalls() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.executeCalls))
	copy(out, e.executeCalls)
	return out
}

type authSequenceStreamExecutor struct {
	id string

	mu      sync.Mutex
	calls   []string
	streams map[string][][]cliproxyexecutor.StreamChunk
}

func (e *authSequenceStreamExecutor) Identifier() string { return e.id }

func (e *authSequenceStreamExecutor) Execute(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, &Error{HTTPStatus: http.StatusNotImplemented, Message: "Execute not implemented"}
}

func (e *authSequenceStreamExecutor) ExecuteStream(_ context.Context, auth *Auth, req cliproxyexecutor.Request, _ cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	key := auth.ID + "|" + req.Model
	e.mu.Lock()
	e.calls = append(e.calls, key)
	callIndex := 0
	for _, call := range e.calls[:len(e.calls)-1] {
		if call == key {
			callIndex++
		}
	}
	streams := e.streams[key]
	var chunks []cliproxyexecutor.StreamChunk
	if callIndex < len(streams) {
		chunks = append([]cliproxyexecutor.StreamChunk(nil), streams[callIndex]...)
	} else {
		chunks = []cliproxyexecutor.StreamChunk{{Payload: []byte(req.Model)}}
	}
	e.mu.Unlock()
	ch := make(chan cliproxyexecutor.StreamChunk, max(1, len(chunks)))
	for _, chunk := range chunks {
		ch <- chunk
	}
	close(ch)
	return &cliproxyexecutor.StreamResult{Headers: http.Header{"X-Model": {req.Model}}, Chunks: ch}, nil
}

func (e *authSequenceStreamExecutor) Refresh(_ context.Context, auth *Auth) (*Auth, error) {
	return auth, nil
}

func (e *authSequenceStreamExecutor) CountTokens(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, &Error{HTTPStatus: http.StatusNotImplemented, Message: "CountTokens not implemented"}
}

func (e *authSequenceStreamExecutor) HttpRequest(context.Context, *Auth, *http.Request) (*http.Response, error) {
	return nil, &Error{HTTPStatus: http.StatusNotImplemented, Message: "HttpRequest not implemented"}
}

func (e *authSequenceStreamExecutor) Calls() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.calls))
	copy(out, e.calls)
	return out
}

func newOpenAICompatPoolTestManager(t *testing.T, alias string, models []internalconfig.OpenAICompatibilityModel, executor *openAICompatPoolExecutor) *Manager {
	t.Helper()
	cfg := &internalconfig.Config{
		OpenAICompatibility: []internalconfig.OpenAICompatibility{{
			Name:   "pool",
			Models: models,
		}},
	}
	m := NewManager(nil, nil, nil)
	m.SetConfig(cfg)
	if executor == nil {
		executor = &openAICompatPoolExecutor{id: "pool"}
	}
	m.RegisterExecutor(executor)

	auth := &Auth{
		ID:       "pool-auth-" + t.Name(),
		Provider: "pool",
		Status:   StatusActive,
		Attributes: map[string]string{
			"api_key":      "test-key",
			"compat_name":  "pool",
			"provider_key": "pool",
		},
	}
	if _, err := m.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(auth.ID, "pool", []*registry.ModelInfo{{ID: alias}})
	t.Cleanup(func() {
		reg.UnregisterClient(auth.ID)
	})
	return m
}

func readOpenAICompatStreamPayload(t *testing.T, streamResult *cliproxyexecutor.StreamResult) string {
	t.Helper()
	if streamResult == nil {
		t.Fatal("expected stream result")
	}
	var payload []byte
	for chunk := range streamResult.Chunks {
		if chunk.Err != nil {
			t.Fatalf("unexpected stream error: %v", chunk.Err)
		}
		payload = append(payload, chunk.Payload...)
	}
	return string(payload)
}

func TestManagerExecuteCount_OpenAICompatAliasPoolStopsOnInvalidRequest(t *testing.T) {
	alias := "claude-opus-4.66"
	invalidErr := &Error{HTTPStatus: http.StatusUnprocessableEntity, Message: "unprocessable entity"}
	executor := &openAICompatPoolExecutor{
		id:          "pool",
		countErrors: map[string]error{"qwen3.5-plus": invalidErr},
	}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "qwen3.5-plus", Alias: alias},
		{Name: "glm-5", Alias: alias},
	}, executor)

	_, err := m.ExecuteCount(context.Background(), []string{"pool"}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
	if err == nil || err.Error() != invalidErr.Error() {
		t.Fatalf("execute count error = %v, want %v", err, invalidErr)
	}
	got := executor.CountModels()
	if len(got) != 1 || got[0] != "qwen3.5-plus" {
		t.Fatalf("count calls = %v, want only first invalid model", got)
	}
}
func TestResolveModelAliasPoolFromConfigModels(t *testing.T) {
	models := []modelAliasEntry{
		internalconfig.OpenAICompatibilityModel{Name: "qwen3.5-plus", Alias: "claude-opus-4.66"},
		internalconfig.OpenAICompatibilityModel{Name: "glm-5", Alias: "claude-opus-4.66"},
		internalconfig.OpenAICompatibilityModel{Name: "kimi-k2.5", Alias: "claude-opus-4.66"},
	}
	got := resolveModelAliasPoolFromConfigModels("claude-opus-4.66(8192)", models)
	want := []string{"qwen3.5-plus(8192)", "glm-5(8192)", "kimi-k2.5(8192)"}
	if len(got) != len(want) {
		t.Fatalf("pool len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pool[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestManagerExecute_OpenAICompatAliasPoolRotatesWithinAuth(t *testing.T) {
	alias := "claude-opus-4.66"
	executor := &openAICompatPoolExecutor{id: "pool"}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "qwen3.5-plus", Alias: alias},
		{Name: "glm-5", Alias: alias},
	}, executor)

	for i := 0; i < 3; i++ {
		resp, err := m.Execute(context.Background(), []string{"pool"}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
		if err != nil {
			t.Fatalf("execute %d: %v", i, err)
		}
		if len(resp.Payload) == 0 {
			t.Fatalf("execute %d returned empty payload", i)
		}
	}

	got := executor.ExecuteModels()
	want := []string{"qwen3.5-plus", "glm-5", "qwen3.5-plus"}
	if len(got) != len(want) {
		t.Fatalf("execute calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("execute call %d model = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestManagerExecute_OpenAICompatAliasPoolStopsOnBadRequest(t *testing.T) {
	alias := "claude-opus-4.66"
	invalidErr := &Error{HTTPStatus: http.StatusBadRequest, Message: "invalid_request_error: malformed payload"}
	executor := &openAICompatPoolExecutor{
		id:            "pool",
		executeErrors: map[string]error{"qwen3.5-plus": invalidErr},
	}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "qwen3.5-plus", Alias: alias},
		{Name: "glm-5", Alias: alias},
	}, executor)

	_, err := m.Execute(context.Background(), []string{"pool"}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
	if err == nil || err.Error() != invalidErr.Error() {
		t.Fatalf("execute error = %v, want %v", err, invalidErr)
	}
	got := executor.ExecuteModels()
	if len(got) != 1 || got[0] != "qwen3.5-plus" {
		t.Fatalf("execute calls = %v, want only first invalid model", got)
	}
}

func TestManagerExecute_OpenAICompatAliasPoolFallsBackOnModelSupportBadRequest(t *testing.T) {
	alias := "claude-opus-4.66"
	modelSupportErr := &Error{
		HTTPStatus: http.StatusBadRequest,
		Message:    "invalid_request_error: The requested model is not supported.",
	}
	executor := &openAICompatPoolExecutor{
		id:            "pool",
		executeErrors: map[string]error{"qwen3.5-plus": modelSupportErr},
	}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "qwen3.5-plus", Alias: alias},
		{Name: "glm-5", Alias: alias},
	}, executor)

	resp, err := m.Execute(context.Background(), []string{"pool"}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
	if err != nil {
		t.Fatalf("execute error = %v, want fallback success", err)
	}
	if string(resp.Payload) != "glm-5" {
		t.Fatalf("payload = %q, want %q", string(resp.Payload), "glm-5")
	}
	got := executor.ExecuteModels()
	want := []string{"qwen3.5-plus", "glm-5"}
	if len(got) != len(want) {
		t.Fatalf("execute calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("execute call %d model = %q, want %q", i, got[i], want[i])
		}
	}

	updated, ok := m.GetByID("pool-auth-" + t.Name())
	if !ok || updated == nil {
		t.Fatalf("expected auth to remain registered")
	}
	state := updated.ModelStates["qwen3.5-plus"]
	if state == nil {
		t.Fatalf("expected suspended upstream model state")
	}
	if !state.Unavailable || state.NextRetryAfter.IsZero() {
		t.Fatalf("expected upstream model suspension, got %+v", state)
	}
}

func TestManagerExecute_OpenAICompatAliasPoolFallsBackOnModelSupportUnprocessableEntity(t *testing.T) {
	alias := "claude-opus-4.66"
	modelSupportErr := &Error{
		HTTPStatus: http.StatusUnprocessableEntity,
		Message:    "The requested model is not supported.",
	}
	executor := &openAICompatPoolExecutor{
		id:            "pool",
		executeErrors: map[string]error{"qwen3.5-plus": modelSupportErr},
	}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "qwen3.5-plus", Alias: alias},
		{Name: "glm-5", Alias: alias},
	}, executor)

	resp, err := m.Execute(context.Background(), []string{"pool"}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
	if err != nil {
		t.Fatalf("execute error = %v, want fallback success", err)
	}
	if string(resp.Payload) != "glm-5" {
		t.Fatalf("payload = %q, want %q", string(resp.Payload), "glm-5")
	}
	got := executor.ExecuteModels()
	want := []string{"qwen3.5-plus", "glm-5"}
	if len(got) != len(want) {
		t.Fatalf("execute calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("execute call %d model = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestManagerExecute_OpenAICompatAliasPoolFallsBackWithinSameAuth(t *testing.T) {
	alias := "claude-opus-4.66"
	executor := &openAICompatPoolExecutor{
		id:            "pool",
		executeErrors: map[string]error{"qwen3.5-plus": &Error{HTTPStatus: http.StatusTooManyRequests, Message: "quota"}},
	}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "qwen3.5-plus", Alias: alias},
		{Name: "glm-5", Alias: alias},
	}, executor)

	resp, err := m.Execute(context.Background(), []string{"pool"}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if string(resp.Payload) != "glm-5" {
		t.Fatalf("payload = %q, want %q", string(resp.Payload), "glm-5")
	}
	got := executor.ExecuteModels()
	want := []string{"qwen3.5-plus", "glm-5"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("execute call %d model = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestManagerExecuteStream_OpenAICompatAliasPoolRetriesOnEmptyBootstrap(t *testing.T) {
	alias := "claude-opus-4.66"
	executor := &openAICompatPoolExecutor{
		id: "pool",
		streamPayloads: map[string][]cliproxyexecutor.StreamChunk{
			"qwen3.5-plus": {},
		},
	}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "qwen3.5-plus", Alias: alias},
		{Name: "glm-5", Alias: alias},
	}, executor)

	streamResult, err := m.ExecuteStream(context.Background(), []string{"pool"}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
	if err != nil {
		t.Fatalf("execute stream: %v", err)
	}
	var payload []byte
	for chunk := range streamResult.Chunks {
		if chunk.Err != nil {
			t.Fatalf("unexpected stream error: %v", chunk.Err)
		}
		payload = append(payload, chunk.Payload...)
	}
	if string(payload) != "glm-5" {
		t.Fatalf("payload = %q, want %q", string(payload), "glm-5")
	}
	got := executor.StreamModels()
	want := []string{"qwen3.5-plus", "glm-5"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("stream call %d model = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestManagerExecuteStream_RetriesEmptyStreamSameWorkerOnce(t *testing.T) {
	previousDelay := emptyStreamBootstrapRetryDelay
	emptyStreamBootstrapRetryDelay = func(int) time.Duration { return 0 }
	t.Cleanup(func() { emptyStreamBootstrapRetryDelay = previousDelay })

	model := "gpt-5.4"
	authID := "worker05"
	executor := &authSequenceStreamExecutor{
		id: "pool",
		streams: map[string][][]cliproxyexecutor.StreamChunk{
			authID + "|" + model: {
				{},
				{{Payload: []byte("ok")}},
			},
		},
	}
	cfg := &internalconfig.Config{
		OpenAICompatibility: []internalconfig.OpenAICompatibility{{
			Name: "pool",
			Models: []internalconfig.OpenAICompatibilityModel{
				{Name: model, Alias: model},
			},
		}},
	}
	m := NewManager(nil, nil, nil)
	m.SetConfig(cfg)
	m.RegisterExecutor(executor)
	auth := &Auth{
		ID:       authID,
		Provider: "pool",
		Status:   StatusActive,
		Attributes: map[string]string{
			"api_key":      "worker05-key",
			"compat_name":  "pool",
			"provider_key": "pool",
		},
	}
	if _, err := m.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(authID, "pool", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() { reg.UnregisterClient(authID) })

	streamResult, err := m.ExecuteStream(context.Background(), []string{"pool"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{})
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	if payload := readOpenAICompatStreamPayload(t, streamResult); payload != "ok" {
		t.Fatalf("payload = %q, want ok", payload)
	}
	got := executor.Calls()
	want := []string{authID + "|" + model, authID + "|" + model}
	if len(got) != len(want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("call %d = %q, want %q", i, got[i], want[i])
		}
	}
	updated, ok := m.GetByID(authID)
	if !ok || updated == nil {
		t.Fatalf("expected auth after execution")
	}
	if state := updated.ModelStates[model]; state == nil || state.Unavailable || state.Health.ConsecutiveEmptyStreams != 0 {
		t.Fatalf("state after recovery = %+v, want available with reset empty-stream streak", state)
	}
}

func TestManagerExecuteStream_RetriesEmptyStreamChunkErrorSameWorkerOnce(t *testing.T) {
	previousDelay := emptyStreamBootstrapRetryDelay
	emptyStreamBootstrapRetryDelay = func(int) time.Duration { return 0 }
	t.Cleanup(func() { emptyStreamBootstrapRetryDelay = previousDelay })

	model := "gpt-5.4"
	authID := "worker06"
	executor := &authSequenceStreamExecutor{
		id: "pool",
		streams: map[string][][]cliproxyexecutor.StreamChunk{
			authID + "|" + model: {
				{{Err: errors.New("upstream stream closed before first payload")}},
				{{Payload: []byte("ok")}},
			},
		},
	}
	cfg := &internalconfig.Config{
		OpenAICompatibility: []internalconfig.OpenAICompatibility{{
			Name: "pool",
			Models: []internalconfig.OpenAICompatibilityModel{
				{Name: model, Alias: model},
			},
		}},
	}
	m := NewManager(nil, nil, nil)
	m.SetConfig(cfg)
	m.RegisterExecutor(executor)
	auth := &Auth{
		ID:       authID,
		Provider: "pool",
		Status:   StatusActive,
		Attributes: map[string]string{
			"api_key":      "worker06-key",
			"compat_name":  "pool",
			"provider_key": "pool",
		},
	}
	if _, err := m.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(authID, "pool", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() { reg.UnregisterClient(authID) })

	streamResult, err := m.ExecuteStream(context.Background(), []string{"pool"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{})
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	if payload := readOpenAICompatStreamPayload(t, streamResult); payload != "ok" {
		t.Fatalf("payload = %q, want ok", payload)
	}
	got := executor.Calls()
	want := []string{authID + "|" + model, authID + "|" + model}
	if len(got) != len(want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("call %d = %q, want %q", i, got[i], want[i])
		}
	}
	updated, ok := m.GetByID(authID)
	if !ok || updated == nil {
		t.Fatalf("expected auth after execution")
	}
	if state := updated.ModelStates[model]; state == nil || state.Unavailable || state.Health.ConsecutiveEmptyStreams != 0 {
		t.Fatalf("state after recovery = %+v, want available with reset empty-stream streak", state)
	}
}

func TestManagerExecuteStream_OpenAICompatAliasPoolFallsBackBeforeFirstByte(t *testing.T) {
	alias := "claude-opus-4.66"
	executor := &openAICompatPoolExecutor{
		id:                "pool",
		streamFirstErrors: map[string]error{"qwen3.5-plus": &Error{HTTPStatus: http.StatusTooManyRequests, Message: "quota"}},
	}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "qwen3.5-plus", Alias: alias},
		{Name: "glm-5", Alias: alias},
	}, executor)

	streamResult, err := m.ExecuteStream(context.Background(), []string{"pool"}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
	if err != nil {
		t.Fatalf("execute stream: %v", err)
	}
	var payload []byte
	for chunk := range streamResult.Chunks {
		if chunk.Err != nil {
			t.Fatalf("unexpected stream error: %v", chunk.Err)
		}
		payload = append(payload, chunk.Payload...)
	}
	if string(payload) != "glm-5" {
		t.Fatalf("payload = %q, want %q", string(payload), "glm-5")
	}
	got := executor.StreamModels()
	want := []string{"qwen3.5-plus", "glm-5"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("stream call %d model = %q, want %q", i, got[i], want[i])
		}
	}
	if gotHeader := streamResult.Headers.Get("X-Model"); gotHeader != "glm-5" {
		t.Fatalf("header X-Model = %q, want %q", gotHeader, "glm-5")
	}
}

func TestManagerExecuteStream_OpenAICompatAliasPoolStopsOnInvalidRequest(t *testing.T) {
	alias := "claude-opus-4.66"
	invalidErr := &Error{HTTPStatus: http.StatusUnprocessableEntity, Message: "unprocessable entity"}
	executor := &openAICompatPoolExecutor{
		id:                "pool",
		streamFirstErrors: map[string]error{"qwen3.5-plus": invalidErr},
	}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "qwen3.5-plus", Alias: alias},
		{Name: "glm-5", Alias: alias},
	}, executor)

	_, err := m.ExecuteStream(context.Background(), []string{"pool"}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
	if err == nil || err.Error() != invalidErr.Error() {
		t.Fatalf("execute stream error = %v, want %v", err, invalidErr)
	}
	got := executor.StreamModels()
	if len(got) != 1 || got[0] != "qwen3.5-plus" {
		t.Fatalf("stream calls = %v, want only first invalid model", got)
	}
}

func TestManagerMarkResult_EmptyStreamDegradesAfterConsecutiveFailures(t *testing.T) {
	model := "gpt-5.4"
	authID := "worker05-" + t.Name()
	m := NewManager(nil, nil, nil)
	m.RegisterExecutor(&authScopedOpenAICompatPoolExecutor{id: "pool"})

	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(authID, "pool", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() {
		reg.UnregisterClient(authID)
	})

	auth := &Auth{
		ID:       authID,
		Provider: "pool",
		Status:   StatusActive,
		Attributes: map[string]string{
			"api_key":      "worker-key",
			"compat_name":  "pool",
			"provider_key": "pool",
		},
	}
	if _, err := m.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	m.MarkResult(context.Background(), Result{
		AuthID:   authID,
		Provider: "pool",
		Model:    model,
		Success:  false,
		Error:    emptyStreamError(),
	})
	afterFirst, ok := m.GetByID(authID)
	if !ok || afterFirst == nil {
		t.Fatalf("expected auth after first empty stream")
	}
	firstState := afterFirst.ModelStates[model]
	if firstState == nil {
		t.Fatalf("expected model state after first empty stream")
	}
	if firstState.NextRetryAfter.After(time.Now()) {
		t.Fatalf("first empty stream NextRetryAfter = %v, want no cooldown", firstState.NextRetryAfter)
	}
	if firstState.Health.ConsecutiveEmptyStreams != 1 {
		t.Fatalf("first empty stream streak = %d, want 1", firstState.Health.ConsecutiveEmptyStreams)
	}

	m.MarkResult(context.Background(), Result{
		AuthID:   authID,
		Provider: "pool",
		Model:    model,
		Success:  false,
		Error:    emptyStreamError(),
	})
	afterSecond, ok := m.GetByID(authID)
	if !ok || afterSecond == nil {
		t.Fatalf("expected auth after second empty stream")
	}
	secondState := afterSecond.ModelStates[model]
	if secondState == nil || !secondState.Unavailable || !secondState.NextRetryAfter.After(time.Now()) {
		t.Fatalf("state after second empty stream = %+v, want temporary cooldown", secondState)
	}
	if secondState.Health.ConsecutiveEmptyStreams != 2 {
		t.Fatalf("second empty stream streak = %d, want 2", secondState.Health.ConsecutiveEmptyStreams)
	}
}

func TestManagerMarkResult_CodexWorkerCooldownAppliesToWholeAuth(t *testing.T) {
	provider := "codex-worker02-haoran"
	authID := provider + "-auth"
	model := "gpt-5.4"
	otherModel := "gpt-5.3-codex"

	m := NewManager(nil, nil, nil)
	m.RegisterExecutor(&authScopedOpenAICompatPoolExecutor{id: provider})

	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(authID, provider, []*registry.ModelInfo{{ID: model}, {ID: otherModel}})
	t.Cleanup(func() {
		reg.UnregisterClient(authID)
	})

	auth := &Auth{
		ID:       authID,
		Provider: provider,
		Status:   StatusActive,
		Attributes: map[string]string{
			"api_key":      "worker-key",
			"compat_name":  provider,
			"provider_key": provider,
		},
	}
	if _, err := m.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	retryAfter := 10 * time.Minute
	m.MarkResult(context.Background(), Result{
		AuthID:     authID,
		Provider:   provider,
		Model:      model,
		Success:    false,
		RetryAfter: &retryAfter,
		Error:      &Error{HTTPStatus: http.StatusTooManyRequests, Message: "worker cooling down"},
	})

	updated, ok := m.GetByID(authID)
	if !ok || updated == nil {
		t.Fatalf("expected auth after cooldown")
	}
	if !updated.Unavailable || !updated.NextRetryAfter.After(time.Now()) {
		t.Fatalf("auth state = %+v, want whole auth cooldown", updated)
	}
	if len(updated.ModelStates) != 0 {
		t.Fatalf("codex worker should not keep model-scoped states, got %+v", updated.ModelStates)
	}

	_, _, err := m.pickNext(context.Background(), provider, otherModel, cliproxyexecutor.Options{}, nil)
	if err == nil {
		t.Fatal("pickNext for other model error = nil, want cooldown")
	}
	var cooldownErr *modelCooldownError
	if !errors.As(err, &cooldownErr) {
		t.Fatalf("pickNext error = %T %v, want modelCooldownError", err, err)
	}
}

func TestManagerMarkResult_CodexWorkerEmptyStreamRequiresConsecutiveFailures(t *testing.T) {
	provider := "codex-worker04-sunwei"
	authID := provider + "-auth"
	model := "gpt-5.4"

	m := NewManager(nil, nil, nil)
	m.RegisterExecutor(&authScopedOpenAICompatPoolExecutor{id: provider})

	auth := &Auth{
		ID:       authID,
		Provider: provider,
		Status:   StatusActive,
		Attributes: map[string]string{
			"api_key":      "worker-key",
			"compat_name":  provider,
			"provider_key": provider,
		},
	}
	if _, err := m.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	m.MarkResult(context.Background(), Result{
		AuthID:   authID,
		Provider: provider,
		Model:    model,
		Success:  false,
		Error:    emptyStreamError(),
	})
	afterFirst, ok := m.GetByID(authID)
	if !ok || afterFirst == nil {
		t.Fatalf("expected auth after first empty stream")
	}
	if afterFirst.Unavailable || afterFirst.NextRetryAfter.After(time.Now()) {
		t.Fatalf("first empty stream auth state = %+v, want still selectable", afterFirst)
	}
	if afterFirst.Health.ConsecutiveEmptyStreams != 1 {
		t.Fatalf("first empty stream streak = %d, want 1", afterFirst.Health.ConsecutiveEmptyStreams)
	}
	if len(afterFirst.ModelStates) != 0 {
		t.Fatalf("codex worker should not keep model-scoped states, got %+v", afterFirst.ModelStates)
	}

	m.MarkResult(context.Background(), Result{
		AuthID:   authID,
		Provider: provider,
		Model:    model,
		Success:  false,
		Error:    emptyStreamError(),
	})
	afterSecond, ok := m.GetByID(authID)
	if !ok || afterSecond == nil {
		t.Fatalf("expected auth after second empty stream")
	}
	if !afterSecond.Unavailable || !afterSecond.NextRetryAfter.After(time.Now()) {
		t.Fatalf("second empty stream auth state = %+v, want whole worker cooldown", afterSecond)
	}
	if afterSecond.Health.ConsecutiveEmptyStreams != 2 {
		t.Fatalf("second empty stream streak = %d, want 2", afterSecond.Health.ConsecutiveEmptyStreams)
	}
}

func TestManagerExecuteStream_CodexWorkerEmptyStreamRetrySuccessKeepsWorkerAvailable(t *testing.T) {
	previousDelay := emptyStreamBootstrapRetryDelay
	emptyStreamBootstrapRetryDelay = func(int) time.Duration { return 0 }
	t.Cleanup(func() { emptyStreamBootstrapRetryDelay = previousDelay })

	provider := "codex-worker06-qinyi"
	model := "gpt-5.4"
	authID := provider + "-auth"
	executor := &authSequenceStreamExecutor{
		id: provider,
		streams: map[string][][]cliproxyexecutor.StreamChunk{
			authID + "|" + model: {
				{},
				{{Payload: []byte("ok")}},
			},
		},
	}
	cfg := &internalconfig.Config{
		OpenAICompatibility: []internalconfig.OpenAICompatibility{{
			Name: provider,
			Models: []internalconfig.OpenAICompatibilityModel{
				{Name: model, Alias: model},
			},
		}},
	}
	m := NewManager(nil, nil, nil)
	m.SetConfig(cfg)
	m.RegisterExecutor(executor)
	auth := &Auth{
		ID:       authID,
		Provider: provider,
		Status:   StatusActive,
		Attributes: map[string]string{
			"api_key":      "worker-key",
			"compat_name":  provider,
			"provider_key": provider,
		},
	}
	if _, err := m.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(authID, provider, []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() { reg.UnregisterClient(authID) })

	streamResult, err := m.ExecuteStream(context.Background(), []string{provider}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{})
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	if payload := readOpenAICompatStreamPayload(t, streamResult); payload != "ok" {
		t.Fatalf("payload = %q, want ok", payload)
	}
	updated, ok := m.GetByID(authID)
	if !ok || updated == nil {
		t.Fatalf("expected auth after execution")
	}
	if updated.Unavailable || updated.Health.ConsecutiveEmptyStreams != 0 {
		t.Fatalf("auth after retry success = %+v, want available with reset empty-stream streak", updated)
	}
}

func TestManagerExecuteStream_CodexWorkersFailOverToAvailableWorker(t *testing.T) {
	previousDelay := emptyStreamBootstrapRetryDelay
	emptyStreamBootstrapRetryDelay = func(int) time.Duration { return 0 }
	t.Cleanup(func() { emptyStreamBootstrapRetryDelay = previousDelay })

	model := "gpt-5.4"
	quotaProvider := "codex-worker02-haoran"
	emptyProvider := "codex-worker03-linxiaoyu"
	goodProvider := "codex-worker04-sunwei"
	quotaAuthID := quotaProvider + "-auth"
	emptyAuthID := emptyProvider + "-auth"
	goodAuthID := goodProvider + "-auth"

	quotaRetryAfter := 24 * time.Hour
	quotaErr := retryAfterTestError{
		statusCode: http.StatusTooManyRequests,
		message:    `{"error":{"type":"usage_limit_reached","resets_in_seconds":86400}}`,
		retryAfter: quotaRetryAfter,
	}
	quotaExecutor := &authSequenceStreamExecutor{
		id: quotaProvider,
		streams: map[string][][]cliproxyexecutor.StreamChunk{
			quotaAuthID + "|" + model: {
				{{Err: quotaErr}},
			},
		},
	}
	emptyExecutor := &authSequenceStreamExecutor{
		id: emptyProvider,
		streams: map[string][][]cliproxyexecutor.StreamChunk{
			emptyAuthID + "|" + model: {
				{{Err: errors.New("upstream stream closed before first payload")}},
				{{Err: errors.New("upstream stream closed before first payload")}},
			},
		},
	}
	goodExecutor := &authSequenceStreamExecutor{
		id: goodProvider,
		streams: map[string][][]cliproxyexecutor.StreamChunk{
			goodAuthID + "|" + model: {
				{{Payload: []byte("ok")}},
			},
		},
	}

	cfg := &internalconfig.Config{
		OpenAICompatibility: []internalconfig.OpenAICompatibility{
			{Name: quotaProvider, Models: []internalconfig.OpenAICompatibilityModel{{Name: model, Alias: model}}},
			{Name: emptyProvider, Models: []internalconfig.OpenAICompatibilityModel{{Name: model, Alias: model}}},
			{Name: goodProvider, Models: []internalconfig.OpenAICompatibilityModel{{Name: model, Alias: model}}},
		},
	}
	m := NewManager(nil, nil, nil)
	m.SetConfig(cfg)
	m.RegisterExecutor(quotaExecutor)
	m.RegisterExecutor(emptyExecutor)
	m.RegisterExecutor(goodExecutor)

	reg := registry.GetGlobalRegistry()
	for _, item := range []struct {
		id       string
		provider string
	}{
		{quotaAuthID, quotaProvider},
		{emptyAuthID, emptyProvider},
		{goodAuthID, goodProvider},
	} {
		auth := &Auth{
			ID:       item.id,
			Provider: item.provider,
			Status:   StatusActive,
			Attributes: map[string]string{
				"api_key":      item.id + "-key",
				"compat_name":  item.provider,
				"provider_key": item.provider,
			},
		}
		if _, err := m.Register(context.Background(), auth); err != nil {
			t.Fatalf("register auth %s: %v", item.id, err)
		}
		reg.RegisterClient(item.id, item.provider, []*registry.ModelInfo{{ID: model}})
		t.Cleanup(func(id string) func() {
			return func() { reg.UnregisterClient(id) }
		}(item.id))
	}

	streamResult, err := m.ExecuteStream(context.Background(), []string{quotaProvider, emptyProvider, goodProvider}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{})
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	if payload := readOpenAICompatStreamPayload(t, streamResult); payload != "ok" {
		t.Fatalf("payload = %q, want ok", payload)
	}

	quotaAuth, _ := m.GetByID(quotaAuthID)
	if quotaAuth == nil || !quotaAuth.Unavailable || !quotaAuth.Quota.Exceeded || quotaAuth.NextRetryAfter.Sub(time.Now()) < 23*time.Hour {
		t.Fatalf("quota auth = %+v, want long quota cooldown", quotaAuth)
	}
	emptyAuth, _ := m.GetByID(emptyAuthID)
	if emptyAuth == nil || !emptyAuth.Unavailable || emptyAuth.Health.ConsecutiveEmptyStreams != 2 {
		t.Fatalf("empty-stream auth = %+v, want short whole-worker cooldown", emptyAuth)
	}
	goodAuth, _ := m.GetByID(goodAuthID)
	if goodAuth == nil || goodAuth.Unavailable {
		t.Fatalf("good auth = %+v, want available", goodAuth)
	}
}

func TestManagerExecute_OpenAICompatAliasPoolSkipsSuspendedUpstreamOnLaterRequests(t *testing.T) {
	alias := "claude-opus-4.66"
	modelSupportErr := &Error{
		HTTPStatus: http.StatusBadRequest,
		Message:    "invalid_request_error: The requested model is not supported.",
	}
	executor := &openAICompatPoolExecutor{
		id:            "pool",
		executeErrors: map[string]error{"qwen3.5-plus": modelSupportErr},
	}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "qwen3.5-plus", Alias: alias},
		{Name: "glm-5", Alias: alias},
	}, executor)

	for i := 0; i < 3; i++ {
		resp, err := m.Execute(context.Background(), []string{"pool"}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
		if err != nil {
			t.Fatalf("execute %d: %v", i, err)
		}
		if string(resp.Payload) != "glm-5" {
			t.Fatalf("execute %d payload = %q, want %q", i, string(resp.Payload), "glm-5")
		}
	}

	got := executor.ExecuteModels()
	want := []string{"qwen3.5-plus", "glm-5", "glm-5", "glm-5"}
	if len(got) != len(want) {
		t.Fatalf("execute calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("execute call %d model = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestManagerExecuteStream_OpenAICompatAliasPoolSkipsSuspendedUpstreamOnLaterRequests(t *testing.T) {
	alias := "claude-opus-4.66"
	modelSupportErr := &Error{
		HTTPStatus: http.StatusUnprocessableEntity,
		Message:    "The requested model is not supported.",
	}
	executor := &openAICompatPoolExecutor{
		id:                "pool",
		streamFirstErrors: map[string]error{"qwen3.5-plus": modelSupportErr},
	}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "qwen3.5-plus", Alias: alias},
		{Name: "glm-5", Alias: alias},
	}, executor)

	for i := 0; i < 3; i++ {
		streamResult, err := m.ExecuteStream(context.Background(), []string{"pool"}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
		if err != nil {
			t.Fatalf("execute stream %d: %v", i, err)
		}
		if payload := readOpenAICompatStreamPayload(t, streamResult); payload != "glm-5" {
			t.Fatalf("execute stream %d payload = %q, want %q", i, payload, "glm-5")
		}
		if gotHeader := streamResult.Headers.Get("X-Model"); gotHeader != "glm-5" {
			t.Fatalf("execute stream %d header X-Model = %q, want %q", i, gotHeader, "glm-5")
		}
	}

	got := executor.StreamModels()
	want := []string{"qwen3.5-plus", "glm-5", "glm-5", "glm-5"}
	if len(got) != len(want) {
		t.Fatalf("stream calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("stream call %d model = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestManagerExecuteCount_OpenAICompatAliasPoolRotatesWithinAuth(t *testing.T) {
	alias := "claude-opus-4.66"
	executor := &openAICompatPoolExecutor{id: "pool"}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "qwen3.5-plus", Alias: alias},
		{Name: "glm-5", Alias: alias},
	}, executor)

	for i := 0; i < 2; i++ {
		resp, err := m.ExecuteCount(context.Background(), []string{"pool"}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
		if err != nil {
			t.Fatalf("execute count %d: %v", i, err)
		}
		if len(resp.Payload) == 0 {
			t.Fatalf("execute count %d returned empty payload", i)
		}
	}

	got := executor.CountModels()
	want := []string{"qwen3.5-plus", "glm-5"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("count call %d model = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestManagerExecuteCount_OpenAICompatAliasPoolSkipsSuspendedUpstreamOnLaterRequests(t *testing.T) {
	alias := "claude-opus-4.66"
	modelSupportErr := &Error{
		HTTPStatus: http.StatusBadRequest,
		Message:    "invalid_request_error: The requested model is unsupported.",
	}
	executor := &openAICompatPoolExecutor{
		id:          "pool",
		countErrors: map[string]error{"qwen3.5-plus": modelSupportErr},
	}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "qwen3.5-plus", Alias: alias},
		{Name: "glm-5", Alias: alias},
	}, executor)

	for i := 0; i < 3; i++ {
		resp, err := m.ExecuteCount(context.Background(), []string{"pool"}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
		if err != nil {
			t.Fatalf("execute count %d: %v", i, err)
		}
		if string(resp.Payload) != "glm-5" {
			t.Fatalf("execute count %d payload = %q, want %q", i, string(resp.Payload), "glm-5")
		}
	}

	got := executor.CountModels()
	want := []string{"qwen3.5-plus", "glm-5", "glm-5", "glm-5"}
	if len(got) != len(want) {
		t.Fatalf("count calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("count call %d model = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestManagerExecute_OpenAICompatAliasPoolBlockedAuthDoesNotConsumeRetryBudget(t *testing.T) {
	alias := "claude-opus-4.66"
	cfg := &internalconfig.Config{
		OpenAICompatibility: []internalconfig.OpenAICompatibility{{
			Name: "pool",
			Models: []internalconfig.OpenAICompatibilityModel{
				{Name: "qwen3.5-plus", Alias: alias},
				{Name: "glm-5", Alias: alias},
			},
		}},
	}
	m := NewManager(nil, nil, nil)
	m.SetConfig(cfg)
	m.SetRetryConfig(0, 0, 1)

	executor := &authScopedOpenAICompatPoolExecutor{id: "pool"}
	m.RegisterExecutor(executor)

	badAuth := &Auth{
		ID:       "aa-blocked-auth",
		Provider: "pool",
		Status:   StatusActive,
		Attributes: map[string]string{
			"api_key":      "bad-key",
			"compat_name":  "pool",
			"provider_key": "pool",
		},
	}
	goodAuth := &Auth{
		ID:       "bb-good-auth",
		Provider: "pool",
		Status:   StatusActive,
		Attributes: map[string]string{
			"api_key":      "good-key",
			"compat_name":  "pool",
			"provider_key": "pool",
		},
	}
	if _, err := m.Register(context.Background(), badAuth); err != nil {
		t.Fatalf("register bad auth: %v", err)
	}
	if _, err := m.Register(context.Background(), goodAuth); err != nil {
		t.Fatalf("register good auth: %v", err)
	}

	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(badAuth.ID, "pool", []*registry.ModelInfo{{ID: alias}})
	reg.RegisterClient(goodAuth.ID, "pool", []*registry.ModelInfo{{ID: alias}})
	t.Cleanup(func() {
		reg.UnregisterClient(badAuth.ID)
		reg.UnregisterClient(goodAuth.ID)
	})

	modelSupportErr := &Error{
		HTTPStatus: http.StatusBadRequest,
		Message:    "invalid_request_error: The requested model is not supported.",
	}
	for _, upstreamModel := range []string{"qwen3.5-plus", "glm-5"} {
		m.MarkResult(context.Background(), Result{
			AuthID:   badAuth.ID,
			Provider: "pool",
			Model:    upstreamModel,
			Success:  false,
			Error:    modelSupportErr,
		})
	}

	resp, err := m.Execute(context.Background(), []string{"pool"}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
	if err != nil {
		t.Fatalf("execute error = %v, want success via fallback auth", err)
	}
	if !strings.HasPrefix(string(resp.Payload), goodAuth.ID+"|") {
		t.Fatalf("payload = %q, want auth %q", string(resp.Payload), goodAuth.ID)
	}

	got := executor.ExecuteCalls()
	if len(got) != 1 {
		t.Fatalf("execute calls = %v, want only one real execution on fallback auth", got)
	}
	if !strings.HasPrefix(got[0], goodAuth.ID+"|") {
		t.Fatalf("execute call = %q, want fallback auth %q", got[0], goodAuth.ID)
	}
}

func TestManagerExecuteStream_OpenAICompatAliasPoolStopsOnInvalidBootstrap(t *testing.T) {
	alias := "claude-opus-4.66"
	invalidErr := &Error{HTTPStatus: http.StatusBadRequest, Message: "invalid_request_error: malformed payload"}
	executor := &openAICompatPoolExecutor{
		id:                "pool",
		streamFirstErrors: map[string]error{"qwen3.5-plus": invalidErr},
	}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "qwen3.5-plus", Alias: alias},
		{Name: "glm-5", Alias: alias},
	}, executor)

	streamResult, err := m.ExecuteStream(context.Background(), []string{"pool"}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
	if err == nil {
		t.Fatal("expected invalid request error")
	}
	if err != invalidErr {
		t.Fatalf("error = %v, want %v", err, invalidErr)
	}
	if streamResult != nil {
		t.Fatalf("streamResult = %#v, want nil on invalid bootstrap", streamResult)
	}
	if got := executor.StreamModels(); len(got) != 1 || got[0] != "qwen3.5-plus" {
		t.Fatalf("stream calls = %v, want only first upstream model", got)
	}
}

func TestManagerExecuteStream_OpenAICompatPoolAggregatesAllAuthCooldowns(t *testing.T) {
	model := "gpt-5.4"
	rawWorkerErr := retryAfterTestError{
		statusCode: http.StatusTooManyRequests,
		message:    `{"error":{"code":"model_cooldown","provider":"codex","reset_seconds":300}}`,
		retryAfter: 5 * time.Minute,
	}
	executor := &openAICompatPoolExecutor{
		id:                "pool",
		streamFirstErrors: map[string]error{model: rawWorkerErr},
	}
	cfg := &internalconfig.Config{
		OpenAICompatibility: []internalconfig.OpenAICompatibility{{
			Name: "pool",
			Models: []internalconfig.OpenAICompatibilityModel{
				{Name: model, Alias: model},
			},
		}},
	}
	m := NewManager(nil, nil, nil)
	m.SetConfig(cfg)
	m.RegisterExecutor(executor)

	reg := registry.GetGlobalRegistry()
	for _, authID := range []string{"pool-auth-a", "pool-auth-b"} {
		auth := &Auth{
			ID:       authID,
			Provider: "pool",
			Status:   StatusActive,
			Attributes: map[string]string{
				"api_key":      authID + "-key",
				"compat_name":  "pool",
				"provider_key": "pool",
			},
		}
		if _, err := m.Register(context.Background(), auth); err != nil {
			t.Fatalf("register auth %s: %v", authID, err)
		}
		reg.RegisterClient(authID, "pool", []*registry.ModelInfo{{ID: model}})
		t.Cleanup(func(id string) func() {
			return func() { reg.UnregisterClient(id) }
		}(authID))
	}

	_, err := m.ExecuteStream(context.Background(), []string{"pool"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{})
	if err == nil {
		t.Fatal("ExecuteStream error = nil, want model cooldown")
	}
	var cooldownErr *modelCooldownError
	if !errors.As(err, &cooldownErr) {
		t.Fatalf("ExecuteStream error = %T %v, want modelCooldownError", err, err)
	}
	if strings.Contains(err.Error(), `"provider":"codex"`) {
		t.Fatalf("aggregate cooldown leaked worker provider detail: %v", err)
	}
	if got := executor.StreamModels(); len(got) != 2 {
		t.Fatalf("stream calls = %v, want both auths attempted once", got)
	}
}

func TestManagerMarkResult_CanonicalizesThinkingSuffixCooldown(t *testing.T) {
	model := "gpt-5.4"
	requestedModel := "gpt-5.4(high)"
	authID := "pool-auth-" + t.Name()
	m := NewManager(nil, nil, nil)
	m.RegisterExecutor(&authScopedOpenAICompatPoolExecutor{id: "pool"})

	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(authID, "pool", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() {
		reg.UnregisterClient(authID)
	})

	auth := &Auth{
		ID:       authID,
		Provider: "pool",
		Status:   StatusActive,
	}
	if _, err := m.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	retryAfter := 5 * time.Minute
	m.MarkResult(context.Background(), Result{
		AuthID:     authID,
		Provider:   "pool",
		Model:      requestedModel,
		Success:    false,
		RetryAfter: &retryAfter,
		Error:      &Error{HTTPStatus: http.StatusTooManyRequests, Message: "rate limited"},
	})

	updated, ok := m.GetByID(authID)
	if !ok || updated == nil {
		t.Fatalf("expected auth to remain registered")
	}
	if state := updated.ModelStates[model]; state == nil || !state.Unavailable || state.NextRetryAfter.IsZero() {
		t.Fatalf("base model state = %+v, want cooldown", state)
	}
	if state := updated.ModelStates[requestedModel]; state != nil {
		t.Fatalf("unexpected suffixed model state: %+v", state)
	}
	_, _, _, err := m.pickNextMixed(context.Background(), []string{"pool"}, model, cliproxyexecutor.Options{}, nil)
	if err == nil {
		t.Fatal("pickNextMixed error = nil, want cooldown")
	}
	var cooldownErr *modelCooldownError
	if !errors.As(err, &cooldownErr) {
		t.Fatalf("pickNextMixed error = %T %v, want modelCooldownError", err, err)
	}
}
