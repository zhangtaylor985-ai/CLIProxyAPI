package api

import (
	"context"
	"testing"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	apihandlers "github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func registerServerCodexChannelTestModel(t *testing.T, provider, clientID string, models ...string) {
	t.Helper()
	registryRef := registry.GetGlobalRegistry()
	entries := make([]*registry.ModelInfo, 0, len(models))
	for _, model := range models {
		entries = append(entries, &registry.ModelInfo{ID: model})
	}
	registryRef.RegisterClient(clientID, provider, entries)
	t.Cleanup(func() {
		registryRef.UnregisterClient(clientID)
	})
}

func modelIDs(models []map[string]any) []string {
	out := make([]string, 0, len(models))
	for _, model := range models {
		if model == nil {
			continue
		}
		if id, _ := model["id"].(string); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func TestServerFilterModelsForAPIKey_RespectsCodexChannelMode(t *testing.T) {
	t.Parallel()

	authManager := coreauth.NewManager(nil, nil, nil)
	oauthCodex := &coreauth.Auth{
		ID:       "codex-oauth",
		Provider: "codex",
		Attributes: map[string]string{
			"auth_kind": "oauth",
		},
	}
	if _, err := authManager.Register(context.Background(), oauthCodex); err != nil {
		t.Fatalf("register codex oauth auth: %v", err)
	}

	registerServerCodexChannelTestModel(t, "codex", oauthCodex.ID, "gpt-5.3-codex", "shared-codex-model")
	registerServerCodexChannelTestModel(t, "openai", "openai-client", "shared-codex-model")

	server := &Server{
		cfg: &internalconfig.Config{
			APIKeyPolicies: []internalconfig.APIKeyPolicy{
				{APIKey: "provider-key", CodexChannelMode: "provider"},
				{APIKey: "auth-file-key", CodexChannelMode: "auth_file"},
			},
		},
		handlers: &apihandlers.BaseAPIHandler{AuthManager: authManager},
	}
	server.cfg.SanitizeAPIKeyPolicies()

	models := []map[string]any{
		{"id": "gpt-5.3-codex"},
		{"id": "shared-codex-model"},
		{"id": "gpt-5.4"},
	}

	filteredProvider := server.filterModelsForAPIKey(models, "provider-key")
	if got := modelIDs(filteredProvider); len(got) != 2 || got[0] != "shared-codex-model" || got[1] != "gpt-5.4" {
		t.Fatalf("provider filtered models = %#v", got)
	}

	filteredAuthFile := server.filterModelsForAPIKey(models, "auth-file-key")
	if got := modelIDs(filteredAuthFile); len(got) != 3 || got[0] != "gpt-5.3-codex" || got[1] != "shared-codex-model" || got[2] != "gpt-5.4" {
		t.Fatalf("auth-file filtered models = %#v", got)
	}
}
