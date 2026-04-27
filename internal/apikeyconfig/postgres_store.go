package apikeyconfig

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

const (
	defaultTableName    = "api_key_config_entries"
	legacyBlobTableName = "api_key_config_store"
	defaultStateID      = "default"
)

// PostgresStoreConfig configures Postgres-backed API key state persistence.
type PostgresStoreConfig struct {
	DSN    string
	Schema string
	Table  string
}

// PostgresStore persists API key identities and policies in PostgreSQL.
type PostgresStore struct {
	db          *sql.DB
	cfg         PostgresStoreConfig
	table       string
	legacyTable string
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
		db:          db,
		cfg:         cfg,
		table:       fullTableName(cfg.Schema, cfg.Table),
		legacyTable: fullTableName(cfg.Schema, legacyBlobTableName),
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

	records, found, err := s.loadRecords(ctx)
	if err != nil {
		return State{}, false, err
	}
	if found {
		return State{Records: records}.Normalized(), true, nil
	}

	legacyState, found, err := s.loadLegacyBlobState(ctx)
	if err != nil {
		return State{}, false, err
	}
	if !found {
		return State{}, false, nil
	}
	return legacyState.Normalized(), true, nil
}

func (s *PostgresStore) SaveState(ctx context.Context, state State) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("api key config store: not initialized")
	}
	normalized := state.Normalized()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("api key config store: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s", s.table)); err != nil {
		return fmt.Errorf("api key config store: clear rows: %w", err)
	}

	if len(normalized.Records) > 0 {
		stmt, err := tx.PrepareContext(ctx, fmt.Sprintf(`
				INSERT INTO %s (api_key, policy_json, created_at, expires_at, disabled, owner_username, owner_role, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
			`, s.table))
		if err != nil {
			return fmt.Errorf("api key config store: prepare insert: %w", err)
		}
		defer func() { _ = stmt.Close() }()

		for _, record := range normalized.Records {
			raw, expiresAt, err := encodePersistedPolicyRecord(record)
			if err != nil {
				return fmt.Errorf("api key config store: encode policy %q: %w", record.APIKey, err)
			}
			if _, err := stmt.ExecContext(
				ctx,
				record.APIKey,
				raw,
				record.CreatedAt.UTC(),
				expiresAt,
				record.Disabled,
				record.OwnerUsername,
				record.OwnerRole,
			); err != nil {
				return fmt.Errorf("api key config store: insert row %q: %w", record.APIKey, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("api key config store: commit transaction: %w", err)
	}
	return nil
}

func (s *PostgresStore) SaveRecord(ctx context.Context, previousAPIKey string, record Record) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("api key config store: not initialized")
	}
	normalized := normalizeRecords([]Record{record})
	if len(normalized) == 0 {
		return fmt.Errorf("api key config store: record is required")
	}
	record = normalized[0]
	previousAPIKey = strings.TrimSpace(previousAPIKey)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("api key config store: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	raw, expiresAt, err := encodePersistedPolicyRecord(record)
	if err != nil {
		return fmt.Errorf("api key config store: encode policy %q: %w", record.APIKey, err)
	}

	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (api_key, policy_json, created_at, expires_at, disabled, owner_username, owner_role, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		ON CONFLICT (api_key) DO UPDATE SET
			policy_json = EXCLUDED.policy_json,
			created_at = EXCLUDED.created_at,
			expires_at = EXCLUDED.expires_at,
			disabled = EXCLUDED.disabled,
			owner_username = EXCLUDED.owner_username,
			owner_role = EXCLUDED.owner_role,
			updated_at = NOW()
	`, s.table),
		record.APIKey,
		raw,
		record.CreatedAt.UTC(),
		expiresAt,
		record.Disabled,
		record.OwnerUsername,
		record.OwnerRole,
	); err != nil {
		return fmt.Errorf("api key config store: upsert row %q: %w", record.APIKey, err)
	}

	if previousAPIKey != "" && previousAPIKey != record.APIKey {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE api_key = $1", s.table), previousAPIKey); err != nil {
			return fmt.Errorf("api key config store: delete previous row %q: %w", previousAPIKey, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("api key config store: commit transaction: %w", err)
	}
	return nil
}

func (s *PostgresStore) DeleteRecord(ctx context.Context, apiKey string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("api key config store: not initialized")
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return fmt.Errorf("api key config store: api key is required")
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE api_key = $1", s.table), apiKey); err != nil {
		return fmt.Errorf("api key config store: delete row %q: %w", apiKey, err)
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
			api_key TEXT PRIMARY KEY,
			policy_json JSONB NOT NULL DEFAULT '{}'::jsonb,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			expires_at TIMESTAMPTZ NULL,
			disabled BOOLEAN NOT NULL DEFAULT FALSE,
			owner_username TEXT NOT NULL DEFAULT 'admin',
			owner_role TEXT NOT NULL DEFAULT 'admin',
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`, s.table)); err != nil {
		return fmt.Errorf("api key config store: create table: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		ALTER TABLE %s
			ADD COLUMN IF NOT EXISTS owner_username TEXT NOT NULL DEFAULT 'admin',
			ADD COLUMN IF NOT EXISTS owner_role TEXT NOT NULL DEFAULT 'admin'
	`, s.table)); err != nil {
		return fmt.Errorf("api key config store: add owner columns: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET owner_username = 'admin'
		WHERE owner_username IS NULL OR btrim(owner_username) = ''
	`, s.table)); err != nil {
		return fmt.Errorf("api key config store: backfill owner username: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET owner_role = 'admin'
		WHERE owner_role IS NULL OR btrim(owner_role) = ''
	`, s.table)); err != nil {
		return fmt.Errorf("api key config store: backfill owner role: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET owner_role = 'staff'
		WHERE replace(replace(replace(lower(btrim(owner_username)), '_', ''), '-', ''), ' ', '') IN ('user01', 'user02')
	`, s.table)); err != nil {
		return fmt.Errorf("api key config store: infer staff owner roles: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET owner_username = CASE
			WHEN replace(replace(replace(lower(btrim(owner_username)), '_', ''), '-', ''), ' ', '') = 'user01' THEN 'user_01'
			WHEN replace(replace(replace(lower(btrim(owner_username)), '_', ''), '-', ''), ' ', '') = 'user02' THEN 'user_02'
			ELSE owner_username
		END
		WHERE replace(replace(replace(lower(btrim(owner_username)), '_', ''), '-', ''), ' ', '') IN ('user01', 'user02')
	`, s.table)); err != nil {
		return fmt.Errorf("api key config store: normalize staff owner usernames: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET owner_username = 'admin'
		WHERE lower(btrim(owner_role)) = 'admin' AND btrim(owner_username) <> 'admin'
	`, s.table)); err != nil {
		return fmt.Errorf("api key config store: collapse admin owner username: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE INDEX IF NOT EXISTS %s ON %s (disabled, expires_at)
	`, quoteIdentifier(tableIndexName(s.cfg.Table, "disabled_expires_at_idx")), s.table)); err != nil {
		return fmt.Errorf("api key config store: create index: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE INDEX IF NOT EXISTS %s ON %s (owner_role, owner_username)
	`, quoteIdentifier(tableIndexName(s.cfg.Table, "owner_idx")), s.table)); err != nil {
		return fmt.Errorf("api key config store: create owner index: %w", err)
	}
	return nil
}

func (s *PostgresStore) loadRecords(ctx context.Context) ([]Record, bool, error) {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT api_key, policy_json, created_at, expires_at, disabled, owner_username, owner_role
		FROM %s
		ORDER BY created_at ASC, api_key ASC
	`, s.table))
	if err != nil {
		return nil, false, fmt.Errorf("api key config store: load records: %w", err)
	}
	defer func() { _ = rows.Close() }()

	records := make([]Record, 0)
	for rows.Next() {
		var (
			apiKey    string
			rawPolicy []byte
			createdAt time.Time
			expiresAt sql.NullTime
			disabled  bool
			ownerName string
			ownerRole string
		)
		if err := rows.Scan(&apiKey, &rawPolicy, &createdAt, &expiresAt, &disabled, &ownerName, &ownerRole); err != nil {
			return nil, false, fmt.Errorf("api key config store: scan record: %w", err)
		}
		policyEntry := config.APIKeyPolicy{}
		if len(rawPolicy) > 0 {
			if err := json.Unmarshal(rawPolicy, &policyEntry); err != nil {
				return nil, false, fmt.Errorf("api key config store: decode record %q: %w", apiKey, err)
			}
		}
		policyEntry.APIKey = strings.TrimSpace(apiKey)
		policyEntry.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		policyEntry.Disabled = disabled
		policyEntry.OwnerUsername = strings.TrimSpace(ownerName)
		policyEntry.OwnerRole = normalizeOwnerRole(ownerRole)
		record := Record{
			APIKey:        strings.TrimSpace(apiKey),
			Policy:        policyEntry,
			CreatedAt:     createdAt.UTC(),
			Disabled:      disabled,
			OwnerUsername: strings.TrimSpace(ownerName),
			OwnerRole:     normalizeOwnerRole(ownerRole),
		}
		if expiresAt.Valid {
			value := expiresAt.Time.UTC()
			record.ExpiresAt = &value
			record.Policy.ExpiresAt = value.Format(time.RFC3339)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("api key config store: iterate records: %w", err)
	}
	if len(records) == 0 {
		return nil, false, nil
	}
	return normalizeRecords(records), true, nil
}

func (s *PostgresStore) loadLegacyBlobState(ctx context.Context) (State, bool, error) {
	var payload []byte
	err := s.db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT content FROM %s WHERE id = $1", s.legacyTable),
		defaultStateID,
	).Scan(&payload)
	if err == sql.ErrNoRows {
		return State{}, false, nil
	}
	if err != nil {
		// Legacy table may not exist on clean installs.
		if strings.Contains(strings.ToLower(err.Error()), "does not exist") {
			return State{}, false, nil
		}
		return State{}, false, fmt.Errorf("api key config store: load legacy state: %w", err)
	}
	var state State
	if err := json.Unmarshal(payload, &state); err != nil {
		return State{}, false, fmt.Errorf("api key config store: decode legacy state: %w", err)
	}
	return state, true, nil
}

func encodePersistedPolicyRecord(record Record) ([]byte, any, error) {
	policyCopy := record.Policy
	policyCopy.CreatedAt = ""
	policyCopy.ExpiresAt = ""
	policyCopy.Disabled = false
	policyCopy.OwnerUsername = ""
	policyCopy.OwnerRole = ""
	raw, err := json.Marshal(policyCopy)
	if err != nil {
		return nil, nil, err
	}
	var expiresAt any
	if record.ExpiresAt != nil {
		expiresAt = record.ExpiresAt.UTC()
	}
	return raw, expiresAt, nil
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

func tableIndexName(table, suffix string) string {
	table = strings.TrimSpace(table)
	suffix = strings.TrimSpace(suffix)
	if table == "" {
		table = defaultTableName
	}
	if suffix == "" {
		return table
	}
	return table + "_" + suffix
}
