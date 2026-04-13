package sessiontrajectory

import "testing"

func TestResolvePostgresConfigFromEnvPrefersDedicatedSessionEnv(t *testing.T) {
	t.Setenv("SESSION_TRAJECTORY_PG_DSN", "postgres://session-user:pass@127.0.0.1:5432/session_db?sslmode=disable")
	t.Setenv("SESSION_TRAJECTORY_PG_SCHEMA", "session_schema")
	t.Setenv("APIKEY_POLICY_PG_DSN", "postgres://policy-user:pass@127.0.0.1:5432/policy_db?sslmode=disable")
	t.Setenv("APIKEY_POLICY_PG_SCHEMA", "policy_schema")

	dsn, schema := ResolvePostgresConfigFromEnv()
	if dsn != "postgres://session-user:pass@127.0.0.1:5432/session_db?sslmode=disable" {
		t.Fatalf("dsn = %q, want dedicated session env", dsn)
	}
	if schema != "session_schema" {
		t.Fatalf("schema = %q, want session_schema", schema)
	}
}

func TestResolvePostgresConfigFromEnvFallsBackToSharedChain(t *testing.T) {
	t.Setenv("SESSION_TRAJECTORY_PG_DSN", "")
	t.Setenv("SESSION_TRAJECTORY_PG_SCHEMA", "")
	t.Setenv("APIKEY_POLICY_PG_DSN", "postgres://policy-user:pass@127.0.0.1:5432/policy_db?sslmode=disable")
	t.Setenv("APIKEY_POLICY_PG_SCHEMA", "policy_schema")
	t.Setenv("APIKEY_BILLING_PG_DSN", "")
	t.Setenv("APIKEY_BILLING_PG_SCHEMA", "")
	t.Setenv("PGSTORE_DSN", "")
	t.Setenv("PGSTORE_SCHEMA", "")

	dsn, schema := ResolvePostgresConfigFromEnv()
	if dsn != "postgres://policy-user:pass@127.0.0.1:5432/policy_db?sslmode=disable" {
		t.Fatalf("dsn = %q, want shared policy env", dsn)
	}
	if schema != "policy_schema" {
		t.Fatalf("schema = %q, want policy_schema", schema)
	}
}
