package billing

import "time"

// PriceMicroUSDPer1M stores USD pricing in micro-dollars per 1M tokens.
// Example: $5 / 1M tokens => 5_000_000 micro-USD per 1M.
type PriceMicroUSDPer1M struct {
	Prompt     int64
	Completion int64
	Cached     int64
}

type ModelPrice struct {
	Model              string  `json:"model"`
	PromptUSDPer1M     float64 `json:"prompt_usd_per_1m"`
	CompletionUSDPer1M float64 `json:"completion_usd_per_1m"`
	CachedUSDPer1M     float64 `json:"cached_usd_per_1m"`
	Source             string  `json:"source,omitempty"` // "saved" | "default"
	UpdatedAt          int64   `json:"updated_at,omitempty"`
}

type DailyUsageRow struct {
	APIKey         string `json:"api_key"`
	Model          string `json:"model"`
	Day            string `json:"day"`
	Requests       int64  `json:"requests"`
	FailedRequests int64  `json:"failed_requests"`

	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens"`
	CachedTokens    int64 `json:"cached_tokens"`
	TotalTokens     int64 `json:"total_tokens"`

	CostMicroUSD int64 `json:"cost_micro_usd"`
	UpdatedAt    int64 `json:"updated_at,omitempty"`
}

type UsageEventRow struct {
	ID int64 `json:"id,omitempty"`

	RequestedAt int64  `json:"requested_at"`
	APIKey      string `json:"api_key"`
	Source      string `json:"source"`
	AuthIndex   string `json:"auth_index"`
	Model       string `json:"model"`
	Failed      bool   `json:"failed"`
	LatencyMs   int64  `json:"latency_ms,omitempty"`

	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens"`
	CachedTokens    int64 `json:"cached_tokens"`
	TotalTokens     int64 `json:"total_tokens"`

	CostMicroUSD int64 `json:"cost_micro_usd"`
	UpdatedAt    int64 `json:"updated_at,omitempty"`
}

type UsageEventAggregateRow struct {
	Source       string `json:"source"`
	AuthIndex    string `json:"auth_index"`
	SuccessCount int64  `json:"success_count"`
	FailureCount int64  `json:"failure_count"`
	SlowCount    int64  `json:"slow_count"`
}

type DailyUsageReport struct {
	APIKey          string          `json:"api_key"`
	Day             string          `json:"day"`
	TotalCostMicro  int64           `json:"total_cost_micro_usd"`
	TotalCostUSD    float64         `json:"total_cost_usd"`
	TotalRequests   int64           `json:"total_requests"`
	TotalFailed     int64           `json:"total_failed_requests"`
	TotalTokens     int64           `json:"total_tokens"`
	Models          []DailyUsageRow `json:"models"`
	GeneratedAtUnix int64           `json:"generated_at_unix"`
}

type usagePersistRecord struct {
	dayKey string
	delta  DailyUsageRow
	event  UsageEventRow
}

func nowUnixUTC() int64 { return time.Now().UTC().Unix() }
