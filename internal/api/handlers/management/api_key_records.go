package management

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/alerting"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/apikeygroup"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/billing"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/managementauth"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/policy"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
)

type apiKeyPolicyView struct {
	APIKey                    string                         `json:"api_key"`
	Name                      string                         `json:"name"`
	Note                      string                         `json:"note"`
	CreatedAt                 string                         `json:"created_at"`
	ExpiresAt                 string                         `json:"expires_at"`
	Disabled                  bool                           `json:"disabled"`
	OwnerUsername             string                         `json:"owner_username"`
	OwnerRole                 string                         `json:"owner_role"`
	GroupID                   string                         `json:"group_id"`
	GroupName                 string                         `json:"group_name"`
	AllowClaudeFamily         bool                           `json:"allow_claude_family"`
	AllowGPTFamily            bool                           `json:"allow_gpt_family"`
	FastMode                  bool                           `json:"fast_mode"`
	SessionTrajectoryDisabled bool                           `json:"session_trajectory_disabled"`
	CodexChannelMode          string                         `json:"codex_channel_mode"`
	EnableClaudeModels        bool                           `json:"enable_claude_models"`
	ClaudeUsageLimitUSD       float64                        `json:"claude_usage_limit_usd"`
	ClaudeGPTTargetFamily     string                         `json:"claude_gpt_target_family"`
	EnableClaudeOpus1M        bool                           `json:"enable_claude_opus_1m"`
	ClaudeCodeOnlyMode        string                         `json:"claude_code_only_mode"`
	UpstreamBaseURL           string                         `json:"upstream_base_url"`
	ExcludedModels            []string                       `json:"excluded_models"`
	AllowClaudeOpus46         bool                           `json:"allow_claude_opus_46"`
	DailyLimits               map[string]int                 `json:"daily_limits"`
	DailyBudgetUSD            float64                        `json:"daily_budget_usd"`
	WeeklyBudgetUSD           float64                        `json:"weekly_budget_usd"`
	WeeklyBudgetAnchorAt      string                         `json:"weekly_budget_anchor_at"`
	TokenPackageUSD           float64                        `json:"token_package_usd"`
	TokenPackageStartedAt     string                         `json:"token_package_started_at"`
	TokenPackages             []apiKeyTokenPackagePolicyView `json:"token_packages"`
	ModelRoutingRules         []config.ModelRoutingRule      `json:"model_routing_rules"`
	ClaudeGlobalFallback      bool                           `json:"claude_global_fallback_enabled"`
}

type apiKeyUsageTotals struct {
	Requests        int64   `json:"requests"`
	FailedRequests  int64   `json:"failed_requests"`
	InputTokens     int64   `json:"input_tokens"`
	OutputTokens    int64   `json:"output_tokens"`
	ReasoningTokens int64   `json:"reasoning_tokens"`
	CachedTokens    int64   `json:"cached_tokens"`
	TotalTokens     int64   `json:"total_tokens"`
	CostMicroUSD    int64   `json:"cost_micro_usd"`
	CostUSD         float64 `json:"cost_usd"`
}

type apiKeyBudgetWindowView struct {
	Enabled      bool      `json:"enabled"`
	Label        string    `json:"label"`
	LimitUSD     float64   `json:"limit_usd"`
	UsedUSD      float64   `json:"used_usd"`
	RemainingUSD float64   `json:"remaining_usd"`
	UsedPercent  float64   `json:"used_percent"`
	StartAt      time.Time `json:"start_at"`
	EndAt        time.Time `json:"end_at"`
}

type apiKeyTokenPackageView struct {
	Enabled      bool      `json:"enabled"`
	StartedAt    time.Time `json:"started_at,omitempty"`
	TotalUSD     float64   `json:"total_usd"`
	UsedUSD      float64   `json:"used_usd"`
	RemainingUSD float64   `json:"remaining_usd"`
	Active       bool      `json:"active"`
}

type apiKeyTokenPackagePolicyView struct {
	ID        string  `json:"id"`
	StartedAt string  `json:"started_at"`
	USD       float64 `json:"usd"`
	Note      string  `json:"note"`
}

type apiKeyTokenPackageLedgerView struct {
	ID           string    `json:"id"`
	StartedAt    time.Time `json:"started_at"`
	TotalUSD     float64   `json:"total_usd"`
	UsedUSD      float64   `json:"used_usd"`
	RemainingUSD float64   `json:"remaining_usd"`
	Active       bool      `json:"active"`
	Note         string    `json:"note,omitempty"`
}

type apiKeyTokenPackageUsageEventView struct {
	RequestedAt  time.Time `json:"requested_at"`
	PackageID    string    `json:"package_id"`
	CostUSD      float64   `json:"cost_usd"`
	CostMicroUSD int64     `json:"cost_micro_usd"`
}

type apiKeyDailyLimitView struct {
	Model     string `json:"model"`
	Limit     int    `json:"limit"`
	Used      int64  `json:"used"`
	Remaining int64  `json:"remaining"`
}

type apiKeyRecentDayView struct {
	Day             string  `json:"day"`
	Requests        int64   `json:"requests"`
	FailedRequests  int64   `json:"failed_requests"`
	InputTokens     int64   `json:"input_tokens"`
	OutputTokens    int64   `json:"output_tokens"`
	ReasoningTokens int64   `json:"reasoning_tokens"`
	CachedTokens    int64   `json:"cached_tokens"`
	TotalTokens     int64   `json:"total_tokens"`
	CostUSD         float64 `json:"cost_usd"`
}

type apiKeyModelUsageView struct {
	Model           string  `json:"model"`
	Requests        int64   `json:"requests"`
	FailedRequests  int64   `json:"failed_requests"`
	InputTokens     int64   `json:"input_tokens"`
	OutputTokens    int64   `json:"output_tokens"`
	ReasoningTokens int64   `json:"reasoning_tokens"`
	CachedTokens    int64   `json:"cached_tokens"`
	TotalTokens     int64   `json:"total_tokens"`
	CostUSD         float64 `json:"cost_usd"`
}

type apiKeyEventView struct {
	RequestedAt     time.Time `json:"requested_at"`
	Source          string    `json:"source"`
	AuthIndex       string    `json:"auth_index"`
	Model           string    `json:"model"`
	Failed          bool      `json:"failed"`
	InputTokens     int64     `json:"input_tokens"`
	OutputTokens    int64     `json:"output_tokens"`
	ReasoningTokens int64     `json:"reasoning_tokens"`
	CachedTokens    int64     `json:"cached_tokens"`
	TotalTokens     int64     `json:"total_tokens"`
	CostUSD         float64   `json:"cost_usd"`
}

type apiKeyRecordSummaryView struct {
	APIKey                    string                         `json:"api_key"`
	MaskedAPIKey              string                         `json:"masked_api_key"`
	Name                      string                         `json:"name"`
	Note                      string                         `json:"note"`
	CreatedAt                 string                         `json:"created_at"`
	ExpiresAt                 string                         `json:"expires_at"`
	Disabled                  bool                           `json:"disabled"`
	OwnerUsername             string                         `json:"owner_username"`
	OwnerRole                 string                         `json:"owner_role"`
	GroupID                   string                         `json:"group_id"`
	GroupName                 string                         `json:"group_name"`
	Registered                bool                           `json:"registered"`
	HasExplicitPolicy         bool                           `json:"has_explicit_policy"`
	LastUsedAt                *time.Time                     `json:"last_used_at,omitempty"`
	Today                     apiKeyUsageTotals              `json:"today"`
	CurrentPeriod             apiKeyUsageTotals              `json:"current_period"`
	DailyBudget               apiKeyBudgetWindowView         `json:"daily_budget"`
	WeeklyBudget              apiKeyBudgetWindowView         `json:"weekly_budget"`
	TokenPackage              apiKeyTokenPackageView         `json:"token_package"`
	TokenPackages             []apiKeyTokenPackageLedgerView `json:"token_packages"`
	DailyLimitCount           int                            `json:"daily_limit_count"`
	PolicyFamily              string                         `json:"policy_family"`
	EnableClaudeModels        bool                           `json:"enable_claude_models"`
	FastMode                  bool                           `json:"fast_mode"`
	SessionTrajectoryDisabled bool                           `json:"session_trajectory_disabled"`
}

type apiKeyRecordDetailView struct {
	Summary                 apiKeyRecordSummaryView            `json:"summary"`
	ExplicitPolicy          apiKeyPolicyView                   `json:"explicit_policy"`
	EffectivePolicy         apiKeyPolicyView                   `json:"effective_policy"`
	TodayReport             apiKeyUsageTotals                  `json:"today_report"`
	CurrentPeriod           apiKeyUsageTotals                  `json:"current_period_report"`
	Group                   *apiKeyGroupView                   `json:"group,omitempty"`
	RecentDays              []apiKeyRecentDayView              `json:"recent_days"`
	ModelUsage              []apiKeyModelUsageView             `json:"model_usage"`
	DailyLimits             []apiKeyDailyLimitView             `json:"daily_limits"`
	TokenPackageUsageEvents []apiKeyTokenPackageUsageEventView `json:"token_package_usage_events"`
	RecentEvents            []apiKeyEventView                  `json:"recent_events"`
}

type apiKeyInsightSummaryView struct {
	MaskedAPIKey  string                         `json:"masked_api_key"`
	CreatedAt     string                         `json:"created_at"`
	ExpiresAt     string                         `json:"expires_at"`
	LastUsedAt    *time.Time                     `json:"last_used_at,omitempty"`
	Today         apiKeyUsageTotals              `json:"today"`
	CurrentPeriod apiKeyUsageTotals              `json:"current_period"`
	DailyBudget   apiKeyBudgetWindowView         `json:"daily_budget"`
	WeeklyBudget  apiKeyBudgetWindowView         `json:"weekly_budget"`
	TokenPackage  apiKeyTokenPackageView         `json:"token_package"`
	TokenPackages []apiKeyTokenPackageLedgerView `json:"token_packages"`
}

type apiKeyInsightDetailView struct {
	Summary       apiKeyInsightSummaryView `json:"summary"`
	TodayReport   apiKeyUsageTotals        `json:"today_report"`
	CurrentPeriod apiKeyUsageTotals        `json:"current_period_report"`
	RecentDays    []apiKeyRecentDayView    `json:"recent_days"`
}

type apiKeyRecordMutation struct {
	NewAPIKey   string           `json:"new_api_key"`
	Policy      apiKeyPolicyView `json:"policy"`
	ClearPolicy bool             `json:"clear_policy"`
}

type apiKeyQueryRequest struct {
	APIKey  string   `json:"api_key"`
	APIKeys []string `json:"api_keys"`
	Range   string   `json:"range"`
}

func (h *Handler) billingStoreAvailable(c *gin.Context) bool {
	if h == nil || h.billingStore == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "billing store unavailable"})
		return false
	}
	return true
}

func (h *Handler) GetAPIKeyRecord(c *gin.Context) {
	apiKey, err := url.PathUnescape(strings.TrimSpace(c.Param("apiKey")))
	if err != nil || strings.TrimSpace(apiKey) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid api key"})
		return
	}
	if !h.canManageAPIKey(c, apiKey) {
		c.JSON(http.StatusNotFound, gin.H{"error": "api key not found"})
		return
	}

	now := time.Now().In(policy.ChinaLocation())
	var detail apiKeyRecordDetailView
	if managementIsStaff(c) {
		detail, err = h.buildAPIKeyRecordPolicyDetail(c.Request.Context(), apiKey, now)
	} else {
		if !h.billingStoreAvailable(c) {
			return
		}
		detail, err = h.buildAPIKeyRecordDetail(c.Request.Context(), apiKey, now, parseRangeDays(c.DefaultQuery("range", "14d")), parsePositiveInt(c.DefaultQuery("events_limit", "100"), 100))
	}
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errAPIKeyNotFound) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, detail)
}

func (h *Handler) ListAPIKeyRecordEvents(c *gin.Context) {
	if !h.billingStoreAvailable(c) {
		return
	}
	apiKey, err := url.PathUnescape(strings.TrimSpace(c.Param("apiKey")))
	if err != nil || strings.TrimSpace(apiKey) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid api key"})
		return
	}
	if !h.apiKeyExists(apiKey) || !h.canManageAPIKey(c, apiKey) {
		c.JSON(http.StatusNotFound, gin.H{"error": "api key not found"})
		return
	}

	limit := parsePositiveInt(c.DefaultQuery("limit", "100"), 100)
	rangeDays := parseRangeDays(c.DefaultQuery("range", "14d"))
	start := time.Now().In(policy.ChinaLocation()).AddDate(0, 0, -(rangeDays - 1))
	events, err := h.listAPIKeyEvents(c.Request.Context(), apiKey, start, time.Time{}, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": events})
}

func (h *Handler) CreateAPIKeyRecord(c *gin.Context) {
	if h == nil || h.cfg == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "config unavailable"})
		return
	}

	var body apiKeyRecordMutation
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}

	apiKey := strings.TrimSpace(body.Policy.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(body.NewAPIKey)
	}
	if apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "api_key is required"})
		return
	}
	if h.apiKeyExists(apiKey) {
		c.JSON(http.StatusConflict, gin.H{"error": "api key already exists"})
		return
	}

	h.cfg.APIKeys = append(h.cfg.APIKeys, apiKey)
	h.cfg.APIKeys = normalizeUniqueAPIKeys(h.cfg.APIKeys)
	policyView := body.Policy
	if isEmptyPolicyView(policyView) {
		policyView = defaultAPIKeyPolicyView(apiKey)
	}
	if body.ClearPolicy {
		upsertAPIKeyPolicy(&h.cfg.APIKeyPolicies, defaultOwnedAPIKeyPolicy(apiKey, managementUsername(c), string(managementRole(c))))
	} else {
		policyEntry := viewToPolicy(apiKey, policyView)
		stampPolicyOwner(&policyEntry, managementUsername(c), string(managementRole(c)))
		upsertAPIKeyPolicy(&h.cfg.APIKeyPolicies, policyEntry)
	}
	h.cfg.SanitizeAPIKeyPolicies()
	if !h.persistAPIKeyRecord(c, "", apiKey) {
		return
	}
	h.notifyAPIKeyManagementAction(c, "api_key_created", apiKey, h.cfg.EffectiveAPIKeyPolicy(apiKey))
}

func (h *Handler) UpdateAPIKeyRecord(c *gin.Context) {
	if h == nil || h.cfg == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "config unavailable"})
		return
	}

	currentKey, err := url.PathUnescape(strings.TrimSpace(c.Param("apiKey")))
	if err != nil || strings.TrimSpace(currentKey) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid api key"})
		return
	}

	var body apiKeyRecordMutation
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	beforePolicy := h.cfg.FindAPIKeyPolicy(currentKey)
	beforeTokenUSD := 0.0
	beforeTokenStartedAt := ""
	if beforePolicy != nil {
		beforeTokenUSD = beforePolicy.TokenPackageUSD
		beforeTokenStartedAt = strings.TrimSpace(beforePolicy.TokenPackageStartedAt)
	}

	newKey := strings.TrimSpace(body.NewAPIKey)
	if newKey == "" {
		newKey = currentKey
	}
	if newKey != currentKey && h.apiKeyExists(newKey) {
		c.JSON(http.StatusConflict, gin.H{"error": "target api key already exists"})
		return
	}
	if !h.apiKeyExists(currentKey) && h.cfg.FindAPIKeyPolicy(currentKey) == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "api key not found"})
		return
	}
	if !h.canManageAPIKey(c, currentKey) {
		c.JSON(http.StatusNotFound, gin.H{"error": "api key not found"})
		return
	}

	ownerUsername, ownerRole := h.apiKeyOwner(currentKey)
	if ownerUsername == "" {
		ownerUsername = managementUsername(c)
	}
	if ownerRole == "" {
		ownerRole = string(managementRole(c))
	}
	for i, key := range h.cfg.APIKeys {
		if strings.TrimSpace(key) == currentKey {
			h.cfg.APIKeys[i] = newKey
		}
	}
	h.cfg.APIKeys = normalizeUniqueAPIKeys(h.cfg.APIKeys)

	if existing := h.cfg.FindAPIKeyPolicy(currentKey); existing != nil {
		existing.APIKey = newKey
		stampPolicyOwner(existing, ownerUsername, ownerRole)
	}
	if body.ClearPolicy {
		removeAPIKeyPolicy(&h.cfg.APIKeyPolicies, newKey)
		removeAPIKeyPolicy(&h.cfg.APIKeyPolicies, currentKey)
		upsertAPIKeyPolicy(&h.cfg.APIKeyPolicies, defaultOwnedAPIKeyPolicy(newKey, ownerUsername, ownerRole))
	} else if !isEmptyPolicyView(body.Policy) {
		policyEntry := viewToPolicy(newKey, body.Policy)
		stampPolicyOwner(&policyEntry, ownerUsername, ownerRole)
		upsertAPIKeyPolicy(&h.cfg.APIKeyPolicies, policyEntry)
		removeDuplicatePolicyAlias(&h.cfg.APIKeyPolicies, currentKey, newKey)
	}
	h.cfg.SanitizeAPIKeyPolicies()
	if !h.persistAPIKeyRecord(c, currentKey, newKey) {
		return
	}
	afterPolicy := h.cfg.FindAPIKeyPolicy(newKey)
	action := "api_key_updated"
	if newKey != currentKey {
		action = "api_key_rekeyed"
	} else if tokenPackageChanged(beforeTokenUSD, beforeTokenStartedAt, afterPolicy) {
		action = "api_key_token_package_updated"
	}
	h.notifyAPIKeyManagementAction(c, action, newKey, h.cfg.EffectiveAPIKeyPolicy(newKey))
}

func (h *Handler) DeleteAPIKeyRecord(c *gin.Context) {
	if h == nil || h.cfg == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "config unavailable"})
		return
	}

	apiKey, err := url.PathUnescape(strings.TrimSpace(c.Param("apiKey")))
	if err != nil || strings.TrimSpace(apiKey) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid api key"})
		return
	}
	if !h.canManageAPIKey(c, apiKey) {
		c.JSON(http.StatusNotFound, gin.H{"error": "api key not found"})
		return
	}

	beforePolicy := h.cfg.EffectiveAPIKeyPolicy(apiKey)
	h.cfg.APIKeys = removeAPIKeyValue(h.cfg.APIKeys, apiKey)
	removeAPIKeyPolicy(&h.cfg.APIKeyPolicies, apiKey)
	h.cfg.SanitizeAPIKeyPolicies()
	if !h.deletePersistedAPIKeyRecord(c, apiKey) {
		return
	}
	h.notifyAPIKeyManagementAction(c, "api_key_deleted", apiKey, beforePolicy)
}

func (h *Handler) QueryAPIKeyInsights(c *gin.Context) {
	if !h.billingStoreAvailable(c) {
		return
	}
	var body apiKeyQueryRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}

	keys := normalizeUniqueAPIKeys(append(body.APIKeys, body.APIKey))
	if len(keys) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "api_keys is required"})
		return
	}
	if len(keys) > 30 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "too many api keys"})
		return
	}

	now := time.Now().In(policy.ChinaLocation())
	rangeDays := parseRangeDays(body.Range)
	items := make([]apiKeyInsightDetailView, 0, len(keys))
	invalid := make([]string, 0)
	for _, apiKey := range keys {
		if !h.apiKeyExists(apiKey) {
			invalid = append(invalid, apiKey)
			continue
		}
		detail, err := h.buildAPIKeyInsightDetail(c.Request.Context(), apiKey, now, rangeDays)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load usage insights"})
			return
		}
		items = append(items, detail)
	}

	c.JSON(http.StatusOK, gin.H{
		"items":        items,
		"invalid_keys": invalid,
	})
}

var errAPIKeyNotFound = errors.New("api key not found")

func (h *Handler) buildAPIKeyRecordSummaries(ctx context.Context, now time.Time, rangeDays int) ([]apiKeyRecordSummaryView, error) {
	if h == nil || h.cfg == nil {
		return nil, nil
	}
	keys := collectKnownAPIKeys(h.cfg)
	result := make([]apiKeyRecordSummaryView, 0, len(keys))
	for _, apiKey := range keys {
		summary, err := h.buildAPIKeySummary(ctx, apiKey, now, rangeDays)
		if err != nil {
			return nil, err
		}
		result = append(result, summary)
	}
	sort.Slice(result, func(i, j int) bool {
		left := result[i].LastUsedAt
		right := result[j].LastUsedAt
		if left == nil && right == nil {
			return result[i].APIKey < result[j].APIKey
		}
		if left == nil {
			return false
		}
		if right == nil {
			return true
		}
		if left.Equal(*right) {
			return result[i].APIKey < result[j].APIKey
		}
		return left.After(*right)
	})
	return result, nil
}

func (h *Handler) buildAPIKeyRecordDetail(ctx context.Context, apiKey string, now time.Time, rangeDays int, eventsLimit int) (apiKeyRecordDetailView, error) {
	if !h.apiKeyExists(apiKey) && (h.cfg == nil || h.cfg.FindAPIKeyPolicy(apiKey) == nil) {
		return apiKeyRecordDetailView{}, errAPIKeyNotFound
	}

	summary, err := h.buildAPIKeySummary(ctx, apiKey, now, rangeDays)
	if err != nil {
		return apiKeyRecordDetailView{}, err
	}
	explicitPolicyEntry, explicitGroup, err := h.resolvePolicyWithGroup(ctx, h.cfg.FindAPIKeyPolicy(apiKey))
	if err != nil {
		return apiKeyRecordDetailView{}, err
	}
	effectivePolicyEntry, effectiveGroup, err := h.resolvePolicyWithGroup(ctx, h.cfg.EffectiveAPIKeyPolicy(apiKey))
	if err != nil {
		return apiKeyRecordDetailView{}, err
	}
	explicitPolicy := policyToView(apiKey, explicitPolicyEntry, explicitGroup)
	effectivePolicy := policyToView(apiKey, effectivePolicyEntry, effectiveGroup)

	todayReport, err := h.loadDayTotals(ctx, apiKey, policy.DayKeyChina(now))
	if err != nil {
		return apiKeyRecordDetailView{}, err
	}

	periodRows, err := h.loadCurrentPeriodRows(ctx, apiKey, now)
	if err != nil {
		return apiKeyRecordDetailView{}, err
	}

	recentDays, err := h.buildRecentDayViews(ctx, apiKey, now, rangeDays)
	if err != nil {
		return apiKeyRecordDetailView{}, err
	}
	modelUsage := buildModelUsageViews(periodRows)
	dailyLimits, err := h.buildDailyLimitViews(ctx, apiKey, now, effectivePolicyEntry)
	if err != nil {
		return apiKeyRecordDetailView{}, err
	}
	tokenPackageUsageEvents, err := h.buildTokenPackageUsageEventViews(ctx, apiKey, now, effectivePolicyEntry, eventsLimit)
	if err != nil {
		return apiKeyRecordDetailView{}, err
	}
	recentEvents, err := h.listAPIKeyEvents(ctx, apiKey, now.AddDate(0, 0, -(rangeDays-1)), time.Time{}, eventsLimit)
	if err != nil {
		return apiKeyRecordDetailView{}, err
	}

	return apiKeyRecordDetailView{
		Summary:                 summary,
		ExplicitPolicy:          explicitPolicy,
		EffectivePolicy:         effectivePolicy,
		TodayReport:             todayReport,
		CurrentPeriod:           totalsFromRows(periodRows),
		Group:                   h.optionalGroupView(effectiveGroup),
		RecentDays:              recentDays,
		ModelUsage:              modelUsage,
		DailyLimits:             dailyLimits,
		TokenPackageUsageEvents: tokenPackageUsageEvents,
		RecentEvents:            recentEvents,
	}, nil
}

func (h *Handler) buildAPIKeyInsightDetail(ctx context.Context, apiKey string, now time.Time, rangeDays int) (apiKeyInsightDetailView, error) {
	if !h.apiKeyExists(apiKey) && (h.cfg == nil || h.cfg.FindAPIKeyPolicy(apiKey) == nil) {
		return apiKeyInsightDetailView{}, errAPIKeyNotFound
	}

	summary, err := h.buildAPIKeySummary(ctx, apiKey, now, rangeDays)
	if err != nil {
		return apiKeyInsightDetailView{}, err
	}
	todayReport, err := h.loadDayTotals(ctx, apiKey, policy.DayKeyChina(now))
	if err != nil {
		return apiKeyInsightDetailView{}, err
	}
	periodRows, err := h.loadCurrentPeriodRows(ctx, apiKey, now)
	if err != nil {
		return apiKeyInsightDetailView{}, err
	}
	recentDays, err := h.buildRecentDayViews(ctx, apiKey, now, rangeDays)
	if err != nil {
		return apiKeyInsightDetailView{}, err
	}

	return apiKeyInsightDetailView{
		Summary: apiKeyInsightSummaryView{
			MaskedAPIKey:  summary.MaskedAPIKey,
			CreatedAt:     summary.CreatedAt,
			ExpiresAt:     summary.ExpiresAt,
			LastUsedAt:    summary.LastUsedAt,
			Today:         summary.Today,
			CurrentPeriod: summary.CurrentPeriod,
			DailyBudget:   summary.DailyBudget,
			WeeklyBudget:  summary.WeeklyBudget,
			TokenPackage:  summary.TokenPackage,
			TokenPackages: summary.TokenPackages,
		},
		TodayReport:   todayReport,
		CurrentPeriod: totalsFromRows(periodRows),
		RecentDays:    recentDays,
	}, nil
}

func (h *Handler) buildAPIKeyRecordPolicyDetail(ctx context.Context, apiKey string, now time.Time) (apiKeyRecordDetailView, error) {
	if !h.apiKeyExists(apiKey) && (h.cfg == nil || h.cfg.FindAPIKeyPolicy(apiKey) == nil) {
		return apiKeyRecordDetailView{}, errAPIKeyNotFound
	}

	explicitPolicyEntry, explicitGroup, err := h.resolvePolicyWithGroup(ctx, h.cfg.FindAPIKeyPolicy(apiKey))
	if err != nil {
		return apiKeyRecordDetailView{}, err
	}
	effectivePolicyEntry, effectiveGroup, err := h.resolvePolicyWithGroup(ctx, h.cfg.EffectiveAPIKeyPolicy(apiKey))
	if err != nil {
		return apiKeyRecordDetailView{}, err
	}

	return apiKeyRecordDetailView{
		Summary:                 h.buildAPIKeyPolicyOnlySummary(apiKey, now, effectivePolicyEntry, effectiveGroup),
		ExplicitPolicy:          policyToView(apiKey, explicitPolicyEntry, explicitGroup),
		EffectivePolicy:         policyToView(apiKey, effectivePolicyEntry, effectiveGroup),
		TodayReport:             apiKeyUsageTotals{},
		CurrentPeriod:           apiKeyUsageTotals{},
		Group:                   h.optionalGroupView(effectiveGroup),
		RecentDays:              []apiKeyRecentDayView{},
		ModelUsage:              []apiKeyModelUsageView{},
		DailyLimits:             []apiKeyDailyLimitView{},
		TokenPackageUsageEvents: []apiKeyTokenPackageUsageEventView{},
		RecentEvents:            []apiKeyEventView{},
	}, nil
}

func (h *Handler) buildAPIKeySummary(ctx context.Context, apiKey string, now time.Time, rangeDays int) (apiKeyRecordSummaryView, error) {
	todayTotals, err := h.loadDayTotals(ctx, apiKey, policy.DayKeyChina(now))
	if err != nil {
		return apiKeyRecordSummaryView{}, err
	}

	currentPeriodRows, err := h.loadCurrentPeriodRows(ctx, apiKey, now)
	if err != nil {
		return apiKeyRecordSummaryView{}, err
	}
	currentPeriodTotals := totalsFromRows(currentPeriodRows)

	effectivePolicy, effectiveGroup, err := h.resolvePolicyWithGroup(ctx, h.cfg.EffectiveAPIKeyPolicy(apiKey))
	if err != nil {
		return apiKeyRecordSummaryView{}, err
	}
	dailyBudget, err := h.buildDailyBudgetWindow(ctx, apiKey, now, effectivePolicy)
	if err != nil {
		return apiKeyRecordSummaryView{}, err
	}
	weeklyBudget, err := h.buildWeeklyBudgetWindow(ctx, apiKey, now, effectivePolicy)
	if err != nil {
		return apiKeyRecordSummaryView{}, err
	}
	tokenPackage, err := h.buildTokenPackageView(ctx, apiKey, now, effectivePolicy)
	if err != nil {
		return apiKeyRecordSummaryView{}, err
	}
	tokenPackages, err := h.buildTokenPackageLedgerViews(ctx, apiKey, now, effectivePolicy)
	if err != nil {
		return apiKeyRecordSummaryView{}, err
	}
	lastUsedAt, err := h.loadLastUsedAt(ctx, apiKey)
	if err != nil {
		return apiKeyRecordSummaryView{}, err
	}

	explicitPolicy := h.cfg.FindAPIKeyPolicy(apiKey)
	summary := apiKeyRecordSummaryView{
		APIKey:             apiKey,
		MaskedAPIKey:       util.HideAPIKey(apiKey),
		Name:               "",
		Note:               "",
		CreatedAt:          "",
		ExpiresAt:          "",
		Disabled:           false,
		GroupID:            "",
		GroupName:          "",
		OwnerUsername:      "admin",
		OwnerRole:          "admin",
		Registered:         h.apiKeyExists(apiKey),
		HasExplicitPolicy:  explicitPolicy != nil,
		LastUsedAt:         lastUsedAt,
		Today:              todayTotals,
		CurrentPeriod:      currentPeriodTotals,
		DailyBudget:        dailyBudget,
		WeeklyBudget:       weeklyBudget,
		TokenPackage:       tokenPackage,
		TokenPackages:      tokenPackages,
		DailyLimitCount:    0,
		PolicyFamily:       "",
		EnableClaudeModels: false,
		FastMode:           false,
	}
	if effectivePolicy != nil {
		summary.Name = strings.TrimSpace(effectivePolicy.Name)
		summary.Note = strings.TrimSpace(effectivePolicy.Note)
		summary.CreatedAt = strings.TrimSpace(effectivePolicy.CreatedAt)
		summary.ExpiresAt = strings.TrimSpace(effectivePolicy.ExpiresAt)
		summary.Disabled = effectivePolicy.Disabled
		summary.OwnerUsername = apiKeyOwnerUsername(effectivePolicy)
		summary.OwnerRole = apiKeyOwnerRole(effectivePolicy)
		summary.GroupID = strings.TrimSpace(effectivePolicy.GroupID)
		summary.DailyLimitCount = len(effectivePolicy.DailyLimits)
		summary.PolicyFamily = effectivePolicy.ClaudeGPTTargetFamilyOrDefault()
		summary.EnableClaudeModels = effectivePolicy.ClaudeModelsEnabled()
		summary.FastMode = effectivePolicy.FastModeEnabled()
		summary.SessionTrajectoryDisabled = effectivePolicy.SessionTrajectoryDisabled
	}
	if effectiveGroup != nil {
		summary.GroupName = effectiveGroup.Name
	}

	_ = rangeDays
	return summary, nil
}

func (h *Handler) buildAPIKeyPolicyOnlySummary(apiKey string, now time.Time, effectivePolicy *config.APIKeyPolicy, effectiveGroup *apikeygroup.Group) apiKeyRecordSummaryView {
	summary := apiKeyRecordSummaryView{
		APIKey:            apiKey,
		MaskedAPIKey:      util.HideAPIKey(apiKey),
		Registered:        h.apiKeyExists(apiKey),
		HasExplicitPolicy: h.cfg != nil && h.cfg.FindAPIKeyPolicy(apiKey) != nil,
		OwnerUsername:     "admin",
		OwnerRole:         "admin",
		DailyBudget: apiKeyBudgetWindowView{
			Enabled: false,
			StartAt: time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, policy.ChinaLocation()),
			EndAt:   time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, policy.ChinaLocation()),
		},
		WeeklyBudget: apiKeyBudgetWindowView{
			Enabled: false,
			StartAt: now,
			EndAt:   now,
		},
		TokenPackage: apiKeyTokenPackageView{
			Enabled: false,
		},
	}
	if effectivePolicy != nil {
		summary.Name = strings.TrimSpace(effectivePolicy.Name)
		summary.Note = strings.TrimSpace(effectivePolicy.Note)
		summary.CreatedAt = strings.TrimSpace(effectivePolicy.CreatedAt)
		summary.ExpiresAt = strings.TrimSpace(effectivePolicy.ExpiresAt)
		summary.Disabled = effectivePolicy.Disabled
		summary.OwnerUsername = apiKeyOwnerUsername(effectivePolicy)
		summary.OwnerRole = apiKeyOwnerRole(effectivePolicy)
		summary.GroupID = strings.TrimSpace(effectivePolicy.GroupID)
		summary.DailyLimitCount = len(effectivePolicy.DailyLimits)
		summary.PolicyFamily = effectivePolicy.ClaudeGPTTargetFamilyOrDefault()
		summary.EnableClaudeModels = effectivePolicy.ClaudeModelsEnabled()
		summary.FastMode = effectivePolicy.FastModeEnabled()
		if effectivePolicy.DailyBudgetUSD > 0 {
			summary.DailyBudget.Enabled = true
			summary.DailyBudget.Label = "daily"
			summary.DailyBudget.LimitUSD = effectivePolicy.DailyBudgetUSD
			summary.DailyBudget.RemainingUSD = effectivePolicy.DailyBudgetUSD
		}
		if effectivePolicy.WeeklyBudgetUSD > 0 {
			summary.WeeklyBudget.Enabled = true
			summary.WeeklyBudget.Label = "weekly"
			summary.WeeklyBudget.LimitUSD = effectivePolicy.WeeklyBudgetUSD
			summary.WeeklyBudget.RemainingUSD = effectivePolicy.WeeklyBudgetUSD
		}
		if entries := effectivePolicy.TokenPackageEntries(); len(entries) > 0 {
			summary.TokenPackage.Enabled = true
			for _, entry := range entries {
				startedAt, _ := time.Parse(time.RFC3339, entry.StartedAt)
				summary.TokenPackage.TotalUSD += entry.USD
				summary.TokenPackage.RemainingUSD += entry.USD
				summary.TokenPackages = append(summary.TokenPackages, apiKeyTokenPackageLedgerView{
					ID:           entry.ID,
					StartedAt:    startedAt,
					TotalUSD:     entry.USD,
					RemainingUSD: entry.USD,
					Active:       !startedAt.After(now),
					Note:         entry.Note,
				})
			}
			summary.TokenPackage.Active = true
			if len(summary.TokenPackages) > 0 {
				summary.TokenPackage.StartedAt = summary.TokenPackages[0].StartedAt
			}
		}
	}
	if effectiveGroup != nil {
		summary.GroupName = effectiveGroup.Name
	}
	return summary
}

func (h *Handler) loadCurrentPeriodRows(ctx context.Context, apiKey string, now time.Time) ([]billing.DailyUsageRow, error) {
	effectivePolicy, _, err := h.resolvePolicyWithGroup(ctx, h.cfg.EffectiveAPIKeyPolicy(apiKey))
	if err != nil {
		return nil, err
	}
	if effectivePolicy == nil {
		return nil, config.ErrWeeklyBudgetAnchorUnavailable
	}
	start, end, err := effectivePolicy.WeeklyBudgetBounds(now)
	if err != nil {
		return nil, err
	}
	events, err := h.billingStore.ListUsageEventsByAPIKey(ctx, apiKey, start, end, 0, false)
	if err != nil {
		return nil, err
	}
	return aggregateUsageEventsByModel(events), nil
}

func (h *Handler) loadDayTotals(ctx context.Context, apiKey, dayKey string) (apiKeyUsageTotals, error) {
	report, err := h.billingStore.GetDailyUsageReport(ctx, apiKey, dayKey)
	if err != nil {
		return apiKeyUsageTotals{}, err
	}
	return apiKeyUsageTotals{
		Requests:        report.TotalRequests,
		FailedRequests:  report.TotalFailed,
		TotalTokens:     report.TotalTokens,
		CostMicroUSD:    report.TotalCostMicro,
		CostUSD:         report.TotalCostUSD,
		InputTokens:     sumDailyRows(report.Models, func(row billing.DailyUsageRow) int64 { return row.InputTokens }),
		OutputTokens:    sumDailyRows(report.Models, func(row billing.DailyUsageRow) int64 { return row.OutputTokens }),
		ReasoningTokens: sumDailyRows(report.Models, func(row billing.DailyUsageRow) int64 { return row.ReasoningTokens }),
		CachedTokens:    sumDailyRows(report.Models, func(row billing.DailyUsageRow) int64 { return row.CachedTokens }),
	}, nil
}

func (h *Handler) buildDailyBudgetWindow(ctx context.Context, apiKey string, now time.Time, p *config.APIKeyPolicy) (apiKeyBudgetWindowView, error) {
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, policy.ChinaLocation())
	end := start.Add(24 * time.Hour)
	usedMicro := int64(0)
	var err error
	if p != nil && p.TokenPackageEnabled() {
		state, errState := billing.ComputeBudgetReplayState(ctx, h.billingStore, apiKey, now, p)
		if errState != nil {
			return apiKeyBudgetWindowView{}, errState
		}
		usedMicro = state.DailyUsedMicro
	} else {
		usedMicro, err = h.billingStore.GetDailyCostMicroUSD(ctx, apiKey, policy.DayKeyChina(now))
		if err != nil {
			return apiKeyBudgetWindowView{}, err
		}
	}
	limitUSD := 0.0
	if p != nil {
		limitUSD = p.DailyBudgetUSD
	}
	return buildBudgetWindowView("Today", p != nil && p.DailyBudgetUSD > 0, limitUSD, usedMicro, start, end), nil
}

func (h *Handler) buildWeeklyBudgetWindow(ctx context.Context, apiKey string, now time.Time, p *config.APIKeyPolicy) (apiKeyBudgetWindowView, error) {
	start, end := now, now
	limitUSD := 0.0
	if p != nil {
		var err error
		start, end, err = p.WeeklyBudgetBounds(now)
		if err != nil {
			return apiKeyBudgetWindowView{}, err
		}
		limitUSD = p.WeeklyBudgetUSD
	}

	var usedMicro int64
	var err error
	if p != nil && p.TokenPackageEnabled() {
		state, errState := billing.ComputeBudgetReplayState(ctx, h.billingStore, apiKey, now, p)
		if errState != nil {
			return apiKeyBudgetWindowView{}, errState
		}
		usedMicro = state.WeeklyUsedMicro
	} else if p != nil {
		usedMicro, err = h.billingStore.GetCostMicroUSDByTimeRange(ctx, apiKey, start, end)
	} else {
		usedMicro = 0
	}
	if err != nil {
		return apiKeyBudgetWindowView{}, err
	}
	return buildBudgetWindowView("Current period", limitUSD > 0, limitUSD, usedMicro, start, end), nil
}

func (h *Handler) buildTokenPackageView(ctx context.Context, apiKey string, now time.Time, p *config.APIKeyPolicy) (apiKeyTokenPackageView, error) {
	summary, _, err := h.buildTokenPackageViews(ctx, apiKey, now, p)
	return summary, err
}

func (h *Handler) buildTokenPackageLedgerViews(ctx context.Context, apiKey string, now time.Time, p *config.APIKeyPolicy) ([]apiKeyTokenPackageLedgerView, error) {
	_, packages, err := h.buildTokenPackageViews(ctx, apiKey, now, p)
	return packages, err
}

func (h *Handler) buildTokenPackageViews(ctx context.Context, apiKey string, now time.Time, p *config.APIKeyPolicy) (apiKeyTokenPackageView, []apiKeyTokenPackageLedgerView, error) {
	if p == nil || !p.TokenPackageEnabled() {
		return apiKeyTokenPackageView{}, nil, nil
	}
	state, err := billing.ComputeBudgetReplayState(ctx, h.billingStore, apiKey, now, p)
	if err != nil {
		return apiKeyTokenPackageView{}, nil, err
	}

	packages := make([]apiKeyTokenPackageLedgerView, 0, len(state.Packages))
	summary := apiKeyTokenPackageView{Enabled: len(state.Packages) > 0}
	for _, pkg := range state.Packages {
		totalUSD := microUSDToUSD(pkg.TotalMicro)
		usedUSD := microUSDToUSD(pkg.UsedMicro)
		remainingUSD := microUSDToUSD(pkg.RemainingMicro)
		packages = append(packages, apiKeyTokenPackageLedgerView{
			ID:           pkg.ID,
			StartedAt:    pkg.StartedAt,
			TotalUSD:     round6(totalUSD),
			UsedUSD:      round6(usedUSD),
			RemainingUSD: round6(remainingUSD),
			Active:       pkg.Active,
			Note:         pkg.Note,
		})
		if summary.StartedAt.IsZero() || pkg.StartedAt.Before(summary.StartedAt) {
			summary.StartedAt = pkg.StartedAt
		}
		summary.TotalUSD += totalUSD
		summary.UsedUSD += usedUSD
		summary.RemainingUSD += remainingUSD
		if pkg.Active {
			summary.Active = true
		}
	}
	summary.TotalUSD = round6(summary.TotalUSD)
	summary.UsedUSD = round6(summary.UsedUSD)
	summary.RemainingUSD = round6(summary.RemainingUSD)
	return summary, packages, nil
}

func (h *Handler) buildTokenPackageUsageEventViews(ctx context.Context, apiKey string, now time.Time, p *config.APIKeyPolicy, limit int) ([]apiKeyTokenPackageUsageEventView, error) {
	if p == nil || !p.TokenPackageEnabled() {
		return []apiKeyTokenPackageUsageEventView{}, nil
	}
	_, allocations, err := billing.ComputeBudgetReplayStateWithAllocations(ctx, h.billingStore, apiKey, now, p)
	if err != nil {
		return nil, err
	}
	result := make([]apiKeyTokenPackageUsageEventView, 0, len(allocations))
	for i := len(allocations) - 1; i >= 0; i-- {
		allocation := allocations[i]
		result = append(result, apiKeyTokenPackageUsageEventView{
			RequestedAt:  time.Unix(allocation.RequestedAt, 0),
			PackageID:    allocation.PackageID,
			CostUSD:      round6(microUSDToUSD(allocation.CostMicroUSD)),
			CostMicroUSD: allocation.CostMicroUSD,
		})
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result, nil
}

func (h *Handler) buildRecentDayViews(ctx context.Context, apiKey string, now time.Time, rangeDays int) ([]apiKeyRecentDayView, error) {
	endExclusive := policy.DayKeyChina(now.AddDate(0, 0, 1))
	startDay := policy.DayKeyChina(now.AddDate(0, 0, -(rangeDays - 1)))
	rows, err := h.billingStore.ListDailyUsageRowsByAPIKey(ctx, apiKey, startDay, endExclusive)
	if err != nil {
		return nil, err
	}

	byDay := make(map[string]apiKeyRecentDayView)
	for _, row := range rows {
		item := byDay[row.Day]
		item.Day = row.Day
		item.Requests += row.Requests
		item.FailedRequests += row.FailedRequests
		item.InputTokens += row.InputTokens
		item.OutputTokens += row.OutputTokens
		item.ReasoningTokens += row.ReasoningTokens
		item.CachedTokens += row.CachedTokens
		item.TotalTokens += row.TotalTokens
		item.CostUSD = round6(item.CostUSD + microUSDToUSD(row.CostMicroUSD))
		byDay[row.Day] = item
	}

	result := make([]apiKeyRecentDayView, 0, rangeDays)
	for offset := 0; offset < rangeDays; offset++ {
		day := policy.DayKeyChina(now.AddDate(0, 0, -(rangeDays - 1 - offset)))
		item := byDay[day]
		item.Day = day
		result = append(result, item)
	}
	return result, nil
}

func (h *Handler) buildDailyLimitViews(ctx context.Context, apiKey string, now time.Time, p *config.APIKeyPolicy) ([]apiKeyDailyLimitView, error) {
	if p == nil || len(p.DailyLimits) == 0 || h.dailyLimiter == nil {
		return nil, nil
	}
	rows, err := h.dailyLimiter.ListUsageCounts(ctx, apiKey, policy.DayKeyChina(now))
	if err != nil {
		return nil, err
	}
	usedByModel := make(map[string]int64, len(rows))
	for _, row := range rows {
		usedByModel[row.Model] = row.Count
	}

	result := make([]apiKeyDailyLimitView, 0, len(p.DailyLimits))
	for model, limit := range p.DailyLimits {
		if limit <= 0 {
			continue
		}
		used := usedByModel[strings.ToLower(strings.TrimSpace(model))]
		remaining := int64(limit) - used
		if remaining < 0 {
			remaining = 0
		}
		result = append(result, apiKeyDailyLimitView{
			Model:     model,
			Limit:     limit,
			Used:      used,
			Remaining: remaining,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Model < result[j].Model })
	return result, nil
}

func (h *Handler) listAPIKeyEvents(ctx context.Context, apiKey string, start time.Time, end time.Time, limit int) ([]apiKeyEventView, error) {
	events, err := h.billingStore.ListUsageEventsByAPIKey(ctx, apiKey, start, end, limit, true)
	if err != nil {
		return nil, err
	}
	result := make([]apiKeyEventView, 0, len(events))
	for _, event := range events {
		result = append(result, apiKeyEventView{
			RequestedAt:     time.Unix(event.RequestedAt, 0).In(policy.ChinaLocation()),
			Source:          strings.TrimSpace(event.Source),
			AuthIndex:       strings.TrimSpace(event.AuthIndex),
			Model:           event.Model,
			Failed:          event.Failed,
			InputTokens:     event.InputTokens,
			OutputTokens:    event.OutputTokens,
			ReasoningTokens: event.ReasoningTokens,
			CachedTokens:    event.CachedTokens,
			TotalTokens:     event.TotalTokens,
			CostUSD:         microUSDToUSD(event.CostMicroUSD),
		})
	}
	return result, nil
}

func (h *Handler) loadLastUsedAt(ctx context.Context, apiKey string) (*time.Time, error) {
	latest, ok, err := h.billingStore.GetLatestUsageEventTime(ctx, apiKey)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	local := latest.In(policy.ChinaLocation())
	return &local, nil
}

func (h *Handler) apiKeyExists(apiKey string) bool {
	if h == nil || h.cfg == nil {
		return false
	}
	target := strings.TrimSpace(apiKey)
	for _, key := range h.cfg.APIKeys {
		if strings.TrimSpace(key) == target {
			return true
		}
	}
	return false
}

func collectKnownAPIKeys(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(cfg.APIKeys)+len(cfg.APIKeyPolicies))
	keys := make([]string, 0, len(cfg.APIKeys)+len(cfg.APIKeyPolicies))
	for _, apiKey := range cfg.APIKeys {
		trimmed := strings.TrimSpace(apiKey)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		keys = append(keys, trimmed)
	}
	for _, entry := range cfg.APIKeyPolicies {
		trimmed := strings.TrimSpace(entry.APIKey)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		keys = append(keys, trimmed)
	}
	sort.Strings(keys)
	return keys
}

func (h *Handler) resolvePolicyWithGroup(ctx context.Context, p *config.APIKeyPolicy) (*config.APIKeyPolicy, *apikeygroup.Group, error) {
	if p == nil {
		return nil, nil, nil
	}
	return apikeygroup.ApplyGroupBudget(ctx, h.groupStore, p)
}

func (h *Handler) optionalGroupView(group *apikeygroup.Group) *apiKeyGroupView {
	if group == nil {
		return nil
	}
	view := h.groupToView(*group)
	return &view
}

func policyToView(apiKey string, p *config.APIKeyPolicy, group *apikeygroup.Group) apiKeyPolicyView {
	allowClaudeFamily, allowGPTFamily, extraExcludedModels := config.ExcludedModelFamilyAccess(nil)
	view := apiKeyPolicyView{
		APIKey:            apiKey,
		Name:              "",
		Note:              "",
		CreatedAt:         "",
		ExpiresAt:         "",
		Disabled:          false,
		GroupID:           "",
		GroupName:         "",
		AllowClaudeFamily: allowClaudeFamily,
		AllowGPTFamily:    allowGPTFamily,
		AllowClaudeOpus46: true,
		DailyLimits:       map[string]int{},
		ExcludedModels:    extraExcludedModels,
		ModelRoutingRules: nil,
		CodexChannelMode:  "auto",
	}
	if p == nil {
		return view
	}
	view.APIKey = strings.TrimSpace(p.APIKey)
	view.Name = strings.TrimSpace(p.Name)
	view.Note = strings.TrimSpace(p.Note)
	view.CreatedAt = strings.TrimSpace(p.CreatedAt)
	view.ExpiresAt = strings.TrimSpace(p.ExpiresAt)
	view.Disabled = p.Disabled
	view.OwnerUsername = apiKeyOwnerUsername(p)
	view.OwnerRole = apiKeyOwnerRole(p)
	view.GroupID = strings.TrimSpace(p.GroupID)
	if group != nil {
		view.GroupName = group.Name
	}
	view.AllowClaudeFamily, view.AllowGPTFamily, view.ExcludedModels = config.ExcludedModelFamilyAccess(p.ExcludedModels)
	view.FastMode = p.FastMode
	view.SessionTrajectoryDisabled = p.SessionTrajectoryDisabled
	view.CodexChannelMode = p.CodexChannelModeOrDefault()
	view.EnableClaudeModels = p.ClaudeModelsEnabled()
	view.ClaudeGlobalFallback = p.AllowsClaudeGlobalFallback()
	view.ClaudeUsageLimitUSD = p.ClaudeUsageLimitUSD
	view.ClaudeGPTTargetFamily = p.ClaudeGPTTargetFamily
	view.EnableClaudeOpus1M = p.ClaudeOpus1MEnabled()
	switch {
	case p.ClaudeCodeOnly == nil:
		view.ClaudeCodeOnlyMode = "inherit"
	case p.ClaudeCodeOnlyEnabled():
		view.ClaudeCodeOnlyMode = "enabled"
	default:
		view.ClaudeCodeOnlyMode = "disabled"
	}
	view.UpstreamBaseURL = p.UpstreamBaseURL
	view.AllowClaudeOpus46 = p.AllowsClaudeOpus46()
	view.DailyLimits = copyDailyLimits(p.DailyLimits)
	view.DailyBudgetUSD = p.DailyBudgetUSD
	view.WeeklyBudgetUSD = p.WeeklyBudgetUSD
	view.WeeklyBudgetAnchorAt = p.WeeklyBudgetAnchorAt
	view.TokenPackageUSD = p.TokenPackageUSD
	view.TokenPackageStartedAt = p.TokenPackageStartedAt
	view.TokenPackages = tokenPackagePolicyEntriesToView(p.TokenPackageEntries())
	view.ModelRoutingRules = append([]config.ModelRoutingRule(nil), p.ModelRouting.Rules...)
	return view
}

func viewToPolicy(apiKey string, view apiKeyPolicyView) config.APIKeyPolicy {
	enableClaudeModels := view.EnableClaudeModels
	claudeGlobalFallback := view.ClaudeGlobalFallback
	enableClaudeOpus1M := view.EnableClaudeOpus1M
	allowClaudeOpus46 := view.AllowClaudeOpus46
	var claudeCodeOnly *bool
	switch strings.ToLower(strings.TrimSpace(view.ClaudeCodeOnlyMode)) {
	case "", "inherit":
		claudeCodeOnly = nil
	case "enabled":
		value := true
		claudeCodeOnly = &value
	case "disabled":
		value := false
		claudeCodeOnly = &value
	}
	return config.APIKeyPolicy{
		APIKey:                      apiKey,
		Name:                        strings.TrimSpace(view.Name),
		Note:                        strings.TrimSpace(view.Note),
		CreatedAt:                   strings.TrimSpace(view.CreatedAt),
		ExpiresAt:                   strings.TrimSpace(view.ExpiresAt),
		Disabled:                    view.Disabled,
		OwnerUsername:               strings.TrimSpace(view.OwnerUsername),
		OwnerRole:                   normalizeAPIKeyOwnerRole(view.OwnerRole),
		GroupID:                     strings.TrimSpace(view.GroupID),
		FastMode:                    view.FastMode,
		SessionTrajectoryDisabled:   view.SessionTrajectoryDisabled,
		CodexChannelMode:            config.NormalizeCodexChannelMode(view.CodexChannelMode),
		EnableClaudeModels:          &enableClaudeModels,
		ClaudeGlobalFallbackEnabled: &claudeGlobalFallback,
		ClaudeUsageLimitUSD:         view.ClaudeUsageLimitUSD,
		ClaudeGPTTargetFamily:       strings.TrimSpace(view.ClaudeGPTTargetFamily),
		EnableClaudeOpus1M:          &enableClaudeOpus1M,
		ClaudeCodeOnly:              claudeCodeOnly,
		UpstreamBaseURL:             strings.TrimSpace(view.UpstreamBaseURL),
		ExcludedModels:              config.BuildExcludedModelFamilies(view.AllowClaudeFamily, view.AllowGPTFamily, view.ExcludedModels),
		AllowClaudeOpus46:           &allowClaudeOpus46,
		DailyLimits:                 copyDailyLimits(view.DailyLimits),
		DailyBudgetUSD:              view.DailyBudgetUSD,
		WeeklyBudgetUSD:             view.WeeklyBudgetUSD,
		WeeklyBudgetAnchorAt:        strings.TrimSpace(view.WeeklyBudgetAnchorAt),
		TokenPackageUSD:             view.TokenPackageUSD,
		TokenPackageStartedAt:       strings.TrimSpace(view.TokenPackageStartedAt),
		TokenPackages:               tokenPackagePolicyEntriesFromView(view.TokenPackages),
		ModelRouting: config.APIKeyModelRoutingPolicy{
			Rules: append([]config.ModelRoutingRule(nil), view.ModelRoutingRules...),
		},
	}
}

func tokenPackagePolicyEntriesToView(entries []config.TokenPackageEntry) []apiKeyTokenPackagePolicyView {
	if len(entries) == 0 {
		return []apiKeyTokenPackagePolicyView{}
	}
	result := make([]apiKeyTokenPackagePolicyView, 0, len(entries))
	for _, entry := range entries {
		result = append(result, apiKeyTokenPackagePolicyView{
			ID:        strings.TrimSpace(entry.ID),
			StartedAt: strings.TrimSpace(entry.StartedAt),
			USD:       entry.USD,
			Note:      strings.TrimSpace(entry.Note),
		})
	}
	return result
}

func tokenPackagePolicyEntriesFromView(entries []apiKeyTokenPackagePolicyView) []config.TokenPackageEntry {
	if len(entries) == 0 {
		return nil
	}
	result := make([]config.TokenPackageEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, config.TokenPackageEntry{
			ID:        strings.TrimSpace(entry.ID),
			StartedAt: strings.TrimSpace(entry.StartedAt),
			USD:       entry.USD,
			Note:      strings.TrimSpace(entry.Note),
		})
	}
	return result
}

func upsertAPIKeyPolicy(policies *[]config.APIKeyPolicy, entry config.APIKeyPolicy) {
	if policies == nil {
		return
	}
	target := strings.TrimSpace(entry.APIKey)
	for i := range *policies {
		if strings.TrimSpace((*policies)[i].APIKey) == target {
			(*policies)[i] = entry
			return
		}
	}
	*policies = append(*policies, entry)
}

func removeAPIKeyPolicy(policies *[]config.APIKeyPolicy, apiKey string) {
	if policies == nil {
		return
	}
	target := strings.TrimSpace(apiKey)
	filtered := (*policies)[:0]
	for _, entry := range *policies {
		if strings.TrimSpace(entry.APIKey) == target {
			continue
		}
		filtered = append(filtered, entry)
	}
	*policies = filtered
}

func removeDuplicatePolicyAlias(policies *[]config.APIKeyPolicy, oldKey, newKey string) {
	if policies == nil {
		return
	}
	oldKey = strings.TrimSpace(oldKey)
	newKey = strings.TrimSpace(newKey)
	filtered := (*policies)[:0]
	seenNew := false
	for _, entry := range *policies {
		trimmed := strings.TrimSpace(entry.APIKey)
		if trimmed == oldKey && oldKey != newKey {
			continue
		}
		if trimmed == newKey {
			if seenNew {
				continue
			}
			seenNew = true
		}
		filtered = append(filtered, entry)
	}
	*policies = filtered
}

func removeAPIKeyValue(keys []string, apiKey string) []string {
	target := strings.TrimSpace(apiKey)
	filtered := keys[:0]
	for _, key := range keys {
		if strings.TrimSpace(key) == target {
			continue
		}
		filtered = append(filtered, key)
	}
	return filtered
}

func normalizeUniqueAPIKeys(keys []string) []string {
	keys = normalizeAPIKeysList(keys)
	seen := make(map[string]struct{}, len(keys))
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, key)
	}
	return result
}

func defaultAPIKeyPolicyView(apiKey string) apiKeyPolicyView {
	now := time.Now().UTC()
	return apiKeyPolicyView{
		APIKey:             strings.TrimSpace(apiKey),
		Name:               "",
		Note:               "",
		CreatedAt:          now.Format(time.RFC3339),
		ExpiresAt:          now.AddDate(0, 1, 0).Format(time.RFC3339),
		AllowClaudeFamily:  true,
		AllowGPTFamily:     false,
		ClaudeCodeOnlyMode: "inherit",
		CodexChannelMode:   "auto",
		AllowClaudeOpus46:  true,
		DailyLimits:        map[string]int{},
		OwnerUsername:      "admin",
		OwnerRole:          "admin",
	}
}

func defaultOwnedAPIKeyPolicy(apiKey, ownerUsername, ownerRole string) config.APIKeyPolicy {
	entry := config.APIKeyPolicy{
		APIKey:         strings.TrimSpace(apiKey),
		ExcludedModels: config.BuildExcludedModelFamilies(true, false, nil),
	}
	stampPolicyOwner(&entry, ownerUsername, ownerRole)
	return entry
}

func copyDailyLimits(src map[string]int) map[string]int {
	if len(src) == 0 {
		return map[string]int{}
	}
	dst := make(map[string]int, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func isEmptyPolicyView(view apiKeyPolicyView) bool {
	return strings.TrimSpace(view.APIKey) == "" &&
		strings.TrimSpace(view.Name) == "" &&
		strings.TrimSpace(view.Note) == "" &&
		strings.TrimSpace(view.CreatedAt) == "" &&
		strings.TrimSpace(view.ExpiresAt) == "" &&
		!view.Disabled &&
		strings.TrimSpace(view.GroupID) == "" &&
		!view.AllowClaudeFamily &&
		!view.AllowGPTFamily &&
		!view.FastMode &&
		!view.SessionTrajectoryDisabled &&
		strings.TrimSpace(view.CodexChannelMode) == "" &&
		!view.EnableClaudeModels &&
		strings.TrimSpace(view.ClaudeCodeOnlyMode) == "" &&
		view.ClaudeUsageLimitUSD == 0 &&
		strings.TrimSpace(view.ClaudeGPTTargetFamily) == "" &&
		!view.EnableClaudeOpus1M &&
		strings.TrimSpace(view.UpstreamBaseURL) == "" &&
		len(view.ExcludedModels) == 0 &&
		!view.AllowClaudeOpus46 &&
		len(view.DailyLimits) == 0 &&
		view.DailyBudgetUSD == 0 &&
		view.WeeklyBudgetUSD == 0 &&
		strings.TrimSpace(view.WeeklyBudgetAnchorAt) == "" &&
		view.TokenPackageUSD == 0 &&
		strings.TrimSpace(view.TokenPackageStartedAt) == "" &&
		len(view.ModelRoutingRules) == 0
}

func tokenPackageChanged(previousUSD float64, previousStartedAt string, afterPolicy *config.APIKeyPolicy) bool {
	nextUSD := 0.0
	nextStartedAt := ""
	if afterPolicy != nil {
		nextUSD = afterPolicy.TokenPackageUSD
		nextStartedAt = strings.TrimSpace(afterPolicy.TokenPackageStartedAt)
	}
	return previousUSD != nextUSD || strings.TrimSpace(previousStartedAt) != nextStartedAt
}

func stampPolicyOwner(policyEntry *config.APIKeyPolicy, username, role string) {
	if policyEntry == nil {
		return
	}
	username = strings.TrimSpace(username)
	role = normalizeAPIKeyOwnerRole(role)
	if role == "" {
		role = "admin"
	}
	if role == string(managementauth.RoleAdmin) {
		username = "admin"
	} else if username == "" {
		username = "unknown"
	}
	policyEntry.OwnerUsername = username
	policyEntry.OwnerRole = role
}

func normalizeAPIKeyOwnerRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case string(managementauth.RoleStaff):
		return string(managementauth.RoleStaff)
	case string(managementauth.RoleAdmin):
		return string(managementauth.RoleAdmin)
	default:
		return ""
	}
}

func apiKeyOwnerUsername(policyEntry *config.APIKeyPolicy) string {
	if policyEntry == nil {
		return "admin"
	}
	if apiKeyOwnerRole(policyEntry) == string(managementauth.RoleAdmin) {
		return "admin"
	}
	return strings.TrimSpace(policyEntry.OwnerUsername)
}

func apiKeyOwnerRole(policyEntry *config.APIKeyPolicy) string {
	if policyEntry == nil {
		return "admin"
	}
	if value := normalizeAPIKeyOwnerRole(policyEntry.OwnerRole); value != "" {
		return value
	}
	return string(managementauth.RoleAdmin)
}

func (h *Handler) apiKeyOwner(apiKey string) (string, string) {
	if h == nil || h.cfg == nil {
		return "", ""
	}
	if policyEntry := h.cfg.EffectiveAPIKeyPolicy(apiKey); policyEntry != nil {
		return apiKeyOwnerUsername(policyEntry), apiKeyOwnerRole(policyEntry)
	}
	return "", ""
}

func (h *Handler) canManageAPIKey(c *gin.Context, apiKey string) bool {
	if !managementIsStaff(c) {
		return true
	}
	ownerUsername, ownerRole := h.apiKeyOwner(apiKey)
	return ownerRole == string(managementauth.RoleStaff) && ownerUsername == managementUsername(c)
}

func apiKeyOwnerUsernameForRole(username, role string) string {
	role = normalizeAPIKeyOwnerRole(role)
	if role == string(managementauth.RoleAdmin) || role == "" {
		return "admin"
	}
	return strings.TrimSpace(username)
}

func apiKeyRecordOwnerMatchesFilter(username, role, ownerFilter string) bool {
	ownerFilter = strings.TrimSpace(ownerFilter)
	if ownerFilter == "" {
		return true
	}
	role = normalizeAPIKeyOwnerRole(role)
	if role == string(managementauth.RoleAdmin) {
		return ownerFilter == "admin"
	}
	return role == string(managementauth.RoleStaff) && strings.TrimSpace(username) == ownerFilter
}

func (h *Handler) notifyAPIKeyManagementAction(c *gin.Context, action, apiKey string, policyEntry *config.APIKeyPolicy) {
	if h == nil {
		return
	}
	group, _ := h.resolveNotificationGroup(c.Request.Context(), policyEntry)
	principal := managementPrincipalFromContext(c)
	role := string(managementRole(c))
	authSource := ""
	if principal != nil {
		authSource = strings.TrimSpace(principal.AuthSource)
	}
	event := alerting.ManagementEvent{
		Action:            action,
		Username:          managementUsername(c),
		Role:              role,
		AuthSource:        authSource,
		APIKey:            strings.TrimSpace(apiKey),
		APIKeyName:        "",
		GroupID:           "",
		GroupName:         "",
		CustomGroup:       false,
		DailyBudgetUSD:    0,
		WeeklyBudgetUSD:   0,
		TokenPackageUSD:   0,
		TokenPackageStart: "",
	}
	if policyEntry != nil {
		event.APIKeyName = strings.TrimSpace(policyEntry.Name)
		event.GroupID = strings.TrimSpace(policyEntry.GroupID)
		event.TokenPackageUSD = policyEntry.TokenPackageUSD
		event.TokenPackageStart = strings.TrimSpace(policyEntry.TokenPackageStartedAt)
	}
	if group != nil {
		event.GroupID = group.ID
		event.GroupName = group.Name
		event.CustomGroup = !group.IsSystem
		event.DailyBudgetUSD = microToBillingUSD(group.DailyBudgetMicroUSD)
		event.WeeklyBudgetUSD = microToBillingUSD(group.WeeklyBudgetMicroUSD)
	}
	alerting.NotifyManagementEvent(event)
}

func (h *Handler) resolveNotificationGroup(ctx context.Context, policyEntry *config.APIKeyPolicy) (*apikeygroup.Group, error) {
	if policyEntry == nil || h == nil || h.groupStore == nil {
		return nil, nil
	}
	groupID := strings.TrimSpace(policyEntry.GroupID)
	if groupID == "" {
		return nil, nil
	}
	group, ok, err := h.groupStore.GetGroup(ctx, groupID)
	if err != nil || !ok {
		return nil, err
	}
	return &group, nil
}

func totalsFromRows(rows []billing.DailyUsageRow) apiKeyUsageTotals {
	totals := apiKeyUsageTotals{}
	for _, row := range rows {
		totals.Requests += row.Requests
		totals.FailedRequests += row.FailedRequests
		totals.InputTokens += row.InputTokens
		totals.OutputTokens += row.OutputTokens
		totals.ReasoningTokens += row.ReasoningTokens
		totals.CachedTokens += row.CachedTokens
		totals.TotalTokens += row.TotalTokens
		totals.CostMicroUSD += row.CostMicroUSD
	}
	totals.CostUSD = microUSDToUSD(totals.CostMicroUSD)
	return totals
}

func aggregateUsageEventsByModel(events []billing.UsageEventRow) []billing.DailyUsageRow {
	byModel := make(map[string]billing.DailyUsageRow)
	for _, event := range events {
		key := strings.TrimSpace(event.Model)
		row := byModel[key]
		row.APIKey = strings.TrimSpace(event.APIKey)
		row.Model = event.Model
		row.Requests++
		if event.Failed {
			row.FailedRequests++
		}
		row.InputTokens += event.InputTokens
		row.OutputTokens += event.OutputTokens
		row.ReasoningTokens += event.ReasoningTokens
		row.CachedTokens += event.CachedTokens
		row.TotalTokens += event.TotalTokens
		row.CostMicroUSD += event.CostMicroUSD
		byModel[key] = row
	}

	rows := make([]billing.DailyUsageRow, 0, len(byModel))
	for _, row := range byModel {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Model < rows[j].Model
	})
	return rows
}

func buildModelUsageViews(rows []billing.DailyUsageRow) []apiKeyModelUsageView {
	byModel := make(map[string]apiKeyModelUsageView)
	for _, row := range rows {
		item := byModel[row.Model]
		item.Model = row.Model
		item.Requests += row.Requests
		item.FailedRequests += row.FailedRequests
		item.InputTokens += row.InputTokens
		item.OutputTokens += row.OutputTokens
		item.ReasoningTokens += row.ReasoningTokens
		item.CachedTokens += row.CachedTokens
		item.TotalTokens += row.TotalTokens
		item.CostUSD = round6(item.CostUSD + microUSDToUSD(row.CostMicroUSD))
		byModel[row.Model] = item
	}
	result := make([]apiKeyModelUsageView, 0, len(byModel))
	for _, item := range byModel {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CostUSD == result[j].CostUSD {
			return result[i].Model < result[j].Model
		}
		return result[i].CostUSD > result[j].CostUSD
	})
	return result
}

func buildBudgetWindowView(label string, enabled bool, limitUSD float64, usedMicro int64, startAt, endAt time.Time) apiKeyBudgetWindowView {
	usedUSD := microUSDToUSD(usedMicro)
	remainingUSD := limitUSD - usedUSD
	if remainingUSD < 0 {
		remainingUSD = 0
	}
	usedPercent := 0.0
	if limitUSD > 0 {
		usedPercent = (usedUSD / limitUSD) * 100
		if usedPercent > 100 {
			usedPercent = 100
		}
	}
	return apiKeyBudgetWindowView{
		Enabled:      enabled,
		Label:        label,
		LimitUSD:     round6(limitUSD),
		UsedUSD:      round6(usedUSD),
		RemainingUSD: round6(remainingUSD),
		UsedPercent:  round6(usedPercent),
		StartAt:      startAt,
		EndAt:        endAt,
	}
}

func parseRangeDays(raw string) int {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "7d":
		return 7
	case "30d":
		return 30
	case "60d":
		return 60
	case "90d":
		return 90
	default:
		return 14
	}
}

func parsePositiveInt(raw string, fallback int) int {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fallback
	}
	value := 0
	for _, r := range trimmed {
		if r < '0' || r > '9' {
			return fallback
		}
		value = value*10 + int(r-'0')
	}
	if value <= 0 {
		return fallback
	}
	return value
}

func microUSDToUSD(value int64) float64 {
	return round6(float64(value) / 1_000_000)
}

func round6(value float64) float64 {
	return math.Round(value*1_000_000) / 1_000_000
}

func sumDailyRows(rows []billing.DailyUsageRow, pick func(billing.DailyUsageRow) int64) int64 {
	var total int64
	for _, row := range rows {
		total += pick(row)
	}
	return total
}
