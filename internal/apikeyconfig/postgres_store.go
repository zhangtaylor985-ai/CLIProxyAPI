package apikeyconfig

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	defaultTableName = "api_key_config_store"
	defaultStateID   = "default"
)

// PostgresStoreConfig configures Postgres-backed API key state persistence.
type PostgresStoreConfig struct {
	DSN    string
	Schema string
	Table  string
}

// PostgresStore persists API key identities and policies in PostgreSQL.
type PostgresStore struct {
	db    *sql.DB
	cfg   PostgresStoreConfig
	table string
}

// NewPostgresStore creates the required schema and tables when needed.
func NewPostgresStore(ctx context.Context, cfg PostgresStoreConfig) (*PostgresStore, error) {
	cfg.DSN = strings.TrimSpace(cfg.DSN)
	cfg.Schema = strings.TrimSpace(cfg.Schema)
	cfg.Table = strings.TrimSpace(cfg.Table)
	if cfg.DSN == "" {
		return nil, fmt.Errorf("api key config store: DSN is required")
	}
	if cfg.Table == "" {
		cfg.Table = defaultTableName
	}

	db, err := sql.Open("pgx", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("api key config store: open database: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("api key config store: ping database: %w", err)
	}

	store := &PostgresStore{
		db:    db,
		cfg:   cfg,
		table: fullTableName(cfg.Schema, cfg.Table),
	}
	if err := store.ensureSchema(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *PostgresStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *PostgresStore) LoadState(ctx context.Context) (State, bool, error) {
	if s == nil || s.db == nil {
		return State{}, false, fmt.Errorf("api key config store: not initialized")
	}
	var payload []byte
	err := s.db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT content FROM %s WHERE id = $1", s.table),
		defaultStateID,
	).Scan(&payload)
	if err == sql.ErrNoRows {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, fmt.Errorf("api key config store: load state: %w", err)
	}
	var state State
	if err := json.Unmarshal(payload, &state); err != nil {
		return State{}, false, fmt.Errorf("api key config store: decode state: %w", err)
	}
	state.APIKeys = normalizeAPIKeys(state.APIKeys)
	state.APIKeyPolicies = clonePolicies(state.APIKeyPolicies)
	return state, true, nil
}

func (s *PostgresStore) SaveState(ctx context.Context, state State) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("api key config store: not initialized")
	}
	state.APIKeys = normalizeAPIKeys(state.APIKeys)
	state.APIKeyPolicies = clonePolicies(state.APIKeyPolicies)
	raw, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("api key config store: encode state: %w", err)
	}
	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (id, content, created_at, updated_at)
		VALUES ($1, $2, NOW(), NOW())
		ON CONFLICT (id)
		DO UPDATE SET content = EXCLUDED.content, updated_at = NOW()
	`, s.table), defaultStateID, raw)
	if err != nil {
		return fmt.Errorf("api key config store: save state: %w", err)
	}
	return nil
}

func (s *PostgresStore) ensureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("api key config store: not initialized")
	}
	if schema := strings.TrimSpace(s.cfg.Schema); schema != "" {
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", quoteIdentifier(schema))); err != nil {
			return fmt.Errorf("api key config store: create schema: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id TEXT PRIMARY KEY,
			content JSONB NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`, s.table)); err != nil {
		return fmt.Errorf("api key config store: create table: %w", err)
	}
	return nil
}

func fullTableName(schema, table string) string {
	if strings.TrimSpace(schema) == "" {
		return quoteIdentifier(table)
	}
	return quoteIdentifier(schema) + "." + quoteIdentifier(table)
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
