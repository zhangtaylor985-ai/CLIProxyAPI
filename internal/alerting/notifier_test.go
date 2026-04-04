package alerting

import (
	"context"
	"net/http"
	"os"
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

	runtimeCfg := newRuntimeConfig(cfg)
	if runtimeCfg.token != "env-token" {
		t.Fatalf("expected env token fallback, got %q", runtimeCfg.token)
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

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
