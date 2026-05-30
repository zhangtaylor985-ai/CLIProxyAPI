package auth

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

type blockingConcurrencyExecutor struct {
	entered chan struct{}
	release chan struct{}
}

func (blockingConcurrencyExecutor) Identifier() string { return "codex" }

func (e blockingConcurrencyExecutor) Execute(ctx context.Context, _ *Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	e.entered <- struct{}{}
	select {
	case <-e.release:
		return cliproxyexecutor.Response{Payload: []byte(`{"ok":true}`)}, nil
	case <-ctx.Done():
		return cliproxyexecutor.Response{}, ctx.Err()
	}
}

func (blockingConcurrencyExecutor) ExecuteStream(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	return nil, nil
}

func (blockingConcurrencyExecutor) Refresh(_ context.Context, auth *Auth) (*Auth, error) {
	return auth, nil
}

func (blockingConcurrencyExecutor) CountTokens(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, nil
}

func (blockingConcurrencyExecutor) HttpRequest(context.Context, *Auth, *http.Request) (*http.Response, error) {
	return nil, nil
}

func TestAuthConcurrencyLimitDefaultsCodexAuthFilesToTen(t *testing.T) {
	fileAuth := &Auth{
		ID:       "codex-file",
		Provider: "codex",
		Attributes: map[string]string{
			"path": "/tmp/codex-file.json",
		},
	}
	if got := fileAuth.ConcurrencyLimit(); got != 10 {
		t.Fatalf("file auth concurrency=%d, want 10", got)
	}

	apiKeyAuth := &Auth{
		ID:       "codex-key",
		Provider: "codex",
		Attributes: map[string]string{
			"api_key": "sk-test",
		},
	}
	if got := apiKeyAuth.ConcurrencyLimit(); got != 0 {
		t.Fatalf("api key auth concurrency=%d, want 0", got)
	}

	explicitAuth := &Auth{
		ID:       "codex-explicit",
		Provider: "codex",
		Metadata: map[string]any{
			"concurrency": float64(3),
		},
	}
	if got := explicitAuth.ConcurrencyLimit(); got != 3 {
		t.Fatalf("explicit auth concurrency=%d, want 3", got)
	}
}

func TestManagerExecuteEnforcesAuthConcurrencyLimit(t *testing.T) {
	const model = "gpt-5.3-codex"
	auth := &Auth{
		ID:       "concurrency-auth",
		Provider: "codex",
		Metadata: map[string]any{
			"concurrency": float64(1),
		},
	}

	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() { reg.UnregisterClient(auth.ID) })

	executor := blockingConcurrencyExecutor{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	manager := NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	firstDone := make(chan error, 1)
	go func() {
		_, err := manager.Execute(context.Background(), []string{"codex"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{})
		firstDone <- err
	}()

	select {
	case <-executor.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not enter executor")
	}

	_, err := manager.Execute(context.Background(), []string{"codex"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{})
	if err == nil {
		t.Fatal("second request unexpectedly succeeded")
	}
	if status := statusCodeFromError(err); status != http.StatusTooManyRequests {
		t.Fatalf("second request status=%d, want %d (err=%v)", status, http.StatusTooManyRequests, err)
	}

	close(executor.release)
	select {
	case errFirst := <-firstDone:
		if errFirst != nil {
			t.Fatalf("first request failed: %v", errFirst)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not finish")
	}
}
