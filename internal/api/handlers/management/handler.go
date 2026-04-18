// Package management provides the management API handlers and middleware
// for configuring the server and managing auth files.
package management

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/apikeyconfig"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/apikeygroup"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/billing"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/buildinfo"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/managementauth"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/policy"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/sessiontrajectory"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	sdkAuth "github.com/router-for-me/CLIProxyAPI/v6/sdk/auth"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

type attemptInfo struct {
	count        int
	blockedUntil time.Time
	lastActivity time.Time // track last activity for cleanup
}

// attemptCleanupInterval controls how often stale IP entries are purged
const attemptCleanupInterval = 1 * time.Hour

// attemptMaxIdleTime controls how long an IP can be idle before cleanup
const attemptMaxIdleTime = 2 * time.Hour

// Handler aggregates config reference, persistence path and helpers.
type Handler struct {
	cfg                    *config.Config
	configFilePath         string
	mu                     sync.Mutex
	attemptsMu             sync.Mutex
	failedAttempts         map[string]*attemptInfo // keyed by client IP
	authManager            *coreauth.Manager
	usageStats             *usage.RequestStatistics
	billingStore           billing.Store
	dailyLimiter           policy.DailyLimiter
	groupStore             apikeygroup.Store
	apiKeyConfigStore      apikeyconfig.Store
	managementUserStore    managementauth.Store
	sessionManager         *managementauth.SessionManager
	sessionTrajectoryStore sessiontrajectory.Store
	sessionExportRoot      string
	tokenStore             coreauth.Store
	localPassword          string
	allowRemoteOverride    bool
	envSecret              string
	logDir                 string
	postAuthHook           func(context.Context, *coreauth.Auth) error
	configUpdated          func(*config.Config)
}

// NewHandler creates a new management handler instance.
func NewHandler(cfg *config.Config, configFilePath string, manager *coreauth.Manager) *Handler {
	envSecret, _ := os.LookupEnv("MANAGEMENT_PASSWORD")
	envSecret = strings.TrimSpace(envSecret)

	h := &Handler{
		cfg:                 cfg,
		configFilePath:      configFilePath,
		failedAttempts:      make(map[string]*attemptInfo),
		authManager:         manager,
		usageStats:          usage.GetRequestStatistics(),
		tokenStore:          sdkAuth.GetTokenStore(),
		allowRemoteOverride: envSecret != "",
		envSecret:           envSecret,
	}
	h.startAttemptCleanup()
	return h
}

// startAttemptCleanup launches a background goroutine that periodically
// removes stale IP entries from failedAttempts to prevent memory leaks.
func (h *Handler) startAttemptCleanup() {
	go func() {
		ticker := time.NewTicker(attemptCleanupInterval)
		defer ticker.Stop()
		for range ticker.C {
			h.purgeStaleAttempts()
		}
	}()
}

// purgeStaleAttempts removes IP entries that have been idle beyond attemptMaxIdleTime
// and whose ban (if any) has expired.
func (h *Handler) purgeStaleAttempts() {
	now := time.Now()
	h.attemptsMu.Lock()
	defer h.attemptsMu.Unlock()
	for ip, ai := range h.failedAttempts {
		// Skip if still banned
		if !ai.blockedUntil.IsZero() && now.Before(ai.blockedUntil) {
			continue
		}
		// Remove if idle too long
		if now.Sub(ai.lastActivity) > attemptMaxIdleTime {
			delete(h.failedAttempts, ip)
		}
	}
}

// NewHandler creates a new management handler instance.
func NewHandlerWithoutConfigFilePath(cfg *config.Config, manager *coreauth.Manager) *Handler {
	return NewHandler(cfg, "", manager)
}

// SetConfig updates the in-memory config reference when the server hot-reloads.
func (h *Handler) SetConfig(cfg *config.Config) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.cfg = cfg
	h.mu.Unlock()
}

// SetAuthManager updates the auth manager reference used by management endpoints.
func (h *Handler) SetAuthManager(manager *coreauth.Manager) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.authManager = manager
	h.mu.Unlock()
}

// SetUsageStatistics allows replacing the usage statistics reference.
func (h *Handler) SetUsageStatistics(stats *usage.RequestStatistics) { h.usageStats = stats }

func (h *Handler) SetBillingStore(store billing.Store) { h.billingStore = store }

func (h *Handler) SetDailyLimiter(limiter policy.DailyLimiter) { h.dailyLimiter = limiter }

func (h *Handler) SetGroupStore(store apikeygroup.Store) { h.groupStore = store }

func (h *Handler) SetAPIKeyConfigStore(store apikeyconfig.Store) { h.apiKeyConfigStore = store }

func (h *Handler) SetManagementUserStore(store managementauth.Store) { h.managementUserStore = store }

func (h *Handler) SetSessionManager(manager *managementauth.SessionManager) {
	h.sessionManager = manager
}

func (h *Handler) HasManagementUserStore() bool {
	return h != nil && h.managementUserStore != nil && h.sessionManager != nil
}

func (h *Handler) SetSessionTrajectoryStore(store sessiontrajectory.Store, exportRoot string) {
	h.sessionTrajectoryStore = store
	h.sessionExportRoot = strings.TrimSpace(exportRoot)
}

// SetLocalPassword configures the runtime-local password accepted for localhost requests.
func (h *Handler) SetLocalPassword(password string) { h.localPassword = password }

// SetLogDirectory updates the directory where main.log should be looked up.
func (h *Handler) SetLogDirectory(dir string) {
	if dir == "" {
		return
	}
	if !filepath.IsAbs(dir) {
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
	}
	h.logDir = dir
}

func (h *Handler) SetPostAuthHook(hook coreauth.PostAuthHook) { h.postAuthHook = hook }

func (h *Handler) SetConfigUpdatedCallback(callback func(*config.Config)) { h.configUpdated = callback }

// Middleware enforces access control for management endpoints.
// All requests (local and remote) require a valid management key.
// Additionally, remote access requires allow-remote-management=true.
func (h *Handler) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-CPA-VERSION", buildinfo.Version)
		c.Header("X-CPA-COMMIT", buildinfo.Commit)
		c.Header("X-CPA-BUILD-DATE", buildinfo.BuildDate)
		if !h.authenticateManagementRequest(c) {
			return
		}
		c.Next()
	}
}

// persist saves the current in-memory config to disk.
func (h *Handler) persist(c *gin.Context) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.persistLocked(c)
}

// persistLocked saves the current in-memory config to disk.
// It expects the caller to hold h.mu.
func (h *Handler) persistLocked(c *gin.Context) bool {
	persistCfg := h.cfg
	if h.apiKeyConfigStore != nil {
		persistCfg = apikeyconfig.ConfigWithoutAPIKeyState(h.cfg)
	}
	// Preserve comments when writing
	if err := config.SaveConfigPreserveComments(h.configFilePath, persistCfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to save config: %v", err)})
		return false
	}
	currentCfg := h.cfg
	h.notifyConfigUpdated(currentCfg)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
	return true
}

func (h *Handler) persistAPIKeyConfig(c *gin.Context) bool {
	h.mu.Lock()
	if h.apiKeyConfigStore == nil {
		h.mu.Unlock()
		return h.persist(c)
	}
	currentCfg := h.cfg
	state := apikeyconfig.StateFromConfig(currentCfg)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err := h.apiKeyConfigStore.SaveState(ctx, state)
	cancel()
	h.mu.Unlock()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to save api key config: %v", err)})
		return false
	}
	h.notifyConfigUpdated(currentCfg)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
	return true
}

func (h *Handler) persistAPIKeyRecord(c *gin.Context, previousAPIKey, apiKey string) bool {
	h.mu.Lock()
	if h.apiKeyConfigStore == nil {
		h.mu.Unlock()
		return h.persist(c)
	}

	currentCfg := h.cfg
	record, ok := apikeyconfig.RecordFromConfig(currentCfg, apiKey)
	if !ok {
		h.mu.Unlock()
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to build api key record for %q", apiKey)})
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err := h.apiKeyConfigStore.SaveRecord(ctx, previousAPIKey, record)
	cancel()
	h.mu.Unlock()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to save api key record: %v", err)})
		return false
	}
	h.notifyConfigUpdated(currentCfg)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
	return true
}

func (h *Handler) deletePersistedAPIKeyRecord(c *gin.Context, apiKey string) bool {
	h.mu.Lock()
	if h.apiKeyConfigStore == nil {
		h.mu.Unlock()
		return h.persist(c)
	}

	currentCfg := h.cfg
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err := h.apiKeyConfigStore.DeleteRecord(ctx, apiKey)
	cancel()
	h.mu.Unlock()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to delete api key record: %v", err)})
		return false
	}
	h.notifyConfigUpdated(currentCfg)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
	return true
}

func (h *Handler) notifyConfigUpdated(cfg *config.Config) {
	if h == nil || h.configUpdated == nil || cfg == nil {
		return
	}
	h.configUpdated(cfg)
}

// Helper methods for simple types
func (h *Handler) updateBoolField(c *gin.Context, set func(bool)) {
	var body struct {
		Value *bool `json:"value"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Value == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	set(*body.Value)
	h.persist(c)
}

func (h *Handler) updateIntField(c *gin.Context, set func(int)) {
	var body struct {
		Value *int `json:"value"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Value == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	set(*body.Value)
	h.persist(c)
}

func (h *Handler) updateStringField(c *gin.Context, set func(string)) {
	var body struct {
		Value *string `json:"value"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Value == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	set(*body.Value)
	h.persist(c)
}
