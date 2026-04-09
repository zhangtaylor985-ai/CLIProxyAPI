package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/apikeyconfig"
)

type migrateConfig struct {
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
	defaultDSN, defaultSchema := apikeyconfig.ResolvePostgresConfigFromEnv()
	var cfg migrateConfig
	flag.StringVar(&cfg.PostgresDSN, "pg-dsn", defaultDSN, "Postgres DSN for the API key config store")
	flag.StringVar(&cfg.PostgresSchema, "pg-schema", defaultSchema, "Postgres schema for the API key config store")
	flag.Parse()
	return cfg
}

func run(cfg migrateConfig) error {
	cfg.PostgresDSN = strings.TrimSpace(cfg.PostgresDSN)
	cfg.PostgresSchema = strings.TrimSpace(cfg.PostgresSchema)
	if cfg.PostgresDSN == "" {
		return fmt.Errorf("--pg-dsn is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	store, err := apikeyconfig.NewPostgresStore(ctx, apikeyconfig.PostgresStoreConfig{
		DSN:    cfg.PostgresDSN,
		Schema: cfg.PostgresSchema,
	})
	if err != nil {
		return fmt.Errorf("prepare postgres api key config store: %w", err)
	}
	defer store.Close()

	state, found, err := store.LoadState(ctx)
	if err != nil {
		return fmt.Errorf("load api key config state: %w", err)
	}
	if !found {
		log.Printf("no api key config state found; schema ensured only")
		return nil
	}

	normalized := state.Normalized()
	if err := store.SaveState(ctx, normalized); err != nil {
		return fmt.Errorf("save row-based api key config state: %w", err)
	}

	log.Printf("row-based api key config store is ready")
	log.Printf("records migrated: %d", len(normalized.Records))
	return nil
}
