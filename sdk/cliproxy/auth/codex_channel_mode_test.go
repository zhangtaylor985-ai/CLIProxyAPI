package auth

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

type codexChannelModeExecutor struct{}

func (codexChannelModeExecutor) Identifier() string { return "codex" }

func (codexChannelModeExecutor) Execute(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, nil
}

func (codexChannelModeExecutor) ExecuteStream(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	return nil, nil
}

func (codexChannelModeExecutor) Refresh(_ context.Context, auth *Auth) (*Auth, error) {
	return auth, nil
}

func (codexChannelModeExecutor) CountTokens(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, nil
}

func (codexChannelModeExecutor) HttpRequest(context.Context, *Auth, *http.Request) (*http.Response, error) {
	return nil, nil
}

func registerCodexChannelTestModel(t *testing.T, model string, auths ...*Auth) {
	t.Helper()
	registryRef := registry.GetGlobalRegistry()
	for _, auth := range auths {
		if auth == nil {
			continue
		}
		registryRef.RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: model}})
	}
	t.Cleanup(func() {
		for _, auth := range auths {
			if auth == nil {
				continue
			}
			registryRef.UnregisterClient(auth.ID)
		}
	})
}

func TestManagerPickNext_FiltersCodexChannelModeWithScheduler(t *testing.T) {
	t.Parallel()

	const model = "gpt-5.3-codex"
	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	manager.RegisterExecutor(codexChannelModeExecutor{})

	providerAuth := &Auth{
		ID:       "codex-provider",
		Provider: "codex",
		Attributes: map[string]string{
			"auth_kind": "apikey",
			"api_key":   "provider-key",
		},
	}
	fileAuth := &Auth{
		ID:       "codex-auth-file",
		Provider: "codex",
		Attributes: map[string]string{
			"auth_kind": "oauth",
		},
	}
	registerCodexChannelTestModel(t, model, providerAuth, fileAuth)
	if _, err := manager.Register(context.Background(), providerAuth); err != nil {
		t.Fatalf("register provider auth: %v", err)
	}
	if _, err := manager.Register(context.Background(), fileAuth); err != nil {
		t.Fatalf("register auth-file auth: %v", err)
	}

	pickedProvider, _, err := manager.pickNext(context.Background(), "codex", model, cliproxyexecutor.Options{
		Metadata: map[string]any{cliproxyexecutor.CodexChannelModeMetadataKey: "provider"},
	}, nil)
	if err != nil {
		t.Fatalf("pick provider auth: %v", err)
	}
	if pickedProvider == nil || pickedProvider.ID != providerAuth.ID {
		t.Fatalf("picked provider auth = %+v, want %q", pickedProvider, providerAuth.ID)
	}

	pickedFile, _, err := manager.pickNext(context.Background(), "codex", model, cliproxyexecutor.Options{
		Metadata: map[string]any{cliproxyexecutor.CodexChannelModeMetadataKey: "auth_file"},
	}, nil)
	if err != nil {
		t.Fatalf("pick auth-file auth: %v", err)
	}
	if pickedFile == nil || pickedFile.ID != fileAuth.ID {
		t.Fatalf("picked auth-file auth = %+v, want %q", pickedFile, fileAuth.ID)
	}
}

func TestManagerPickNextLegacy_FiltersCodexChannelMode(t *testing.T) {
	t.Parallel()

	const model = "gpt-5.3-codex"
	manager := NewManager(nil, &trackingSelector{}, nil)
	manager.RegisterExecutor(codexChannelModeExecutor{})

	providerAuth := &Auth{
		ID:       "legacy-provider",
		Provider: "codex",
		Attributes: map[string]string{
			"auth_kind": "apikey",
			"api_key":   "provider-key",
		},
	}
	fileAuth := &Auth{
		ID:       "legacy-auth-file",
		Provider: "codex",
		Attributes: map[string]string{
			"auth_kind": "oauth",
		},
	}
	registerCodexChannelTestModel(t, model, providerAuth, fileAuth)
	if _, err := manager.Register(context.Background(), providerAuth); err != nil {
		t.Fatalf("register provider auth: %v", err)
	}
	if _, err := manager.Register(context.Background(), fileAuth); err != nil {
		t.Fatalf("register auth-file auth: %v", err)
	}

	pickedProvider, _, err := manager.pickNextLegacy(context.Background(), "codex", model, cliproxyexecutor.Options{
		Metadata: map[string]any{cliproxyexecutor.CodexChannelModeMetadataKey: "provider"},
	}, nil)
	if err != nil {
		t.Fatalf("pick provider auth: %v", err)
	}
	if pickedProvider == nil || pickedProvider.ID != providerAuth.ID {
		t.Fatalf("picked provider auth = %+v, want %q", pickedProvider, providerAuth.ID)
	}

	pickedFile, _, err := manager.pickNextLegacy(context.Background(), "codex", model, cliproxyexecutor.Options{
		Metadata: map[string]any{cliproxyexecutor.CodexChannelModeMetadataKey: "auth_file"},
	}, nil)
	if err != nil {
		t.Fatalf("pick auth-file auth: %v", err)
	}
	if pickedFile == nil || pickedFile.ID != fileAuth.ID {
		t.Fatalf("picked auth-file auth = %+v, want %q", pickedFile, fileAuth.ID)
	}
}

func TestManagerPickNextMixed_FiltersCodexChannelMode(t *testing.T) {
	t.Parallel()

	const model = "gpt-5.3-codex"
	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	manager.RegisterExecutor(codexChannelModeExecutor{})

	providerAuth := &Auth{
		ID:       "mixed-provider",
		Provider: "codex",
		Attributes: map[string]string{
			"auth_kind": "apikey",
			"api_key":   "provider-key",
		},
	}
	fileAuth := &Auth{
		ID:       "mixed-auth-file",
		Provider: "codex",
		Attributes: map[string]string{
			"auth_kind": "oauth",
		},
	}
	registerCodexChannelTestModel(t, model, providerAuth, fileAuth)
	if _, err := manager.Register(context.Background(), providerAuth); err != nil {
		t.Fatalf("register provider auth: %v", err)
	}
	if _, err := manager.Register(context.Background(), fileAuth); err != nil {
		t.Fatalf("register auth-file auth: %v", err)
	}

	pickedProvider, _, providerName, err := manager.pickNextMixed(context.Background(), []string{"codex", "openai"}, model, cliproxyexecutor.Options{
		Metadata: map[string]any{cliproxyexecutor.CodexChannelModeMetadataKey: "provider"},
	}, nil)
	if err != nil {
		t.Fatalf("pick mixed provider auth: %v", err)
	}
	if providerName != "codex" || pickedProvider == nil || pickedProvider.ID != providerAuth.ID {
		t.Fatalf("picked mixed provider auth = provider:%q auth:%+v", providerName, pickedProvider)
	}

	pickedFile, _, providerName, err := manager.pickNextMixed(context.Background(), []string{"codex", "openai"}, model, cliproxyexecutor.Options{
		Metadata: map[string]any{cliproxyexecutor.CodexChannelModeMetadataKey: "auth_file"},
	}, nil)
	if err != nil {
		t.Fatalf("pick mixed auth-file auth: %v", err)
	}
	if providerName != "codex" || pickedFile == nil || pickedFile.ID != fileAuth.ID {
		t.Fatalf("picked mixed auth-file auth = provider:%q auth:%+v", providerName, pickedFile)
	}
}

func TestManagerPickNextMixed_ChannelFilteredCooldownDoesNotProduceRetryableError(t *testing.T) {
	t.Parallel()

	const model = "gpt-5.3-codex"
	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	manager.RegisterExecutor(codexChannelModeExecutor{})

	cooldownAuth := &Auth{
		ID:       "cooldown-auth-file",
		Provider: "codex",
		Attributes: map[string]string{
			"auth_kind": "oauth",
		},
		ModelStates: map[string]*ModelState{
			model: {
				Status:         StatusError,
				Unavailable:    true,
				NextRetryAfter: time.Now().Add(30 * time.Second),
			},
		},
	}
	registerCodexChannelTestModel(t, model, cooldownAuth)
	if _, err := manager.Register(context.Background(), cooldownAuth); err != nil {
		t.Fatalf("register cooldown auth: %v", err)
	}

	_, _, _, err := manager.pickNextMixed(context.Background(), []string{"codex"}, model, cliproxyexecutor.Options{
		Metadata: map[string]any{cliproxyexecutor.CodexChannelModeMetadataKey: "provider"},
	}, nil)
	if err == nil {
		t.Fatal("expected error when channel mode filters out all codex auths")
	}

	var authErr *Error
	if !errors.As(err, &authErr) || authErr == nil {
		t.Fatalf("expected auth error, got %v", err)
	}
	if authErr.Code != "auth_not_found" {
		t.Fatalf("expected auth_not_found when only filtered cooldown auth exists, got %+v", authErr)
	}
}
