package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestAPIKeyUpstreamProxyMiddleware_ForwardsGETModels_BaseWithoutV1(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("upstream path=%q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer abc" {
			t.Fatalf("upstream authorization=%q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		APIKeyPolicies: []config.APIKeyPolicy{
			{APIKey: "k", UpstreamBaseURL: upstream.URL},
		},
	}
	cfg.SanitizeAPIKeyPolicies()

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("apiKey", "k")
		c.Next()
	})
	r.Use(APIKeyPolicyMiddleware(func() *config.Config { return cfg }, nil, nil, nil))
	r.Use(APIKeyUpstreamProxyMiddleware(func() *config.Config { return cfg }))
	r.GET("/v1/models", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"local": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer abc")
	w := newCloseNotifyRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != `{"ok":true}` {
		t.Fatalf("body=%q", got)
	}
}

func TestAPIKeyUpstreamProxyMiddleware_ForwardsGETModels_BaseWithV1(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("upstream path=%q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		APIKeyPolicies: []config.APIKeyPolicy{
			{APIKey: "k", UpstreamBaseURL: upstream.URL + "/v1"},
		},
	}
	cfg.SanitizeAPIKeyPolicies()

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("apiKey", "k")
		c.Next()
	})
	r.Use(APIKeyPolicyMiddleware(func() *config.Config { return cfg }, nil, nil, nil))
	r.Use(APIKeyUpstreamProxyMiddleware(func() *config.Config { return cfg }))
	r.GET("/v1/models", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"local": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := newCloseNotifyRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != `{"ok":true}` {
		t.Fatalf("body=%q", got)
	}
}

func TestAPIKeyUpstreamProxyMiddleware_PassthroughWhenUnset(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		APIKeyPolicies: []config.APIKeyPolicy{
			{APIKey: "k"},
		},
	}
	cfg.SanitizeAPIKeyPolicies()

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("apiKey", "k")
		c.Next()
	})
	r.Use(APIKeyPolicyMiddleware(func() *config.Config { return cfg }, nil, nil, nil))
	r.Use(APIKeyUpstreamProxyMiddleware(func() *config.Config { return cfg }))
	r.GET("/v1/models", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"local": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := newCloseNotifyRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != `{"local":true}` {
		t.Fatalf("body=%q", got)
	}
}

type closeNotifyRecorder struct {
	*httptest.ResponseRecorder
	ch chan bool
}

func newCloseNotifyRecorder() *closeNotifyRecorder {
	return &closeNotifyRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		ch:               make(chan bool, 1),
	}
}

func (r *closeNotifyRecorder) CloseNotify() <-chan bool { return r.ch }
