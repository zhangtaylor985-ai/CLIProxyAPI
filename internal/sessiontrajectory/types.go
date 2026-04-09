package sessiontrajectory

import (
	"context"
	"encoding/json"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
)

const (
	StatusSuccess = "success"
	StatusError   = "error"

	SessionStatusActive = "active"
	SessionStatusError  = "error"

	defaultActiveWindow      = 24 * time.Hour
	defaultStrongMatchWindow = 2 * time.Hour
	defaultAsyncQueueSize    = 512
	defaultRecentCandidates  = 20
)

// CompletedRequest captures a single HTTP request/response exchange after the
// handler has fully completed.
type CompletedRequest struct {
	RequestID            string
	RequestMethod        string
	RequestURL           string
	RequestHeaders       map[string][]string
	RequestBody          []byte
	ResponseStatusCode   int
	ResponseHeaders      map[string][]string
	ResponseBody         []byte
	APIRequestBody       []byte
	APIResponseBody      []byte
	APIResponseTimestamp time.Time
	RequestTimestamp     time.Time
	ResponseTimestamp    time.Time
	APIResponseErrors    []*interfaces.ErrorMessage
	IsStreaming          bool
}

// Recorder persists completed requests.
type Recorder interface {
	Record(context.Context, *CompletedRequest) error
	Close() error
}

// Store exposes query and export operations for management APIs.
type Store interface {
	Recorder
	ListSessions(context.Context, SessionListFilter) ([]SessionSummary, error)
	GetSession(context.Context, string) (SessionSummary, bool, error)
	ListSessionRequests(context.Context, SessionRequestFilter) ([]SessionRequest, error)
	ListSessionTokenRounds(context.Context, string, int) ([]SessionTokenRound, error)
	ExportSession(context.Context, string, string) (SessionExportResult, error)
	ExportSessions(context.Context, SessionExportFilter, string) ([]SessionExportResult, error)
}

type SessionListFilter struct {
	UserID               string
	Source               string
	CallType             string
	Status               string
	Provider             string
	CanonicalModelFamily string
	Limit                int
	Before               time.Time
}

type SessionRequestFilter struct {
	SessionID         string
	Limit             int
	AfterRequestIndex int64
	IncludePayloads   bool
}

type SessionExportFilter struct {
	UserID   string
	Source   string
	CallType string
	Status   string
	Limit    int
	Before   time.Time
}

type SessionSummary struct {
	SessionID            string          `json:"session_id"`
	UserID               string          `json:"user_id"`
	Source               string          `json:"source"`
	CallType             string          `json:"call_type"`
	Provider             string          `json:"provider"`
	CanonicalModelFamily string          `json:"canonical_model_family"`
	ProviderSessionID    string          `json:"provider_session_id,omitempty"`
	SessionName          string          `json:"session_name,omitempty"`
	MessageCount         int64           `json:"message_count"`
	RequestCount         int64           `json:"request_count"`
	StartedAt            time.Time       `json:"started_at"`
	LastActivityAt       time.Time       `json:"last_activity_at"`
	ClosedAt             *time.Time      `json:"closed_at,omitempty"`
	Status               string          `json:"status"`
	Metadata             json.RawMessage `json:"metadata,omitempty"`
}

type SessionRequest struct {
	ID                string          `json:"id"`
	RequestID         string          `json:"request_id"`
	SessionID         string          `json:"session_id"`
	UserID            string          `json:"user_id"`
	ProviderRequestID string          `json:"provider_request_id,omitempty"`
	UpstreamLogID     string          `json:"upstream_log_id,omitempty"`
	RequestIndex      int64           `json:"request_index"`
	Source            string          `json:"source"`
	CallType          string          `json:"call_type"`
	Provider          string          `json:"provider"`
	Model             string          `json:"model"`
	UserAgent         string          `json:"user_agent,omitempty"`
	Status            string          `json:"status"`
	StartedAt         time.Time       `json:"started_at"`
	EndedAt           *time.Time      `json:"ended_at,omitempty"`
	DurationMS        int64           `json:"duration_ms"`
	InputTokens       int64           `json:"input_tokens"`
	OutputTokens      int64           `json:"output_tokens"`
	ReasoningTokens   int64           `json:"reasoning_tokens"`
	CachedTokens      int64           `json:"cached_tokens"`
	TotalTokens       int64           `json:"total_tokens"`
	CostMicroUSD      int64           `json:"cost_micro_usd"`
	RequestJSON       json.RawMessage `json:"request_json,omitempty"`
	ResponseJSON      json.RawMessage `json:"response_json,omitempty"`
	NormalizedJSON    json.RawMessage `json:"normalized_json,omitempty"`
	ErrorJSON         json.RawMessage `json:"error_json,omitempty"`
}

type SessionTokenRound struct {
	RequestID       string     `json:"request_id"`
	RequestIndex    int64      `json:"request_index"`
	StartedAt       time.Time  `json:"started_at"`
	EndedAt         *time.Time `json:"ended_at,omitempty"`
	Model           string     `json:"model"`
	Status          string     `json:"status"`
	InputTokens     int64      `json:"input_tokens"`
	OutputTokens    int64      `json:"output_tokens"`
	ReasoningTokens int64      `json:"reasoning_tokens"`
	CachedTokens    int64      `json:"cached_tokens"`
	TotalTokens     int64      `json:"total_tokens"`
}

type ExportedFile struct {
	RequestID    string `json:"request_id"`
	RequestIndex int64  `json:"request_index"`
	ExportIndex  int64  `json:"export_index"`
	ExportPath   string `json:"export_path"`
}

type ExportTokenTotals struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens"`
	CachedTokens    int64 `json:"cached_tokens"`
	TotalTokens     int64 `json:"total_tokens"`
}

type SessionExportResult struct {
	SessionID   string            `json:"session_id"`
	UserID      string            `json:"user_id"`
	ExportDir   string            `json:"export_dir"`
	FileCount   int               `json:"file_count"`
	ExportedAt  time.Time         `json:"exported_at"`
	TokenTotals ExportTokenTotals `json:"token_totals"`
	Files       []ExportedFile    `json:"files"`
}
