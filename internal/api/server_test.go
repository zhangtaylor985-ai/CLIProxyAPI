package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gin "github.com/gin-gonic/gin"
	proxyconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	internallogging "github.com/router-for-me/CLIProxyAPI/v6/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	sdkaccess "github.com/router-for-me/CLIProxyAPI/v6/sdk/access"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

type rejectingAccessProvider struct{}

func (rejectingAccessProvider) Identifier() string { return "rejecting-test" }

func (rejectingAccessProvider) Authenticate(context.Context, *http.Request) (*sdkaccess.Result, *sdkaccess.AuthError) {
	return nil, sdkaccess.NewInvalidCredentialError()
}

func newTestServer(t *testing.T) *Server {
	t.Helper()

	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatalf("failed to create auth dir: %v", err)
	}

	cfg := &proxyconfig.Config{
		SDKConfig: sdkconfig.SDKConfig{
			APIKeys: []string{"test-key"},
		},
		Port:                   0,
		AuthDir:                authDir,
		Debug:                  true,
		LoggingToFile:          false,
		UsageStatisticsEnabled: false,
	}

	authManager := auth.NewManager(nil, nil, nil)
	accessManager := sdkaccess.NewManager()

	configPath := filepath.Join(tmpDir, "config.yaml")
	return NewServer(cfg, authManager, accessManager, configPath)
}

func TestAmpProviderModelRoutes(t *testing.T) {
	testCases := []struct {
		name         string
		path         string
		wantStatus   int
		wantContains string
	}{
		{
			name:         "openai root models",
			path:         "/api/provider/openai/models",
			wantStatus:   http.StatusOK,
			wantContains: `"object":"list"`,
		},
		{
			name:         "groq root models",
			path:         "/api/provider/groq/models",
			wantStatus:   http.StatusOK,
			wantContains: `"object":"list"`,
		},
		{
			name:         "openai models",
			path:         "/api/provider/openai/v1/models",
			wantStatus:   http.StatusOK,
			wantContains: `"object":"list"`,
		},
		{
			name:         "anthropic models",
			path:         "/api/provider/anthropic/v1/models",
			wantStatus:   http.StatusOK,
			wantContains: `"data"`,
		},
		{
			name:         "google models v1",
			path:         "/api/provider/google/v1/models",
			wantStatus:   http.StatusOK,
			wantContains: `"models"`,
		},
		{
			name:         "google models v1beta",
			path:         "/api/provider/google/v1beta/models",
			wantStatus:   http.StatusOK,
			wantContains: `"models"`,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			server := newTestServer(t)

			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Authorization", "Bearer test-key")

			rr := httptest.NewRecorder()
			server.engine.ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus {
				t.Fatalf("unexpected status code for %s: got %d want %d; body=%s", tc.path, rr.Code, tc.wantStatus, rr.Body.String())
			}
			if body := rr.Body.String(); !strings.Contains(body, tc.wantContains) {
				t.Fatalf("response body for %s missing %q: %s", tc.path, tc.wantContains, body)
			}
		})
	}
}

func TestModelsWithClientVersionReturnsCodexCatalog(t *testing.T) {
	modelRegistry := registry.GetGlobalRegistry()
	clientID := "test-client-version-catalog"
	modelRegistry.RegisterClient(clientID, "openai", []*registry.ModelInfo{
		{
			ID:            "gpt-5.5",
			Object:        "model",
			Created:       1776902400,
			OwnedBy:       "openai",
			Type:          "openai",
			DisplayName:   "GPT 5.5",
			Description:   "Frontier model for complex coding, research, and real-world work.",
			ContextLength: 272000,
			Thinking:      &registry.ThinkingSupport{Levels: []string{"low", "medium", "high", "xhigh"}},
		},
		{
			ID:            "custom-codex-model-test",
			Object:        "model",
			OwnedBy:       "test",
			Type:          "openai",
			DisplayName:   "Custom Codex Model",
			Description:   "Custom model from registry",
			ContextLength: 123456,
			Thinking:      &registry.ThinkingSupport{Levels: []string{"low", "medium"}},
		},
		{ID: "grok-imagine-image-quality", Object: "model", OwnedBy: "xai", Type: "openai"},
		{ID: "gpt-image-2", Object: "model", OwnedBy: "openai", Type: "openai"},
		{ID: "grok-imagine-image", Object: "model", OwnedBy: "xai", Type: "openai"},
		{ID: "grok-imagine-video", Object: "model", OwnedBy: "xai", Type: "openai"},
	})
	t.Cleanup(func() {
		modelRegistry.UnregisterClient(clientID)
	})

	server := newTestServer(t)
	server.cfg.APIKeyPolicies = []proxyconfig.APIKeyPolicy{{APIKey: "test-key"}}

	req := httptest.NewRequest(http.MethodGet, "/v1/models?client_version=0.130.0", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("User-Agent", "claude-cli/1.0")

	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp struct {
		Models []map[string]any `json:"models"`
		Object string           `json:"object"`
		Data   []any            `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v; body=%s", err, rr.Body.String())
	}
	if resp.Object != "" || resp.Data != nil {
		t.Fatalf("expected codex catalog format without object/data, got object=%q data=%v", resp.Object, resp.Data)
	}
	if len(resp.Models) == 0 {
		t.Fatal("expected codex catalog models")
	}

	var gpt55 map[string]any
	var custom map[string]any
	for _, model := range resp.Models {
		switch slug, _ := model["slug"].(string); slug {
		case "gpt-5.5":
			gpt55 = model
		case "custom-codex-model-test":
			custom = model
		}
	}
	if gpt55 == nil {
		t.Fatal("expected gpt-5.5 codex catalog entry")
	}
	if _, ok := gpt55["minimal_client_version"]; !ok {
		t.Fatal("expected minimal_client_version in codex catalog")
	}
	if got, _ := gpt55["prefer_websockets"].(bool); !got {
		t.Fatalf("gpt-5.5 prefer_websockets = %v, want true", gpt55["prefer_websockets"])
	}
	if got, _ := gpt55["apply_patch_tool_type"].(string); got != "freeform" {
		t.Fatalf("gpt-5.5 apply_patch_tool_type = %q, want freeform", got)
	}
	serviceTiers, ok := gpt55["service_tiers"].([]any)
	if !ok || len(serviceTiers) != 1 {
		t.Fatalf("expected gpt-5.5 priority service tier, got %#v", gpt55["service_tiers"])
	}
	if custom == nil {
		t.Fatal("expected custom model codex catalog entry")
	}
	if got, _ := custom["display_name"].(string); got != "Custom Codex Model" {
		t.Fatalf("custom display_name = %q, want Custom Codex Model", got)
	}
	if got, _ := custom["description"].(string); got != "Custom model from registry" {
		t.Fatalf("custom description = %q, want Custom model from registry", got)
	}
	if got, _ := custom["context_window"].(float64); got != 123456 {
		t.Fatalf("custom context_window = %v, want 123456", custom["context_window"])
	}
	if custom["base_instructions"] != gpt55["base_instructions"] {
		t.Fatal("expected custom model to use gpt-5.5 base_instructions fallback")
	}
	if _, ok := custom["available_in_plans"].([]any); !ok {
		t.Fatalf("expected custom model to use gpt-5.5 available_in_plans fallback, got %#v", custom["available_in_plans"])
	}
	if got, _ := custom["prefer_websockets"].(bool); got {
		t.Fatalf("custom prefer_websockets = %v, want false", custom["prefer_websockets"])
	}
	if _, ok := custom["apply_patch_tool_type"]; ok {
		t.Fatal("expected custom model to omit apply_patch_tool_type")
	}
	if _, ok := custom["upgrade"]; ok {
		t.Fatal("expected custom model to omit upgrade")
	}
	if _, ok := custom["availability_nux"]; ok {
		t.Fatal("expected custom model to omit availability_nux")
	}

	hiddenModels := map[string]bool{
		"grok-imagine-image-quality": false,
		"gpt-image-2":                false,
		"grok-imagine-image":         false,
		"grok-imagine-video":         false,
	}
	for _, model := range resp.Models {
		slug, _ := model["slug"].(string)
		if _, ok := hiddenModels[slug]; !ok {
			continue
		}
		if visibility, _ := model["visibility"].(string); visibility != "hide" {
			t.Fatalf("%s visibility = %q, want hide", slug, visibility)
		}
		hiddenModels[slug] = true
	}
	for slug, found := range hiddenModels {
		if !found {
			t.Fatalf("expected hidden model %s in codex catalog", slug)
		}
	}
}

func TestHealthzRouteDoesNotRequireAPIKey(t *testing.T) {
	server := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{`"status":"ok"`, `"service":"cliproxyapi"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("healthz response missing %q: %s", want, body)
		}
	}
}

func TestRootHeadRouteDoesNotRequireAPIKey(t *testing.T) {
	server := newTestServer(t)

	req := httptest.NewRequest(http.MethodHead, "/", nil)
	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("unexpected status code: got %d want %d; body=%s", rr.Code, http.StatusNoContent, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Fatalf("expected empty root HEAD response body, got %q", rr.Body.String())
	}
}

func TestCodexEventLoggingBatchRouteDoesNotRequireAPIKey(t *testing.T) {
	server := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/event_logging/batch", strings.NewReader(`{"events":[]}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("unexpected status code: got %d want %d; body=%s", rr.Code, http.StatusNoContent, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Fatalf("expected empty telemetry response body, got %q", rr.Body.String())
	}
}

func TestAuthMiddleware_AttachesRequestIDToAuthErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	manager := sdkaccess.NewManager()
	manager.SetProviders([]sdkaccess.Provider{rejectingAccessProvider{}})

	router := gin.New()
	router.Use(func(c *gin.Context) {
		internallogging.SetGinRequestID(c, "req-auth-test")
		c.Next()
	})
	router.Use(AuthMiddleware(manager))
	router.GET("/v1/messages", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/messages", nil)
	req.Header.Set("Authorization", "Bearer bad-key")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status code: got %d want %d; body=%s", rr.Code, http.StatusUnauthorized, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{`"error":"Invalid API key"`, `"request_id":"req-auth-test"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("auth error response missing %q: %s", want, body)
		}
	}
}

func TestDefaultRequestLoggerFactory_UsesResolvedLogDirectory(t *testing.T) {
	t.Setenv("WRITABLE_PATH", "")
	t.Setenv("writable_path", "")

	originalWD, errGetwd := os.Getwd()
	if errGetwd != nil {
		t.Fatalf("failed to get current working directory: %v", errGetwd)
	}

	tmpDir := t.TempDir()
	if errChdir := os.Chdir(tmpDir); errChdir != nil {
		t.Fatalf("failed to switch working directory: %v", errChdir)
	}
	defer func() {
		if errChdirBack := os.Chdir(originalWD); errChdirBack != nil {
			t.Fatalf("failed to restore working directory: %v", errChdirBack)
		}
	}()

	// Force ResolveLogDirectory to fallback to auth-dir/logs by making ./logs not a writable directory.
	if errWriteFile := os.WriteFile(filepath.Join(tmpDir, "logs"), []byte("not-a-directory"), 0o644); errWriteFile != nil {
		t.Fatalf("failed to create blocking logs file: %v", errWriteFile)
	}

	configDir := filepath.Join(tmpDir, "config")
	if errMkdirConfig := os.MkdirAll(configDir, 0o755); errMkdirConfig != nil {
		t.Fatalf("failed to create config dir: %v", errMkdirConfig)
	}
	configPath := filepath.Join(configDir, "config.yaml")

	authDir := filepath.Join(tmpDir, "auth")
	if errMkdirAuth := os.MkdirAll(authDir, 0o700); errMkdirAuth != nil {
		t.Fatalf("failed to create auth dir: %v", errMkdirAuth)
	}

	cfg := &proxyconfig.Config{
		SDKConfig: proxyconfig.SDKConfig{
			RequestLog: false,
		},
		AuthDir:           authDir,
		ErrorLogsMaxFiles: 10,
	}

	logger := defaultRequestLoggerFactory(cfg, configPath)
	fileLogger, ok := logger.(*internallogging.FileRequestLogger)
	if !ok {
		t.Fatalf("expected *FileRequestLogger, got %T", logger)
	}

	errLog := fileLogger.LogRequestWithOptions(
		"/v1/chat/completions",
		http.MethodPost,
		map[string][]string{"Content-Type": []string{"application/json"}},
		[]byte(`{"input":"hello"}`),
		http.StatusBadGateway,
		map[string][]string{"Content-Type": []string{"application/json"}},
		[]byte(`{"error":"upstream failure"}`),
		nil,
		nil,
		nil,
		true,
		"issue-1711",
		time.Now(),
		time.Now(),
	)
	if errLog != nil {
		t.Fatalf("failed to write forced error request log: %v", errLog)
	}

	authLogsDir := filepath.Join(authDir, "logs")
	authEntries, errReadAuthDir := os.ReadDir(authLogsDir)
	if errReadAuthDir != nil {
		t.Fatalf("failed to read auth logs dir %s: %v", authLogsDir, errReadAuthDir)
	}
	foundErrorLogInAuthDir := false
	for _, entry := range authEntries {
		if strings.HasPrefix(entry.Name(), "error-") && strings.HasSuffix(entry.Name(), ".log") {
			foundErrorLogInAuthDir = true
			break
		}
	}
	if !foundErrorLogInAuthDir {
		t.Fatalf("expected forced error log in auth fallback dir %s, got entries: %+v", authLogsDir, authEntries)
	}

	configLogsDir := filepath.Join(configDir, "logs")
	configEntries, errReadConfigDir := os.ReadDir(configLogsDir)
	if errReadConfigDir != nil && !os.IsNotExist(errReadConfigDir) {
		t.Fatalf("failed to inspect config logs dir %s: %v", configLogsDir, errReadConfigDir)
	}
	for _, entry := range configEntries {
		if strings.HasPrefix(entry.Name(), "error-") && strings.HasSuffix(entry.Name(), ".log") {
			t.Fatalf("unexpected forced error log in config dir %s", configLogsDir)
		}
	}
}

func TestResolveBillingPostgresConfigPrefersPolicyEnv(t *testing.T) {
	t.Setenv("APIKEY_POLICY_PG_DSN", "postgres://policy-user:pass@127.0.0.1:5432/policy_db?sslmode=disable")
	t.Setenv("APIKEY_POLICY_PG_SCHEMA", "policy_schema")
	t.Setenv("APIKEY_BILLING_PG_DSN", "")
	t.Setenv("APIKEY_BILLING_PG_SCHEMA", "")
	t.Setenv("PGSTORE_DSN", "")
	t.Setenv("PGSTORE_SCHEMA", "")

	dsn, schema := resolveBillingPostgresConfig()
	if dsn != "postgres://policy-user:pass@127.0.0.1:5432/policy_db?sslmode=disable" {
		t.Fatalf("dsn = %q, want policy env", dsn)
	}
	if schema != "policy_schema" {
		t.Fatalf("schema = %q, want policy_schema", schema)
	}
}

func TestResolveBillingPostgresConfigFallsBackToBillingThenPGStoreEnv(t *testing.T) {
	t.Setenv("APIKEY_POLICY_PG_DSN", "")
	t.Setenv("APIKEY_POLICY_PG_SCHEMA", "")
	t.Setenv("APIKEY_BILLING_PG_DSN", "postgres://billing-user:pass@127.0.0.1:5432/billing_db?sslmode=disable")
	t.Setenv("APIKEY_BILLING_PG_SCHEMA", "billing_schema")
	t.Setenv("PGSTORE_DSN", "postgres://pgstore-user:pass@127.0.0.1:5432/pgstore_db?sslmode=disable")
	t.Setenv("PGSTORE_SCHEMA", "pgstore_schema")

	dsn, schema := resolveBillingPostgresConfig()
	if dsn != "postgres://billing-user:pass@127.0.0.1:5432/billing_db?sslmode=disable" {
		t.Fatalf("dsn = %q, want billing env", dsn)
	}
	if schema != "billing_schema" {
		t.Fatalf("schema = %q, want billing_schema", schema)
	}

	t.Setenv("APIKEY_BILLING_PG_DSN", "")
	t.Setenv("APIKEY_BILLING_PG_SCHEMA", "")

	dsn, schema = resolveBillingPostgresConfig()
	if dsn != "postgres://pgstore-user:pass@127.0.0.1:5432/pgstore_db?sslmode=disable" {
		t.Fatalf("dsn = %q, want pgstore env", dsn)
	}
	if schema != "pgstore_schema" {
		t.Fatalf("schema = %q, want pgstore_schema", schema)
	}
}

func TestResolveSessionTrajectoryPostgresConfigPrefersDedicatedEnv(t *testing.T) {
	t.Setenv("SESSION_TRAJECTORY_PG_DSN", "postgres://session-user:pass@127.0.0.1:5432/session_db?sslmode=disable")
	t.Setenv("SESSION_TRAJECTORY_PG_SCHEMA", "session_schema")
	t.Setenv("APIKEY_POLICY_PG_DSN", "postgres://policy-user:pass@127.0.0.1:5432/policy_db?sslmode=disable")
	t.Setenv("APIKEY_POLICY_PG_SCHEMA", "policy_schema")

	dsn, schema := resolveSessionTrajectoryPostgresConfig()
	if dsn != "postgres://session-user:pass@127.0.0.1:5432/session_db?sslmode=disable" {
		t.Fatalf("dsn = %q, want dedicated session env", dsn)
	}
	if schema != "session_schema" {
		t.Fatalf("schema = %q, want session_schema", schema)
	}
}
