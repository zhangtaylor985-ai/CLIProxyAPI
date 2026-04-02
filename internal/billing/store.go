package billing

import (
	"context"
	"time"

	internalusage "github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
)

// DailyCostReader is the minimal interface needed by request-time middleware.
type DailyCostReader interface {
	GetDailyCostMicroUSD(ctx context.Context, apiKey, dayKey string) (int64, error)
	GetCostMicroUSDByDayRange(ctx context.Context, apiKey, startDay, endDayExclusive string) (int64, error)
	GetCostMicroUSDByTimeRange(ctx context.Context, apiKey string, startInclusive, endExclusive time.Time) (int64, error)
	GetCostMicroUSDByModelPrefix(ctx context.Context, apiKey, modelPrefix string) (int64, error)
}

// PendingUsageProvider exposes in-memory pending usage batches to the persistent store.
type PendingUsageProvider interface {
	Stop(ctx context.Context) error
	PendingDailyCostMicroUSD(apiKey, dayKey string) int64
	PendingDailyUsageRows(apiKey, dayKey string) []DailyUsageRow
	PendingDailyUsageRowsByRange(apiKey, startDay, endDayExclusive string) []DailyUsageRow
	PendingCostMicroUSDByDayRange(apiKey, startDay, endDayExclusive string) int64
	PendingCostMicroUSDByTimeRange(apiKey string, startInclusive, endExclusive time.Time) int64
	PendingCostMicroUSDByModelPrefix(apiKey, modelPrefix string) int64
	PendingUsageEvents(apiKey string, startInclusive, endExclusive time.Time, limit int, desc bool) []UsageEventRow
	PendingLatestRequestedAt(apiKey string) int64
	MergePendingSnapshot(snapshot *internalusage.StatisticsSnapshot)
}

// Store defines the billing persistence contract.
type Store interface {
	DailyCostReader

	Close() error
	SetPendingUsageProvider(provider PendingUsageProvider)

	UpsertModelPrice(ctx context.Context, model string, price PriceMicroUSDPer1M) error
	DeleteModelPrice(ctx context.Context, model string) (bool, error)
	ResolvePriceMicro(ctx context.Context, model string) (price PriceMicroUSDPer1M, source string, updatedAt int64, err error)
	ListModelPrices(ctx context.Context) ([]ModelPrice, error)

	AddUsage(ctx context.Context, apiKey, model, dayKey string, delta DailyUsageRow) error
	AddUsageEvent(ctx context.Context, event UsageEventRow) error
	AddUsageBatch(ctx context.Context, batch []usagePersistRecord) error

	BuildUsageStatisticsSnapshot(ctx context.Context) (internalusage.StatisticsSnapshot, error)
	ImportUsageStatisticsSnapshot(ctx context.Context, snapshot internalusage.StatisticsSnapshot) (internalusage.MergeResult, error)

	GetDailyUsageReport(ctx context.Context, apiKey, dayKey string) (DailyUsageReport, error)
	ListDailyUsageRows(ctx context.Context, startDay, endDay string) ([]DailyUsageRow, error)
	ListDailyUsageRowsByAPIKey(ctx context.Context, apiKey, startDay, endDayExclusive string) ([]DailyUsageRow, error)
	ListUsageEvents(ctx context.Context, startAt, endAt time.Time) ([]UsageEventRow, error)
	ListUsageEventsByAPIKey(ctx context.Context, apiKey string, startAt, endAt time.Time, limit int, desc bool) ([]UsageEventRow, error)
	GetLatestUsageEventTime(ctx context.Context, apiKey string) (time.Time, bool, error)
	ListUsageEventAggregateRows(ctx context.Context) ([]UsageEventAggregateRow, error)
}

// UsageEventReader is the minimal event source needed for budget replay.
type UsageEventReader interface {
	ListUsageEventsByAPIKey(ctx context.Context, apiKey string, startAt, endAt time.Time, limit int, desc bool) ([]UsageEventRow, error)
}
