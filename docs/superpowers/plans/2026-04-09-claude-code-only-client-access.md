# Claude Code Only Client Access Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a global Claude Code-only access switch plus per-API-key override so normal API clients can be blocked while Claude Code remains allowed.

**Architecture:** Reuse the existing management config pipeline for the global switch and the existing `APIKeyPolicy` row-based policy store for the per-key override. Enforce the effective restriction in `APIKeyPolicyMiddleware` using a small, isolated Claude Code client detector shared by `/v1/messages` and `/v1/models`-style request handling.

**Tech Stack:** Go, Gin, existing config/apikeyconfig stores, React, TypeScript, existing management UI state/services.

---

### Task 1: Add failing backend tests for effective policy and request enforcement

**Files:**
- Modify: `internal/api/middleware/api_key_policy_test.go`
- Modify: `internal/config/api_key_policies_test.go`

- [ ] **Step 1: Write the failing effective-policy test**

```go
func TestConfig_EffectiveAPIKeyPolicy_UsesGlobalClaudeCodeOnlyByDefault(t *testing.T) {
	cfg := &Config{
		SDKConfig: SDKConfig{ClaudeCodeOnlyEnabled: true},
	}

	policy := cfg.EffectiveAPIKeyPolicy("k1")
	if policy == nil || !policy.ClaudeCodeOnlyEnabled() {
		t.Fatalf("expected global Claude Code only to apply by default")
	}
}
```

- [ ] **Step 2: Write the failing override tests**

```go
func TestConfig_EffectiveAPIKeyPolicy_PerKeyClaudeCodeOnlyOverride(t *testing.T) {
	enabled := true
	disabled := false

	cfg := &Config{
		SDKConfig: SDKConfig{ClaudeCodeOnlyEnabled: false},
		APIKeyPolicies: []APIKeyPolicy{
			{APIKey: "k-enabled", ClaudeCodeOnly: &enabled},
			{APIKey: "k-disabled", ClaudeCodeOnly: &disabled},
		},
	}

	if !cfg.EffectiveAPIKeyPolicy("k-enabled").ClaudeCodeOnlyEnabled() {
		t.Fatalf("expected per-key enabled override")
	}
	if cfg.EffectiveAPIKeyPolicy("k-disabled").ClaudeCodeOnlyEnabled() {
		t.Fatalf("expected per-key disabled override")
	}
}
```

- [ ] **Step 3: Write the failing middleware tests**

```go
func TestAPIKeyPolicyMiddleware_ClaudeCodeOnlyRejectsGenericClient(t *testing.T) {
	enabled := true
	cfg := &config.Config{
		SDKConfig: config.SDKConfig{ClaudeCodeOnlyEnabled: false},
		APIKeyPolicies: []config.APIKeyPolicy{{APIKey: "k", ClaudeCodeOnly: &enabled}},
	}

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("apiKey", "k")
		c.Next()
	})
	r.Use(APIKeyPolicyMiddleware(func() *config.Config { return cfg }, nil, nil, nil))
	r.POST("/v1/messages", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-sonnet-4-5"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "curl/8.7.1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}
```

- [ ] **Step 4: Run tests to verify they fail**

Run: `go test ./internal/config ./internal/api/middleware -run 'ClaudeCodeOnly' -v`

Expected: FAIL because the config field, effective-policy logic, and middleware enforcement do not exist yet.

### Task 2: Implement backend config, effective-policy, detection helper, and middleware

**Files:**
- Modify: `internal/config/sdk_config.go`
- Modify: `internal/config/api_key_policies.go`
- Modify: `internal/api/middleware/api_key_policy.go`
- Create: `internal/clientidentity/claude_code.go`
- Create: `internal/clientidentity/claude_code_test.go`

- [ ] **Step 1: Add the global config field and per-policy override**

```go
type SDKConfig struct {
	ClaudeCodeOnlyEnabled bool `yaml:"claude-code-only-enabled" json:"claude-code-only-enabled"`
}

type APIKeyPolicy struct {
	ClaudeCodeOnly *bool `yaml:"claude-code-only,omitempty" json:"claude-code-only,omitempty"`
}
```

- [ ] **Step 2: Add effective-policy helpers**

```go
func (p *APIKeyPolicy) ClaudeCodeOnlyEnabled() bool {
	return p != nil && p.ClaudeCodeOnly != nil && *p.ClaudeCodeOnly
}

func (cfg *Config) effectiveClaudeCodeOnlyFor(entry *APIKeyPolicy) bool {
	if entry != nil && entry.ClaudeCodeOnly != nil {
		return *entry.ClaudeCodeOnly
	}
	return cfg != nil && cfg.ClaudeCodeOnlyEnabled
}
```

- [ ] **Step 3: Add an isolated Claude Code detector**

```go
func IsClaudeCodeRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	userAgent := strings.ToLower(strings.TrimSpace(r.Header.Get("User-Agent")))
	if !strings.HasPrefix(userAgent, "claude-cli/") {
		return false
	}

	switch r.URL.Path {
	case "/v1/messages", "/v1/models":
		return true
	default:
		return false
	}
}
```

- [ ] **Step 4: Enforce the effective restriction in middleware**

```go
if policyEntry != nil && policyEntry.ClaudeCodeOnlyEnabled() && !clientidentity.IsClaudeCodeRequest(c.Request) {
	body := handlers.BuildErrorResponseBody(http.StatusForbidden, "api key is restricted to Claude Code clients")
	c.Abort()
	c.Data(http.StatusForbidden, "application/json", body)
	return
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/clientidentity ./internal/config ./internal/api/middleware -run 'ClaudeCodeOnly' -v`

Expected: PASS

### Task 3: Expose the global switch and per-key override through management APIs

**Files:**
- Modify: `internal/api/handlers/management/config_basic.go`
- Modify: `internal/api/server.go`
- Modify: `internal/api/handlers/management/api_key_records.go`
- Modify: `internal/api/handlers/management/api_key_records_test.go`

- [ ] **Step 1: Add failing management handler tests**

```go
func TestGetClaudeCodeOnlyEnabled(t *testing.T) {}
func TestPolicyViewRoundTrip_ClaudeCodeOnlyOverride(t *testing.T) {}
```

- [ ] **Step 2: Add management endpoints for the global switch**

```go
func (h *Handler) GetClaudeCodeOnlyEnabled(c *gin.Context) {
	c.JSON(200, gin.H{"claude-code-only-enabled": h.cfg.ClaudeCodeOnlyEnabled})
}

func (h *Handler) PutClaudeCodeOnlyEnabled(c *gin.Context) {
	h.updateBoolField(c, func(v bool) { h.cfg.ClaudeCodeOnlyEnabled = v })
}
```

- [ ] **Step 3: Add the field to API key policy view mapping**

```go
type apiKeyPolicyView struct {
	ClaudeCodeOnlyMode string `json:"claude_code_only_mode"`
}
```

```go
switch {
case p != nil && p.ClaudeCodeOnly != nil && *p.ClaudeCodeOnly:
	view.ClaudeCodeOnlyMode = "enabled"
case p != nil && p.ClaudeCodeOnly != nil && !*p.ClaudeCodeOnly:
	view.ClaudeCodeOnlyMode = "disabled"
default:
	view.ClaudeCodeOnlyMode = "inherit"
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/api/handlers/management -run 'ClaudeCodeOnly' -v`

Expected: PASS

### Task 4: Add management UI controls for the global switch and API key override

**Files:**
- Modify: `/Users/taylor/code/tools/Cli-Proxy-API-Management-Center-ori/src/services/api/config.ts`
- Modify: `/Users/taylor/code/tools/Cli-Proxy-API-Management-Center-ori/src/services/api/apiKeyRecords.ts`
- Modify: `/Users/taylor/code/tools/Cli-Proxy-API-Management-Center-ori/src/pages/SystemPage.tsx`
- Modify: `/Users/taylor/code/tools/Cli-Proxy-API-Management-Center-ori/src/pages/APIKeysWorkbenchPage.tsx`
- Modify: `/Users/taylor/code/tools/Cli-Proxy-API-Management-Center-ori/src/i18n/locales/zh-CN.json`
- Modify: `/Users/taylor/code/tools/Cli-Proxy-API-Management-Center-ori/src/i18n/locales/en.json`

- [ ] **Step 1: Add the failing TypeScript shape changes**

```ts
export interface ApiKeyPolicyView {
  claude_code_only_mode: 'inherit' | 'enabled' | 'disabled';
}
```

- [ ] **Step 2: Add the global config API helper**

```ts
updateClaudeCodeOnlyEnabled: (enabled: boolean) =>
  apiClient.put('/claude-code-only-enabled', { value: enabled }),
```

- [ ] **Step 3: Add the System page card**

```tsx
<ToggleSwitch
  label="启用全局 Claude Code 客户端限制"
  checked={claudeCodeOnlyEnabled}
  onChange={(value) => {
    void handleClaudeCodeOnlyToggle(value);
  }}
/>
```

- [ ] **Step 4: Add the API Key workbench three-state selector**

```tsx
<Select
  value={draft.claudeCodeOnlyMode}
  onChange={(value) => updateDraft('claudeCodeOnlyMode', value as WorkbenchDraft['claudeCodeOnlyMode'])}
  options={[
    { value: 'inherit', label: '跟随全局' },
    { value: 'enabled', label: '仅允许 Claude Code' },
    { value: 'disabled', label: '不限制客户端' },
  ]}
/>
```

- [ ] **Step 5: Build the frontend to verify it passes**

Run: `cd /Users/taylor/code/tools/Cli-Proxy-API-Management-Center-ori && npm run build`

Expected: PASS

### Task 5: Run final verification

**Files:**
- No additional file changes required

- [ ] **Step 1: Run targeted backend tests**

Run: `go test ./internal/clientidentity ./internal/config ./internal/api/middleware ./internal/api/handlers/management -v`

Expected: PASS

- [ ] **Step 2: Run frontend build**

Run: `cd /Users/taylor/code/tools/Cli-Proxy-API-Management-Center-ori && npm run build`

Expected: PASS

- [ ] **Step 3: Sanity-check the change**

Run:

```bash
git diff --stat
```

Expected: only backend/client-detection/management UI files related to Claude Code-only access control are changed.
