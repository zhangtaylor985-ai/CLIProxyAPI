// Package config provides configuration management for the CLI Proxy API server.
// It handles loading and parsing YAML configuration files, and provides structured
// access to application settings including server port, authentication directory,
// debug settings, proxy configuration, and API keys.
package config

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

	// DisableClaudeOpus1M strips Claude Opus 1M capability from client API key requests by default.
	// When true, the proxy removes the custom 1M header and beta flag unless the API key policy
	// explicitly enables Opus 1M for that key.
	DisableClaudeOpus1M bool `yaml:"disable-claude-opus-1m" json:"disable-claude-opus-1m"`

	// ForceModelPrefix requires explicit model prefixes (e.g., "teamA/gemini-3-pro-preview")
	// to target prefixed credentials. When false, unprefixed model requests may use prefixed
	// credentials as well.
	ForceModelPrefix bool `yaml:"force-model-prefix" json:"force-model-prefix"`

	// RequestLog enables or disables detailed request logging functionality.
	RequestLog bool `yaml:"request-log" json:"request-log"`

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
