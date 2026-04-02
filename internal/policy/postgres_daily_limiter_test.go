package policy

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresDailyLimiter_Consume_Persists(t *testing.T) {
	dsn, schema, cleanup := newPostgresLimiterTestSchema(t)
	defer cleanup()

	limiter, err := NewPostgresDailyLimiter(context.Background(), PostgresDailyLimiterConfig{
		DSN:    dsn,
		Schema: schema,
	})
	if err != nil {
		t.Fatalf("NewPostgresDailyLimiter: %v", err)
	}
	defer limiter.Close()

	dayKey := DayKeyChina(time.Date(2026, 2, 8, 0, 0, 0, 0, time.UTC))
	ctx := context.Background()

	if count, allowed, err := limiter.Consume(ctx, "k1", "claude-opus-4-6", dayKey, 2); err != nil || !allowed || count != 1 {
		t.Fatalf("consume #1: count=%d allowed=%v err=%v", count, allowed, err)
	}
	if count, allowed, err := limiter.Consume(ctx, "k1", "claude-opus-4-6", dayKey, 2); err != nil || !allowed || count != 2 {
		t.Fatalf("consume #2: count=%d allowed=%v err=%v", count, allowed, err)
	}
	if _, allowed, err := limiter.Consume(ctx, "k1", "claude-opus-4-6", dayKey, 2); err != nil || allowed {
		t.Fatalf("consume #3: allowed=%v err=%v", allowed, err)
	}

	if err := limiter.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	limiter, err = NewPostgresDailyLimiter(context.Background(), PostgresDailyLimiterConfig{
		DSN:    dsn,
		Schema: schema,
	})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer limiter.Close()

	if _, allowed, err := limiter.Consume(ctx, "k1", "claude-opus-4-6", dayKey, 2); err != nil || allowed {
		t.Fatalf("consume after reopen: allowed=%v err=%v", allowed, err)
	}
}

func TestWeekBoundsChina_StartsOnMonday(t *testing.T) {
	cases := []struct {
		name      string
		now       time.Time
		wantStart string
		wantEnd   string
	}{
		{
			name:      "wednesday in china week",
			now:       time.Date(2026, 3, 11, 12, 30, 0, 0, time.UTC),
			wantStart: "2026-03-09 00:00:00",
			wantEnd:   "2026-03-16 00:00:00",
		},
		{
			name:      "sunday maps to prior monday",
			now:       time.Date(2026, 3, 15, 14, 0, 0, 0, ChinaLocation()),
			wantStart: "2026-03-09 00:00:00",
			wantEnd:   "2026-03-16 00:00:00",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, end := WeekBoundsChina(tc.now)
			if got := start.In(ChinaLocation()).Format("2006-01-02 15:04:05"); got != tc.wantStart {
				t.Fatalf("start=%s want=%s", got, tc.wantStart)
			}
			if got := end.In(ChinaLocation()).Format("2006-01-02 15:04:05"); got != tc.wantEnd {
				t.Fatalf("end=%s want=%s", got, tc.wantEnd)
			}
		})
	}
}

func TestAnchoredWindowBounds_UsesAnchorHour(t *testing.T) {
	anchor, ok := ParseHourlyAnchorRFC3339("2026-03-15T10:37:00+08:00")
	if !ok {
		t.Fatal("expected anchor to parse")
	}
	start, end := AnchoredWindowBounds(anchor, time.Date(2026, 3, 18, 12, 0, 0, 0, ChinaLocation()), 7*24*time.Hour)
	if got := start.Format(time.RFC3339); got != "2026-03-15T10:00:00+08:00" {
		t.Fatalf("start=%s", got)
	}
	if got := end.Format(time.RFC3339); got != "2026-03-22T10:00:00+08:00" {
		t.Fatalf("end=%s", got)
	}
}

func newPostgresLimiterTestSchema(t *testing.T) (dsn string, schema string, cleanup func()) {
	t.Helper()

	dsn = strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set")
	}
	schema = fmt.Sprintf("test_%d_%s", time.Now().UnixNano(), sanitizePostgresTestIdentifier(t.Name()))

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		t.Fatalf("ping postgres: %v", err)
	}

	cleanup = func() {
		_, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + quotePostgresTestIdentifier(schema) + ` CASCADE`)
		_ = db.Close()
	}
	return dsn, schema, cleanup
}

func sanitizePostgresTestIdentifier(value string) string {
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

func quotePostgresTestIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
