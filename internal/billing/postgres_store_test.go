package billing

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/policy"
)

func TestPostgresStore_ModelPrices_DefaultAndOverride(t *testing.T) {
	store, cleanup := newPostgresBillingTestStore(t)
	defer cleanup()

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

func TestPostgresStore_AddUsageAndDailyCost(t *testing.T) {
	store, cleanup := newPostgresBillingTestStore(t)
	defer cleanup()

	ctx := context.Background()
	apiKey := "k"
	model := "claude-opus-4-5-20251101"
	modelKey := policy.NormaliseModelKey(model)
	day := "2026-02-13"

	if err := store.UpsertModelPrice(ctx, model, PriceMicroUSDPer1M{Prompt: 1_000_000, Completion: 0, Cached: 0}); err != nil {
		t.Fatalf("UpsertModelPrice: %v", err)
	}
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
		t.Fatalf("cost=%d want=2", cost)
	}
}

func newPostgresBillingTestStore(t *testing.T) (*PostgresStore, func()) {
	t.Helper()

	dsn := strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set")
	}
	schema := fmt.Sprintf("test_%d_%s", time.Now().UnixNano(), sanitizeBillingPostgresIdentifier(t.Name()))

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		t.Fatalf("ping postgres: %v", err)
	}

	store, err := NewPostgresStore(context.Background(), PostgresStoreConfig{
		DSN:    dsn,
		Schema: schema,
	})
	if err != nil {
		_ = db.Close()
		t.Fatalf("NewPostgresStore: %v", err)
	}

	cleanup := func() {
		_ = store.Close()
		_, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + quoteBillingPostgresIdentifier(schema) + ` CASCADE`)
		_ = db.Close()
	}
	return store, cleanup
}

func TestPostgresStore_GetLatestUsageEventTimesBatch(t *testing.T) {
	store, cleanup := newPostgresBillingTestStore(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC).Unix()

	events := []UsageEventRow{
		{APIKey: "k1", Model: "m", Source: "test", RequestedAt: now - 60, TotalTokens: 1, CostMicroUSD: 1},
		{APIKey: "k1", Model: "m", Source: "test", RequestedAt: now, TotalTokens: 1, CostMicroUSD: 1},
		{APIKey: "k2", Model: "m", Source: "test", RequestedAt: now - 30, TotalTokens: 1, CostMicroUSD: 1},
	}
	for _, ev := range events {
		if err := store.AddUsageEvent(ctx, ev); err != nil {
			t.Fatalf("AddUsageEvent: %v", err)
		}
	}

	// Include a duplicate and an unknown key to exercise dedup + missing handling.
	result, err := store.GetLatestUsageEventTimesBatch(ctx, []string{"k1", "k1", "k2", "k3"})
	if err != nil {
		t.Fatalf("GetLatestUsageEventTimesBatch: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("result len = %d, want 2", len(result))
	}
	if got := result["k1"].Unix(); got != now {
		t.Fatalf("k1 latest = %d, want %d", got, now)
	}
	if got := result["k2"].Unix(); got != now-30 {
		t.Fatalf("k2 latest = %d, want %d", got, now-30)
	}
	if _, ok := result["k3"]; ok {
		t.Fatalf("expected k3 to be absent when no events recorded")
	}

	// Empty input must return an empty map, not nil, so callers can index.
	empty, err := store.GetLatestUsageEventTimesBatch(ctx, nil)
	if err != nil {
		t.Fatalf("GetLatestUsageEventTimesBatch(nil): %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty input should yield empty map, got %+v", empty)
	}
}

func sanitizeBillingPostgresIdentifier(value string) string {
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

func quoteBillingPostgresIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
