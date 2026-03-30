package management

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/billing"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
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
		Items       []apiKeyRecordDetailView `json:"items"`
		InvalidKeys []string                 `json:"invalid_keys"`
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
	if body.Items[0].Summary.APIKey != "k-valid" {
		t.Fatalf("summary api key = %q, want k-valid", body.Items[0].Summary.APIKey)
	}
}

func newAPIKeyRecordsTestHandler(t *testing.T, cfg *config.Config) (*Handler, func()) {
	t.Helper()

	tempDir := t.TempDir()
	store, err := billing.NewSQLiteStore(filepath.Join(tempDir, "usage.sqlite"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	limiter, err := policy.NewSQLiteDailyLimiter(filepath.Join(tempDir, "limits.sqlite"))
	if err != nil {
		t.Fatalf("NewSQLiteDailyLimiter: %v", err)
	}

	handler := &Handler{cfg: cfg, billingStore: store, dailyLimiter: limiter}
	cleanup := func() {
		_ = store.Close()
		_ = limiter.Close()
	}
	return handler, cleanup
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
