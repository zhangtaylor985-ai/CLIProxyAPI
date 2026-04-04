package sessiontrajectory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type PostgresStoreConfig struct {
	DSN    string
	Schema string
}

type PostgresStore struct {
	db     *sql.DB
	schema string
}

type recentSessionCandidate struct {
	UserID          string
	SessionID       string
	RequestCount    int64
	LastActivityAt  time.Time
	NormalizedJSON  []byte
	ResponseJSON    []byte
	SessionStatus   string
	ProviderSession string
}

func NewPostgresStore(ctx context.Context, cfg PostgresStoreConfig) (*PostgresStore, error) {
	dsn := strings.TrimSpace(cfg.DSN)
	if dsn == "" {
		return nil, fmt.Errorf("session trajectory postgres: DSN is required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("session trajectory postgres: open database: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("session trajectory postgres: ping database: %w", err)
	}
	store := &PostgresStore{
		db:     db,
		schema: strings.TrimSpace(cfg.Schema),
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

func (s *PostgresStore) Record(ctx context.Context, record *CompletedRequest) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("session trajectory postgres: not initialized")
	}
	normalized, requestJSON, responseJSON, normalizedJSON, err := normalizeCompletedRequest(record)
	if err != nil {
		return err
	}
	if normalized == nil {
		return nil
	}

	if normalized.UserID == "" {
		normalized.UserID = deriveUserID(record)
	}
	if normalized.UserID == "" {
		return nil
	}
	if payload, marshalErr := json.Marshal(normalized); marshalErr == nil {
		normalizedJSON = payload
	}

	status := StatusSuccess
	if record.ResponseStatusCode >= 400 || len(record.APIResponseErrors) > 0 {
		status = StatusError
	}

	startedAt := record.RequestTimestamp.UTC()
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	endedAt := record.ResponseTimestamp.UTC()
	if endedAt.IsZero() {
		if !record.APIResponseTimestamp.IsZero() {
			endedAt = record.APIResponseTimestamp.UTC()
		} else {
			endedAt = time.Now().UTC()
		}
	}
	if endedAt.Before(startedAt) {
		endedAt = startedAt
	}

	requestID := strings.TrimSpace(record.RequestID)
	if requestID == "" {
		requestID = uuid.NewString()
	}

	requestPK := uuid.NewString()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("session trajectory postgres: begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	sessionID, requestIndex, err := s.resolveSessionTx(ctx, tx, normalized, startedAt, endedAt, status)
	if err != nil {
		return err
	}

	errorJSON := []byte(nil)
	if status == StatusError {
		if len(responseJSON) > 0 {
			errorJSON = responseJSON
		} else if len(record.APIResponseErrors) > 0 {
			if payload, marshalErr := json.Marshal(record.APIResponseErrors); marshalErr == nil {
				errorJSON = payload
			}
		}
	}

	_, err = tx.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (
			id, request_id, session_id, user_id, provider_request_id, upstream_log_id,
			request_index, source, call_type, provider, model, user_agent, status,
			started_at, ended_at, duration_ms,
			input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens,
			request_json, response_json, normalized_json, error_json
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11, $12, $13,
			$14, $15, $16,
			$17, $18, $19, $20, $21,
			$22, $23, $24, $25
		)
	`, s.table("session_trajectory_requests")),
		requestPK,
		requestID,
		sessionID,
		normalized.UserID,
		nullableString(normalized.ProviderRequestID),
		nullableString(normalized.UpstreamLogID),
		requestIndex,
		normalized.Source,
		normalized.CallType,
		normalized.Provider,
		normalized.Model,
		nullableString(normalized.UserAgent),
		status,
		startedAt,
		endedAt,
		durationMillis(startedAt, endedAt),
		normalized.Usage.InputTokens,
		normalized.Usage.OutputTokens,
		normalized.Usage.ReasoningTokens,
		normalized.Usage.CachedTokens,
		normalized.Usage.TotalTokens,
		jsonOrEmptyObject(requestJSON),
		jsonOrNull(responseJSON),
		jsonOrNull(normalizedJSON),
		jsonOrNull(errorJSON),
	)
	if err != nil {
		return fmt.Errorf("session trajectory postgres: insert request: %w", err)
	}

	sessionStatus := SessionStatusActive
	if status == StatusError {
		sessionStatus = SessionStatusError
	}
	_, err = tx.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET provider_session_id = COALESCE(NULLIF(provider_session_id, ''), NULLIF($2, '')),
			request_count = $3,
			message_count = $4,
			last_activity_at = $5,
			status = $6,
			metadata = metadata || $7::jsonb
		WHERE id = $1
	`, s.table("session_trajectory_sessions")),
		sessionID,
		normalized.ProviderSessionID,
		requestIndex,
		normalized.MessageCount,
		endedAt,
		sessionStatus,
		sessionMetadataJSON(normalized),
	)
	if err != nil {
		return fmt.Errorf("session trajectory postgres: update session: %w", err)
	}

	if normalized.ProviderSessionID != "" {
		_, err = tx.ExecContext(ctx, fmt.Sprintf(`
			INSERT INTO %s (provider_session_id, session_id, user_id, source, created_at, updated_at)
			VALUES ($1, $2, $3, $4, NOW(), NOW())
			ON CONFLICT (provider_session_id)
			DO UPDATE SET session_id = EXCLUDED.session_id, user_id = EXCLUDED.user_id, source = EXCLUDED.source, updated_at = NOW()
		`, s.table("session_trajectory_session_aliases")),
			normalized.ProviderSessionID,
			sessionID,
			normalized.UserID,
			normalized.Source,
		)
		if err != nil {
			return fmt.Errorf("session trajectory postgres: upsert session alias: %w", err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("session trajectory postgres: commit tx: %w", err)
	}
	return nil
}

func (s *PostgresStore) resolveSessionTx(ctx context.Context, tx *sql.Tx, normalized *normalizedConversation, startedAt, endedAt time.Time, status string) (string, int64, error) {
	if normalized == nil {
		return "", 0, fmt.Errorf("session trajectory postgres: normalized request is required")
	}

	if normalized.ProviderSessionID != "" {
		sessionID, requestCount, found, err := s.findSessionByAliasTx(ctx, tx, normalized.ProviderSessionID)
		if err != nil {
			return "", 0, err
		}
		if found {
			return sessionID, requestCount + 1, nil
		}
	}

	sessionID, requestCount, found, err := s.matchRecentSessionTx(ctx, tx, normalized, endedAt)
	if err != nil {
		return "", 0, err
	}
	if found {
		return sessionID, requestCount + 1, nil
	}

	sessionID = uuid.NewString()
	_, err = tx.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (
			id, user_id, source, call_type, provider, canonical_model_family,
			provider_session_id, session_name, message_count, request_count,
			started_at, last_activity_at, closed_at, status, metadata
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, NULL, $8, 0,
			$9, $10, NULL, $11, $12::jsonb
		)
	`, s.table("session_trajectory_sessions")),
		sessionID,
		normalized.UserID,
		normalized.Source,
		normalized.CallType,
		normalized.Provider,
		normalized.CanonicalModelFamily,
		nullableString(normalized.ProviderSessionID),
		normalized.MessageCount,
		startedAt,
		endedAt,
		sessionStatusFromRequestStatus(status),
		sessionMetadataJSON(normalized),
	)
	if err != nil {
		return "", 0, fmt.Errorf("session trajectory postgres: insert session: %w", err)
	}
	return sessionID, 1, nil
}

func (s *PostgresStore) findSessionByAliasTx(ctx context.Context, tx *sql.Tx, providerSessionID string) (string, int64, bool, error) {
	var sessionID string
	err := tx.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT session_id
		FROM %s
		WHERE provider_session_id = $1
	`, s.table("session_trajectory_session_aliases")), providerSessionID).Scan(&sessionID)
	if err == sql.ErrNoRows {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, fmt.Errorf("session trajectory postgres: query alias: %w", err)
	}

	requestCount, found, err := s.lockSessionForUpdateTx(ctx, tx, sessionID)
	if err != nil {
		return "", 0, false, err
	}
	if !found {
		return "", 0, false, nil
	}
	return sessionID, requestCount, true, nil
}

func (s *PostgresStore) matchRecentSessionTx(ctx context.Context, tx *sql.Tx, normalized *normalizedConversation, endedAt time.Time) (string, int64, bool, error) {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`
		SELECT s.user_id, s.id, s.request_count, s.last_activity_at, s.status, s.provider_session_id, r.normalized_json, r.response_json
		FROM %s s
		JOIN LATERAL (
			SELECT normalized_json, response_json
			FROM %s
			WHERE session_id = s.id
			ORDER BY request_index DESC
			LIMIT 1
		) r ON TRUE
		WHERE s.user_id = $1
		  AND s.source = $2
		  AND s.call_type = $3
		  AND s.last_activity_at >= $4
		ORDER BY s.last_activity_at DESC
		LIMIT %d
	`, s.table("session_trajectory_sessions"), s.table("session_trajectory_requests"), defaultRecentCandidates),
		normalized.UserID,
		normalized.Source,
		normalized.CallType,
		endedAt.Add(-defaultActiveWindow),
	)
	if err != nil {
		return "", 0, false, fmt.Errorf("session trajectory postgres: query recent sessions: %w", err)
	}
	defer rows.Close()

	candidates := make([]recentSessionCandidate, 0, defaultRecentCandidates)
	for rows.Next() {
		var (
			candidate       recentSessionCandidate
			providerSession sql.NullString
		)
		if err := rows.Scan(&candidate.UserID, &candidate.SessionID, &candidate.RequestCount, &candidate.LastActivityAt, &candidate.SessionStatus, &providerSession, &candidate.NormalizedJSON, &candidate.ResponseJSON); err != nil {
			return "", 0, false, fmt.Errorf("session trajectory postgres: scan recent session: %w", err)
		}
		if providerSession.Valid {
			candidate.ProviderSession = providerSession.String
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return "", 0, false, fmt.Errorf("session trajectory postgres: recent session rows: %w", err)
	}

	for _, candidate := range candidates {
		var previous normalizedConversation
		if err := json.Unmarshal(candidate.NormalizedJSON, &previous); err != nil {
			continue
		}
		if previous.UserID == "" {
			previous.UserID = candidate.UserID
		}
		if !sessionsCompatible(&previous, normalized) {
			continue
		}
		candidateMessages := previous.Messages
		if augmented := appendAssistantResponseToMessages(previous.CallType, previous.Messages, candidate.ResponseJSON); len(augmented) > 0 {
			candidateMessages = augmented
		}
		if !messagesExactlyMatch(previous.Messages, normalized.Messages) &&
			!messagesPrefixMatch(previous.Messages, normalized.Messages) &&
			!messagesExactlyMatch(candidateMessages, normalized.Messages) &&
			!messagesPrefixMatch(candidateMessages, normalized.Messages) {
			if endedAt.Sub(candidate.LastActivityAt) > defaultStrongMatchWindow {
				continue
			}
			continue
		}
		requestCount, found, err := s.lockSessionForUpdateTx(ctx, tx, candidate.SessionID)
		if err != nil {
			return "", 0, false, err
		}
		if !found {
			continue
		}
		return candidate.SessionID, requestCount, true, nil
	}
	return "", 0, false, nil
}

func sessionsCompatible(previous, current *normalizedConversation) bool {
	if previous == nil || current == nil {
		return false
	}
	if previous.UserID != current.UserID {
		return false
	}
	if previous.Source != current.Source || previous.CallType != current.CallType {
		return false
	}
	if previous.SystemHash != current.SystemHash {
		return false
	}
	if previous.ToolsHash != current.ToolsHash {
		return false
	}
	return true
}

func (s *PostgresStore) lockSessionForUpdateTx(ctx context.Context, tx *sql.Tx, sessionID string) (int64, bool, error) {
	var requestCount int64
	err := tx.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT request_count
		FROM %s
		WHERE id = $1
		FOR UPDATE
	`, s.table("session_trajectory_sessions")), sessionID).Scan(&requestCount)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("session trajectory postgres: lock session: %w", err)
	}
	return requestCount, true, nil
}

func (s *PostgresStore) ensureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("session trajectory postgres: not initialized")
	}
	if s.schema != "" {
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, quoteIdentifier(s.schema))); err != nil {
			return fmt.Errorf("session trajectory postgres: create schema: %w", err)
		}
	}
	statements := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			id UUID PRIMARY KEY,
			user_id TEXT NOT NULL,
			source TEXT NOT NULL,
			call_type TEXT NOT NULL,
			provider TEXT NOT NULL,
			canonical_model_family TEXT NOT NULL,
			provider_session_id TEXT NULL,
			session_name TEXT NULL,
			message_count BIGINT NOT NULL DEFAULT 0,
			request_count BIGINT NOT NULL DEFAULT 0,
			started_at TIMESTAMPTZ NOT NULL,
			last_activity_at TIMESTAMPTZ NOT NULL,
			closed_at TIMESTAMPTZ NULL,
			status TEXT NOT NULL,
			metadata JSONB NOT NULL DEFAULT '{}'::jsonb
		)`, s.table("session_trajectory_sessions")),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			id UUID PRIMARY KEY,
			request_id TEXT NOT NULL UNIQUE,
			session_id UUID NOT NULL REFERENCES %s(id),
			user_id TEXT NOT NULL,
			provider_request_id TEXT NULL,
			upstream_log_id TEXT NULL,
			request_index BIGINT NOT NULL,
			source TEXT NOT NULL,
			call_type TEXT NOT NULL,
			provider TEXT NOT NULL,
			model TEXT NOT NULL,
			user_agent TEXT NULL,
			status TEXT NOT NULL,
			started_at TIMESTAMPTZ NOT NULL,
			ended_at TIMESTAMPTZ NULL,
			duration_ms BIGINT NULL,
			input_tokens BIGINT NOT NULL DEFAULT 0,
			output_tokens BIGINT NOT NULL DEFAULT 0,
			reasoning_tokens BIGINT NOT NULL DEFAULT 0,
			cached_tokens BIGINT NOT NULL DEFAULT 0,
			total_tokens BIGINT NOT NULL DEFAULT 0,
			cost_micro_usd BIGINT NOT NULL DEFAULT 0,
			request_json JSONB NOT NULL,
			response_json JSONB NULL,
			normalized_json JSONB NULL,
			error_json JSONB NULL,
			UNIQUE (session_id, request_index)
		)`, s.table("session_trajectory_requests"), s.table("session_trajectory_sessions")),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			provider_session_id TEXT PRIMARY KEY,
			session_id UUID NOT NULL REFERENCES %s(id),
			user_id TEXT NOT NULL,
			source TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`, s.table("session_trajectory_session_aliases"), s.table("session_trajectory_sessions")),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			request_id UUID PRIMARY KEY REFERENCES %s(id),
			session_id UUID NOT NULL REFERENCES %s(id),
			export_path TEXT NOT NULL,
			export_index BIGINT NOT NULL,
			exported_at TIMESTAMPTZ NOT NULL,
			export_version TEXT NOT NULL
		)`, s.table("session_trajectory_request_exports"), s.table("session_trajectory_requests"), s.table("session_trajectory_sessions")),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (user_id, last_activity_at DESC)`,
			quoteIdentifier("idx_session_trajectory_sessions_user_activity"), s.table("session_trajectory_sessions")),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (provider_session_id) WHERE provider_session_id IS NOT NULL`,
			quoteIdentifier("idx_session_trajectory_sessions_provider_session"), s.table("session_trajectory_sessions")),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (source, last_activity_at DESC)`,
			quoteIdentifier("idx_session_trajectory_sessions_source_activity"), s.table("session_trajectory_sessions")),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (session_id, request_index)`,
			quoteIdentifier("idx_session_trajectory_requests_session_request_index"), s.table("session_trajectory_requests")),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (user_id, started_at DESC)`,
			quoteIdentifier("idx_session_trajectory_requests_user_started_at"), s.table("session_trajectory_requests")),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (provider_request_id) WHERE provider_request_id IS NOT NULL`,
			quoteIdentifier("idx_session_trajectory_requests_provider_request"), s.table("session_trajectory_requests")),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (upstream_log_id) WHERE upstream_log_id IS NOT NULL`,
			quoteIdentifier("idx_session_trajectory_requests_upstream_log"), s.table("session_trajectory_requests")),
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("session trajectory postgres: ensure schema: %w", err)
		}
	}
	return nil
}

func (s *PostgresStore) table(name string) string {
	if s.schema == "" {
		return quoteIdentifier(name)
	}
	return quoteIdentifier(s.schema) + "." + quoteIdentifier(name)
}

func deriveUserID(record *CompletedRequest) string {
	if record == nil {
		return ""
	}
	for _, headerName := range []string{"X-User-Id", "X-CLIProxy-User"} {
		if value := strings.TrimSpace(firstHeader(record.RequestHeaders, headerName)); value != "" {
			return value
		}
	}
	apiKey := extractInboundAPIKey(record.RequestHeaders)
	if apiKey == "" {
		return ""
	}
	return "api_key:" + hashBytes([]byte(apiKey))[:24]
}

func extractInboundAPIKey(headers map[string][]string) string {
	auth := strings.TrimSpace(firstHeader(headers, "Authorization"))
	if auth != "" {
		parts := strings.SplitN(auth, " ", 2)
		if len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[0]), "bearer") {
			return strings.TrimSpace(parts[1])
		}
		return auth
	}
	for _, name := range []string{"X-Api-Key", "X-Goog-Api-Key"} {
		if value := strings.TrimSpace(firstHeader(headers, name)); value != "" {
			return value
		}
	}
	return ""
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func durationMillis(startedAt, endedAt time.Time) int64 {
	if endedAt.Before(startedAt) {
		return 0
	}
	return endedAt.Sub(startedAt).Milliseconds()
}

func jsonOrEmptyObject(value []byte) string {
	if compacted := compactJSON(value); len(compacted) > 0 {
		return string(compacted)
	}
	return `{}`
}

func jsonOrNull(value []byte) any {
	if compacted := compactJSON(value); len(compacted) > 0 {
		return string(compacted)
	}
	return nil
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}

func sessionMetadataJSON(normalized *normalizedConversation) string {
	payload := map[string]any{
		"user_agent": normalized.UserAgent,
		"model":      normalized.Model,
		"provider":   normalized.Provider,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return `{}`
	}
	return string(raw)
}

func sessionStatusFromRequestStatus(status string) string {
	if status == StatusError {
		return SessionStatusError
	}
	return SessionStatusActive
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
