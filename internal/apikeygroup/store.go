package apikeygroup

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type Group struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	DailyBudgetMicroUSD  int64     `json:"daily_budget_micro_usd"`
	WeeklyBudgetMicroUSD int64     `json:"weekly_budget_micro_usd"`
	IsSystem             bool      `json:"is_system"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type Store interface {
	Close() error
	EnsureSchema(ctx context.Context) error
	SeedDefaults(ctx context.Context) error
	ListGroups(ctx context.Context) ([]Group, error)
	GetGroup(ctx context.Context, id string) (Group, bool, error)
	UpsertGroup(ctx context.Context, group Group) (Group, error)
	DeleteGroup(ctx context.Context, id string) error
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

var DefaultGroups = []Group{
	{ID: "dedicated", Name: "独享车", DailyBudgetMicroUSD: 300_000_000, WeeklyBudgetMicroUSD: 1_000_000_000, IsSystem: true},
	{ID: "double", Name: "双人车", DailyBudgetMicroUSD: 150_000_000, WeeklyBudgetMicroUSD: 500_000_000, IsSystem: true},
	{ID: "triple", Name: "三人车", DailyBudgetMicroUSD: 100_000_000, WeeklyBudgetMicroUSD: 300_000_000, IsSystem: true},
	{ID: "quad", Name: "四人车", DailyBudgetMicroUSD: 60_000_000, WeeklyBudgetMicroUSD: 250_000_000, IsSystem: true},
}

func NewPostgresStore(ctx context.Context, cfg PostgresStoreConfig) (*PostgresStore, error) {
	dsn := strings.TrimSpace(cfg.DSN)
	if dsn == "" {
		return nil, fmt.Errorf("api key group store: DSN is required")
	}
	if strings.TrimSpace(cfg.Table) == "" {
		cfg.Table = "api_key_groups"
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("api key group store: open database: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("api key group store: ping database: %w", err)
	}
	store := &PostgresStore{
		db:  db,
		cfg: cfg,
	}
	store.table = store.fullTableName(cfg.Table)
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
		return fmt.Errorf("api key group store: not initialized")
	}
	if schema := strings.TrimSpace(s.cfg.Schema); schema != "" {
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", quoteIdentifier(schema))); err != nil {
			return fmt.Errorf("api key group store: create schema: %w", err)
		}
	}
	query := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			daily_budget_micro_usd BIGINT NOT NULL,
			weekly_budget_micro_usd BIGINT NOT NULL,
			is_system BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`, s.table)
	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("api key group store: create table: %w", err)
	}
	return nil
}

func (s *PostgresStore) SeedDefaults(ctx context.Context) error {
	if err := s.EnsureSchema(ctx); err != nil {
		return err
	}
	for _, item := range DefaultGroups {
		if _, err := s.UpsertGroup(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresStore) ListGroups(ctx context.Context) ([]Group, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("api key group store: not initialized")
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, name, daily_budget_micro_usd, weekly_budget_micro_usd, is_system, created_at, updated_at
		FROM %s
		ORDER BY is_system DESC, daily_budget_micro_usd DESC, name ASC
	`, s.table))
	if err != nil {
		return nil, fmt.Errorf("api key group store: list groups: %w", err)
	}
	defer rows.Close()

	result := make([]Group, 0)
	for rows.Next() {
		var item Group
		if err := rows.Scan(&item.ID, &item.Name, &item.DailyBudgetMicroUSD, &item.WeeklyBudgetMicroUSD, &item.IsSystem, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("api key group store: scan group: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("api key group store: iterate groups: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) GetGroup(ctx context.Context, id string) (Group, bool, error) {
	if s == nil || s.db == nil {
		return Group{}, false, fmt.Errorf("api key group store: not initialized")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return Group{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT id, name, daily_budget_micro_usd, weekly_budget_micro_usd, is_system, created_at, updated_at
		FROM %s
		WHERE id = $1
	`, s.table), id)
	var item Group
	if err := row.Scan(&item.ID, &item.Name, &item.DailyBudgetMicroUSD, &item.WeeklyBudgetMicroUSD, &item.IsSystem, &item.CreatedAt, &item.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return Group{}, false, nil
		}
		return Group{}, false, fmt.Errorf("api key group store: get group: %w", err)
	}
	return item, true, nil
}

func (s *PostgresStore) UpsertGroup(ctx context.Context, group Group) (Group, error) {
	if s == nil || s.db == nil {
		return Group{}, fmt.Errorf("api key group store: not initialized")
	}
	group.ID = strings.TrimSpace(group.ID)
	group.Name = strings.TrimSpace(group.Name)
	if group.ID == "" {
		return Group{}, fmt.Errorf("api key group store: id is required")
	}
	if group.Name == "" {
		return Group{}, fmt.Errorf("api key group store: name is required")
	}
	if group.DailyBudgetMicroUSD < 0 || group.WeeklyBudgetMicroUSD < 0 {
		return Group{}, fmt.Errorf("api key group store: budgets must be >= 0")
	}
	row := s.db.QueryRowContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (id, name, daily_budget_micro_usd, weekly_budget_micro_usd, is_system)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT(id) DO UPDATE SET
			name = EXCLUDED.name,
			daily_budget_micro_usd = EXCLUDED.daily_budget_micro_usd,
			weekly_budget_micro_usd = EXCLUDED.weekly_budget_micro_usd,
			is_system = EXCLUDED.is_system,
			updated_at = NOW()
		RETURNING id, name, daily_budget_micro_usd, weekly_budget_micro_usd, is_system, created_at, updated_at
	`, s.table), group.ID, group.Name, group.DailyBudgetMicroUSD, group.WeeklyBudgetMicroUSD, group.IsSystem)
	var saved Group
	if err := row.Scan(&saved.ID, &saved.Name, &saved.DailyBudgetMicroUSD, &saved.WeeklyBudgetMicroUSD, &saved.IsSystem, &saved.CreatedAt, &saved.UpdatedAt); err != nil {
		return Group{}, fmt.Errorf("api key group store: upsert group: %w", err)
	}
	return saved, nil
}

func (s *PostgresStore) DeleteGroup(ctx context.Context, id string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("api key group store: not initialized")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("api key group store: id is required")
	}
	var isSystem bool
	row := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT is_system FROM %s WHERE id = $1`, s.table), id)
	if err := row.Scan(&isSystem); err != nil {
		if err == sql.ErrNoRows {
			return sql.ErrNoRows
		}
		return fmt.Errorf("api key group store: lookup group: %w", err)
	}
	if isSystem {
		return fmt.Errorf("system groups cannot be deleted")
	}
	res, err := s.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id = $1`, s.table), id)
	if err != nil {
		return fmt.Errorf("api key group store: delete group: %w", err)
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *PostgresStore) fullTableName(table string) string {
	if schema := strings.TrimSpace(s.cfg.Schema); schema != "" {
		return quoteIdentifier(schema) + "." + quoteIdentifier(table)
	}
	return quoteIdentifier(table)
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
