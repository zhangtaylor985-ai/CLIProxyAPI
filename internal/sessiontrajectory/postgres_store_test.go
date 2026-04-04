package sessiontrajectory

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresStore_ReusesSessionOnMessagePrefixContinuation(t *testing.T) {
	store, cleanup := newPostgresSessionTrajectoryTestStore(t)
	defer cleanup()

	apiKey := "sk-user-a"
	headers := map[string][]string{
		"Authorization": {"Bearer " + apiKey},
		"User-Agent":    {"claude-cli/2.0.0"},
	}
	firstResponseAt := time.Now().UTC()
	err := store.Record(context.Background(), &CompletedRequest{
		RequestID:         "req-1",
		RequestMethod:     "POST",
		RequestURL:        "/v1/messages",
		RequestHeaders:    headers,
		RequestBody:       []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`),
		ResponseHeaders:   map[string][]string{"X-Request-Id": {"upstream-1"}},
		ResponseBody:      []byte(`{"id":"msg-1","model":"claude-sonnet-4-5","usage":{"input_tokens":10,"output_tokens":20}}`),
		RequestTimestamp:  firstResponseAt.Add(-2 * time.Second),
		ResponseTimestamp: firstResponseAt,
	})
	if err != nil {
		t.Fatalf("store.Record first: %v", err)
	}

	secondResponseAt := firstResponseAt.Add(10 * time.Second)
	err = store.Record(context.Background(), &CompletedRequest{
		RequestID:         "req-2",
		RequestMethod:     "POST",
		RequestURL:        "/v1/messages",
		RequestHeaders:    headers,
		RequestBody:       []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]},{"role":"assistant","content":[{"type":"text","text":"hi"}]},{"role":"user","content":[{"type":"text","text":"continue"}]}]}`),
		ResponseHeaders:   map[string][]string{"X-Request-Id": {"upstream-2"}},
		ResponseBody:      []byte(`{"id":"msg-2","model":"claude-sonnet-4-5","usage":{"input_tokens":15,"output_tokens":30}}`),
		RequestTimestamp:  secondResponseAt.Add(-3 * time.Second),
		ResponseTimestamp: secondResponseAt,
	})
	if err != nil {
		t.Fatalf("store.Record second: %v", err)
	}

	rows, err := store.db.Query(`
		SELECT session_id, request_index
		FROM ` + store.table("session_trajectory_requests") + `
		ORDER BY request_index ASC`)
	if err != nil {
		t.Fatalf("query requests: %v", err)
	}
	defer rows.Close()

	var (
		sessionIDs []string
		indexes    []int64
	)
	for rows.Next() {
		var sessionID string
		var requestIndex int64
		if err := rows.Scan(&sessionID, &requestIndex); err != nil {
			t.Fatalf("scan request row: %v", err)
		}
		sessionIDs = append(sessionIDs, sessionID)
		indexes = append(indexes, requestIndex)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}
	if len(sessionIDs) != 2 {
		t.Fatalf("request count = %d, want 2", len(sessionIDs))
	}
	if sessionIDs[0] != sessionIDs[1] {
		t.Fatalf("expected same session id, got %q and %q", sessionIDs[0], sessionIDs[1])
	}
	if indexes[0] != 1 || indexes[1] != 2 {
		t.Fatalf("request indexes = %#v, want [1 2]", indexes)
	}
}

func TestPostgresStore_ReusesSessionOnProviderAlias(t *testing.T) {
	store, cleanup := newPostgresSessionTrajectoryTestStore(t)
	defer cleanup()

	headers := map[string][]string{
		"Authorization": {"Bearer sk-user-b"},
		"User-Agent":    {"claude-cli/2.0.0"},
	}
	now := time.Now().UTC()
	firstRequest := []byte(`{"model":"claude-sonnet-4-5","metadata":{"session_id":"provider-session-1"},"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
	secondRequest := []byte(`{"model":"claude-sonnet-4-5","metadata":{"session_id":"provider-session-1"},"messages":[{"role":"user","content":[{"type":"text","text":"totally different"}]}]}`)

	if err := store.Record(context.Background(), &CompletedRequest{
		RequestID:         "req-alias-1",
		RequestMethod:     "POST",
		RequestURL:        "/v1/messages",
		RequestHeaders:    headers,
		RequestBody:       firstRequest,
		ResponseBody:      []byte(`{"id":"msg-a","model":"claude-sonnet-4-5"}`),
		RequestTimestamp:  now.Add(-2 * time.Second),
		ResponseTimestamp: now,
	}); err != nil {
		t.Fatalf("store.Record first: %v", err)
	}
	if err := store.Record(context.Background(), &CompletedRequest{
		RequestID:         "req-alias-2",
		RequestMethod:     "POST",
		RequestURL:        "/v1/messages",
		RequestHeaders:    headers,
		RequestBody:       secondRequest,
		ResponseBody:      []byte(`{"id":"msg-b","model":"claude-sonnet-4-5"}`),
		RequestTimestamp:  now.Add(5 * time.Second),
		ResponseTimestamp: now.Add(6 * time.Second),
	}); err != nil {
		t.Fatalf("store.Record second: %v", err)
	}

	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM `+store.table("session_trajectory_session_aliases")+` WHERE provider_session_id = $1`, "provider-session-1").Scan(&count); err != nil {
		t.Fatalf("query alias count: %v", err)
	}
	if count != 1 {
		t.Fatalf("alias count = %d, want 1", count)
	}

	var distinctSessions int
	if err := store.db.QueryRow(`SELECT COUNT(DISTINCT session_id) FROM ` + store.table("session_trajectory_requests")).Scan(&distinctSessions); err != nil {
		t.Fatalf("query distinct sessions: %v", err)
	}
	if distinctSessions != 1 {
		t.Fatalf("distinct session count = %d, want 1", distinctSessions)
	}
}

func TestPostgresStore_ReusesSessionOnStructuredMetadataUserIDSessionID(t *testing.T) {
	store, cleanup := newPostgresSessionTrajectoryTestStore(t)
	defer cleanup()

	headers := map[string][]string{
		"Authorization": {"Bearer sk-user-c"},
		"User-Agent":    {"claude-cli/2.1.92 (external, sdk-cli)"},
	}
	now := time.Now().UTC()
	firstRequest := []byte(`{"model":"gpt-5.4","metadata":{"user_id":"{\"device_id\":\"abc\",\"account_uuid\":\"\",\"session_id\":\"structured-session-1\"}"},"messages":[{"role":"user","content":[{"type":"text","text":"Reply with exactly ISO_OK"}]}]}`)
	secondRequest := []byte(`{"model":"gpt-5.4","metadata":{"user_id":"{\"device_id\":\"abc\",\"account_uuid\":\"\",\"session_id\":\"structured-session-1\"}"},"messages":[{"role":"user","content":[{"type":"text","text":"What was my previous instruction? Reply with exactly: ISO_PREV"}]},{"role":"assistant","content":[{"type":"text","text":"ISO_OK"}]},{"role":"user","content":[{"type":"text","text":"Repeat it"}]}]}`)

	if err := store.Record(context.Background(), &CompletedRequest{
		RequestID:         "req-structured-1",
		RequestMethod:     "POST",
		RequestURL:        "/v1/messages",
		RequestHeaders:    headers,
		RequestBody:       firstRequest,
		ResponseBody:      []byte(`{"id":"msg-structured-1","model":"gpt-5.4","usage":{"input_tokens":10,"output_tokens":5}}`),
		RequestTimestamp:  now.Add(-2 * time.Second),
		ResponseTimestamp: now,
	}); err != nil {
		t.Fatalf("store.Record first: %v", err)
	}
	if err := store.Record(context.Background(), &CompletedRequest{
		RequestID:         "req-structured-2",
		RequestMethod:     "POST",
		RequestURL:        "/v1/messages",
		RequestHeaders:    headers,
		RequestBody:       secondRequest,
		ResponseBody:      []byte(`{"id":"msg-structured-2","model":"gpt-5.4","usage":{"input_tokens":20,"output_tokens":8}}`),
		RequestTimestamp:  now.Add(5 * time.Second),
		ResponseTimestamp: now.Add(6 * time.Second),
	}); err != nil {
		t.Fatalf("store.Record second: %v", err)
	}

	var distinctSessions int
	if err := store.db.QueryRow(`SELECT COUNT(DISTINCT session_id) FROM ` + store.table("session_trajectory_requests")).Scan(&distinctSessions); err != nil {
		t.Fatalf("query distinct sessions: %v", err)
	}
	if distinctSessions != 1 {
		t.Fatalf("distinct session count = %d, want 1", distinctSessions)
	}

	var requestCount int64
	if err := store.db.QueryRow(`SELECT request_count FROM ` + store.table("session_trajectory_sessions") + ` LIMIT 1`).Scan(&requestCount); err != nil {
		t.Fatalf("query request count: %v", err)
	}
	if requestCount != 2 {
		t.Fatalf("request_count = %d, want 2", requestCount)
	}
}

func newPostgresSessionTrajectoryTestStore(t *testing.T) (*PostgresStore, func()) {
	t.Helper()

	dsn := strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set")
	}
	schema := fmt.Sprintf("test_%d_%s", time.Now().UnixNano(), sanitizeSessionTrajectoryIdentifier(t.Name()))

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		t.Fatalf("ping postgres: %v", err)
	}

	store, err := NewPostgresStore(context.Background(), PostgresStoreConfig{
		DSN:    dsn,
		Schema: schema,
	})
	if err != nil {
		_ = db.Close()
		t.Fatalf("NewPostgresStore: %v", err)
	}

	cleanup := func() {
		_ = store.Close()
		_, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + quoteSessionTrajectoryIdentifier(schema) + ` CASCADE`)
		_ = db.Close()
	}
	return store, cleanup
}

func sanitizeSessionTrajectoryIdentifier(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "test"
	}
	var builder strings.Builder
	for _, ch := range value {
		valid := (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')
		if valid {
			builder.WriteRune(ch)
			continue
		}
		builder.WriteByte('_')
	}
	return strings.Trim(builder.String(), "_")
}

func quoteSessionTrajectoryIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
