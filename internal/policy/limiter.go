package policy

import "context"

// DailyLimiter defines the persistent per-day request counter used by API key policies.
type DailyLimiter interface {
	Close() error
	Consume(ctx context.Context, apiKey, model, dayKey string, limit int) (count int, allowed bool, err error)
	ListUsageCounts(ctx context.Context, apiKey, dayKey string) ([]DailyUsageCountRow, error)
}
