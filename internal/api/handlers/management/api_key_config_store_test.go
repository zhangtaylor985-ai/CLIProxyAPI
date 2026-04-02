package management

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/apikeyconfig"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

type stubAPIKeyConfigStore struct {
	state apikeyconfig.State
	err   error
}

func (s *stubAPIKeyConfigStore) Close() error { return nil }

func (s *stubAPIKeyConfigStore) LoadState(context.Context) (apikeyconfig.State, bool, error) {
	return apikeyconfig.State{}, false, nil
}

func (s *stubAPIKeyConfigStore) SaveState(_ context.Context, state apikeyconfig.State) error {
	if s.err != nil {
		return s.err
	}
	s.state = state
	return nil
}

func TestPersistAPIKeyConfigUsesStoreAndCallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPatch, "/v0/management/api-key-policies", nil)

	store := &stubAPIKeyConfigStore{}
	callbackCount := 0
	handler := &Handler{
		cfg: &config.Config{
			SDKConfig: config.SDKConfig{
				APIKeys: []string{"key-1"},
			},
			APIKeyPolicies: []config.APIKeyPolicy{
				{APIKey: "key-1", DailyBudgetUSD: 12},
			},
		},
		apiKeyConfigStore: store,
		configUpdated: func(*config.Config) {
			callbackCount++
		},
	}

	if ok := handler.persistAPIKeyConfig(ctx); !ok {
		t.Fatal("persistAPIKeyConfig returned false")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if callbackCount != 1 {
		t.Fatalf("expected callback once, got %d", callbackCount)
	}
	if len(store.state.APIKeys) != 1 || store.state.APIKeys[0] != "key-1" {
		t.Fatalf("unexpected stored api keys: %#v", store.state.APIKeys)
	}
	if len(store.state.APIKeyPolicies) != 1 || store.state.APIKeyPolicies[0].APIKey != "key-1" {
		t.Fatalf("unexpected stored policies: %#v", store.state.APIKeyPolicies)
	}
}

func TestRenderConfigYAMLUsesEffectiveConfigWhenStoreEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("host: \"\"\nport: 8317\napi-keys:\n  - old-key\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	handler := &Handler{
		cfg: &config.Config{
			SDKConfig: config.SDKConfig{
				APIKeys: []string{"pg-key"},
			},
			Host: "",
			Port: 8317,
			APIKeyPolicies: []config.APIKeyPolicy{
				{APIKey: "pg-key", DailyBudgetUSD: 8},
			},
		},
		configFilePath:    configPath,
		apiKeyConfigStore: &stubAPIKeyConfigStore{},
	}

	data, err := handler.renderConfigYAML()
	if err != nil {
		t.Fatalf("renderConfigYAML: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "pg-key") {
		t.Fatalf("expected rendered yaml to contain pg-key, got:\n%s", text)
	}
	if strings.Contains(text, "old-key") {
		t.Fatalf("expected rendered yaml to hide stale key, got:\n%s", text)
	}
}
