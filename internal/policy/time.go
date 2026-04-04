package policy

import (
	"strings"
	"time"
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

// AnchoredWindowBoundsFloor returns the [start, end) window containing now for a fixed-size
// anchored interval, including timestamps before the anchor.
func AnchoredWindowBoundsFloor(anchor, now time.Time, duration time.Duration) (start time.Time, end time.Time) {
	if duration <= 0 {
		duration = 7 * 24 * time.Hour
	}
	if anchor.IsZero() {
		return now, now.Add(duration)
	}
	if now.IsZero() {
		now = time.Now()
	}

	elapsed := now.Sub(anchor)
	windows := elapsed / duration
	if elapsed < 0 && elapsed%duration != 0 {
		windows--
	}
	start = anchor.Add(windows * duration)
	end = start.Add(duration)
	return start, end
}
