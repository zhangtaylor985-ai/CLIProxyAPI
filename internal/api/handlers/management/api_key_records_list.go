package management

import (
	"context"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/apikeygroup"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/policy"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
)

// apiKeyRecordSummaryLiteView is the trimmed summary used by the paginated
// list endpoint. It intentionally omits any usage or budget fields so the
// handler can build an entire page without touching the billing store more
// than twice (groups + batched last_used_at lookup).
type apiKeyRecordSummaryLiteView struct {
	APIKey             string     `json:"api_key"`
	MaskedAPIKey       string     `json:"masked_api_key"`
	CreatedAt          string     `json:"created_at"`
	ExpiresAt          string     `json:"expires_at"`
	Disabled           bool       `json:"disabled"`
	GroupID            string     `json:"group_id"`
	GroupName          string     `json:"group_name"`
	Registered         bool       `json:"registered"`
	HasExplicitPolicy  bool       `json:"has_explicit_policy"`
	LastUsedAt         *time.Time `json:"last_used_at,omitempty"`
	DailyLimitCount    int        `json:"daily_limit_count"`
	PolicyFamily       string     `json:"policy_family"`
	EnableClaudeModels bool       `json:"enable_claude_models"`
	FastMode           bool       `json:"fast_mode"`
	Expired            bool       `json:"expired"`
}

type apiKeyRecordListPagination struct {
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
	Total      int `json:"total"`
	TotalPages int `json:"total_pages"`
}

type apiKeyRecordListResponse struct {
	Items      []apiKeyRecordSummaryLiteView `json:"items"`
	Pagination apiKeyRecordListPagination    `json:"pagination"`
}

// apiKeyRecordStatsRequest is the request body for the batch stats endpoint.
// Clients typically pass one page worth of api keys (<= 50).
type apiKeyRecordStatsRequest struct {
	APIKeys []string `json:"api_keys"`
	Range   string   `json:"range"`
}

type apiKeyRecordStatsItem struct {
	APIKey        string                 `json:"api_key"`
	Today         apiKeyUsageTotals      `json:"today"`
	CurrentPeriod apiKeyUsageTotals      `json:"current_period"`
	DailyBudget   apiKeyBudgetWindowView `json:"daily_budget"`
	WeeklyBudget  apiKeyBudgetWindowView `json:"weekly_budget"`
	TokenPackage  apiKeyTokenPackageView `json:"token_package"`
}

type apiKeyRecordStatsResponse struct {
	Items []apiKeyRecordStatsItem `json:"items"`
}

const (
	apiKeyRecordListDefaultPageSize = 20
	apiKeyRecordListMaxPageSize     = 100
	apiKeyRecordStatsMaxKeys        = 50
)

type apiKeyRecordListParams struct {
	Page     int
	PageSize int
	Search   string
	Status   string
	GroupID  string
	Sort     string
	Order    string
}

func parseAPIKeyRecordListParams(c *gin.Context) apiKeyRecordListParams {
	pageSize := parsePositiveInt(c.DefaultQuery("page_size", "20"), apiKeyRecordListDefaultPageSize)
	if pageSize > apiKeyRecordListMaxPageSize {
		pageSize = apiKeyRecordListMaxPageSize
	}

	status := strings.ToLower(strings.TrimSpace(c.DefaultQuery("status", "all")))
	switch status {
	case "active", "disabled", "expired":
	default:
		status = "all"
	}

	sortKey := strings.ToLower(strings.TrimSpace(c.DefaultQuery("sort", "last_used")))
	switch sortKey {
	case "last_used", "created", "expires", "api_key":
	default:
		sortKey = "last_used"
	}

	order := strings.ToLower(strings.TrimSpace(c.DefaultQuery("order", "desc")))
	if order != "asc" {
		order = "desc"
	}

	return apiKeyRecordListParams{
		Page:     parsePositiveInt(c.DefaultQuery("page", "1"), 1),
		PageSize: pageSize,
		Search:   strings.ToLower(strings.TrimSpace(c.Query("search"))),
		Status:   status,
		GroupID:  strings.TrimSpace(c.Query("group_id")),
		Sort:     sortKey,
		Order:    order,
	}
}

// ListAPIKeyRecordsLite replaces the legacy ListAPIKeyRecords handler. It
// returns a lightweight, paginated list that only requires cheap in-memory
// work plus a single batch last_used_at lookup, making the list endpoint
// safe to call for configs with thousands of api keys.
func (h *Handler) ListAPIKeyRecordsLite(c *gin.Context) {
	if !h.billingStoreAvailable(c) {
		return
	}

	params := parseAPIKeyRecordListParams(c)
	now := time.Now().In(policy.ChinaLocation())
	items, err := h.buildAPIKeyRecordSummariesLite(c.Request.Context(), now)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	filtered := filterAPIKeyRecordsLite(items, params)
	sortAPIKeyRecordsLite(filtered, params)

	total := len(filtered)
	totalPages := 0
	if params.PageSize > 0 {
		totalPages = int(math.Ceil(float64(total) / float64(params.PageSize)))
	}
	page := params.Page
	if totalPages > 0 && page > totalPages {
		page = totalPages
	}
	if page <= 0 {
		page = 1
	}

	start := (page - 1) * params.PageSize
	if start < 0 || start > total {
		start = total
	}
	end := start + params.PageSize
	if end > total {
		end = total
	}
	pageItems := make([]apiKeyRecordSummaryLiteView, 0, end-start)
	if end > start {
		pageItems = append(pageItems, filtered[start:end]...)
	}

	c.JSON(http.StatusOK, apiKeyRecordListResponse{
		Items: pageItems,
		Pagination: apiKeyRecordListPagination{
			Page:       page,
			PageSize:   params.PageSize,
			Total:      total,
			TotalPages: totalPages,
		},
	})
}

// StatsAPIKeyRecords returns the expensive billing/budget snapshots for the
// provided api keys. The frontend calls this once per page render with the
// current page worth of keys after rendering the lightweight list.
func (h *Handler) StatsAPIKeyRecords(c *gin.Context) {
	if !h.billingStoreAvailable(c) {
		return
	}
	var body apiKeyRecordStatsRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	keys := normalizeUniqueAPIKeys(body.APIKeys)
	if len(keys) == 0 {
		c.JSON(http.StatusOK, apiKeyRecordStatsResponse{Items: []apiKeyRecordStatsItem{}})
		return
	}
	if len(keys) > apiKeyRecordStatsMaxKeys {
		c.JSON(http.StatusBadRequest, gin.H{"error": "too many api keys"})
		return
	}

	now := time.Now().In(policy.ChinaLocation())
	_ = parseRangeDays(body.Range) // range retained for API parity; stats only need current day/period windows
	result := make([]apiKeyRecordStatsItem, 0, len(keys))
	for _, apiKey := range keys {
		if !h.apiKeyExists(apiKey) && (h.cfg == nil || h.cfg.FindAPIKeyPolicy(apiKey) == nil) {
			continue
		}
		item, err := h.buildAPIKeyRecordStats(c.Request.Context(), apiKey, now)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		result = append(result, item)
	}
	c.JSON(http.StatusOK, apiKeyRecordStatsResponse{Items: result})
}

func (h *Handler) buildAPIKeyRecordStats(ctx context.Context, apiKey string, now time.Time) (apiKeyRecordStatsItem, error) {
	todayTotals, err := h.loadDayTotals(ctx, apiKey, policy.DayKeyChina(now))
	if err != nil {
		return apiKeyRecordStatsItem{}, err
	}
	currentPeriodRows, err := h.loadCurrentPeriodRows(ctx, apiKey, now)
	if err != nil {
		return apiKeyRecordStatsItem{}, err
	}
	effectivePolicy, _, err := h.resolvePolicyWithGroup(ctx, h.cfg.EffectiveAPIKeyPolicy(apiKey))
	if err != nil {
		return apiKeyRecordStatsItem{}, err
	}
	dailyBudget, err := h.buildDailyBudgetWindow(ctx, apiKey, now, effectivePolicy)
	if err != nil {
		return apiKeyRecordStatsItem{}, err
	}
	weeklyBudget, err := h.buildWeeklyBudgetWindow(ctx, apiKey, now, effectivePolicy)
	if err != nil {
		return apiKeyRecordStatsItem{}, err
	}
	tokenPackage, err := h.buildTokenPackageView(ctx, apiKey, now, effectivePolicy)
	if err != nil {
		return apiKeyRecordStatsItem{}, err
	}
	return apiKeyRecordStatsItem{
		APIKey:        apiKey,
		Today:         todayTotals,
		CurrentPeriod: totalsFromRows(currentPeriodRows),
		DailyBudget:   dailyBudget,
		WeeklyBudget:  weeklyBudget,
		TokenPackage:  tokenPackage,
	}, nil
}

// buildAPIKeyRecordSummariesLite walks all known api keys and produces lite
// summary rows using cheap in-memory config data plus a single batch call to
// the billing store for last_used_at timestamps and a single groups fetch.
func (h *Handler) buildAPIKeyRecordSummariesLite(ctx context.Context, now time.Time) ([]apiKeyRecordSummaryLiteView, error) {
	if h == nil || h.cfg == nil {
		return nil, nil
	}
	keys := collectKnownAPIKeys(h.cfg)
	groups, err := h.loadGroupsByID(ctx)
	if err != nil {
		return nil, err
	}

	lastUsed := map[string]time.Time{}
	if len(keys) > 0 && h.billingStore != nil {
		lastUsed, err = h.billingStore.GetLatestUsageEventTimesBatch(ctx, keys)
		if err != nil {
			return nil, err
		}
	}

	result := make([]apiKeyRecordSummaryLiteView, 0, len(keys))
	for _, apiKey := range keys {
		view := apiKeyRecordSummaryLiteView{
			APIKey:       apiKey,
			MaskedAPIKey: util.HideAPIKey(apiKey),
			Registered:   h.apiKeyExists(apiKey),
		}
		explicit := h.cfg.FindAPIKeyPolicy(apiKey)
		view.HasExplicitPolicy = explicit != nil
		effective := h.cfg.EffectiveAPIKeyPolicy(apiKey)
		if effective != nil {
			view.CreatedAt = strings.TrimSpace(effective.CreatedAt)
			view.ExpiresAt = strings.TrimSpace(effective.ExpiresAt)
			view.Disabled = effective.Disabled
			view.GroupID = strings.TrimSpace(effective.GroupID)
			view.DailyLimitCount = len(effective.DailyLimits)
			view.PolicyFamily = effective.ClaudeGPTTargetFamilyOrDefault()
			view.EnableClaudeModels = effective.ClaudeModelsEnabled()
			view.FastMode = effective.FastModeEnabled()
		}
		if view.GroupID != "" {
			if grp, ok := groups[view.GroupID]; ok {
				view.GroupName = grp.Name
			}
		}
		if ts, ok := lastUsed[apiKey]; ok {
			local := ts.In(policy.ChinaLocation())
			view.LastUsedAt = &local
		}
		view.Expired = isExpiryBefore(view.ExpiresAt, now)
		result = append(result, view)
	}
	return result, nil
}

// loadGroupsByID caches group lookups for the duration of a single list
// request. Returns an empty map when the group store is unavailable so
// callers do not need to nil-check.
func (h *Handler) loadGroupsByID(ctx context.Context) (map[string]apikeygroup.Group, error) {
	result := map[string]apikeygroup.Group{}
	if h == nil || h.groupStore == nil {
		return result, nil
	}
	groups, err := h.groupStore.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	for _, group := range groups {
		result[group.ID] = group
	}
	return result, nil
}

func isExpiryBefore(expiresAt string, now time.Time) bool {
	trimmed := strings.TrimSpace(expiresAt)
	if trimmed == "" {
		return false
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return false
	}
	return !parsed.After(now)
}

func filterAPIKeyRecordsLite(items []apiKeyRecordSummaryLiteView, params apiKeyRecordListParams) []apiKeyRecordSummaryLiteView {
	search := params.Search
	groupID := params.GroupID
	status := params.Status
	result := make([]apiKeyRecordSummaryLiteView, 0, len(items))
	for _, item := range items {
		if search != "" {
			apiKeyLower := strings.ToLower(item.APIKey)
			maskedLower := strings.ToLower(item.MaskedAPIKey)
			if !strings.Contains(apiKeyLower, search) && !strings.Contains(maskedLower, search) {
				continue
			}
		}
		if groupID != "" && strings.TrimSpace(item.GroupID) != groupID {
			continue
		}
		switch status {
		case "active":
			if item.Disabled || item.Expired {
				continue
			}
		case "disabled":
			if !item.Disabled {
				continue
			}
		case "expired":
			if !item.Expired {
				continue
			}
		}
		result = append(result, item)
	}
	return result
}

func sortAPIKeyRecordsLite(items []apiKeyRecordSummaryLiteView, params apiKeyRecordListParams) {
	asc := params.Order == "asc"
	switch params.Sort {
	case "api_key":
		sort.SliceStable(items, func(i, j int) bool {
			if asc {
				return items[i].APIKey < items[j].APIKey
			}
			return items[i].APIKey > items[j].APIKey
		})
	case "created":
		sort.SliceStable(items, func(i, j int) bool {
			a := strings.TrimSpace(items[i].CreatedAt)
			b := strings.TrimSpace(items[j].CreatedAt)
			return compareStringWithEmptyLast(a, b, asc, items[i].APIKey, items[j].APIKey)
		})
	case "expires":
		sort.SliceStable(items, func(i, j int) bool {
			a := strings.TrimSpace(items[i].ExpiresAt)
			b := strings.TrimSpace(items[j].ExpiresAt)
			return compareStringWithEmptyLast(a, b, asc, items[i].APIKey, items[j].APIKey)
		})
	default: // last_used
		sort.SliceStable(items, func(i, j int) bool {
			left := items[i].LastUsedAt
			right := items[j].LastUsedAt
			if left == nil && right == nil {
				if asc {
					return items[i].APIKey < items[j].APIKey
				}
				return items[i].APIKey < items[j].APIKey
			}
			if left == nil {
				return false
			}
			if right == nil {
				return true
			}
			if left.Equal(*right) {
				return items[i].APIKey < items[j].APIKey
			}
			if asc {
				return left.Before(*right)
			}
			return left.After(*right)
		})
	}
}

// compareStringWithEmptyLast sorts non-empty values first when desc, and keeps
// empty values at the bottom so "未使用" / "无过期时间" rows do not dominate the
// first page.
func compareStringWithEmptyLast(a, b string, asc bool, tieA, tieB string) bool {
	if a == "" && b == "" {
		return tieA < tieB
	}
	if a == "" {
		return false
	}
	if b == "" {
		return true
	}
	if a == b {
		return tieA < tieB
	}
	if asc {
		return a < b
	}
	return a > b
}

