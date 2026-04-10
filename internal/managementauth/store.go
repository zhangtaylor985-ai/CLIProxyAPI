package managementauth

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/crypto/bcrypt"
)

type Role string

const (
	RoleAdmin Role = "admin"
	RoleStaff Role = "staff"
)

const defaultTableName = "management_users"

type User struct {
	Username     string
	PasswordHash string
	Role         Role
	Enabled      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type SeedUser struct {
	Username string
	Password string
	Role     Role
}

var defaultSeedUsers = []SeedUser{
	{Username: "cpa_admin_6296680d", Password: "e2r.zkMAYsL2utvhh!Ds", Role: RoleAdmin},
	{Username: "user_01", Password: "LK8DDxq6CNj7!rwvEdky", Role: RoleStaff},
	{Username: "user_02", Password: "dk7bgvAN7zeL*nvFqxXR", Role: RoleStaff},
}

type Store interface {
	Close() error
	EnsureSchema(context.Context) error
	SeedDefaults(context.Context) error
	GetByUsername(context.Context, string) (User, bool, error)
}

type PostgresStoreConfig struct {
	DSN    string
	Schema string
	Table  string
}

type PostgresStore struct {
	db    *sql.DB
	cfg   PostgresStoreConfig
	table string
}

func NewPostgresStore(ctx context.Context, cfg PostgresStoreConfig) (*PostgresStore, error) {
	cfg.DSN = strings.TrimSpace(cfg.DSN)
	cfg.Schema = strings.TrimSpace(cfg.Schema)
	cfg.Table = strings.TrimSpace(cfg.Table)
	if cfg.DSN == "" {
		return nil, fmt.Errorf("management auth store: DSN is required")
	}
	if cfg.Table == "" {
		cfg.Table = defaultTableName
	}

	db, err := sql.Open("pgx", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("management auth store: open database: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("management auth store: ping database: %w", err)
	}

	store := &PostgresStore{
		db:    db,
		cfg:   cfg,
		table: fullTableName(cfg.Schema, cfg.Table),
	}
	if err := store.EnsureSchema(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.SeedDefaults(ctx); err != nil {
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

func (s *PostgresStore) EnsureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("management auth store: not initialized")
	}
	if schema := strings.TrimSpace(s.cfg.Schema); schema != "" {
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", quoteIdentifier(schema))); err != nil {
			return fmt.Errorf("management auth store: create schema: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			username TEXT PRIMARY KEY,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL,
			enabled BOOLEAN NOT NULL DEFAULT TRUE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`, s.table)); err != nil {
		return fmt.Errorf("management auth store: create table: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE INDEX IF NOT EXISTS %s ON %s (role, enabled)
	`, quoteIdentifier(tableIndexName(s.cfg.Table, "role_enabled_idx")), s.table)); err != nil {
		return fmt.Errorf("management auth store: create index: %w", err)
	}
	return nil
}

func (s *PostgresStore) SeedDefaults(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("management auth store: not initialized")
	}
	for _, seed := range defaultSeedUsers {
		username := strings.TrimSpace(seed.Username)
		password := strings.TrimSpace(seed.Password)
		if username == "" || password == "" {
			continue
		}
		hashBytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("management auth store: hash seed password for %s: %w", username, err)
		}
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
			INSERT INTO %s (username, password_hash, role, enabled)
			VALUES ($1, $2, $3, TRUE)
			ON CONFLICT (username) DO NOTHING
		`, s.table), username, string(hashBytes), string(normalizeRole(seed.Role))); err != nil {
			return fmt.Errorf("management auth store: seed user %s: %w", username, err)
		}
	}
	return nil
}

func (s *PostgresStore) GetByUsername(ctx context.Context, username string) (User, bool, error) {
	if s == nil || s.db == nil {
		return User{}, false, fmt.Errorf("management auth store: not initialized")
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return User{}, false, nil
	}

	row := s.db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT username, password_hash, role, enabled, created_at, updated_at
		FROM %s
		WHERE username = $1
	`, s.table), username)

	var (
		user    User
		roleRaw string
	)
	if err := row.Scan(&user.Username, &user.PasswordHash, &roleRaw, &user.Enabled, &user.CreatedAt, &user.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return User{}, false, nil
		}
		return User{}, false, fmt.Errorf("management auth store: lookup user %s: %w", username, err)
	}
	user.Role = normalizeRole(Role(roleRaw))
	return user, true, nil
}

func normalizeRole(role Role) Role {
	switch Role(strings.ToLower(strings.TrimSpace(string(role)))) {
	case RoleStaff:
		return RoleStaff
	default:
		return RoleAdmin
	}
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
	base := strings.TrimSpace(table)
	if base == "" {
		base = defaultTableName
	}
	base = strings.ReplaceAll(base, `"`, "")
	return base + "_" + suffix
}
