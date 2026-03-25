package billing

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/policy"
	internalusage "github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

func TestSQLiteStore_ModelPrices_DefaultAndOverride(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "billing.sqlite")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	model := "claude-opus-4-5-20251101"

	price, source, _, err := store.ResolvePriceMicro(ctx, model)
	if err != nil {
		t.Fatalf("ResolvePriceMicro: %v", err)
	}
	if source != "saved" {
		t.Fatalf("source=%q", source)
	}
	if price.Prompt == 0 || price.Completion == 0 {
		t.Fatalf("unexpected default price: %+v", price)
	}

	override := PriceMicroUSDPer1M{Prompt: 1, Completion: 2, Cached: 3}
	if err := store.UpsertModelPrice(ctx, model, override); err != nil {
		t.Fatalf("UpsertModelPrice: %v", err)
	}
	price2, source2, _, err := store.ResolvePriceMicro(ctx, model)
	if err != nil {
		t.Fatalf("ResolvePriceMicro(override): %v", err)
	}
	if source2 != "saved" {
		t.Fatalf("source=%q", source2)
	}
	if price2 != override {
		t.Fatalf("price=%+v want=%+v", price2, override)
	}
}

func TestSQLiteStore_ModelPrices_DefaultCoverage(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "billing.sqlite")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	cases := []struct {
		model string
		want  PriceMicroUSDPer1M
	}{
		{
			model: "claude-opus-4-6",
			want:  PriceMicroUSDPer1M{Prompt: 5_000_000, Completion: 25_000_000, Cached: 500_000},
		},
		{
			model: "claude-sonnet-4-6",
			want:  PriceMicroUSDPer1M{Prompt: 3_000_000, Completion: 15_000_000, Cached: 300_000},
		},
		{
			model: "claude-haiku-4-5-20251001",
			want:  PriceMicroUSDPer1M{Prompt: 1_000_000, Completion: 5_000_000, Cached: 100_000},
		},
		{
			model: "gpt-5.4(high)",
			want:  PriceMicroUSDPer1M{Prompt: 2_500_000, Completion: 15_000_000, Cached: 250_000},
		},
	}

	for _, tc := range cases {
		price, source, _, err := store.ResolvePriceMicro(ctx, tc.model)
		if err != nil {
			t.Fatalf("ResolvePriceMicro(%q): %v", tc.model, err)
		}
		if source != "saved" {
			t.Fatalf("ResolvePriceMicro(%q) source=%q", tc.model, source)
		}
		if price != tc.want {
			t.Fatalf("ResolvePriceMicro(%q) price=%+v want=%+v", tc.model, price, tc.want)
		}
	}
}

func TestSQLiteStore_DefaultPricesSeededToDatabase(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "billing.sqlite")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	prices, err := store.ListModelPrices(ctx)
	if err != nil {
		t.Fatalf("ListModelPrices: %v", err)
	}
	if len(prices) < len(DefaultPrices) {
		t.Fatalf("prices=%d want_at_least=%d", len(prices), len(DefaultPrices))
	}

	found := false
	for _, item := range prices {
		if item.Model == policy.NormaliseModelKey("gpt-5.4") {
			found = true
			if item.Source != "saved" {
				t.Fatalf("source=%q", item.Source)
			}
		}
	}
	if !found {
		t.Fatalf("missing seeded price for gpt-5.4")
	}
}

func TestSQLiteStore_BuildHistoricalUsageSnapshot(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "billing.sqlite")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.AddUsage(ctx, "k1", "claude-opus-4-6", "2026-03-06", DailyUsageRow{
		Requests:        10,
		FailedRequests:  2,
		InputTokens:     1_000,
		OutputTokens:    500,
		ReasoningTokens: 200,
		CachedTokens:    100,
		TotalTokens:     1_800,
	}); err != nil {
		t.Fatalf("AddUsage: %v", err)
	}
	if err := store.AddUsage(ctx, "k1", "claude-opus-4-6", "2026-03-09", DailyUsageRow{
		Requests:    1,
		TotalTokens: 10,
	}); err != nil {
		t.Fatalf("AddUsage(today): %v", err)
	}

	snapshot, err := store.BuildHistoricalUsageSnapshot(ctx, "2026-03-09")
	if err != nil {
		t.Fatalf("BuildHistoricalUsageSnapshot: %v", err)
	}

	if snapshot.TotalRequests != 10 || snapshot.SuccessCount != 8 || snapshot.FailureCount != 2 {
		t.Fatalf("snapshot totals=%+v", snapshot)
	}
	if snapshot.RequestsByDay["2026-03-06"] != 10 {
		t.Fatalf("requests_by_day=%v", snapshot.RequestsByDay)
	}
	if snapshot.RequestsByDay["2026-03-09"] != 0 {
		t.Fatalf("today should be excluded: %v", snapshot.RequestsByDay)
	}

	apiSnapshot, ok := snapshot.APIs["k1"]
	if !ok {
		t.Fatalf("missing api snapshot")
	}
	modelSnapshot, ok := apiSnapshot.Models[policy.NormaliseModelKey("claude-opus-4-6")]
	if !ok {
		t.Fatalf("missing model snapshot")
	}
	if len(modelSnapshot.Details) != 1 {
		t.Fatalf("details=%d", len(modelSnapshot.Details))
	}
	if modelSnapshot.Details[0].RequestCount != 10 || modelSnapshot.Details[0].SuccessCount != 8 || modelSnapshot.Details[0].FailureCount != 2 {
		t.Fatalf("detail=%+v", modelSnapshot.Details[0])
	}
}

func TestSQLiteStore_AddUsageAndDailyCost(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "billing.sqlite")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	apiKey := "k"
	model := "claude-opus-4-5-20251101"
	modelKey := policy.NormaliseModelKey(model)
	day := "2026-02-13"

	if err := store.UpsertModelPrice(ctx, model, PriceMicroUSDPer1M{Prompt: 1_000_000, Completion: 0, Cached: 0}); err != nil {
		t.Fatalf("UpsertModelPrice: %v", err)
	}

	// 2 tokens @ $1 / 1M => 2 micro-USD
	if err := store.AddUsage(ctx, apiKey, modelKey, day, DailyUsageRow{
		Requests:     1,
		InputTokens:  2,
		TotalTokens:  2,
		CostMicroUSD: 2,
	}); err != nil {
		t.Fatalf("AddUsage: %v", err)
	}
	cost, err := store.GetDailyCostMicroUSD(ctx, apiKey, day)
	if err != nil {
		t.Fatalf("GetDailyCostMicroUSD: %v", err)
	}
	if cost != 2 {
		t.Fatalf("cost=%d", cost)
	}

	report, err := store.GetDailyUsageReport(ctx, apiKey, day)
	if err != nil {
		t.Fatalf("GetDailyUsageReport: %v", err)
	}
	if report.TotalCostMicro != 2 || report.TotalRequests != 1 || report.TotalTokens != 2 {
		t.Fatalf("report=%+v", report)
	}
	if len(report.Models) != 1 {
		t.Fatalf("models=%d", len(report.Models))
	}
}

func TestUsagePersistPlugin_PersistsWhenRequestContextCancelled(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "billing.sqlite")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	plugin := NewUsagePersistPlugin(store)
	store.SetPendingUsageProvider(plugin)
	requestedAt := time.Date(2026, 3, 16, 12, 0, 0, 0, time.UTC)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	plugin.HandleUsage(ctx, coreusage.Record{
		Provider:    "codex",
		Model:       "gpt-5.4",
		APIKey:      "client-key",
		AuthIndex:   "auth-1",
		Source:      "client-key",
		RequestedAt: requestedAt,
		Detail: coreusage.Detail{
			InputTokens:  18,
			OutputTokens: 5,
			TotalTokens:  23,
		},
	})
	if err := plugin.flushPending(context.Background()); err != nil {
		t.Fatalf("flushPending: %v", err)
	}

	report, err := store.GetDailyUsageReport(context.Background(), "client-key", policy.DayKeyChina(requestedAt))
	if err != nil {
		t.Fatalf("GetDailyUsageReport: %v", err)
	}
	if report.TotalRequests != 1 {
		t.Fatalf("TotalRequests=%d want=1", report.TotalRequests)
	}
	if report.TotalTokens != 23 {
		t.Fatalf("TotalTokens=%d want=23", report.TotalTokens)
	}

	events, err := store.ListUsageEvents(context.Background(), requestedAt.Add(-time.Minute), requestedAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("ListUsageEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("usage events=%d want=1", len(events))
	}
	if events[0].APIKey != "client-key" {
		t.Fatalf("event api_key=%q want=client-key", events[0].APIKey)
	}
	if events[0].Model != policy.NormaliseModelKey("gpt-5.4") {
		t.Fatalf("event model=%q", events[0].Model)
	}
}

func TestUsagePersistPlugin_PendingUsageVisibleBeforeFlush(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "billing.sqlite")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	plugin := NewUsagePersistPlugin(store)
	store.SetPendingUsageProvider(plugin)
	defer store.Close()

	requestedAt := time.Date(2026, 3, 16, 13, 0, 0, 0, time.UTC)
	plugin.HandleUsage(context.Background(), coreusage.Record{
		Provider:    "codex",
		Model:       "gpt-5.4",
		APIKey:      "client-key",
		AuthIndex:   "auth-1",
		Source:      "client-key",
		RequestedAt: requestedAt,
		Detail: coreusage.Detail{
			InputTokens:  20,
			OutputTokens: 10,
			TotalTokens:  30,
		},
	})

	dayKey := policy.DayKeyChina(requestedAt)
	cost, err := store.GetDailyCostMicroUSD(context.Background(), "client-key", dayKey)
	if err != nil {
		t.Fatalf("GetDailyCostMicroUSD: %v", err)
	}
	if cost <= 0 {
		t.Fatalf("pending daily cost=%d want>0", cost)
	}

	report, err := store.GetDailyUsageReport(context.Background(), "client-key", dayKey)
	if err != nil {
		t.Fatalf("GetDailyUsageReport: %v", err)
	}
	if report.TotalRequests != 1 {
		t.Fatalf("report total_requests=%d want=1", report.TotalRequests)
	}
	if report.TotalTokens != 30 {
		t.Fatalf("report total_tokens=%d want=30", report.TotalTokens)
	}

	snapshot, err := store.BuildUsageStatisticsSnapshot(context.Background())
	if err != nil {
		t.Fatalf("BuildUsageStatisticsSnapshot: %v", err)
	}
	if snapshot.TotalRequests != 1 {
		t.Fatalf("snapshot total_requests=%d want=1", snapshot.TotalRequests)
	}
	if snapshot.TotalTokens != 30 {
		t.Fatalf("snapshot total_tokens=%d want=30", snapshot.TotalTokens)
	}
}

func TestSQLiteStore_CloseFlushesPendingUsage(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "billing.sqlite")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	plugin := NewUsagePersistPlugin(store)
	store.SetPendingUsageProvider(plugin)

	requestedAt := time.Date(2026, 3, 16, 14, 0, 0, 0, time.UTC)
	plugin.HandleUsage(context.Background(), coreusage.Record{
		Provider:    "codex",
		Model:       "gpt-5.4",
		APIKey:      "client-key",
		AuthIndex:   "auth-1",
		Source:      "client-key",
		RequestedAt: requestedAt,
		Detail: coreusage.Detail{
			InputTokens:  11,
			OutputTokens: 7,
			TotalTokens:  18,
		},
	})

	if err := store.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	reopened, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen): %v", err)
	}
	defer reopened.Close()

	dayKey := policy.DayKeyChina(requestedAt)
	report, err := reopened.GetDailyUsageReport(context.Background(), "client-key", dayKey)
	if err != nil {
		t.Fatalf("GetDailyUsageReport: %v", err)
	}
	if report.TotalRequests != 1 {
		t.Fatalf("TotalRequests=%d want=1", report.TotalRequests)
	}
	if report.TotalTokens != 18 {
		t.Fatalf("TotalTokens=%d want=18", report.TotalTokens)
	}
}

func TestSQLiteStore_GetCostMicroUSDByDayRange(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "billing.sqlite")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	apiKey := "k"
	model := policy.NormaliseModelKey("claude-opus-4-5-20251101")

	rows := []struct {
		day  string
		cost int64
	}{
		{day: "2026-03-09", cost: 120_000_000},
		{day: "2026-03-12", cost: 80_000_000},
		{day: "2026-03-16", cost: 50_000_000},
	}
	for _, row := range rows {
		if err := store.AddUsage(ctx, apiKey, model, row.day, DailyUsageRow{
			Requests:     1,
			TotalTokens:  1,
			CostMicroUSD: row.cost,
		}); err != nil {
			t.Fatalf("AddUsage(%s): %v", row.day, err)
		}
	}

	total, err := store.GetCostMicroUSDByDayRange(ctx, apiKey, "2026-03-09", "2026-03-16")
	if err != nil {
		t.Fatalf("GetCostMicroUSDByDayRange: %v", err)
	}
	if total != 200_000_000 {
		t.Fatalf("total=%d", total)
	}
}

func TestSQLiteStore_GetCostMicroUSDByTimeRange(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "billing.sqlite")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.AddUsageEvent(ctx, UsageEventRow{
		RequestedAt:  time.Date(2026, 3, 15, 10, 5, 0, 0, policy.ChinaLocation()).Unix(),
		APIKey:       "k",
		Source:       "openai",
		AuthIndex:    "0",
		Model:        "gpt-5.4",
		InputTokens:  10,
		OutputTokens: 2,
		TotalTokens:  12,
		CostMicroUSD: 111,
	}); err != nil {
		t.Fatalf("AddUsageEvent#1: %v", err)
	}
	if err := store.AddUsageEvent(ctx, UsageEventRow{
		RequestedAt:  time.Date(2026, 3, 16, 9, 55, 0, 0, policy.ChinaLocation()).Unix(),
		APIKey:       "k",
		Source:       "openai",
		AuthIndex:    "0",
		Model:        "gpt-5.4",
		InputTokens:  10,
		OutputTokens: 2,
		TotalTokens:  12,
		CostMicroUSD: 222,
	}); err != nil {
		t.Fatalf("AddUsageEvent#2: %v", err)
	}
	if err := store.AddUsageEvent(ctx, UsageEventRow{
		RequestedAt:  time.Date(2026, 3, 16, 10, 5, 0, 0, policy.ChinaLocation()).Unix(),
		APIKey:       "k",
		Source:       "openai",
		AuthIndex:    "0",
		Model:        "gpt-5.4",
		InputTokens:  10,
		OutputTokens: 2,
		TotalTokens:  12,
		CostMicroUSD: 333,
	}); err != nil {
		t.Fatalf("AddUsageEvent#3: %v", err)
	}

	start := time.Date(2026, 3, 15, 10, 0, 0, 0, policy.ChinaLocation())
	end := time.Date(2026, 3, 16, 10, 0, 0, 0, policy.ChinaLocation())
	total, err := store.GetCostMicroUSDByTimeRange(ctx, "k", start, end)
	if err != nil {
		t.Fatalf("GetCostMicroUSDByTimeRange: %v", err)
	}
	if total != 333 {
		t.Fatalf("total=%d", total)
	}
}

func TestSQLiteStore_BuildUsageStatisticsSnapshot_DBFirst(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "billing.sqlite")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	if err := store.AddUsageEvent(ctx, UsageEventRow{
		RequestedAt:  time.Date(2026, 3, 13, 10, 30, 0, 0, policy.ChinaLocation()).Unix(),
		APIKey:       "k1",
		Source:       "openai",
		AuthIndex:    "0",
		Model:        "gpt-5.4",
		InputTokens:  100,
		OutputTokens: 50,
		TotalTokens:  150,
		CostMicroUSD: 12,
	}); err != nil {
		t.Fatalf("AddUsageEvent: %v", err)
	}
	if err := store.AddUsage(ctx, "k1", "gpt-5.4", "2026-03-13", DailyUsageRow{
		Requests:     1,
		InputTokens:  100,
		OutputTokens: 50,
		TotalTokens:  150,
		CostMicroUSD: 12,
	}); err != nil {
		t.Fatalf("AddUsage(today): %v", err)
	}
	if err := store.AddUsage(ctx, "k1", "gpt-5.4", "2026-03-10", DailyUsageRow{
		Requests:        3,
		FailedRequests:  1,
		InputTokens:     300,
		OutputTokens:    120,
		ReasoningTokens: 30,
		TotalTokens:     450,
		CostMicroUSD:    40,
	}); err != nil {
		t.Fatalf("AddUsage(legacy): %v", err)
	}

	snapshot, err := store.BuildUsageStatisticsSnapshot(ctx)
	if err != nil {
		t.Fatalf("BuildUsageStatisticsSnapshot: %v", err)
	}
	if snapshot.TotalRequests != 4 || snapshot.FailureCount != 1 || snapshot.TotalTokens != 600 {
		t.Fatalf("snapshot totals=%+v", snapshot)
	}
	if snapshot.RequestsByDay["2026-03-13"] != 1 {
		t.Fatalf("today requests=%v", snapshot.RequestsByDay)
	}
	if snapshot.RequestsByDay["2026-03-10"] != 3 {
		t.Fatalf("legacy requests=%v", snapshot.RequestsByDay)
	}
	modelSnapshot := snapshot.APIs["k1"].Models[policy.NormaliseModelKey("gpt-5.4")]
	if len(modelSnapshot.Details) != 2 {
		t.Fatalf("details=%d", len(modelSnapshot.Details))
	}
}

func TestSQLiteStore_ImportUsageStatisticsSnapshot_Deduplicates(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "billing.sqlite")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	snapshot := internalusage.StatisticsSnapshot{
		APIs:          map[string]internalusage.APISnapshot{},
		RequestsByDay: map[string]int64{},
		TokensByDay:   map[string]int64{},
	}
	ts := time.Date(2026, 3, 13, 10, 30, 0, 0, policy.ChinaLocation())
	snapshot.APIs["k1"] = internalusage.APISnapshot{
		Models: map[string]internalusage.ModelSnapshot{
			policy.NormaliseModelKey("gpt-5.4"): {
				Details: []internalusage.RequestDetail{
					{
						Timestamp: ts,
						Source:    "openai",
						AuthIndex: "0",
						Tokens: internalusage.TokenStats{
							InputTokens:  100,
							OutputTokens: 50,
							TotalTokens:  150,
						},
					},
				},
			},
		},
	}

	result, err := store.ImportUsageStatisticsSnapshot(ctx, snapshot)
	if err != nil {
		t.Fatalf("ImportUsageStatisticsSnapshot: %v", err)
	}
	if result.Added != 1 || result.Skipped != 0 {
		t.Fatalf("result=%+v", result)
	}

	result, err = store.ImportUsageStatisticsSnapshot(ctx, snapshot)
	if err != nil {
		t.Fatalf("ImportUsageStatisticsSnapshot second: %v", err)
	}
	if result.Added != 0 || result.Skipped != 1 {
		t.Fatalf("result2=%+v", result)
	}

	events, err := store.ListUsageEvents(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ListUsageEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events=%d", len(events))
	}
	report, err := store.GetDailyUsageReport(ctx, "k1", "2026-03-13")
	if err != nil {
		t.Fatalf("GetDailyUsageReport: %v", err)
	}
	if report.TotalRequests != 1 || report.TotalTokens != 150 {
		t.Fatalf("report=%+v", report)
	}
}

func TestSQLiteStore_RecalculateHistoricalCostsOnOpen(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "billing.sqlite")

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}

	ctx := context.Background()
	apiKey := "k"
	day := "2026-03-06"
	if err := store.AddUsage(ctx, apiKey, "gpt-5.4", day, DailyUsageRow{
		Requests:     1,
		InputTokens:  1_000_000,
		TotalTokens:  1_000_000,
		CostMicroUSD: 0,
	}); err != nil {
		t.Fatalf("AddUsage: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen): %v", err)
	}
	defer reopened.Close()

	report, err := reopened.GetDailyUsageReport(ctx, apiKey, day)
	if err != nil {
		t.Fatalf("GetDailyUsageReport: %v", err)
	}
	if report.TotalCostMicro != 2_500_000 {
		t.Fatalf("TotalCostMicro=%d want=%d", report.TotalCostMicro, int64(2_500_000))
	}
}

func TestSQLiteStore_UpsertModelPriceRecalculatesHistoricalUsage(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "billing.sqlite")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	apiKey := "k"
	day := "2026-03-06"
	model := "custom-model"
	if err := store.AddUsage(ctx, apiKey, model, day, DailyUsageRow{
		Requests:     1,
		InputTokens:  2,
		TotalTokens:  2,
		CostMicroUSD: 0,
	}); err != nil {
		t.Fatalf("AddUsage: %v", err)
	}

	reportBefore, err := store.GetDailyUsageReport(ctx, apiKey, day)
	if err != nil {
		t.Fatalf("GetDailyUsageReport(before): %v", err)
	}
	if reportBefore.TotalCostMicro != 0 {
		t.Fatalf("TotalCostMicro(before)=%d", reportBefore.TotalCostMicro)
	}

	if err := store.UpsertModelPrice(ctx, model, PriceMicroUSDPer1M{
		Prompt:     1_000_000,
		Completion: 0,
		Cached:     0,
	}); err != nil {
		t.Fatalf("UpsertModelPrice: %v", err)
	}

	reportAfter, err := store.GetDailyUsageReport(ctx, apiKey, day)
	if err != nil {
		t.Fatalf("GetDailyUsageReport(after): %v", err)
	}
	if reportAfter.TotalCostMicro != 2 {
		t.Fatalf("TotalCostMicro(after)=%d want=%d", reportAfter.TotalCostMicro, int64(2))
	}
}

func TestSQLiteStore_ListUsageEventAggregateRows(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "billing.sqlite")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	base := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC).Unix()
	events := []UsageEventRow{
		{RequestedAt: base, APIKey: "k1", Source: "alpha.json", AuthIndex: "11", Model: "gpt-5.4"},
		{RequestedAt: base + 1, APIKey: "k1", Source: "alpha.json", AuthIndex: "11", Model: "gpt-5.4", Failed: true},
		{RequestedAt: base + 2, APIKey: "k2", Source: "beta.json", AuthIndex: "12", Model: "gpt-5.4"},
	}
	for _, event := range events {
		if err := store.AddUsageEvent(ctx, event); err != nil {
			t.Fatalf("AddUsageEvent: %v", err)
		}
	}

	rows, err := store.ListUsageEventAggregateRows(ctx)
	if err != nil {
		t.Fatalf("ListUsageEventAggregateRows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows=%d want=2", len(rows))
	}

	if rows[0].Source != "alpha.json" || rows[0].AuthIndex != "11" || rows[0].SuccessCount != 1 || rows[0].FailureCount != 1 {
		t.Fatalf("rows[0]=%+v", rows[0])
	}
	if rows[1].Source != "beta.json" || rows[1].AuthIndex != "12" || rows[1].SuccessCount != 1 || rows[1].FailureCount != 0 {
		t.Fatalf("rows[1]=%+v", rows[1])
	}
}
