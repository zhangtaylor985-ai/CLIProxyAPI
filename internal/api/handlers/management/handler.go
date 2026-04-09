// Package management provides the management API handlers and middleware
// for configuring the server and managing auth files.
package management

import (
	"context"
	"crypto/subtle"
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
	"github.com/router-for-me/CLIProxyAPI/v6/internal/policy"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/sessiontrajectory"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	sdkAuth "github.com/router-for-me/CLIProxyAPI/v6/sdk/auth"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	"golang.org/x/crypto/bcrypt"
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
func (h *Handler) SetConfig(cfg *config.Config) { h.cfg = cfg }

// SetAuthManager updates the auth manager reference used by management endpoints.
func (h *Handler) SetAuthManager(manager *coreauth.Manager) { h.authManager = manager }

// SetUsageStatistics allows replacing the usage statistics reference.
func (h *Handler) SetUsageStatistics(stats *usage.RequestStatistics) { h.usageStats = stats }

func (h *Handler) SetBillingStore(store billing.Store) { h.billingStore = store }

func (h *Handler) SetDailyLimiter(limiter policy.DailyLimiter) { h.dailyLimiter = limiter }

func (h *Handler) SetGroupStore(store apikeygroup.Store) { h.groupStore = store }

func (h *Handler) SetAPIKeyConfigStore(store apikeyconfig.Store) { h.apiKeyConfigStore = store }

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
	const maxFailures = 5
	const banDuration = 30 * time.Minute

	return func(c *gin.Context) {
		c.Header("X-CPA-VERSION", buildinfo.Version)
		c.Header("X-CPA-COMMIT", buildinfo.Commit)
		c.Header("X-CPA-BUILD-DATE", buildinfo.BuildDate)

		clientIP := c.ClientIP()
		localClient := clientIP == "127.0.0.1" || clientIP == "::1"
		localPasswordEnabled := localClient && strings.TrimSpace(h.localPassword) != ""
		cfg := h.cfg
		var (
			allowRemote bool
			secretHash  string
		)
		if cfg != nil {
			allowRemote = cfg.RemoteManagement.AllowRemote
			secretHash = cfg.RemoteManagement.SecretKey
		}
		if h.allowRemoteOverride {
			allowRemote = true
		}
		envSecret := h.envSecret

		fail := func() {}
		if !localClient {
			h.attemptsMu.Lock()
			ai := h.failedAttempts[clientIP]
			if ai != nil {
				if !ai.blockedUntil.IsZero() {
					if time.Now().Before(ai.blockedUntil) {
						remaining := time.Until(ai.blockedUntil).Round(time.Second)
						h.attemptsMu.Unlock()
						c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf("IP banned due to too many failed attempts. Try again in %s", remaining)})
						return
					}
					// Ban expired, reset state
					ai.blockedUntil = time.Time{}
					ai.count = 0
				}
			}
			h.attemptsMu.Unlock()

			if !allowRemote {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "remote management disabled"})
				return
			}

			fail = func() {
				h.attemptsMu.Lock()
				aip := h.failedAttempts[clientIP]
				if aip == nil {
					aip = &attemptInfo{}
					h.failedAttempts[clientIP] = aip
				}
				aip.count++
				aip.lastActivity = time.Now()
				if aip.count >= maxFailures {
					aip.blockedUntil = time.Now().Add(banDuration)
					aip.count = 0
				}
				h.attemptsMu.Unlock()
			}
		}
		if secretHash == "" && envSecret == "" && !localPasswordEnabled {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "remote management key not set"})
			return
		}

		// Accept either Authorization: Bearer <key> or X-Management-Key
		var provided string
		if ah := c.GetHeader("Authorization"); ah != "" {
			parts := strings.SplitN(ah, " ", 2)
			if len(parts) == 2 && strings.ToLower(parts[0]) == "bearer" {
				provided = parts[1]
			} else {
				provided = ah
			}
		}
		if provided == "" {
			provided = c.GetHeader("X-Management-Key")
		}

		if provided == "" {
			if !localClient {
				fail()
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing management key"})
			return
		}

		if localPasswordEnabled {
			if lp := h.localPassword; lp != "" {
				if subtle.ConstantTimeCompare([]byte(provided), []byte(lp)) == 1 {
					c.Next()
					return
				}
			}
		}

		if envSecret != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(envSecret)) == 1 {
			if !localClient {
				h.attemptsMu.Lock()
				if ai := h.failedAttempts[clientIP]; ai != nil {
					ai.count = 0
					ai.blockedUntil = time.Time{}
				}
				h.attemptsMu.Unlock()
			}
			c.Next()
			return
		}

		if secretHash == "" || bcrypt.CompareHashAndPassword([]byte(secretHash), []byte(provided)) != nil {
			if !localClient {
				fail()
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid management key"})
			return
		}

		if !localClient {
			h.attemptsMu.Lock()
			if ai := h.failedAttempts[clientIP]; ai != nil {
				ai.count = 0
				ai.blockedUntil = time.Time{}
			}
			h.attemptsMu.Unlock()
		}

		c.Next()
	}
}

// persist saves the current in-memory config to disk.
func (h *Handler) persist(c *gin.Context) bool {
	h.mu.Lock()
	persistCfg := h.cfg
	if h.apiKeyConfigStore != nil {
		persistCfg = apikeyconfig.ConfigWithoutAPIKeyState(h.cfg)
	}
	// Preserve comments when writing
	if err := config.SaveConfigPreserveComments(h.configFilePath, persistCfg); err != nil {
		h.mu.Unlock()
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to save config: %v", err)})
		return false
	}
	currentCfg := h.cfg
	h.mu.Unlock()
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
