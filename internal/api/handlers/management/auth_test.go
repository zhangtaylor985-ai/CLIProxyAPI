package management

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/managementauth"
	"golang.org/x/crypto/bcrypt"
)

func TestManagementLoginAndRoleAuthorization(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	passwordHash, err := bcrypt.GenerateFromPassword([]byte("staff-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword: %v", err)
	}

	handler := NewHandlerWithoutConfigFilePath(&config.Config{}, nil)
	handler.SetManagementUserStore(&memoryManagementUserStore{
		items: map[string]managementauth.User{
			"user_01": {
				Username:     "user_01",
				PasswordHash: string(passwordHash),
				Role:         managementauth.RoleStaff,
				Enabled:      true,
			},
		},
	})
	handler.SetSessionManager(managementauth.NewSessionManager(30 * time.Minute))

	router := gin.New()
	public := router.Group("/v0/management")
	public.POST("/login", handler.Login)
	authenticated := router.Group("/v0/management")
	authenticated.Use(handler.Middleware())
	authenticated.GET("/me", handler.Me)
	authenticated.GET("/api-key-records", handler.RequireRoles(managementauth.RoleAdmin, managementauth.RoleStaff), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	authenticated.GET("/api-key-records/item", handler.RequireRoles(managementauth.RoleAdmin, managementauth.RoleStaff), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	authenticated.POST("/api-key-records", handler.RequireRoles(managementauth.RoleAdmin, managementauth.RoleStaff), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	authenticated.DELETE("/api-key-records/item", handler.RequireRoles(managementauth.RoleAdmin, managementauth.RoleStaff), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	authenticated.GET("/admin-only", handler.RequireRoles(managementauth.RoleAdmin), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	authenticated.POST("/api-key-records/stats", handler.RequireRoles(managementauth.RoleAdmin), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	authenticated.GET("/api-key-records/item/events", handler.RequireRoles(managementauth.RoleAdmin), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	authenticated.PUT("/session-trajectory-enabled", handler.RequireRoles(managementauth.RoleAdmin), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	body, _ := json.Marshal(gin.H{
		"username": "user_01",
		"password": "staff-pass",
	})
	req := httptest.NewRequest(http.MethodPost, "/v0/management/login", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body=%s", rec.Code, rec.Body.String())
	}

	var loginResp managementLoginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("Unmarshal login response: %v", err)
	}
	if loginResp.Role != string(managementauth.RoleStaff) || loginResp.Token == "" {
		t.Fatalf("unexpected login response: %+v", loginResp)
	}

	meReq := httptest.NewRequest(http.MethodGet, "/v0/management/me", nil)
	meReq.RemoteAddr = "127.0.0.1:12345"
	meReq.Header.Set("Authorization", "Bearer "+loginResp.Token)
	meRec := httptest.NewRecorder()
	router.ServeHTTP(meRec, meReq)
	if meRec.Code != http.StatusOK {
		t.Fatalf("me status = %d, body=%s", meRec.Code, meRec.Body.String())
	}

	apiKeyReq := httptest.NewRequest(http.MethodGet, "/v0/management/api-key-records", nil)
	apiKeyReq.RemoteAddr = "127.0.0.1:12345"
	apiKeyReq.Header.Set("Authorization", "Bearer "+loginResp.Token)
	apiKeyRec := httptest.NewRecorder()
	router.ServeHTTP(apiKeyRec, apiKeyReq)
	if apiKeyRec.Code != http.StatusOK {
		t.Fatalf("api-key-records status = %d, body=%s", apiKeyRec.Code, apiKeyRec.Body.String())
	}

	adminReq := httptest.NewRequest(http.MethodGet, "/v0/management/admin-only", nil)
	adminReq.RemoteAddr = "127.0.0.1:12345"
	adminReq.Header.Set("Authorization", "Bearer "+loginResp.Token)
	adminRec := httptest.NewRecorder()
	router.ServeHTTP(adminRec, adminReq)
	if adminRec.Code != http.StatusForbidden {
		t.Fatalf("admin-only status = %d, want 403, body=%s", adminRec.Code, adminRec.Body.String())
	}

	statsReq := httptest.NewRequest(http.MethodPost, "/v0/management/api-key-records/stats", bytes.NewReader([]byte(`{}`)))
	statsReq.RemoteAddr = "127.0.0.1:12345"
	statsReq.Header.Set("Authorization", "Bearer "+loginResp.Token)
	statsReq.Header.Set("Content-Type", "application/json")
	statsRec := httptest.NewRecorder()
	router.ServeHTTP(statsRec, statsReq)
	if statsRec.Code != http.StatusForbidden {
		t.Fatalf("stats status = %d, want 403, body=%s", statsRec.Code, statsRec.Body.String())
	}

	eventsReq := httptest.NewRequest(http.MethodGet, "/v0/management/api-key-records/item/events", nil)
	eventsReq.RemoteAddr = "127.0.0.1:12345"
	eventsReq.Header.Set("Authorization", "Bearer "+loginResp.Token)
	eventsRec := httptest.NewRecorder()
	router.ServeHTTP(eventsRec, eventsReq)
	if eventsRec.Code != http.StatusForbidden {
		t.Fatalf("events status = %d, want 403, body=%s", eventsRec.Code, eventsRec.Body.String())
	}

	sessionTrajectoryReq := httptest.NewRequest(http.MethodPut, "/v0/management/session-trajectory-enabled", bytes.NewReader([]byte(`{"value":false}`)))
	sessionTrajectoryReq.RemoteAddr = "127.0.0.1:12345"
	sessionTrajectoryReq.Header.Set("Authorization", "Bearer "+loginResp.Token)
	sessionTrajectoryReq.Header.Set("Content-Type", "application/json")
	sessionTrajectoryRec := httptest.NewRecorder()
	router.ServeHTTP(sessionTrajectoryRec, sessionTrajectoryReq)
	if sessionTrajectoryRec.Code != http.StatusForbidden {
		t.Fatalf("session-trajectory-enabled status = %d, want 403, body=%s", sessionTrajectoryRec.Code, sessionTrajectoryRec.Body.String())
	}
}

func TestHasManagementUserStoreRequiresSessionManager(t *testing.T) {
	t.Parallel()

	handler := NewHandlerWithoutConfigFilePath(&config.Config{}, nil)
	handler.SetManagementUserStore(&memoryManagementUserStore{})
	if handler.HasManagementUserStore() {
		t.Fatal("expected username/password login to stay unavailable without session manager")
	}

	handler.SetSessionManager(managementauth.NewSessionManager(30 * time.Minute))
	if !handler.HasManagementUserStore() {
		t.Fatal("expected username/password login to become available once session manager is configured")
	}
}

func TestManagementSessionExpiresAfterPasswordChange(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	passwordHash, err := bcrypt.GenerateFromPassword([]byte("staff-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword: %v", err)
	}
	updatedPasswordHash, err := bcrypt.GenerateFromPassword([]byte("staff-pass-2"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword updated: %v", err)
	}

	userStore := &memoryManagementUserStore{
		items: map[string]managementauth.User{
			"user_01": {
				Username:     "user_01",
				PasswordHash: string(passwordHash),
				Role:         managementauth.RoleStaff,
				Enabled:      true,
			},
		},
	}

	handler := NewHandlerWithoutConfigFilePath(&config.Config{}, nil)
	handler.SetManagementUserStore(userStore)
	handler.SetSessionManager(managementauth.NewSessionManager(30 * time.Minute))

	router := gin.New()
	public := router.Group("/v0/management")
	public.POST("/login", handler.Login)
	authenticated := router.Group("/v0/management")
	authenticated.Use(handler.Middleware())
	authenticated.GET("/me", handler.Me)

	body, _ := json.Marshal(gin.H{
		"username": "user_01",
		"password": "staff-pass",
	})
	req := httptest.NewRequest(http.MethodPost, "/v0/management/login", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body=%s", rec.Code, rec.Body.String())
	}

	var loginResp managementLoginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("Unmarshal login response: %v", err)
	}
	if loginResp.Token == "" {
		t.Fatalf("expected token in login response: %+v", loginResp)
	}

	userStore.mu.Lock()
	user := userStore.items["user_01"]
	user.PasswordHash = string(updatedPasswordHash)
	userStore.items["user_01"] = user
	userStore.mu.Unlock()

	meReq := httptest.NewRequest(http.MethodGet, "/v0/management/me", nil)
	meReq.RemoteAddr = "127.0.0.1:12345"
	meReq.Header.Set("Authorization", "Bearer "+loginResp.Token)
	meRec := httptest.NewRecorder()
	router.ServeHTTP(meRec, meReq)
	if meRec.Code != http.StatusUnauthorized {
		t.Fatalf("me status = %d, want 401, body=%s", meRec.Code, meRec.Body.String())
	}

	retryReq := httptest.NewRequest(http.MethodGet, "/v0/management/me", nil)
	retryReq.RemoteAddr = "127.0.0.1:12345"
	retryReq.Header.Set("Authorization", "Bearer "+loginResp.Token)
	retryRec := httptest.NewRecorder()
	router.ServeHTTP(retryRec, retryReq)
	if retryRec.Code != http.StatusUnauthorized {
		t.Fatalf("retry me status = %d, want 401, body=%s", retryRec.Code, retryRec.Body.String())
	}
}
