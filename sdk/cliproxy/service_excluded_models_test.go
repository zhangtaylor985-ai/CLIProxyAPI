package cliproxy

import (
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

func TestRegisterModelsForAuth_UsesPreMergedExcludedModelsAttribute(t *testing.T) {
	service := &Service{
		cfg: &config.Config{
			OAuthExcludedModels: map[string][]string{
				"gemini-cli": {"gemini-2.5-pro"},
			},
		},
	}
	auth := &coreauth.Auth{
		ID:       "auth-gemini-cli",
		Provider: "gemini-cli",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"auth_kind":       "oauth",
			"excluded_models": "gemini-2.5-flash",
		},
	}

	registry := GlobalModelRegistry()
	registry.UnregisterClient(auth.ID)
	t.Cleanup(func() {
		registry.UnregisterClient(auth.ID)
	})

	service.registerModelsForAuth(auth)

	models := registry.GetAvailableModelsByProvider("gemini-cli")
	if len(models) == 0 {
		t.Fatal("expected gemini-cli models to be registered")
	}

	for _, model := range models {
		if model == nil {
			continue
		}
		modelID := strings.TrimSpace(model.ID)
		if strings.EqualFold(modelID, "gemini-2.5-flash") {
			t.Fatalf("expected model %q to be excluded by auth attribute", modelID)
		}
	}

	seenGlobalExcluded := false
	for _, model := range models {
		if model == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(model.ID), "gemini-2.5-pro") {
			seenGlobalExcluded = true
			break
		}
	}
	if !seenGlobalExcluded {
		t.Fatal("expected global excluded model to be present when attribute override is set")
	}
}

func TestRegisterModelsForAuth_GeminiCLIDoesNotClaimGPTModelsWithoutAlias(t *testing.T) {
	service := &Service{cfg: &config.Config{}}
	auth := &coreauth.Auth{
		ID:       "auth-gemini-cli-no-gpt-alias",
		Provider: "gemini-cli",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"auth_kind": "oauth",
		},
	}

	modelRegistry := registry.GetGlobalRegistry()
	modelRegistry.UnregisterClient(auth.ID)
	t.Cleanup(func() {
		modelRegistry.UnregisterClient(auth.ID)
	})

	service.registerModelsForAuth(auth)

	for _, provider := range modelRegistry.GetModelProviders("gpt-5.4") {
		if strings.EqualFold(strings.TrimSpace(provider), "gemini-cli") {
			t.Fatalf("expected gemini-cli auth without alias to not register gpt-5.4, providers=%v", modelRegistry.GetModelProviders("gpt-5.4"))
		}
	}
}

func TestRegisterModelsForAuth_GeminiCLIForkAliasCanClaimExplicitClaudeAlias(t *testing.T) {
	service := &Service{
		cfg: &config.Config{
			OAuthModelAlias: map[string][]config.OAuthModelAlias{
				"gemini-cli": {
					{Name: "gemini-2.5-pro", Alias: "claude-sonnet-4-6", Fork: true},
				},
			},
		},
	}
	auth := &coreauth.Auth{
		ID:       "auth-gemini-cli-claude-alias",
		Provider: "gemini-cli",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"auth_kind": "oauth",
		},
	}

	modelRegistry := registry.GetGlobalRegistry()
	modelRegistry.UnregisterClient(auth.ID)
	t.Cleanup(func() {
		modelRegistry.UnregisterClient(auth.ID)
	})

	service.registerModelsForAuth(auth)

	providers := modelRegistry.GetModelProviders("claude-sonnet-4-6")
	found := false
	for _, provider := range providers {
		if strings.EqualFold(strings.TrimSpace(provider), "gemini-cli") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected explicit gemini-cli fork alias to register claude-sonnet-4-6, providers=%v", providers)
	}
}
