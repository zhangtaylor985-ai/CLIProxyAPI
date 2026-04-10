package management

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/policy"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

// runListAPIKeyRecords is a helper that dispatches the list handler with an
// arbitrary query string and returns the parsed response.
func runListAPIKeyRecords(t *testing.T, handler *Handler, rawQuery string) (apiKeyRecordListResponse, int) {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	target := "/v0/management/api-key-records"
	if rawQuery != "" {
		target = target + "?" + rawQuery
	}
	ctx.Request = httptest.NewRequest(http.MethodGet, target, nil)

	handler.ListAPIKeyRecordsLite(ctx)

	var body apiKeyRecordListResponse
	if recorder.Code == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal list body: %v", err)
		}
	}
	return body, recorder.Code
}

func TestListAPIKeyRecordsLite_Pagination(t *testing.T) {
	gin.SetMode(gin.TestMode)

	keys := make([]string, 0, 25)
	policies := make([]config.APIKeyPolicy, 0, 25)
	now := time.Date(2026, 4, 6, 12, 0, 0, 0, policy.ChinaLocation())
	for i := 0; i < 25; i++ {
		apiKey := fmt.Sprintf("k-%02d", i)
		keys = append(keys, apiKey)
		policies = append(policies, config.APIKeyPolicy{
			APIKey:    apiKey,
			CreatedAt: now.Add(-time.Duration(i) * time.Hour).Format(time.RFC3339),
			ExpiresAt: now.AddDate(0, 1, 0).Format(time.RFC3339),
		})
	}

	handler, cleanup := newAPIKeyRecordsTestHandler(t, &config.Config{
		SDKConfig:      sdkconfig.SDKConfig{APIKeys: keys},
		APIKeyPolicies: policies,
	})
	defer cleanup()

	// Default page size is 20; page 1 returns 20 items.
	page1, status := runListAPIKeyRecords(t, handler, "page=1&sort=api_key&order=asc")
	if status != http.StatusOK {
		t.Fatalf("page1 status = %d", status)
	}
	if got := len(page1.Items); got != 20 {
		t.Fatalf("page1 items = %d, want 20", got)
	}
	if page1.Pagination.Total != 25 {
		t.Fatalf("pagination.total = %d, want 25", page1.Pagination.Total)
	}
	if page1.Pagination.TotalPages != 2 {
		t.Fatalf("pagination.total_pages = %d, want 2", page1.Pagination.TotalPages)
	}
	if page1.Items[0].APIKey != "k-00" || page1.Items[19].APIKey != "k-19" {
		t.Fatalf("page1 order = [%s..%s], want [k-00..k-19]", page1.Items[0].APIKey, page1.Items[19].APIKey)
	}

	page2, status := runListAPIKeyRecords(t, handler, "page=2&sort=api_key&order=asc")
	if status != http.StatusOK {
		t.Fatalf("page2 status = %d", status)
	}
	if got := len(page2.Items); got != 5 {
		t.Fatalf("page2 items = %d, want 5", got)
	}
	if page2.Items[0].APIKey != "k-20" || page2.Items[4].APIKey != "k-24" {
		t.Fatalf("page2 order = [%s..%s], want [k-20..k-24]", page2.Items[0].APIKey, page2.Items[4].APIKey)
	}

	// Custom page size of 10 splits into 3 pages.
	custom, _ := runListAPIKeyRecords(t, handler, "page=3&page_size=10&sort=api_key&order=asc")
	if custom.Pagination.TotalPages != 3 {
		t.Fatalf("custom total_pages = %d, want 3", custom.Pagination.TotalPages)
	}
	if len(custom.Items) != 5 {
		t.Fatalf("custom page3 items = %d, want 5", len(custom.Items))
	}
}

func TestListAPIKeyRecordsLite_FiltersStatusAndGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)

	now := time.Date(2026, 4, 6, 12, 0, 0, 0, policy.ChinaLocation())
	past := now.Add(-48 * time.Hour).Format(time.RFC3339)
	future := now.AddDate(0, 0, 30).Format(time.RFC3339)
	policies := []config.APIKeyPolicy{
		{APIKey: "alpha-active", ExpiresAt: future, GroupID: "team-alpha"},
		{APIKey: "alpha-disabled", ExpiresAt: future, Disabled: true, GroupID: "team-alpha"},
		{APIKey: "beta-expired", ExpiresAt: past, GroupID: "team-beta"},
		{APIKey: "beta-active", ExpiresAt: future, GroupID: "team-beta"},
	}
	handler, cleanup := newAPIKeyRecordsTestHandler(t, &config.Config{
		SDKConfig: sdkconfig.SDKConfig{APIKeys: []string{
			"alpha-active", "alpha-disabled", "beta-expired", "beta-active",
		}},
		APIKeyPolicies: policies,
	})
	defer cleanup()

	activeResp, _ := runListAPIKeyRecords(t, handler, "status=active&sort=api_key&order=asc")
	if got := collectAPIKeysFromList(activeResp); len(got) != 2 || got[0] != "alpha-active" || got[1] != "beta-active" {
		t.Fatalf("active filter = %v, want [alpha-active beta-active]", got)
	}

	disabledResp, _ := runListAPIKeyRecords(t, handler, "status=disabled")
	if got := collectAPIKeysFromList(disabledResp); len(got) != 1 || got[0] != "alpha-disabled" {
		t.Fatalf("disabled filter = %v, want [alpha-disabled]", got)
	}

	expiredResp, _ := runListAPIKeyRecords(t, handler, "status=expired")
	if got := collectAPIKeysFromList(expiredResp); len(got) != 1 || got[0] != "beta-expired" {
		t.Fatalf("expired filter = %v, want [beta-expired]", got)
	}

	groupResp, _ := runListAPIKeyRecords(t, handler, "group_id=team-alpha&sort=api_key&order=asc")
	if got := collectAPIKeysFromList(groupResp); len(got) != 2 {
		t.Fatalf("group filter = %v, want 2 items", got)
	}

	searchResp, _ := runListAPIKeyRecords(t, handler, "search="+url.QueryEscape("beta"))
	if got := collectAPIKeysFromList(searchResp); len(got) != 2 {
		t.Fatalf("search beta = %v, want 2 items", got)
	}

	// Combined: beta + active leaves only beta-active.
	comboResp, _ := runListAPIKeyRecords(t, handler, "status=active&group_id=team-beta")
	if got := collectAPIKeysFromList(comboResp); len(got) != 1 || got[0] != "beta-active" {
		t.Fatalf("combo filter = %v, want [beta-active]", got)
	}
}

func TestListAPIKeyRecordsLite_SortByLastUsed(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, cleanup := newAPIKeyRecordsTestHandler(t, &config.Config{
		SDKConfig: sdkconfig.SDKConfig{APIKeys: []string{"k-old", "k-new", "k-unused"}},
		APIKeyPolicies: []config.APIKeyPolicy{
			{APIKey: "k-old"},
			{APIKey: "k-new"},
			{APIKey: "k-unused"},
		},
	})
	defer cleanup()

	now := time.Date(2026, 4, 6, 12, 0, 0, 0, policy.ChinaLocation())
	if err := seedUsage(t, handler, "k-old", "gpt-5.4", now.Add(-72*time.Hour), 1_000, 10); err != nil {
		t.Fatalf("seedUsage(k-old): %v", err)
	}
	if err := seedUsage(t, handler, "k-new", "gpt-5.4", now.Add(-1*time.Hour), 1_000, 10); err != nil {
		t.Fatalf("seedUsage(k-new): %v", err)
	}

	descResp, _ := runListAPIKeyRecords(t, handler, "sort=last_used&order=desc")
	got := collectAPIKeysFromList(descResp)
	if len(got) != 3 || got[0] != "k-new" || got[1] != "k-old" || got[2] != "k-unused" {
		t.Fatalf("desc sort = %v, want [k-new k-old k-unused]", got)
	}

	ascResp, _ := runListAPIKeyRecords(t, handler, "sort=last_used&order=asc")
	gotAsc := collectAPIKeysFromList(ascResp)
	if len(gotAsc) != 3 || gotAsc[0] != "k-old" || gotAsc[1] != "k-new" || gotAsc[2] != "k-unused" {
		t.Fatalf("asc sort = %v, want [k-old k-new k-unused]", gotAsc)
	}
}

func TestStatsAPIKeyRecords_BatchReturnsBudgets(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, cleanup := newAPIKeyRecordsTestHandler(t, &config.Config{
		SDKConfig: sdkconfig.SDKConfig{APIKeys: []string{"s-budget", "s-empty"}},
		APIKeyPolicies: []config.APIKeyPolicy{
			{
				APIKey:          "s-budget",
				DailyBudgetUSD:  10,
				WeeklyBudgetUSD: 20,
			},
			{APIKey: "s-empty"},
		},
	})
	defer cleanup()

	// Exercise the pure builder directly so the test can pin wall-clock time
	// to the seeded day; the HTTP handler itself uses time.Now() and is
	// covered by TestStatsAPIKeyRecords_RejectsTooManyKeys.
	now := time.Date(2026, 4, 6, 10, 0, 0, 0, policy.ChinaLocation())
	if err := seedUsage(t, handler, "s-budget", "gpt-5.4", now, 4_000_000, 1000); err != nil {
		t.Fatalf("seedUsage: %v", err)
	}

	budget, err := handler.buildAPIKeyRecordStats(context.Background(), "s-budget", now)
	if err != nil {
		t.Fatalf("buildAPIKeyRecordStats(s-budget): %v", err)
	}
	if !budget.DailyBudget.Enabled || budget.DailyBudget.LimitUSD != 10 {
		t.Fatalf("daily budget = %+v", budget.DailyBudget)
	}
	if budget.Today.CostUSD <= 0 {
		t.Fatalf("today cost = %v, want > 0", budget.Today.CostUSD)
	}

	empty, err := handler.buildAPIKeyRecordStats(context.Background(), "s-empty", now)
	if err != nil {
		t.Fatalf("buildAPIKeyRecordStats(s-empty): %v", err)
	}
	if empty.DailyBudget.Enabled {
		t.Fatalf("empty key should have disabled daily budget")
	}
}

func TestStatsAPIKeyRecords_RejectsTooManyKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, cleanup := newAPIKeyRecordsTestHandler(t, &config.Config{
		SDKConfig: sdkconfig.SDKConfig{APIKeys: []string{"k1"}},
	})
	defer cleanup()

	keys := make([]string, apiKeyRecordStatsMaxKeys+1)
	for i := range keys {
		keys[i] = fmt.Sprintf("k-%d", i)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	payload, _ := json.Marshal(apiKeyRecordStatsRequest{APIKeys: keys})
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v0/management/api-key-records/stats", bytes.NewReader(payload))
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.StatsAPIKeyRecords(ctx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func collectAPIKeysFromList(resp apiKeyRecordListResponse) []string {
	result := make([]string, 0, len(resp.Items))
	for _, item := range resp.Items {
		result = append(result, item.APIKey)
	}
	return result
}
