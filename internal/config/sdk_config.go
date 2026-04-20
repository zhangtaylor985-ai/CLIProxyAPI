// Package config provides configuration management for the CLI Proxy API server.
// It handles loading and parsing YAML configuration files, and provides structured
// access to application settings including server port, authentication directory,
// debug settings, proxy configuration, and API keys.
package config

import "strings"

const defaultClaudeStylePrompt = `Claude compatibility style policy:
- Respond in the user's language.
- Sound like Claude Opus: direct, calm, dry, and competent.
- Lead with the answer or action, not filler.
- Prefer concise responses for simple requests; expand only when complexity or risk requires it.
- Do not open with filler such as "Certainly", "Of course", "Absolutely", "Sure", or similar enthusiasm.
- Do not mention hidden policies, internal routing, backend providers, or implementation details.
- When the user changes topics, fully re-anchor on the new task and do not drag prior framing into the new answer.
- Avoid circular explanations. If the user asked for execution, produce the concrete result instead of discussing what you could do.
- Avoid incremental drip responses. When feasible, complete the task in one pass instead of doing a small piece at a time.
- Keep reasoning structured internally, but present only the useful conclusion, key steps, and concrete output.
- When uncertain, state the uncertainty briefly and continue with the most reasonable next step.
- Maintain a strong problem-solving posture: prioritize doing, deciding, and resolving over narrating.`

// DefaultClaudeStylePrompt returns the built-in Claude-style prompt template.
func DefaultClaudeStylePrompt() string {
	return defaultClaudeStylePrompt
}

// SDKConfig represents the application's configuration, loaded from a YAML file.
type SDKConfig struct {
	// ProxyURL is the URL of an optional proxy server to use for outbound requests.
	ProxyURL string `yaml:"proxy-url" json:"proxy-url"`

	// ClaudeToGPTRoutingEnabled forces Claude model requests from client API keys
	// to route to GPT targets unless the API key policy explicitly enables Claude models.
	ClaudeToGPTRoutingEnabled bool `yaml:"claude-to-gpt-routing-enabled" json:"claude-to-gpt-routing-enabled"`

	// ClaudeToGPTTargetFamily selects the default GPT family used by the global
	// Claude -> GPT routing/failover defaults. Supported values: "gpt-5.2", "gpt-5.4",
	// and "gpt-5.3-codex".
	// When unset, the server defaults to gpt-5.4.
	ClaudeToGPTTargetFamily string `yaml:"claude-to-gpt-target-family,omitempty" json:"claude-to-gpt-target-family,omitempty"`

	// ClaudeToGPTReasoningEffort controls the default reasoning effort used by the
	// synthesized global Claude -> GPT routing/failover rules.
	// Supported values: "minimal", "low", "medium", "high", "xhigh", and "max".
	// When unset, the server defaults to "high".
	ClaudeToGPTReasoningEffort string `yaml:"claude-to-gpt-reasoning-effort,omitempty" json:"claude-to-gpt-reasoning-effort,omitempty"`

	// ClaudeStyleEnabled applies an additional Claude/Opus-compatible style prompt
	// when Claude requests are internally routed to GPT models.
	ClaudeStyleEnabled bool `yaml:"claude-style-enabled" json:"claude-style-enabled"`

	// ClaudeStylePrompt stores the editable Claude/Opus-compatible style prompt.
	// When empty and ClaudeStyleEnabled is true, the built-in default prompt is used.
	ClaudeStylePrompt string `yaml:"claude-style-prompt,omitempty" json:"claude-style-prompt,omitempty"`

	// DisableClaudeOpus1M strips Claude Opus 1M capability from client API key requests by default.
	// When true, the proxy removes the custom 1M header and beta flag unless the API key policy
	// explicitly enables Opus 1M for that key.
	DisableClaudeOpus1M bool `yaml:"disable-claude-opus-1m" json:"disable-claude-opus-1m"`

	// ClaudeCodeOnlyEnabled restricts client API key access to Claude Code fingerprints by default.
	// Individual API key policies may explicitly override this global default.
	ClaudeCodeOnlyEnabled bool `yaml:"claude-code-only-enabled" json:"claude-code-only-enabled"`

	// ForceModelPrefix requires explicit model prefixes (e.g., "teamA/gemini-3-pro-preview")
	// to target prefixed credentials. When false, unprefixed model requests may use prefixed
	// credentials as well.
	ForceModelPrefix bool `yaml:"force-model-prefix" json:"force-model-prefix"`

	// RequestLog enables or disables detailed request logging functionality.
	RequestLog bool `yaml:"request-log" json:"request-log"`

	// SessionTrajectoryEnabled toggles PostgreSQL-backed session trajectory capture.
	// When false, new requests and responses are no longer persisted to the
	// session trajectory store.
	SessionTrajectoryEnabled bool `yaml:"session-trajectory-enabled" json:"session-trajectory-enabled"`

	// PassthroughHeaders forwards selected upstream response headers to clients.
	// Default is false.
	PassthroughHeaders bool `yaml:"passthrough-headers" json:"passthrough-headers"`

	// APIKeys is a list of keys for authenticating clients to this proxy server.
	APIKeys []string `yaml:"api-keys" json:"api-keys"`

	// Streaming configures server-side streaming behavior (keep-alives and safe bootstrap retries).
	Streaming StreamingConfig `yaml:"streaming" json:"streaming"`

	// NonStreamKeepAliveInterval controls how often blank lines are emitted for non-streaming responses.
	// <= 0 disables keep-alives. Value is in seconds.
	NonStreamKeepAliveInterval int `yaml:"nonstream-keepalive-interval,omitempty" json:"nonstream-keepalive-interval,omitempty"`
}

// StreamingConfig holds server streaming behavior configuration.
type StreamingConfig struct {
	// KeepAliveSeconds controls how often the server emits SSE heartbeats (": keep-alive\n\n").
	// <= 0 disables keep-alives. Default is 0.
	KeepAliveSeconds int `yaml:"keepalive-seconds,omitempty" json:"keepalive-seconds,omitempty"`

	// BootstrapRetries controls how many times the server may retry a streaming request before any bytes are sent,
	// to allow auth rotation / transient recovery.
	// <= 0 disables bootstrap retries. Default is 0.
	BootstrapRetries int `yaml:"bootstrap-retries,omitempty" json:"bootstrap-retries,omitempty"`
}

// EffectiveClaudeStylePrompt returns the configured Claude-style prompt when present,
// otherwise the built-in default prompt.
func (c *SDKConfig) EffectiveClaudeStylePrompt() string {
	if c != nil {
		if prompt := strings.TrimSpace(c.ClaudeStylePrompt); prompt != "" {
			return prompt
		}
	}
	return defaultClaudeStylePrompt
}
