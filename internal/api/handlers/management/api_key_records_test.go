package management

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/apikeygroup"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/billing"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/managementauth"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/policy"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

func TestListAPIKeyRecordsIncludesUsageAndBudgets(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	handler, cleanup := newAPIKeyRecordsTestHandler(t, &config.Config{
		SDKConfig: sdkconfig.SDKConfig{APIKeys: []string{"k1"}},
		APIKeyPolicies: []config.APIKeyPolicy{
			{
				APIKey:                "k1",
				CreatedAt:             "2026-03-24T00:00:00+08:00",
				DailyBudgetUSD:        10,
				WeeklyBudgetUSD:       20,
				TokenPackageUSD:       30,
				TokenPackageStartedAt: "2026-03-24T00:00:00+08:00",
				DailyLimits: map[string]int{
					"gpt-5.4": 100,
				},
			},
		},
	})
	defer cleanup()

	now := time.Date(2026, 4, 2, 10, 0, 0, 0, policy.ChinaLocation())
	if err := seedUsage(t, handler, "k1", "gpt-5.4", now, 2_500_000, 1200); err != nil {
		t.Fatalf("seedUsage(today): %v", err)
	}
	if err := seedUsage(t, handler, "k1", "gpt-5.4", now.AddDate(0, 0, -2), 5_000_000, 2400); err != nil {
		t.Fatalf("seedUsage(week): %v", err)
	}
	if _, _, err := handler.dailyLimiter.Consume(context.Background(), "k1", "gpt-5.4", policy.DayKeyChina(now), 100); err != nil {
		t.Fatalf("Consume: %v", err)
	}

	items, err := handler.buildAPIKeyRecordSummaries(context.Background(), now, 14)
	if err != nil {
		t.Fatalf("buildAPIKeyRecordSummaries: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1", len(items))
	}
	item := items[0]
	if got := item.Today.CostUSD; got <= 0 {
		t.Fatalf("today cost = %v, want > 0", got)
	}
	if got := item.CurrentPeriod.CostUSD; got <= item.Today.CostUSD {
		t.Fatalf("current period cost = %v, want > today cost %v", got, item.Today.CostUSD)
	}
	if !item.DailyBudget.Enabled || item.DailyBudget.LimitUSD != 10 {
		t.Fatalf("daily budget = %+v", item.DailyBudget)
	}
	if !item.WeeklyBudget.Enabled || item.WeeklyBudget.LimitUSD != 20 {
		t.Fatalf("weekly budget = %+v", item.WeeklyBudget)
	}
	if !item.TokenPackage.Enabled || item.TokenPackage.TotalUSD != 30 {
		t.Fatalf("token package = %+v", item.TokenPackage)
	}
	if item.DailyLimitCount != 1 {
		t.Fatalf("daily limit count = %d, want 1", item.DailyLimitCount)
	}
}

func TestQueryAPIKeyInsightsFiltersInvalidKeys(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	handler, cleanup := newAPIKeyRecordsTestHandler(t, &config.Config{
		SDKConfig: sdkconfig.SDKConfig{APIKeys: []string{"k-valid"}},
		APIKeyPolicies: []config.APIKeyPolicy{
			{
				APIKey:                "k-valid",
				CreatedAt:             "2026-03-24T00:00:00+08:00",
				ClaudeGPTTargetFamily: "gpt-5.5",
				DailyLimits:           map[string]int{"gpt-5.4": 100},
				ModelRouting: config.APIKeyModelRoutingPolicy{
					Rules: []config.ModelRoutingRule{
						{FromModel: "claude-*", TargetModel: "gpt-5.5(high)"},
					},
				},
			},
		},
	})
	defer cleanup()

	now := time.Date(2026, 3, 30, 10, 0, 0, 0, policy.ChinaLocation())
	if err := seedUsage(t, handler, "k-valid", "gpt-5.4", now, 1_250_000, 800); err != nil {
		t.Fatalf("seedUsage: %v", err)
	}

	payload := []byte(`{"api_keys":["k-valid","k-invalid"],"range":"7d"}`)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/v0/api-key-insights/query", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	ctx.Request = req

	handler.QueryAPIKeyInsights(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var body struct {
		Items       []apiKeyInsightDetailView `json:"items"`
		InvalidKeys []string                  `json:"invalid_keys"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("items len = %d, want 1", len(body.Items))
	}
	if len(body.InvalidKeys) != 1 || body.InvalidKeys[0] != "k-invalid" {
		t.Fatalf("invalid keys = %+v, want [k-invalid]", body.InvalidKeys)
	}
	if body.Items[0].Summary.MaskedAPIKey == "" {
		t.Fatalf("summary masked api key is empty")
	}

	response := strings.ToLower(recorder.Body.String())
	for _, forbidden := range []string{
		"gpt",
		"model_usage",
		"explicit_policy",
		"effective_policy",
		"model_routing_rules",
		"daily_limits",
		"recent_events",
		"source",
		"auth_index",
		"id_token",
		"account",
		"email",
		"api_key\":\"k-valid",
	} {
		if strings.Contains(response, forbidden) {
			t.Fatalf("public insights response leaked %q: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestQueryAPIKeyInsightsWithoutBillingStoreReturnsBadRequest(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	handler := &Handler{
		cfg: &config.Config{
			SDKConfig: sdkconfig.SDKConfig{APIKeys: []string{"k-valid"}},
		},
	}

	payload := []byte(`{"api_keys":["k-valid"],"range":"14d"}`)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/v0/api-key-insights/query", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	ctx.Request = req

	handler.QueryAPIKeyInsights(ctx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if body := recorder.Body.String(); body != "{\"error\":\"billing store unavailable\"}" {
		t.Fatalf("body = %s, want billing store unavailable", body)
	}
}

func TestAPIKeyRecordSummary_AnchoredWindowUsesExactPeriodAndIgnoresPreAnchorBudgetReplay(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	handler, cleanup := newAPIKeyRecordsTestHandler(t, &config.Config{
		SDKConfig: sdkconfig.SDKConfig{APIKeys: []string{"k1"}},
		APIKeyPolicies: []config.APIKeyPolicy{
			{
				APIKey:                "k1",
				DailyBudgetUSD:        150,
				WeeklyBudgetUSD:       500,
				WeeklyBudgetAnchorAt:  "2026-04-02T10:00:00+08:00",
				TokenPackageUSD:       50,
				TokenPackageStartedAt: "2026-03-26T16:01:00+08:00",
			},
		},
	})
	defer cleanup()

	now := time.Date(2026, 4, 3, 12, 0, 0, 0, policy.ChinaLocation())
	seedCases := []struct {
		at    time.Time
		cost  int64
		token int64
	}{
		{at: time.Date(2026, 3, 27, 12, 0, 0, 0, policy.ChinaLocation()), cost: 150_000_000, token: 1000},
		{at: time.Date(2026, 3, 28, 12, 0, 0, 0, policy.ChinaLocation()), cost: 150_000_000, token: 1000},
		{at: time.Date(2026, 3, 29, 12, 0, 0, 0, policy.ChinaLocation()), cost: 150_000_000, token: 1000},
		{at: time.Date(2026, 3, 30, 12, 0, 0, 0, policy.ChinaLocation()), cost: 150_000_000, token: 1000},
		{at: time.Date(2026, 4, 2, 8, 0, 0, 0, policy.ChinaLocation()), cost: 20_000_000, token: 1000},
		{at: time.Date(2026, 4, 2, 12, 0, 0, 0, policy.ChinaLocation()), cost: 100_000_000, token: 1000},
	}
	for _, item := range seedCases {
		if err := seedUsage(t, handler, "k1", "gpt-5.4", item.at, item.cost, item.token); err != nil {
			t.Fatalf("seedUsage(%s): %v", item.at.Format(time.RFC3339), err)
		}
	}

	summary, err := handler.buildAPIKeySummary(context.Background(), "k1", now, 14)
	if err != nil {
		t.Fatalf("buildAPIKeySummary: %v", err)
	}
	if got := summary.CurrentPeriod.CostUSD; got != 100 {
		t.Fatalf("current period cost = %v, want 100", got)
	}
	if got := summary.CurrentPeriod.Requests; got != 1 {
		t.Fatalf("current period requests = %d, want 1", got)
	}
	if got := summary.WeeklyBudget.UsedUSD; got != 100 {
		t.Fatalf("weekly budget used = %v, want 100", got)
	}
	if got := summary.WeeklyBudget.UsedPercent; got != 20 {
		t.Fatalf("weekly budget used percent = %v, want 20", got)
	}

	detail, err := handler.buildAPIKeyRecordDetail(context.Background(), "k1", now, 14, 20)
	if err != nil {
		t.Fatalf("buildAPIKeyRecordDetail: %v", err)
	}
	if got := detail.CurrentPeriod.CostUSD; got != 100 {
		t.Fatalf("detail current period cost = %v, want 100", got)
	}
	if got := detail.CurrentPeriod.Requests; got != 1 {
		t.Fatalf("detail current period requests = %d, want 1", got)
	}
	if len(detail.ModelUsage) != 1 || detail.ModelUsage[0].CostUSD != 100 {
		t.Fatalf("model usage = %+v, want one exact current-period model row", detail.ModelUsage)
	}
}

func TestPolicyViewRoundTripUsesFamilyAccessTogglesAndMetadata(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 6, 9, 0, 0, 0, time.UTC)
	expiresAt := now.Add(24 * time.Hour)
	policyEntry := &config.APIKeyPolicy{
		APIKey:                      "k1",
		Name:                        "Test Key",
		Note:                        "Operator note",
		CreatedAt:                   now.Format(time.RFC3339),
		ExpiresAt:                   expiresAt.Format(time.RFC3339),
		Disabled:                    true,
		SessionTrajectoryDisabled:   true,
		CodexChannelMode:            "provider",
		EnableClaudeModels:          boolPtr(true),
		ClaudeGlobalFallbackEnabled: boolPtr(false),
		ExcludedModels:              []string{"claude-*", "gpt-*", "chatgpt-*", "o1*", "o3*", "o4*", "custom-*"},
	}

	view := policyToView("k1", policyEntry, nil)
	if view.AllowClaudeFamily {
		t.Fatal("expected claude family to be denied")
	}
	if view.AllowGPTFamily {
		t.Fatal("expected gpt family to be denied")
	}
	if len(view.ExcludedModels) != 1 || view.ExcludedModels[0] != "custom-*" {
		t.Fatalf("unexpected extra excluded models: %#v", view.ExcludedModels)
	}
	if view.Name != "Test Key" || view.Note != "Operator note" {
		t.Fatalf("expected name/note to round-trip, got %+v", view)
	}
	if !view.Disabled || view.CreatedAt == "" || view.ExpiresAt == "" {
		t.Fatalf("expected metadata to round-trip, got %+v", view)
	}
	if !view.SessionTrajectoryDisabled {
		t.Fatalf("expected session trajectory disabled flag to round-trip, got %+v", view)
	}
	if view.CodexChannelMode != "provider" {
		t.Fatalf("expected codex channel mode to round-trip, got %+v", view)
	}
	if view.ClaudeGlobalFallback {
		t.Fatalf("expected claude global fallback flag to round-trip as disabled, got %+v", view)
	}

	roundTrip := viewToPolicy("k1", view)
	if !roundTrip.Disabled || roundTrip.CreatedAt != policyEntry.CreatedAt || roundTrip.ExpiresAt != policyEntry.ExpiresAt {
		t.Fatalf("unexpected round-trip metadata: %+v", roundTrip)
	}
	if roundTrip.CodexChannelMode != policyEntry.CodexChannelMode {
		t.Fatalf("unexpected codex channel mode: %+v", roundTrip)
	}
	if roundTrip.Name != policyEntry.Name || roundTrip.Note != policyEntry.Note {
		t.Fatalf("unexpected round-trip name/note: %+v", roundTrip)
	}
	if !roundTrip.SessionTrajectoryDisabled {
		t.Fatalf("unexpected round-trip session trajectory flag: %+v", roundTrip)
	}
	if roundTrip.ClaudeGlobalFallbackEnabled == nil || *roundTrip.ClaudeGlobalFallbackEnabled {
		t.Fatalf("unexpected round-trip claude global fallback flag: %+v", roundTrip)
	}
	if got := roundTrip.ExcludedModels; len(got) != 7 {
		t.Fatalf("unexpected round-trip excluded models: %#v", got)
	}
}

func TestViewToPolicyClearsBaseBudgetsButPreservesAnchorWhenGroupBoundAfterSanitize(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		APIKeyPolicies: []config.APIKeyPolicy{
			viewToPolicy("k1", apiKeyPolicyView{
				APIKey:               "k1",
				GroupID:              "triple",
				DailyBudgetUSD:       100,
				WeeklyBudgetUSD:      300,
				WeeklyBudgetAnchorAt: "2026-04-02T10:00:00+08:00",
			}),
		},
	}

	cfg.SanitizeAPIKeyPolicies()

	got := cfg.FindAPIKeyPolicy("k1")
	if got == nil {
		t.Fatal("expected k1 policy")
	}
	if got.GroupID != "triple" {
		t.Fatalf("group id = %q, want triple", got.GroupID)
	}
	if got.DailyBudgetUSD != 0 || got.WeeklyBudgetUSD != 0 {
		t.Fatalf("expected group-bound base budgets cleared, got %+v", got)
	}
	if got.WeeklyBudgetAnchorAt != "2026-04-02T10:00:00+08:00" {
		t.Fatalf("expected normalized anchor to be preserved, got %+v", got)
	}
}

func TestDefaultAPIKeyPolicyViewSetsOneMonthExpiryAndClaudeOnly(t *testing.T) {
	t.Parallel()

	view := defaultAPIKeyPolicyView("k-default")
	if !view.AllowClaudeFamily {
		t.Fatal("expected claude family to be allowed by default")
	}
	if view.AllowGPTFamily {
		t.Fatal("expected gpt family to be denied by default")
	}
	if view.Disabled {
		t.Fatal("expected default api key to be enabled")
	}
	if view.CodexChannelMode != "auto" {
		t.Fatalf("expected default codex channel mode auto, got %+v", view)
	}
	if view.CreatedAt == "" || view.ExpiresAt == "" {
		t.Fatalf("expected created/expires metadata, got %+v", view)
	}

	createdAt, err := time.Parse(time.RFC3339, view.CreatedAt)
	if err != nil {
		t.Fatalf("parse created_at: %v", err)
	}
	expiresAt, err := time.Parse(time.RFC3339, view.ExpiresAt)
	if err != nil {
		t.Fatalf("parse expires_at: %v", err)
	}
	if !expiresAt.Equal(createdAt.AddDate(0, 1, 0)) {
		t.Fatalf("expires_at = %s, want %s", expiresAt.Format(time.RFC3339), createdAt.AddDate(0, 1, 0).Format(time.RFC3339))
	}
}

func TestIsEmptyPolicyViewTreatsZeroValueAsEmpty(t *testing.T) {
	t.Parallel()

	if !isEmptyPolicyView(apiKeyPolicyView{}) {
		t.Fatal("expected zero-value policy view to be treated as empty")
	}
	if isEmptyPolicyView(apiKeyPolicyView{AllowClaudeFamily: true}) {
		t.Fatal("expected non-empty family toggle to prevent empty-policy fallback")
	}
	if isEmptyPolicyView(apiKeyPolicyView{CodexChannelMode: "auto"}) {
		t.Fatal("expected codex channel mode to prevent empty-policy fallback")
	}
	if isEmptyPolicyView(apiKeyPolicyView{Disabled: true}) {
		t.Fatal("expected disabled policy to be treated as non-empty")
	}
	if isEmptyPolicyView(apiKeyPolicyView{SessionTrajectoryDisabled: true}) {
		t.Fatal("expected session trajectory disabled policy to be treated as non-empty")
	}
}

func TestGetAPIKeyRecord_StaffReceivesPolicyOnlyDetailWithoutBillingStore(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	handler, cleanup := newAPIKeyRecordsTestHandler(t, &config.Config{
		SDKConfig: sdkconfig.SDKConfig{APIKeys: []string{"k-staff"}},
		APIKeyPolicies: []config.APIKeyPolicy{
			{
				APIKey:                "k-staff",
				Name:                  "Staff Key",
				Note:                  "Editable by staff",
				GroupID:               "team-alpha",
				DailyBudgetUSD:        12.5,
				WeeklyBudgetUSD:       44.25,
				TokenPackageUSD:       99.99,
				TokenPackageStartedAt: "2026-04-10T08:00:00Z",
			},
		},
	})
	defer cleanup()

	if _, err := handler.groupStore.UpsertGroup(context.Background(), apikeygroup.Group{
		ID:                   "team-alpha",
		Name:                 "Team Alpha",
		DailyBudgetMicroUSD:  12_500_000,
		WeeklyBudgetMicroUSD: 44_250_000,
	}); err != nil {
		t.Fatalf("UpsertGroup: %v", err)
	}

	handler.billingStore = nil

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "apiKey", Value: "k-staff"}}
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/api-key-records/k-staff", nil)
	setManagementPrincipal(ctx, managementPrincipal{
		Username: "user_01",
		Role:     managementauth.RoleStaff,
	})

	handler.GetAPIKeyRecord(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var body apiKeyRecordDetailView
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if body.Summary.Name != "Staff Key" || body.Summary.Note != "Editable by staff" {
		t.Fatalf("unexpected summary metadata: %+v", body.Summary)
	}
	if body.Summary.Today.CostUSD != 0 || body.CurrentPeriod.CostUSD != 0 {
		t.Fatalf("staff detail should not include usage totals: %+v", body)
	}
	if len(body.RecentDays) != 0 || len(body.ModelUsage) != 0 || len(body.RecentEvents) != 0 {
		t.Fatalf("staff detail should omit analytics: %+v", body)
	}
	if !body.Summary.TokenPackage.Enabled || body.Summary.TokenPackage.TotalUSD != 99.99 {
		t.Fatalf("unexpected token package summary: %+v", body.Summary.TokenPackage)
	}
	if body.Group == nil || body.Group.ID != "team-alpha" {
		t.Fatalf("expected resolved group, got %+v", body.Group)
	}
}

func newAPIKeyRecordsTestHandler(t *testing.T, cfg *config.Config) (*Handler, func()) {
	t.Helper()
	return newPostgresManagementTestHandler(t, cfg)
}

func seedUsage(t *testing.T, handler *Handler, apiKey, model string, requestedAt time.Time, costMicroUSD, totalTokens int64) error {
	t.Helper()

	ctx := context.Background()
	dayKey := policy.DayKeyChina(requestedAt)
	delta := billing.DailyUsageRow{
		APIKey:       apiKey,
		Model:        model,
		Day:          dayKey,
		Requests:     1,
		TotalTokens:  totalTokens,
		InputTokens:  totalTokens / 2,
		OutputTokens: totalTokens / 2,
		CostMicroUSD: costMicroUSD,
	}
	if err := handler.billingStore.AddUsage(ctx, apiKey, model, dayKey, delta); err != nil {
		return err
	}
	return handler.billingStore.AddUsageEvent(ctx, billing.UsageEventRow{
		RequestedAt:  requestedAt.Unix(),
		APIKey:       apiKey,
		Model:        model,
		Source:       "test",
		AuthIndex:    "auth:test",
		TotalTokens:  totalTokens,
		InputTokens:  totalTokens / 2,
		OutputTokens: totalTokens / 2,
		CostMicroUSD: costMicroUSD,
	})
}
