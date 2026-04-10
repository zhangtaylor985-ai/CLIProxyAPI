package sessiontrajectory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

const exportVersion = "session-trajectory-v1"
const exportRequestsPageSize = 500

func (s *PostgresStore) ExportSession(ctx context.Context, sessionID string, exportRoot string) (SessionExportResult, error) {
	if s == nil || s.db == nil {
		return SessionExportResult{}, fmt.Errorf("session trajectory postgres: not initialized")
	}
	session, found, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return SessionExportResult{}, err
	}
	if !found {
		return SessionExportResult{}, fmt.Errorf("session trajectory postgres: session not found")
	}
	requests, err := s.listAllSessionRequests(ctx, sessionID, true)
	if err != nil {
		return SessionExportResult{}, err
	}
	root, err := resolveExportRoot(exportRoot)
	if err != nil {
		return SessionExportResult{}, err
	}
	exportDir := filepath.Join(root, exportSessionDirectoryName(session))
	if err := os.MkdirAll(exportDir, 0o755); err != nil {
		return SessionExportResult{}, fmt.Errorf("session trajectory postgres: create export dir: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SessionExportResult{}, fmt.Errorf("session trajectory postgres: begin export tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	files := make([]ExportedFile, 0, len(requests))
	exportedAt := time.Now().UTC()
	tokenTotals := ExportTokenTotals{}
	for _, request := range requests {
		exportIndex := request.RequestIndex
		exportPath := filepath.Join(exportDir, exportFileName(exportIndex, request.RequestID))
		payload, buildErr := buildExportPayload(session, request)
		if buildErr != nil {
			err = buildErr
			return SessionExportResult{}, err
		}
		raw, marshalErr := json.MarshalIndent(payload, "", "  ")
		if marshalErr != nil {
			err = fmt.Errorf("session trajectory postgres: marshal export payload: %w", marshalErr)
			return SessionExportResult{}, err
		}
		if writeErr := os.WriteFile(exportPath, raw, 0o644); writeErr != nil {
			err = fmt.Errorf("session trajectory postgres: write export file: %w", writeErr)
			return SessionExportResult{}, err
		}
		_, err = tx.ExecContext(ctx, fmt.Sprintf(`
			INSERT INTO %s (request_id, session_id, export_path, export_index, exported_at, export_version)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (request_id)
			DO UPDATE SET export_path = EXCLUDED.export_path,
			              export_index = EXCLUDED.export_index,
			              exported_at = EXCLUDED.exported_at,
			              export_version = EXCLUDED.export_version
		`, s.table("session_trajectory_request_exports")),
			request.ID,
			session.SessionID,
			exportPath,
			exportIndex,
			exportedAt,
			exportVersion,
		)
		if err != nil {
			err = fmt.Errorf("session trajectory postgres: upsert request export: %w", err)
			return SessionExportResult{}, err
		}
		files = append(files, ExportedFile{
			RequestID:    request.RequestID,
			RequestIndex: request.RequestIndex,
			ExportIndex:  exportIndex,
			ExportPath:   exportPath,
		})
		tokenTotals.InputTokens += request.InputTokens
		tokenTotals.OutputTokens += request.OutputTokens
		tokenTotals.ReasoningTokens += request.ReasoningTokens
		tokenTotals.CachedTokens += request.CachedTokens
		tokenTotals.TotalTokens += request.TotalTokens
	}

	if err = tx.Commit(); err != nil {
		return SessionExportResult{}, fmt.Errorf("session trajectory postgres: commit export tx: %w", err)
	}

	return SessionExportResult{
		SessionID:   session.SessionID,
		UserID:      session.UserID,
		ExportDir:   exportDir,
		FileCount:   len(files),
		ExportedAt:  exportedAt,
		TokenTotals: tokenTotals,
		Files:       files,
	}, nil
}

func (s *PostgresStore) InspectExportSession(ctx context.Context, sessionID string, exportRoot string) (SessionExportResult, bool, error) {
	if s == nil || s.db == nil {
		return SessionExportResult{}, false, fmt.Errorf("session trajectory postgres: not initialized")
	}
	session, found, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return SessionExportResult{}, false, err
	}
	if !found {
		return SessionExportResult{}, false, fmt.Errorf("session trajectory postgres: session not found")
	}
	requests, err := s.listAllSessionRequests(ctx, sessionID, false)
	if err != nil {
		return SessionExportResult{}, false, err
	}
	root, err := resolveExportRoot(exportRoot)
	if err != nil {
		return SessionExportResult{}, false, err
	}
	exportDir := filepath.Join(root, exportSessionDirectoryName(session))
	dirInfo, err := os.Stat(exportDir)
	if err != nil {
		if os.IsNotExist(err) {
			return SessionExportResult{}, false, nil
		}
		return SessionExportResult{}, false, fmt.Errorf("session trajectory postgres: stat export dir: %w", err)
	}
	if !dirInfo.IsDir() {
		return SessionExportResult{}, false, fmt.Errorf("session trajectory postgres: export path is not a directory: %s", exportDir)
	}

	files := make([]ExportedFile, 0, len(requests))
	tokenTotals := ExportTokenTotals{}
	latestModTime := dirInfo.ModTime().UTC()
	complete := true
	for _, request := range requests {
		exportPath := filepath.Join(exportDir, exportFileName(request.RequestIndex, request.RequestID))
		info, statErr := os.Stat(exportPath)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				complete = false
				break
			}
			return SessionExportResult{}, false, fmt.Errorf("session trajectory postgres: stat export file: %w", statErr)
		}
		if info.ModTime().After(latestModTime) {
			latestModTime = info.ModTime().UTC()
		}
		files = append(files, ExportedFile{
			RequestID:    request.RequestID,
			RequestIndex: request.RequestIndex,
			ExportIndex:  request.RequestIndex,
			ExportPath:   exportPath,
		})
		tokenTotals.InputTokens += request.InputTokens
		tokenTotals.OutputTokens += request.OutputTokens
		tokenTotals.ReasoningTokens += request.ReasoningTokens
		tokenTotals.CachedTokens += request.CachedTokens
		tokenTotals.TotalTokens += request.TotalTokens
	}

	if !complete {
		return SessionExportResult{}, false, nil
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].RequestIndex < files[j].RequestIndex
	})

	return SessionExportResult{
		SessionID:   session.SessionID,
		UserID:      session.UserID,
		ExportDir:   exportDir,
		FileCount:   len(files),
		ExportedAt:  latestModTime,
		TokenTotals: tokenTotals,
		Files:       files,
	}, true, nil
}

func (s *PostgresStore) ExportSessions(ctx context.Context, filter SessionExportFilter, exportRoot string) ([]SessionExportResult, error) {
	sessions, err := s.ListSessions(ctx, SessionListFilter{
		UserID:   filter.UserID,
		Source:   filter.Source,
		CallType: filter.CallType,
		Status:   filter.Status,
		Limit:    clampLimit(filter.Limit, 20, 100),
		Before:   filter.Before,
	})
	if err != nil {
		return nil, err
	}
	results := make([]SessionExportResult, 0, len(sessions))
	for _, session := range sessions {
		item, err := s.ExportSession(ctx, session.SessionID, exportRoot)
		if err != nil {
			return nil, err
		}
		results = append(results, item)
	}
	return results, nil
}

func (s *PostgresStore) listAllSessionRequests(ctx context.Context, sessionID string, includePayloads bool) ([]SessionRequest, error) {
	requests := make([]SessionRequest, 0, exportRequestsPageSize)
	var afterRequestIndex int64
	for {
		items, err := s.ListSessionRequests(ctx, SessionRequestFilter{
			SessionID:         sessionID,
			Limit:             exportRequestsPageSize,
			AfterRequestIndex: afterRequestIndex,
			IncludePayloads:   includePayloads,
		})
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			break
		}
		requests = append(requests, items...)
		afterRequestIndex = items[len(items)-1].RequestIndex
		if len(items) < exportRequestsPageSize {
			break
		}
	}
	return requests, nil
}

func buildExportPayload(session SessionSummary, request SessionRequest) (map[string]any, error) {
	normalized := normalizedConversation{}
	if len(request.NormalizedJSON) > 0 {
		_ = json.Unmarshal(request.NormalizedJSON, &normalized)
	}
	if len(normalized.System) == 0 || len(normalized.Tools) == 0 || len(normalized.Messages) == 0 {
		requestRoot := gjson.ParseBytes(request.RequestJSON)
		responseRoot := gjson.ParseBytes(request.ResponseJSON)
		if len(normalized.System) == 0 {
			normalized.System = cloneJSON(normalizeSystem(request.CallType, requestRoot))
		}
		if len(normalized.Tools) == 0 {
			normalized.Tools = cloneJSON(normalizeTools(request.CallType, requestRoot))
		}
		if len(normalized.Messages) == 0 {
			normalized.Messages = cloneJSON(normalizeMessages(request.CallType, requestRoot))
		}
		if normalized.ProviderSessionID == "" {
			normalized.ProviderSessionID = extractProviderSessionID(requestRoot, responseRoot)
		}
	}

	payload := map[string]any{
		"request_id":           request.RequestID,
		"session_id":           session.SessionID,
		"canonical_session_id": session.SessionID,
		"user_id":              request.UserID,
		"start_time":           request.StartedAt.UTC().Format(time.RFC3339Nano),
		"end_time":             formatTimePointer(request.EndedAt),
		"user_agent":           request.UserAgent,
		"call_type":            request.CallType,
		"status":               request.Status,
		"provider":             request.Provider,
		"model":                request.Model,
		"provider_session_id":  firstNonEmpty(normalized.ProviderSessionID, session.ProviderSessionID),
		"provider_request_id":  request.ProviderRequestID,
		"upstream_log_id":      request.UpstreamLogID,
		"request_index":        request.RequestIndex,
		"source":               request.Source,
		"normalized_by":        exportVersion,
	}
	payload["system"] = decodeJSONOrDefault(normalized.System, []any{})
	payload["tools"] = decodeJSONOrDefault(normalized.Tools, []any{})
	payload["messages"] = decodeJSONOrDefault(normalized.Messages, []any{})
	payload["response"] = decodeJSONOrDefault(request.ResponseJSON, nil)
	return payload, nil
}

func resolveExportRoot(exportRoot string) (string, error) {
	root := strings.TrimSpace(exportRoot)
	if root == "" {
		root = filepath.Join("session-data", "session-exports")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("session trajectory postgres: resolve export root: %w", err)
	}
	return abs, nil
}

func exportSessionDirectoryName(session SessionSummary) string {
	userID := sanitizePathSegment(session.UserID)
	sessionID := sanitizePathSegment(session.SessionID)
	if userID == "" || len(userID) > 80 || strings.Contains(userID, sessionID) {
		return sessionID
	}
	return userID + "_" + sessionID
}

func exportFileName(index int64, requestID string) string {
	name := sanitizePathSegment(requestID)
	if name == "" {
		return fmt.Sprintf("%06d.json", index)
	}
	return fmt.Sprintf("%06d_%s.json", index, name)
}

func sanitizePathSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var builder strings.Builder
	for _, ch := range value {
		valid := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_'
		if valid {
			builder.WriteRune(ch)
			continue
		}
		builder.WriteByte('_')
	}
	return strings.Trim(builder.String(), "_")
}

func decodeJSONOrDefault(raw []byte, fallback any) any {
	compacted := compactJSON(raw)
	if len(compacted) == 0 {
		return fallback
	}
	var value any
	if err := json.Unmarshal(compacted, &value); err != nil {
		return fallback
	}
	return value
}

func formatTimePointer(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}
