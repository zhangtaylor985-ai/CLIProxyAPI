# Claude Relay Probe Detection

This project normally allows Claude-format requests to be routed through the configured
Claude-to-GPT policy. Some external relay verification sites send Claude-Code-shaped probe
traffic to determine whether a relay is backed by a native Claude provider. When this probe is
detected, the request is pinned to a Claude provider marked with `probe-target: true`.

## Configuration

Mark exactly one Claude API provider as the preferred probe target:

```yaml
claude-api-key:
  - api-key: sk-...
    base-url: https://boomai.cloud
    probe-target: true
    proxy-url: ""
    models: []
```

Normal traffic does not automatically use this provider. The provider is selected only after the
request matches the relay probe fingerprint.

## Detection Fingerprint

The detector is intentionally narrow. A request must first match all of these shell conditions:

- The request path is `/v1/messages`.
- The active handler type is `claude`.
- `User-Agent` starts with `claude-cli/`.
- `Anthropic-Beta` contains `interleaved-thinking-2025-05-14`.
- `metadata.user_id` uses relayAPI/hvoy's string session shape:
  `user_<hex>_account__session_<uuid>`.
- The request is not a real Claude Code request. Real Claude Code is excluded when it has
  `X-Claude-Code-Session-Id`, JSON-shaped `metadata.user_id`, Claude Agent SDK system text,
  `CWD`/`Date` system metadata, or system-reminder blocks.
- `stream` is `true`.
- `tools` is absent or an empty array.
- The system prompt shell is either the legacy single official Claude Code prompt block, or the
  newer hvoy shell with a synthetic `x-anthropic-billing-header` block followed by the official
  Claude Code prompt.

After the shell matches, the detector accepts the probe body when it has one of these thinking
shapes:

- `thinking.type=enabled` with a positive `budget_tokens` up to `65536`.
- `thinking.type=adaptive`.
- No `thinking` block, but `output_config.format.type=json_schema` is present. This covers hvoy's
  calculation probe.

The detector then labels known fixed relayAPI prompts as `relayapi_stage1`,
`relayapi_stage2`, or `relayapi_detector`. Other one-turn or three-turn requests with the same
relay shell are labeled `relayapi_web_like`, which covers hvoy's newer randomized knowledge,
document, signature, and calculation probes.

## Runtime Behavior

When a probe is detected and a `probe-target` Claude provider exists, the handler sets pinned-auth
metadata and forces the provider list to Claude for that request. This bypasses the global
Claude-to-GPT routing policy only for the probe request.

Expected log line:

```text
relay probe detected; pinning request to configured Claude probe target
```

If no `probe-target` provider exists, the request is not pinned and a warning is logged.

## Performance

The detector performs a small number of header string checks and targeted JSON path lookups using
`gjson`. Results are cached in the request context, so repeated checks during the same request do
not re-parse the probe. The performance impact is negligible compared with upstream model latency.
