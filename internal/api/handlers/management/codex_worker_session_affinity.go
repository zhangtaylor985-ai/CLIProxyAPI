package management

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

const defaultCodexWorkerSessionAffinityTTL = "3h"

type codexWorkerSessionAffinityView struct {
	Enabled            bool   `json:"enabled"`
	TTL                string `json:"ttl"`
	SessionAffinityTTL string `json:"session-affinity-ttl"`
}

type codexWorkerSessionAffinityRequest struct {
	Enabled            bool   `json:"enabled"`
	TTL                string `json:"ttl"`
	SessionAffinityTTL string `json:"session-affinity-ttl"`
}

func (h *Handler) GetCodexWorkerSessionAffinity(c *gin.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cfg == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "config is unavailable"})
		return
	}
	c.JSON(http.StatusOK, buildCodexWorkerSessionAffinityView(h.cfg))
}

func (h *Handler) PutCodexWorkerSessionAffinity(c *gin.Context) {
	var req codexWorkerSessionAffinityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	ttlValue := req.TTL
	if strings.TrimSpace(ttlValue) == "" {
		ttlValue = req.SessionAffinityTTL
	}
	ttl := normalizeCodexWorkerSessionAffinityTTL(ttlValue)
	if _, err := time.ParseDuration(ttl); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid ttl"})
		return
	}

	h.mu.Lock()
	if h.cfg == nil {
		h.mu.Unlock()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "config is unavailable"})
		return
	}
	h.cfg.Routing.SessionAffinity = req.Enabled
	h.cfg.Routing.ClaudeCodeSessionAffinity = false
	h.cfg.Routing.SessionAffinityTTL = ttl
	currentCfg := h.cfg
	if err := config.SaveConfigPreserveComments(h.configFilePath, currentCfg); err != nil {
		h.mu.Unlock()
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to save config: %v", err)})
		return
	}
	h.mu.Unlock()
	h.notifyConfigUpdated(currentCfg)

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func buildCodexWorkerSessionAffinityView(cfg *config.Config) codexWorkerSessionAffinityView {
	ttl := normalizeCodexWorkerSessionAffinityTTL(cfg.Routing.SessionAffinityTTL)
	enabled := cfg.Routing.SessionAffinity || cfg.Routing.ClaudeCodeSessionAffinity
	return codexWorkerSessionAffinityView{
		Enabled:            enabled,
		TTL:                ttl,
		SessionAffinityTTL: ttl,
	}
}

func normalizeCodexWorkerSessionAffinityTTL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultCodexWorkerSessionAffinityTTL
	}
	return value
}
