package sessiontrajectory

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// EnsurePostgresSchema prepares the session trajectory schema explicitly for
// deployment-time migrations. Runtime still keeps ensureSchema as a safety
// fallback, but production rollout should run this entrypoint first.
func EnsurePostgresSchema(ctx context.Context, cfg PostgresStoreConfig) error {
	dsn := strings.TrimSpace(cfg.DSN)
	if dsn == "" {
		return fmt.Errorf("session trajectory postgres: DSN is required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("session trajectory postgres: open database: %w", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("session trajectory postgres: ping database: %w", err)
	}

	store := &PostgresStore{
		db:     db,
		schema: strings.TrimSpace(cfg.Schema),
	}
	if err := store.ensureSchema(ctx); err != nil {
		return err
	}
	return nil
}
