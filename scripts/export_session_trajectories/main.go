package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/apikeyconfig"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/sessiontrajectory"
)

const exportManifestVersion = "session-trajectory-export-manifest-v1"

type exportConfig struct {
	PostgresDSN          string
	PostgresSchema       string
	ExportRoot           string
	ManifestDir          string
	SessionIDFile        string
	WriteSessionIDFile   string
	UserID               string
	Source               string
	CallType             string
	Status               string
	Provider             string
	CanonicalModelFamily string
	StartTimeRaw         string
	EndTimeRaw           string
	PageSize             int
	ConnectTimeout       time.Duration
	DryRun               bool
	SkipExisting         bool
}

type exportFilters struct {
	UserID               string     `json:"user_id,omitempty"`
	Source               string     `json:"source,omitempty"`
	CallType             string     `json:"call_type,omitempty"`
	Status               string     `json:"status,omitempty"`
	Provider             string     `json:"provider,omitempty"`
	CanonicalModelFamily string     `json:"canonical_model_family,omitempty"`
	StartTime            *time.Time `json:"start_time,omitempty"`
	EndTime              *time.Time `json:"end_time,omitempty"`
	PageSize             int        `json:"page_size"`
	DryRun               bool       `json:"dry_run"`
}

type exportManifest struct {
	Version          string                                  `json:"version"`
	ExportedAt       time.Time                               `json:"exported_at"`
	ExportRoot       string                                  `json:"export_root"`
	ManifestPath     string                                  `json:"manifest_path"`
	Filters          exportFilters                           `json:"filters"`
	TotalSessions    int64                                   `json:"total_sessions"`
	ExportedSessions int                                     `json:"exported_sessions"`
	ExportedFiles    int                                     `json:"exported_files"`
	TokenTotals      sessiontrajectory.ExportTokenTotals     `json:"token_totals"`
	Items            []sessiontrajectory.SessionExportResult `json:"items"`
}

type pagedSession struct {
	sessiontrajectory.SessionSummary
}

type sessionCursor struct {
	LastActivityAt time.Time
	SessionID      string
}

func main() {
	loadDotEnv()
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		log.Fatalf("session trajectory export failed: %v", err)
	}
}

func loadDotEnv() {
	wd, err := os.Getwd()
	if err != nil {
		return
	}
	_ = godotenv.Load(filepath.Join(wd, ".env"))
}

func parseFlags() exportConfig {
	defaultDSN, defaultSchema := apikeyconfig.ResolvePostgresConfigFromEnv()

	var cfg exportConfig
	flag.StringVar(&cfg.PostgresDSN, "pg-dsn", defaultDSN, "Postgres DSN for session trajectory tables")
	flag.StringVar(&cfg.PostgresSchema, "pg-schema", defaultSchema, "Postgres schema for session trajectory tables")
	flag.StringVar(&cfg.ExportRoot, "export-root", filepath.Join("session-data", "session-exports"), "Directory used for exported trajectory files")
	flag.StringVar(&cfg.ManifestDir, "manifest-dir", filepath.Join("session-data", "session-export-manifests"), "Directory used for export manifest files")
	flag.StringVar(&cfg.SessionIDFile, "session-id-file", "", "Optional file containing one session id per line to export")
	flag.StringVar(&cfg.WriteSessionIDFile, "write-session-id-file", "", "Optional file path used to persist the resolved session id snapshot")
	flag.StringVar(&cfg.UserID, "user-id", "", "Optional exact user_id filter")
	flag.StringVar(&cfg.Source, "source", "", "Optional exact source filter")
	flag.StringVar(&cfg.CallType, "call-type", "", "Optional exact call_type filter")
	flag.StringVar(&cfg.Status, "status", "", "Optional exact session status filter")
	flag.StringVar(&cfg.Provider, "provider", "", "Optional exact provider filter")
	flag.StringVar(&cfg.CanonicalModelFamily, "canonical-model-family", "", "Optional exact canonical_model_family filter")
	flag.StringVar(&cfg.StartTimeRaw, "start-time", "", "Optional inclusive start time in RFC3339 format")
	flag.StringVar(&cfg.EndTimeRaw, "end-time", "", "Optional inclusive end time in RFC3339 format")
	flag.IntVar(&cfg.PageSize, "page-size", 100, "Number of sessions fetched per page (1-500)")
	flag.DurationVar(&cfg.ConnectTimeout, "connect-timeout", 30*time.Second, "Database connection timeout")
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "Only count and page matching sessions without exporting files")
	flag.BoolVar(&cfg.SkipExisting, "skip-existing", false, "Reuse already complete export directories instead of rewriting them")
	flag.Parse()
	return cfg
}

func run(cfg exportConfig) error {
	cfg.PostgresDSN = strings.TrimSpace(cfg.PostgresDSN)
	cfg.PostgresSchema = strings.TrimSpace(cfg.PostgresSchema)
	cfg.ExportRoot = strings.TrimSpace(cfg.ExportRoot)
	cfg.ManifestDir = strings.TrimSpace(cfg.ManifestDir)
	cfg.SessionIDFile = strings.TrimSpace(cfg.SessionIDFile)
	cfg.WriteSessionIDFile = strings.TrimSpace(cfg.WriteSessionIDFile)
	cfg.UserID = strings.TrimSpace(cfg.UserID)
	cfg.Source = strings.TrimSpace(cfg.Source)
	cfg.CallType = strings.TrimSpace(cfg.CallType)
	cfg.Status = strings.TrimSpace(cfg.Status)
	cfg.Provider = strings.TrimSpace(cfg.Provider)
	cfg.CanonicalModelFamily = strings.TrimSpace(cfg.CanonicalModelFamily)
	cfg.StartTimeRaw = strings.TrimSpace(cfg.StartTimeRaw)
	cfg.EndTimeRaw = strings.TrimSpace(cfg.EndTimeRaw)
	if cfg.PostgresDSN == "" {
		return fmt.Errorf("--pg-dsn is required")
	}
	if cfg.PageSize <= 0 {
		cfg.PageSize = 100
	}
	if cfg.PageSize > 500 {
		cfg.PageSize = 500
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 30 * time.Second
	}

	startTime, err := parseRFC3339("start-time", cfg.StartTimeRaw)
	if err != nil {
		return err
	}
	endTime, err := parseRFC3339("end-time", cfg.EndTimeRaw)
	if err != nil {
		return err
	}
	if startTime != nil && endTime != nil && startTime.After(*endTime) {
		return fmt.Errorf("--start-time must be earlier than or equal to --end-time")
	}

	connectCtx, cancel := context.WithTimeout(context.Background(), cfg.ConnectTimeout)
	defer cancel()

	store, err := sessiontrajectory.NewPostgresStore(connectCtx, sessiontrajectory.PostgresStoreConfig{
		DSN:    cfg.PostgresDSN,
		Schema: cfg.PostgresSchema,
	})
	if err != nil {
		return err
	}
	defer func() {
		_ = store.Close()
	}()

	db, err := sql.Open("pgx", cfg.PostgresDSN)
	if err != nil {
		return fmt.Errorf("open export query database: %w", err)
	}
	defer func() {
		_ = db.Close()
	}()
	if err := db.PingContext(connectCtx); err != nil {
		return fmt.Errorf("ping export query database: %w", err)
	}

	filters := exportFilters{
		UserID:               cfg.UserID,
		Source:               cfg.Source,
		CallType:             cfg.CallType,
		Status:               cfg.Status,
		Provider:             cfg.Provider,
		CanonicalModelFamily: cfg.CanonicalModelFamily,
		PageSize:             cfg.PageSize,
		DryRun:               cfg.DryRun,
	}
	if startTime != nil {
		filters.StartTime = startTime
	}
	if endTime != nil {
		filters.EndTime = endTime
	}

	sessionIDs, err := resolveSessionIDs(context.Background(), db, cfg, filters)
	if err != nil {
		return err
	}
	if cfg.WriteSessionIDFile != "" && cfg.SessionIDFile == "" {
		if err := writeSessionIDsFile(cfg.WriteSessionIDFile, sessionIDs); err != nil {
			return err
		}
		log.Printf("wrote %d session ids to %s", len(sessionIDs), resolvePath(cfg.WriteSessionIDFile))
	}

	totalSessions := int64(len(sessionIDs))
	log.Printf("matched %d sessions for export", totalSessions)

	results := make([]sessiontrajectory.SessionExportResult, 0)
	exportedFiles := 0
	exportedSessions := 0
	tokenTotals := sessiontrajectory.ExportTokenTotals{}
	for _, sessionID := range sessionIDs {
		exportedSessions++
		if cfg.DryRun {
			log.Printf("[dry-run] %d/%d session=%s", exportedSessions, totalSessions, sessionID)
			continue
		}

		var result sessiontrajectory.SessionExportResult
		if cfg.SkipExisting {
			existing, complete, inspectErr := store.InspectExportSession(context.Background(), sessionID, cfg.ExportRoot)
			if inspectErr != nil {
				return fmt.Errorf("inspect existing export for session %s: %w", sessionID, inspectErr)
			}
			if complete {
				result = existing
				log.Printf("reused %d/%d sessions (%d files) session=%s dir=%s", exportedSessions, totalSessions, exportedFiles+result.FileCount, result.SessionID, result.ExportDir)
			} else {
				exported, exportErr := store.ExportSession(context.Background(), sessionID, cfg.ExportRoot)
				if exportErr != nil {
					return fmt.Errorf("export session %s: %w", sessionID, exportErr)
				}
				result = exported
				log.Printf("exported %d/%d sessions (%d files) session=%s dir=%s", exportedSessions, totalSessions, exportedFiles+result.FileCount, result.SessionID, result.ExportDir)
			}
		} else {
			exported, exportErr := store.ExportSession(context.Background(), sessionID, cfg.ExportRoot)
			if exportErr != nil {
				return fmt.Errorf("export session %s: %w", sessionID, exportErr)
			}
			result = exported
			log.Printf("exported %d/%d sessions (%d files) session=%s dir=%s", exportedSessions, totalSessions, exportedFiles+result.FileCount, result.SessionID, result.ExportDir)
		}

		results = append(results, result)
		exportedFiles += result.FileCount
		tokenTotals.InputTokens += result.TokenTotals.InputTokens
		tokenTotals.OutputTokens += result.TokenTotals.OutputTokens
		tokenTotals.ReasoningTokens += result.TokenTotals.ReasoningTokens
		tokenTotals.CachedTokens += result.TokenTotals.CachedTokens
		tokenTotals.TotalTokens += result.TokenTotals.TotalTokens
	}

	manifest := exportManifest{
		Version:          exportManifestVersion,
		ExportedAt:       time.Now().UTC(),
		ExportRoot:       resolvePath(cfg.ExportRoot),
		Filters:          filters,
		TotalSessions:    totalSessions,
		ExportedSessions: exportedSessions,
		ExportedFiles:    exportedFiles,
		TokenTotals:      tokenTotals,
		Items:            results,
	}
	manifestPath, err := writeManifest(cfg.ManifestDir, manifest)
	if err != nil {
		return err
	}
	manifest.ManifestPath = manifestPath
	if err := rewriteManifest(manifestPath, manifest); err != nil {
		return err
	}

	log.Printf("session trajectory export finished: sessions=%d files=%d manifest=%s", exportedSessions, exportedFiles, manifestPath)
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal export manifest: %w", err)
	}
	fmt.Println(string(raw))
	return nil
}

func parseRFC3339(name, raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("--%s must be RFC3339: %w", name, err)
	}
	utc := value.UTC()
	return &utc, nil
}

func countSessions(ctx context.Context, db *sql.DB, schema string, filters exportFilters) (int64, error) {
	var (
		args       []any
		conditions []string
	)
	appendCond := func(expr string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(expr, len(args)))
	}
	appendFilterConditions(filters, appendCond)
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s", tableName(schema, "session_trajectory_sessions"))
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	var count int64
	if err := db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count session trajectories: %w", err)
	}
	return count, nil
}

func fetchSessionIDsSnapshot(ctx context.Context, db *sql.DB, schema string, filters exportFilters) ([]string, error) {
	var (
		args       []any
		conditions []string
	)
	appendCond := func(expr string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(expr, len(args)))
	}
	appendFilterConditions(filters, appendCond)
	query := fmt.Sprintf(`
		SELECT id
		FROM %s
	`, tableName(schema, "session_trajectory_sessions"))
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY last_activity_at DESC, id DESC"

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query session trajectory snapshot: %w", err)
	}
	defer rows.Close()

	ids := make([]string, 0, 256)
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			return nil, fmt.Errorf("scan session trajectory snapshot id: %w", err)
		}
		ids = append(ids, sessionID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session trajectory snapshot rows: %w", err)
	}
	return ids, nil
}

func appendFilterConditions(filters exportFilters, appendCond func(string, any)) {
	if filters.UserID != "" {
		appendCond("user_id = $%d", filters.UserID)
	}
	if filters.Source != "" {
		appendCond("source = $%d", filters.Source)
	}
	if filters.CallType != "" {
		appendCond("call_type = $%d", filters.CallType)
	}
	if filters.Status != "" {
		appendCond("status = $%d", filters.Status)
	}
	if filters.Provider != "" {
		appendCond("provider = $%d", filters.Provider)
	}
	if filters.CanonicalModelFamily != "" {
		appendCond("canonical_model_family = $%d", filters.CanonicalModelFamily)
	}
	if filters.StartTime != nil {
		appendCond("last_activity_at >= $%d", filters.StartTime.UTC())
	}
	if filters.EndTime != nil {
		appendCond("last_activity_at <= $%d", filters.EndTime.UTC())
	}
}

func resolveSessionIDs(ctx context.Context, db *sql.DB, cfg exportConfig, filters exportFilters) ([]string, error) {
	if cfg.SessionIDFile != "" {
		return loadSessionIDsFile(cfg.SessionIDFile)
	}
	return fetchSessionIDsSnapshot(ctx, db, cfg.PostgresSchema, filters)
}

func writeManifest(manifestDir string, manifest exportManifest) (string, error) {
	dir := resolvePath(manifestDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create manifest dir: %w", err)
	}
	filename := fmt.Sprintf("session-trajectory-export-%s.json", manifest.ExportedAt.UTC().Format("20060102T150405Z"))
	path := filepath.Join(dir, filename)
	if err := rewriteManifest(path, manifest); err != nil {
		return "", err
	}
	return path, nil
}

func loadSessionIDsFile(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read session id file: %w", err)
	}
	lines := strings.Split(string(raw), "\n")
	ids := make([]string, 0, len(lines))
	seen := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		sessionID := strings.TrimSpace(line)
		if sessionID == "" {
			continue
		}
		if _, ok := seen[sessionID]; ok {
			continue
		}
		seen[sessionID] = struct{}{}
		ids = append(ids, sessionID)
	}
	return ids, nil
}

func writeSessionIDsFile(path string, ids []string) error {
	resolved := resolvePath(path)
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return fmt.Errorf("create session id file dir: %w", err)
	}
	payload := strings.Join(ids, "\n")
	if payload != "" {
		payload += "\n"
	}
	if err := os.WriteFile(resolved, []byte(payload), 0o644); err != nil {
		return fmt.Errorf("write session id file: %w", err)
	}
	return nil
}

func rewriteManifest(path string, manifest exportManifest) error {
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

func resolvePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "."
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

func tableName(schema, name string) string {
	schema = strings.TrimSpace(schema)
	if schema == "" {
		return quoteIdentifier(name)
	}
	return quoteIdentifier(schema) + "." + quoteIdentifier(name)
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
