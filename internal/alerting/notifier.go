package alerting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	log "github.com/sirupsen/logrus"
)

var (
	defaultProviderBackoff = []time.Duration{
		5 * time.Minute,
		15 * time.Minute,
		30 * time.Minute,
		60 * time.Minute,
	}
	defaultErrorBackoff = []time.Duration{
		1 * time.Minute,
		5 * time.Minute,
		15 * time.Minute,
		60 * time.Minute,
		6 * time.Hour,
	}
	defaultErrorExcludePatterns = []string{
		"broken pipe",
		"context canceled",
		"context deadline exceeded",
		"use of closed network connection",
		"client disconnected",
	}
	normalizeUUIDRegexp   = regexp.MustCompile(`\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	normalizeHexRegexp    = regexp.MustCompile(`\b[0-9a-f]{12,}\b`)
	normalizeNumberRegexp = regexp.MustCompile(`\b\d+\b`)
	globalNotifier        = newNotifier()
)

type runtimeConfig struct {
	enabled             bool
	token               string
	providerChatID      string
	errorChatID         string
	opsChatID           string
	sendRecovery        bool
	recoveryMinCooldown time.Duration
	requestTimeout      time.Duration
	providerBackoff     []time.Duration
	errorBackoff        []time.Duration
	errorMinLevel       log.Level
	errorExcludes       []string
	httpClient          *http.Client
}

type suppressionState struct {
	nextAllowed     time.Time
	backoffIndex    int
	suppressedCount int
}

// ProviderEvent describes a provider-side timeout, degradation, or failover event.
type ProviderEvent struct {
	Kind                   string
	Provider               string
	AuthID                 string
	AuthIndex              string
	AuthLabel              string
	MaskedAPIKey           string
	BaseURL                string
	Model                  string
	RequestID              string
	ErrorCode              string
	ErrorMessage           string
	HTTPStatus             int
	Cooldown               time.Duration
	CooldownUntil          time.Time
	RetryAfter             time.Duration
	FirstActivityLatency   time.Duration
	CompletedLatency       time.Duration
	SwitchedToProvider     string
	SwitchedToAuthID       string
	SwitchedToAuthIndex    string
	SwitchedToMaskedAPIKey string
	SwitchedToBaseURL      string
}

type ManagementEvent struct {
	Action            string
	Username          string
	Role              string
	AuthSource        string
	APIKey            string
	APIKeyName        string
	GroupID           string
	GroupName         string
	CustomGroup       bool
	DailyBudgetUSD    float64
	WeeklyBudgetUSD   float64
	TokenPackageUSD   float64
	TokenPackageStart string
}

type notifier struct {
	mu            sync.Mutex
	cfg           runtimeConfig
	providerState map[string]*suppressionState
	errorState    map[string]*suppressionState
	sendTelegram  func(context.Context, *http.Client, string, string, string) error
}

func newNotifier() *notifier {
	return &notifier{
		providerState: make(map[string]*suppressionState),
		errorState:    make(map[string]*suppressionState),
		sendTelegram:  sendTelegramMessage,
	}
}

// ConfigureFromConfig updates the global notifier using the latest config snapshot.
func ConfigureFromConfig(cfg *internalconfig.Config) {
	globalNotifier.configure(cfg)
}

// NotifyProviderEvent emits a provider alert routed to the provider chat when enabled.
func NotifyProviderEvent(event ProviderEvent) {
	globalNotifier.notifyProvider(event)
}

func NotifyManagementEvent(event ManagementEvent) {
	globalNotifier.notifyManagement(event)
}

func (n *notifier) configure(cfg *internalconfig.Config) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cfg = newRuntimeConfig(cfg)
}

func newRuntimeConfig(cfg *internalconfig.Config) runtimeConfig {
	out := runtimeConfig{
		providerBackoff:     append([]time.Duration(nil), defaultProviderBackoff...),
		errorBackoff:        append([]time.Duration(nil), defaultErrorBackoff...),
		errorMinLevel:       log.ErrorLevel,
		errorExcludes:       append([]string(nil), defaultErrorExcludePatterns...),
		recoveryMinCooldown: 5 * time.Minute,
		requestTimeout:      10 * time.Second,
	}
	if cfg == nil {
		return out
	}

	tg := cfg.Notifications.Telegram
	out.enabled = tg.Enabled
	out.token = resolveTelegramBotToken(strings.TrimSpace(tg.BotToken))
	out.providerChatID = firstNonEmpty(strings.TrimSpace(tg.ProviderChatID), lookupEnvTrimmed("TELEGRAM_PROVIDER_CHAT_ID", "TG_PROVIDER_CHAT_ID"))
	out.errorChatID = firstNonEmpty(strings.TrimSpace(tg.ErrorLogChatID), lookupEnvTrimmed("TELEGRAM_ERROR_LOG_CHAT_ID", "TG_ERROR_LOG_CHAT_ID"))
	out.opsChatID = firstNonEmpty(strings.TrimSpace(tg.OpsChatID), lookupEnvTrimmed("TELEGRAM_OPS_CHAT_ID", "TG_OPS_CHAT_ID"))
	out.sendRecovery = tg.SendRecovery
	if tg.RecoveryMinCooldownSeconds > 0 {
		out.recoveryMinCooldown = time.Duration(tg.RecoveryMinCooldownSeconds) * time.Second
	}
	if tg.RequestTimeoutSeconds > 0 {
		out.requestTimeout = time.Duration(tg.RequestTimeoutSeconds) * time.Second
	}
	if parsed := parseBackoffDurations(tg.Provider.Backoff, defaultProviderBackoff); len(parsed) > 0 {
		out.providerBackoff = parsed
	}
	if parsed := parseBackoffDurations(tg.ErrorLog.Backoff, defaultErrorBackoff); len(parsed) > 0 {
		out.errorBackoff = parsed
	}
	if level, err := log.ParseLevel(strings.TrimSpace(tg.ErrorLog.MinLevel)); err == nil {
		out.errorMinLevel = level
	}
	if len(tg.ErrorLog.ExcludePatterns) > 0 {
		out.errorExcludes = append(out.errorExcludes, lowerTrimmedSlice(tg.ErrorLog.ExcludePatterns)...)
	}
	client := &http.Client{Timeout: out.requestTimeout}
	out.httpClient = util.SetProxy(&cfg.SDKConfig, client)
	return out
}

func lookupEnvTrimmed(keys ...string) string {
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func resolveTelegramBotToken(configToken string) string {
	if trimmed := strings.TrimSpace(configToken); trimmed != "" {
		return trimmed
	}
	for _, key := range []string{"TELEGRAM_BOT_TOKEN", "TG_BOT_TOKEN"} {
		if value, ok := os.LookupEnv(key); ok {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func parseBackoffDurations(values []string, fallback []time.Duration) []time.Duration {
	if len(values) == 0 {
		return append([]time.Duration(nil), fallback...)
	}
	out := make([]time.Duration, 0, len(values))
	for _, raw := range values {
		parsed, err := time.ParseDuration(strings.TrimSpace(raw))
		if err != nil || parsed <= 0 {
			continue
		}
		out = append(out, parsed)
	}
	if len(out) == 0 {
		return append([]time.Duration(nil), fallback...)
	}
	return out
}

func lowerTrimmedSlice(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.ToLower(strings.TrimSpace(value)); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func (n *notifier) notifyProvider(event ProviderEvent) {
	event.Kind = strings.ToLower(strings.TrimSpace(event.Kind))
	if event.Kind == "" {
		event.Kind = "provider_event"
	}
	if event.Kind == "recovered" {
		n.mu.Lock()
		sendRecovery := n.cfg.sendRecovery
		minCooldown := n.cfg.recoveryMinCooldown
		n.mu.Unlock()
		if !sendRecovery {
			return
		}
		if event.Cooldown > 0 && event.Cooldown < minCooldown {
			return
		}
	}

	n.mu.Lock()
	cfg := n.cfg
	if !cfg.enabled || cfg.token == "" || cfg.providerChatID == "" {
		n.mu.Unlock()
		return
	}
	fingerprint := providerFingerprint(event)
	suppressed, send := allowNotification(n.providerState, fingerprint, cfg.providerBackoff, time.Now())
	n.mu.Unlock()
	if !send {
		return
	}

	text := formatProviderEvent(event, suppressed)
	go n.dispatch(cfg, cfg.providerChatID, text)
}

func (n *notifier) notifyErrorEntry(entry *log.Entry) {
	if entry == nil {
		return
	}
	message := normalizeLogMessage(entry)

	n.mu.Lock()
	cfg := n.cfg
	if !cfg.enabled || cfg.token == "" || cfg.errorChatID == "" {
		n.mu.Unlock()
		return
	}
	if entry.Level > cfg.errorMinLevel {
		n.mu.Unlock()
		return
	}
	if shouldExcludeErrorMessage(message, cfg.errorExcludes) {
		n.mu.Unlock()
		return
	}
	if isNoisyRequestLogEntry(entry, message) {
		n.mu.Unlock()
		return
	}
	fingerprint := errorFingerprint(entry, message)
	suppressed, send := allowNotification(n.errorState, fingerprint, cfg.errorBackoff, time.Now())
	n.mu.Unlock()
	if !send {
		return
	}

	text := formatErrorEntry(entry, message, suppressed)
	go n.dispatch(cfg, cfg.errorChatID, text)
}

func (n *notifier) notifyManagement(event ManagementEvent) {
	n.mu.Lock()
	cfg := n.cfg
	if !cfg.enabled || cfg.token == "" || cfg.opsChatID == "" {
		n.mu.Unlock()
		return
	}
	n.mu.Unlock()

	text := formatManagementEvent(event)
	go n.dispatch(cfg, cfg.opsChatID, text)
}

func allowNotification(states map[string]*suppressionState, fingerprint string, backoff []time.Duration, now time.Time) (int, bool) {
	if fingerprint == "" {
		fingerprint = "default"
	}
	state := states[fingerprint]
	if state == nil {
		state = &suppressionState{}
		states[fingerprint] = state
	}
	if !state.nextAllowed.IsZero() && now.Before(state.nextAllowed) {
		state.suppressedCount++
		return 0, false
	}
	suppressed := state.suppressedCount
	state.suppressedCount = 0
	if len(backoff) > 0 {
		index := state.backoffIndex
		if index < 0 {
			index = 0
		}
		if index >= len(backoff) {
			index = len(backoff) - 1
		}
		state.nextAllowed = now.Add(backoff[index])
		if state.backoffIndex < len(backoff)-1 {
			state.backoffIndex++
		}
	}
	return suppressed, true
}

func providerFingerprint(event ProviderEvent) string {
	parts := []string{
		strings.ToLower(strings.TrimSpace(event.Kind)),
		strings.ToLower(strings.TrimSpace(event.Provider)),
		strings.ToLower(strings.TrimSpace(event.AuthIndex)),
		strings.ToLower(strings.TrimSpace(event.Model)),
		strings.ToLower(strings.TrimSpace(event.ErrorCode)),
	}
	if parts[2] == "" {
		parts[2] = strings.ToLower(strings.TrimSpace(event.AuthID))
	}
	if parts[4] == "" {
		parts[4] = normalizeStringForFingerprint(event.ErrorMessage)
	}
	return strings.Join(parts, "|")
}

func errorFingerprint(entry *log.Entry, normalizedMessage string) string {
	location := ""
	if entry != nil && entry.Caller != nil {
		location = fmt.Sprintf("%s:%d", filepath.Base(entry.Caller.File), entry.Caller.Line)
	}
	return strings.Join([]string{
		strings.ToLower(entry.Level.String()),
		location,
		normalizeStringForFingerprint(normalizedMessage),
	}, "|")
}

func normalizeStringForFingerprint(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	value = normalizeUUIDRegexp.ReplaceAllString(value, "#")
	value = normalizeHexRegexp.ReplaceAllString(value, "#")
	value = normalizeNumberRegexp.ReplaceAllString(value, "#")
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 160 {
		value = value[:160]
	}
	return value
}

func normalizeLogMessage(entry *log.Entry) string {
	if entry == nil {
		return ""
	}
	message := strings.TrimSpace(entry.Message)
	if errValue, ok := entry.Data["error"]; ok && errValue != nil {
		errText := strings.TrimSpace(fmt.Sprint(errValue))
		if errText != "" && !strings.Contains(message, errText) {
			message = strings.TrimSpace(message + " | error=" + errText)
		}
	}
	if errValue, ok := entry.Data["api_error"]; ok && errValue != nil {
		errText := strings.TrimSpace(fmt.Sprint(errValue))
		if errText != "" && !strings.Contains(message, errText) {
			message = strings.TrimSpace(message + " | api_error=" + errText)
		}
	}
	return strings.Join(strings.Fields(message), " ")
}

func shouldExcludeErrorMessage(message string, patterns []string) bool {
	lower := strings.ToLower(strings.TrimSpace(message))
	if lower == "" {
		return false
	}
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

func isNoisyRequestLogEntry(entry *log.Entry, message string) bool {
	if entry == nil || entry.Caller == nil {
		return false
	}
	if filepath.Base(entry.Caller.File) != "gin_logger.go" {
		return false
	}
	if _, ok := entry.Data["api_error"]; ok {
		return false
	}
	trimmed := strings.TrimSpace(message)
	if !strings.Contains(trimmed, "|") {
		return false
	}
	fields := strings.Split(trimmed, "|")
	if len(fields) < 4 {
		return false
	}
	status := strings.TrimSpace(fields[0])
	return status == "500" || status == "502" || status == "503" || status == "504"
}

func formatProviderEvent(event ProviderEvent, suppressed int) string {
	var builder strings.Builder
	builder.WriteString("Provider alert\n")
	builder.WriteString("Kind: " + strings.TrimSpace(event.Kind) + "\n")
	if provider := strings.TrimSpace(event.Provider); provider != "" {
		builder.WriteString("Provider: " + provider + "\n")
	}
	if authRef := formatAuthRef(event.AuthIndex, event.AuthID, event.AuthLabel); authRef != "" {
		builder.WriteString("Auth: " + authRef + "\n")
	}
	if apiKey := strings.TrimSpace(event.MaskedAPIKey); apiKey != "" {
		builder.WriteString("Key: " + apiKey + "\n")
	}
	if baseURL := strings.TrimSpace(event.BaseURL); baseURL != "" {
		builder.WriteString("Base URL: " + trimForTelegram(baseURL, 240) + "\n")
	}
	if model := strings.TrimSpace(event.Model); model != "" {
		builder.WriteString("Model: " + model + "\n")
	}
	if code := strings.TrimSpace(event.ErrorCode); code != "" {
		builder.WriteString("Reason: " + code + "\n")
	} else if message := strings.TrimSpace(event.ErrorMessage); message != "" {
		builder.WriteString("Reason: " + trimForTelegram(message, 240) + "\n")
	}
	if event.HTTPStatus > 0 {
		builder.WriteString(fmt.Sprintf("HTTP: %d\n", event.HTTPStatus))
	}
	if event.FirstActivityLatency > 0 {
		builder.WriteString("First activity: " + event.FirstActivityLatency.String() + "\n")
	}
	if event.CompletedLatency > 0 {
		builder.WriteString("Completed: " + event.CompletedLatency.String() + "\n")
	}
	if event.Cooldown > 0 {
		builder.WriteString("Cooldown: " + event.Cooldown.String() + "\n")
	}
	if !event.CooldownUntil.IsZero() {
		builder.WriteString("Cooldown until: " + event.CooldownUntil.UTC().Format(time.RFC3339) + "\n")
	}
	if event.RetryAfter > 0 {
		builder.WriteString("Retry after: " + event.RetryAfter.String() + "\n")
	}
	if to := formatAuthRef(event.SwitchedToAuthIndex, event.SwitchedToAuthID, ""); to != "" || strings.TrimSpace(event.SwitchedToProvider) != "" {
		target := strings.TrimSpace(event.SwitchedToProvider)
		if target == "" {
			target = "other_provider"
		}
		if to != "" {
			target = target + " (" + to + ")"
		}
		builder.WriteString("Switched to: " + target + "\n")
		if apiKey := strings.TrimSpace(event.SwitchedToMaskedAPIKey); apiKey != "" {
			builder.WriteString("Switched key: " + apiKey + "\n")
		}
		if baseURL := strings.TrimSpace(event.SwitchedToBaseURL); baseURL != "" {
			builder.WriteString("Switched base URL: " + trimForTelegram(baseURL, 240) + "\n")
		}
	}
	if requestID := strings.TrimSpace(event.RequestID); requestID != "" {
		builder.WriteString("Request ID: " + requestID + "\n")
	}
	if suppressed > 0 {
		builder.WriteString(fmt.Sprintf("Suppressed similar alerts: %d\n", suppressed))
	}
	return strings.TrimRight(builder.String(), "\n")
}

func formatErrorEntry(entry *log.Entry, normalizedMessage string, suppressed int) string {
	var builder strings.Builder
	builder.WriteString("Error log alert\n")
	builder.WriteString("Level: " + strings.ToUpper(entry.Level.String()) + "\n")
	if entry.Caller != nil {
		builder.WriteString(fmt.Sprintf("Source: %s:%d\n", filepath.Base(entry.Caller.File), entry.Caller.Line))
	}
	if requestID, ok := entry.Data["request_id"].(string); ok && strings.TrimSpace(requestID) != "" {
		builder.WriteString("Request ID: " + strings.TrimSpace(requestID) + "\n")
	}
	if normalizedMessage != "" {
		builder.WriteString("Message: " + trimForTelegram(normalizedMessage, 600) + "\n")
	}
	if suppressed > 0 {
		builder.WriteString(fmt.Sprintf("Suppressed similar alerts: %d\n", suppressed))
	}
	return strings.TrimRight(builder.String(), "\n")
}

func formatManagementEvent(event ManagementEvent) string {
	var builder strings.Builder
	builder.WriteString("Management action\n")
	if action := strings.TrimSpace(event.Action); action != "" {
		builder.WriteString("Action: " + action + "\n")
	}
	if username := strings.TrimSpace(event.Username); username != "" {
		builder.WriteString("Username: " + username + "\n")
	}
	if role := strings.TrimSpace(event.Role); role != "" {
		builder.WriteString("Role: " + role + "\n")
	}
	if source := strings.TrimSpace(event.AuthSource); source != "" {
		builder.WriteString("Auth source: " + source + "\n")
	}
	if apiKey := strings.TrimSpace(event.APIKey); apiKey != "" {
		builder.WriteString("API Key: " + apiKey + "\n")
	}
	if apiKeyName := strings.TrimSpace(event.APIKeyName); apiKeyName != "" {
		builder.WriteString("Key name: " + trimForTelegram(apiKeyName, 120) + "\n")
	}
	groupLabel := strings.TrimSpace(event.GroupName)
	if groupLabel == "" {
		groupLabel = strings.TrimSpace(event.GroupID)
	}
	if groupLabel == "" {
		groupLabel = "unassigned"
	}
	if event.GroupID != "" && event.GroupName != "" && event.GroupID != event.GroupName {
		groupLabel = event.GroupName + " (" + event.GroupID + ")"
	}
	builder.WriteString("Account group: " + groupLabel + "\n")
	if event.CustomGroup {
		builder.WriteString(fmt.Sprintf("Custom group daily budget: $%.4f\n", event.DailyBudgetUSD))
		builder.WriteString(fmt.Sprintf("Custom group weekly budget: $%.4f\n", event.WeeklyBudgetUSD))
		builder.WriteString(fmt.Sprintf("Token package quota: $%.4f\n", event.TokenPackageUSD))
		if started := strings.TrimSpace(event.TokenPackageStart); started != "" {
			builder.WriteString("Token package start: " + started + "\n")
		}
	}
	return strings.TrimRight(builder.String(), "\n")
}

func formatAuthRef(index, id, label string) string {
	parts := make([]string, 0, 2)
	if trimmed := strings.TrimSpace(label); trimmed != "" {
		parts = append(parts, trimmed)
	}
	if trimmed := strings.TrimSpace(index); trimmed != "" {
		parts = append(parts, trimmed[:min(len(trimmed), 12)])
	} else if trimmed := strings.TrimSpace(id); trimmed != "" {
		if len(trimmed) > 12 {
			trimmed = trimmed[len(trimmed)-12:]
		}
		parts = append(parts, trimmed)
	}
	return strings.Join(parts, " / ")
}

func trimForTelegram(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}

func (n *notifier) dispatch(cfg runtimeConfig, chatID, text string) {
	if chatID == "" || text == "" || n.sendTelegram == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.requestTimeout)
	defer cancel()
	_ = n.sendTelegram(ctx, cfg.httpClient, cfg.token, chatID, text)
}

func sendTelegramMessage(ctx context.Context, client *http.Client, token, chatID, text string) error {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	payload := map[string]any{
		"chat_id":                  chatID,
		"text":                     text,
		"disable_web_page_preview": true,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	targetURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("telegram send failed: status=%d", resp.StatusCode)
	}
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
