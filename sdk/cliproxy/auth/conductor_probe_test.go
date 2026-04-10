package auth

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

func TestBuildProviderHealthProbeRequestSupportsCustomOpenAIChatModeAndPath(t *testing.T) {
	auth := &Auth{
		Provider: "codex",
		Attributes: map[string]string{
			"api_key":    "key-1",
			"base_url":   "https://capi.quan2go.com/openai",
			"probe_mode": "openai_chat",
			"probe_path": "v1/chat/completions",
		},
	}

	body, requestURL, err := buildProviderHealthProbeRequest(auth, "gpt-4.1", providerHealthProbePlan{
		Kind:      probeKindHealth,
		Mode:      providerHealthProbeMode(auth),
		Path:      providerHealthProbePath(auth),
		Prompt:    "health probe",
		MaxTokens: 1,
	})
	if err != nil {
		t.Fatalf("buildProviderHealthProbeRequest returned error: %v", err)
	}
	if requestURL != "https://capi.quan2go.com/openai/v1/chat/completions" {
		t.Fatalf("requestURL = %q", requestURL)
	}
	if !strings.Contains(string(body), "\"messages\"") {
		t.Fatalf("expected OpenAI chat payload, got %s", string(body))
	}
}

func TestBuildProviderHealthProbeRequestUsesCodexStreamingFormat(t *testing.T) {
	auth := &Auth{
		Provider: "codex",
		Attributes: map[string]string{
			"api_key":    "key-1",
			"base_url":   "https://capi.quan2go.com/openai",
			"probe_mode": "codex_responses",
		},
	}

	body, requestURL, err := buildProviderHealthProbeRequest(auth, "gpt-5", providerHealthProbePlan{
		Kind:      probeKindCanary,
		Mode:      providerHealthProbeMode(auth),
		Path:      providerHealthProbePath(auth),
		Prompt:    "Explain retries briefly.",
		MaxTokens: 96,
	})
	if err != nil {
		t.Fatalf("buildProviderHealthProbeRequest returned error: %v", err)
	}
	if requestURL != "https://capi.quan2go.com/openai/responses" {
		t.Fatalf("requestURL = %q", requestURL)
	}
	if got := gjson.GetBytes(body, "stream").Bool(); !got {
		t.Fatalf("stream = %t, want true", got)
	}
	if got := gjson.GetBytes(body, "store").Bool(); got {
		t.Fatalf("store = %t, want false", got)
	}
	if got := gjson.GetBytes(body, "parallel_tool_calls").Bool(); !got {
		t.Fatalf("parallel_tool_calls = %t, want true", got)
	}
	if got := gjson.GetBytes(body, "input.0.type").String(); got != "message" {
		t.Fatalf("input.0.type = %q", got)
	}
	if got := gjson.GetBytes(body, "input.0.content.0.type").String(); got != "input_text" {
		t.Fatalf("input.0.content.0.type = %q", got)
	}
	if got := gjson.GetBytes(body, "input.0.content.0.text").String(); got != "Explain retries briefly." {
		t.Fatalf("input.0.content.0.text = %q", got)
	}
	if gjson.GetBytes(body, "max_output_tokens").Exists() {
		t.Fatalf("max_output_tokens should be omitted for codex probe, body=%s", string(body))
	}
}

func TestProviderHealthProbePlanForAuthUsesCanaryWhenDue(t *testing.T) {
	auth := &Auth{
		Provider: "claude",
		Attributes: map[string]string{
			"canary_prompt":           "Explain retries briefly.",
			"canary_interval_seconds": "300",
		},
		ModelStates: map[string]*ModelState{
			"claude-sonnet-4-5": {
				Health: ProviderHealthState{
					LastCanaryAt: time.Now().Add(-10 * time.Minute),
				},
			},
		},
	}

	plan := providerHealthProbePlanForAuth(auth, "claude-sonnet-4-5", time.Now())
	if plan.Kind != probeKindCanary {
		t.Fatalf("plan.Kind = %q, want %q", plan.Kind, probeKindCanary)
	}
	if plan.Prompt != "Explain retries briefly." {
		t.Fatalf("plan.Prompt = %q", plan.Prompt)
	}
	if plan.MaxTokens <= 1 {
		t.Fatalf("plan.MaxTokens = %d, want canary-sized output budget", plan.MaxTokens)
	}
}

func TestApplyProviderHealthProbeHeadersUsesModeSpecificDefaults(t *testing.T) {
	auth := &Auth{Provider: "codex"}
	plan := providerHealthProbePlan{Mode: "openai_chat"}
	reqBody, _ := json.Marshal(map[string]any{"ok": true})
	req, err := http.NewRequest("POST", "https://example.com", strings.NewReader(string(reqBody)))
	if err != nil {
		t.Fatalf("http.NewRequest returned error: %v", err)
	}
	applyProviderHealthProbeHeaders(req, auth, plan)
	if got := req.Header.Get("User-Agent"); got != "cli-proxy-health-probe" {
		t.Fatalf("User-Agent = %q", got)
	}
	if got := req.Header.Get("Version"); got != "" {
		t.Fatalf("Version header = %q, want empty for openai_chat", got)
	}
}

func TestApplyProviderHealthProbeHeadersUsesCodexStreamingAccept(t *testing.T) {
	auth := &Auth{Provider: "codex"}
	plan := providerHealthProbePlan{Mode: "codex_responses"}
	req, err := http.NewRequest("POST", "https://example.com", strings.NewReader(`{"ok":true}`))
	if err != nil {
		t.Fatalf("http.NewRequest returned error: %v", err)
	}
	applyProviderHealthProbeHeaders(req, auth, plan)
	if got := req.Header.Get("Accept"); got != "text/event-stream" {
		t.Fatalf("Accept = %q, want text/event-stream", got)
	}
}
