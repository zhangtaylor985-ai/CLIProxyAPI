package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/sessiontrajectory"
)

type row struct {
	ID                string          `json:"id"`
	RequestID         string          `json:"request_id"`
	ProviderRequestID string          `json:"provider_request_id,omitempty"`
	UpstreamLogID     string          `json:"upstream_log_id,omitempty"`
	SessionID         string          `json:"session_id"`
	UserID            string          `json:"user_id"`
	Source            string          `json:"source"`
	CallType          string          `json:"call_type"`
	Provider          string          `json:"provider"`
	Model             string          `json:"model"`
	Status            string          `json:"status"`
	RequestIndex      int64           `json:"request_index"`
	StartedAt         time.Time       `json:"started_at"`
	EndedAt           *time.Time      `json:"ended_at,omitempty"`
	Message           string          `json:"message,omitempty"`
	RequestJSON       json.RawMessage `json:"request_json,omitempty"`
	ResponseJSON      json.RawMessage `json:"response_json,omitempty"`
	ErrorJSON         json.RawMessage `json:"error_json,omitempty"`
}

func main() {
	requestID := flag.String("request-id", "", "local or upstream request id to look up")
	start := flag.String("start", "", "optional RFC3339 or '2006-01-02 15:04:05Z07:00' lower bound for payload fallback scan")
	end := flag.String("end", "", "optional RFC3339 or '2006-01-02 15:04:05Z07:00' upper bound for payload fallback scan")
	limit := flag.Int("limit", 20, "maximum rows to return")
	includePayloads := flag.Bool("include-payloads", false, "include request/response/error JSON payloads")
	envPath := flag.String("env", ".env", "env file to load before resolving PG config")
	flag.Parse()

	id := strings.TrimSpace(*requestID)
	if id == "" {
		fatalf("--request-id is required")
	}
	if *limit <= 0 || *limit > 200 {
		*limit = 20
	}

	loadDotEnv(*envPath)
	dsn, schema := sessiontrajectory.ResolvePostgresConfigFromEnv()
	if strings.TrimSpace(dsn) == "" {
		fatalf("session trajectory postgres DSN is not configured")
	}
	if strings.TrimSpace(schema) == "" {
		schema = "public"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		fatalf("open postgres: %v", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		fatalf("ping postgres: %v", err)
	}
	_, _ = db.ExecContext(ctx, "SET statement_timeout = '20s'")

	rows, err := queryExact(ctx, db, schema, id, *limit, *includePayloads)
	if err != nil {
		fatalf("query exact request id: %v", err)
	}
	mode := "indexed"
	if len(rows) == 0 && strings.TrimSpace(*start) != "" && strings.TrimSpace(*end) != "" {
		startAt, err := parseTime(*start)
		if err != nil {
			fatalf("parse --start: %v", err)
		}
		endAt, err := parseTime(*end)
		if err != nil {
			fatalf("parse --end: %v", err)
		}
		rows, err = queryWindowPayload(ctx, db, schema, id, startAt, endAt, *limit, *includePayloads)
		if err != nil {
			fatalf("query window payload fallback: %v", err)
		}
		mode = "payload_window"
	}
	if rows == nil {
		rows = []row{}
	}

	out := map[string]any{
		"query":      id,
		"mode":       mode,
		"count":      len(rows),
		"schema":     schema,
		"matched_at": time.Now().UTC().Format(time.RFC3339),
		"rows":       rows,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fatalf("encode output: %v", err)
	}
}

func queryExact(ctx context.Context, db *sql.DB, schema, id string, limit int, includePayloads bool) ([]row, error) {
	return queryRows(ctx, db, fmt.Sprintf(`
		SELECT id::text, request_id, COALESCE(provider_request_id, ''), COALESCE(upstream_log_id, ''),
		       session_id::text, user_id, source, call_type, provider, model, status, request_index,
		       started_at, ended_at,
		       LEFT(COALESCE(error_json #>> '{error,message}', response_json #>> '{error,message}', error_json::text, response_json::text, ''), 800),
		       %s, %s, %s
		FROM %s
		WHERE request_id = $1 OR provider_request_id = $1 OR upstream_log_id = $1
		ORDER BY started_at DESC
		LIMIT $2
	`, payloadExpr("request_json", includePayloads), payloadExpr("response_json", includePayloads), payloadExpr("error_json", includePayloads), table(schema, "session_trajectory_requests")), id, limit)
}

func queryWindowPayload(ctx context.Context, db *sql.DB, schema, id string, startAt, endAt time.Time, limit int, includePayloads bool) ([]row, error) {
	like := "%" + id + "%"
	return queryRows(ctx, db, fmt.Sprintf(`
		SELECT id::text, request_id, COALESCE(provider_request_id, ''), COALESCE(upstream_log_id, ''),
		       session_id::text, user_id, source, call_type, provider, model, status, request_index,
		       started_at, ended_at,
		       LEFT(COALESCE(error_json #>> '{error,message}', response_json #>> '{error,message}', error_json::text, response_json::text, ''), 800),
		       %s, %s, %s
		FROM %s
		WHERE started_at >= $1 AND started_at <= $2
		  AND (request_json::text ILIKE $3 OR response_json::text ILIKE $3 OR error_json::text ILIKE $3 OR normalized_json::text ILIKE $3)
		ORDER BY started_at DESC
		LIMIT $4
	`, payloadExpr("request_json", includePayloads), payloadExpr("response_json", includePayloads), payloadExpr("error_json", includePayloads), table(schema, "session_trajectory_requests")), startAt, endAt, like, limit)
}

func queryRows(ctx context.Context, db *sql.DB, query string, args ...any) ([]row, error) {
	rs, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()

	var out []row
	for rs.Next() {
		var item row
		var ended sql.NullTime
		var requestJSON, responseJSON, errorJSON []byte
		if err := rs.Scan(
			&item.ID, &item.RequestID, &item.ProviderRequestID, &item.UpstreamLogID,
			&item.SessionID, &item.UserID, &item.Source, &item.CallType, &item.Provider, &item.Model,
			&item.Status, &item.RequestIndex, &item.StartedAt, &ended, &item.Message,
			&requestJSON, &responseJSON, &errorJSON,
		); err != nil {
			return nil, err
		}
		if ended.Valid {
			item.EndedAt = &ended.Time
		}
		item.RequestJSON = compactRaw(requestJSON)
		item.ResponseJSON = compactRaw(responseJSON)
		item.ErrorJSON = compactRaw(errorJSON)
		out = append(out, item)
	}
	return out, rs.Err()
}

func payloadExpr(column string, include bool) string {
	if !include {
		return "NULL::jsonb"
	}
	return column
}

func compactRaw(raw []byte) json.RawMessage {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.RawMessage(raw)
}

func table(schema, name string) string {
	return quoteIdent(schema) + "." + quoteIdent(name)
}

func quoteIdent(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func parseTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02 15:04:05Z07:00", value)
}

func loadDotEnv(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key != "" && os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
