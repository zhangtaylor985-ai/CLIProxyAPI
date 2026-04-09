package main

import (
	"flag"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/apikeyconfig"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
	flag.Parse()

	if err := run(strings.TrimSpace(*configPath)); err != nil {
		log.Fatalf("cleanup failed: %v", err)
	}
}

func run(configPath string) error {
	if configPath == "" {
		return fmt.Errorf("--config is required")
	}
	absPath, err := filepath.Abs(configPath)
	if err != nil {
		return fmt.Errorf("resolve config path: %w", err)
	}

	cfg, err := config.LoadConfigOptional(absPath, false)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if err := config.SaveConfigPreserveComments(absPath, apikeyconfig.ConfigWithoutAPIKeyState(cfg)); err != nil {
		return fmt.Errorf("save cleaned config: %w", err)
	}

	log.Printf("cleaned api key sections from %s", absPath)
	return nil
}
