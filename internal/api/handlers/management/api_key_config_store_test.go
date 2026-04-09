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
	state            apikeyconfig.State
	savedRecord      apikeyconfig.Record
	savedPreviousKey string
	deletedAPIKey    string
	err              error
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

func (s *stubAPIKeyConfigStore) SaveRecord(_ context.Context, previousAPIKey string, record apikeyconfig.Record) error {
	if s.err != nil {
		return s.err
	}
	s.savedPreviousKey = previousAPIKey
	s.savedRecord = record
	return nil
}

func (s *stubAPIKeyConfigStore) DeleteRecord(_ context.Context, apiKey string) error {
	if s.err != nil {
		return s.err
	}
	s.deletedAPIKey = apiKey
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
	if len(store.state.Records) != 1 || store.state.Records[0].APIKey != "key-1" {
		t.Fatalf("unexpected stored records: %#v", store.state.Records)
	}
	if got := store.state.Records[0].Policy.DailyBudgetUSD; got != 12 {
		t.Fatalf("unexpected stored policy daily budget: %v", got)
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
	if strings.Contains(text, "pg-key") || strings.Contains(text, "old-key") {
		t.Fatalf("expected rendered yaml to omit api keys when store is enabled, got:\n%s", text)
	}
}

func TestPersistAPIKeyRecordUsesRowStoreAndCallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPatch, "/v0/management/api-key-records/key-1", nil)

	store := &stubAPIKeyConfigStore{}
	callbackCount := 0
	handler := &Handler{
		cfg: &config.Config{
			SDKConfig: config.SDKConfig{
				APIKeys: []string{"key-1"},
			},
			APIKeyPolicies: []config.APIKeyPolicy{
				{APIKey: "key-1", DailyBudgetUSD: 12, CreatedAt: "2026-04-06T13:00:00Z"},
			},
		},
		apiKeyConfigStore: store,
		configUpdated: func(*config.Config) {
			callbackCount++
		},
	}

	if ok := handler.persistAPIKeyRecord(ctx, "", "key-1"); !ok {
		t.Fatal("persistAPIKeyRecord returned false")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if callbackCount != 1 {
		t.Fatalf("expected callback once, got %d", callbackCount)
	}
	if store.savedPreviousKey != "" {
		t.Fatalf("unexpected previous key: %q", store.savedPreviousKey)
	}
	if store.savedRecord.APIKey != "key-1" {
		t.Fatalf("unexpected saved record: %#v", store.savedRecord)
	}
	if got := store.savedRecord.Policy.DailyBudgetUSD; got != 12 {
		t.Fatalf("unexpected stored policy daily budget: %v", got)
	}
}

func TestDeletePersistedAPIKeyRecordUsesRowStoreAndCallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodDelete, "/v0/management/api-key-records/key-1", nil)

	store := &stubAPIKeyConfigStore{}
	callbackCount := 0
	handler := &Handler{
		cfg: &config.Config{
			SDKConfig: config.SDKConfig{
				APIKeys: []string{"key-1"},
			},
		},
		apiKeyConfigStore: store,
		configUpdated: func(*config.Config) {
			callbackCount++
		},
	}

	if ok := handler.deletePersistedAPIKeyRecord(ctx, "key-1"); !ok {
		t.Fatal("deletePersistedAPIKeyRecord returned false")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if callbackCount != 1 {
		t.Fatalf("expected callback once, got %d", callbackCount)
	}
	if store.deletedAPIKey != "key-1" {
		t.Fatalf("unexpected deleted key: %q", store.deletedAPIKey)
	}
}
