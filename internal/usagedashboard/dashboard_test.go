package usagedashboard

import (
	"testing"
	"time"
)

func TestBuildDashboard_SummaryAndDailyCharts(t *testing.T) {
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, chinaLocation)
	summaryEntries := []AggregateEntry{
		{
			Timestamp:       time.Date(2026, 3, 8, 23, 59, 59, 0, chinaLocation),
			APIKey:          "k1",
			Model:           "gpt-5.4",
			Requests:        10,
			SuccessCount:    8,
			FailureCount:    2,
			ReasoningTokens: 20,
			CachedTokens:    10,
			TotalTokens:     300,
			CostMicroUSD:    2500,
		},
		{
			Timestamp:       time.Date(2026, 3, 9, 23, 59, 59, 0, chinaLocation),
			APIKey:          "k1",
			Model:           "claude-opus-4-6",
			Requests:        5,
			SuccessCount:    5,
			FailureCount:    0,
			ReasoningTokens: 10,
			CachedTokens:    5,
			TotalTokens:     120,
			CostMicroUSD:    366799,
		},
	}

	dashboard := Build(Range(range7D), summaryEntries, nil, now)

	if dashboard.Summary.TotalRequests != 15 {
		t.Fatalf("TotalRequests=%d", dashboard.Summary.TotalRequests)
	}
	if dashboard.Summary.SuccessCount != 13 || dashboard.Summary.FailureCount != 2 {
		t.Fatalf("summary=%+v", dashboard.Summary)
	}
	if dashboard.Summary.TotalTokens != 420 {
		t.Fatalf("TotalTokens=%d", dashboard.Summary.TotalTokens)
	}
	if len(dashboard.ModelNames) != 2 {
		t.Fatalf("ModelNames=%v", dashboard.ModelNames)
	}
	if len(dashboard.ModelStats) != 2 {
		t.Fatalf("ModelStats=%v", dashboard.ModelStats)
	}
	if len(dashboard.APIStats) != 1 {
		t.Fatalf("APIStats=%v", dashboard.APIStats)
	}
	if got := dashboard.Charts.Requests.Day.Labels; len(got) != 2 || got[0] != "2026-03-08" || got[1] != "2026-03-09" {
		t.Fatalf("day labels=%v", got)
	}
	if len(dashboard.Charts.Requests.Day.Series) == 0 || dashboard.Charts.Requests.Day.Series[0].Key != "all" {
		t.Fatalf("day series=%v", dashboard.Charts.Requests.Day.Series)
	}
}

func TestBuildDashboard_HourlyRatesAndSparklines(t *testing.T) {
	now := time.Date(2026, 3, 9, 12, 30, 0, 0, chinaLocation)
	detailEntries := []AggregateEntry{
		{
			Timestamp:    time.Date(2026, 3, 9, 12, 10, 0, 0, chinaLocation),
			APIKey:       "k1",
			Model:        "gpt-5.4",
			Requests:     3,
			SuccessCount: 2,
			FailureCount: 1,
			TotalTokens:  90,
			CostMicroUSD: 900,
		},
		{
			Timestamp:    time.Date(2026, 3, 9, 12, 25, 0, 0, chinaLocation),
			APIKey:       "k1",
			Model:        "claude-opus-4-6",
			Requests:     1,
			SuccessCount: 1,
			FailureCount: 0,
			TotalTokens:  50,
			CostMicroUSD: 500,
		},
	}

	dashboard := Build(Range(range24H), detailEntries, detailEntries, now)

	if dashboard.Rates.RequestCount != 4 || dashboard.Rates.TokenCount != 140 {
		t.Fatalf("rates=%+v", dashboard.Rates)
	}
	if dashboard.Rates.WindowMinute != 30 {
		t.Fatalf("window=%d", dashboard.Rates.WindowMinute)
	}
	if got := dashboard.Charts.Requests.Hour.Labels; len(got) != 24 {
		t.Fatalf("hour labels=%d", len(got))
	}
	if len(dashboard.Sparklines.Cost.Data) != 60 {
		t.Fatalf("cost sparkline=%d", len(dashboard.Sparklines.Cost.Data))
	}
}
