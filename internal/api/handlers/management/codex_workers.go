package management

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

const (
	codexWorkerManagementFileEnv = "CODEX_WORKER_MANAGEMENT_FILE"
	defaultCodexWorkerFileName   = "codex-worker-management.json"
	codexUsageURL                = "https://chatgpt.com/backend-api/wham/usage"
)

type codexWorkerManagementFile struct {
	Workers []codexWorkerManagementConfig `json:"workers"`
}

type codexWorkerManagementConfig struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Container     string   `json:"container"`
	BaseURL       string   `json:"base_url"`
	ManagementKey string   `json:"management_key"`
	SSHTarget     string   `json:"ssh_target"`
	SSHOptions    []string `json:"ssh_options"`
	AuthFileName  string   `json:"auth_file_name"`
}

type codexWorkerAuthFileView struct {
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Email     string         `json:"email,omitempty"`
	Provider  string         `json:"provider,omitempty"`
	Type      string         `json:"type,omitempty"`
	AuthIndex string         `json:"auth_index,omitempty"`
	Disabled  bool           `json:"disabled,omitempty"`
	Status    string         `json:"status,omitempty"`
	Account   string         `json:"account,omitempty"`
	IDToken   map[string]any `json:"id_token,omitempty"`
}

type codexWorkerQuotaView struct {
	StatusCode int            `json:"status_code,omitempty"`
	Error      string         `json:"error,omitempty"`
	Body       map[string]any `json:"body,omitempty"`
}

type codexWorkerView struct {
	ID              string                    `json:"id"`
	Name            string                    `json:"name"`
	Container       string                    `json:"container"`
	BaseURL         string                    `json:"base_url"`
	SSHConfigured   bool                      `json:"ssh_configured"`
	ContainerStatus string                    `json:"container_status,omitempty"`
	ProxyURL        string                    `json:"proxy_url,omitempty"`
	Health          string                    `json:"health,omitempty"`
	Error           string                    `json:"error,omitempty"`
	AuthFiles       []codexWorkerAuthFileView `json:"auth_files,omitempty"`
	Quota           *codexWorkerQuotaView     `json:"quota,omitempty"`
	RouteConfigured bool                      `json:"route_configured"`
	RouteEnabled    bool                      `json:"route_enabled"`
	RouteProvider   string                    `json:"route_provider,omitempty"`
	RouteIndex      int                       `json:"route_index,omitempty"`
}

type codexWorkerActionRequest struct {
	Action string `json:"action"`
}

type codexWorkerProxyRequest struct {
	ProxyURL string `json:"proxy_url"`
}

type codexWorkerRoutingRequest struct {
	Enabled *bool `json:"enabled"`
}

type codexWorkerSaveAuthRequest struct {
	Name    string          `json:"name"`
	Content string          `json:"content"`
	JSON    json.RawMessage `json:"json"`
}

type workerAuthFilesResponse struct {
	Files []map[string]any `json:"files"`
}

type workerAPICallResponse struct {
	StatusCode int            `json:"status_code"`
	Header     map[string]any `json:"header"`
	Body       any            `json:"body"`
}

func (h *Handler) ListCodexWorkers(c *gin.Context) {
	workers, err := h.loadCodexWorkerManagementConfigs()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]codexWorkerView, 0, len(workers))
	for _, worker := range workers {
		out = append(out, h.buildCodexWorkerView(c.Request.Context(), worker))
	}
	c.JSON(http.StatusOK, gin.H{"workers": out})
}

func (h *Handler) DownloadCodexWorkerAuthFile(c *gin.Context) {
	worker, ok := h.findCodexWorkerConfig(c)
	if !ok {
		return
	}
	name := strings.TrimSpace(c.Query("name"))
	if name == "" {
		name = strings.TrimSpace(worker.AuthFileName)
	}
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "\\") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid auth file name"})
		return
	}
	resp, err := h.codexWorkerRequest(c.Request.Context(), worker, http.MethodGet, "/v0/management/auth-files/download?name="+url.QueryEscape(name), nil, "")
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		c.Data(resp.StatusCode, "application/json; charset=utf-8", body)
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}

func (h *Handler) SaveCodexWorkerAuthFile(c *gin.Context) {
	worker, ok := h.findCodexWorkerConfig(c)
	if !ok {
		return
	}
	var req codexWorkerSaveAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = strings.TrimSpace(worker.AuthFileName)
	}
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "\\") || !strings.HasSuffix(strings.ToLower(name), ".json") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid auth file name"})
		return
	}
	content := strings.TrimSpace(req.Content)
	if len(req.JSON) > 0 {
		content = string(req.JSON)
	}
	if content == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "empty auth file content"})
		return
	}
	var parsed any
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid auth file json"})
		return
	}
	resp, err := h.codexWorkerRequest(c.Request.Context(), worker, http.MethodPost, "/v0/management/auth-files?name="+url.QueryEscape(name), bytes.NewReader([]byte(content)), "application/json")
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		c.Data(resp.StatusCode, "application/json; charset=utf-8", body)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) UpdateCodexWorkerProxy(c *gin.Context) {
	worker, ok := h.findCodexWorkerConfig(c)
	if !ok {
		return
	}
	var req codexWorkerProxyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	body, _ := json.Marshal(gin.H{"value": strings.TrimSpace(req.ProxyURL)})
	resp, err := h.codexWorkerRequest(c.Request.Context(), worker, http.MethodPut, "/v0/management/proxy-url", bytes.NewReader(body), "application/json")
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		c.Data(resp.StatusCode, "application/json; charset=utf-8", respBody)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) UpdateCodexWorkerRouting(c *gin.Context) {
	worker, ok := h.findCodexWorkerConfig(c)
	if !ok {
		return
	}
	var req codexWorkerRoutingRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "enabled is required"})
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cfg == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "config is unavailable"})
		return
	}
	idx, _ := findCodexWorkerRouteProviderIndex(h.cfg.OpenAICompatibility, worker)
	if idx < 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "worker provider route not found"})
		return
	}
	entry := h.cfg.OpenAICompatibility[idx]
	if *req.Enabled {
		entry.ExcludedModels = withoutDisableAllModelsRule(entry.ExcludedModels)
	} else {
		entry.ExcludedModels = withDisableAllModelsRule(entry.ExcludedModels)
	}
	h.cfg.OpenAICompatibility[idx] = entry
	h.cfg.SanitizeOpenAICompatibility()
	h.persistLocked(c)
}

func (h *Handler) ControlCodexWorkerContainer(c *gin.Context) {
	worker, ok := h.findCodexWorkerConfig(c)
	if !ok {
		return
	}
	var req codexWorkerActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	action := strings.ToLower(strings.TrimSpace(req.Action))
	switch action {
	case "start", "stop", "restart":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid action"})
		return
	}
	output, err := runCodexWorkerSSH(c.Request.Context(), worker, "docker", action, worker.Container)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error(), "output": output})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "output": strings.TrimSpace(output)})
}

func (h *Handler) buildCodexWorkerView(ctx context.Context, worker codexWorkerManagementConfig) codexWorkerView {
	view := codexWorkerView{
		ID:            worker.ID,
		Name:          worker.Name,
		Container:     worker.Container,
		BaseURL:       sanitizeWorkerBaseURL(worker.BaseURL),
		SSHConfigured: strings.TrimSpace(worker.SSHTarget) != "" && strings.TrimSpace(worker.Container) != "",
	}
	if view.Name == "" {
		view.Name = view.ID
	}
	routeView := h.codexWorkerRouteView(worker)
	view.RouteConfigured = routeView.configured
	view.RouteEnabled = routeView.enabled
	view.RouteProvider = routeView.provider
	view.RouteIndex = routeView.index
	if status, err := h.codexWorkerContainerStatus(ctx, worker); err == nil {
		view.ContainerStatus = status
	} else if view.SSHConfigured {
		view.Error = strings.TrimSpace(err.Error())
	}
	if proxyURL, err := h.codexWorkerProxyURL(ctx, worker); err == nil {
		view.ProxyURL = proxyURL
	} else {
		appendCodexWorkerError(&view, err)
	}
	authFiles, err := h.codexWorkerAuthFiles(ctx, worker)
	if err != nil {
		appendCodexWorkerError(&view, err)
	} else {
		view.AuthFiles = authFiles
	}
	if len(authFiles) > 0 {
		quota := h.codexWorkerQuota(ctx, worker, authFiles[0])
		view.Quota = quota
	}
	if view.Error == "" {
		view.Health = "ok"
	} else {
		view.Health = "degraded"
	}
	return view
}

type codexWorkerRouteView struct {
	configured bool
	enabled    bool
	provider   string
	index      int
}

func (h *Handler) codexWorkerRouteView(worker codexWorkerManagementConfig) codexWorkerRouteView {
	if h == nil {
		return codexWorkerRouteView{index: -1}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cfg == nil {
		return codexWorkerRouteView{index: -1}
	}
	idx, provider := findCodexWorkerRouteProviderIndex(h.cfg.OpenAICompatibility, worker)
	if idx < 0 {
		return codexWorkerRouteView{index: -1}
	}
	return codexWorkerRouteView{
		configured: true,
		enabled:    !hasDisableAllModelsRule(provider.ExcludedModels),
		provider:   provider.Name,
		index:      idx,
	}
}

func (h *Handler) codexWorkerAuthFiles(ctx context.Context, worker codexWorkerManagementConfig) ([]codexWorkerAuthFileView, error) {
	resp, err := h.codexWorkerRequest(ctx, worker, http.MethodGet, "/v0/management/auth-files", nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("worker auth-files returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var parsed workerAuthFilesResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	out := make([]codexWorkerAuthFileView, 0, len(parsed.Files))
	for _, item := range parsed.Files {
		out = append(out, codexWorkerAuthFileView{
			ID:        stringFromAny(item["id"]),
			Name:      firstStringFromMap(item, "name", "file_name"),
			Email:     firstStringFromMap(item, "email", "account"),
			Provider:  stringFromAny(item["provider"]),
			Type:      stringFromAny(item["type"]),
			AuthIndex: firstStringFromMap(item, "auth_index", "authIndex"),
			Disabled:  boolFromAny(item["disabled"]),
			Status:    stringFromAny(item["status"]),
			Account:   stringFromAny(item["account"]),
			IDToken:   mapFromAny(item["id_token"]),
		})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

func (h *Handler) codexWorkerProxyURL(ctx context.Context, worker codexWorkerManagementConfig) (string, error) {
	resp, err := h.codexWorkerRequest(ctx, worker, http.MethodGet, "/v0/management/proxy-url", nil, "")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("worker proxy-url returned %d", resp.StatusCode)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	return firstStringFromMap(parsed, "proxy-url", "proxy_url", "value"), nil
}

func (h *Handler) codexWorkerQuota(ctx context.Context, worker codexWorkerManagementConfig, auth codexWorkerAuthFileView) *codexWorkerQuotaView {
	authIndex := strings.TrimSpace(auth.AuthIndex)
	if authIndex == "" {
		return &codexWorkerQuotaView{Error: "missing auth_index"}
	}
	accountID := firstStringFromMap(auth.IDToken, "chatgpt_account_id", "chatgptAccountID")
	if accountID == "" {
		return &codexWorkerQuotaView{Error: "missing chatgpt_account_id"}
	}
	payload := gin.H{
		"auth_index": authIndex,
		"method":     http.MethodGet,
		"url":        codexUsageURL,
		"header": gin.H{
			"Authorization":      "Bearer $TOKEN$",
			"Content-Type":       "application/json",
			"User-Agent":         "codex_cli_rs/0.76.0 (Debian 13.0.0; x86_64) WindowsTerminal",
			"Chatgpt-Account-Id": accountID,
		},
	}
	body, _ := json.Marshal(payload)
	resp, err := h.codexWorkerRequest(ctx, worker, http.MethodPost, "/v0/management/api-call", bytes.NewReader(body), "application/json")
	if err != nil {
		return &codexWorkerQuotaView{Error: err.Error()}
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return &codexWorkerQuotaView{Error: err.Error()}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &codexWorkerQuotaView{StatusCode: resp.StatusCode, Error: strings.TrimSpace(string(respBody))}
	}
	var apiResp workerAPICallResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return &codexWorkerQuotaView{Error: err.Error()}
	}
	view := &codexWorkerQuotaView{StatusCode: apiResp.StatusCode}
	if apiResp.Body != nil {
		switch body := apiResp.Body.(type) {
		case map[string]any:
			view.Body = body
		case string:
			var usage map[string]any
			if err := json.Unmarshal([]byte(body), &usage); err == nil {
				view.Body = usage
			} else {
				view.Error = body
			}
		default:
			encoded, _ := json.Marshal(body)
			var usage map[string]any
			if err := json.Unmarshal(encoded, &usage); err == nil {
				view.Body = usage
			} else {
				view.Error = string(encoded)
			}
		}
	}
	if apiResp.StatusCode < 200 || apiResp.StatusCode >= 300 {
		if view.Error == "" {
			if apiResp.Body != nil {
				view.Error = fmt.Sprint(apiResp.Body)
			}
		}
	}
	return view
}

func (h *Handler) codexWorkerContainerStatus(ctx context.Context, worker codexWorkerManagementConfig) (string, error) {
	if strings.TrimSpace(worker.SSHTarget) == "" || strings.TrimSpace(worker.Container) == "" {
		return "", nil
	}
	out, err := runCodexWorkerSSH(ctx, worker, "docker", "inspect", "-f", "{{.State.Status}}", worker.Container)
	return strings.TrimSpace(out), err
}

func (h *Handler) codexWorkerRequest(ctx context.Context, worker codexWorkerManagementConfig, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	baseURL := sanitizeWorkerBaseURL(worker.BaseURL)
	if baseURL == "" {
		return nil, fmt.Errorf("worker %s missing base_url", worker.ID)
	}
	if strings.TrimSpace(worker.ManagementKey) == "" {
		return nil, fmt.Errorf("worker %s missing management_key", worker.ID)
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+worker.ManagementKey)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	client := &http.Client{
		Timeout: 45 * time.Second,
		Transport: &http.Transport{
			Proxy: nil,
		},
	}
	return client.Do(req)
}

func findCodexWorkerRouteProviderIndex(entries []config.OpenAICompatibility, worker codexWorkerManagementConfig) (int, config.OpenAICompatibility) {
	workerID := strings.ToLower(strings.TrimSpace(worker.ID))
	workerName := strings.ToLower(strings.TrimSpace(worker.Name))
	workerBaseURL := sanitizeWorkerBaseURL(worker.BaseURL)
	for i := range entries {
		entry := entries[i]
		providerName := strings.ToLower(strings.TrimSpace(entry.Name))
		baseURL := sanitizeWorkerBaseURL(entry.BaseURL)
		if workerBaseURL != "" && baseURL == workerBaseURL {
			return i, entry
		}
		if workerID != "" && providerName == workerID {
			return i, entry
		}
		if workerName != "" && providerName == workerName {
			return i, entry
		}
	}
	return -1, config.OpenAICompatibility{}
}

func hasDisableAllModelsRule(models []string) bool {
	for _, model := range models {
		if strings.TrimSpace(model) == "*" {
			return true
		}
	}
	return false
}

func withoutDisableAllModelsRule(models []string) []string {
	out := make([]string, 0, len(models))
	for _, model := range models {
		if strings.TrimSpace(model) == "*" {
			continue
		}
		out = append(out, model)
	}
	return config.NormalizeExcludedModels(out)
}

func withDisableAllModelsRule(models []string) []string {
	out := withoutDisableAllModelsRule(models)
	out = append(out, "*")
	return config.NormalizeExcludedModels(out)
}

func (h *Handler) findCodexWorkerConfig(c *gin.Context) (codexWorkerManagementConfig, bool) {
	id := strings.TrimSpace(c.Param("id"))
	workers, err := h.loadCodexWorkerManagementConfigs()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return codexWorkerManagementConfig{}, false
	}
	for _, worker := range workers {
		if worker.ID == id || worker.Name == id {
			return worker, true
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "worker not found"})
	return codexWorkerManagementConfig{}, false
}

func (h *Handler) loadCodexWorkerManagementConfigs() ([]codexWorkerManagementConfig, error) {
	path := strings.TrimSpace(os.Getenv(codexWorkerManagementFileEnv))
	if path == "" {
		candidates := []string{defaultCodexWorkerFileName}
		if h != nil && strings.TrimSpace(h.configFilePath) != "" {
			candidates = append([]string{filepath.Join(filepath.Dir(h.configFilePath), defaultCodexWorkerFileName)}, candidates...)
		}
		for _, candidate := range candidates {
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
				break
			}
		}
	}
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read codex worker management file: %w", err)
	}
	var parsed codexWorkerManagementFile
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parse codex worker management file: %w", err)
	}
	out := make([]codexWorkerManagementConfig, 0, len(parsed.Workers))
	for _, worker := range parsed.Workers {
		worker.ID = strings.TrimSpace(worker.ID)
		worker.Name = strings.TrimSpace(worker.Name)
		worker.Container = strings.TrimSpace(worker.Container)
		worker.BaseURL = sanitizeWorkerBaseURL(worker.BaseURL)
		worker.SSHTarget = strings.TrimSpace(worker.SSHTarget)
		worker.ManagementKey = strings.TrimSpace(worker.ManagementKey)
		worker.AuthFileName = strings.TrimSpace(worker.AuthFileName)
		if worker.ID == "" && worker.Name != "" {
			worker.ID = worker.Name
		}
		if worker.Name == "" {
			worker.Name = worker.ID
		}
		if worker.ID != "" {
			out = append(out, worker)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func runCodexWorkerSSH(ctx context.Context, worker codexWorkerManagementConfig, args ...string) (string, error) {
	target := strings.TrimSpace(worker.SSHTarget)
	if target == "" {
		return "", fmt.Errorf("worker %s missing ssh_target", worker.ID)
	}
	sshArgs := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=8",
		"-o", "StrictHostKeyChecking=accept-new",
	}
	sshArgs = append(sshArgs, worker.SSHOptions...)
	sshArgs = append(sshArgs, target)
	sshArgs = append(sshArgs, args...)
	cmd := exec.CommandContext(ctx, "ssh", sshArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("ssh command failed: %w", err)
	}
	return string(output), nil
}

func sanitizeWorkerBaseURL(raw string) string {
	baseURL := strings.TrimRight(strings.TrimSpace(raw), "/")
	baseURL = strings.TrimSuffix(baseURL, "/v1")
	return strings.TrimRight(baseURL, "/")
}

func appendCodexWorkerError(view *codexWorkerView, err error) {
	if err == nil {
		return
	}
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return
	}
	if view.Error == "" {
		view.Error = msg
	} else {
		view.Error += "; " + msg
	}
}

func stringFromAny(value any) string {
	return strings.TrimSpace(fmt.Sprint(value))
}

func firstStringFromMap(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if values == nil {
			return ""
		}
		if value, ok := values[key]; ok {
			if out := stringFromAny(value); out != "" && out != "<nil>" {
				return out
			}
		}
	}
	return ""
}

func boolFromAny(value any) bool {
	if b, ok := value.(bool); ok {
		return b
	}
	return strings.EqualFold(strings.TrimSpace(fmt.Sprint(value)), "true")
}

func mapFromAny(value any) map[string]any {
	if out, ok := value.(map[string]any); ok {
		return out
	}
	return nil
}
