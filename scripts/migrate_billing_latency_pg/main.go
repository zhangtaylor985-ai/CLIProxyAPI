package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/apikeyconfig"
)

type migrateConfig struct {
	PostgresDSN    string
	PostgresSchema string
	Timeout        time.Duration
}

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		log.Fatalf("migration failed: %v", err)
	}
}

func parseFlags() migrateConfig {
	defaultDSN, defaultSchema := apikeyconfig.ResolvePostgresConfigFromEnv()

	var cfg migrateConfig
	flag.StringVar(&cfg.PostgresDSN, "pg-dsn", defaultDSN, "Postgres DSN for billing tables")
	flag.StringVar(&cfg.PostgresSchema, "pg-schema", defaultSchema, "Postgres schema for billing tables")
	flag.DurationVar(&cfg.Timeout, "timeout", 30*time.Second, "Migration timeout")
	flag.Parse()
	return cfg
}

func run(cfg migrateConfig) error {
	cfg.PostgresDSN = strings.TrimSpace(cfg.PostgresDSN)
	cfg.PostgresSchema = strings.TrimSpace(cfg.PostgresSchema)
	if cfg.PostgresDSN == "" {
		return fmt.Errorf("--pg-dsn is required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	db, err := sql.Open("pgx", cfg.PostgresDSN)
	if err != nil {
		return fmt.Errorf("open postgres database: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping postgres database: %w", err)
	}

	table := "usage_events"
	if cfg.PostgresSchema != "" {
		table = quoteIdentifier(cfg.PostgresSchema) + "." + quoteIdentifier(table)
	} else {
		table = quoteIdentifier(table)
	}

	if _, err := db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN IF NOT EXISTS latency_ms BIGINT NOT NULL DEFAULT 0`, table)); err != nil {
		return fmt.Errorf("alter usage_events: %w", err)
	}

	targetSchema := cfg.PostgresSchema
	if targetSchema == "" {
		targetSchema = "public"
	}
	log.Printf("billing latency migration applied to schema: %s", targetSchema)
	return nil
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(strings.TrimSpace(value), `"`, `""`) + `"`
}
