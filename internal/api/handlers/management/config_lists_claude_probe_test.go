package management

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestPutClaudeKeysPersistsProbeTarget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("host: \"\"\nport: 8317\nclaude-api-key: []\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/v0/management/claude-api-key", bytes.NewBufferString(`[
		{"api-key":"sk-probe","base-url":"https://boomai.cloud","probe-target":true}
	]`))
	handler := &Handler{
		cfg:            &config.Config{},
		configFilePath: configPath,
	}

	handler.PutClaudeKeys(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(handler.cfg.ClaudeKey) != 1 || !handler.cfg.ClaudeKey[0].ProbeTarget {
		t.Fatalf("ProbeTarget not persisted in memory: %#v", handler.cfg.ClaudeKey)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), "probe-target: true") {
		t.Fatalf("expected config to contain probe-target: true, got:\n%s", string(data))
	}
}

func TestPatchClaudeKeyPersistsProbeTarget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("host: \"\"\nport: 8317\nclaude-api-key:\n  - api-key: sk-probe\n    base-url: https://boomai.cloud\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPatch, "/v0/management/claude-api-key", bytes.NewBufferString(`{
		"index": 0,
		"value": {"probe-target": true}
	}`))
	handler := &Handler{
		cfg: &config.Config{
			ClaudeKey: []config.ClaudeKey{
				{APIKey: "sk-probe", BaseURL: "https://boomai.cloud"},
			},
		},
		configFilePath: configPath,
	}

	handler.PatchClaudeKey(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(handler.cfg.ClaudeKey) != 1 || !handler.cfg.ClaudeKey[0].ProbeTarget {
		t.Fatalf("ProbeTarget not patched in memory: %#v", handler.cfg.ClaudeKey)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), "probe-target: true") {
		t.Fatalf("expected config to contain probe-target: true, got:\n%s", string(data))
	}
}

func TestPutClaudeKeysPersistsOpus47To46(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("host: \"\"\nport: 8317\nclaude-api-key: []\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/v0/management/claude-api-key", bytes.NewBufferString(`[
		{"api-key":"sk-opus","base-url":"https://api.anthropic.com","opus-4-7-to-4-6":true}
	]`))
	handler := &Handler{
		cfg:            &config.Config{},
		configFilePath: configPath,
	}

	handler.PutClaudeKeys(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(handler.cfg.ClaudeKey) != 1 || !handler.cfg.ClaudeKey[0].Opus47To46 {
		t.Fatalf("Opus47To46 not persisted in memory: %#v", handler.cfg.ClaudeKey)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), "opus-4-7-to-4-6: true") {
		t.Fatalf("expected config to contain opus-4-7-to-4-6: true, got:\n%s", string(data))
	}
}

func TestPatchClaudeKeyPersistsOpus47To46(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("host: \"\"\nport: 8317\nclaude-api-key:\n  - api-key: sk-opus\n    base-url: https://api.anthropic.com\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPatch, "/v0/management/claude-api-key", bytes.NewBufferString(`{
		"index": 0,
		"value": {"opus-4-7-to-4-6": true}
	}`))
	handler := &Handler{
		cfg: &config.Config{
			ClaudeKey: []config.ClaudeKey{
				{APIKey: "sk-opus", BaseURL: "https://api.anthropic.com"},
			},
		},
		configFilePath: configPath,
	}

	handler.PatchClaudeKey(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(handler.cfg.ClaudeKey) != 1 || !handler.cfg.ClaudeKey[0].Opus47To46 {
		t.Fatalf("Opus47To46 not patched in memory: %#v", handler.cfg.ClaudeKey)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), "opus-4-7-to-4-6: true") {
		t.Fatalf("expected config to contain opus-4-7-to-4-6: true, got:\n%s", string(data))
	}
}
