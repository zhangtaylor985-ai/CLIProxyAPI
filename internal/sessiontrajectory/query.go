package sessiontrajectory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func (s *PostgresStore) ListSessions(ctx context.Context, filter SessionListFilter) ([]SessionSummary, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("session trajectory postgres: not initialized")
	}
	limit := clampLimit(filter.Limit, 50, 200)
	args := make([]any, 0, 8)
	conditions := make([]string, 0, 8)
	appendCond := func(sql string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(sql, len(args)))
	}
	if value := strings.TrimSpace(filter.UserID); value != "" {
		appendCond("user_id = $%d", value)
	}
	if value := strings.TrimSpace(filter.Source); value != "" {
		appendCond("source = $%d", value)
	}
	if value := strings.TrimSpace(filter.CallType); value != "" {
		appendCond("call_type = $%d", value)
	}
	if value := strings.TrimSpace(filter.Status); value != "" {
		appendCond("status = $%d", value)
	}
	if value := strings.TrimSpace(filter.Provider); value != "" {
		appendCond("provider = $%d", value)
	}
	if value := strings.TrimSpace(filter.CanonicalModelFamily); value != "" {
		appendCond("canonical_model_family = $%d", value)
	}
	if !filter.Before.IsZero() {
		appendCond("last_activity_at < $%d", filter.Before.UTC())
	}

	query := fmt.Sprintf(`
		SELECT id, user_id, source, call_type, provider, canonical_model_family,
		       COALESCE(provider_session_id, ''), COALESCE(session_name, ''),
		       message_count, request_count, started_at, last_activity_at, closed_at,
		       status, metadata
		FROM %s
	`, s.table("session_trajectory_sessions"))
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += fmt.Sprintf(" ORDER BY last_activity_at DESC LIMIT %d", limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("session trajectory postgres: list sessions: %w", err)
	}
	defer rows.Close()

	result := make([]SessionSummary, 0, limit)
	for rows.Next() {
		item, err := scanSessionSummary(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("session trajectory postgres: list sessions rows: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) GetSession(ctx context.Context, sessionID string) (SessionSummary, bool, error) {
	if s == nil || s.db == nil {
		return SessionSummary{}, false, fmt.Errorf("session trajectory postgres: not initialized")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return SessionSummary{}, false, fmt.Errorf("session trajectory postgres: session id is required")
	}
	row := s.db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT id, user_id, source, call_type, provider, canonical_model_family,
		       COALESCE(provider_session_id, ''), COALESCE(session_name, ''),
		       message_count, request_count, started_at, last_activity_at, closed_at,
		       status, metadata
		FROM %s
		WHERE id = $1
	`, s.table("session_trajectory_sessions")), sessionID)
	item, err := scanSessionSummary(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionSummary{}, false, nil
	}
	if err != nil {
		return SessionSummary{}, false, err
	}
	return item, true, nil
}

func (s *PostgresStore) ListSessionRequests(ctx context.Context, filter SessionRequestFilter) ([]SessionRequest, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("session trajectory postgres: not initialized")
	}
	sessionID := strings.TrimSpace(filter.SessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("session trajectory postgres: session id is required")
	}
	limit := clampLimit(filter.Limit, 100, 500)
	query := `
		SELECT id, request_id, session_id, user_id,
		       COALESCE(provider_request_id, ''), COALESCE(upstream_log_id, ''),
		       request_index, source, call_type, provider, model, COALESCE(user_agent, ''),
		       status, started_at, ended_at, COALESCE(duration_ms, 0),
		       input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens,
		       cost_micro_usd`
	if filter.IncludePayloads {
		query += `,
		       request_json, response_json, normalized_json, error_json`
	}
	query += fmt.Sprintf(`
		FROM %s
		WHERE session_id = $1
	`, s.table("session_trajectory_requests"))
	args := []any{sessionID}
	if filter.AfterRequestIndex > 0 {
		args = append(args, filter.AfterRequestIndex)
		query += fmt.Sprintf(" AND request_index > $%d", len(args))
	}
	query += fmt.Sprintf(" ORDER BY request_index ASC LIMIT %d", limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("session trajectory postgres: list session requests: %w", err)
	}
	defer rows.Close()

	result := make([]SessionRequest, 0, limit)
	for rows.Next() {
		item, err := scanSessionRequest(rows, filter.IncludePayloads)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("session trajectory postgres: list session requests rows: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) ListSessionTokenRounds(ctx context.Context, sessionID string, limit int) ([]SessionTokenRound, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("session trajectory postgres: not initialized")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("session trajectory postgres: session id is required")
	}
	limit = clampLimit(limit, 100, 500)
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT request_id, request_index, started_at, ended_at, model, status,
		       input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens
		FROM %s
		WHERE session_id = $1
		ORDER BY request_index ASC
		LIMIT %d
	`, s.table("session_trajectory_requests"), limit), sessionID)
	if err != nil {
		return nil, fmt.Errorf("session trajectory postgres: list session token rounds: %w", err)
	}
	defer rows.Close()

	result := make([]SessionTokenRound, 0, limit)
	for rows.Next() {
		var (
			item  SessionTokenRound
			ended sql.NullTime
		)
		if err := rows.Scan(
			&item.RequestID,
			&item.RequestIndex,
			&item.StartedAt,
			&ended,
			&item.Model,
			&item.Status,
			&item.InputTokens,
			&item.OutputTokens,
			&item.ReasoningTokens,
			&item.CachedTokens,
			&item.TotalTokens,
		); err != nil {
			return nil, fmt.Errorf("session trajectory postgres: scan session token round: %w", err)
		}
		if ended.Valid {
			value := ended.Time.UTC()
			item.EndedAt = &value
		}
		item.StartedAt = item.StartedAt.UTC()
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("session trajectory postgres: list session token rounds rows: %w", err)
	}
	return result, nil
}

func scanSessionSummary(scanner interface {
	Scan(dest ...any) error
}) (SessionSummary, error) {
	var (
		item     SessionSummary
		closedAt sql.NullTime
		metadata []byte
	)
	err := scanner.Scan(
		&item.SessionID,
		&item.UserID,
		&item.Source,
		&item.CallType,
		&item.Provider,
		&item.CanonicalModelFamily,
		&item.ProviderSessionID,
		&item.SessionName,
		&item.MessageCount,
		&item.RequestCount,
		&item.StartedAt,
		&item.LastActivityAt,
		&closedAt,
		&item.Status,
		&metadata,
	)
	if err != nil {
		return SessionSummary{}, fmt.Errorf("session trajectory postgres: scan session summary: %w", err)
	}
	item.StartedAt = item.StartedAt.UTC()
	item.LastActivityAt = item.LastActivityAt.UTC()
	if closedAt.Valid {
		value := closedAt.Time.UTC()
		item.ClosedAt = &value
	}
	if compacted := compactJSON(metadata); len(compacted) > 0 {
		item.Metadata = append(json.RawMessage(nil), compacted...)
	}
	return item, nil
}

func scanSessionRequest(scanner interface {
	Scan(dest ...any) error
}, includePayloads bool) (SessionRequest, error) {
	var (
		item        SessionRequest
		endedAt     sql.NullTime
		requestRaw  []byte
		responseRaw []byte
		normRaw     []byte
		errorRaw    []byte
	)
	dest := []any{
		&item.ID,
		&item.RequestID,
		&item.SessionID,
		&item.UserID,
		&item.ProviderRequestID,
		&item.UpstreamLogID,
		&item.RequestIndex,
		&item.Source,
		&item.CallType,
		&item.Provider,
		&item.Model,
		&item.UserAgent,
		&item.Status,
		&item.StartedAt,
		&endedAt,
		&item.DurationMS,
		&item.InputTokens,
		&item.OutputTokens,
		&item.ReasoningTokens,
		&item.CachedTokens,
		&item.TotalTokens,
		&item.CostMicroUSD,
	}
	if includePayloads {
		dest = append(dest, &requestRaw, &responseRaw, &normRaw, &errorRaw)
	}
	if err := scanner.Scan(dest...); err != nil {
		return SessionRequest{}, fmt.Errorf("session trajectory postgres: scan session request: %w", err)
	}
	item.StartedAt = item.StartedAt.UTC()
	if endedAt.Valid {
		value := endedAt.Time.UTC()
		item.EndedAt = &value
	}
	if includePayloads {
		item.RequestJSON = cloneJSON(compactJSON(requestRaw))
		item.ResponseJSON = cloneJSON(compactJSON(responseRaw))
		item.NormalizedJSON = cloneJSON(compactJSON(normRaw))
		item.ErrorJSON = cloneJSON(compactJSON(errorRaw))
	}
	return item, nil
}

func clampLimit(value, fallback, max int) int {
	if value <= 0 {
		return fallback
	}
	if value > max {
		return max
	}
	return value
}
