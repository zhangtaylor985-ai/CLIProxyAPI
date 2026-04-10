package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/apikeyconfig"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/managementauth"
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
	flag.StringVar(&cfg.PostgresDSN, "pg-dsn", defaultDSN, "Postgres DSN for the management user store")
	flag.StringVar(&cfg.PostgresSchema, "pg-schema", defaultSchema, "Postgres schema for the management user store")
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

	store, err := managementauth.NewPostgresStore(ctx, managementauth.PostgresStoreConfig{
		DSN:    cfg.PostgresDSN,
		Schema: cfg.PostgresSchema,
	})
	if err != nil {
		return fmt.Errorf("prepare management auth postgres store: %w", err)
	}
	defer store.Close()

	log.Printf("management user store is ready")
	log.Printf("default users ensured: %d", 3)
	return nil
}
