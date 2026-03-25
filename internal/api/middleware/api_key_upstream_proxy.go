package middleware

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	internalutil "github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/proxy"
)

// APIKeyUpstreamProxyMiddleware transparently proxies /v1/* requests to a configured upstream base URL.
// The upstream base URL is configured per-client API key via api-key-policies[].upstream-base-url.
//
// This is intended for chaining CLIProxyAPI instances: one instance can route a subset of client keys
// to another instance by treating that instance as the upstream "provider" address.
func APIKeyUpstreamProxyMiddleware(getConfig func() *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c == nil || c.Request == nil || c.Request.URL == nil {
			return
		}

		// Only apply to /v1 paths. (Other API groups can add their own middleware if needed.)
		if !strings.HasPrefix(c.Request.URL.Path, "/v1") {
			c.Next()
			return
		}

		apiKey := strings.TrimSpace(c.GetString("apiKey"))
		if apiKey == "" {
			c.Next()
			return
		}

		cfg := (*config.Config)(nil)
		if getConfig != nil {
			cfg = getConfig()
		}

		var policyEntry *config.APIKeyPolicy
		if v, exists := c.Get(apiKeyPolicyContextKey); exists {
			if p, ok := v.(*config.APIKeyPolicy); ok && p != nil {
				policyEntry = p
			}
		}
		if policyEntry == nil && cfg != nil {
			if p := cfg.FindAPIKeyPolicy(apiKey); p != nil {
				copyPolicy := *p
				policyEntry = &copyPolicy
			}
		}

		if policyEntry == nil {
			c.Next()
			return
		}

		upstreamBase := strings.TrimSpace(policyEntry.UpstreamBaseURL)
		if upstreamBase == "" {
			c.Next()
			return
		}

		proxyHandler, err := newAPIKeyUpstreamReverseProxy(upstreamBase, cfg)
		if err != nil {
			log.WithError(err).Warnf("api key upstream proxy: invalid upstream-base-url for key %s", hideAPIKeySafe(apiKey))
			body := handlers.BuildErrorResponseBody(http.StatusBadGateway, "invalid upstream-base-url")
			c.Abort()
			c.Data(http.StatusBadGateway, "application/json", body)
			return
		}

		c.Abort()
		proxyHandler.ServeHTTP(proxyResponseWriter{ResponseWriter: c.Writer}, c.Request)
	}
}

// proxyResponseWriter hides http.CloseNotifier to prevent panics in gin's CloseNotify delegation
// when the underlying writer doesn't implement it (e.g. in tests or wrapped writers).
// It still forwards Flusher/Hijacker/Pusher when supported.
type proxyResponseWriter struct {
	http.ResponseWriter
}

func (w proxyResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w proxyResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijack not supported")
	}
	return hijacker.Hijack()
}

func (w proxyResponseWriter) Push(target string, opts *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}

func newAPIKeyUpstreamReverseProxy(upstreamBaseURL string, cfg *config.Config) (*httputil.ReverseProxy, error) {
	target, err := url.Parse(strings.TrimSpace(upstreamBaseURL))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(target.Scheme) == "" || strings.TrimSpace(target.Host) == "" {
		return nil, fmt.Errorf("missing scheme/host")
	}

	basePath := strings.TrimSuffix(target.Path, "/")
	baseQuery := target.RawQuery

	transport := http.RoundTripper(http.DefaultTransport)
	if cfg != nil {
		if proxyStr := strings.TrimSpace(cfg.ProxyURL); proxyStr != "" {
			if tr := buildProxyTransport(proxyStr); tr != nil {
				transport = tr
			}
		}
	}

	director := func(req *http.Request) {
		sanitizeOpenAICompatJSONBody(req)

		// Preserve original path/query until we've computed the outgoing URL.
		originalPath := req.URL.Path
		originalQuery := req.URL.RawQuery

		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.Host = target.Host

		pathToAppend := originalPath
		// If the upstream base URL already ends with "/v1" (including "/api/v1"), avoid duplicating "/v1".
		if strings.HasSuffix(basePath, "/v1") && strings.HasPrefix(originalPath, "/v1") {
			pathToAppend = strings.TrimPrefix(originalPath, "/v1")
			if pathToAppend == "" {
				pathToAppend = "/"
			}
		}
		req.URL.Path = joinURLPath(basePath, pathToAppend)
		req.URL.RawPath = ""

		switch {
		case baseQuery == "":
			req.URL.RawQuery = originalQuery
		case originalQuery == "":
			req.URL.RawQuery = baseQuery
		default:
			req.URL.RawQuery = baseQuery + "&" + originalQuery
		}
	}

	errorHandler := func(rw http.ResponseWriter, _ *http.Request, err error) {
		log.WithError(err).Warnf("api key upstream proxy: upstream request failed (%s)", upstreamBaseURL)
		body := handlers.BuildErrorResponseBody(http.StatusBadGateway, "upstream proxy error")
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusBadGateway)
		_, _ = rw.Write(body)
	}

	return &httputil.ReverseProxy{
		Director:     director,
		Transport:    transport,
		ErrorHandler: errorHandler,
	}, nil
}

func sanitizeOpenAICompatJSONBody(req *http.Request) {
	if req == nil || req.Body == nil {
		return
	}
	switch req.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
	default:
		return
	}

	contentType := strings.ToLower(strings.TrimSpace(req.Header.Get("Content-Type")))
	if contentType != "" && !strings.Contains(contentType, "application/json") {
		return
	}

	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil || len(body) == 0 {
		// Best-effort: restore original (empty) body to keep proxy behavior consistent.
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
		req.Header.Set("Content-Length", strconv.Itoa(len(body)))
		return
	}

	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
		req.Header.Set("Content-Length", strconv.Itoa(len(body)))
		return
	}

	sanitized := internalutil.NormalizeOpenAIToolsPayload(body)
	if len(sanitized) == 0 {
		// Avoid sending an empty body if normalization fails unexpectedly.
		sanitized = body
	}

	req.Body = io.NopCloser(bytes.NewReader(sanitized))
	req.ContentLength = int64(len(sanitized))
	req.Header.Set("Content-Length", strconv.Itoa(len(sanitized)))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(sanitized)), nil
	}
}

func joinURLPath(basePath, suffixPath string) string {
	base := basePath
	suffix := suffixPath

	if base == "" {
		if suffix == "" {
			return "/"
		}
		if strings.HasPrefix(suffix, "/") {
			return suffix
		}
		return "/" + suffix
	}

	if !strings.HasPrefix(base, "/") {
		base = "/" + base
	}
	if suffix == "" {
		return base
	}
	if !strings.HasPrefix(suffix, "/") {
		suffix = "/" + suffix
	}
	if strings.HasSuffix(base, "/") {
		base = strings.TrimSuffix(base, "/")
	}
	return base + suffix
}

func buildProxyTransport(proxyStr string) *http.Transport {
	proxyURL, errParse := url.Parse(strings.TrimSpace(proxyStr))
	if errParse != nil {
		return nil
	}

	switch proxyURL.Scheme {
	case "socks5":
		var proxyAuth *proxy.Auth
		if proxyURL.User != nil {
			username := proxyURL.User.Username()
			password, _ := proxyURL.User.Password()
			proxyAuth = &proxy.Auth{User: username, Password: password}
		}
		dialer, errSOCKS5 := proxy.SOCKS5("tcp", proxyURL.Host, proxyAuth, proxy.Direct)
		if errSOCKS5 != nil {
			return nil
		}
		return &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			},
		}
	case "http", "https":
		return &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	default:
		return nil
	}
}

func hideAPIKeySafe(apiKey string) string {
	key := strings.TrimSpace(apiKey)
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}
