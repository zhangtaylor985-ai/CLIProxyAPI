package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/logging"
)

const (
	codexRawSSELogDirEnv      = "CLIPROXY_CODEX_RAW_SSE_LOG_DIR"
	codexRawSSEMaxBytesEnv    = "CLIPROXY_CODEX_RAW_SSE_MAX_BYTES"
	defaultCodexRawSSEMaxSize = 50 << 20
)

type codexRawSSELogger struct {
	file      *os.File
	path      string
	maxBytes  int64
	written   int64
	truncated bool
}

func newCodexRawSSELogger(ctx context.Context, model string) *codexRawSSELogger {
	dir := strings.TrimSpace(os.Getenv(codexRawSSELogDirEnv))
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		logWithRequestID(ctx).WithError(err).Warn("codex raw sse diagnostics: create log dir failed")
		return nil
	}

	requestID := safeDiagnosticName(logging.GetRequestID(ctx))
	if requestID == "" {
		requestID = "no-request-id"
	}
	pattern := fmt.Sprintf("codex-raw-sse-%s-*.log", requestID)
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		logWithRequestID(ctx).WithError(err).Warn("codex raw sse diagnostics: create log file failed")
		return nil
	}

	logger := &codexRawSSELogger{
		file:     file,
		path:     file.Name(),
		maxBytes: codexRawSSEMaxBytes(),
	}
	logger.writeString("# codex raw upstream SSE diagnostics\n")
	logger.writeString("# timestamp: " + time.Now().UTC().Format(time.RFC3339Nano) + "\n")
	logger.writeString("# request_id: " + requestID + "\n")
	logger.writeString("# model: " + strings.TrimSpace(model) + "\n")
	logger.writeString("# note: response body only; request headers and auth credentials are not written\n\n")
	logWithRequestID(ctx).WithField("path", logger.path).Debug("codex raw sse diagnostics enabled")
	return logger
}

func codexRawSSEMaxBytes() int64 {
	value := strings.TrimSpace(os.Getenv(codexRawSSEMaxBytesEnv))
	if value == "" {
		return defaultCodexRawSSEMaxSize
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return defaultCodexRawSSEMaxSize
	}
	return parsed
}

func (l *codexRawSSELogger) WriteStatus(status int) {
	if l == nil || l.file == nil {
		return
	}
	l.writeString("# upstream_status: " + strconv.Itoa(status) + "\n\n")
}

func (l *codexRawSSELogger) WriteLine(line []byte) {
	if l == nil || l.file == nil {
		return
	}
	l.writeBytes(line)
	l.writeString("\n")
}

func (l *codexRawSSELogger) WriteScannerError(err error) {
	if l == nil || l.file == nil || err == nil {
		return
	}
	l.writeString("\n# scanner_error: " + err.Error() + "\n")
}

func (l *codexRawSSELogger) WriteEOF(sawCompleted bool, terminalEvent string, incompleteReason string) {
	if l == nil || l.file == nil {
		return
	}
	l.writeString("\n# eof: true\n")
	l.writeString("# saw_completion_event: " + strconv.FormatBool(sawCompleted) + "\n")
	l.writeString("# saw_terminal_event: " + strconv.FormatBool(strings.TrimSpace(terminalEvent) != "") + "\n")
	if terminalEvent = strings.TrimSpace(terminalEvent); terminalEvent != "" {
		l.writeString("# terminal_event: " + terminalEvent + "\n")
	}
	if incompleteReason = strings.TrimSpace(incompleteReason); incompleteReason != "" {
		l.writeString("# incomplete_reason: " + incompleteReason + "\n")
	}
}

func (l *codexRawSSELogger) Close() {
	if l == nil || l.file == nil {
		return
	}
	if err := l.file.Close(); err != nil {
		logWithRequestID(context.Background()).WithError(err).WithField("path", l.path).Warn("codex raw sse diagnostics: close log failed")
	}
	l.file = nil
}

func (l *codexRawSSELogger) writeString(value string) {
	l.writeBytes([]byte(value))
}

func (l *codexRawSSELogger) writeBytes(data []byte) {
	if l == nil || l.file == nil || len(data) == 0 || l.truncated {
		return
	}
	if l.maxBytes > 0 && l.written+int64(len(data)) > l.maxBytes {
		remaining := l.maxBytes - l.written
		if remaining > 0 {
			_, _ = l.file.Write(data[:remaining])
			l.written += remaining
		}
		_, _ = l.file.Write([]byte("\n# raw_sse_log_truncated: true\n"))
		l.truncated = true
		return
	}
	n, _ := l.file.Write(data)
	l.written += int64(n)
}

func safeDiagnosticName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return filepath.Base(b.String())
}
