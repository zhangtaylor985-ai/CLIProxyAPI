package billing

import (
	"fmt"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/policy"
	internalusage "github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
)

type usageCostRecalcRow struct {
	APIKey          string
	Model           string
	Day             string
	InputTokens     int64
	OutputTokens    int64
	ReasoningTokens int64
	CachedTokens    int64
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func historicalDetailTimestamp(day string) time.Time {
	return historicalDetailTimestampAt(day, time.Now().In(policy.ChinaLocation()))
}

func historicalDetailTimestampAt(day string, now time.Time) time.Time {
	loc := policy.ChinaLocation()
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

func usageAggregateKey(apiKey, model, day string) string {
	return strings.TrimSpace(apiKey) + "|" + policy.NormaliseModelKey(model) + "|" + strings.TrimSpace(day)
}

func addUsageEventToSnapshot(snapshot *internalusage.StatisticsSnapshot, row UsageEventRow) {
	if snapshot == nil {
		return
	}
	timestamp := time.Unix(row.RequestedAt, 0).In(policy.ChinaLocation())
	detail := internalusage.RequestDetail{
		Timestamp: timestamp,
		LatencyMs: max64(0, row.LatencyMs),
		Source:    strings.TrimSpace(row.Source),
		AuthIndex: strings.TrimSpace(row.AuthIndex),
		Tokens: internalusage.TokenStats{
			InputTokens:     max64(0, row.InputTokens),
			OutputTokens:    max64(0, row.OutputTokens),
			ReasoningTokens: max64(0, row.ReasoningTokens),
			CachedTokens:    max64(0, row.CachedTokens),
			TotalTokens:     max64(0, row.TotalTokens),
		},
		Failed: row.Failed,
	}
	addSnapshotDetail(snapshot, strings.TrimSpace(row.APIKey), policy.NormaliseModelKey(row.Model), detail)
}

func addDailyDeltaToSnapshot(snapshot *internalusage.StatisticsSnapshot, row DailyUsageRow) {
	if snapshot == nil {
		return
	}
	successCount := max64(0, row.Requests-row.FailedRequests)
	detail := internalusage.RequestDetail{
		Timestamp: historicalDetailTimestamp(row.Day),
		Source:    strings.TrimSpace(row.APIKey),
		Tokens: internalusage.TokenStats{
			InputTokens:     max64(0, row.InputTokens),
			OutputTokens:    max64(0, row.OutputTokens),
			ReasoningTokens: max64(0, row.ReasoningTokens),
			CachedTokens:    max64(0, row.CachedTokens),
			TotalTokens:     max64(0, row.TotalTokens),
		},
		Failed:       row.Requests > 0 && row.FailedRequests >= row.Requests,
		RequestCount: max64(0, row.Requests),
		SuccessCount: successCount,
		FailureCount: max64(0, row.FailedRequests),
	}
	addSnapshotDetail(snapshot, strings.TrimSpace(row.APIKey), policy.NormaliseModelKey(row.Model), detail)
}

func addSnapshotDetail(snapshot *internalusage.StatisticsSnapshot, apiKey, model string, detail internalusage.RequestDetail) {
	if snapshot == nil {
		return
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return
	}
	model = policy.NormaliseModelKey(model)
	if model == "" {
		model = "unknown"
	}
	detail = normalizeSnapshotDetail(detail)
	requestCount := usageSnapshotRequestCount(detail)
	successCount, failureCount := usageSnapshotOutcomeCounts(detail)
	totalTokens := max64(0, detail.Tokens.TotalTokens)

	apiSnapshot := snapshot.APIs[apiKey]
	if apiSnapshot.Models == nil {
		apiSnapshot.Models = map[string]internalusage.ModelSnapshot{}
	}
	apiSnapshot.TotalRequests += requestCount
	apiSnapshot.SuccessCount += successCount
	apiSnapshot.FailureCount += failureCount
	apiSnapshot.TotalTokens += totalTokens

	modelSnapshot := apiSnapshot.Models[model]
	modelSnapshot.TotalRequests += requestCount
	modelSnapshot.SuccessCount += successCount
	modelSnapshot.FailureCount += failureCount
	modelSnapshot.TotalTokens += totalTokens
	modelSnapshot.Details = append(modelSnapshot.Details, detail)
	apiSnapshot.Models[model] = modelSnapshot
	snapshot.APIs[apiKey] = apiSnapshot

	dayKey := detail.Timestamp.In(policy.ChinaLocation()).Format("2006-01-02")
	hourKey := detail.Timestamp.In(policy.ChinaLocation()).Format("15")
	snapshot.TotalRequests += requestCount
	snapshot.SuccessCount += successCount
	snapshot.FailureCount += failureCount
	snapshot.TotalTokens += totalTokens
	snapshot.RequestsByDay[dayKey] += requestCount
	snapshot.RequestsByHour[hourKey] += requestCount
	snapshot.TokensByDay[dayKey] += totalTokens
	snapshot.TokensByHour[hourKey] += totalTokens
}

func normalizeSnapshotDetail(detail internalusage.RequestDetail) internalusage.RequestDetail {
	if detail.Timestamp.IsZero() {
		detail.Timestamp = time.Now().In(policy.ChinaLocation())
	}
	detail.Timestamp = detail.Timestamp.In(policy.ChinaLocation())
	detail.Tokens = normalizeSnapshotTokenStats(detail.Tokens)
	return detail
}

func normalizeSnapshotTokenStats(tokens internalusage.TokenStats) internalusage.TokenStats {
	if tokens.TotalTokens == 0 {
		tokens.TotalTokens = tokens.InputTokens + tokens.OutputTokens + tokens.ReasoningTokens
	}
	if tokens.TotalTokens == 0 {
		tokens.TotalTokens = tokens.InputTokens + tokens.OutputTokens + tokens.ReasoningTokens + tokens.CachedTokens
	}
	tokens.InputTokens = max64(0, tokens.InputTokens)
	tokens.OutputTokens = max64(0, tokens.OutputTokens)
	tokens.ReasoningTokens = max64(0, tokens.ReasoningTokens)
	tokens.CachedTokens = max64(0, tokens.CachedTokens)
	tokens.TotalTokens = max64(0, tokens.TotalTokens)
	return tokens
}

func usageSnapshotRequestCount(detail internalusage.RequestDetail) int64 {
	if detail.RequestCount > 0 {
		return detail.RequestCount
	}
	return 1
}

func usageSnapshotOutcomeCounts(detail internalusage.RequestDetail) (success int64, failure int64) {
	if detail.SuccessCount > 0 || detail.FailureCount > 0 {
		success = max64(0, detail.SuccessCount)
		failure = max64(0, detail.FailureCount)
		if success+failure == 0 {
			return 1, 0
		}
		return success, failure
	}
	if detail.Failed {
		return 0, usageSnapshotRequestCount(detail)
	}
	return usageSnapshotRequestCount(detail), 0
}

func buildUsageSnapshotDedupSet(snapshot internalusage.StatisticsSnapshot) map[string]struct{} {
	seen := make(map[string]struct{})
	for apiKey, apiSnapshot := range snapshot.APIs {
		for modelName, modelSnapshot := range apiSnapshot.Models {
			for _, detail := range modelSnapshot.Details {
				seen[usageSnapshotDedupKey(apiKey, modelName, detail)] = struct{}{}
			}
		}
	}
	return seen
}

func usageSnapshotDedupKey(apiKey, model string, detail internalusage.RequestDetail) string {
	detail = normalizeSnapshotDetail(detail)
	return fmt.Sprintf(
		"%s|%s|%s|%s|%s|%t|%d|%d|%d|%d|%d|%d|%d|%d",
		strings.TrimSpace(apiKey),
		policy.NormaliseModelKey(model),
		detail.Timestamp.UTC().Format(time.RFC3339Nano),
		strings.TrimSpace(detail.Source),
		strings.TrimSpace(detail.AuthIndex),
		detail.Failed,
		usageSnapshotRequestCount(detail),
		max64(0, detail.SuccessCount),
		max64(0, detail.FailureCount),
		detail.Tokens.InputTokens,
		detail.Tokens.OutputTokens,
		detail.Tokens.ReasoningTokens,
		detail.Tokens.CachedTokens,
		detail.Tokens.TotalTokens,
	)
}
