package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/apikeygroup"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/billing"
	_ "modernc.org/sqlite"
)

type migrateConfig struct {
	SQLitePath     string
	PostgresDSN    string
	PostgresSchema string
}

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		log.Fatalf("migration failed: %v", err)
	}
}

func parseFlags() migrateConfig {
	defaultDSN := strings.TrimSpace(os.Getenv("APIKEY_BILLING_PG_DSN"))
	if defaultDSN == "" {
		defaultDSN = strings.TrimSpace(os.Getenv("PGSTORE_DSN"))
	}
	defaultSchema := strings.TrimSpace(os.Getenv("APIKEY_BILLING_PG_SCHEMA"))
	if defaultSchema == "" {
		defaultSchema = strings.TrimSpace(os.Getenv("PGSTORE_SCHEMA"))
	}
	defaultSQLite := strings.TrimSpace(os.Getenv("APIKEY_POLICY_SQLITE_PATH"))
	if defaultSQLite == "" {
		defaultSQLite = "api_key_policy_limits.sqlite"
	}

	var cfg migrateConfig
	flag.StringVar(&cfg.SQLitePath, "sqlite", defaultSQLite, "Path to the source SQLite billing database")
	flag.StringVar(&cfg.PostgresDSN, "pg-dsn", defaultDSN, "Postgres DSN for the target billing database")
	flag.StringVar(&cfg.PostgresSchema, "pg-schema", defaultSchema, "Target Postgres schema")
	flag.Parse()
	return cfg
}

func run(cfg migrateConfig) error {
	cfg.SQLitePath = strings.TrimSpace(cfg.SQLitePath)
	cfg.PostgresDSN = strings.TrimSpace(cfg.PostgresDSN)
	cfg.PostgresSchema = strings.TrimSpace(cfg.PostgresSchema)
	if cfg.SQLitePath == "" {
		return fmt.Errorf("--sqlite is required")
	}
	if cfg.PostgresDSN == "" {
		return fmt.Errorf("--pg-dsn is required")
	}

	absSQLite, err := filepath.Abs(cfg.SQLitePath)
	if err != nil {
		return fmt.Errorf("resolve sqlite path: %w", err)
	}
	if _, err := os.Stat(absSQLite); err != nil {
		return fmt.Errorf("stat sqlite path: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	store, err := billing.NewPostgresStore(ctx, billing.PostgresStoreConfig{
		DSN:    cfg.PostgresDSN,
		Schema: cfg.PostgresSchema,
	})
	if err != nil {
		return fmt.Errorf("prepare postgres billing schema: %w", err)
	}
	if err := store.Close(); err != nil {
		return fmt.Errorf("close postgres billing bootstrap store: %w", err)
	}
	groupStore, err := apikeygroup.NewPostgresStore(ctx, apikeygroup.PostgresStoreConfig{
		DSN:    cfg.PostgresDSN,
		Schema: cfg.PostgresSchema,
	})
	if err != nil {
		return fmt.Errorf("prepare postgres api key group schema: %w", err)
	}
	if err := groupStore.SeedDefaults(ctx); err != nil {
		_ = groupStore.Close()
		return fmt.Errorf("seed default api key groups: %w", err)
	}
	if err := groupStore.Close(); err != nil {
		return fmt.Errorf("close postgres api key group bootstrap store: %w", err)
	}

	sqliteDB, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro", absSQLite))
	if err != nil {
		return fmt.Errorf("open sqlite database: %w", err)
	}
	defer sqliteDB.Close()
	if err := sqliteDB.PingContext(ctx); err != nil {
		return fmt.Errorf("ping sqlite database: %w", err)
	}

	pgDB, err := sql.Open("pgx", cfg.PostgresDSN)
	if err != nil {
		return fmt.Errorf("open postgres database: %w", err)
	}
	defer pgDB.Close()
	if err := pgDB.PingContext(ctx); err != nil {
		return fmt.Errorf("ping postgres database: %w", err)
	}

	m := migrator{
		sqlite: sqliteDB,
		pg:     pgDB,
		schema: cfg.PostgresSchema,
	}

	log.Printf("source sqlite: %s", absSQLite)
	if cfg.PostgresSchema == "" {
		log.Printf("target postgres schema: public")
	} else {
		log.Printf("target postgres schema: %s", cfg.PostgresSchema)
	}

	if err := m.migrateModelPrices(ctx); err != nil {
		return err
	}
	if err := m.migrateDailyUsage(ctx); err != nil {
		return err
	}
	if err := m.migrateUsageEvents(ctx); err != nil {
		return err
	}

	log.Printf("migration completed successfully")
	return nil
}

type migrator struct {
	sqlite *sql.DB
	pg     *sql.DB
	schema string
}

func (m migrator) migrateModelPrices(ctx context.Context) error {
	rows, err := m.sqlite.QueryContext(ctx, `
		SELECT model, prompt_micro_usd_per_1m, completion_micro_usd_per_1m, cached_micro_usd_per_1m, updated_at
		FROM model_prices
		ORDER BY model ASC
	`)
	if err != nil {
		return fmt.Errorf("query sqlite model_prices: %w", err)
	}
	defer rows.Close()

	tx, err := m.pg.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin postgres model_prices transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (model, prompt_micro_usd_per_1m, completion_micro_usd_per_1m, cached_micro_usd_per_1m, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT(model) DO UPDATE SET
			prompt_micro_usd_per_1m = EXCLUDED.prompt_micro_usd_per_1m,
			completion_micro_usd_per_1m = EXCLUDED.completion_micro_usd_per_1m,
			cached_micro_usd_per_1m = EXCLUDED.cached_micro_usd_per_1m,
			updated_at = EXCLUDED.updated_at
	`, m.table("model_prices")))
	if err != nil {
		return fmt.Errorf("prepare postgres model_prices statement: %w", err)
	}
	defer stmt.Close()

	count := 0
	for rows.Next() {
		var model string
		var prompt, completion, cached, updatedAt int64
		if err := rows.Scan(&model, &prompt, &completion, &cached, &updatedAt); err != nil {
			return fmt.Errorf("scan sqlite model_prices row: %w", err)
		}
		if _, err := stmt.ExecContext(ctx, model, prompt, completion, cached, updatedAt); err != nil {
			return fmt.Errorf("insert postgres model_prices row %q: %w", model, err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate sqlite model_prices rows: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit postgres model_prices transaction: %w", err)
	}

	log.Printf("migrated model_prices: %d rows", count)
	return nil
}

func (m migrator) migrateDailyUsage(ctx context.Context) error {
	rows, err := m.sqlite.QueryContext(ctx, `
		SELECT api_key, model, day, requests, failed_requests, input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens, cost_micro_usd, updated_at
		FROM api_key_model_daily_usage
		ORDER BY day ASC, api_key ASC, model ASC
	`)
	if err != nil {
		return fmt.Errorf("query sqlite api_key_model_daily_usage: %w", err)
	}
	defer rows.Close()

	tx, err := m.pg.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin postgres api_key_model_daily_usage transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (
			api_key, model, day, requests, failed_requests, input_tokens, output_tokens,
			reasoning_tokens, cached_tokens, total_tokens, cost_micro_usd, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT(api_key, model, day) DO UPDATE SET
			requests = EXCLUDED.requests,
			failed_requests = EXCLUDED.failed_requests,
			input_tokens = EXCLUDED.input_tokens,
			output_tokens = EXCLUDED.output_tokens,
			reasoning_tokens = EXCLUDED.reasoning_tokens,
			cached_tokens = EXCLUDED.cached_tokens,
			total_tokens = EXCLUDED.total_tokens,
			cost_micro_usd = EXCLUDED.cost_micro_usd,
			updated_at = EXCLUDED.updated_at
	`, m.table("api_key_model_daily_usage")))
	if err != nil {
		return fmt.Errorf("prepare postgres api_key_model_daily_usage statement: %w", err)
	}
	defer stmt.Close()

	count := 0
	for rows.Next() {
		var row struct {
			APIKey          string
			Model           string
			Day             string
			Requests        int64
			FailedRequests  int64
			InputTokens     int64
			OutputTokens    int64
			ReasoningTokens int64
			CachedTokens    int64
			TotalTokens     int64
			CostMicroUSD    int64
			UpdatedAt       int64
		}
		if err := rows.Scan(
			&row.APIKey,
			&row.Model,
			&row.Day,
			&row.Requests,
			&row.FailedRequests,
			&row.InputTokens,
			&row.OutputTokens,
			&row.ReasoningTokens,
			&row.CachedTokens,
			&row.TotalTokens,
			&row.CostMicroUSD,
			&row.UpdatedAt,
		); err != nil {
			return fmt.Errorf("scan sqlite api_key_model_daily_usage row: %w", err)
		}
		if _, err := stmt.ExecContext(
			ctx,
			row.APIKey,
			row.Model,
			row.Day,
			row.Requests,
			row.FailedRequests,
			row.InputTokens,
			row.OutputTokens,
			row.ReasoningTokens,
			row.CachedTokens,
			row.TotalTokens,
			row.CostMicroUSD,
			row.UpdatedAt,
		); err != nil {
			return fmt.Errorf("insert postgres api_key_model_daily_usage row %q/%q/%q: %w", row.APIKey, row.Model, row.Day, err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate sqlite api_key_model_daily_usage rows: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit postgres api_key_model_daily_usage transaction: %w", err)
	}

	log.Printf("migrated api_key_model_daily_usage: %d rows", count)
	return nil
}

func (m migrator) migrateUsageEvents(ctx context.Context) error {
	hasLatencyColumn, err := m.sqliteColumnExists(ctx, "usage_events", "latency_ms")
	if err != nil {
		return err
	}
	selectColumns := "id, requested_at, api_key, source, auth_index, model, failed, input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens, cost_micro_usd, updated_at"
	if hasLatencyColumn {
		selectColumns = "id, requested_at, api_key, source, auth_index, model, failed, latency_ms, input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens, cost_micro_usd, updated_at"
	}
	rows, err := m.sqlite.QueryContext(ctx, fmt.Sprintf(`
		SELECT %s
		FROM usage_events
		ORDER BY id ASC
	`, selectColumns))
	if err != nil {
		return fmt.Errorf("query sqlite usage_events: %w", err)
	}
	defer rows.Close()

	tx, err := m.pg.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin postgres usage_events transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (
			id, requested_at, api_key, source, auth_index, model, failed, latency_ms, input_tokens, output_tokens,
			reasoning_tokens, cached_tokens, total_tokens, cost_micro_usd, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		ON CONFLICT(id) DO UPDATE SET
			requested_at = EXCLUDED.requested_at,
			api_key = EXCLUDED.api_key,
			source = EXCLUDED.source,
			auth_index = EXCLUDED.auth_index,
			model = EXCLUDED.model,
			failed = EXCLUDED.failed,
			latency_ms = EXCLUDED.latency_ms,
			input_tokens = EXCLUDED.input_tokens,
			output_tokens = EXCLUDED.output_tokens,
			reasoning_tokens = EXCLUDED.reasoning_tokens,
			cached_tokens = EXCLUDED.cached_tokens,
			total_tokens = EXCLUDED.total_tokens,
			cost_micro_usd = EXCLUDED.cost_micro_usd,
			updated_at = EXCLUDED.updated_at
	`, m.table("usage_events")))
	if err != nil {
		return fmt.Errorf("prepare postgres usage_events statement: %w", err)
	}
	defer stmt.Close()

	count := 0
	var maxID int64
	for rows.Next() {
		var row struct {
			ID              int64
			RequestedAt     int64
			APIKey          string
			Source          string
			AuthIndex       string
			Model           string
			Failed          bool
			LatencyMs       int64
			InputTokens     int64
			OutputTokens    int64
			ReasoningTokens int64
			CachedTokens    int64
			TotalTokens     int64
			CostMicroUSD    int64
			UpdatedAt       int64
		}
		scanArgs := []any{
			&row.ID,
			&row.RequestedAt,
			&row.APIKey,
			&row.Source,
			&row.AuthIndex,
			&row.Model,
			&row.Failed,
		}
		if hasLatencyColumn {
			scanArgs = append(scanArgs, &row.LatencyMs)
		}
		scanArgs = append(scanArgs,
			&row.InputTokens,
			&row.OutputTokens,
			&row.ReasoningTokens,
			&row.CachedTokens,
			&row.TotalTokens,
			&row.CostMicroUSD,
			&row.UpdatedAt,
		)
		if err := rows.Scan(scanArgs...); err != nil {
			return fmt.Errorf("scan sqlite usage_events row: %w", err)
		}
		if _, err := stmt.ExecContext(
			ctx,
			row.ID,
			row.RequestedAt,
			row.APIKey,
			row.Source,
			row.AuthIndex,
			row.Model,
			row.Failed,
			row.LatencyMs,
			row.InputTokens,
			row.OutputTokens,
			row.ReasoningTokens,
			row.CachedTokens,
			row.TotalTokens,
			row.CostMicroUSD,
			row.UpdatedAt,
		); err != nil {
			return fmt.Errorf("insert postgres usage_events row id=%d: %w", row.ID, err)
		}
		count++
		if row.ID > maxID {
			maxID = row.ID
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate sqlite usage_events rows: %w", err)
	}
	if err := m.bumpUsageEventSequence(ctx, tx, maxID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit postgres usage_events transaction: %w", err)
	}

	log.Printf("migrated usage_events: %d rows", count)
	return nil
}

func (m migrator) sqliteColumnExists(ctx context.Context, table, column string) (bool, error) {
	rows, err := m.sqlite.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return false, fmt.Errorf("query sqlite table info %s: %w", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return false, fmt.Errorf("scan sqlite table info %s: %w", table, err)
		}
		if strings.EqualFold(strings.TrimSpace(name), column) {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate sqlite table info %s: %w", table, err)
	}
	return false, nil
}

func (m migrator) bumpUsageEventSequence(ctx context.Context, tx *sql.Tx, maxID int64) error {
	sequenceTarget := m.serialTableName("usage_events")
	query := fmt.Sprintf(`
		SELECT setval(
			pg_get_serial_sequence($1, 'id'),
			COALESCE((SELECT MAX(id) FROM %s), 1),
			COALESCE((SELECT MAX(id) FROM %s), 0) > 0
		)
	`, m.table("usage_events"), m.table("usage_events"))
	if _, err := tx.ExecContext(ctx, query, sequenceTarget); err != nil {
		return fmt.Errorf("bump postgres usage_events sequence to %d: %w", maxID, err)
	}
	return nil
}

func (m migrator) table(name string) string {
	if strings.TrimSpace(m.schema) == "" {
		return quoteIdentifier(name)
	}
	return quoteIdentifier(m.schema) + "." + quoteIdentifier(name)
}

func (m migrator) serialTableName(name string) string {
	if strings.TrimSpace(m.schema) == "" {
		return name
	}
	return m.schema + "." + name
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
