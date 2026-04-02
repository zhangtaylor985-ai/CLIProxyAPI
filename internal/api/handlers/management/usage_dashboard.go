package management

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/billing"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/policy"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usagedashboard"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
)

func (h *Handler) GetUsageDashboard(c *gin.Context) {
	rangeKey, err := usagedashboard.ParseRange(c.Query("range"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	dashboard, err := h.buildUsageDashboard(c.Request.Context(), rangeKey, time.Now().In(chinaDashboardLocation()))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, dashboard)
}

func (h *Handler) buildUsageDashboard(
	ctx context.Context,
	rangeKey usagedashboard.Range,
	now time.Time,
) (usagedashboard.Dashboard, error) {
	if h == nil || h.billingStore == nil {
		return usagedashboard.Dashboard{}, nil
	}

	detailStart, detailEnd := dashboardEventRange(rangeKey, now)
	eventRows, err := h.billingStore.ListUsageEvents(ctx, detailStart, detailEnd)
	if err != nil {
		return usagedashboard.Dashboard{}, err
	}
	detailEntries := dashboardEntriesFromUsageEvents(eventRows)

	summaryEntries := detailEntries
	if rangeKey == usagedashboard.Range("7d") || rangeKey == usagedashboard.Range("all") {
		startDay, endDay := dashboardDayRange(rangeKey, now)
		rows, err := h.billingStore.ListDailyUsageRows(ctx, startDay, endDay)
		if err != nil {
			return usagedashboard.Dashboard{}, err
		}
		summaryEntries = dashboardEntriesFromDailyRows(rows)
	}

	return usagedashboard.Build(rangeKey, summaryEntries, detailEntries, now), nil
}

func dashboardEntriesFromDailyRows(rows []billing.DailyUsageRow) []usagedashboard.AggregateEntry {
	entries := make([]usagedashboard.AggregateEntry, 0, len(rows))
	for _, row := range rows {
		successCount := row.Requests - row.FailedRequests
		if successCount < 0 {
			successCount = 0
		}
		entries = append(entries, usagedashboard.AggregateEntry{
			Timestamp:       historicalDashboardTimestamp(row.Day),
			APIKey:          maskDashboardAPIKey(row.APIKey),
			Source:          "",
			AuthIndex:       "",
			Model:           row.Model,
			Requests:        row.Requests,
			SuccessCount:    successCount,
			FailureCount:    maxDashboardInt64(0, row.FailedRequests),
			InputTokens:     maxDashboardInt64(0, row.InputTokens),
			OutputTokens:    maxDashboardInt64(0, row.OutputTokens),
			ReasoningTokens: maxDashboardInt64(0, row.ReasoningTokens),
			CachedTokens:    maxDashboardInt64(0, row.CachedTokens),
			TotalTokens:     maxDashboardInt64(0, row.TotalTokens),
			CostMicroUSD:    maxDashboardInt64(0, row.CostMicroUSD),
		})
	}
	return entries
}

func dashboardEntriesFromUsageEvents(rows []billing.UsageEventRow) []usagedashboard.AggregateEntry {
	entries := make([]usagedashboard.AggregateEntry, 0, len(rows))
	for _, row := range rows {
		successCount := int64(1)
		failureCount := int64(0)
		if row.Failed {
			successCount = 0
			failureCount = 1
		}
		entries = append(entries, usagedashboard.AggregateEntry{
			Timestamp:       time.Unix(row.RequestedAt, 0).In(chinaDashboardLocation()),
			APIKey:          maskDashboardAPIKey(row.APIKey),
			Source:          strings.TrimSpace(row.Source),
			AuthIndex:       strings.TrimSpace(row.AuthIndex),
			Model:           row.Model,
			Requests:        1,
			SuccessCount:    successCount,
			FailureCount:    failureCount,
			InputTokens:     maxDashboardInt64(0, row.InputTokens),
			OutputTokens:    maxDashboardInt64(0, row.OutputTokens),
			ReasoningTokens: maxDashboardInt64(0, row.ReasoningTokens),
			CachedTokens:    maxDashboardInt64(0, row.CachedTokens),
			TotalTokens:     maxDashboardInt64(0, row.TotalTokens),
			CostMicroUSD:    maxDashboardInt64(0, row.CostMicroUSD),
		})
	}
	return entries
}

func filterDashboardEntriesByRange(
	entries []usagedashboard.AggregateEntry,
	rangeKey usagedashboard.Range,
	now time.Time,
) []usagedashboard.AggregateEntry {
	if rangeKey == usagedashboard.Range("all") {
		return entries
	}

	var duration time.Duration
	switch rangeKey {
	case usagedashboard.Range("7h"):
		duration = 7 * time.Hour
	case usagedashboard.Range("24h"):
		duration = 24 * time.Hour
	case usagedashboard.Range("7d"):
		duration = 7 * 24 * time.Hour
	default:
		duration = 24 * time.Hour
	}

	windowStart := now.Add(-duration)
	filtered := make([]usagedashboard.AggregateEntry, 0, len(entries))
	for _, entry := range entries {
		ts := entry.Timestamp.In(chinaDashboardLocation())
		if ts.Before(windowStart) || ts.After(now) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func dashboardDayRange(rangeKey usagedashboard.Range, now time.Time) (startDay string, endDay string) {
	endDay = policy.DayKeyChina(now)
	switch rangeKey {
	case usagedashboard.Range("7d"):
		return policy.DayKeyChina(now.AddDate(0, 0, -6)), endDay
	case usagedashboard.Range("all"):
		return "", ""
	default:
		return "", endDay
	}
}

func dashboardEventRange(rangeKey usagedashboard.Range, now time.Time) (time.Time, time.Time) {
	switch rangeKey {
	case usagedashboard.Range("7h"):
		return now.Add(-7 * time.Hour), now
	case usagedashboard.Range("24h"):
		return now.Add(-24 * time.Hour), now
	case usagedashboard.Range("7d"):
		return now.Add(-7 * 24 * time.Hour), now
	case usagedashboard.Range("all"):
		return now.Add(-24 * time.Hour), now
	default:
		return now.Add(-24 * time.Hour), now
	}
}

func maskDashboardAPIKey(apiKey string) string {
	trimmed := strings.TrimSpace(apiKey)
	if trimmed == "" {
		return "unknown"
	}
	return util.HideAPIKey(trimmed)
}

func historicalDashboardTimestamp(day string) time.Time {
	return historicalDashboardTimestampAt(day, time.Now().In(chinaDashboardLocation()))
}

func historicalDashboardTimestampAt(day string, now time.Time) time.Time {
	loc := chinaDashboardLocation()
	now = now.In(loc)
	trimmedDay := strings.TrimSpace(day)
	if trimmedDay == "" {
		return now
	}

	parsed, err := time.ParseInLocation("2006-01-02 15:04:05", trimmedDay+" 23:59:59", loc)
	if err != nil {
		return now
	}
	if parsed.After(now) && policy.DayKeyChina(parsed) == policy.DayKeyChina(now) {
		return now
	}
	return parsed
}

func chinaDashboardLocation() *time.Location {
	return time.FixedZone("CST", 8*60*60)
}

func maxDashboardInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
