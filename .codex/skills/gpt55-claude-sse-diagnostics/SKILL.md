---
name: gpt55-claude-sse-diagnostics
description: 当用户排查 CLIProxyAPI 中 Claude 到 GPT-5.5 路由出现 `stream closed before response.completed`、半截 `tool_use`、Claude Code/Claude CLI API Error、或需要在线上开启 Codex raw upstream SSE 诊断并对齐 session trajectory、JSONL、systemd 日志时使用。适用于 `/Users/taylor/code/tools/CLIProxyAPI-ori` 与线上 `/root/cliapp/CLIProxyAPI`。
---

# GPT-5.5 Claude SSE Diagnostics

## Goal

用于排查显式选择 GPT-5.5 的 Claude Code / Claude CLI 链路中：

- `stream error: stream disconnected before completion: stream closed before response.completed`
- 半截 `tool_use`，尤其是 tool input 为空或 `stop_reason=null`
- Claude JSONL / UI 里出现 API Error，但服务端 HTTP 状态看起来是 200
- 需要判断是上游真实 EOF、本地 scanner/framer 问题，还是网络/凭据断流

默认结论口径：

- 能用 session trajectory 定位失败 request 与下游症状。
- 不能仅凭历史 session trajectory 还原未归一化 raw upstream SSE。
- 没有 raw SSE 前，不要声称已定位 GPT-5.5 根因；只能说已定位失败轮和失败形态。

## Field Mapping

Claude JSONL 里的 `sessionId` 通常不是 `session_trajectory_sessions.id`。

查询时按这个顺序理解：

- `session_trajectory_sessions.id`：内部 canonical UUID。
- `session_trajectory_sessions.provider_session_id`：Claude JSONL / 客户端 session id。
- `session_trajectory_session_aliases.provider_session_id`：历史 alias 映射。

如果用户给的是 JSONL `sessionId`，先查 `provider_session_id`，不要只查主键。

## Local PG Lookup

在源仓库执行，默认读取当前 `.env` 的 session PG 配置：

```bash
cd /Users/taylor/code/tools/CLIProxyAPI-ori
set -a; source .env >/dev/null 2>&1; set +a
SCHEMA=${SESSION_TRAJECTORY_PG_SCHEMA:-public}
SID='<claude-jsonl-session-id>'

psql "$SESSION_TRAJECTORY_PG_DSN" -X -v ON_ERROR_STOP=1 -A -F $'\t' <<SQL
SELECT 'sessions.id' AS place, COUNT(*)::text AS count
FROM "$SCHEMA".session_trajectory_sessions
WHERE id::text = '$SID'
UNION ALL
SELECT 'sessions.provider_session_id', COUNT(*)::text
FROM "$SCHEMA".session_trajectory_sessions
WHERE provider_session_id = '$SID'
UNION ALL
SELECT 'aliases.provider_session_id', COUNT(*)::text
FROM "$SCHEMA".session_trajectory_session_aliases
WHERE provider_session_id = '$SID';

SELECT s.id::text AS canonical_session_id, COALESCE(s.provider_session_id,'') AS provider_session_id,
       s.source, s.call_type, s.provider, s.canonical_model_family,
       s.request_count, s.status, s.started_at, s.last_activity_at
FROM "$SCHEMA".session_trajectory_sessions s
LEFT JOIN "$SCHEMA".session_trajectory_session_aliases a ON a.session_id = s.id
WHERE s.id::text = '$SID' OR s.provider_session_id = '$SID' OR a.provider_session_id = '$SID'
GROUP BY s.id
ORDER BY s.last_activity_at DESC
LIMIT 5;
SQL
```

定位失败 request：

```bash
psql "$SESSION_TRAJECTORY_PG_DSN" -X -v ON_ERROR_STOP=1 -A -F $'\t' <<SQL
SELECT r.request_index, r.request_id, COALESCE(r.provider_request_id,'') AS provider_request_id,
       COALESCE(r.upstream_log_id,'') AS upstream_log_id,
       r.status, r.model, r.duration_ms, r.input_tokens, r.output_tokens,
       r.started_at, r.ended_at,
       LEFT(COALESCE(r.error_json #>> '{error,message}', r.response_json #>> '{error,message}',
                     r.error_json::text, r.response_json::text, ''), 1200) AS message
FROM "$SCHEMA".session_trajectory_requests r
JOIN "$SCHEMA".session_trajectory_sessions s ON s.id = r.session_id
LEFT JOIN "$SCHEMA".session_trajectory_session_aliases a ON a.session_id = s.id
WHERE s.id::text = '$SID' OR s.provider_session_id = '$SID' OR a.provider_session_id = '$SID'
ORDER BY r.request_index;
SQL
```

重点看：

- `status <> success`
- `duration_ms`
- `provider_request_id`
- `input_tokens/output_tokens/total_tokens` 是否全 0
- `response_json.stop_reason` 是否为 `null`
- `content` 是否停在半截 text / tool_use

## Online Raw SSE Capture

前提：线上二进制必须包含 raw SSE diagnostics 代码。

在生产机开启：

```bash
sudo mkdir -p /root/cliapp/CLIProxyAPI/logs/codex-raw-sse
sudo chmod 700 /root/cliapp/CLIProxyAPI/logs/codex-raw-sse
sudo vim /root/cliapp/CLIProxyAPI/.env
```

追加：

```bash
CLIPROXY_CODEX_RAW_SSE_LOG_DIR=/root/cliapp/CLIProxyAPI/logs/codex-raw-sse
CLIPROXY_CODEX_RAW_SSE_MAX_BYTES=104857600
```

重启并确认：

```bash
sudo systemctl restart cliproxyapi
sleep 2
systemctl status cliproxyapi --no-pager -l
```

复现后查：

```bash
sudo ls -lh /root/cliapp/CLIProxyAPI/logs/codex-raw-sse
sudo tail -60 /root/cliapp/CLIProxyAPI/logs/codex-raw-sse/codex-raw-sse-*.log
```

判断：

- `# saw_completion_event: true`：该 upstream attempt 有完成事件。
- `# saw_completion_event: false` 且 `# eof: true`：流在 completion 前结束。
- `# saw_terminal_event: true` + `# terminal_event: response.incomplete`：上游返回了终止事件，但不是成功完成。
- `# incomplete_reason: max_output_tokens`：上游因输出上限提前结束；若此时工具参数半截或 `tool_use.input={}`，不要当成功写回 transcript。
- `# scanner_error: ...`：本地读流/scanner 层异常。
- `# raw_sse_log_truncated: true`：日志达到上限，需提高 max bytes 或缩小复现范围。

raw SSE 文件记录 response body，可能包含用户内容、工具参数、reasoning encrypted content。只短期开启，抓到样本后关闭。

关闭：

```bash
sudo sed -i.bak '/^CLIPROXY_CODEX_RAW_SSE_LOG_DIR=/d;/^CLIPROXY_CODEX_RAW_SSE_MAX_BYTES=/d' /root/cliapp/CLIProxyAPI/.env
sudo systemctl restart cliproxyapi
```

## Evidence Bundle

排查结论前至少收集：

- 用户 Claude JSONL
- JSONL `sessionId`
- PG canonical session id
- 失败 `request_index`
- 本地 `request_id`
- `provider_request_id`
- 失败时间窗
- `response_json` / `error_json` 摘要
- raw SSE 文件尾部的 `eof` / `saw_completion_event` / `scanner_error`
- 若存在 `response.incomplete`，记录 `terminal_event` 与 `incomplete_reason`
- `journalctl -u cliproxyapi` 对应时间窗

线上 systemd 日志：

```bash
sudo journalctl -u cliproxyapi --since '2026-04-28 16:45:00' --until '2026-04-28 17:05:00' --no-pager -l
```

## Safe Conclusion Wording

可以说：

- “已定位失败轮和失败形态。”
- “该轮下游形成半截 tool_use，且未收到 completion。”
- “raw SSE 显示上游/链路在 completion 前 EOF。”
- “raw SSE 显示 scanner error，更偏本地读流问题。”
- “raw SSE 显示上游返回 `response.incomplete`，reason 为 `max_output_tokens`；这不是本地 scanner 丢帧，应保留失败处理，避免半截 tool_use 污染会话。”

不要说：

- “已确认是 GPT-5.5 模型 bug。”除非 raw SSE、网络日志、上游响应都能支持。
- “session 查不到。”除非已同时查过 `id`、`provider_session_id`、alias。
- “HTTP 200 就是成功。”流式请求可能已经 200 后中途失败。

## Relationship to cc1 TTY Skill

如果任务重点是真实 TTY、PTY 屏幕、`--debug-file`、客户端是否命中本地 `ANTHROPIC_BASE_URL`，使用 `cc1-tty-blackbox-testing`。

如果任务重点是线上 GPT-5.5 断流、raw upstream SSE、session trajectory 取证，使用本 skill。
