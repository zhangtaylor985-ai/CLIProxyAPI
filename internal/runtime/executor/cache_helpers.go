package executor

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

type codexCache struct {
	ID     string
	Expire time.Time
}

// codexCacheMap stores prompt cache IDs keyed by auth isolation scope plus prompt identity.
// Protected by codexCacheMu. Entries expire after 1 hour.
var (
	codexCacheMap = make(map[string]codexCache)
	codexCacheMu  sync.RWMutex
)

// codexCacheCleanupInterval controls how often expired entries are purged.
const codexCacheCleanupInterval = 15 * time.Minute

// codexCacheCleanupOnce ensures the background cleanup goroutine starts only once.
var codexCacheCleanupOnce sync.Once

// startCodexCacheCleanup launches a background goroutine that periodically
// removes expired entries from codexCacheMap to prevent memory leaks.
func startCodexCacheCleanup() {
	go func() {
		ticker := time.NewTicker(codexCacheCleanupInterval)
		defer ticker.Stop()
		for range ticker.C {
			purgeExpiredCodexCache()
		}
	}()
}

// purgeExpiredCodexCache removes entries that have expired.
func purgeExpiredCodexCache() {
	now := time.Now()
	codexCacheMu.Lock()
	defer codexCacheMu.Unlock()
	for key, cache := range codexCacheMap {
		if cache.Expire.Before(now) {
			delete(codexCacheMap, key)
		}
	}
}

// getCodexCache retrieves a cached entry, returning ok=false if not found or expired.
func getCodexCache(key string) (codexCache, bool) {
	codexCacheCleanupOnce.Do(startCodexCacheCleanup)
	codexCacheMu.RLock()
	cache, ok := codexCacheMap[key]
	codexCacheMu.RUnlock()
	if !ok || cache.Expire.Before(time.Now()) {
		return codexCache{}, false
	}
	return cache, true
}

// setCodexCache stores a cache entry.
func setCodexCache(key string, cache codexCache) {
	codexCacheCleanupOnce.Do(startCodexCacheCleanup)
	codexCacheMu.Lock()
	codexCacheMap[key] = cache
	codexCacheMu.Unlock()
}

func codexAuthIsolationKey(auth *cliproxyauth.Auth) string {
	if auth == nil {
		return ""
	}

	parts := make([]string, 0, 12)
	add := func(name, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		parts = append(parts, name+"="+value)
	}
	addSecretHash := func(name, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		sum := sha256.Sum256([]byte(value))
		parts = append(parts, name+"_sha256="+hex.EncodeToString(sum[:]))
	}

	add("id", auth.ID)
	add("provider", auth.Provider)
	add("label", auth.Label)
	add("proxy_url", auth.ProxyURL)
	if auth.Attributes != nil {
		add("base_url", auth.Attributes["base_url"])
		add("source", auth.Attributes["source"])
		add("compat_name", auth.Attributes["compat_name"])
		add("provider_key", auth.Attributes["provider_key"])
		addSecretHash("api_key", auth.Attributes["api_key"])
	}
	if auth.Metadata != nil {
		if accountID, ok := auth.Metadata["account_id"].(string); ok {
			add("account_id", accountID)
		}
		if email, ok := auth.Metadata["email"].(string); ok {
			add("email", email)
		}
		if authType, ok := auth.Metadata["type"].(string); ok {
			add("type", authType)
		}
	}

	if len(parts) == 0 {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func codexScopedCacheKey(auth *cliproxyauth.Auth, parts ...string) string {
	keyParts := make([]string, 0, len(parts)+2)
	if isolationKey := codexAuthIsolationKey(auth); isolationKey != "" {
		keyParts = append(keyParts, "auth", isolationKey)
	}
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		keyParts = append(keyParts, trimmed)
	}
	return strings.Join(keyParts, "\x00")
}
