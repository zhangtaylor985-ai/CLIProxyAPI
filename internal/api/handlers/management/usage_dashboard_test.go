package management

import (
	"testing"
	"time"
)

func TestHistoricalDashboardTimestampAt_CurrentDayUsesNow(t *testing.T) {
	now := time.Date(2026, 4, 2, 12, 34, 56, 0, chinaDashboardLocation())

	got := historicalDashboardTimestampAt("2026-04-02", now)

	if !got.Equal(now) {
		t.Fatalf("expected current day timestamp to use now, got %s want %s", got.Format(time.RFC3339), now.Format(time.RFC3339))
	}
}

func TestHistoricalDashboardTimestampAt_PastDayUsesEndOfDay(t *testing.T) {
	now := time.Date(2026, 4, 2, 12, 34, 56, 0, chinaDashboardLocation())

	got := historicalDashboardTimestampAt("2026-04-01", now)
	want := time.Date(2026, 4, 1, 23, 59, 59, 0, chinaDashboardLocation())

	if !got.Equal(want) {
		t.Fatalf("expected past day timestamp to use end of day, got %s want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}
