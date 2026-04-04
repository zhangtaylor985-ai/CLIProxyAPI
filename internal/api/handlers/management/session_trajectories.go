package management

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/sessiontrajectory"
)

type sessionTrajectoryExportPayload struct {
	Version    int                                     `json:"version"`
	ExportedAt time.Time                               `json:"exported_at"`
	Items      []sessiontrajectory.SessionExportResult `json:"items"`
}

func (h *Handler) sessionTrajectoryStoreAvailable(c *gin.Context) bool {
	if h == nil || h.sessionTrajectoryStore == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session trajectory store unavailable"})
		return false
	}
	return true
}

func (h *Handler) ListSessionTrajectories(c *gin.Context) {
	if !h.sessionTrajectoryStoreAvailable(c) {
		return
	}
	filter := sessiontrajectory.SessionListFilter{
		UserID:               strings.TrimSpace(c.Query("user_id")),
		Source:               strings.TrimSpace(c.Query("source")),
		CallType:             strings.TrimSpace(c.Query("call_type")),
		Status:               strings.TrimSpace(c.Query("status")),
		Provider:             strings.TrimSpace(c.Query("provider")),
		CanonicalModelFamily: strings.TrimSpace(c.Query("canonical_model_family")),
		Limit:                parsePositiveInt(c.DefaultQuery("limit", "50"), 50),
		Before:               parseRFC3339Time(c.Query("before")),
	}
	items, err := h.sessionTrajectoryStore.ListSessions(c.Request.Context(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *Handler) GetSessionTrajectory(c *gin.Context) {
	if !h.sessionTrajectoryStoreAvailable(c) {
		return
	}
	sessionID := strings.TrimSpace(c.Param("sessionId"))
	item, found, err := h.sessionTrajectoryStore.GetSession(c.Request.Context(), sessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"item": item})
}

func (h *Handler) ListSessionTrajectoryRequests(c *gin.Context) {
	if !h.sessionTrajectoryStoreAvailable(c) {
		return
	}
	items, err := h.sessionTrajectoryStore.ListSessionRequests(c.Request.Context(), sessiontrajectory.SessionRequestFilter{
		SessionID:         strings.TrimSpace(c.Param("sessionId")),
		Limit:             parsePositiveInt(c.DefaultQuery("limit", "100"), 100),
		AfterRequestIndex: int64(parsePositiveInt(c.Query("after_request_index"), 0)),
		IncludePayloads:   parseBoolQuery(c.Query("include_payloads")),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *Handler) GetSessionTrajectoryTokenRounds(c *gin.Context) {
	if !h.sessionTrajectoryStoreAvailable(c) {
		return
	}
	items, err := h.sessionTrajectoryStore.ListSessionTokenRounds(
		c.Request.Context(),
		strings.TrimSpace(c.Param("sessionId")),
		parsePositiveInt(c.DefaultQuery("limit", "100"), 100),
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	summary := gin.H{
		"round_count":      len(items),
		"input_tokens":     int64(0),
		"output_tokens":    int64(0),
		"reasoning_tokens": int64(0),
		"cached_tokens":    int64(0),
		"total_tokens":     int64(0),
	}
	for _, item := range items {
		summary["input_tokens"] = summary["input_tokens"].(int64) + item.InputTokens
		summary["output_tokens"] = summary["output_tokens"].(int64) + item.OutputTokens
		summary["reasoning_tokens"] = summary["reasoning_tokens"].(int64) + item.ReasoningTokens
		summary["cached_tokens"] = summary["cached_tokens"].(int64) + item.CachedTokens
		summary["total_tokens"] = summary["total_tokens"].(int64) + item.TotalTokens
	}
	c.JSON(http.StatusOK, gin.H{"summary": summary, "items": items})
}

func (h *Handler) ExportSessionTrajectory(c *gin.Context) {
	if !h.sessionTrajectoryStoreAvailable(c) {
		return
	}
	item, err := h.sessionTrajectoryStore.ExportSession(c.Request.Context(), strings.TrimSpace(c.Param("sessionId")), h.sessionExportRoot)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"item": item})
}

func (h *Handler) ExportSessionTrajectories(c *gin.Context) {
	if !h.sessionTrajectoryStoreAvailable(c) {
		return
	}
	items, err := h.sessionTrajectoryStore.ExportSessions(c.Request.Context(), sessiontrajectory.SessionExportFilter{
		UserID:   strings.TrimSpace(c.Query("user_id")),
		Source:   strings.TrimSpace(c.Query("source")),
		CallType: strings.TrimSpace(c.Query("call_type")),
		Status:   strings.TrimSpace(c.Query("status")),
		Limit:    parsePositiveInt(c.DefaultQuery("limit", "20"), 20),
		Before:   parseRFC3339Time(c.Query("before")),
	}, h.sessionExportRoot)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, sessionTrajectoryExportPayload{
		Version:    1,
		ExportedAt: time.Now().UTC(),
		Items:      items,
	})
}

func parseBoolQuery(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func parseRFC3339Time(raw string) time.Time {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}
