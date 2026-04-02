package billing

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/policy"
	internalusage "github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
)

type PostgresStoreConfig struct {
	DSN    string
	Schema string
}

type PostgresStore struct {
	db     *sql.DB
	schema string

	pendingMu       sync.RWMutex
	pendingProvider PendingUsageProvider
}

func NewPostgresStore(ctx context.Context, cfg PostgresStoreConfig) (*PostgresStore, error) {
	dsn := strings.TrimSpace(cfg.DSN)
	if dsn == "" {
		return nil, fmt.Errorf("billing postgres: DSN is required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("billing postgres: open database: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("billing postgres: ping database: %w", err)
	}
	store := &PostgresStore{db: db, schema: strings.TrimSpace(cfg.Schema)}
	if err := store.ensureSchema(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.syncDefaultModelPrices(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *PostgresStore) Close() error {
	if s == nil {
		return nil
	}
	if provider := s.getPendingUsageProvider(); provider != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := provider.Stop(ctx); err != nil {
			return err
		}
	}
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *PostgresStore) SetPendingUsageProvider(provider PendingUsageProvider) {
	if s == nil {
		return
	}
	s.pendingMu.Lock()
	s.pendingProvider = provider
	s.pendingMu.Unlock()
}

func (s *PostgresStore) getPendingUsageProvider() PendingUsageProvider {
	if s == nil {
		return nil
	}
	s.pendingMu.RLock()
	defer s.pendingMu.RUnlock()
	return s.pendingProvider
}

func (s *PostgresStore) ensureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("billing postgres: not initialized")
	}
	if s.schema != "" {
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, billingQuoteIdentifier(s.schema))); err != nil {
			return fmt.Errorf("billing postgres: create schema: %w", err)
		}
	}
	stmts := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			model TEXT PRIMARY KEY,
			prompt_micro_usd_per_1m BIGINT NOT NULL,
			completion_micro_usd_per_1m BIGINT NOT NULL,
			cached_micro_usd_per_1m BIGINT NOT NULL,
			updated_at BIGINT NOT NULL
		)`, s.table("model_prices")),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			api_key TEXT NOT NULL,
			model TEXT NOT NULL,
			day TEXT NOT NULL,
			requests BIGINT NOT NULL,
			failed_requests BIGINT NOT NULL,
			input_tokens BIGINT NOT NULL,
			output_tokens BIGINT NOT NULL,
			reasoning_tokens BIGINT NOT NULL,
			cached_tokens BIGINT NOT NULL,
			total_tokens BIGINT NOT NULL,
			cost_micro_usd BIGINT NOT NULL,
			updated_at BIGINT NOT NULL,
			PRIMARY KEY (api_key, model, day)
		)`, s.table("api_key_model_daily_usage")),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			id BIGSERIAL PRIMARY KEY,
			requested_at BIGINT NOT NULL,
			api_key TEXT NOT NULL,
			source TEXT NOT NULL,
			auth_index TEXT NOT NULL,
			model TEXT NOT NULL,
			failed BOOLEAN NOT NULL,
			input_tokens BIGINT NOT NULL,
			output_tokens BIGINT NOT NULL,
			reasoning_tokens BIGINT NOT NULL,
			cached_tokens BIGINT NOT NULL,
			total_tokens BIGINT NOT NULL,
			cost_micro_usd BIGINT NOT NULL,
			updated_at BIGINT NOT NULL
		)`, s.table("usage_events")),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (api_key, day)`, billingQuoteIdentifier("idx_api_key_model_daily_usage_api_day"), s.table("api_key_model_daily_usage")),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (requested_at)`, billingQuoteIdentifier("idx_usage_events_requested_at"), s.table("usage_events")),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (source, requested_at)`, billingQuoteIdentifier("idx_usage_events_source_requested_at"), s.table("usage_events")),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (auth_index, requested_at)`, billingQuoteIdentifier("idx_usage_events_auth_index_requested_at"), s.table("usage_events")),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (api_key, requested_at)`, billingQuoteIdentifier("idx_usage_events_api_key_requested_at"), s.table("usage_events")),
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("billing postgres: ensure schema: %w", err)
		}
	}
	return nil
}

func (s *PostgresStore) UpsertModelPrice(ctx context.Context, model string, price PriceMicroUSDPer1M) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("billing postgres: not initialized")
	}
	key := policy.NormaliseModelKey(model)
	if key == "" {
		return fmt.Errorf("billing postgres: model is required")
	}
	if price.Prompt < 0 || price.Completion < 0 || price.Cached < 0 {
		return fmt.Errorf("billing postgres: invalid price")
	}
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (model, prompt_micro_usd_per_1m, completion_micro_usd_per_1m, cached_micro_usd_per_1m, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT(model) DO UPDATE SET
			prompt_micro_usd_per_1m = EXCLUDED.prompt_micro_usd_per_1m,
			completion_micro_usd_per_1m = EXCLUDED.completion_micro_usd_per_1m,
			cached_micro_usd_per_1m = EXCLUDED.cached_micro_usd_per_1m,
			updated_at = EXCLUDED.updated_at
	`, s.table("model_prices")), key, price.Prompt, price.Completion, price.Cached, nowUnixUTC())
	if err != nil {
		return fmt.Errorf("billing postgres: upsert model price: %w", err)
	}
	if err := s.recalculateUsageCosts(ctx, key); err != nil {
		return err
	}
	return nil
}

func (s *PostgresStore) DeleteModelPrice(ctx context.Context, model string) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("billing postgres: not initialized")
	}
	key := policy.NormaliseModelKey(model)
	if key == "" {
		return false, fmt.Errorf("billing postgres: model is required")
	}
	res, err := s.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE model = $1`, s.table("model_prices")), key)
	if err != nil {
		return false, fmt.Errorf("billing postgres: delete model price: %w", err)
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		if err := s.recalculateUsageCosts(ctx, key); err != nil {
			return false, err
		}
	}
	return n > 0, nil
}

func (s *PostgresStore) getSavedPriceMicro(ctx context.Context, modelKey string) (PriceMicroUSDPer1M, bool, int64, error) {
	if s == nil || s.db == nil {
		return PriceMicroUSDPer1M{}, false, 0, fmt.Errorf("billing postgres: not initialized")
	}
	row := s.db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT prompt_micro_usd_per_1m, completion_micro_usd_per_1m, cached_micro_usd_per_1m, updated_at
		FROM %s WHERE model = $1
	`, s.table("model_prices")), modelKey)
	var p, c, cached, updated int64
	if err := row.Scan(&p, &c, &cached, &updated); err != nil {
		if err == sql.ErrNoRows {
			return PriceMicroUSDPer1M{}, false, 0, nil
		}
		return PriceMicroUSDPer1M{}, false, 0, fmt.Errorf("billing postgres: query price: %w", err)
	}
	return PriceMicroUSDPer1M{Prompt: p, Completion: c, Cached: cached}, true, updated, nil
}

func (s *PostgresStore) ResolvePriceMicro(ctx context.Context, model string) (price PriceMicroUSDPer1M, source string, updatedAt int64, err error) {
	modelKey := policy.NormaliseModelKey(model)
	if modelKey == "" {
		return PriceMicroUSDPer1M{}, "", 0, fmt.Errorf("billing postgres: model is required")
	}
	baseKey := policy.StripThinkingVariant(modelKey)
	if price, ok, updatedAt, errSaved := s.getSavedPriceMicro(ctx, modelKey); errSaved != nil {
		return PriceMicroUSDPer1M{}, "", 0, errSaved
	} else if ok {
		return price, "saved", updatedAt, nil
	}
	if baseKey != "" && baseKey != modelKey {
		if price, ok, updatedAt, errSaved := s.getSavedPriceMicro(ctx, baseKey); errSaved != nil {
			return PriceMicroUSDPer1M{}, "", 0, errSaved
		} else if ok {
			return price, "saved", updatedAt, nil
		}
	}
	if price, ok := ResolveDefaultPrice(modelKey); ok {
		return price, "default", 0, nil
	}
	return PriceMicroUSDPer1M{}, "missing", 0, nil
}

func (s *PostgresStore) ListModelPrices(ctx context.Context) ([]ModelPrice, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("billing postgres: not initialized")
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT model, prompt_micro_usd_per_1m, completion_micro_usd_per_1m, cached_micro_usd_per_1m, updated_at
		FROM %s
		ORDER BY model ASC
	`, s.table("model_prices")))
	if err != nil {
		return nil, fmt.Errorf("billing postgres: list model prices: %w", err)
	}
	defer rows.Close()
	result := make([]ModelPrice, 0)
	for rows.Next() {
		var model string
		var p, c, cached, updated int64
		if err := rows.Scan(&model, &p, &c, &cached, &updated); err != nil {
			return nil, fmt.Errorf("billing postgres: scan model price: %w", err)
		}
		result = append(result, ModelPrice{
			Model:              model,
			PromptUSDPer1M:     MicroUSDPer1MToUSDPer1M(p),
			CompletionUSDPer1M: MicroUSDPer1MToUSDPer1M(c),
			CachedUSDPer1M:     MicroUSDPer1MToUSDPer1M(cached),
			Source:             "saved",
			UpdatedAt:          updated,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("billing postgres: list model prices rows: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) AddUsage(ctx context.Context, apiKey, model, dayKey string, delta DailyUsageRow) error {
	return s.addUsageExec(ctx, s.db, apiKey, model, dayKey, delta)
}

func (s *PostgresStore) addUsageExec(ctx context.Context, exec interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, apiKey, model, dayKey string, delta DailyUsageRow) error {
	if exec == nil {
		return fmt.Errorf("billing postgres: executor is required")
	}
	apiKey = strings.TrimSpace(apiKey)
	model = policy.NormaliseModelKey(model)
	dayKey = strings.TrimSpace(dayKey)
	if apiKey == "" || model == "" || dayKey == "" {
		return fmt.Errorf("billing postgres: invalid inputs")
	}
	if delta.Requests < 0 || delta.FailedRequests < 0 || delta.InputTokens < 0 || delta.OutputTokens < 0 ||
		delta.ReasoningTokens < 0 || delta.CachedTokens < 0 || delta.TotalTokens < 0 || delta.CostMicroUSD < 0 {
		return fmt.Errorf("billing postgres: invalid request deltas")
	}
	_, err := exec.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (
			api_key, model, day, requests, failed_requests, input_tokens, output_tokens,
			reasoning_tokens, cached_tokens, total_tokens, cost_micro_usd, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT(api_key, model, day) DO UPDATE SET
			requests = %s.requests + EXCLUDED.requests,
			failed_requests = %s.failed_requests + EXCLUDED.failed_requests,
			input_tokens = %s.input_tokens + EXCLUDED.input_tokens,
			output_tokens = %s.output_tokens + EXCLUDED.output_tokens,
			reasoning_tokens = %s.reasoning_tokens + EXCLUDED.reasoning_tokens,
			cached_tokens = %s.cached_tokens + EXCLUDED.cached_tokens,
			total_tokens = %s.total_tokens + EXCLUDED.total_tokens,
			cost_micro_usd = %s.cost_micro_usd + EXCLUDED.cost_micro_usd,
			updated_at = EXCLUDED.updated_at
	`, s.table("api_key_model_daily_usage"),
		s.table("api_key_model_daily_usage"), s.table("api_key_model_daily_usage"), s.table("api_key_model_daily_usage"),
		s.table("api_key_model_daily_usage"), s.table("api_key_model_daily_usage"), s.table("api_key_model_daily_usage"),
		s.table("api_key_model_daily_usage"), s.table("api_key_model_daily_usage")),
		apiKey, model, dayKey, delta.Requests, delta.FailedRequests, delta.InputTokens, delta.OutputTokens,
		delta.ReasoningTokens, delta.CachedTokens, delta.TotalTokens, delta.CostMicroUSD, nowUnixUTC(),
	)
	if err != nil {
		return fmt.Errorf("billing postgres: add usage: %w", err)
	}
	return nil
}

func (s *PostgresStore) AddUsageEvent(ctx context.Context, event UsageEventRow) error {
	return s.addUsageEventExec(ctx, s.db, event)
}

func (s *PostgresStore) addUsageEventExec(ctx context.Context, exec interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, event UsageEventRow) error {
	if exec == nil {
		return fmt.Errorf("billing postgres: executor is required")
	}
	if event.RequestedAt == 0 || strings.TrimSpace(event.APIKey) == "" || policy.NormaliseModelKey(event.Model) == "" {
		return fmt.Errorf("billing postgres: invalid inputs")
	}
	_, err := exec.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (
			requested_at, api_key, source, auth_index, model, failed, input_tokens, output_tokens,
			reasoning_tokens, cached_tokens, total_tokens, cost_micro_usd, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`, s.table("usage_events")),
		event.RequestedAt,
		strings.TrimSpace(event.APIKey),
		strings.TrimSpace(event.Source),
		strings.TrimSpace(event.AuthIndex),
		policy.NormaliseModelKey(event.Model),
		event.Failed,
		max64(0, event.InputTokens),
		max64(0, event.OutputTokens),
		max64(0, event.ReasoningTokens),
		max64(0, event.CachedTokens),
		max64(0, event.TotalTokens),
		max64(0, event.CostMicroUSD),
		nowUnixUTC(),
	)
	if err != nil {
		return fmt.Errorf("billing postgres: add usage event: %w", err)
	}
	return nil
}

func (s *PostgresStore) AddUsageBatch(ctx context.Context, batch []usagePersistRecord) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("billing postgres: not initialized")
	}
	if len(batch) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("billing postgres: begin usage batch: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, item := range batch {
		if err := s.addUsageExec(ctx, tx, item.delta.APIKey, item.delta.Model, item.dayKey, item.delta); err != nil {
			return err
		}
		if err := s.addUsageEventExec(ctx, tx, item.event); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("billing postgres: commit usage batch: %w", err)
	}
	return nil
}

func (s *PostgresStore) GetDailyCostMicroUSD(ctx context.Context, apiKey, dayKey string) (int64, error) {
	return s.sumCost(ctx, fmt.Sprintf(`SELECT COALESCE(SUM(cost_micro_usd), 0) FROM %s WHERE api_key = $1 AND day = $2`, s.table("api_key_model_daily_usage")), strings.TrimSpace(apiKey), strings.TrimSpace(dayKey))
}

func (s *PostgresStore) GetCostMicroUSDByDayRange(ctx context.Context, apiKey, startDay, endDayExclusive string) (int64, error) {
	return s.sumCost(ctx, fmt.Sprintf(`SELECT COALESCE(SUM(cost_micro_usd), 0) FROM %s WHERE api_key = $1 AND day >= $2 AND day < $3`, s.table("api_key_model_daily_usage")), strings.TrimSpace(apiKey), strings.TrimSpace(startDay), strings.TrimSpace(endDayExclusive))
}

func (s *PostgresStore) GetCostMicroUSDByTimeRange(ctx context.Context, apiKey string, startInclusive, endExclusive time.Time) (int64, error) {
	return s.sumCost(ctx, fmt.Sprintf(`SELECT COALESCE(SUM(cost_micro_usd), 0) FROM %s WHERE api_key = $1 AND requested_at >= $2 AND requested_at < $3`, s.table("usage_events")), strings.TrimSpace(apiKey), startInclusive.Unix(), endExclusive.Unix())
}

func (s *PostgresStore) GetCostMicroUSDByModelPrefix(ctx context.Context, apiKey, modelPrefix string) (int64, error) {
	return s.sumCost(ctx, fmt.Sprintf(`SELECT COALESCE(SUM(cost_micro_usd), 0) FROM %s WHERE api_key = $1 AND model LIKE $2`, s.table("usage_events")), strings.TrimSpace(apiKey), strings.ToLower(strings.TrimSpace(modelPrefix))+"%")
}

func (s *PostgresStore) sumCost(ctx context.Context, query string, args ...any) (int64, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("billing postgres: not initialized")
	}
	var total int64
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("billing postgres: sum cost: %w", err)
	}
	if provider := s.getPendingUsageProvider(); provider != nil && len(args) >= 2 {
		switch {
		case strings.Contains(query, "day ="):
			total += provider.PendingDailyCostMicroUSD(args[0].(string), args[1].(string))
		case strings.Contains(query, "day >="):
			total += provider.PendingCostMicroUSDByDayRange(args[0].(string), args[1].(string), args[2].(string))
		case strings.Contains(query, "requested_at >="):
			total += provider.PendingCostMicroUSDByTimeRange(args[0].(string), time.Unix(args[1].(int64), 0), time.Unix(args[2].(int64), 0))
		case strings.Contains(query, "model LIKE"):
			total += provider.PendingCostMicroUSDByModelPrefix(args[0].(string), strings.TrimSuffix(args[1].(string), "%"))
		}
	}
	return total, nil
}

func (s *PostgresStore) BuildUsageStatisticsSnapshot(ctx context.Context) (internalusage.StatisticsSnapshot, error) {
	snapshot := internalusage.StatisticsSnapshot{
		APIs:           map[string]internalusage.APISnapshot{},
		RequestsByDay:  map[string]int64{},
		RequestsByHour: map[string]int64{},
		TokensByDay:    map[string]int64{},
		TokensByHour:   map[string]int64{},
	}
	if s == nil || s.db == nil {
		return snapshot, fmt.Errorf("billing postgres: not initialized")
	}
	eventRows, err := s.ListUsageEvents(ctx, time.Time{}, time.Time{})
	if err != nil {
		return snapshot, err
	}
	eventDaily := make(map[string]DailyUsageRow, len(eventRows))
	for _, row := range eventRows {
		addUsageEventToSnapshot(&snapshot, row)
		dayKey := policy.DayKeyChina(time.Unix(row.RequestedAt, 0))
		key := usageAggregateKey(row.APIKey, row.Model, dayKey)
		agg := eventDaily[key]
		agg.APIKey = strings.TrimSpace(row.APIKey)
		agg.Model = policy.NormaliseModelKey(row.Model)
		agg.Day = dayKey
		agg.Requests++
		if row.Failed {
			agg.FailedRequests++
		}
		agg.InputTokens += max64(0, row.InputTokens)
		agg.OutputTokens += max64(0, row.OutputTokens)
		agg.ReasoningTokens += max64(0, row.ReasoningTokens)
		agg.CachedTokens += max64(0, row.CachedTokens)
		agg.TotalTokens += max64(0, row.TotalTokens)
		agg.CostMicroUSD += max64(0, row.CostMicroUSD)
		eventDaily[key] = agg
	}
	dailyRows, err := s.ListDailyUsageRows(ctx, "", "")
	if err != nil {
		return snapshot, err
	}
	for _, row := range dailyRows {
		key := usageAggregateKey(row.APIKey, row.Model, row.Day)
		existing := eventDaily[key]
		delta := DailyUsageRow{
			APIKey:          strings.TrimSpace(row.APIKey),
			Model:           policy.NormaliseModelKey(row.Model),
			Day:             strings.TrimSpace(row.Day),
			Requests:        max64(0, row.Requests-existing.Requests),
			FailedRequests:  max64(0, row.FailedRequests-existing.FailedRequests),
			InputTokens:     max64(0, row.InputTokens-existing.InputTokens),
			OutputTokens:    max64(0, row.OutputTokens-existing.OutputTokens),
			ReasoningTokens: max64(0, row.ReasoningTokens-existing.ReasoningTokens),
			CachedTokens:    max64(0, row.CachedTokens-existing.CachedTokens),
			TotalTokens:     max64(0, row.TotalTokens-existing.TotalTokens),
			CostMicroUSD:    max64(0, row.CostMicroUSD-existing.CostMicroUSD),
		}
		if delta.Requests == 0 && delta.FailedRequests == 0 && delta.TotalTokens == 0 &&
			delta.InputTokens == 0 && delta.OutputTokens == 0 && delta.ReasoningTokens == 0 &&
			delta.CachedTokens == 0 && delta.CostMicroUSD == 0 {
			continue
		}
		addDailyDeltaToSnapshot(&snapshot, delta)
	}
	if provider := s.getPendingUsageProvider(); provider != nil {
		provider.MergePendingSnapshot(&snapshot)
	}
	return snapshot, nil
}

func (s *PostgresStore) ImportUsageStatisticsSnapshot(ctx context.Context, snapshot internalusage.StatisticsSnapshot) (internalusage.MergeResult, error) {
	result := internalusage.MergeResult{}
	if s == nil || s.db == nil {
		return result, fmt.Errorf("billing postgres: not initialized")
	}
	current, err := s.BuildUsageStatisticsSnapshot(ctx)
	if err != nil {
		return result, err
	}
	seen := buildUsageSnapshotDedupSet(current)
	pendingDaily := map[string]DailyUsageRow{}
	for apiKey, apiSnapshot := range snapshot.APIs {
		apiKey = strings.TrimSpace(apiKey)
		if apiKey == "" {
			continue
		}
		for modelName, modelSnapshot := range apiSnapshot.Models {
			modelKey := policy.NormaliseModelKey(modelName)
			if modelKey == "" {
				modelKey = "unknown"
			}
			for _, detail := range modelSnapshot.Details {
				normalized := normalizeSnapshotDetail(detail)
				key := usageSnapshotDedupKey(apiKey, modelKey, normalized)
				if _, exists := seen[key]; exists {
					result.Skipped++
					continue
				}
				seen[key] = struct{}{}
				requestCount := usageSnapshotRequestCount(normalized)
				_, failureCount := usageSnapshotOutcomeCounts(normalized)
				dayKey := policy.DayKeyChina(normalized.Timestamp)
				price, _, _, errPrice := s.ResolvePriceMicro(ctx, modelKey)
				if errPrice != nil {
					return result, errPrice
				}
				cost := calculateUsageCostMicro(
					normalized.Tokens.InputTokens,
					normalized.Tokens.OutputTokens,
					normalized.Tokens.ReasoningTokens,
					normalized.Tokens.CachedTokens,
					price,
				)
				delta := DailyUsageRow{
					APIKey:          apiKey,
					Model:           modelKey,
					Day:             dayKey,
					Requests:        requestCount,
					FailedRequests:  failureCount,
					InputTokens:     max64(0, normalized.Tokens.InputTokens),
					OutputTokens:    max64(0, normalized.Tokens.OutputTokens),
					ReasoningTokens: max64(0, normalized.Tokens.ReasoningTokens),
					CachedTokens:    max64(0, normalized.Tokens.CachedTokens),
					TotalTokens:     max64(0, normalized.Tokens.TotalTokens),
					CostMicroUSD:    max64(0, cost),
				}
				if requestCount == 1 {
					if err := s.AddUsageEvent(ctx, UsageEventRow{
						RequestedAt:     normalized.Timestamp.Unix(),
						APIKey:          apiKey,
						Source:          strings.TrimSpace(normalized.Source),
						AuthIndex:       strings.TrimSpace(normalized.AuthIndex),
						Model:           modelKey,
						Failed:          failureCount > 0,
						InputTokens:     delta.InputTokens,
						OutputTokens:    delta.OutputTokens,
						ReasoningTokens: delta.ReasoningTokens,
						CachedTokens:    delta.CachedTokens,
						TotalTokens:     delta.TotalTokens,
						CostMicroUSD:    delta.CostMicroUSD,
					}); err != nil {
						return result, err
					}
				}
				pendingKey := usageAggregateKey(apiKey, modelKey, dayKey)
				acc := pendingDaily[pendingKey]
				acc.APIKey = apiKey
				acc.Model = modelKey
				acc.Day = dayKey
				acc.Requests += delta.Requests
				acc.FailedRequests += delta.FailedRequests
				acc.InputTokens += delta.InputTokens
				acc.OutputTokens += delta.OutputTokens
				acc.ReasoningTokens += delta.ReasoningTokens
				acc.CachedTokens += delta.CachedTokens
				acc.TotalTokens += delta.TotalTokens
				acc.CostMicroUSD += delta.CostMicroUSD
				pendingDaily[pendingKey] = acc
				result.Added++
			}
		}
	}
	for _, delta := range pendingDaily {
		if err := s.AddUsage(ctx, delta.APIKey, delta.Model, delta.Day, delta); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (s *PostgresStore) GetDailyUsageReport(ctx context.Context, apiKey, dayKey string) (DailyUsageReport, error) {
	report := DailyUsageReport{APIKey: strings.TrimSpace(apiKey), Day: strings.TrimSpace(dayKey), GeneratedAtUnix: nowUnixUTC()}
	if report.APIKey == "" || report.Day == "" {
		return report, fmt.Errorf("billing postgres: api_key and day are required")
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT api_key, model, day, requests, failed_requests, input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens, cost_micro_usd, updated_at
		FROM %s
		WHERE api_key = $1 AND day = $2
		ORDER BY model ASC
	`, s.table("api_key_model_daily_usage")), report.APIKey, report.Day)
	if err != nil {
		return report, fmt.Errorf("billing postgres: query daily usage: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row DailyUsageRow
		if err := rows.Scan(&row.APIKey, &row.Model, &row.Day, &row.Requests, &row.FailedRequests, &row.InputTokens, &row.OutputTokens, &row.ReasoningTokens, &row.CachedTokens, &row.TotalTokens, &row.CostMicroUSD, &row.UpdatedAt); err != nil {
			return report, fmt.Errorf("billing postgres: scan daily usage: %w", err)
		}
		report.Models = append(report.Models, row)
		report.TotalCostMicro += row.CostMicroUSD
		report.TotalRequests += row.Requests
		report.TotalFailed += row.FailedRequests
		report.TotalTokens += row.TotalTokens
	}
	if err := rows.Err(); err != nil {
		return report, fmt.Errorf("billing postgres: daily usage rows: %w", err)
	}
	if provider := s.getPendingUsageProvider(); provider != nil {
		for _, row := range provider.PendingDailyUsageRows(report.APIKey, report.Day) {
			report.Models = append(report.Models, row)
			report.TotalCostMicro += row.CostMicroUSD
			report.TotalRequests += row.Requests
			report.TotalFailed += row.FailedRequests
			report.TotalTokens += row.TotalTokens
		}
	}
	report.TotalCostUSD = microUSDToUSD(report.TotalCostMicro)
	return report, nil
}

func (s *PostgresStore) ListDailyUsageRows(ctx context.Context, startDay, endDay string) ([]DailyUsageRow, error) {
	query := fmt.Sprintf(`SELECT api_key, model, day, requests, failed_requests, input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens, cost_micro_usd, updated_at FROM %s`, s.table("api_key_model_daily_usage"))
	args := make([]any, 0, 2)
	conds := make([]string, 0, 2)
	if startDay = strings.TrimSpace(startDay); startDay != "" {
		conds = append(conds, fmt.Sprintf("day >= $%d", len(args)+1))
		args = append(args, startDay)
	}
	if endDay = strings.TrimSpace(endDay); endDay != "" {
		conds = append(conds, fmt.Sprintf("day < $%d", len(args)+1))
		args = append(args, endDay)
	}
	if len(conds) > 0 {
		query += " WHERE " + strings.Join(conds, " AND ")
	}
	query += " ORDER BY day ASC, api_key ASC, model ASC"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("billing postgres: list daily usage rows: %w", err)
	}
	defer rows.Close()
	result := make([]DailyUsageRow, 0)
	for rows.Next() {
		var row DailyUsageRow
		if err := rows.Scan(&row.APIKey, &row.Model, &row.Day, &row.Requests, &row.FailedRequests, &row.InputTokens, &row.OutputTokens, &row.ReasoningTokens, &row.CachedTokens, &row.TotalTokens, &row.CostMicroUSD, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("billing postgres: scan daily usage row: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("billing postgres: daily usage rows: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) ListDailyUsageRowsByAPIKey(ctx context.Context, apiKey, startDay, endDayExclusive string) ([]DailyUsageRow, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("billing postgres: not initialized")
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("billing postgres: api key is required")
	}

	query := fmt.Sprintf(`
		SELECT api_key, model, day, requests, failed_requests, input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens, cost_micro_usd, updated_at
		FROM %s
		WHERE api_key = $1
	`, s.table("api_key_model_daily_usage"))
	args := []any{apiKey}
	if trimmed := strings.TrimSpace(startDay); trimmed != "" {
		query += fmt.Sprintf(" AND day >= $%d", len(args)+1)
		args = append(args, trimmed)
	}
	if trimmed := strings.TrimSpace(endDayExclusive); trimmed != "" {
		query += fmt.Sprintf(" AND day < $%d", len(args)+1)
		args = append(args, trimmed)
	}
	query += " ORDER BY day ASC, model ASC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("billing postgres: list daily usage rows by api key: %w", err)
	}
	defer rows.Close()

	result := make([]DailyUsageRow, 0)
	indexByKey := make(map[string]int)
	for rows.Next() {
		var row DailyUsageRow
		if err := rows.Scan(&row.APIKey, &row.Model, &row.Day, &row.Requests, &row.FailedRequests, &row.InputTokens, &row.OutputTokens, &row.ReasoningTokens, &row.CachedTokens, &row.TotalTokens, &row.CostMicroUSD, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("billing postgres: scan daily usage row: %w", err)
		}
		key := row.Day + "|" + row.Model
		indexByKey[key] = len(result)
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("billing postgres: daily usage rows by api key: %w", err)
	}
	if provider := s.getPendingUsageProvider(); provider != nil {
		for _, row := range provider.PendingDailyUsageRowsByRange(apiKey, startDay, endDayExclusive) {
			key := row.Day + "|" + row.Model
			if idx, ok := indexByKey[key]; ok {
				result[idx].Requests += row.Requests
				result[idx].FailedRequests += row.FailedRequests
				result[idx].InputTokens += row.InputTokens
				result[idx].OutputTokens += row.OutputTokens
				result[idx].ReasoningTokens += row.ReasoningTokens
				result[idx].CachedTokens += row.CachedTokens
				result[idx].TotalTokens += row.TotalTokens
				result[idx].CostMicroUSD += row.CostMicroUSD
				if row.UpdatedAt > result[idx].UpdatedAt {
					result[idx].UpdatedAt = row.UpdatedAt
				}
				continue
			}
			indexByKey[key] = len(result)
			result = append(result, row)
		}
		sort.Slice(result, func(i, j int) bool {
			if result[i].Day == result[j].Day {
				return result[i].Model < result[j].Model
			}
			return result[i].Day < result[j].Day
		})
	}
	return result, nil
}

func (s *PostgresStore) ListUsageEvents(ctx context.Context, startAt, endAt time.Time) ([]UsageEventRow, error) {
	return s.listUsageEvents(ctx, "", startAt, endAt, 0, false)
}

func (s *PostgresStore) ListUsageEventsByAPIKey(ctx context.Context, apiKey string, startAt, endAt time.Time, limit int, desc bool) ([]UsageEventRow, error) {
	return s.listUsageEvents(ctx, strings.TrimSpace(apiKey), startAt, endAt, limit, desc)
}

func (s *PostgresStore) listUsageEvents(ctx context.Context, apiKey string, startAt, endAt time.Time, limit int, desc bool) ([]UsageEventRow, error) {
	query := fmt.Sprintf(`SELECT id, requested_at, api_key, source, auth_index, model, failed, input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens, cost_micro_usd, updated_at FROM %s WHERE 1=1`, s.table("usage_events"))
	args := make([]any, 0, 4)
	if apiKey != "" {
		args = append(args, apiKey)
		query += fmt.Sprintf(" AND api_key = $%d", len(args))
	}
	if !startAt.IsZero() {
		args = append(args, startAt.Unix())
		query += fmt.Sprintf(" AND requested_at >= $%d", len(args))
	}
	if !endAt.IsZero() {
		args = append(args, endAt.Unix())
		query += fmt.Sprintf(" AND requested_at < $%d", len(args))
	}
	order := "ASC"
	if desc {
		order = "DESC"
	}
	query += " ORDER BY requested_at " + order + ", id " + order
	if limit > 0 {
		args = append(args, limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("billing postgres: list usage events: %w", err)
	}
	defer rows.Close()
	result := make([]UsageEventRow, 0)
	for rows.Next() {
		var row UsageEventRow
		if err := rows.Scan(&row.ID, &row.RequestedAt, &row.APIKey, &row.Source, &row.AuthIndex, &row.Model, &row.Failed, &row.InputTokens, &row.OutputTokens, &row.ReasoningTokens, &row.CachedTokens, &row.TotalTokens, &row.CostMicroUSD, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("billing postgres: scan usage event: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("billing postgres: usage event rows: %w", err)
	}
	if provider := s.getPendingUsageProvider(); provider != nil {
		result = append(result, provider.PendingUsageEvents(apiKey, startAt, endAt, 0, desc)...)
		sort.Slice(result, func(i, j int) bool {
			if result[i].RequestedAt == result[j].RequestedAt {
				if desc {
					return result[i].ID > result[j].ID
				}
				return result[i].ID < result[j].ID
			}
			if desc {
				return result[i].RequestedAt > result[j].RequestedAt
			}
			return result[i].RequestedAt < result[j].RequestedAt
		})
		if limit > 0 && len(result) > limit {
			result = result[:limit]
		}
	}
	return result, nil
}

func (s *PostgresStore) GetLatestUsageEventTime(ctx context.Context, apiKey string) (time.Time, bool, error) {
	if s == nil || s.db == nil {
		return time.Time{}, false, fmt.Errorf("billing postgres: not initialized")
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return time.Time{}, false, fmt.Errorf("billing postgres: api key is required")
	}
	var unix int64
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COALESCE(MAX(requested_at), 0) FROM %s WHERE api_key = $1`, s.table("usage_events")), apiKey).Scan(&unix); err != nil {
		return time.Time{}, false, fmt.Errorf("billing postgres: latest usage event time: %w", err)
	}
	if provider := s.getPendingUsageProvider(); provider != nil {
		if pending := provider.PendingLatestRequestedAt(apiKey); pending > unix {
			unix = pending
		}
	}
	if unix <= 0 {
		return time.Time{}, false, nil
	}
	return time.Unix(unix, 0), true, nil
}

func (s *PostgresStore) ListUsageEventAggregateRows(ctx context.Context) ([]UsageEventAggregateRow, error) {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT source, auth_index,
			COALESCE(SUM(CASE WHEN failed THEN 0 ELSE 1 END), 0) AS success_count,
			COALESCE(SUM(CASE WHEN failed THEN 1 ELSE 0 END), 0) AS failure_count
		FROM %s
		GROUP BY source, auth_index
		ORDER BY source ASC, auth_index ASC
	`, s.table("usage_events")))
	if err != nil {
		return nil, fmt.Errorf("billing postgres: list usage event aggregates: %w", err)
	}
	defer rows.Close()
	result := make([]UsageEventAggregateRow, 0)
	for rows.Next() {
		var row UsageEventAggregateRow
		if err := rows.Scan(&row.Source, &row.AuthIndex, &row.SuccessCount, &row.FailureCount); err != nil {
			return nil, fmt.Errorf("billing postgres: scan usage event aggregate: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("billing postgres: usage event aggregate rows: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) syncDefaultModelPrices(ctx context.Context) error {
	if len(DefaultPrices) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("billing postgres: begin default price sync: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (model, prompt_micro_usd_per_1m, completion_micro_usd_per_1m, cached_micro_usd_per_1m, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT(model) DO NOTHING
	`, s.table("model_prices")))
	if err != nil {
		return fmt.Errorf("billing postgres: prepare default price sync: %w", err)
	}
	defer stmt.Close()
	now := nowUnixUTC()
	for model, price := range DefaultPrices {
		if _, err := stmt.ExecContext(ctx, model, price.Prompt, price.Completion, price.Cached, now); err != nil {
			return fmt.Errorf("billing postgres: sync default price %q: %w", model, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("billing postgres: commit default price sync: %w", err)
	}
	return nil
}

func (s *PostgresStore) table(name string) string {
	if s.schema == "" {
		return billingQuoteIdentifier(name)
	}
	return billingQuoteIdentifier(s.schema) + "." + billingQuoteIdentifier(name)
}

func billingQuoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func (s *PostgresStore) recalculateUsageCosts(ctx context.Context, model string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("billing postgres: not initialized")
	}

	modelKey := strings.TrimSpace(model)
	query := fmt.Sprintf(`
		SELECT api_key, model, day, input_tokens, output_tokens, reasoning_tokens, cached_tokens
		FROM %s
	`, s.table("api_key_model_daily_usage"))
	args := []any{}
	if modelKey != "" {
		query += " WHERE model = $1"
		args = append(args, modelKey)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("billing postgres: query usage for cost repair: %w", err)
	}
	defer rows.Close()

	items := make([]usageCostRecalcRow, 0)
	models := map[string]struct{}{}
	for rows.Next() {
		var item usageCostRecalcRow
		if err := rows.Scan(
			&item.APIKey,
			&item.Model,
			&item.Day,
			&item.InputTokens,
			&item.OutputTokens,
			&item.ReasoningTokens,
			&item.CachedTokens,
		); err != nil {
			return fmt.Errorf("billing postgres: scan usage for cost repair: %w", err)
		}
		items = append(items, item)
		models[item.Model] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("billing postgres: usage rows for cost repair: %w", err)
	}
	if len(items) == 0 {
		return nil
	}

	priceByModel := make(map[string]PriceMicroUSDPer1M, len(models))
	for modelKey := range models {
		price, _, _, err := s.ResolvePriceMicro(ctx, modelKey)
		if err != nil {
			return err
		}
		priceByModel[modelKey] = price
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("billing postgres: begin cost repair: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET cost_micro_usd = $1, updated_at = $2
		WHERE api_key = $3 AND model = $4 AND day = $5
	`, s.table("api_key_model_daily_usage")))
	if err != nil {
		return fmt.Errorf("billing postgres: prepare cost repair: %w", err)
	}
	defer stmt.Close()

	now := nowUnixUTC()
	for _, item := range items {
		price := priceByModel[item.Model]
		cost := calculateUsageCostMicro(item.InputTokens, item.OutputTokens, item.ReasoningTokens, item.CachedTokens, price)
		if _, err := stmt.ExecContext(ctx, cost, now, item.APIKey, item.Model, item.Day); err != nil {
			return fmt.Errorf("billing postgres: update repaired cost: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("billing postgres: commit cost repair: %w", err)
	}
	return nil
}
