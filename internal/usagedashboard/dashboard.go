package usagedashboard

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	range7H  = "7h"
	range24H = "24h"
	range7D  = "7d"
	rangeAll = "all"
)

var chinaLocation = time.FixedZone("CST", 8*60*60)

type Range string

func ParseRange(raw string) (Range, error) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", range24H:
		return Range(range24H), nil
	case range7H:
		return Range(range7H), nil
	case range7D:
		return Range(range7D), nil
	case rangeAll:
		return Range(rangeAll), nil
	default:
		return "", fmt.Errorf("invalid range %q", raw)
	}
}

func (r Range) String() string {
	if r == "" {
		return range24H
	}
	return string(r)
}

func (r Range) HourWindowHours() int {
	switch r {
	case Range(range7H):
		return 7
	case Range(range24H):
		return 24
	case Range(range7D):
		return 7 * 24
	case Range(rangeAll):
		return 24
	default:
		return 24
	}
}

type AggregateEntry struct {
	Timestamp       time.Time
	APIKey          string
	Source          string
	AuthIndex       string
	Model           string
	Requests        int64
	SuccessCount    int64
	FailureCount    int64
	InputTokens     int64
	OutputTokens    int64
	ReasoningTokens int64
	CachedTokens    int64
	TotalTokens     int64
	CostMicroUSD    int64
}

type Dashboard struct {
	Range       string          `json:"range"`
	GeneratedAt time.Time       `json:"generated_at"`
	Summary     Summary         `json:"summary"`
	Rates       RateStats       `json:"rates"`
	ModelNames  []string        `json:"model_names"`
	APIStats    []APIStat       `json:"api_stats"`
	ModelStats  []ModelStat     `json:"model_stats"`
	Charts      ChartCollection `json:"charts"`
	Sparklines  SparklineSet    `json:"sparklines"`
}

type Summary struct {
	TotalRequests   int64   `json:"total_requests"`
	SuccessCount    int64   `json:"success_count"`
	FailureCount    int64   `json:"failure_count"`
	TotalTokens     int64   `json:"total_tokens"`
	TotalCostUSD    float64 `json:"total_cost_usd"`
	CachedTokens    int64   `json:"cached_tokens"`
	ReasoningTokens int64   `json:"reasoning_tokens"`
}

type RateStats struct {
	RPM          float64 `json:"rpm"`
	TPM          float64 `json:"tpm"`
	WindowMinute int64   `json:"window_minutes"`
	RequestCount int64   `json:"request_count"`
	TokenCount   int64   `json:"token_count"`
}

type APIStat struct {
	Endpoint      string        `json:"endpoint"`
	TotalRequests int64         `json:"total_requests"`
	SuccessCount  int64         `json:"success_count"`
	FailureCount  int64         `json:"failure_count"`
	TotalTokens   int64         `json:"total_tokens"`
	TotalCostUSD  float64       `json:"total_cost_usd"`
	Models        []APIModeStat `json:"models"`
}

type APIModeStat struct {
	Model        string  `json:"model"`
	Requests     int64   `json:"requests"`
	SuccessCount int64   `json:"success_count"`
	FailureCount int64   `json:"failure_count"`
	Tokens       int64   `json:"tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

type ModelStat struct {
	Model        string  `json:"model"`
	Requests     int64   `json:"requests"`
	SuccessCount int64   `json:"success_count"`
	FailureCount int64   `json:"failure_count"`
	Tokens       int64   `json:"tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

type ChartCollection struct {
	Requests MetricCharts `json:"requests"`
	Tokens   MetricCharts `json:"tokens"`
}

type MetricCharts struct {
	Hour SeriesSet `json:"hour"`
	Day  SeriesSet `json:"day"`
}

type SeriesSet struct {
	Labels []string      `json:"labels"`
	Series []SeriesEntry `json:"series"`
}

type SeriesEntry struct {
	Key  string    `json:"key"`
	Data []float64 `json:"data"`
}

type SparklineSet struct {
	Requests SparklineSeries `json:"requests"`
	Tokens   SparklineSeries `json:"tokens"`
	RPM      SparklineSeries `json:"rpm"`
	TPM      SparklineSeries `json:"tpm"`
	Cost     SparklineSeries `json:"cost"`
}

type SparklineSeries struct {
	Labels []string  `json:"labels"`
	Data   []float64 `json:"data"`
}

func Build(r Range, summaryEntries, detailEntries []AggregateEntry, now time.Time) Dashboard {
	if now.IsZero() {
		now = time.Now().In(chinaLocation)
	}
	dashboard := Dashboard{
		Range:       r.String(),
		GeneratedAt: now.UTC(),
	}

	dashboard.Summary = buildSummary(summaryEntries)
	dashboard.Rates = buildRates(detailEntries, now, 30)
	dashboard.ModelNames = buildModelNames(summaryEntries, detailEntries)
	dashboard.APIStats = buildAPIStats(summaryEntries)
	dashboard.ModelStats = buildModelStats(summaryEntries)
	dashboard.Charts = ChartCollection{
		Requests: MetricCharts{
			Hour: buildHourlySeries(detailEntries, now, r.HourWindowHours(), metricRequests),
			Day:  buildDailySeries(summaryEntries, metricRequests),
		},
		Tokens: MetricCharts{
			Hour: buildHourlySeries(detailEntries, now, r.HourWindowHours(), metricTokens),
			Day:  buildDailySeries(summaryEntries, metricTokens),
		},
	}

	requestSparkline := buildMinuteSparkline(detailEntries, now, 60, metricRequests)
	tokenSparkline := buildMinuteSparkline(detailEntries, now, 60, metricTokens)
	dashboard.Sparklines = SparklineSet{
		Requests: requestSparkline,
		Tokens:   tokenSparkline,
		RPM:      requestSparkline,
		TPM:      tokenSparkline,
		Cost:     buildMinuteSparkline(detailEntries, now, 60, metricCostUSD),
	}

	return dashboard
}

type metricKind string

const (
	metricRequests metricKind = "requests"
	metricTokens   metricKind = "tokens"
	metricCostUSD  metricKind = "cost_usd"
)

func buildSummary(entries []AggregateEntry) Summary {
	summary := Summary{}
	totalCostMicro := int64(0)

	for _, entry := range entries {
		summary.TotalRequests += nonNegative(entry.Requests)
		summary.SuccessCount += nonNegative(entry.SuccessCount)
		summary.FailureCount += nonNegative(entry.FailureCount)
		summary.TotalTokens += nonNegative(entry.TotalTokens)
		summary.CachedTokens += nonNegative(entry.CachedTokens)
		summary.ReasoningTokens += nonNegative(entry.ReasoningTokens)
		totalCostMicro += nonNegative(entry.CostMicroUSD)
	}

	summary.TotalCostUSD = microUSDToUSD(totalCostMicro)
	return summary
}

func buildRates(entries []AggregateEntry, now time.Time, windowMinutes int64) RateStats {
	rates := RateStats{WindowMinute: windowMinutes}
	if windowMinutes <= 0 {
		return rates
	}

	windowStart := now.Add(-time.Duration(windowMinutes) * time.Minute)
	for _, entry := range entries {
		ts := entry.Timestamp.In(chinaLocation)
		if ts.Before(windowStart) || ts.After(now) {
			continue
		}
		rates.RequestCount += nonNegative(entry.Requests)
		rates.TokenCount += nonNegative(entry.TotalTokens)
	}

	denominator := float64(windowMinutes)
	if denominator <= 0 {
		denominator = 1
	}
	rates.RPM = float64(rates.RequestCount) / denominator
	rates.TPM = float64(rates.TokenCount) / denominator
	return rates
}

func buildModelNames(summaryEntries, detailEntries []AggregateEntry) []string {
	set := map[string]struct{}{}
	for _, entry := range summaryEntries {
		if model := strings.TrimSpace(entry.Model); model != "" {
			set[model] = struct{}{}
		}
	}
	for _, entry := range detailEntries {
		if model := strings.TrimSpace(entry.Model); model != "" {
			set[model] = struct{}{}
		}
	}
	names := make([]string, 0, len(set))
	for model := range set {
		names = append(names, model)
	}
	sort.Strings(names)
	return names
}

func buildAPIStats(entries []AggregateEntry) []APIStat {
	type apiModelAccumulator struct {
		Requests     int64
		SuccessCount int64
		FailureCount int64
		Tokens       int64
		CostMicroUSD int64
	}
	type apiAccumulator struct {
		TotalRequests int64
		SuccessCount  int64
		FailureCount  int64
		TotalTokens   int64
		CostMicroUSD  int64
		Models        map[string]*apiModelAccumulator
	}

	accumulators := map[string]*apiAccumulator{}
	for _, entry := range entries {
		endpoint := strings.TrimSpace(entry.APIKey)
		if endpoint == "" {
			endpoint = "unknown"
		}
		acc := accumulators[endpoint]
		if acc == nil {
			acc = &apiAccumulator{Models: map[string]*apiModelAccumulator{}}
			accumulators[endpoint] = acc
		}

		acc.TotalRequests += nonNegative(entry.Requests)
		acc.SuccessCount += nonNegative(entry.SuccessCount)
		acc.FailureCount += nonNegative(entry.FailureCount)
		acc.TotalTokens += nonNegative(entry.TotalTokens)
		acc.CostMicroUSD += nonNegative(entry.CostMicroUSD)

		modelKey := strings.TrimSpace(entry.Model)
		if modelKey == "" {
			modelKey = "unknown"
		}
		modelAcc := acc.Models[modelKey]
		if modelAcc == nil {
			modelAcc = &apiModelAccumulator{}
			acc.Models[modelKey] = modelAcc
		}
		modelAcc.Requests += nonNegative(entry.Requests)
		modelAcc.SuccessCount += nonNegative(entry.SuccessCount)
		modelAcc.FailureCount += nonNegative(entry.FailureCount)
		modelAcc.Tokens += nonNegative(entry.TotalTokens)
		modelAcc.CostMicroUSD += nonNegative(entry.CostMicroUSD)
	}

	apiStats := make([]APIStat, 0, len(accumulators))
	for endpoint, acc := range accumulators {
		models := make([]APIModeStat, 0, len(acc.Models))
		for model, modelAcc := range acc.Models {
			models = append(models, APIModeStat{
				Model:        model,
				Requests:     modelAcc.Requests,
				SuccessCount: modelAcc.SuccessCount,
				FailureCount: modelAcc.FailureCount,
				Tokens:       modelAcc.Tokens,
				CostUSD:      microUSDToUSD(modelAcc.CostMicroUSD),
			})
		}
		sort.Slice(models, func(i, j int) bool {
			if models[i].Requests == models[j].Requests {
				return models[i].Model < models[j].Model
			}
			return models[i].Requests > models[j].Requests
		})

		apiStats = append(apiStats, APIStat{
			Endpoint:      endpoint,
			TotalRequests: acc.TotalRequests,
			SuccessCount:  acc.SuccessCount,
			FailureCount:  acc.FailureCount,
			TotalTokens:   acc.TotalTokens,
			TotalCostUSD:  microUSDToUSD(acc.CostMicroUSD),
			Models:        models,
		})
	}

	sort.Slice(apiStats, func(i, j int) bool {
		if apiStats[i].TotalRequests == apiStats[j].TotalRequests {
			return apiStats[i].Endpoint < apiStats[j].Endpoint
		}
		return apiStats[i].TotalRequests > apiStats[j].TotalRequests
	})
	return apiStats
}

func buildModelStats(entries []AggregateEntry) []ModelStat {
	type accumulator struct {
		Requests     int64
		SuccessCount int64
		FailureCount int64
		Tokens       int64
		CostMicroUSD int64
	}
	accumulators := map[string]*accumulator{}
	for _, entry := range entries {
		modelKey := strings.TrimSpace(entry.Model)
		if modelKey == "" {
			modelKey = "unknown"
		}
		acc := accumulators[modelKey]
		if acc == nil {
			acc = &accumulator{}
			accumulators[modelKey] = acc
		}
		acc.Requests += nonNegative(entry.Requests)
		acc.SuccessCount += nonNegative(entry.SuccessCount)
		acc.FailureCount += nonNegative(entry.FailureCount)
		acc.Tokens += nonNegative(entry.TotalTokens)
		acc.CostMicroUSD += nonNegative(entry.CostMicroUSD)
	}

	result := make([]ModelStat, 0, len(accumulators))
	for model, acc := range accumulators {
		result = append(result, ModelStat{
			Model:        model,
			Requests:     acc.Requests,
			SuccessCount: acc.SuccessCount,
			FailureCount: acc.FailureCount,
			Tokens:       acc.Tokens,
			CostUSD:      microUSDToUSD(acc.CostMicroUSD),
		})
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Requests == result[j].Requests {
			return result[i].Model < result[j].Model
		}
		return result[i].Requests > result[j].Requests
	})
	return result
}

func buildDailySeries(entries []AggregateEntry, metric metricKind) SeriesSet {
	labelsSet := map[string]struct{}{}
	valuesByModel := map[string]map[string]float64{}

	for _, entry := range entries {
		modelKey := strings.TrimSpace(entry.Model)
		if modelKey == "" {
			modelKey = "unknown"
		}
		dayLabel := entry.Timestamp.In(chinaLocation).Format("2006-01-02")
		if dayLabel == "" {
			continue
		}
		if _, ok := valuesByModel[modelKey]; !ok {
			valuesByModel[modelKey] = map[string]float64{}
		}
		valuesByModel[modelKey][dayLabel] += metricValue(entry, metric)
		labelsSet[dayLabel] = struct{}{}
	}

	labels := make([]string, 0, len(labelsSet))
	for label := range labelsSet {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	return buildSeriesSet(labels, valuesByModel)
}

func buildHourlySeries(entries []AggregateEntry, now time.Time, hourWindow int, metric metricKind) SeriesSet {
	if hourWindow <= 0 {
		hourWindow = 24
	}

	currentHour := now.In(chinaLocation).Truncate(time.Hour)
	earliestHour := currentHour.Add(-time.Duration(hourWindow-1) * time.Hour)
	labels := make([]string, 0, hourWindow)
	valuesByModel := map[string]map[string]float64{}

	for i := 0; i < hourWindow; i++ {
		bucket := earliestHour.Add(time.Duration(i) * time.Hour)
		labels = append(labels, bucket.Format("01-02 15:00"))
	}

	for _, entry := range entries {
		ts := entry.Timestamp.In(chinaLocation)
		if ts.Before(earliestHour) || ts.After(currentHour.Add(time.Hour).Add(-time.Nanosecond)) {
			continue
		}
		modelKey := strings.TrimSpace(entry.Model)
		if modelKey == "" {
			modelKey = "unknown"
		}
		if _, ok := valuesByModel[modelKey]; !ok {
			valuesByModel[modelKey] = map[string]float64{}
		}
		bucketLabel := ts.Truncate(time.Hour).Format("01-02 15:00")
		valuesByModel[modelKey][bucketLabel] += metricValue(entry, metric)
	}

	return buildSeriesSet(labels, valuesByModel)
}

func buildMinuteSparkline(entries []AggregateEntry, now time.Time, minuteWindow int, metric metricKind) SparklineSeries {
	if minuteWindow <= 0 {
		minuteWindow = 60
	}

	windowStart := now.Add(-time.Duration(minuteWindow) * time.Minute)
	values := make([]float64, minuteWindow)
	labels := make([]string, minuteWindow)

	for i := 0; i < minuteWindow; i++ {
		bucketTime := windowStart.Add(time.Duration(i+1) * time.Minute)
		labels[i] = bucketTime.In(chinaLocation).Format("15:04")
	}

	for _, entry := range entries {
		ts := entry.Timestamp.In(chinaLocation)
		if ts.Before(windowStart) || ts.After(now) {
			continue
		}
		index := int(ts.Sub(windowStart) / time.Minute)
		if index < 0 {
			continue
		}
		if index >= minuteWindow {
			index = minuteWindow - 1
		}
		values[index] += metricValue(entry, metric)
	}

	return SparklineSeries{Labels: labels, Data: values}
}

func buildSeriesSet(labels []string, valuesByModel map[string]map[string]float64) SeriesSet {
	if len(labels) == 0 {
		return SeriesSet{}
	}

	keys := make([]string, 0, len(valuesByModel))
	for key := range valuesByModel {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	allValues := make([]float64, len(labels))
	series := make([]SeriesEntry, 0, len(keys)+1)
	for _, key := range keys {
		data := make([]float64, len(labels))
		for i, label := range labels {
			value := valuesByModel[key][label]
			data[i] = value
			allValues[i] += value
		}
		series = append(series, SeriesEntry{Key: key, Data: data})
	}

	series = append([]SeriesEntry{{Key: "all", Data: allValues}}, series...)
	return SeriesSet{Labels: labels, Series: series}
}

func metricValue(entry AggregateEntry, metric metricKind) float64 {
	switch metric {
	case metricRequests:
		return float64(nonNegative(entry.Requests))
	case metricTokens:
		return float64(nonNegative(entry.TotalTokens))
	case metricCostUSD:
		return microUSDToUSD(entry.CostMicroUSD)
	default:
		return 0
	}
}

func nonNegative(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

func microUSDToUSD(v int64) float64 {
	if v <= 0 {
		return 0
	}
	return float64(v) / 1_000_000
}
