package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/apikeyconfig"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

type apiKeyConfigMigrateConfig struct {
	ConfigPath      string
	PostgresDSN     string
	PostgresSchema  string
	CloudDeployMode bool
}

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		log.Fatalf("migration failed: %v", err)
	}
}

func parseFlags() apiKeyConfigMigrateConfig {
	defaultDSN, defaultSchema := apikeyconfig.ResolvePostgresConfigFromEnv()
	defaultConfig := "config.yaml"

	var cfg apiKeyConfigMigrateConfig
	flag.StringVar(&cfg.ConfigPath, "config", defaultConfig, "Path to config.yaml")
	flag.StringVar(&cfg.PostgresDSN, "pg-dsn", defaultDSN, "Postgres DSN for the target API key config store")
	flag.StringVar(&cfg.PostgresSchema, "pg-schema", defaultSchema, "Target Postgres schema")
	flag.BoolVar(&cfg.CloudDeployMode, "cloud-deploy", false, "Validate config with cloud deploy optional semantics")
	flag.Parse()
	return cfg
}

func run(cfg apiKeyConfigMigrateConfig) error {
	cfg.ConfigPath = strings.TrimSpace(cfg.ConfigPath)
	cfg.PostgresDSN = strings.TrimSpace(cfg.PostgresDSN)
	cfg.PostgresSchema = strings.TrimSpace(cfg.PostgresSchema)
	if cfg.ConfigPath == "" {
		return fmt.Errorf("--config is required")
	}
	if cfg.PostgresDSN == "" {
		return fmt.Errorf("--pg-dsn is required")
	}

	absConfig, err := filepath.Abs(cfg.ConfigPath)
	if err != nil {
		return fmt.Errorf("resolve config path: %w", err)
	}
	if _, err := os.Stat(absConfig); err != nil {
		return fmt.Errorf("stat config path: %w", err)
	}

	cfgFile, err := config.LoadConfigOptional(absConfig, cfg.CloudDeployMode)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
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

	state := apikeyconfig.StateFromConfig(cfgFile)
	if err := store.SaveState(ctx, state); err != nil {
		return fmt.Errorf("save api key config state: %w", err)
	}

	log.Printf("source config: %s", absConfig)
	if cfg.PostgresSchema == "" {
		log.Printf("target postgres schema: public")
	} else {
		log.Printf("target postgres schema: %s", cfg.PostgresSchema)
	}
	log.Printf("migrated api-keys: %d", len(state.APIKeys))
	log.Printf("migrated api-key-policies: %d", len(state.APIKeyPolicies))
	return nil
}
