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

find logs -maxdepth 1 -type f \( -name 'main*.log' -o -name 'error-v1-messages-*' \) \
  -printf '%TY-%Tm-%Td %TH:%TM %s %p\n' | sort | tail -30

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

- `session-trajectory-enabled: true`: prefer Session DB first; it can correlate `provider_session_id`, request ID, request status, response JSON, and error JSON.
- `request-log: true`: detailed request logs are written for all supported requests; use `logs/*-<request_id>.log` or the management `request-log-by-id` endpoint.
- `request-log` absent or false: full request logs are disabled, but forced `error-*.log` files are still generated for HTTP/API error responses.
- `error-v1-messages-*` exists only for server-detected error responses. A client-side parse failure over HTTP 200 may not generate an error log.

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
- missing rows can mean trajectory persistence failed; check main logs for `failed to persist session trajectory`.

Avoid selecting huge `request_json` unless needed. If needed, query one request by `request_id`, not a time range.

### 4. Correlate Main Logs

Find the main log file covering the request time:

```bash
find logs -maxdepth 1 -type f -name 'main*log' -printf '%TY-%Tm-%Td %TH:%TM %s %p\n' | sort
```

Then search by `request_id`:

```bash
rg -n '<request_id>|Headers were already written|request error, error status|failed to persist session trajectory' logs/main*.log -S
```

For exact context:

```bash
sed -n '<start-line>,<end-line>p' logs/<main-log-file>
```

Important signatures:

- `Headers were already written. Wanted to override status code 200 with 500`: mid-stream failure after SSE/HTTP 200 had already started.
- `Wanted to override status code 200 with 408`: often stream timeout/client disconnect/incomplete stream.
- `request error, error status: 401`: invalidated OAuth token/session expired upstream.
- `request error, error status: 400, error message: No tool output found for function call ...`: historical tool_use/tool_result mismatch sent upstream.
- `failed to persist session trajectory ... unsupported Unicode escape`: Session DB recording gap for that request.

### 5. Check Error Request Logs

Request/response error snapshots rotate aggressively. Verify whether files still exist:

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
- A Claude client `API Error: Failed to parse JSON` can happen after the proxy returns HTTP 200 with a body the client cannot parse. That case may have no `error-v1-messages-*`; use Session DB `response_json` / `error_json`, then correlate `request_id` in `main.log`.
- If no `error-v1-messages-*` file exists for the incident date, state that the detailed request snapshot is unavailable and rely on main logs plus Session DB.

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
- Mid-stream failure: access log shows `200`, Session request is `error`, and main log says `Headers were already written`; fix stream terminal error behavior, not only JSON body format.
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
- matching request_id lines
- whether HTTP 200 was already committed
- upstream error signature if available

Conclusion:
- classify as pre-stream schema / mid-stream SSE / upstream auth / tool history / local tool issue
- state whether existing fix covers it
- next code or ops action
```
