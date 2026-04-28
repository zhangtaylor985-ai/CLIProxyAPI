// Package claude provides HTTP handlers for Claude API code-related functionality.
// This package implements Claude-compatible streaming chat completions with sophisticated
// client rotation and quota management systems to ensure high availability and optimal
// resource utilization across multiple backend clients. It handles request translation
// between Claude API format and the underlying Gemini backend, providing seamless
// API compatibility while maintaining robust error handling and connection management.
package claude

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	. "github.com/router-for-me/CLIProxyAPI/v6/internal/constant"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

// ClaudeCodeAPIHandler contains the handlers for Claude API endpoints.
// It holds a pool of clients to interact with the backend service.
type ClaudeCodeAPIHandler struct {
	*handlers.BaseAPIHandler
}

// NewClaudeCodeAPIHandler creates a new Claude API handlers instance.
// It takes an BaseAPIHandler instance as input and returns a ClaudeCodeAPIHandler.
//
// Parameters:
//   - apiHandlers: The base API handler instance.
//
// Returns:
//   - *ClaudeCodeAPIHandler: A new Claude code API handler instance.
func NewClaudeCodeAPIHandler(apiHandlers *handlers.BaseAPIHandler) *ClaudeCodeAPIHandler {
	return &ClaudeCodeAPIHandler{
		BaseAPIHandler: apiHandlers,
	}
}

// HandlerType returns the identifier for this handler implementation.
func (h *ClaudeCodeAPIHandler) HandlerType() string {
	return Claude
}

// Models returns a list of models supported by this handler.
func (h *ClaudeCodeAPIHandler) Models() []map[string]any {
	// Get dynamic models from the global registry
	modelRegistry := registry.GetGlobalRegistry()
	return modelRegistry.GetAvailableModels("claude")
}

// ClaudeMessages handles Claude-compatible streaming chat completions.
// This function implements a sophisticated client rotation and quota management system
// to ensure high availability and optimal resource utilization across multiple backend clients.
//
// Parameters:
//   - c: The Gin context for the request.
func (h *ClaudeCodeAPIHandler) ClaudeMessages(c *gin.Context) {
	// Extract raw JSON data from the incoming request
	rawJSON, err := c.GetRawData()
	// If data retrieval fails, return a 400 Bad Request error.
	if err != nil {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: fmt.Sprintf("Invalid request: %v", err),
				Type:    "invalid_request_error",
			},
		})
		return
	}

	// Check if the client requested a streaming response.
	streamResult := gjson.GetBytes(rawJSON, "stream")
	if !streamResult.Exists() || streamResult.Type == gjson.False {
		h.handleNonStreamingResponse(c, rawJSON)
	} else {
		h.handleStreamingResponse(c, rawJSON)
	}
}

// ClaudeMessages handles Claude-compatible streaming chat completions.
// This function implements a sophisticated client rotation and quota management system
// to ensure high availability and optimal resource utilization across multiple backend clients.
//
// Parameters:
//   - c: The Gin context for the request.
func (h *ClaudeCodeAPIHandler) ClaudeCountTokens(c *gin.Context) {
	// Extract raw JSON data from the incoming request
	rawJSON, err := c.GetRawData()
	// If data retrieval fails, return a 400 Bad Request error.
	if err != nil {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: fmt.Sprintf("Invalid request: %v", err),
				Type:    "invalid_request_error",
			},
		})
		return
	}

	c.Header("Content-Type", "application/json")

	alt := h.GetAlt(c)
	cliCtx, cliCancel := h.GetContextWithCancel(h, c, context.Background())

	modelName := gjson.GetBytes(rawJSON, "model").String()

	resp, upstreamHeaders, errMsg := h.ExecuteCountWithAuthManager(cliCtx, h.HandlerType(), modelName, rawJSON, alt)
	if errMsg != nil {
		h.writeClientError(c, errMsg)
		cliCancel(errMsg.Error)
		return
	}
	handlers.WriteUpstreamHeaders(c.Writer.Header(), upstreamHeaders)
	_, _ = c.Writer.Write(resp)
	cliCancel()
}

// ClaudeModels handles the Claude models listing endpoint.
// It returns a JSON response containing available Claude models and their specifications.
//
// Parameters:
//   - c: The Gin context for the request.
func (h *ClaudeCodeAPIHandler) ClaudeModels(c *gin.Context) {
	models := h.Models()
	firstID := ""
	lastID := ""
	if len(models) > 0 {
		if id, ok := models[0]["id"].(string); ok {
			firstID = id
		}
		if id, ok := models[len(models)-1]["id"].(string); ok {
			lastID = id
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"data":     models,
		"has_more": false,
		"first_id": firstID,
		"last_id":  lastID,
	})
}

// handleNonStreamingResponse handles non-streaming content generation requests for Claude models.
// This function processes the request synchronously and returns the complete generated
// response in a single API call. It supports various generation parameters and
// response formats.
//
// Parameters:
//   - c: The Gin context for the request
//   - modelName: The name of the Gemini model to use for content generation
//   - rawJSON: The raw JSON request body containing generation parameters and content
func (h *ClaudeCodeAPIHandler) handleNonStreamingResponse(c *gin.Context, rawJSON []byte) {
	c.Header("Content-Type", "application/json")
	alt := h.GetAlt(c)
	cliCtx, cliCancel := h.GetContextWithCancel(h, c, context.Background())

	modelName := gjson.GetBytes(rawJSON, "model").String()

	// Claude Code treats any non-streaming body bytes as the final JSON response,
	// so body keep-alives would commit HTTP 200 before an upstream error can be returned.
	resp, upstreamHeaders, errMsg := h.ExecuteWithAuthManager(cliCtx, h.HandlerType(), modelName, rawJSON, alt)
	if errMsg != nil {
		h.writeClientError(c, errMsg)
		cliCancel(errMsg.Error)
		return
	}

	// Decompress gzipped responses - Claude API sometimes returns gzip without Content-Encoding header
	// This fixes title generation and other non-streaming responses that arrive compressed
	if len(resp) >= 2 && resp[0] == 0x1f && resp[1] == 0x8b {
		gzReader, errGzip := gzip.NewReader(bytes.NewReader(resp))
		if errGzip != nil {
			log.Warnf("failed to decompress gzipped Claude response: %v", errGzip)
		} else {
			defer func() {
				if errClose := gzReader.Close(); errClose != nil {
					log.Warnf("failed to close Claude gzip reader: %v", errClose)
				}
			}()
			decompressed, errRead := io.ReadAll(gzReader)
			if errRead != nil {
				log.Warnf("failed to read decompressed Claude response: %v", errRead)
			} else {
				resp = decompressed
			}
		}
	}

	if len(bytes.TrimSpace(resp)) == 0 {
		errEmpty := errors.New("empty upstream response")
		h.writeClientError(c, &interfaces.ErrorMessage{StatusCode: http.StatusBadGateway, Error: errEmpty})
		cliCancel(errEmpty)
		return
	}

	handlers.WriteUpstreamHeaders(c.Writer.Header(), upstreamHeaders)
	_, _ = c.Writer.Write(resp)
	cliCancel()
}

// handleStreamingResponse streams Claude-compatible responses backed by Gemini.
// It sets up SSE, selects a backend client with rotation/quota logic,
// forwards chunks, and translates them to Claude CLI format.
//
// Parameters:
//   - c: The Gin context for the request.
//   - rawJSON: The raw JSON request body.
func (h *ClaudeCodeAPIHandler) handleStreamingResponse(c *gin.Context, rawJSON []byte) {
	// Get the http.Flusher interface to manually flush the response.
	// This is crucial for streaming as it allows immediate sending of data chunks
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: "Streaming not supported",
				Type:    "server_error",
			},
		})
		return
	}

	modelName := gjson.GetBytes(rawJSON, "model").String()

	// Create a cancellable context for the backend client request
	// This allows proper cleanup and cancellation of ongoing requests
	cliCtx, cliCancel := h.GetContextWithCancel(h, c, context.Background())

	dataChan, upstreamHeaders, errChan := h.ExecuteStreamWithAuthManager(cliCtx, h.HandlerType(), modelName, rawJSON, "")
	setSSEHeaders := func() {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("Access-Control-Allow-Origin", "*")
	}

	// Peek at the first chunk to determine success or failure before setting headers
	for {
		select {
		case <-c.Request.Context().Done():
			cliCancel(c.Request.Context().Err())
			return
		case errMsg, ok := <-errChan:
			if !ok {
				// Err channel closed cleanly; wait for data channel.
				errChan = nil
				continue
			}
			// Upstream failed immediately. Return proper error status and JSON.
			h.writeClientError(c, errMsg)
			if errMsg != nil {
				cliCancel(errMsg.Error)
			} else {
				cliCancel(nil)
			}
			return
		case chunk, ok := <-dataChan:
			if !ok {
				h.writeClientError(c, &interfaces.ErrorMessage{
					StatusCode: http.StatusBadGateway,
					Error:      errors.New("upstream stream closed before first payload"),
				})
				cliCancel(errors.New("upstream stream closed before first payload"))
				return
			}

			// Success! Set headers now.
			setSSEHeaders()
			handlers.WriteUpstreamHeaders(c.Writer.Header(), upstreamHeaders)

			// Write the first chunk
			if len(chunk) > 0 {
				_, _ = c.Writer.Write(chunk)
				flusher.Flush()
			}

			// Continue streaming the rest
			h.forwardClaudeStream(c, flusher, func(err error) { cliCancel(err) }, dataChan, errChan)
			return
		}
	}
}

func (h *ClaudeCodeAPIHandler) forwardClaudeStream(c *gin.Context, flusher http.Flusher, cancel func(error), data <-chan []byte, errs <-chan *interfaces.ErrorMessage) {
	h.ForwardStream(c, flusher, cancel, data, errs, handlers.StreamForwardOptions{
		KeepAliveInterval: func() *time.Duration {
			interval := 5 * time.Second
			return &interval
		}(),
		WriteChunk: func(chunk []byte) {
			if len(chunk) == 0 {
				return
			}
			_, _ = c.Writer.Write(chunk)
		},
		WriteTerminalError: func(errMsg *interfaces.ErrorMessage) {
			if errMsg == nil {
				return
			}
			errMsg = h.sanitizeClientError(c, errMsg)
			handlers.AppendAPIResponseError(c, errMsg)
			status := http.StatusInternalServerError
			if errMsg.StatusCode > 0 {
				status = errMsg.StatusCode
			}
			if !c.Writer.Written() {
				c.Status(status)
			}

			errorBytes, _ := json.Marshal(h.toClaudeError(errMsg))
			_, _ = fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", errorBytes)
		},
		WriteKeepAlive: func() {
			_, _ = c.Writer.Write([]byte("event: ping\ndata: {\"type\":\"ping\"}\n\n"))
		},
	})
}

func (h *ClaudeCodeAPIHandler) writeClientError(c *gin.Context, msg *interfaces.ErrorMessage) {
	sanitized := h.sanitizeClientError(c, msg)
	h.WriteErrorResponseBody(c, sanitized, handlers.BuildClaudeErrorResponseBodyFromMessage(sanitized))
}

func (h *ClaudeCodeAPIHandler) sanitizeClientError(c *gin.Context, msg *interfaces.ErrorMessage) *interfaces.ErrorMessage {
	if !shouldSuppressClientError(c, msg) {
		return msg
	}

	entry := log.WithFields(log.Fields{
		"component":   "claude_error_sanitize",
		"status_code": msg.StatusCode,
	})
	if hasCodexFailoverMarker(c) {
		entry = entry.WithField("provider", "codex")
	}
	if msg != nil && msg.Error != nil {
		entry = entry.WithError(msg.Error)
	}
	entry.Error("suppressing raw upstream error for Claude client")

	sanitized := *msg
	sanitized.StatusCode = http.StatusServiceUnavailable
	sanitized.Error = errors.New(handlers.GenericSensitiveClientErrorMessage)
	return &sanitized
}

func shouldSuppressClientError(c *gin.Context, msg *interfaces.ErrorMessage) bool {
	if hasCodexFailoverMarker(c) {
		return true
	}
	return shouldSuppressSensitiveUpstreamError(msg)
}

func hasCodexFailoverMarker(c *gin.Context) bool {
	if c == nil {
		return false
	}
	value, exists := c.Get("cpa_failover_provider")
	if !exists {
		return false
	}
	provider, ok := value.(string)
	if !ok {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(provider), "codex")
}

func shouldSuppressSensitiveUpstreamError(msg *interfaces.ErrorMessage) bool {
	if msg == nil || msg.Error == nil {
		return false
	}

	raw := strings.TrimSpace(msg.Error.Error())
	status := msg.StatusCode
	if status <= 0 {
		status = http.StatusInternalServerError
	}
	embedded := extractEmbeddedJSON(raw)
	envelopeMsg, envelopeType, envelopeCode := extractErrorEnvelopeFields(embedded)
	combined := strings.ToLower(strings.TrimSpace(strings.Join([]string{
		raw,
		envelopeMsg,
		envelopeType,
		envelopeCode,
	}, " ")))
	if _, ok := handlers.SanitizeClientErrorText(status, combined); ok {
		return true
	}

	switch {
	case status >= http.StatusInternalServerError:
		return containsAny(combined,
			"service unavailable",
			"temporarily unavailable",
			"upstream",
			"gateway",
			"timeout",
			"unknown provider for model",
			"cf-ray",
			"request id",
			"request_id",
			"open1.codes",
			"api.openai.com",
			"api.anthropic.com",
		)
	case status == http.StatusTooManyRequests:
		return containsAny(combined,
			"usage_limit_reached",
			"model_cooldown",
			"rate_limit",
			"too many requests",
			"cooling down",
			"reset_seconds",
			"resets_in_seconds",
		)
	case status == http.StatusBadRequest || status == http.StatusUnauthorized || status == http.StatusForbidden:
		return containsAny(combined,
			"organization has been disabled",
			"organization disabled",
			"organization has been suspended",
			"account disabled",
			"account suspended",
			"account banned",
			"account blocked",
			"credential",
			"oauth",
			"session",
			"login",
			"api key",
			"invalid_api_key",
			"permission_error",
			"authentication_error",
		)
	default:
		return false
	}
}

func extractEmbeddedJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if json.Valid([]byte(raw)) {
		return raw
	}
	start := strings.IndexByte(raw, '{')
	if start < 0 {
		return ""
	}
	candidate := strings.TrimSpace(raw[start:])
	if !json.Valid([]byte(candidate)) {
		return ""
	}
	return candidate
}

func extractErrorEnvelopeFields(raw string) (message string, errType string, code string) {
	if strings.TrimSpace(raw) == "" {
		return "", "", ""
	}

	var payload struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return "", "", ""
	}

	message = strings.TrimSpace(payload.Error.Message)
	if message == "" {
		message = strings.TrimSpace(payload.Message)
	}
	errType = strings.TrimSpace(payload.Error.Type)
	if errType == "" {
		errType = strings.TrimSpace(payload.Type)
	}
	code = strings.TrimSpace(payload.Error.Code)
	if code == "" {
		code = strings.TrimSpace(payload.Code)
	}
	return message, errType, code
}

func containsAny(haystack string, needles ...string) bool {
	haystack = strings.ToLower(strings.TrimSpace(haystack))
	if haystack == "" {
		return false
	}
	for _, needle := range needles {
		needle = strings.ToLower(strings.TrimSpace(needle))
		if needle != "" && strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}

type claudeErrorDetail struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type claudeErrorResponse struct {
	Type  string            `json:"type"`
	Error claudeErrorDetail `json:"error"`
}

func (h *ClaudeCodeAPIHandler) toClaudeError(msg *interfaces.ErrorMessage) claudeErrorResponse {
	return claudeErrorResponse{
		Type: "error",
		Error: claudeErrorDetail{
			Type:    "api_error",
			Message: msg.Error.Error(),
		},
	}
}
