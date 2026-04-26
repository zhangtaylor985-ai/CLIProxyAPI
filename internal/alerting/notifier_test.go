package alerting

import (
	"context"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	log "github.com/sirupsen/logrus"
)

func TestNewRuntimeConfigResolvesEnvToken(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "env-token")
	cfg := &internalconfig.Config{}
	cfg.Notifications.Telegram.Enabled = true
	cfg.Notifications.Telegram.ProviderChatID = "provider-chat"
	cfg.Notifications.Telegram.ErrorLogChatID = "error-chat"
	cfg.Notifications.Telegram.OpsChatID = "ops-chat"

	runtimeCfg := newRuntimeConfig(cfg)
	if runtimeCfg.token != "env-token" {
		t.Fatalf("expected env token fallback, got %q", runtimeCfg.token)
	}
}

func TestNewRuntimeConfigResolvesEnvChatRoutes(t *testing.T) {
	t.Setenv("TELEGRAM_PROVIDER_CHAT_ID", "provider-env")
	t.Setenv("TELEGRAM_ERROR_LOG_CHAT_ID", "error-env")
	t.Setenv("TELEGRAM_OPS_CHAT_ID", "ops-env")

	runtimeCfg := newRuntimeConfig(&internalconfig.Config{})
	if runtimeCfg.providerChatID != "provider-env" {
		t.Fatalf("provider chat = %q", runtimeCfg.providerChatID)
	}
	if runtimeCfg.errorChatID != "error-env" {
		t.Fatalf("error chat = %q", runtimeCfg.errorChatID)
	}
	if runtimeCfg.opsChatID != "ops-env" {
		t.Fatalf("ops chat = %q", runtimeCfg.opsChatID)
	}
}

func TestAllowNotificationBackoffSuppressesDuplicates(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	states := make(map[string]*suppressionState)
	backoff := []time.Duration{time.Minute, 5 * time.Minute}

	suppressed, send := allowNotification(states, "provider|timeout", backoff, now)
	if !send || suppressed != 0 {
		t.Fatalf("first alert should send immediately, send=%v suppressed=%d", send, suppressed)
	}

	suppressed, send = allowNotification(states, "provider|timeout", backoff, now.Add(30*time.Second))
	if send || suppressed != 0 {
		t.Fatalf("second alert inside backoff should be suppressed, send=%v suppressed=%d", send, suppressed)
	}

	suppressed, send = allowNotification(states, "provider|timeout", backoff, now.Add(61*time.Second))
	if !send || suppressed != 1 {
		t.Fatalf("alert after backoff should send with suppressed count, send=%v suppressed=%d", send, suppressed)
	}
}

func TestNotifyErrorEntryExcludesNoisyMessages(t *testing.T) {
	sent := make(chan string, 2)
	n := newNotifier()
	n.sendTelegram = func(_ context.Context, _ *http.Client, _, _, text string) error {
		sent <- text
		return nil
	}
	n.cfg = runtimeConfig{
		enabled:       true,
		token:         "token",
		errorChatID:   "chat-id",
		errorBackoff:  []time.Duration{time.Millisecond},
		errorMinLevel: log.ErrorLevel,
		errorExcludes: []string{"broken pipe"},
		httpClient:    &http.Client{},
	}

	noisyEntry := log.NewEntry(log.New())
	noisyEntry.Level = log.ErrorLevel
	noisyEntry.Message = "write tcp 127.0.0.1: broken pipe"
	n.notifyErrorEntry(noisyEntry)

	select {
	case msg := <-sent:
		t.Fatalf("expected noisy error to be ignored, got alert %q", msg)
	case <-time.After(100 * time.Millisecond):
	}

	usefulEntry := log.NewEntry(log.New())
	usefulEntry.Level = log.ErrorLevel
	usefulEntry.Message = "upstream stream bootstrap timeout"
	n.notifyErrorEntry(usefulEntry)

	select {
	case <-sent:
	case <-time.After(time.Second):
		t.Fatal("expected actionable error log alert to be sent")
	}
}

func TestNotifyErrorEntrySuppressesEmptyGinServerErrorLine(t *testing.T) {
	sent := make(chan string, 1)
	n := newNotifier()
	n.sendTelegram = func(_ context.Context, _ *http.Client, _, _, text string) error {
		sent <- text
		return nil
	}
	n.cfg = runtimeConfig{
		enabled:       true,
		token:         "token",
		errorChatID:   "chat-id",
		errorBackoff:  []time.Duration{time.Millisecond},
		errorMinLevel: log.ErrorLevel,
		httpClient:    &http.Client{},
	}

	entry := log.NewEntry(log.New())
	entry.Level = log.ErrorLevel
	entry.Message = `500 | 220ms | 127.0.0.1 | POST "/v1/messages?beta=true"`
	entry.Caller = &runtime.Frame{File: "/app/internal/logging/gin_logger.go", Line: 89}

	n.notifyErrorEntry(entry)
	select {
	case msg := <-sent:
		t.Fatalf("expected empty gin request line to be ignored, got alert %q", msg)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestResolveTelegramBotTokenPrefersConfigToken(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "env-token")
	token := resolveTelegramBotToken("config-token")
	if token != "config-token" {
		t.Fatalf("expected config token to win, got %q", token)
	}
}

func TestNotifyProviderRecoveryRequiresSendRecovery(t *testing.T) {
	n := newNotifier()
	sent := make(chan string, 2)
	n.sendTelegram = func(_ context.Context, _ *http.Client, _, _, text string) error {
		sent <- text
		return nil
	}
	n.cfg = runtimeConfig{
		enabled:             true,
		token:               "token",
		providerChatID:      "chat-id",
		providerBackoff:     []time.Duration{time.Millisecond},
		recoveryMinCooldown: 5 * time.Minute,
		httpClient:          &http.Client{},
	}

	n.notifyProvider(ProviderEvent{Kind: "recovered", Provider: "codex", AuthID: "a1", Model: "gpt-5", Cooldown: 10 * time.Minute})
	select {
	case msg := <-sent:
		t.Fatalf("expected recovery alert to be suppressed when sendRecovery=false, got %q", msg)
	case <-time.After(100 * time.Millisecond):
	}

	n.cfg.sendRecovery = true
	n.notifyProvider(ProviderEvent{Kind: "recovered", Provider: "codex", AuthID: "a1", Model: "gpt-5", Cooldown: 10 * time.Minute})
	select {
	case <-sent:
	case <-time.After(time.Second):
		t.Fatal("expected recovery alert to be sent when sendRecovery=true")
	}
}

func TestNotifyProviderRecoveryHonorsMinCooldown(t *testing.T) {
	n := newNotifier()
	sent := make(chan string, 1)
	n.sendTelegram = func(_ context.Context, _ *http.Client, _, _, text string) error {
		sent <- text
		return nil
	}
	n.cfg = runtimeConfig{
		enabled:             true,
		token:               "token",
		providerChatID:      "chat-id",
		sendRecovery:        true,
		providerBackoff:     []time.Duration{time.Millisecond},
		recoveryMinCooldown: 5 * time.Minute,
		httpClient:          &http.Client{},
	}

	n.notifyProvider(ProviderEvent{Kind: "recovered", Provider: "codex", AuthID: "a1", Model: "gpt-5", Cooldown: 2 * time.Minute})
	select {
	case msg := <-sent:
		t.Fatalf("expected short-cooldown recovery to be suppressed, got %q", msg)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestFormatProviderEventIncludesProviderIdentityAndFailoverTarget(t *testing.T) {
	cooldownUntil := time.Date(2026, 4, 10, 9, 30, 0, 0, time.UTC)
	text := formatProviderEvent(ProviderEvent{
		Kind:                   "degraded",
		Provider:               "claude",
		AuthID:                 "auth-source",
		AuthIndex:              "claude:source",
		AuthLabel:              "Claude Pool A",
		MaskedAPIKey:           "sk-abcd****wxyz",
		BaseURL:                "https://cc.claudepool.com/",
		Model:                  "claude-sonnet-4-5",
		Cooldown:               5 * time.Minute,
		CooldownUntil:          cooldownUntil,
		SwitchedToProvider:     "claude",
		SwitchedToAuthID:       "auth-target",
		SwitchedToAuthIndex:    "claude:target",
		SwitchedToMaskedAPIKey: "sk-zzzz****9999",
		SwitchedToBaseURL:      "https://backup.claudepool.com/",
	}, 0)

	expected := []string{
		"Provider alert",
		"Kind: degraded",
		"Auth: Claude Pool A / claude:sourc",
		"Key: sk-abcd****wxyz",
		"Base URL: https://cc.claudepool.com/",
		"Cooldown: 5m0s",
		"Cooldown until: 2026-04-10T09:30:00Z",
		"Switched to: claude (claude:targe)",
		"Switched key: sk-zzzz****9999",
		"Switched base URL: https://backup.claudepool.com/",
	}
	for _, fragment := range expected {
		if !strings.Contains(text, fragment) {
			t.Fatalf("expected %q in %q", fragment, text)
		}
	}
}

func TestFormatManagementEventIncludesCustomGroupDetails(t *testing.T) {
	text := formatManagementEvent(ManagementEvent{
		Action:            "api_key_created",
		Username:          "user_01",
		Role:              "staff",
		AuthSource:        "session",
		APIKey:            "sk-test",
		APIKeyName:        "Key A",
		GroupID:           "team-alpha",
		GroupName:         "Team Alpha",
		CustomGroup:       true,
		DailyBudgetUSD:    12.5,
		WeeklyBudgetUSD:   44.25,
		TokenPackageUSD:   99.99,
		TokenPackageStart: "2026-04-10T08:00:00Z",
	})

	expected := []string{
		"Action: api_key_created",
		"Username: user_01",
		"Role: staff",
		"Auth source: session",
		"API Key: sk-test",
		"Key name: Key A",
		"Account group: Team Alpha (team-alpha)",
		"Custom group daily budget: $12.5000",
		"Custom group weekly budget: $44.2500",
		"Token package quota: $99.9900",
		"Token package start: 2026-04-10T08:00:00Z",
	}
	for _, fragment := range expected {
		if !strings.Contains(text, fragment) {
			t.Fatalf("expected %q in %q", fragment, text)
		}
	}
}

func TestNotifyManagementSendsTelegram(t *testing.T) {
	sent := make(chan string, 1)
	n := newNotifier()
	n.sendTelegram = func(_ context.Context, _ *http.Client, _, _, text string) error {
		sent <- text
		return nil
	}
	n.cfg = runtimeConfig{
		enabled:    true,
		token:      "token",
		opsChatID:  "chat-id",
		httpClient: &http.Client{},
	}

	n.notifyManagement(ManagementEvent{
		Action:   "api_key_deleted",
		Username: "user_02",
		APIKey:   "sk-delete",
	})

	select {
	case msg := <-sent:
		if !strings.Contains(msg, "Action: api_key_deleted") {
			t.Fatalf("unexpected management alert: %q", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("expected management alert to be sent")
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
