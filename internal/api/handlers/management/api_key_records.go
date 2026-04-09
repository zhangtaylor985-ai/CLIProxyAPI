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
	"github.com/router-for-me/CLIProxyAPI/v6/internal/apikeygroup"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/billing"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/policy"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
)

type apiKeyPolicyView struct {
	APIKey                string                     `json:"api_key"`
	CreatedAt             string                     `json:"created_at"`
	ExpiresAt             string                     `json:"expires_at"`
	Disabled              bool                       `json:"disabled"`
	GroupID               string                     `json:"group_id"`
	GroupName             string                     `json:"group_name"`
	AllowClaudeFamily     bool                       `json:"allow_claude_family"`
	AllowGPTFamily        bool                       `json:"allow_gpt_family"`
	FastMode              bool                       `json:"fast_mode"`
	EnableClaudeModels    bool                       `json:"enable_claude_models"`
	ClaudeUsageLimitUSD   float64                    `json:"claude_usage_limit_usd"`
	ClaudeGPTTargetFamily string                     `json:"claude_gpt_target_family"`
	EnableClaudeOpus1M    bool                       `json:"enable_claude_opus_1m"`
	UpstreamBaseURL       string                     `json:"upstream_base_url"`
	ExcludedModels        []string                   `json:"excluded_models"`
	AllowClaudeOpus46     bool                       `json:"allow_claude_opus_46"`
	DailyLimits           map[string]int             `json:"daily_limits"`
	DailyBudgetUSD        float64                    `json:"daily_budget_usd"`
	WeeklyBudgetUSD       float64                    `json:"weekly_budget_usd"`
	WeeklyBudgetAnchorAt  string                     `json:"weekly_budget_anchor_at"`
	TokenPackageUSD       float64                    `json:"token_package_usd"`
	TokenPackageStartedAt string                     `json:"token_package_started_at"`
	ModelRoutingRules     []config.ModelRoutingRule  `json:"model_routing_rules"`
	ClaudeFailoverEnabled bool                       `json:"claude_failover_enabled"`
	ClaudeFailoverTarget  string                     `json:"claude_failover_target"`
	ClaudeFailoverRules   []config.ModelFailoverRule `json:"claude_failover_rules"`
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
	APIKey             string                 `json:"api_key"`
	MaskedAPIKey       string                 `json:"masked_api_key"`
	CreatedAt          string                 `json:"created_at"`
	ExpiresAt          string                 `json:"expires_at"`
	Disabled           bool                   `json:"disabled"`
	GroupID            string                 `json:"group_id"`
	GroupName          string                 `json:"group_name"`
	Registered         bool                   `json:"registered"`
	HasExplicitPolicy  bool                   `json:"has_explicit_policy"`
	LastUsedAt         *time.Time             `json:"last_used_at,omitempty"`
	Today              apiKeyUsageTotals      `json:"today"`
	CurrentPeriod      apiKeyUsageTotals      `json:"current_period"`
	DailyBudget        apiKeyBudgetWindowView `json:"daily_budget"`
	WeeklyBudget       apiKeyBudgetWindowView `json:"weekly_budget"`
	TokenPackage       apiKeyTokenPackageView `json:"token_package"`
	DailyLimitCount    int                    `json:"daily_limit_count"`
	PolicyFamily       string                 `json:"policy_family"`
	EnableClaudeModels bool                   `json:"enable_claude_models"`
	FastMode           bool                   `json:"fast_mode"`
}

type apiKeyRecordDetailView struct {
	Summary         apiKeyRecordSummaryView `json:"summary"`
	ExplicitPolicy  apiKeyPolicyView        `json:"explicit_policy"`
	EffectivePolicy apiKeyPolicyView        `json:"effective_policy"`
	TodayReport     apiKeyUsageTotals       `json:"today_report"`
	CurrentPeriod   apiKeyUsageTotals       `json:"current_period_report"`
	Group           *apiKeyGroupView        `json:"group,omitempty"`
	RecentDays      []apiKeyRecentDayView   `json:"recent_days"`
	ModelUsage      []apiKeyModelUsageView  `json:"model_usage"`
	DailyLimits     []apiKeyDailyLimitView  `json:"daily_limits"`
	RecentEvents    []apiKeyEventView       `json:"recent_events"`
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

func (h *Handler) ListAPIKeyRecords(c *gin.Context) {
	if !h.billingStoreAvailable(c) {
		return
	}
	rangeDays := parseRangeDays(c.DefaultQuery("range", "14d"))
	search := strings.ToLower(strings.TrimSpace(c.Query("search")))

	records, err := h.buildAPIKeyRecordSummaries(c.Request.Context(), time.Now().In(policy.ChinaLocation()), rangeDays)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if search != "" {
		filtered := make([]apiKeyRecordSummaryView, 0, len(records))
		for _, record := range records {
			if strings.Contains(strings.ToLower(record.APIKey), search) || strings.Contains(strings.ToLower(record.MaskedAPIKey), search) {
				filtered = append(filtered, record)
			}
		}
		records = filtered
	}

	c.JSON(http.StatusOK, gin.H{"items": records})
}

func (h *Handler) GetAPIKeyRecord(c *gin.Context) {
	if !h.billingStoreAvailable(c) {
		return
	}
	apiKey, err := url.PathUnescape(strings.TrimSpace(c.Param("apiKey")))
	if err != nil || strings.TrimSpace(apiKey) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid api key"})
		return
	}

	detail, err := h.buildAPIKeyRecordDetail(c.Request.Context(), apiKey, time.Now().In(policy.ChinaLocation()), parseRangeDays(c.DefaultQuery("range", "14d")), parsePositiveInt(c.DefaultQuery("events_limit", "100"), 100))
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
	if !h.apiKeyExists(apiKey) {
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
	if !body.ClearPolicy {
		upsertAPIKeyPolicy(&h.cfg.APIKeyPolicies, viewToPolicy(apiKey, policyView))
	}
	h.cfg.SanitizeAPIKeyPolicies()
	h.persistAPIKeyRecord(c, "", apiKey)
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

	for i, key := range h.cfg.APIKeys {
		if strings.TrimSpace(key) == currentKey {
			h.cfg.APIKeys[i] = newKey
		}
	}
	h.cfg.APIKeys = normalizeUniqueAPIKeys(h.cfg.APIKeys)

	if existing := h.cfg.FindAPIKeyPolicy(currentKey); existing != nil {
		existing.APIKey = newKey
	}
	if body.ClearPolicy {
		removeAPIKeyPolicy(&h.cfg.APIKeyPolicies, newKey)
		removeAPIKeyPolicy(&h.cfg.APIKeyPolicies, currentKey)
	} else if !isEmptyPolicyView(body.Policy) {
		upsertAPIKeyPolicy(&h.cfg.APIKeyPolicies, viewToPolicy(newKey, body.Policy))
		removeDuplicatePolicyAlias(&h.cfg.APIKeyPolicies, currentKey, newKey)
	}
	h.cfg.SanitizeAPIKeyPolicies()
	h.persistAPIKeyRecord(c, currentKey, newKey)
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

	h.cfg.APIKeys = removeAPIKeyValue(h.cfg.APIKeys, apiKey)
	removeAPIKeyPolicy(&h.cfg.APIKeyPolicies, apiKey)
	h.cfg.SanitizeAPIKeyPolicies()
	h.deletePersistedAPIKeyRecord(c, apiKey)
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
	items := make([]apiKeyRecordDetailView, 0, len(keys))
	invalid := make([]string, 0)
	for _, apiKey := range keys {
		if !h.apiKeyExists(apiKey) {
			invalid = append(invalid, apiKey)
			continue
		}
		detail, err := h.buildAPIKeyRecordDetail(c.Request.Context(), apiKey, now, rangeDays, 50)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
	recentEvents, err := h.listAPIKeyEvents(ctx, apiKey, now.AddDate(0, 0, -(rangeDays-1)), time.Time{}, eventsLimit)
	if err != nil {
		return apiKeyRecordDetailView{}, err
	}

	return apiKeyRecordDetailView{
		Summary:         summary,
		ExplicitPolicy:  explicitPolicy,
		EffectivePolicy: effectivePolicy,
		TodayReport:     todayReport,
		CurrentPeriod:   totalsFromRows(periodRows),
		Group:           h.optionalGroupView(effectiveGroup),
		RecentDays:      recentDays,
		ModelUsage:      modelUsage,
		DailyLimits:     dailyLimits,
		RecentEvents:    recentEvents,
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
	lastUsedAt, err := h.loadLastUsedAt(ctx, apiKey)
	if err != nil {
		return apiKeyRecordSummaryView{}, err
	}

	explicitPolicy := h.cfg.FindAPIKeyPolicy(apiKey)
	summary := apiKeyRecordSummaryView{
		APIKey:             apiKey,
		MaskedAPIKey:       util.HideAPIKey(apiKey),
		CreatedAt:          "",
		ExpiresAt:          "",
		Disabled:           false,
		GroupID:            "",
		GroupName:          "",
		Registered:         h.apiKeyExists(apiKey),
		HasExplicitPolicy:  explicitPolicy != nil,
		LastUsedAt:         lastUsedAt,
		Today:              todayTotals,
		CurrentPeriod:      currentPeriodTotals,
		DailyBudget:        dailyBudget,
		WeeklyBudget:       weeklyBudget,
		TokenPackage:       tokenPackage,
		DailyLimitCount:    0,
		PolicyFamily:       "",
		EnableClaudeModels: false,
		FastMode:           false,
	}
	if effectivePolicy != nil {
		summary.CreatedAt = strings.TrimSpace(effectivePolicy.CreatedAt)
		summary.ExpiresAt = strings.TrimSpace(effectivePolicy.ExpiresAt)
		summary.Disabled = effectivePolicy.Disabled
		summary.GroupID = strings.TrimSpace(effectivePolicy.GroupID)
		summary.DailyLimitCount = len(effectivePolicy.DailyLimits)
		summary.PolicyFamily = effectivePolicy.ClaudeGPTTargetFamilyOrDefault()
		summary.EnableClaudeModels = effectivePolicy.ClaudeModelsEnabled()
		summary.FastMode = effectivePolicy.FastModeEnabled()
	}
	if effectiveGroup != nil {
		summary.GroupName = effectiveGroup.Name
	}

	_ = rangeDays
	return summary, nil
}

func (h *Handler) loadCurrentPeriodRows(ctx context.Context, apiKey string, now time.Time) ([]billing.DailyUsageRow, error) {
	start, end := policy.WeekBoundsChina(now)
	effectivePolicy, _, err := h.resolvePolicyWithGroup(ctx, h.cfg.EffectiveAPIKeyPolicy(apiKey))
	if err != nil {
		return nil, err
	}
	if effectivePolicy != nil {
		start, end = effectivePolicy.WeeklyBudgetBounds(now)
	}
	if effectivePolicy != nil && strings.TrimSpace(effectivePolicy.WeeklyBudgetAnchorAt) != "" {
		events, err := h.billingStore.ListUsageEventsByAPIKey(ctx, apiKey, start, end, 0, false)
		if err != nil {
			return nil, err
		}
		return aggregateUsageEventsByModel(events), nil
	}
	rows, err := h.billingStore.ListDailyUsageRowsByAPIKey(ctx, apiKey, policy.DayKeyChina(start), policy.DayKeyChina(end))
	if err != nil {
		return nil, err
	}
	return rows, nil
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
	start, end := policy.WeekBoundsChina(now)
	limitUSD := 0.0
	if p != nil {
		start, end = p.WeeklyBudgetBounds(now)
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
	} else if p != nil && strings.TrimSpace(p.WeeklyBudgetAnchorAt) != "" {
		usedMicro, err = h.billingStore.GetCostMicroUSDByTimeRange(ctx, apiKey, start, end)
	} else {
		usedMicro, err = h.billingStore.GetCostMicroUSDByDayRange(ctx, apiKey, policy.DayKeyChina(start), policy.DayKeyChina(end))
	}
	if err != nil {
		return apiKeyBudgetWindowView{}, err
	}
	return buildBudgetWindowView("Current period", limitUSD > 0, limitUSD, usedMicro, start, end), nil
}

func (h *Handler) buildTokenPackageView(ctx context.Context, apiKey string, now time.Time, p *config.APIKeyPolicy) (apiKeyTokenPackageView, error) {
	if p == nil || !p.TokenPackageEnabled() {
		return apiKeyTokenPackageView{}, nil
	}
	startedAt, ok := p.TokenPackageStartTime()
	if !ok {
		return apiKeyTokenPackageView{}, nil
	}
	if startedAt.After(now) {
		return apiKeyTokenPackageView{
			Enabled:      true,
			StartedAt:    startedAt,
			TotalUSD:     p.TokenPackageUSD,
			RemainingUSD: p.TokenPackageUSD,
		}, nil
	}

	state, err := billing.ComputeBudgetReplayState(ctx, h.billingStore, apiKey, now, p)
	if err != nil {
		return apiKeyTokenPackageView{}, err
	}
	usedUSD := microUSDToUSD(state.PackageUsedMicro)
	remainingUSD := p.TokenPackageUSD - usedUSD
	if remainingUSD < 0 {
		remainingUSD = 0
	}
	return apiKeyTokenPackageView{
		Enabled:      true,
		StartedAt:    startedAt,
		TotalUSD:     p.TokenPackageUSD,
		UsedUSD:      round6(usedUSD),
		RemainingUSD: round6(remainingUSD),
		Active:       remainingUSD > 0,
	}, nil
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
		APIKey:              apiKey,
		CreatedAt:           "",
		ExpiresAt:           "",
		Disabled:            false,
		GroupID:             "",
		GroupName:           "",
		AllowClaudeFamily:   allowClaudeFamily,
		AllowGPTFamily:      allowGPTFamily,
		AllowClaudeOpus46:   true,
		DailyLimits:         map[string]int{},
		ExcludedModels:      extraExcludedModels,
		ModelRoutingRules:   nil,
		ClaudeFailoverRules: nil,
	}
	if p == nil {
		return view
	}
	view.APIKey = strings.TrimSpace(p.APIKey)
	view.CreatedAt = strings.TrimSpace(p.CreatedAt)
	view.ExpiresAt = strings.TrimSpace(p.ExpiresAt)
	view.Disabled = p.Disabled
	view.GroupID = strings.TrimSpace(p.GroupID)
	if group != nil {
		view.GroupName = group.Name
	}
	view.AllowClaudeFamily, view.AllowGPTFamily, view.ExcludedModels = config.ExcludedModelFamilyAccess(p.ExcludedModels)
	view.FastMode = p.FastMode
	view.EnableClaudeModels = p.ClaudeModelsEnabled()
	view.ClaudeUsageLimitUSD = p.ClaudeUsageLimitUSD
	view.ClaudeGPTTargetFamily = p.ClaudeGPTTargetFamily
	view.EnableClaudeOpus1M = p.ClaudeOpus1MEnabled()
	view.UpstreamBaseURL = p.UpstreamBaseURL
	view.AllowClaudeOpus46 = p.AllowsClaudeOpus46()
	view.DailyLimits = copyDailyLimits(p.DailyLimits)
	view.DailyBudgetUSD = p.DailyBudgetUSD
	view.WeeklyBudgetUSD = p.WeeklyBudgetUSD
	view.WeeklyBudgetAnchorAt = p.WeeklyBudgetAnchorAt
	view.TokenPackageUSD = p.TokenPackageUSD
	view.TokenPackageStartedAt = p.TokenPackageStartedAt
	view.ModelRoutingRules = append([]config.ModelRoutingRule(nil), p.ModelRouting.Rules...)
	view.ClaudeFailoverEnabled = p.Failover.Claude.Enabled
	view.ClaudeFailoverTarget = p.Failover.Claude.TargetModel
	view.ClaudeFailoverRules = append([]config.ModelFailoverRule(nil), p.Failover.Claude.Rules...)
	return view
}

func viewToPolicy(apiKey string, view apiKeyPolicyView) config.APIKeyPolicy {
	enableClaudeModels := view.EnableClaudeModels
	enableClaudeOpus1M := view.EnableClaudeOpus1M
	allowClaudeOpus46 := view.AllowClaudeOpus46
	return config.APIKeyPolicy{
		APIKey:                apiKey,
		CreatedAt:             strings.TrimSpace(view.CreatedAt),
		ExpiresAt:             strings.TrimSpace(view.ExpiresAt),
		Disabled:              view.Disabled,
		GroupID:               strings.TrimSpace(view.GroupID),
		FastMode:              view.FastMode,
		EnableClaudeModels:    &enableClaudeModels,
		ClaudeUsageLimitUSD:   view.ClaudeUsageLimitUSD,
		ClaudeGPTTargetFamily: strings.TrimSpace(view.ClaudeGPTTargetFamily),
		EnableClaudeOpus1M:    &enableClaudeOpus1M,
		UpstreamBaseURL:       strings.TrimSpace(view.UpstreamBaseURL),
		ExcludedModels:        config.BuildExcludedModelFamilies(view.AllowClaudeFamily, view.AllowGPTFamily, view.ExcludedModels),
		AllowClaudeOpus46:     &allowClaudeOpus46,
		DailyLimits:           copyDailyLimits(view.DailyLimits),
		DailyBudgetUSD:        view.DailyBudgetUSD,
		WeeklyBudgetUSD:       view.WeeklyBudgetUSD,
		WeeklyBudgetAnchorAt:  strings.TrimSpace(view.WeeklyBudgetAnchorAt),
		TokenPackageUSD:       view.TokenPackageUSD,
		TokenPackageStartedAt: strings.TrimSpace(view.TokenPackageStartedAt),
		ModelRouting: config.APIKeyModelRoutingPolicy{
			Rules: append([]config.ModelRoutingRule(nil), view.ModelRoutingRules...),
		},
		Failover: config.APIKeyFailoverPolicy{
			Claude: config.ProviderFailoverPolicy{
				Enabled:     view.ClaudeFailoverEnabled,
				TargetModel: strings.TrimSpace(view.ClaudeFailoverTarget),
				Rules:       append([]config.ModelFailoverRule(nil), view.ClaudeFailoverRules...),
			},
		},
	}
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
		APIKey:            strings.TrimSpace(apiKey),
		CreatedAt:         now.Format(time.RFC3339),
		ExpiresAt:         now.AddDate(0, 1, 0).Format(time.RFC3339),
		AllowClaudeFamily: true,
		AllowGPTFamily:    false,
		AllowClaudeOpus46: true,
		DailyLimits:       map[string]int{},
	}
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
		strings.TrimSpace(view.CreatedAt) == "" &&
		strings.TrimSpace(view.ExpiresAt) == "" &&
		!view.Disabled &&
		strings.TrimSpace(view.GroupID) == "" &&
		!view.AllowClaudeFamily &&
		!view.AllowGPTFamily &&
		!view.FastMode &&
		!view.EnableClaudeModels &&
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
		len(view.ModelRoutingRules) == 0 &&
		!view.ClaudeFailoverEnabled &&
		strings.TrimSpace(view.ClaudeFailoverTarget) == "" &&
		len(view.ClaudeFailoverRules) == 0
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
