package management

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/apikeygroup"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/billing"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/policy"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

func TestPostgresManagement_GroupCRUDAndRecordBudgets(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, cleanup := newPostgresManagementTestHandler(t, &config.Config{
		SDKConfig: sdkconfig.SDKConfig{APIKeys: []string{"k1"}},
		APIKeyPolicies: []config.APIKeyPolicy{
			{
				APIKey:               "k1",
				GroupID:              "team-alpha",
				TokenPackageUSD:      30,
				WeeklyBudgetAnchorAt: "2026-03-31T00:00:00+08:00",
				DailyLimits: map[string]int{
					"gpt-5.4": 100,
				},
			},
		},
	})
	defer cleanup()

	createRec := performJSONRequest(t, http.MethodPost, "/v0/management/api-key-groups", map[string]any{
		"id":                "team-alpha",
		"name":              "Team Alpha",
		"daily_budget_usd":  45.5,
		"weekly_budget_usd": 120.75,
	}, handler.CreateAPIKeyGroup, nil)
	if createRec.Code != http.StatusOK {
		t.Fatalf("CreateAPIKeyGroup status = %d, body = %s", createRec.Code, createRec.Body.String())
	}

	listRec := performJSONRequest(t, http.MethodGet, "/v0/management/api-key-groups", nil, handler.ListAPIKeyGroups, nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("ListAPIKeyGroups status = %d, body = %s", listRec.Code, listRec.Body.String())
	}
	var listBody struct {
		Items []apiKeyGroupView `json:"items"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("unmarshal groups: %v", err)
	}
	if len(listBody.Items) < len(apikeygroup.DefaultGroups)+1 {
		t.Fatalf("groups len = %d, want at least %d", len(listBody.Items), len(apikeygroup.DefaultGroups)+1)
	}

	updateRec := performJSONRequest(t, http.MethodPut, "/v0/management/api-key-groups/team-alpha", map[string]any{
		"name":              "Team Alpha Pro",
		"daily_budget_usd":  60.0,
		"weekly_budget_usd": 180.0,
	}, handler.UpdateAPIKeyGroup, gin.Params{{Key: "id", Value: "team-alpha"}})
	if updateRec.Code != http.StatusOK {
		t.Fatalf("UpdateAPIKeyGroup status = %d, body = %s", updateRec.Code, updateRec.Body.String())
	}

	resolved, group, err := handler.resolvePolicyWithGroup(context.Background(), &handler.cfg.APIKeyPolicies[0])
	if err != nil {
		t.Fatalf("resolvePolicyWithGroup: %v", err)
	}
	if group == nil || group.Name != "Team Alpha Pro" {
		t.Fatalf("group = %+v, want Team Alpha Pro", group)
	}
	if resolved == nil || resolved.DailyBudgetUSD != 60 || resolved.WeeklyBudgetUSD != 180 {
		t.Fatalf("resolved policy = %+v, want group budgets applied", resolved)
	}

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
		t.Fatalf("record items len = %d, want 1", len(items))
	}
	item := items[0]
	if item.GroupID != "team-alpha" || item.GroupName != "Team Alpha Pro" {
		t.Fatalf("record group = %s/%s, want team-alpha/Team Alpha Pro", item.GroupID, item.GroupName)
	}
	if !item.DailyBudget.Enabled || item.DailyBudget.LimitUSD != 60 {
		t.Fatalf("daily budget = %+v, want 60", item.DailyBudget)
	}
	if !item.WeeklyBudget.Enabled || item.WeeklyBudget.LimitUSD != 180 {
		t.Fatalf("weekly budget = %+v, want 180", item.WeeklyBudget)
	}
	if item.Today.CostUSD <= 0 || item.CurrentPeriod.CostUSD <= item.Today.CostUSD {
		t.Fatalf("usage totals = today %v current %v", item.Today.CostUSD, item.CurrentPeriod.CostUSD)
	}
	if item.DailyLimitCount != 1 {
		t.Fatalf("daily limit count = %d, want 1", item.DailyLimitCount)
	}

	deleteUsedRec := performJSONRequest(t, http.MethodDelete, "/v0/management/api-key-groups/team-alpha", nil, handler.DeleteAPIKeyGroup, gin.Params{{Key: "id", Value: "team-alpha"}})
	if deleteUsedRec.Code != http.StatusBadRequest || !strings.Contains(deleteUsedRec.Body.String(), "still used") {
		t.Fatalf("DeleteAPIKeyGroup(used) status=%d body=%s", deleteUsedRec.Code, deleteUsedRec.Body.String())
	}

	handler.cfg.APIKeyPolicies[0].GroupID = ""
	deleteRec := performJSONRequest(t, http.MethodDelete, "/v0/management/api-key-groups/team-alpha", nil, handler.DeleteAPIKeyGroup, gin.Params{{Key: "id", Value: "team-alpha"}})
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("DeleteAPIKeyGroup status = %d, body = %s", deleteRec.Code, deleteRec.Body.String())
	}
}

func TestPostgresManagement_UsageStatisticsAndModelPrices(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, cleanup := newPostgresManagementTestHandler(t, &config.Config{
		SDKConfig: sdkconfig.SDKConfig{APIKeys: []string{"k1"}},
	})
	defer cleanup()

	requestedAt := time.Date(2026, 4, 2, 9, 30, 0, 0, policy.ChinaLocation())
	dayKey := policy.DayKeyChina(requestedAt)
	if err := handler.billingStore.AddUsage(context.Background(), "k1", "gpt-5.4", dayKey, billing.DailyUsageRow{
		APIKey:       "k1",
		Model:        "gpt-5.4",
		Day:          dayKey,
		Requests:     1,
		InputTokens:  600,
		OutputTokens: 600,
		TotalTokens:  1200,
		CostMicroUSD: 0,
		UpdatedAt:    requestedAt.Unix(),
	}); err != nil {
		t.Fatalf("AddUsage: %v", err)
	}

	putPriceRec := performJSONRequest(t, http.MethodPut, "/v0/management/model-prices", map[string]any{
		"model":                 "gpt-5.4",
		"prompt_usd_per_1m":     2.0,
		"completion_usd_per_1m": 4.0,
		"cached_usd_per_1m":     1.0,
	}, handler.PutModelPrice, nil)
	if putPriceRec.Code != http.StatusOK {
		t.Fatalf("PutModelPrice status = %d, body = %s", putPriceRec.Code, putPriceRec.Body.String())
	}

	getPricesRec := performJSONRequest(t, http.MethodGet, "/v0/management/model-prices", nil, handler.GetModelPrices, nil)
	if getPricesRec.Code != http.StatusOK {
		t.Fatalf("GetModelPrices status = %d, body = %s", getPricesRec.Code, getPricesRec.Body.String())
	}
	var pricesBody struct {
		Prices []billing.ModelPrice `json:"prices"`
	}
	if err := json.Unmarshal(getPricesRec.Body.Bytes(), &pricesBody); err != nil {
		t.Fatalf("unmarshal prices: %v", err)
	}
	if len(pricesBody.Prices) == 0 {
		t.Fatal("expected saved model prices")
	}

	getDailyUsageRec := performJSONRequest(t, http.MethodGet, "/v0/management/api-key-usage?api-key=k1&day="+dayKey, nil, handler.GetAPIKeyDailyUsage, nil)
	if getDailyUsageRec.Code != http.StatusOK {
		t.Fatalf("GetAPIKeyDailyUsage status = %d, body = %s", getDailyUsageRec.Code, getDailyUsageRec.Body.String())
	}
	var usageBody struct {
		Usage billing.DailyUsageReport `json:"usage"`
	}
	if err := json.Unmarshal(getDailyUsageRec.Body.Bytes(), &usageBody); err != nil {
		t.Fatalf("unmarshal daily usage: %v", err)
	}
	if usageBody.Usage.TotalCostUSD <= 0 {
		t.Fatalf("daily usage total cost = %v, want > 0", usageBody.Usage.TotalCostUSD)
	}
	if usageBody.Usage.TotalRequests != 1 {
		t.Fatalf("daily usage total requests = %d, want 1", usageBody.Usage.TotalRequests)
	}

	getStatsRec := performJSONRequest(t, http.MethodGet, "/v0/management/usage-statistics", nil, handler.GetUsageStatistics, nil)
	if getStatsRec.Code != http.StatusOK {
		t.Fatalf("GetUsageStatistics status = %d, body = %s", getStatsRec.Code, getStatsRec.Body.String())
	}
	var statsBody struct {
		Usage struct {
			TotalRequests int64 `json:"total_requests"`
			FailureCount  int64 `json:"failure_count"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(getStatsRec.Body.Bytes(), &statsBody); err != nil {
		t.Fatalf("unmarshal usage statistics: %v", err)
	}
	if statsBody.Usage.TotalRequests != 1 {
		t.Fatalf("usage total requests = %d, want 1", statsBody.Usage.TotalRequests)
	}
	if statsBody.Usage.FailureCount != 0 {
		t.Fatalf("usage failure count = %d, want 0", statsBody.Usage.FailureCount)
	}
}

func newPostgresManagementTestHandler(t *testing.T, cfg *config.Config) (*Handler, func()) {
	t.Helper()

	dsn, schema, dropSchema := newPostgresTestSchema(t)
	ctx := context.Background()

	billingStore, err := billing.NewPostgresStore(ctx, billing.PostgresStoreConfig{
		DSN:    dsn,
		Schema: schema,
	})
	if err != nil {
		dropSchema()
		t.Fatalf("NewPostgresStore: %v", err)
	}
	limiter, err := policy.NewPostgresDailyLimiter(ctx, policy.PostgresDailyLimiterConfig{
		DSN:    dsn,
		Schema: schema,
	})
	if err != nil {
		_ = billingStore.Close()
		dropSchema()
		t.Fatalf("NewPostgresDailyLimiter: %v", err)
	}
	groupStore, err := apikeygroup.NewPostgresStore(ctx, apikeygroup.PostgresStoreConfig{
		DSN:    dsn,
		Schema: schema,
	})
	if err != nil {
		_ = limiter.Close()
		_ = billingStore.Close()
		dropSchema()
		t.Fatalf("NewPostgresGroupStore: %v", err)
	}
	if err := groupStore.SeedDefaults(ctx); err != nil {
		_ = groupStore.Close()
		_ = limiter.Close()
		_ = billingStore.Close()
		dropSchema()
		t.Fatalf("SeedDefaults: %v", err)
	}

	handler := &Handler{
		cfg:          cfg,
		billingStore: billingStore,
		dailyLimiter: limiter,
		groupStore:   groupStore,
	}
	cleanup := func() {
		_ = groupStore.Close()
		_ = limiter.Close()
		_ = billingStore.Close()
		dropSchema()
	}
	return handler, cleanup
}

func newPostgresTestSchema(t *testing.T) (dsn string, schema string, dropSchema func()) {
	t.Helper()

	dsn = strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set")
	}
	schema = fmt.Sprintf("test_%d_%s", time.Now().UnixNano(), sanitizePostgresIdentifier(t.Name()))

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		t.Fatalf("ping postgres: %v", err)
	}

	dropSchema = func() {
		_, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + quotePostgresIdentifier(schema) + ` CASCADE`)
		_ = db.Close()
	}
	return dsn, schema, dropSchema
}

func performJSONRequest(
	t *testing.T,
	method string,
	target string,
	body any,
	handlerFunc func(*gin.Context),
	params gin.Params,
) *httptest.ResponseRecorder {
	t.Helper()

	var reqBody *bytes.Reader
	if body == nil {
		reqBody = bytes.NewReader(nil)
	} else {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reqBody = bytes.NewReader(payload)
	}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(method, target, reqBody)
	if body != nil {
		ctx.Request.Header.Set("Content-Type", "application/json")
	}
	ctx.Params = params

	handlerFunc(ctx)
	return rec
}

func sanitizePostgresIdentifier(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "test"
	}
	var builder strings.Builder
	for _, ch := range value {
		valid := (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')
		if valid {
			builder.WriteRune(ch)
			continue
		}
		builder.WriteByte('_')
	}
	return strings.Trim(builder.String(), "_")
}

func quotePostgresIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
