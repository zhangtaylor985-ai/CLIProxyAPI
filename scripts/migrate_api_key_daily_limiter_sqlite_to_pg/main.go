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
	"github.com/router-for-me/CLIProxyAPI/v6/internal/apikeyconfig"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/policy"
	_ "modernc.org/sqlite"
)

type dailyLimiterMigrateConfig struct {
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

func parseFlags() dailyLimiterMigrateConfig {
	defaultDSN, defaultSchema := apikeyconfig.ResolvePostgresConfigFromEnv()
	defaultSQLite := strings.TrimSpace(os.Getenv("APIKEY_POLICY_SQLITE_PATH"))
	if defaultSQLite == "" {
		defaultSQLite = "api_key_policy_limits.sqlite"
	}

	var cfg dailyLimiterMigrateConfig
	flag.StringVar(&cfg.SQLitePath, "sqlite", defaultSQLite, "Path to the source SQLite daily limiter database")
	flag.StringVar(&cfg.PostgresDSN, "pg-dsn", defaultDSN, "Postgres DSN for the target limiter database")
	flag.StringVar(&cfg.PostgresSchema, "pg-schema", defaultSchema, "Target Postgres schema")
	flag.Parse()
	return cfg
}

func run(cfg dailyLimiterMigrateConfig) error {
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	limiter, err := policy.NewPostgresDailyLimiter(ctx, policy.PostgresDailyLimiterConfig{
		DSN:    cfg.PostgresDSN,
		Schema: cfg.PostgresSchema,
	})
	if err != nil {
		return fmt.Errorf("prepare postgres daily limiter schema: %w", err)
	}
	if err := limiter.Close(); err != nil {
		return fmt.Errorf("close postgres limiter bootstrap store: %w", err)
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

	tableName := "api_model_daily_usage"
	if cfg.PostgresSchema != "" {
		tableName = postgresQuoteIdentifier(cfg.PostgresSchema) + "." + postgresQuoteIdentifier(tableName)
	} else {
		tableName = postgresQuoteIdentifier(tableName)
	}

	rows, err := sqliteDB.QueryContext(ctx, `
		SELECT api_key, model, day, count, updated_at
		FROM api_model_daily_usage
		ORDER BY day ASC, api_key ASC, model ASC
	`)
	if err != nil {
		return fmt.Errorf("query sqlite daily limiter rows: %w", err)
	}
	defer rows.Close()

	tx, err := pgDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin postgres transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (api_key, model, day, count, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (api_key, model, day)
		DO UPDATE SET count = EXCLUDED.count, updated_at = EXCLUDED.updated_at
	`, tableName))
	if err != nil {
		return fmt.Errorf("prepare postgres statement: %w", err)
	}
	defer stmt.Close()

	count := 0
	for rows.Next() {
		var (
			apiKey    string
			model     string
			day       string
			usedCount int64
			updatedAt int64
		)
		if err := rows.Scan(&apiKey, &model, &day, &usedCount, &updatedAt); err != nil {
			return fmt.Errorf("scan sqlite daily limiter row: %w", err)
		}
		if _, err := stmt.ExecContext(ctx, apiKey, strings.ToLower(strings.TrimSpace(model)), day, usedCount, updatedAt); err != nil {
			return fmt.Errorf("insert postgres daily limiter row %q/%q/%q: %w", apiKey, model, day, err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate sqlite daily limiter rows: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit postgres transaction: %w", err)
	}

	log.Printf("source sqlite: %s", absSQLite)
	if cfg.PostgresSchema == "" {
		log.Printf("target postgres schema: public")
	} else {
		log.Printf("target postgres schema: %s", cfg.PostgresSchema)
	}
	log.Printf("migrated daily limiter rows: %d", count)
	return nil
}

func postgresQuoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
