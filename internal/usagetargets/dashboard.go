package usagetargets

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/billing"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usageidentity"
)

const (
	statusBlockCount = 20
	statusWindow     = 200 * time.Minute
	statusBlockWidth = 10 * time.Minute
)

type StatusBlockState string

const (
	StatusBlockSuccess StatusBlockState = "success"
	StatusBlockFailure StatusBlockState = "failure"
	StatusBlockMixed   StatusBlockState = "mixed"
	StatusBlockIdle    StatusBlockState = "idle"
)

type TargetStat struct {
	SuccessCount int64     `json:"success_count"`
	FailureCount int64     `json:"failure_count"`
	StatusBar    StatusBar `json:"status_bar"`
}

type StatusBar struct {
	Blocks       []StatusBlockState `json:"blocks"`
	SuccessRate  float64            `json:"success_rate"`
	TotalSuccess int64              `json:"total_success"`
	TotalFailure int64              `json:"total_failure"`
}

type CredentialStat struct {
	APIKey       string    `json:"api_key,omitempty"`
	Prefix       string    `json:"prefix,omitempty"`
	SuccessCount int64     `json:"success_count"`
	FailureCount int64     `json:"failure_count"`
	StatusBar    StatusBar `json:"status_bar"`
}

type OpenAIProviderStat struct {
	Name          string           `json:"name"`
	Prefix        string           `json:"prefix,omitempty"`
	BaseURL       string           `json:"base_url,omitempty"`
	SuccessCount  int64            `json:"success_count"`
	FailureCount  int64            `json:"failure_count"`
	StatusBar     StatusBar        `json:"status_bar"`
	APIKeyEntries []CredentialStat `json:"api_key_entries"`
}

type Providers struct {
	Gemini []CredentialStat     `json:"gemini"`
	Codex  []CredentialStat     `json:"codex"`
	Claude []CredentialStat     `json:"claude"`
	Vertex []CredentialStat     `json:"vertex"`
	OpenAI []OpenAIProviderStat `json:"openai"`
}

type AuthFiles struct {
	ByAuthIndex map[string]TargetStat `json:"by_auth_index"`
	BySource    map[string]TargetStat `json:"by_source"`
}

type Dashboard struct {
	GeneratedAt time.Time `json:"generated_at"`
	Providers   Providers `json:"providers"`
	AuthFiles   AuthFiles `json:"auth_files"`
}

type counter struct {
	Success int64
	Failure int64
}

type statusAccumulator struct {
	Blocks       [statusBlockCount]counter
	TotalSuccess int64
	TotalFailure int64
}

func Build(
	cfg *config.Config,
	dailyRows []billing.DailyUsageRow,
	aggregateRows []billing.UsageEventAggregateRow,
	recentRows []billing.UsageEventRow,
	now time.Time,
) Dashboard {
	if now.IsZero() {
		now = time.Now()
	}

	dailyByAPIKey := buildDailyCountsByAPIKey(dailyRows)
	providerSourceTotals, authSourceTotals, authIndexTotals := buildAggregateCounts(aggregateRows)
	providerSourceStatus, authSourceStatus, authIndexStatus := buildRecentStatusMaps(recentRows, now)

	dashboard := Dashboard{
		GeneratedAt: now.UTC(),
		AuthFiles: AuthFiles{
			ByAuthIndex: make(map[string]TargetStat),
			BySource:    make(map[string]TargetStat),
		},
	}

	if cfg != nil {
		dashboard.Providers = Providers{
			Gemini: buildGeminiStats(cfg.GeminiKey, dailyByAPIKey, providerSourceTotals, providerSourceStatus),
			Codex:  buildCodexStats(cfg.CodexKey, dailyByAPIKey, providerSourceTotals, providerSourceStatus),
			Claude: buildClaudeStats(cfg.ClaudeKey, dailyByAPIKey, providerSourceTotals, providerSourceStatus),
			Vertex: buildVertexStats(cfg.VertexCompatAPIKey, dailyByAPIKey, providerSourceTotals, providerSourceStatus),
			OpenAI: buildOpenAIStats(cfg.OpenAICompatibility, dailyByAPIKey, providerSourceTotals, providerSourceStatus),
		}
	}

	for authIndex, counts := range authIndexTotals {
		dashboard.AuthFiles.ByAuthIndex[authIndex] = TargetStat{
			SuccessCount: counts.Success,
			FailureCount: counts.Failure,
			StatusBar:    buildStatusBarFromAccumulator(authIndexStatus[authIndex]),
		}
	}
	for source, counts := range authSourceTotals {
		dashboard.AuthFiles.BySource[source] = TargetStat{
			SuccessCount: counts.Success,
			FailureCount: counts.Failure,
			StatusBar:    buildStatusBarFromAccumulator(authSourceStatus[source]),
		}
	}

	return dashboard
}

func buildGeminiStats(
	entries []config.GeminiKey,
	dailyByAPIKey map[string]counter,
	providerSourceTotals map[string]counter,
	providerSourceStatus map[string]statusAccumulator,
) []CredentialStat {
	stats := make([]CredentialStat, 0, len(entries))
	for _, entry := range entries {
		stats = append(stats, buildCredentialStat(entry.APIKey, entry.Prefix, dailyByAPIKey, providerSourceTotals, providerSourceStatus))
	}
	return stats
}

func buildCodexStats(
	entries []config.CodexKey,
	dailyByAPIKey map[string]counter,
	providerSourceTotals map[string]counter,
	providerSourceStatus map[string]statusAccumulator,
) []CredentialStat {
	stats := make([]CredentialStat, 0, len(entries))
	for _, entry := range entries {
		stats = append(stats, buildCredentialStat(entry.APIKey, entry.Prefix, dailyByAPIKey, providerSourceTotals, providerSourceStatus))
	}
	return stats
}

func buildClaudeStats(
	entries []config.ClaudeKey,
	dailyByAPIKey map[string]counter,
	providerSourceTotals map[string]counter,
	providerSourceStatus map[string]statusAccumulator,
) []CredentialStat {
	stats := make([]CredentialStat, 0, len(entries))
	for _, entry := range entries {
		stats = append(stats, buildCredentialStat(entry.APIKey, entry.Prefix, dailyByAPIKey, providerSourceTotals, providerSourceStatus))
	}
	return stats
}

func buildVertexStats(
	entries []config.VertexCompatKey,
	dailyByAPIKey map[string]counter,
	providerSourceTotals map[string]counter,
	providerSourceStatus map[string]statusAccumulator,
) []CredentialStat {
	stats := make([]CredentialStat, 0, len(entries))
	for _, entry := range entries {
		stats = append(stats, buildCredentialStat(entry.APIKey, entry.Prefix, dailyByAPIKey, providerSourceTotals, providerSourceStatus))
	}
	return stats
}

func buildOpenAIStats(
	entries []config.OpenAICompatibility,
	dailyByAPIKey map[string]counter,
	providerSourceTotals map[string]counter,
	providerSourceStatus map[string]statusAccumulator,
) []OpenAIProviderStat {
	stats := make([]OpenAIProviderStat, 0, len(entries))
	for _, entry := range entries {
		providerStat := OpenAIProviderStat{
			Name:          strings.TrimSpace(entry.Name),
			Prefix:        strings.TrimSpace(entry.Prefix),
			BaseURL:       strings.TrimSpace(entry.BaseURL),
			APIKeyEntries: make([]CredentialStat, 0, len(entry.APIKeyEntries)),
		}

		seenAPIKeys := make(map[string]struct{}, len(entry.APIKeyEntries))
		providerCandidates := usageidentity.BuildCandidateSourceIDs("", entry.Prefix)

		for _, apiKeyEntry := range entry.APIKeyEntries {
			stat := buildCredentialStat(apiKeyEntry.APIKey, "", dailyByAPIKey, providerSourceTotals, providerSourceStatus)
			providerStat.APIKeyEntries = append(providerStat.APIKeyEntries, stat)

			trimmedKey := strings.TrimSpace(apiKeyEntry.APIKey)
			if trimmedKey == "" {
				continue
			}
			if _, ok := seenAPIKeys[trimmedKey]; ok {
				continue
			}
			seenAPIKeys[trimmedKey] = struct{}{}
			counts := dailyByAPIKey[trimmedKey]
			providerStat.SuccessCount += counts.Success
			providerStat.FailureCount += counts.Failure
			providerCandidates = append(providerCandidates, usageidentity.BuildCandidateSourceIDs(trimmedKey, "")...)
		}

		if providerStat.SuccessCount == 0 && providerStat.FailureCount == 0 {
			fallback := combineCounters(providerSourceTotals, providerCandidates)
			providerStat.SuccessCount = fallback.Success
			providerStat.FailureCount = fallback.Failure
		}
		providerStat.StatusBar = buildStatusBarFromAccumulator(combineStatusAccumulators(providerSourceStatus, providerCandidates))
		stats = append(stats, providerStat)
	}
	return stats
}

func buildCredentialStat(
	apiKey string,
	prefix string,
	dailyByAPIKey map[string]counter,
	providerSourceTotals map[string]counter,
	providerSourceStatus map[string]statusAccumulator,
) CredentialStat {
	trimmedKey := strings.TrimSpace(apiKey)
	trimmedPrefix := strings.TrimSpace(prefix)
	stat := CredentialStat{
		APIKey: trimmedKey,
		Prefix: trimmedPrefix,
	}

	if trimmedKey != "" {
		counts := dailyByAPIKey[trimmedKey]
		stat.SuccessCount = counts.Success
		stat.FailureCount = counts.Failure
	}

	candidates := usageidentity.BuildCandidateSourceIDs(trimmedKey, trimmedPrefix)
	if stat.SuccessCount == 0 && stat.FailureCount == 0 {
		fallback := combineCounters(providerSourceTotals, candidates)
		stat.SuccessCount = fallback.Success
		stat.FailureCount = fallback.Failure
	}
	stat.StatusBar = buildStatusBarFromAccumulator(combineStatusAccumulators(providerSourceStatus, candidates))
	return stat
}

func buildDailyCountsByAPIKey(rows []billing.DailyUsageRow) map[string]counter {
	result := make(map[string]counter)
	for _, row := range rows {
		apiKey := strings.TrimSpace(row.APIKey)
		if apiKey == "" {
			continue
		}
		counts := result[apiKey]
		counts.Success += max64(row.Requests-row.FailedRequests, 0)
		counts.Failure += max64(row.FailedRequests, 0)
		result[apiKey] = counts
	}
	return result
}

func buildAggregateCounts(rows []billing.UsageEventAggregateRow) (map[string]counter, map[string]counter, map[string]counter) {
	providerSource := make(map[string]counter)
	authSource := make(map[string]counter)
	authIndex := make(map[string]counter)

	for _, row := range rows {
		success := max64(row.SuccessCount, 0)
		failure := max64(row.FailureCount, 0)

		if normalizedSource := usageidentity.NormalizeSourceID(row.Source); normalizedSource != "" {
			counts := providerSource[normalizedSource]
			counts.Success += success
			counts.Failure += failure
			providerSource[normalizedSource] = counts
		}

		if rawSource := strings.TrimSpace(row.Source); rawSource != "" {
			counts := authSource[rawSource]
			counts.Success += success
			counts.Failure += failure
			authSource[rawSource] = counts

			baseName := strings.TrimSpace(strings.TrimSuffix(rawSource, filepath.Ext(rawSource)))
			if baseName != "" && baseName != rawSource {
				baseCounts := authSource[baseName]
				baseCounts.Success += success
				baseCounts.Failure += failure
				authSource[baseName] = baseCounts
			}
		}

		if trimmedAuthIndex := strings.TrimSpace(row.AuthIndex); trimmedAuthIndex != "" {
			counts := authIndex[trimmedAuthIndex]
			counts.Success += success
			counts.Failure += failure
			authIndex[trimmedAuthIndex] = counts
		}
	}

	return providerSource, authSource, authIndex
}

func buildRecentStatusMaps(rows []billing.UsageEventRow, now time.Time) (map[string]statusAccumulator, map[string]statusAccumulator, map[string]statusAccumulator) {
	providerSource := make(map[string]statusAccumulator)
	authSource := make(map[string]statusAccumulator)
	authIndex := make(map[string]statusAccumulator)
	windowStart := now.Add(-statusWindow)

	for _, row := range rows {
		timestamp := time.Unix(row.RequestedAt, 0)
		if row.RequestedAt <= 0 || timestamp.Before(windowStart) || timestamp.After(now) {
			continue
		}
		blockIndex := statusBlockIndex(now, timestamp)
		if blockIndex < 0 || blockIndex >= statusBlockCount {
			continue
		}

		success := int64(1)
		failure := int64(0)
		if row.Failed {
			success = 0
			failure = 1
		}

		if normalizedSource := usageidentity.NormalizeSourceID(row.Source); normalizedSource != "" {
			acc := providerSource[normalizedSource]
			acc = addStatusSample(acc, blockIndex, success, failure)
			providerSource[normalizedSource] = acc
		}

		if rawSource := strings.TrimSpace(row.Source); rawSource != "" {
			acc := authSource[rawSource]
			acc = addStatusSample(acc, blockIndex, success, failure)
			authSource[rawSource] = acc

			baseName := strings.TrimSpace(strings.TrimSuffix(rawSource, filepath.Ext(rawSource)))
			if baseName != "" && baseName != rawSource {
				baseAcc := authSource[baseName]
				baseAcc = addStatusSample(baseAcc, blockIndex, success, failure)
				authSource[baseName] = baseAcc
			}
		}

		if trimmedAuthIndex := strings.TrimSpace(row.AuthIndex); trimmedAuthIndex != "" {
			acc := authIndex[trimmedAuthIndex]
			acc = addStatusSample(acc, blockIndex, success, failure)
			authIndex[trimmedAuthIndex] = acc
		}
	}

	return providerSource, authSource, authIndex
}

func addStatusSample(acc statusAccumulator, blockIndex int, success, failure int64) statusAccumulator {
	acc.Blocks[blockIndex].Success += max64(success, 0)
	acc.Blocks[blockIndex].Failure += max64(failure, 0)
	acc.TotalSuccess += max64(success, 0)
	acc.TotalFailure += max64(failure, 0)
	return acc
}

func combineCounters(values map[string]counter, keys []string) counter {
	var combined counter
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		counts := values[trimmed]
		combined.Success += counts.Success
		combined.Failure += counts.Failure
	}
	return combined
}

func combineStatusAccumulators(values map[string]statusAccumulator, keys []string) statusAccumulator {
	var combined statusAccumulator
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		combined = mergeStatusAccumulator(combined, values[trimmed])
	}
	return combined
}

func mergeStatusAccumulator(dst, src statusAccumulator) statusAccumulator {
	for idx := range dst.Blocks {
		dst.Blocks[idx].Success += src.Blocks[idx].Success
		dst.Blocks[idx].Failure += src.Blocks[idx].Failure
	}
	dst.TotalSuccess += src.TotalSuccess
	dst.TotalFailure += src.TotalFailure
	return dst
}

func buildStatusBarFromAccumulator(acc statusAccumulator) StatusBar {
	blocks := make([]StatusBlockState, statusBlockCount)
	for idx, block := range acc.Blocks {
		switch {
		case block.Success == 0 && block.Failure == 0:
			blocks[idx] = StatusBlockIdle
		case block.Failure == 0:
			blocks[idx] = StatusBlockSuccess
		case block.Success == 0:
			blocks[idx] = StatusBlockFailure
		default:
			blocks[idx] = StatusBlockMixed
		}
	}

	successRate := 100.0
	total := acc.TotalSuccess + acc.TotalFailure
	if total > 0 {
		successRate = float64(acc.TotalSuccess) / float64(total) * 100
	}

	return StatusBar{
		Blocks:       blocks,
		SuccessRate:  successRate,
		TotalSuccess: acc.TotalSuccess,
		TotalFailure: acc.TotalFailure,
	}
}

func statusBlockIndex(now, timestamp time.Time) int {
	age := now.Sub(timestamp)
	if age < 0 || age > statusWindow {
		return -1
	}
	return statusBlockCount - 1 - int(age/statusBlockWidth)
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
