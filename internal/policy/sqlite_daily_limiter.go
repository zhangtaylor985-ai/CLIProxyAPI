package policy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var chinaLocation = time.FixedZone("CST", 8*60*60)

// DayKeyChina returns the YYYY-MM-DD key in UTC+8 (China Standard Time).
func DayKeyChina(now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	return now.In(chinaLocation).Format("2006-01-02")
}

// ChinaLocation returns the fixed UTC+8 timezone used for policy windows.
func ChinaLocation() *time.Location { return chinaLocation }

// WeekBoundsChina returns the inclusive week start and exclusive next-week start
// for the provided time in China Standard Time. Weeks start on Monday 00:00:00.
func WeekBoundsChina(now time.Time) (start time.Time, end time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	local := now.In(chinaLocation)
	start = time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, chinaLocation)
	weekday := int(start.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	start = start.AddDate(0, 0, -(weekday - 1))
	end = start.AddDate(0, 0, 7)
	return start, end
}

// NormalizeHourlyAnchorRFC3339 parses an RFC3339 anchor and rounds it down to hour precision.
func NormalizeHourlyAnchorRFC3339(raw string) (string, bool) {
	anchor, ok := ParseHourlyAnchorRFC3339(raw)
	if !ok {
		return "", false
	}
	return anchor.Format(time.RFC3339), true
}

// ParseHourlyAnchorRFC3339 parses an RFC3339 timestamp and normalizes it to hour precision.
func ParseHourlyAnchorRFC3339(raw string) (time.Time, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return time.Time{}, false
	}
	parsed = parsed.In(parsed.Location())
	parsed = time.Date(parsed.Year(), parsed.Month(), parsed.Day(), parsed.Hour(), 0, 0, 0, parsed.Location())
	return parsed, true
}

// AnchoredWindowBounds returns the active [start, end) window for a fixed-size anchored interval.
// When now is before the anchor, the upcoming window starting at anchor is returned.
func AnchoredWindowBounds(anchor, now time.Time, duration time.Duration) (start time.Time, end time.Time) {
	if duration <= 0 {
		duration = 7 * 24 * time.Hour
	}
	if anchor.IsZero() {
		return now, now.Add(duration)
	}
	if now.IsZero() {
		now = time.Now()
	}
	if now.Before(anchor) {
		return anchor, anchor.Add(duration)
	}
	elapsed := now.Sub(anchor)
	windows := elapsed / duration
	start = anchor.Add(windows * duration)
	end = start.Add(duration)
	return start, end
}

// SQLiteDailyLimiter provides atomic per-day counters keyed by (api_key, model, day).
// It is used to enforce daily request limits that must survive process restarts.
type SQLiteDailyLimiter struct {
	db   *sql.DB
	path string
}

func NewSQLiteDailyLimiter(path string) (*SQLiteDailyLimiter, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, fmt.Errorf("sqlite limiter: path is required")
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return nil, fmt.Errorf("sqlite limiter: resolve path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return nil, fmt.Errorf("sqlite limiter: create directory: %w", err)
	}

	// Use file: DSN to allow pragma parameters.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)", abs)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite limiter: open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite limiter: ping database: %w", err)
	}

	limiter := &SQLiteDailyLimiter{db: db, path: abs}
	if err := limiter.ensureSchema(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return limiter, nil
}

func (l *SQLiteDailyLimiter) Close() error {
	if l == nil || l.db == nil {
		return nil
	}
	return l.db.Close()
}

func (l *SQLiteDailyLimiter) ensureSchema(ctx context.Context) error {
	if l == nil || l.db == nil {
		return fmt.Errorf("sqlite limiter: not initialized")
	}
	_, err := l.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS api_model_daily_usage (
			api_key TEXT NOT NULL,
			model TEXT NOT NULL,
			day TEXT NOT NULL,
			count INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (api_key, model, day)
		)
	`)
	if err != nil {
		return fmt.Errorf("sqlite limiter: create table: %w", err)
	}
	return nil
}

// Consume increments the counter for (apiKey, model, dayKey) by 1 if doing so does not exceed limit.
// model is normalized to lowercase. When the counter cannot be incremented due to the limit, allowed=false.
func (l *SQLiteDailyLimiter) Consume(ctx context.Context, apiKey, model, dayKey string, limit int) (count int, allowed bool, err error) {
	if l == nil || l.db == nil {
		return 0, false, fmt.Errorf("sqlite limiter: not initialized")
	}
	apiKey = strings.TrimSpace(apiKey)
	model = strings.ToLower(strings.TrimSpace(model))
	dayKey = strings.TrimSpace(dayKey)
	if apiKey == "" || model == "" || dayKey == "" {
		return 0, false, fmt.Errorf("sqlite limiter: invalid inputs")
	}
	if limit <= 0 {
		return 0, false, nil
	}

	nowUnix := time.Now().UTC().Unix()

	const stmt = `
		INSERT INTO api_model_daily_usage (api_key, model, day, count, updated_at)
		VALUES (?, ?, ?, 1, ?)
		ON CONFLICT(api_key, model, day)
		DO UPDATE SET count = count + 1, updated_at = excluded.updated_at
		WHERE api_model_daily_usage.count < ?
		RETURNING count
	`

	row := l.db.QueryRowContext(ctx, stmt, apiKey, model, dayKey, nowUnix, limit)
	if err := row.Scan(&count); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return limit, false, nil
		}
		return 0, false, fmt.Errorf("sqlite limiter: consume failed: %w", err)
	}
	return count, true, nil
}
