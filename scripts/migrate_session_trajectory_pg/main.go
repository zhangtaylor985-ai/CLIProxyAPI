package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/sessiontrajectory"
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
	defaultDSN, defaultSchema := sessiontrajectory.ResolvePostgresConfigFromEnv()

	var cfg migrateConfig
	flag.StringVar(&cfg.PostgresDSN, "pg-dsn", defaultDSN, "Postgres DSN for session trajectory tables")
	flag.StringVar(&cfg.PostgresSchema, "pg-schema", defaultSchema, "Postgres schema for session trajectory tables")
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

	if err := sessiontrajectory.EnsurePostgresSchema(ctx, sessiontrajectory.PostgresStoreConfig{
		DSN:    cfg.PostgresDSN,
		Schema: cfg.PostgresSchema,
	}); err != nil {
		return err
	}

	targetSchema := cfg.PostgresSchema
	if targetSchema == "" {
		targetSchema = "public"
	}
	log.Printf("session trajectory postgres schema ready: %s", targetSchema)
	return nil
}
