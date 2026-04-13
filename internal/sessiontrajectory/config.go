package sessiontrajectory

import (
	"os"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/apikeyconfig"
)

// ResolvePostgresConfigFromEnv resolves the dedicated session trajectory
// PostgreSQL config first, then falls back to the shared PG config chain.
func ResolvePostgresConfigFromEnv() (dsn string, schema string) {
	dsn = strings.TrimSpace(dedicatedEnv("SESSION_TRAJECTORY_PG_DSN"))
	schema = strings.TrimSpace(dedicatedEnv("SESSION_TRAJECTORY_PG_SCHEMA"))
	if dsn != "" || schema != "" {
		return dsn, schema
	}
	return apikeyconfig.ResolvePostgresConfigFromEnv()
}

func dedicatedEnv(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}
