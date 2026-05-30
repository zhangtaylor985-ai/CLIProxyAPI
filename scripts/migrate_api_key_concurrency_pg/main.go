package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/apikeyconfig"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/apikeygroup"
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
	flag.StringVar(&cfg.PostgresDSN, "pg-dsn", defaultDSN, "Postgres DSN for the API key policy/group store")
	flag.StringVar(&cfg.PostgresSchema, "pg-schema", defaultSchema, "Postgres schema for the API key policy/group store")
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

	apiKeyStore, err := apikeyconfig.NewPostgresStore(ctx, apikeyconfig.PostgresStoreConfig{
		DSN:    cfg.PostgresDSN,
		Schema: cfg.PostgresSchema,
	})
	if err != nil {
		return fmt.Errorf("prepare postgres api key config store: %w", err)
	}
	defer apiKeyStore.Close()

	groupStore, err := apikeygroup.NewPostgresStore(ctx, apikeygroup.PostgresStoreConfig{
		DSN:    cfg.PostgresDSN,
		Schema: cfg.PostgresSchema,
	})
	if err != nil {
		return fmt.Errorf("prepare postgres api key group store: %w", err)
	}
	defer groupStore.Close()

	if err := groupStore.EnsureSchema(ctx); err != nil {
		return err
	}
	if err := groupStore.SeedDefaults(ctx); err != nil {
		return fmt.Errorf("seed default api key groups: %w", err)
	}
	log.Printf("api key concurrency schema and default group limits are ready")
	return nil
}
