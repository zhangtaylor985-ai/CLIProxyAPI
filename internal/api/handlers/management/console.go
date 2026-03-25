package management

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"gopkg.in/yaml.v3"
)

type consoleClaudeAuthFile struct {
	Name     string
	Email    string
	Disabled bool
}

type consoleAuthFilePayload struct {
	Type     string `json:"type"`
	Email    string `json:"email"`
	Disabled bool   `json:"disabled"`
}

func (h *Handler) consoleBaseDir() string {
	if h != nil {
		if configPath := strings.TrimSpace(h.configFilePath); configPath != "" {
			return filepath.Join(filepath.Dir(configPath), "mocks")
		}
	}

	wd, err := os.Getwd()
	if err != nil {
		return "mocks"
	}
	return filepath.Join(wd, "mocks")
}

func (h *Handler) consoleConfigPath() string {
	return filepath.Join(h.consoleBaseDir(), "config.yaml")
}

func (h *Handler) ensureConsoleBaseDir() error {
	return os.MkdirAll(h.consoleBaseDir(), 0o755)
}

func (h *Handler) GetConsoleConfig(c *gin.Context) {
	data, err := os.ReadFile(h.consoleConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(404, gin.H{"error": "not_found", "message": "config file not found"})
			return
		}
		c.JSON(500, gin.H{"error": "read_failed", "message": err.Error()})
		return
	}

	var payload map[string]any
	if err := yaml.Unmarshal(data, &payload); err != nil {
		c.JSON(500, gin.H{"error": "invalid_yaml", "message": err.Error()})
		return
	}

	c.JSON(200, payload)
}

func (h *Handler) GetConsoleConfigYAML(c *gin.Context) {
	data, err := os.ReadFile(h.consoleConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(404, gin.H{"error": "not_found", "message": "config file not found"})
			return
		}
		c.JSON(500, gin.H{"error": "read_failed", "message": err.Error()})
		return
	}
	c.Header("Content-Type", "application/yaml; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	_, _ = c.Writer.Write(data)
}

func (h *Handler) PutConsoleConfigYAML(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid_yaml", "message": "cannot read request body"})
		return
	}

	var cfg config.Config
	if err := yaml.Unmarshal(body, &cfg); err != nil {
		c.JSON(400, gin.H{"error": "invalid_yaml", "message": err.Error()})
		return
	}

	if err := h.ensureConsoleBaseDir(); err != nil {
		c.JSON(500, gin.H{"error": "write_failed", "message": err.Error()})
		return
	}

	tmpFile, err := os.CreateTemp(h.consoleBaseDir(), "console-config-validate-*.yaml")
	if err != nil {
		c.JSON(500, gin.H{"error": "write_failed", "message": err.Error()})
		return
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(body); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		c.JSON(500, gin.H{"error": "write_failed", "message": err.Error()})
		return
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		c.JSON(500, gin.H{"error": "write_failed", "message": err.Error()})
		return
	}
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := config.LoadConfigOptional(tmpPath, false); err != nil {
		c.JSON(422, gin.H{"error": "invalid_config", "message": err.Error()})
		return
	}

	if err := WriteConfig(h.consoleConfigPath(), body); err != nil {
		c.JSON(500, gin.H{"error": "write_failed", "message": err.Error()})
		return
	}

	c.JSON(200, gin.H{"ok": true, "changed": []string{"console-config"}})
}

func (h *Handler) ListConsoleAuthFiles(c *gin.Context) {
	files, err := h.listConsoleAuthFileEntries()
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"files": files})
}

func (h *Handler) DownloadConsoleAuthFile(c *gin.Context) {
	name := filepath.Base(strings.TrimSpace(c.Query("name")))
	if name == "" || name == "." || strings.Contains(name, string(os.PathSeparator)) {
		c.JSON(400, gin.H{"error": "invalid name"})
		return
	}
	if !strings.HasSuffix(strings.ToLower(name), ".json") {
		c.JSON(400, gin.H{"error": "name must end with .json"})
		return
	}

	data, err := os.ReadFile(filepath.Join(h.consoleBaseDir(), name))
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(404, gin.H{"error": "file not found"})
			return
		}
		c.JSON(500, gin.H{"error": fmt.Sprintf("failed to read file: %v", err)})
		return
	}

	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", name))
	c.Data(200, "application/json", data)
}

func (h *Handler) UploadConsoleAuthFile(c *gin.Context) {
	if err := h.ensureConsoleBaseDir(); err != nil {
		c.JSON(500, gin.H{"error": fmt.Sprintf("failed to prepare data dir: %v", err)})
		return
	}

	if file, err := c.FormFile("file"); err == nil && file != nil {
		name := filepath.Base(strings.TrimSpace(file.Filename))
		if !strings.HasSuffix(strings.ToLower(name), ".json") {
			c.JSON(400, gin.H{"error": "file must be .json"})
			return
		}
		dst := filepath.Join(h.consoleBaseDir(), name)
		if err := c.SaveUploadedFile(file, dst); err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("failed to save file: %v", err)})
			return
		}
		c.JSON(200, gin.H{"status": "ok"})
		return
	}

	name := filepath.Base(strings.TrimSpace(c.Query("name")))
	if name == "" || name == "." || strings.Contains(name, string(os.PathSeparator)) {
		c.JSON(400, gin.H{"error": "invalid name"})
		return
	}
	if !strings.HasSuffix(strings.ToLower(name), ".json") {
		c.JSON(400, gin.H{"error": "name must end with .json"})
		return
	}

	data, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(400, gin.H{"error": "failed to read body"})
		return
	}

	if !json.Valid(bytes.TrimSpace(data)) {
		c.JSON(400, gin.H{"error": "invalid json"})
		return
	}

	if err := os.WriteFile(filepath.Join(h.consoleBaseDir(), name), data, 0o600); err != nil {
		c.JSON(500, gin.H{"error": fmt.Sprintf("failed to write file: %v", err)})
		return
	}

	c.JSON(200, gin.H{"status": "ok"})
}

func (h *Handler) DeleteConsoleAuthFile(c *gin.Context) {
	if all := c.Query("all"); all == "true" || all == "1" || all == "*" {
		entries, err := os.ReadDir(h.consoleBaseDir())
		if err != nil {
			if os.IsNotExist(err) {
				c.JSON(200, gin.H{"status": "ok", "deleted": 0})
				return
			}
			c.JSON(500, gin.H{"error": fmt.Sprintf("failed to read data dir: %v", err)})
			return
		}
		deleted := 0
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !strings.HasSuffix(strings.ToLower(name), ".json") {
				continue
			}
			if err := os.Remove(filepath.Join(h.consoleBaseDir(), name)); err == nil {
				deleted++
			}
		}
		c.JSON(200, gin.H{"status": "ok", "deleted": deleted})
		return
	}

	name := filepath.Base(strings.TrimSpace(c.Query("name")))
	if name == "" || name == "." || strings.Contains(name, string(os.PathSeparator)) {
		c.JSON(400, gin.H{"error": "invalid name"})
		return
	}
	if err := os.Remove(filepath.Join(h.consoleBaseDir(), name)); err != nil {
		if os.IsNotExist(err) {
			c.JSON(404, gin.H{"error": "file not found"})
			return
		}
		c.JSON(500, gin.H{"error": fmt.Sprintf("failed to remove file: %v", err)})
		return
	}
	c.JSON(200, gin.H{"status": "ok"})
}

func (h *Handler) listConsoleAuthFileEntries() ([]gin.H, error) {
	entries, err := os.ReadDir(h.consoleBaseDir())
	if err != nil {
		if os.IsNotExist(err) {
			return []gin.H{}, nil
		}
		return nil, fmt.Errorf("failed to read data dir: %w", err)
	}

	files := make([]gin.H, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".json") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		fullPath := filepath.Join(h.consoleBaseDir(), name)
		fileData := gin.H{
			"name":     name,
			"size":     info.Size(),
			"modtime":  info.ModTime(),
			"provider": "unknown",
			"type":     "unknown",
			"source":   "local",
		}

		if data, err := os.ReadFile(fullPath); err == nil {
			var payload map[string]any
			if err := json.Unmarshal(data, &payload); err == nil {
				if value := strings.TrimSpace(scalarStringValue(payload["type"])); value != "" {
					fileData["type"] = value
					fileData["provider"] = value
				}
				if value := strings.TrimSpace(scalarStringValue(payload["email"])); value != "" {
					fileData["email"] = value
				}
				if disabled, ok := boolValue(payload["disabled"]); ok {
					fileData["disabled"] = disabled
				}
			}
		}

		files = append(files, fileData)
	}

	sort.Slice(files, func(i, j int) bool {
		nameI, _ := files[i]["name"].(string)
		nameJ, _ := files[j]["name"].(string)
		return strings.ToLower(nameI) < strings.ToLower(nameJ)
	})

	return files, nil
}

func (h *Handler) GetConsoleClaudeQuotas(c *gin.Context) {
	files, err := h.listConsoleClaudeAuthFiles()
	if err != nil {
		c.JSON(500, gin.H{"error": fmt.Sprintf("failed to load auth files: %v", err)})
		return
	}

	now := time.Now()
	items := make([]gin.H, 0, len(files))
	for _, file := range files {
		items = append(items, gin.H{
			"name":     file.Name,
			"email":    file.Email,
			"type":     "claude",
			"disabled": file.Disabled,
			"windows":  buildConsoleClaudeQuotaWindows(file, now),
		})
	}

	c.JSON(200, gin.H{
		"generated_at": now.Format(time.RFC3339),
		"items":        items,
	})
}

func (h *Handler) listConsoleClaudeAuthFiles() ([]consoleClaudeAuthFile, error) {
	entries, err := os.ReadDir(h.consoleBaseDir())
	if err != nil {
		if os.IsNotExist(err) {
			return []consoleClaudeAuthFile{}, nil
		}
		return nil, err
	}

	files := make([]consoleClaudeAuthFile, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".json") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(h.consoleBaseDir(), name))
		if err != nil {
			continue
		}

		var payload consoleAuthFilePayload
		if err := json.Unmarshal(data, &payload); err != nil {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(payload.Type), "claude") {
			continue
		}

		files = append(files, consoleClaudeAuthFile{
			Name:     name,
			Email:    strings.TrimSpace(payload.Email),
			Disabled: payload.Disabled,
		})
	}

	sort.Slice(files, func(i, j int) bool {
		return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name)
	})

	return files, nil
}

func buildConsoleClaudeQuotaWindows(file consoleClaudeAuthFile, now time.Time) []gin.H {
	seedBase := file.Name
	if file.Email != "" {
		seedBase = file.Email
	}

	fiveHourPercent, fiveHourReset := computeConsoleFiveHourQuotaPercent(seedBase+":5h", now)
	weeklyPercent, weeklyReset := computeConsoleWeeklyQuotaPercent(seedBase+":7d", now)

	if file.Disabled {
		fiveHourPercent = maxInt(0, fiveHourPercent-12)
		weeklyPercent = maxInt(0, weeklyPercent-12)
	}

	return []gin.H{
		{
			"id":           "five-hour",
			"label":        "5 hours",
			"used_percent": fiveHourPercent,
			"reset_at":     fiveHourReset.Format(time.RFC3339),
		},
		{
			"id":           "seven-day",
			"label":        "7 days",
			"used_percent": weeklyPercent,
			"reset_at":     weeklyReset.Format(time.RFC3339),
		},
	}
}

func computeConsoleWeeklyQuotaPercent(seed string, now time.Time) (int, time.Time) {
	const duration = 7 * 24 * time.Hour

	windowStart := now.Truncate(duration)
	resetAt := windowStart.Add(duration)
	elapsedDays := clampFloat(now.Sub(windowStart).Hours()/24, 0, 7)
	progress := clampFloat(elapsedDays/7, 0, 1)

	windowSeed := fmt.Sprintf("%s:%d", seed, windowStart.Unix())
	minUsed := elapsedDays * 0.10
	maxUsed := elapsedDays * 0.13
	targetUsed := elapsedDays * (0.10 + hashUnitFloat(windowSeed+":daily")*0.03)
	intradayWave := math.Sin(progress*math.Pi*2+hashUnitFloat(windowSeed+":phase")*math.Pi*2) * 0.004
	drift := (hashUnitFloat(windowSeed+":drift") - 0.5) * 0.004
	used := clampFloat(targetUsed+intradayWave+drift, minUsed, maxUsed)
	used = clampFloat(used, 0, 0.88)

	return int(math.Round(used * 100)), resetAt
}

func computeConsoleFiveHourQuotaPercent(seed string, now time.Time) (int, time.Time) {
	const duration = 5 * time.Hour

	windowStart := now.Truncate(duration)
	resetAt := windowStart.Add(duration)
	progress := clampFloat(now.Sub(windowStart).Seconds()/duration.Seconds(), 0, 1)

	windowSeed := fmt.Sprintf("%s:%d", seed, windowStart.Unix())
	targetUsed := 0.58 + hashUnitFloat(windowSeed+":target")*0.14
	curve := 0.12*progress + 0.88*math.Pow(progress, 0.92)
	burst := math.Sin(progress*math.Pi*2+hashUnitFloat(windowSeed+":phase")*math.Pi*2) * (0.012 + progress*0.01)
	drift := (hashUnitFloat(windowSeed+":drift") - 0.5) * 0.018
	used := targetUsed*curve + burst + drift

	minUsed := 0.02 + progress*targetUsed*0.72
	maxUsed := 0.06 + progress*targetUsed*1.02
	used = clampFloat(used, minUsed, math.Min(maxUsed, targetUsed))
	used = clampFloat(used, 0, 0.82)

	return int(math.Round(used * 100)), resetAt
}

func hashUnitFloat(key string) float64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(key))
	return float64(hasher.Sum64()%10000) / 10000.0
}

func clampFloat(value, minValue, maxValue float64) float64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func scalarStringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		if value == nil {
			return ""
		}
		return fmt.Sprint(value)
	}
}

func boolValue(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes", "on":
			return true, true
		case "false", "0", "no", "off":
			return false, true
		}
	}
	return false, false
}
