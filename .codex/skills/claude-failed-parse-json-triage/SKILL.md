---
name: claude-failed-parse-json-triage
description: Use when investigating Claude CLI or Claude Code client reports like "API Error: Failed to parse JSON", especially for CLIProxyAPI production incidents where local JSONL transcripts must be correlated with server logs and the Session trajectory PostgreSQL database.
metadata:
  short-description: Triage Claude Failed to parse JSON incidents
---

# Claude Failed To Parse JSON Triage

Use this skill to quickly prove whether a Claude client `API Error: Failed to parse JSON` came from:

- a pre-stream HTTP error body with the wrong schema,
- a mid-stream SSE failure after HTTP 200 was already committed,
- a client/local tool problem unrelated to the proxy,
- a Session DB recording gap.

## Ground Rules

- Default to the current repo `.env` database connection. Do not switch databases unless the user explicitly asks.
- Do not expose `.env` secrets or full auth filenames in final summaries. Mask account labels when not essential.
- Do not start with broad `request_json::text LIKE` scans on `session_trajectory_requests`; use session aliases and time windows first.
- Treat Session trajectory as the primary request-level evidence source. It normally replaces local `logging-to-file` files for Claude parse-error triage.
- Do not enable `logging-to-file` or full `request-log` just to investigate a normal request failure. Use `journalctl -u cliproxyapi.service` only when checking process-level events or Session DB recording gaps.
- If the user says not to change code, only inspect and report.
- Use UTC timestamps from server logs and Postgres unless explicitly converting from client local time.

## Inputs

Typical user inputs are one or more Claude JSONL transcript paths, for example:

```bash
/root/cliapp/<session-id>.jsonl
```

Treat the JSONL filename as a likely Claude `sessionId`, but verify by parsing the file.

## Workflow

### 0. Check Current Visibility

Before deep triage, confirm what evidence is currently available:

```bash
rg -n '^(session-trajectory-enabled|request-log|error-logs-max-files|logging-to-file):' config.yaml

set -a
source ./.env >/dev/null 2>&1
set +a

psql "$SESSION_TRAJECTORY_PG_DSN" -F $'\t' -Atc "
select request_id, session_id, status, started_at, ended_at, duration_ms,
       source, call_type, provider, model,
       left(coalesce(error_json::text,''),240)
from public.session_trajectory_requests
order by started_at desc
limit 12;
"
```

Interpretation:

- `session-trajectory-enabled: true`: preferred and normally sufficient for request triage; it correlates `provider_session_id`, request ID, status, request JSON, response JSON, error JSON, token usage, model, and timing.
- `logging-to-file: false`: acceptable production posture. Request-level triage should still work from Session DB; process-level logs remain available through systemd journal.
- `request-log` absent or false: preferred production posture. Full per-request local files are disabled.
- `request-log: true`: high-overhead raw request logging. Treat as a temporary last resort for raw headers/wire payloads, not a default triage dependency.

### 1. Extract Client Evidence

For each JSONL:

```bash
rg -n '"sessionId"|"API Error: Failed to parse JSON"|"isApiErrorMessage"|"timestamp"' /path/to/session.jsonl -S
wc -l /path/to/session.jsonl
```

Record:

- `sessionId`
- exact `timestamp` of `API Error: Failed to parse JSON`
- `model`, `cwd`, `version`, `entrypoint` if present
- nearby user prompt and preceding assistant/tool messages

If the JSONL contains no `Failed to parse JSON`, say so explicitly and look for local tool errors such as `tool_result.is_error`, MCP errors, or local hook failures.

### 2. Resolve Session Alias In Postgres

Use the repo `.env` DSN:

```bash
set -a
source ./.env >/dev/null 2>&1
set +a

psql "$SESSION_TRAJECTORY_PG_DSN" -F $'\t' -Atc "
select 'sessions', id, user_id, provider_session_id, source, provider,
       canonical_model_family, request_count, started_at, last_activity_at, status
from public.session_trajectory_sessions
where provider_session_id in ('<client-session-id>')
   or id::text in ('<client-session-id>');

select 'aliases', provider_session_id, session_id, user_id, source, created_at, updated_at
from public.session_trajectory_session_aliases
where provider_session_id in ('<client-session-id>');
"
```

Use the returned internal `session_id` UUID for all request lookups.

### 3. Query Requests By Time Window

Query a narrow window around the client error timestamp:

```bash
psql "$SESSION_TRAJECTORY_PG_DSN" -F $'\t' -Atc "
select request_index, request_id, status, started_at, ended_at, duration_ms, model,
       left(coalesce(error_json::text,''),1200),
       left(coalesce(response_json::text,''),1200)
from public.session_trajectory_requests
where session_id = '<internal-session-uuid>'
  and started_at between '<start-utc>' and '<end-utc>'
order by started_at;
"
```

Classify the result:

- `status=error` plus `response_json` is an empty assistant skeleton usually means upstream/stream failed and the recorder did not preserve the real upstream error in `error_json`.
- `status=success` at the same client timestamp suggests the parse error may be a later client-side transcript/local tool issue or a different request.
- missing rows can mean trajectory persistence failed; check `journalctl -u cliproxyapi.service` for `failed to persist session trajectory`.

Avoid selecting huge `request_json` unless needed. If needed, query one request by `request_id`, not a time range.

### 4. Correlate Process Logs Only If Needed

Use process logs only when Session DB has a gap or when the failure is about service startup, config reload, recorder initialization, or persistence. With `logging-to-file: false`, use systemd journal instead of local `logs/main*.log`:

```bash
journalctl -u cliproxyapi.service --since '<start-utc>' --until '<end-utc>' --no-pager -l
```

Search by `request_id` or known signatures:

```bash
journalctl -u cliproxyapi.service --since '<start-utc>' --until '<end-utc>' --no-pager -l \
  | rg '<request_id>|Headers were already written|request error, error status|failed to persist session trajectory'
```

Important signatures:

- `Headers were already written. Wanted to override status code 200 with 500`: mid-stream failure after SSE/HTTP 200 had already started.
- `Wanted to override status code 200 with 408`: often stream timeout/client disconnect/incomplete stream.
- `request error, error status: 401`: invalidated OAuth token/session expired upstream.
- `request error, error status: 400, error message: No tool output found for function call ...`: historical tool_use/tool_result mismatch sent upstream.
- `failed to persist session trajectory ... unsupported Unicode escape`: Session DB recording gap for that request.

### 5. Avoid Local Request Logs By Default

Do not depend on local request log files for normal triage. They may be absent when `logging-to-file=false` or `request-log=false`, and Session DB is the source of truth for completed request records.

Only check local error snapshots if Session DB lacks the needed raw payload:

```bash
find logs -maxdepth 1 -type f \( -name 'error-v1-messages-<date>*' -o -name '*<request_id>*' \) -printf '%TY-%Tm-%Td %TH:%TM %s %p\n' | sort
```

You can also look up a known request ID through the management route:

```bash
# Requires management authentication in real use.
GET /v0/management/request-log-by-id/<request_id>
```

Behavior to remember:

- When `request-log=true`, request logs are normal full logs and are not returned by `/request-error-logs`.
- When `request-log=false`, `error-*.log` files are forced only for responses the server classifies as errors: final HTTP status `>=400` or an internal API error marker.
- A Claude client `API Error: Failed to parse JSON` can happen after the proxy returns HTTP 200 with a body the client cannot parse. That case may have no `error-v1-messages-*`; use Session DB `response_json` / `error_json`, then correlate `request_id` in journal only if needed.
- If no `error-v1-messages-*` file exists for the incident date, do not treat that as a blocker; rely on Session DB first.

### 6. Inspect Request Consistency Only When Needed

For suspected tool history mismatch, inspect one request:

```bash
psql "$SESSION_TRAJECTORY_PG_DSN" -Atc "
select request_json::text
from public.session_trajectory_requests
where session_id = '<internal-session-uuid>'
  and request_id = '<request_id>';
" > /tmp/request.json
```

If `jq` is unavailable, use Python to count `tool_use.id` and `tool_result.tool_use_id`. Check for missing or orphaned tool results before blaming the stream layer.

## Root Cause Mapping

Use this mapping in the report:

- Pre-stream schema issue: request fails before first SSE chunk; code path should use Claude error JSON body.
- Mid-stream failure: Session request is `error`, and journal may say `Headers were already written`; fix stream terminal error behavior, not only JSON body format.
- Upstream auth/session: logs show `401 invalidated oauth token` or `session expired`; rotate/re-auth upstream account and consider failover behavior.
- Tool history mismatch: logs show `No tool output found for function call`; inspect request history transformation and thinking/tool replay cleanup.
- Local client/tool issue: JSONL shows local `tool_result` errors and no proxy parse error.

## Reporting Template

Keep the final concise:

```text
Found / not found in JSONL:
- <session-id>: <timestamps and client-side error lines>

Session DB:
- provider_session_id -> internal session_id
- request_id/status/model/duration around the error

Main logs:
- matching journal lines, if needed
- whether HTTP 200 was already committed, if visible
- upstream error signature if available

Conclusion:
- classify as pre-stream schema / mid-stream SSE / upstream auth / tool history / local tool issue
- state whether existing fix covers it
- next code or ops action
```
