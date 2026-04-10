package management

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/managementauth"
	"golang.org/x/crypto/bcrypt"
)

const (
	managementMaxFailures = 5
	managementBanDuration = 30 * time.Minute
)

type managementPrincipal struct {
	Username   string
	Role       managementauth.Role
	AuthSource string
}

const managementPrincipalContextKey = "management_principal"

type managementLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type managementLoginResponse struct {
	Token     string `json:"token"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	ExpiresAt string `json:"expires_at"`
}

type managementMeResponse struct {
	Username   string `json:"username"`
	Role       string `json:"role"`
	AuthSource string `json:"auth_source"`
}

func (h *Handler) Login(c *gin.Context) {
	if h.managementUserStore == nil || h.sessionManager == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username/password login unavailable"})
		return
	}
	clientIP, localClient, fail, ok := h.prepareManagementAccess(c)
	if !ok {
		return
	}

	var body managementLoginRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	username := strings.TrimSpace(body.Username)
	password := strings.TrimSpace(body.Password)
	if username == "" || password == "" {
		if !localClient {
			fail()
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid username or password"})
		return
	}

	user, found, err := h.managementUserStore.GetByUsername(c.Request.Context(), username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load management user"})
		return
	}
	if !found || !user.Enabled || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		if !localClient {
			fail()
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid username or password"})
		return
	}

	if !localClient {
		h.clearFailedAttempts(clientIP)
	}
	session, err := h.sessionManager.Create(user.Username, user.Role, user.PasswordHash)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create management session"})
		return
	}
	c.JSON(http.StatusOK, managementLoginResponse{
		Token:     session.Token,
		Username:  user.Username,
		Role:      string(user.Role),
		ExpiresAt: session.ExpiresAt.Format(time.RFC3339),
	})
}

func (h *Handler) Logout(c *gin.Context) {
	if token := extractManagementCredential(c); token != "" && strings.HasPrefix(token, "mgmt_session_") && h.sessionManager != nil {
		h.sessionManager.Delete(token)
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) Me(c *gin.Context) {
	principal := managementPrincipalFromContext(c)
	if principal == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	c.JSON(http.StatusOK, managementMeResponse{
		Username:   principal.Username,
		Role:       string(principal.Role),
		AuthSource: principal.AuthSource,
	})
}

func (h *Handler) RequireRoles(roles ...managementauth.Role) gin.HandlerFunc {
	allowed := make(map[managementauth.Role]struct{}, len(roles))
	for _, role := range roles {
		allowed[normalizeManagementRole(role)] = struct{}{}
	}
	return func(c *gin.Context) {
		principal := managementPrincipalFromContext(c)
		if principal == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		if _, ok := allowed[normalizeManagementRole(principal.Role)]; !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		c.Next()
	}
}

func (h *Handler) authenticateManagementRequest(c *gin.Context) bool {
	clientIP, localClient, fail, ok := h.prepareManagementAccess(c)
	if !ok {
		return false
	}

	provided := extractManagementCredential(c)
	localPasswordEnabled := localClient && strings.TrimSpace(h.localPassword) != ""
	secretHash := ""
	if h.cfg != nil {
		secretHash = strings.TrimSpace(h.cfg.RemoteManagement.SecretKey)
	}
	envSecret := strings.TrimSpace(h.envSecret)

	if provided == "" {
		if !localClient {
			fail()
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing management authorization"})
		return false
	}

	if localPasswordEnabled && subtle.ConstantTimeCompare([]byte(provided), []byte(strings.TrimSpace(h.localPassword))) == 1 {
		setManagementPrincipal(c, managementPrincipal{
			Username:   "local_admin",
			Role:       managementauth.RoleAdmin,
			AuthSource: "local_password",
		})
		return true
	}

	if h.sessionManager != nil {
		if session, ok := h.sessionManager.Get(provided); ok {
			if h.managementUserStore != nil {
				user, found, err := h.managementUserStore.GetByUsername(c.Request.Context(), session.Username)
				if err != nil {
					c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to validate management session"})
					return false
				}
				if !found ||
					!user.Enabled ||
					normalizeManagementRole(user.Role) != normalizeManagementRole(session.Role) ||
					strings.TrimSpace(user.PasswordHash) != strings.TrimSpace(session.PasswordHash) {
					h.sessionManager.Delete(provided)
					c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "management session expired"})
					return false
				}
			}
			if !localClient {
				h.clearFailedAttempts(clientIP)
			}
			setManagementPrincipal(c, managementPrincipal{
				Username:   session.Username,
				Role:       session.Role,
				AuthSource: "session",
			})
			return true
		}
	}

	if envSecret != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(envSecret)) == 1 {
		if !localClient {
			h.clearFailedAttempts(clientIP)
		}
		setManagementPrincipal(c, managementPrincipal{
			Username:   "legacy_admin",
			Role:       managementauth.RoleAdmin,
			AuthSource: "env_secret",
		})
		return true
	}

	if secretHash != "" && bcrypt.CompareHashAndPassword([]byte(secretHash), []byte(provided)) == nil {
		if !localClient {
			h.clearFailedAttempts(clientIP)
		}
		setManagementPrincipal(c, managementPrincipal{
			Username:   "legacy_admin",
			Role:       managementauth.RoleAdmin,
			AuthSource: "config_secret",
		})
		return true
	}

	if !localClient {
		fail()
	}
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid management authorization"})
	return false
}

func (h *Handler) prepareManagementAccess(c *gin.Context) (clientIP string, localClient bool, fail func(), ok bool) {
	clientIP = c.ClientIP()
	localClient = clientIP == "127.0.0.1" || clientIP == "::1"
	fail = func() {}

	if localClient {
		return clientIP, true, fail, true
	}

	h.attemptsMu.Lock()
	ai := h.failedAttempts[clientIP]
	if ai != nil {
		if !ai.blockedUntil.IsZero() {
			if time.Now().Before(ai.blockedUntil) {
				remaining := time.Until(ai.blockedUntil).Round(time.Second)
				h.attemptsMu.Unlock()
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "IP banned due to too many failed attempts. Try again in " + remaining.String()})
				return clientIP, false, fail, false
			}
			ai.blockedUntil = time.Time{}
			ai.count = 0
		}
	}
	h.attemptsMu.Unlock()

	allowRemote := false
	if h.cfg != nil {
		allowRemote = h.cfg.RemoteManagement.AllowRemote
	}
	if h.allowRemoteOverride {
		allowRemote = true
	}
	if !allowRemote {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "remote management disabled"})
		return clientIP, false, fail, false
	}

	fail = func() {
		h.attemptsMu.Lock()
		defer h.attemptsMu.Unlock()
		info := h.failedAttempts[clientIP]
		if info == nil {
			info = &attemptInfo{}
			h.failedAttempts[clientIP] = info
		}
		info.count++
		info.lastActivity = time.Now()
		if info.count >= managementMaxFailures {
			info.blockedUntil = time.Now().Add(managementBanDuration)
			info.count = 0
		}
	}
	return clientIP, false, fail, true
}

func (h *Handler) clearFailedAttempts(clientIP string) {
	if strings.TrimSpace(clientIP) == "" {
		return
	}
	h.attemptsMu.Lock()
	if ai := h.failedAttempts[clientIP]; ai != nil {
		ai.count = 0
		ai.blockedUntil = time.Time{}
		ai.lastActivity = time.Now()
	}
	h.attemptsMu.Unlock()
}

func extractManagementCredential(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if ah := strings.TrimSpace(c.GetHeader("Authorization")); ah != "" {
		parts := strings.SplitN(ah, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
			return strings.TrimSpace(parts[1])
		}
		return ah
	}
	return strings.TrimSpace(c.GetHeader("X-Management-Key"))
}

func setManagementPrincipal(c *gin.Context, principal managementPrincipal) {
	if c == nil {
		return
	}
	c.Set(managementPrincipalContextKey, principal)
}

func managementPrincipalFromContext(c *gin.Context) *managementPrincipal {
	if c == nil {
		return nil
	}
	raw, ok := c.Get(managementPrincipalContextKey)
	if !ok {
		return nil
	}
	principal, ok := raw.(managementPrincipal)
	if !ok {
		return nil
	}
	return &principal
}

func normalizeManagementRole(role managementauth.Role) managementauth.Role {
	if role == managementauth.RoleStaff {
		return managementauth.RoleStaff
	}
	return managementauth.RoleAdmin
}

func managementUsername(c *gin.Context) string {
	principal := managementPrincipalFromContext(c)
	if principal == nil || strings.TrimSpace(principal.Username) == "" {
		return "unknown"
	}
	return strings.TrimSpace(principal.Username)
}

func managementRole(c *gin.Context) managementauth.Role {
	principal := managementPrincipalFromContext(c)
	if principal == nil {
		return managementauth.RoleAdmin
	}
	return normalizeManagementRole(principal.Role)
}

func managementIsStaff(c *gin.Context) bool {
	return managementRole(c) == managementauth.RoleStaff
}
