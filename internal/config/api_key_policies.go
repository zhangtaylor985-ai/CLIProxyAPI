package config

import (
	"hash/fnv"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/policy"
)

var defaultClientHiddenGPTModelPatterns = []string{
	"gpt-*",
	"chatgpt-*",
	"o1*",
	"o3*",
	"o4*",
}

var defaultClientHiddenClaudeModelPatterns = []string{
	"claude-*",
}

// APIKeyPolicy defines restrictions and quotas applied to an authenticated client API key.
// The APIKey value must match the authenticated principal as provided by the access manager.
type APIKeyPolicy struct {
	APIKey string `yaml:"api-key" json:"api-key"`

	// Name is an optional display name for the client API key.
	Name string `yaml:"name,omitempty" json:"name,omitempty"`

	// Note is an optional operator note for the client API key.
	Note string `yaml:"note,omitempty" json:"note,omitempty"`

	// CreatedAt records when this client API key record was created in the external store.
	// It is not persisted to config.yaml.
	CreatedAt string `yaml:"-" json:"created_at,omitempty"`

	// ExpiresAt records when this client API key should stop being accepted.
	// Empty means no automatic expiry. It is not persisted to config.yaml.
	ExpiresAt string `yaml:"-" json:"expires_at,omitempty"`

	// Disabled immediately blocks this client API key when true.
	// It is not persisted to config.yaml.
	Disabled bool `yaml:"-" json:"disabled,omitempty"`

	// GroupID binds the API key to a managed account group stored in Postgres.
	// When set, daily/weekly base budgets are resolved from that group.
	GroupID string `yaml:"group-id,omitempty" json:"group-id,omitempty"`

	// FastMode forces OpenAI-compatible upstream requests for this client API key
	// to use the priority service tier when the target model supports it.
	FastMode bool `yaml:"fast-mode,omitempty" json:"fast-mode,omitempty"`

	// CodexChannelMode restricts which Codex credential channel this API key may use.
	// Supported values are "auto", "provider", and "auth_file".
	CodexChannelMode string `yaml:"codex-channel-mode,omitempty" json:"codex-channel-mode,omitempty"`

	// EnableClaudeModels disables the global Claude -> GPT routing override for this API key.
	// It only takes effect when claude-to-gpt-routing-enabled is true.
	EnableClaudeModels *bool `yaml:"enable-claude-models,omitempty" json:"enable-claude-models,omitempty"`

	// ClaudeUsageLimitUSD defines the cumulative Claude spend ceiling (USD) for this API key
	// when EnableClaudeModels is enabled. Once the persisted Claude usage reaches the limit,
	// the key falls back to the global Claude -> GPT default strategy when that global switch is on.
	// Values <= 0 are treated as disabled.
	ClaudeUsageLimitUSD float64 `yaml:"claude-usage-limit-usd,omitempty" json:"claude-usage-limit-usd,omitempty"`

	// ClaudeGPTTargetFamily optionally overrides the target base model used by the synthesized
	// Claude -> GPT routing/failover defaults for this API key. Supported values:
	// "gpt-5.2", "gpt-5.4", and "gpt-5.3-codex". When unset, the server defaults to gpt-5.4.
	ClaudeGPTTargetFamily string `yaml:"claude-gpt-target-family,omitempty" json:"claude-gpt-target-family,omitempty"`

	// EnableClaudeOpus1M allows this API key to keep Claude Opus 1M capability even when
	// the global disable-claude-opus-1m switch is enabled.
	EnableClaudeOpus1M *bool `yaml:"enable-claude-opus-1m,omitempty" json:"enable-claude-opus-1m,omitempty"`

	// ClaudeCodeOnly overrides the global Claude Code-only client restriction for this API key.
	// nil means inherit the global setting.
	ClaudeCodeOnly *bool `yaml:"claude-code-only,omitempty" json:"claude-code-only,omitempty"`

	// UpstreamBaseURL overrides the upstream API base URL for this client API key.
	// When set, /v1/* requests will be transparently proxied to this base URL instead of
	// routing to the configured providers in this server. This is useful for chaining
	// CLIProxyAPI instances (per-client upstream proxy routing).
	//
	// Examples:
	//   - "http://127.0.0.1:8001" (incoming /v1/models -> http://127.0.0.1:8001/v1/models)
	//   - "http://127.0.0.1:8001/v1" (incoming /v1/models -> http://127.0.0.1:8001/v1/models)
	UpstreamBaseURL string `yaml:"upstream-base-url,omitempty" json:"upstream-base-url,omitempty"`

	// ExcludedModels lists model IDs or wildcard patterns that this API key is NOT allowed to access.
	// Matching is case-insensitive. Supports '*' wildcard.
	ExcludedModels []string `yaml:"excluded-models,omitempty" json:"excluded-models,omitempty"`

	// ModelRouting optionally rewrites an incoming request model to a configured target model.
	// This supports deterministic time-window split routing for coherence (e.g. 50% of 1h windows
	// routed to Codex while still presenting the original requested model to the client).
	ModelRouting APIKeyModelRoutingPolicy `yaml:"model-routing,omitempty" json:"model-routing,omitempty"`

	// Failover controls automatic provider failover behaviour for this API key.
	// It allows transparent retry against a configured target model when a provider becomes unavailable
	// (e.g., Claude weekly cap, rolling-window caps, or account issues).
	Failover APIKeyFailoverPolicy `yaml:"failover,omitempty" json:"failover,omitempty"`

	// AllowClaudeOpus46 controls whether requests may use claude-opus-4-6.
	// When false, the server will transparently downgrade claude-opus-4-6* to claude-opus-4-5-20251101*.
	// Defaults to true when unset.
	AllowClaudeOpus46 *bool `yaml:"allow-claude-opus-4-6,omitempty" json:"allow-claude-opus-4-6,omitempty"`

	// DailyLimits defines per-model daily request limits for this API key.
	// Key is a model ID (case-insensitive). Values <= 0 are treated as disabled and dropped.
	DailyLimits map[string]int `yaml:"daily-limits,omitempty" json:"daily-limits,omitempty"`

	// DailyBudgetUSD defines the maximum daily spend (USD) allowed for this API key.
	// Values <= 0 are treated as disabled.
	DailyBudgetUSD float64 `yaml:"daily-budget-usd,omitempty" json:"daily-budget-usd,omitempty"`

	// WeeklyBudgetUSD defines the maximum weekly spend (USD) allowed for this API key.
	// Weeks are tracked in China Standard Time (UTC+8) starting Monday 00:00.
	// Values <= 0 are treated as disabled.
	WeeklyBudgetUSD float64 `yaml:"weekly-budget-usd,omitempty" json:"weekly-budget-usd,omitempty"`

	// WeeklyBudgetAnchorAt optionally switches weekly budgeting to anchored 168-hour windows.
	// The value is stored as RFC3339 and normalized to hour precision during sanitization.
	// When empty, the legacy Monday 00:00 (UTC+8) calendar-week window is used.
	WeeklyBudgetAnchorAt string `yaml:"weekly-budget-anchor-at,omitempty" json:"weekly-budget-anchor-at,omitempty"`

	// TokenPackageUSD defines a one-time prepaid spend allowance (USD) for this API key.
	// While the package still has balance, daily/weekly spend budgets are bypassed.
	// Values <= 0 are treated as disabled.
	TokenPackageUSD float64 `yaml:"token-package-usd,omitempty" json:"token-package-usd,omitempty"`

	// TokenPackageStartedAt defines when the prepaid package becomes active.
	// The value is stored as RFC3339 during sanitization.
	TokenPackageStartedAt string `yaml:"token-package-started-at,omitempty" json:"token-package-started-at,omitempty"`
}

// APIKeyModelRoutingPolicy groups model routing configuration for a client API key.
type APIKeyModelRoutingPolicy struct {
	Rules []ModelRoutingRule `yaml:"rules,omitempty" json:"rules,omitempty"`
}

// ModelRoutingRule deterministically routes a subset of time windows to a target model.
// Matching is case-insensitive and supports '*' wildcard on from-model.
type ModelRoutingRule struct {
	// Enabled toggles routing for this rule. Defaults to true when unset.
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`

	FromModel string `yaml:"from-model,omitempty" json:"from-model,omitempty"`

	// TargetModel is the model ID to execute when the rule routes to target.
	TargetModel string `yaml:"target-model,omitempty" json:"target-model,omitempty"`

	// TargetPercent is the percentage (0-100) of time windows that should route to TargetModel.
	TargetPercent int `yaml:"target-percent,omitempty" json:"target-percent,omitempty"`

	// StickyWindowSeconds is the routing time window size in seconds. Defaults to 3600 when <= 0.
	StickyWindowSeconds int `yaml:"sticky-window-seconds,omitempty" json:"sticky-window-seconds,omitempty"`
}

type ModelRoutingDecision struct {
	FromModel           string
	TargetModel         string
	TargetPercent       int
	StickyWindowSeconds int
	Bucket              int64
}

// ProviderFailoverPolicy defines per-provider automatic failover settings.
type ProviderFailoverPolicy struct {
	// Enabled toggles failover behaviour for the provider.
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`

	// TargetModel is the model ID to retry when failover triggers (e.g. "gpt-5.4(medium)").
	TargetModel string `yaml:"target-model,omitempty" json:"target-model,omitempty"`

	// Rules optionally override the target model based on the requested model.
	// Matching is case-insensitive and supports '*' wildcard.
	Rules []ModelFailoverRule `yaml:"rules,omitempty" json:"rules,omitempty"`
}

// ModelFailoverRule defines a model-specific failover target.
type ModelFailoverRule struct {
	FromModel   string `yaml:"from-model,omitempty" json:"from-model,omitempty"`
	TargetModel string `yaml:"target-model,omitempty" json:"target-model,omitempty"`
}

// APIKeyFailoverPolicy groups failover configuration for a client API key.
// Provider keys match internal provider identifiers (e.g. "claude").
type APIKeyFailoverPolicy struct {
	// Claude controls failover behaviour when the request is routed to the Claude provider.
	Claude ProviderFailoverPolicy `yaml:"claude,omitempty" json:"claude,omitempty"`
}

type APIKeyPolicyEffectiveOptions struct {
	ForceGlobalClaudeRouting bool
}

func defaultGlobalClaudeRoutingRules(reasoningEffort string) []ModelRoutingRule {
	enabled := true
	opusTarget, _ := policy.DefaultGlobalClaudeGPTTarget("claude-opus-default", reasoningEffort)
	defaultTarget, _ := policy.DefaultGlobalClaudeGPTTarget("claude-default", reasoningEffort)
	return []ModelRoutingRule{
		{
			Enabled:             &enabled,
			FromModel:           "claude-opus-*",
			TargetModel:         opusTarget,
			TargetPercent:       100,
			StickyWindowSeconds: 3600,
		},
		{
			Enabled:             &enabled,
			FromModel:           "claude-*",
			TargetModel:         defaultTarget,
			TargetPercent:       100,
			StickyWindowSeconds: 3600,
		},
	}
}

func defaultGlobalClaudeRoutingRulesForFamily(family string) []ModelRoutingRule {
	enabled := true
	opusTarget, _ := policy.DefaultClaudeGPTTargetForFamily("claude-opus-default", family)
	defaultTarget, _ := policy.DefaultClaudeGPTTargetForFamily("claude-default", family)
	return []ModelRoutingRule{
		{
			Enabled:             &enabled,
			FromModel:           "claude-opus-*",
			TargetModel:         opusTarget,
			TargetPercent:       100,
			StickyWindowSeconds: 3600,
		},
		{
			Enabled:             &enabled,
			FromModel:           "claude-*",
			TargetModel:         defaultTarget,
			TargetPercent:       100,
			StickyWindowSeconds: 3600,
		},
	}
}

func defaultGlobalClaudeFailoverRules(reasoningEffort string) []ModelFailoverRule {
	opusTarget, _ := policy.DefaultGlobalClaudeGPTTarget("claude-opus-default", reasoningEffort)
	defaultTarget, _ := policy.DefaultGlobalClaudeGPTTarget("claude-default", reasoningEffort)
	return []ModelFailoverRule{
		{FromModel: "claude-opus-*", TargetModel: opusTarget},
		{FromModel: "claude-*", TargetModel: defaultTarget},
	}
}

func defaultGlobalClaudeFailoverRulesForFamily(family string) []ModelFailoverRule {
	opusTarget, _ := policy.DefaultClaudeGPTTargetForFamily("claude-opus-default", family)
	defaultTarget, _ := policy.DefaultClaudeGPTTargetForFamily("claude-default", family)
	return []ModelFailoverRule{
		{FromModel: "claude-opus-*", TargetModel: opusTarget},
		{FromModel: "claude-*", TargetModel: defaultTarget},
	}
}

func synthesizedClaudeRoutingRules(targetFamily, reasoningEffort string) []ModelRoutingRule {
	if strings.TrimSpace(targetFamily) != "" {
		return defaultGlobalClaudeRoutingRulesForFamily(targetFamily)
	}
	return defaultGlobalClaudeRoutingRules(reasoningEffort)
}

func synthesizedClaudeFailoverRules(targetFamily, reasoningEffort string) []ModelFailoverRule {
	if strings.TrimSpace(targetFamily) != "" {
		return defaultGlobalClaudeFailoverRulesForFamily(targetFamily)
	}
	return defaultGlobalClaudeFailoverRules(reasoningEffort)
}

func hasExplicitClaudeFailoverConfig(p ProviderFailoverPolicy) bool {
	return p.Enabled || strings.TrimSpace(p.TargetModel) != "" || len(p.Rules) > 0
}

// RoutedModelFor resolves the effective model that should be executed for a request.
// It returns the target model and a decision object when routing to target is selected.
// When no rule matches or the decision keeps the original model, it returns ("", nil).
func (p *APIKeyPolicy) RoutedModelFor(apiKey, requestedModel string, now time.Time) (string, *ModelRoutingDecision) {
	if p == nil {
		return "", nil
	}
	requestKey := policy.NormaliseModelKey(requestedModel)
	if requestKey == "" || len(p.ModelRouting.Rules) == 0 {
		return "", nil
	}
	key := strings.TrimSpace(apiKey)
	if key == "" {
		key = strings.TrimSpace(p.APIKey)
	}
	for _, rule := range p.ModelRouting.Rules {
		if rule.Enabled != nil && !*rule.Enabled {
			continue
		}
		from := strings.ToLower(strings.TrimSpace(rule.FromModel))
		if from == "" {
			continue
		}
		if !policy.MatchWildcard(from, requestKey) {
			continue
		}
		target := strings.TrimSpace(rule.TargetModel)
		if target == "" {
			return "", nil
		}

		percent := rule.TargetPercent
		if percent < 0 {
			percent = 0
		}
		if percent > 100 {
			percent = 100
		}
		if percent == 0 {
			return "", nil
		}
		if percent == 100 {
			return target, &ModelRoutingDecision{
				FromModel:           from,
				TargetModel:         target,
				TargetPercent:       percent,
				StickyWindowSeconds: normalizeStickyWindowSeconds(rule.StickyWindowSeconds),
				Bucket:              routingBucket(now, normalizeStickyWindowSeconds(rule.StickyWindowSeconds)),
			}
		}

		window := normalizeStickyWindowSeconds(rule.StickyWindowSeconds)
		bucket := routingBucket(now, window)
		phase := routingPhase(key, requestKey)
		n := bucket + phase
		value := (n * int64(percent)) % 100
		if value < int64(percent) {
			return target, &ModelRoutingDecision{
				FromModel:           from,
				TargetModel:         target,
				TargetPercent:       percent,
				StickyWindowSeconds: window,
				Bucket:              bucket,
			}
		}
		return "", nil
	}
	return "", nil
}

func normalizeStickyWindowSeconds(seconds int) int {
	if seconds <= 0 {
		return 3600
	}
	return seconds
}

func routingBucket(now time.Time, windowSeconds int) int64 {
	if windowSeconds <= 0 {
		windowSeconds = 3600
	}
	sec := now.Unix()
	if sec < 0 {
		sec = 0
	}
	return sec / int64(windowSeconds)
}

func routingPhase(apiKey, requestedModelKey string) int64 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(apiKey))))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(requestedModelKey))))
	return int64(h.Sum32() % 100)
}

func (p *APIKeyPolicy) AllowsClaudeOpus46() bool {
	if p == nil || p.AllowClaudeOpus46 == nil {
		return true
	}
	return *p.AllowClaudeOpus46
}

func (p *APIKeyPolicy) ClaudeModelsEnabled() bool {
	if p == nil || p.EnableClaudeModels == nil {
		return false
	}
	return *p.EnableClaudeModels
}

func (p *APIKeyPolicy) ClaudeUsageLimitEnabled() bool {
	if p == nil {
		return false
	}
	return p.ClaudeUsageLimitUSD > 0
}

func (p *APIKeyPolicy) ClaudeGPTTargetFamilyOrDefault() string {
	if p == nil {
		return policy.EffectiveClaudeGPTTargetBase("")
	}
	return policy.EffectiveClaudeGPTTargetBase(p.ClaudeGPTTargetFamily)
}

func (p *APIKeyPolicy) ClaudeOpus1MEnabled() bool {
	if p == nil || p.EnableClaudeOpus1M == nil {
		return false
	}
	return *p.EnableClaudeOpus1M
}

func (p *APIKeyPolicy) ClaudeCodeOnlyEnabled() bool {
	if p == nil || p.ClaudeCodeOnly == nil {
		return false
	}
	return *p.ClaudeCodeOnly
}

func (p *APIKeyPolicy) FastModeEnabled() bool {
	if p == nil {
		return false
	}
	return p.FastMode
}

func NormalizeCodexChannelMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto":
		return "auto"
	case "provider":
		return "provider"
	case "auth_file":
		return "auth_file"
	default:
		return "auto"
	}
}

func (p *APIKeyPolicy) CodexChannelModeOrDefault() string {
	if p == nil {
		return "auto"
	}
	return NormalizeCodexChannelMode(p.CodexChannelMode)
}

func (p *APIKeyPolicy) UsesGroupBudget() bool {
	if p == nil {
		return false
	}
	return strings.TrimSpace(p.GroupID) != ""
}

func (p *APIKeyPolicy) TokenPackageEnabled() bool {
	if p == nil {
		return false
	}
	return p.TokenPackageUSD > 0
}

func (p *APIKeyPolicy) TokenPackageStartTime() (time.Time, bool) {
	if p == nil || strings.TrimSpace(p.TokenPackageStartedAt) == "" {
		return time.Time{}, false
	}
	startedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(p.TokenPackageStartedAt))
	if err != nil {
		return time.Time{}, false
	}
	return startedAt, true
}

func (p *APIKeyPolicy) CreatedTime() (time.Time, bool) {
	if p == nil || strings.TrimSpace(p.CreatedAt) == "" {
		return time.Time{}, false
	}
	createdAt, err := time.Parse(time.RFC3339, strings.TrimSpace(p.CreatedAt))
	if err != nil {
		return time.Time{}, false
	}
	return createdAt, true
}

func (p *APIKeyPolicy) ExpiryTime() (time.Time, bool) {
	if p == nil || strings.TrimSpace(p.ExpiresAt) == "" {
		return time.Time{}, false
	}
	expiresAt, err := time.Parse(time.RFC3339, strings.TrimSpace(p.ExpiresAt))
	if err != nil {
		return time.Time{}, false
	}
	return expiresAt, true
}

func (p *APIKeyPolicy) IsExpiredAt(now time.Time) bool {
	expiresAt, ok := p.ExpiryTime()
	if !ok {
		return false
	}
	return !expiresAt.After(now)
}

func (p *APIKeyPolicy) IsDisabledAt(now time.Time) bool {
	if p == nil {
		return false
	}
	if p.Disabled {
		return true
	}
	return p.IsExpiredAt(now)
}

func ExcludedModelFamilyAccess(models []string) (allowClaude bool, allowGPT bool, extra []string) {
	normalized := NormalizeExcludedModels(models)
	allowClaude = true
	allowGPT = true
	if len(normalized) == 0 {
		return allowClaude, allowGPT, nil
	}

	gptPatterns := make(map[string]struct{}, len(defaultClientHiddenGPTModelPatterns))
	for _, pattern := range defaultClientHiddenGPTModelPatterns {
		gptPatterns[strings.ToLower(strings.TrimSpace(pattern))] = struct{}{}
	}
	claudePatterns := make(map[string]struct{}, len(defaultClientHiddenClaudeModelPatterns))
	for _, pattern := range defaultClientHiddenClaudeModelPatterns {
		claudePatterns[strings.ToLower(strings.TrimSpace(pattern))] = struct{}{}
	}

	for _, pattern := range normalized {
		if _, ok := gptPatterns[pattern]; ok {
			allowGPT = false
			continue
		}
		if _, ok := claudePatterns[pattern]; ok {
			allowClaude = false
			continue
		}
		extra = append(extra, pattern)
	}
	return allowClaude, allowGPT, extra
}

func BuildExcludedModelFamilies(allowClaude bool, allowGPT bool, extra []string) []string {
	models := make([]string, 0, len(extra)+len(defaultClientHiddenGPTModelPatterns)+len(defaultClientHiddenClaudeModelPatterns))
	models = append(models, NormalizeExcludedModels(extra)...)
	if !allowClaude {
		models = append(models, defaultClientHiddenClaudeModelPatterns...)
	}
	if !allowGPT {
		models = append(models, defaultClientHiddenGPTModelPatterns...)
	}
	return NormalizeExcludedModels(models)
}

// WeeklyBudgetBounds resolves the active weekly budget window for the policy.
// When WeeklyBudgetAnchorAt is unset or invalid, it falls back to the legacy
// Monday 00:00 -> next Monday 00:00 China-time window.
func (p *APIKeyPolicy) WeeklyBudgetBounds(now time.Time) (time.Time, time.Time) {
	if p == nil {
		return policy.WeekBoundsChina(now)
	}
	if anchor, ok := policy.ParseHourlyAnchorRFC3339(p.WeeklyBudgetAnchorAt); ok {
		return policy.AnchoredWindowBounds(anchor, now, 7*24*time.Hour)
	}
	return policy.WeekBoundsChina(now)
}

// ClaudeFailoverTargetModel resolves the configured Claude failover target model.
// Returns ("", false) when failover is disabled.
// When enabled but target-model is empty, it returns a safe default.
func (p *APIKeyPolicy) ClaudeFailoverTargetModel() (string, bool) {
	if p == nil {
		return "", false
	}
	if !p.Failover.Claude.Enabled {
		return "", false
	}
	target := strings.TrimSpace(p.Failover.Claude.TargetModel)
	if target == "" {
		target, _ = policy.DefaultClaudeGPTTargetForFamily("claude-default", p.ClaudeGPTTargetFamilyOrDefault())
	}
	return target, true
}

// ClaudeFailoverTargetModelFor resolves the configured Claude failover target model for a specific request.
// Rules are evaluated first; when no rules match, it falls back to ClaudeFailoverTargetModel().
func (p *APIKeyPolicy) ClaudeFailoverTargetModelFor(requestedModel string) (string, bool) {
	if p == nil {
		return "", false
	}
	if !p.Failover.Claude.Enabled {
		return "", false
	}

	requestKey := policy.NormaliseModelKey(requestedModel)
	if requestKey != "" && len(p.Failover.Claude.Rules) > 0 {
		for _, rule := range p.Failover.Claude.Rules {
			from := strings.ToLower(strings.TrimSpace(rule.FromModel))
			if from == "" {
				continue
			}
			if !policy.MatchWildcard(from, requestKey) {
				continue
			}
			target := strings.TrimSpace(rule.TargetModel)
			if target == "" {
				continue
			}
			return target, true
		}
	}

	return p.ClaudeFailoverTargetModel()
}

// FindAPIKeyPolicy returns the APIKeyPolicy matching the provided key.
// It returns nil when no policy is configured or the key is blank.
func (cfg *Config) FindAPIKeyPolicy(apiKey string) *APIKeyPolicy {
	if cfg == nil {
		return nil
	}
	key := strings.TrimSpace(apiKey)
	if key == "" || len(cfg.APIKeyPolicies) == 0 {
		return nil
	}
	for i := range cfg.APIKeyPolicies {
		if strings.TrimSpace(cfg.APIKeyPolicies[i].APIKey) == key {
			return &cfg.APIKeyPolicies[i]
		}
	}
	return nil
}

// ShouldRouteClaudeToGPT reports whether this client API key should be subject to
// the global Claude -> GPT routing override.
func (cfg *Config) ShouldRouteClaudeToGPT(apiKey string) bool {
	if cfg == nil || !cfg.ClaudeToGPTRoutingEnabled {
		return false
	}
	entry := cfg.FindAPIKeyPolicy(apiKey)
	if entry == nil {
		return true
	}
	return !entry.ClaudeModelsEnabled()
}

func (cfg *Config) ClaudeGPTTargetFamilyOrDefault() string {
	if cfg == nil {
		return policy.EffectiveClaudeGPTTargetFamily("")
	}
	return policy.EffectiveClaudeGPTTargetFamily(cfg.ClaudeToGPTTargetFamily)
}

func (cfg *Config) ClaudeGPTReasoningEffortOrDefault() string {
	if cfg == nil {
		return policy.EffectiveClaudeGPTReasoningEffort("")
	}
	return policy.EffectiveClaudeGPTReasoningEffort(cfg.ClaudeToGPTReasoningEffort)
}

// AllowsClaudeOpus1M reports whether this client API key may keep Claude Opus 1M capability.
// When the global switch is off, all keys are allowed. When the global switch is on, only
// keys with enable-claude-opus-1m=true are allowed.
func (cfg *Config) AllowsClaudeOpus1M(apiKey string) bool {
	if cfg == nil || !cfg.DisableClaudeOpus1M {
		return true
	}
	entry := cfg.FindAPIKeyPolicy(apiKey)
	if entry == nil {
		return false
	}
	return entry.ClaudeOpus1MEnabled()
}

// EffectiveAPIKeyPolicy returns a copy of the API key policy augmented with
// global defaults such as Claude -> GPT routing.
func (cfg *Config) EffectiveAPIKeyPolicy(apiKey string) *APIKeyPolicy {
	return cfg.EffectiveAPIKeyPolicyWithOptions(apiKey, APIKeyPolicyEffectiveOptions{})
}

func (cfg *Config) EffectiveAPIKeyPolicyWithOptions(apiKey string, opts APIKeyPolicyEffectiveOptions) *APIKeyPolicy {
	if cfg == nil {
		return nil
	}
	key := strings.TrimSpace(apiKey)
	if key == "" {
		return nil
	}

	entry := APIKeyPolicy{APIKey: key}
	found := cfg.FindAPIKeyPolicy(key)
	if found != nil {
		entry = *found
	} else {
		entry.ExcludedModels = append(entry.ExcludedModels, defaultClientHiddenGPTModelPatterns...)
		entry.ExcludedModels = NormalizeExcludedModels(entry.ExcludedModels)
	}
	if opts.ForceGlobalClaudeRouting {
		entry.EnableClaudeModels = nil
	}
	if entry.ClaudeCodeOnly == nil {
		entry.ClaudeCodeOnly = boolValuePtr(cfg.ClaudeCodeOnlyEnabled)
	}

	if !cfg.ShouldRouteClaudeToGPT(key) {
		if opts.ForceGlobalClaudeRouting && cfg.ClaudeToGPTRoutingEnabled {
			entry.ModelRouting.Rules = append(
				defaultGlobalClaudeRoutingRules(cfg.ClaudeGPTReasoningEffortOrDefault()),
				entry.ModelRouting.Rules...,
			)
			return &entry
		}
		if found != nil {
			if cfg.ClaudeToGPTRoutingEnabled && entry.ClaudeModelsEnabled() && !hasExplicitClaudeFailoverConfig(entry.Failover.Claude) {
				entry.Failover.Claude.Enabled = true
				entry.Failover.Claude.Rules = append(
					[]ModelFailoverRule(nil),
					synthesizedClaudeFailoverRules(entry.ClaudeGPTTargetFamily, cfg.ClaudeGPTReasoningEffortOrDefault())...,
				)
			}
			return &entry
		}
		return &entry
	}

	if opts.ForceGlobalClaudeRouting {
		entry.ModelRouting.Rules = append(
			defaultGlobalClaudeRoutingRules(cfg.ClaudeGPTReasoningEffortOrDefault()),
			entry.ModelRouting.Rules...,
		)
		return &entry
	}

	entry.ModelRouting.Rules = append(
		synthesizedClaudeRoutingRules(entry.ClaudeGPTTargetFamily, cfg.ClaudeGPTReasoningEffortOrDefault()),
		entry.ModelRouting.Rules...,
	)

	return &entry
}

// SanitizeAPIKeyPolicies trims keys, normalizes excluded-model patterns, and drops invalid limits.
func (cfg *Config) SanitizeAPIKeyPolicies() {
	if cfg == nil || len(cfg.APIKeyPolicies) == 0 {
		return
	}

	type indexEntry struct {
		idx int
	}
	seen := make(map[string]indexEntry, len(cfg.APIKeyPolicies))
	out := make([]APIKeyPolicy, 0, len(cfg.APIKeyPolicies))

	for i := range cfg.APIKeyPolicies {
		entry := cfg.APIKeyPolicies[i]
		entry.APIKey = strings.TrimSpace(entry.APIKey)
		if entry.APIKey == "" {
			continue
		}
		entry.Name = strings.TrimSpace(entry.Name)
		entry.Note = strings.TrimSpace(entry.Note)
		entry.GroupID = strings.TrimSpace(entry.GroupID)

		entry.UpstreamBaseURL = strings.TrimSpace(entry.UpstreamBaseURL)
		entry.ClaudeGPTTargetFamily = policy.NormalizeClaudeGPTTargetBase(entry.ClaudeGPTTargetFamily)
		entry.CodexChannelMode = NormalizeCodexChannelMode(entry.CodexChannelMode)

		entry.ExcludedModels = NormalizeExcludedModels(entry.ExcludedModels)

		// Routing sanitization.
		if len(entry.ModelRouting.Rules) > 0 {
			rules := make([]ModelRoutingRule, 0, len(entry.ModelRouting.Rules))
			for _, rule := range entry.ModelRouting.Rules {
				rule.FromModel = strings.TrimSpace(rule.FromModel)
				rule.TargetModel = strings.TrimSpace(rule.TargetModel)
				if rule.TargetPercent < 0 {
					rule.TargetPercent = 0
				}
				if rule.TargetPercent > 100 {
					rule.TargetPercent = 100
				}
				if rule.StickyWindowSeconds < 0 {
					rule.StickyWindowSeconds = 0
				}
				if rule.FromModel == "" || rule.TargetModel == "" {
					continue
				}
				rules = append(rules, rule)
			}
			entry.ModelRouting.Rules = rules
		}

		// Failover sanitization.
		entry.Failover.Claude.TargetModel = strings.TrimSpace(entry.Failover.Claude.TargetModel)
		if len(entry.Failover.Claude.Rules) > 0 {
			rules := make([]ModelFailoverRule, 0, len(entry.Failover.Claude.Rules))
			for _, rule := range entry.Failover.Claude.Rules {
				rule.FromModel = strings.TrimSpace(rule.FromModel)
				rule.TargetModel = strings.TrimSpace(rule.TargetModel)
				if rule.FromModel == "" || rule.TargetModel == "" {
					continue
				}
				rules = append(rules, rule)
			}
			entry.Failover.Claude.Rules = rules
		}
		if !entry.Failover.Claude.Enabled {
			// Disabled failover should not leave inert target/rule config behind.
			entry.Failover.Claude.TargetModel = ""
			entry.Failover.Claude.Rules = nil
		}

		if len(entry.DailyLimits) > 0 {
			normalized := make(map[string]int, len(entry.DailyLimits))
			for modelID, limit := range entry.DailyLimits {
				m := strings.ToLower(strings.TrimSpace(modelID))
				if m == "" {
					continue
				}
				if limit <= 0 {
					continue
				}
				normalized[m] = limit
			}
			if len(normalized) > 0 {
				entry.DailyLimits = normalized
			} else {
				entry.DailyLimits = nil
			}
		}

		if entry.DailyBudgetUSD <= 0 {
			entry.DailyBudgetUSD = 0
		}
		if entry.ClaudeUsageLimitUSD <= 0 {
			entry.ClaudeUsageLimitUSD = 0
		}
		if entry.WeeklyBudgetUSD <= 0 {
			entry.WeeklyBudgetUSD = 0
		}
		if normalized, ok := policy.NormalizeHourlyAnchorRFC3339(entry.WeeklyBudgetAnchorAt); ok {
			entry.WeeklyBudgetAnchorAt = normalized
		} else {
			entry.WeeklyBudgetAnchorAt = ""
		}
		if entry.UsesGroupBudget() {
			entry.DailyBudgetUSD = 0
			entry.WeeklyBudgetUSD = 0
			entry.WeeklyBudgetAnchorAt = ""
		}
		if entry.TokenPackageUSD <= 0 {
			entry.TokenPackageUSD = 0
			entry.TokenPackageStartedAt = ""
		} else {
			startedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(entry.TokenPackageStartedAt))
			if err != nil {
				entry.TokenPackageUSD = 0
				entry.TokenPackageStartedAt = ""
			} else {
				entry.TokenPackageStartedAt = startedAt.Format(time.RFC3339)
			}
		}

		if createdAt, err := time.Parse(time.RFC3339, strings.TrimSpace(entry.CreatedAt)); err == nil {
			entry.CreatedAt = createdAt.Format(time.RFC3339)
		} else {
			entry.CreatedAt = ""
		}
		if expiresAt, err := time.Parse(time.RFC3339, strings.TrimSpace(entry.ExpiresAt)); err == nil {
			entry.ExpiresAt = expiresAt.Format(time.RFC3339)
		} else {
			entry.ExpiresAt = ""
		}

		key := entry.APIKey
		if prior, ok := seen[key]; ok {
			out[prior.idx] = entry
			continue
		}
		seen[key] = indexEntry{idx: len(out)}
		out = append(out, entry)
	}

	cfg.APIKeyPolicies = out
}

func boolValuePtr(v bool) *bool {
	return &v
}
