package management

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestCodexWorkerPriorityScheduleAppliesWindowPriorities(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("port: 8317\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	workerFile := filepath.Join(dir, defaultCodexWorkerFileName)
	workerJSON := `{"workers":[{"id":"codex-worker08","name":"codex-worker08","container":"cliproxy-worker08","base_url":"http://127.0.0.1:18324"}]}`
	if err := os.WriteFile(workerFile, []byte(workerJSON), 0o644); err != nil {
		t.Fatalf("write worker file: %v", err)
	}

	cfg := &config.Config{
		CodexWorkerPrioritySchedule: config.CodexWorkerPriorityScheduleConfig{
			Enabled:            true,
			Timezone:           "Asia/Shanghai",
			StartTime:          "15:00",
			EndTime:            "17:30",
			APIProviderBaseURL: "https://apibridge012.online",
		},
		Routing: config.RoutingConfig{SessionAffinity: false, SessionAffinityTTL: "1h"},
		OpenAICompatibility: []config.OpenAICompatibility{
			{Name: "codex-worker08", BaseURL: "http://127.0.0.1:18324/v1", ExcludedModels: []string{"legacy-disabled"}},
			{Name: "codex-api", BaseURL: "https://apibridge012.online/v1"},
		},
	}
	handler := NewHandler(cfg, configPath, nil)
	now := time.Date(2026, 5, 22, 7, 10, 0, 0, time.UTC) // 15:10 Asia/Shanghai

	changed, err := handler.applyCodexWorkerPrioritySchedule(nil, now)
	if err != nil {
		t.Fatalf("apply schedule: %v", err)
	}
	if !changed {
		t.Fatal("expected active window to change config")
	}
	if got := cfg.OpenAICompatibility[0].Priority; got != 20 {
		t.Fatalf("worker priority = %d, want 20", got)
	}
	if got := cfg.OpenAICompatibility[1].Priority; got != 0 {
		t.Fatalf("api priority = %d, want 0", got)
	}
	if cfg.Routing.SessionAffinity {
		t.Fatal("priority schedule should not enable session affinity")
	}
	if got := cfg.Routing.SessionAffinityTTL; got != "1h" {
		t.Fatalf("session affinity ttl = %q, want unchanged 1h", got)
	}
	if got := cfg.OpenAICompatibility[0].ExcludedModels; len(got) != 1 || got[0] != "legacy-disabled" {
		t.Fatalf("worker excluded models changed: %#v", got)
	}
}

func TestCodexWorkerPriorityScheduleRestoresOutsidePriorities(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("port: 8317\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	workerFile := filepath.Join(dir, defaultCodexWorkerFileName)
	workerJSON := `{"workers":[{"id":"codex-worker08","name":"codex-worker08","container":"cliproxy-worker08","base_url":"http://127.0.0.1:18324"}]}`
	if err := os.WriteFile(workerFile, []byte(workerJSON), 0o644); err != nil {
		t.Fatalf("write worker file: %v", err)
	}

	cfg := &config.Config{
		CodexWorkerPrioritySchedule: config.CodexWorkerPriorityScheduleConfig{
			Enabled:            true,
			Timezone:           "Asia/Shanghai",
			StartTime:          "15:00",
			EndTime:            "17:30",
			APIProviderBaseURL: "https://apibridge012.online",
		},
		Routing: config.RoutingConfig{SessionAffinity: true, SessionAffinityTTL: "3h"},
		OpenAICompatibility: []config.OpenAICompatibility{
			{Name: "codex-worker08", BaseURL: "http://127.0.0.1:18324/v1", Priority: 20},
			{Name: "codex-api", BaseURL: "https://apibridge012.online/v1", Priority: 0},
		},
	}
	handler := NewHandler(cfg, configPath, nil)
	now := time.Date(2026, 5, 22, 10, 0, 0, 0, time.UTC) // 18:00 Asia/Shanghai

	changed, err := handler.applyCodexWorkerPrioritySchedule(nil, now)
	if err != nil {
		t.Fatalf("apply schedule: %v", err)
	}
	if !changed {
		t.Fatal("expected outside window to change config")
	}
	if got := cfg.OpenAICompatibility[0].Priority; got != 0 {
		t.Fatalf("worker priority = %d, want 0", got)
	}
	if got := cfg.OpenAICompatibility[1].Priority; got != 20 {
		t.Fatalf("api priority = %d, want 20", got)
	}
	if !cfg.Routing.SessionAffinity {
		t.Fatal("priority schedule should not disable session affinity")
	}
	if got := cfg.Routing.SessionAffinityTTL; got != "3h" {
		t.Fatalf("session affinity ttl = %q, want unchanged 3h", got)
	}
}

func TestCodexWorkerSessionAffinityCanBeManagedSeparately(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("port: 8317\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg := &config.Config{
		Routing: config.RoutingConfig{SessionAffinity: false, SessionAffinityTTL: "1h"},
	}
	handler := NewHandler(cfg, configPath, nil)

	body := bytes.NewBufferString(`{"enabled":true,"ttl":"3h"}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/v0/management/codex-workers/session-affinity", body)
	c.Request.Header.Set("Content-Type", "application/json")

	handler.PutCodexWorkerSessionAffinity(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if !cfg.Routing.SessionAffinity {
		t.Fatal("session affinity should be enabled")
	}
	if got := cfg.Routing.SessionAffinityTTL; got != "3h" {
		t.Fatalf("session affinity ttl = %q, want 3h", got)
	}
}
