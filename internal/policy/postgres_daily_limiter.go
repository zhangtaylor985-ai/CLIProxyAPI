package policy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const defaultPostgresDailyLimiterTable = "api_model_daily_usage"

// PostgresDailyLimiterConfig configures a PostgreSQL-backed daily limiter.
type PostgresDailyLimiterConfig struct {
	DSN    string
	Schema string
	Table  string
}

// PostgresDailyLimiter provides atomic per-day counters keyed by (api_key, model, day).
type PostgresDailyLimiter struct {
	db    *sql.DB
	table string
	cfg   PostgresDailyLimiterConfig
}

func NewPostgresDailyLimiter(ctx context.Context, cfg PostgresDailyLimiterConfig) (*PostgresDailyLimiter, error) {
	cfg.DSN = strings.TrimSpace(cfg.DSN)
	cfg.Schema = strings.TrimSpace(cfg.Schema)
	cfg.Table = strings.TrimSpace(cfg.Table)
	if cfg.DSN == "" {
		return nil, fmt.Errorf("postgres limiter: DSN is required")
	}
	if cfg.Table == "" {
		cfg.Table = defaultPostgresDailyLimiterTable
	}
	db, err := sql.Open("pgx", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("postgres limiter: open database: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("postgres limiter: ping database: %w", err)
	}
	limiter := &PostgresDailyLimiter{
		db:    db,
		table: postgresLimiterTableName(cfg.Schema, cfg.Table),
		cfg:   cfg,
	}
	if err := limiter.ensureSchema(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return limiter, nil
}

func (l *PostgresDailyLimiter) Close() error {
	if l == nil || l.db == nil {
		return nil
	}
	return l.db.Close()
}

func (l *PostgresDailyLimiter) ensureSchema(ctx context.Context) error {
	if l == nil || l.db == nil {
		return fmt.Errorf("postgres limiter: not initialized")
	}
	if schema := strings.TrimSpace(l.cfg.Schema); schema != "" {
		if _, err := l.db.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", postgresQuoteIdentifier(schema))); err != nil {
			return fmt.Errorf("postgres limiter: create schema: %w", err)
		}
	}
	if _, err := l.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			api_key TEXT NOT NULL,
			model TEXT NOT NULL,
			day TEXT NOT NULL,
			count BIGINT NOT NULL,
			updated_at BIGINT NOT NULL,
			PRIMARY KEY (api_key, model, day)
		)
	`, l.table)); err != nil {
		return fmt.Errorf("postgres limiter: create table: %w", err)
	}
	return nil
}

func (l *PostgresDailyLimiter) Consume(ctx context.Context, apiKey, model, dayKey string, limit int) (count int, allowed bool, err error) {
	if l == nil || l.db == nil {
		return 0, false, fmt.Errorf("postgres limiter: not initialized")
	}
	apiKey = strings.TrimSpace(apiKey)
	model = strings.ToLower(strings.TrimSpace(model))
	dayKey = strings.TrimSpace(dayKey)
	if apiKey == "" || model == "" || dayKey == "" {
		return 0, false, fmt.Errorf("postgres limiter: invalid inputs")
	}
	if limit <= 0 {
		return 0, false, nil
	}

	nowUnix := time.Now().UTC().Unix()
	targetTable := postgresQuoteIdentifier(l.cfg.Table)
	row := l.db.QueryRowContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (api_key, model, day, count, updated_at)
		VALUES ($1, $2, $3, 1, $4)
		ON CONFLICT (api_key, model, day)
		DO UPDATE SET count = %s.count + 1, updated_at = EXCLUDED.updated_at
		WHERE %s.count < $5
		RETURNING count
	`, l.table, targetTable, targetTable), apiKey, model, dayKey, nowUnix, limit)
	if err := row.Scan(&count); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return limit, false, nil
		}
		return 0, false, fmt.Errorf("postgres limiter: consume failed: %w", err)
	}
	return count, true, nil
}

func (l *PostgresDailyLimiter) ListUsageCounts(ctx context.Context, apiKey, dayKey string) ([]DailyUsageCountRow, error) {
	if l == nil || l.db == nil {
		return nil, fmt.Errorf("postgres limiter: not initialized")
	}

	query := fmt.Sprintf(`
		SELECT api_key, model, day, count
		FROM %s
	`, l.table)
	conditions := make([]string, 0, 2)
	args := make([]any, 0, 2)
	if trimmed := strings.TrimSpace(apiKey); trimmed != "" {
		conditions = append(conditions, fmt.Sprintf("api_key = $%d", len(args)+1))
		args = append(args, trimmed)
	}
	if trimmed := strings.TrimSpace(dayKey); trimmed != "" {
		conditions = append(conditions, fmt.Sprintf("day = $%d", len(args)+1))
		args = append(args, trimmed)
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY day ASC, api_key ASC, model ASC"

	rows, err := l.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres limiter: list usage counts: %w", err)
	}
	defer rows.Close()

	result := make([]DailyUsageCountRow, 0)
	for rows.Next() {
		var row DailyUsageCountRow
		if err := rows.Scan(&row.APIKey, &row.Model, &row.Day, &row.Count); err != nil {
			return nil, fmt.Errorf("postgres limiter: scan usage count: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres limiter: usage count rows: %w", err)
	}
	return result, nil
}

func postgresLimiterTableName(schema, table string) string {
	if strings.TrimSpace(schema) == "" {
		return postgresQuoteIdentifier(table)
	}
	return postgresQuoteIdentifier(schema) + "." + postgresQuoteIdentifier(table)
}

func postgresQuoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
