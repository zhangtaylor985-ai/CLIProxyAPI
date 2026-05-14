# 2026-04-29 GPT-5.5 Raw SSE 与告警缺失排查记录

## 背景

线上用户反馈：从 2026-04-29 00:00 UTC 后，用户侧错误仍然大量出现，但 Telegram `CC Alerts` channel 没有继续收到告警。

截图中的典型客户端症状：

- `TodoWrite failed ... The required parameter todos is missing`
- `stream error: upstream response.incomplete before response.completed: reason=max_output_tokens`

相关用户 API key 以 `sk-YNA...C8Cs` 记录。用户为了止血，已在 2026-04-29 05:02:08 UTC 左右将该 key 从 GPT-5.5 手动改回 GPT-5.4。

## 已确认事实

### 1. Raw SSE 线上目录状态

排查时线上 raw SSE 诊断开关仍开启：

- `CLIPROXY_CODEX_RAW_SSE_LOG_DIR`
- `CLIPROXY_CODEX_RAW_SSE_MAX_BYTES`

线上目录：

`/root/cliapp/CLIProxyAPI/logs/codex-raw-sse`

清理前状态：

- 目录大小约 `9.0G`
- 文件数约 `28283`
- 根分区使用量约 `19G / 150G`

已执行清理并关闭开关：

- 删除 `.env` 中两个 `CLIPROXY_CODEX_RAW_SSE_*` 变量
- 重启 `cliproxyapi.service`
- 健康检查 `/healthz` 返回 ok
- 清理后 raw SSE 文件数为 `0`
- 目录残留大小约 `1.9M`
- `.env` 备份：`/root/cliapp/CLIProxyAPI/.env.raw-sse-off-20260429T061755Z.bak`

本地未发现同步副本：

- 未找到本地 `logs/codex-raw-sse`
- 未找到 `codex-raw-sse-*.log`
- 未找到相关 tar/zip/tgz 包
- shell history 未发现从 `204.168.245.138:/root/cliapp/CLIProxyAPI/logs/codex-raw-sse` 拉取记录

因此，raw SSE response body 级证据已丢失，不能再回放原始 upstream SSE 帧。

### 2. 该 key 的失败窗口

session trajectory user id：

`api_key:3958c786178694b99ddf9b54`

session trajectory 中该 key 今日错误摘要：

- `2026-04-29 04:53:51 UTC` 到 `2026-04-29 05:02:28 UTC`
- 共记录 `10` 个 `status=error`
- Claude 侧记录模型显示为 `claude-opus-4-7`
- billing usage 侧显示当时实际上游模型为 `gpt-5.5`

失败 request id：

- `ac70835d`
- `63718876`
- `38b6ad2c`
- `ed5483f9`
- `ca028e57`
- `a81ac1fc`
- `1691a5a9`
- `df336de0`
- `57534703`
- `44b0ee6e`

其中两个关键错误形态：

- `ac70835d`：assistant 响应包含普通 text 后接半截 `TodoWrite`，`tool_use.input = {}`，`stop_reason = null`
- `63718876`：assistant 响应只剩半截 `TodoWrite`，`tool_use.input = {}`，`stop_reason = null`

后续多条错误主要为：

`stream error: stream disconnected before completion: stream closed before response.completed`

billing usage 在同一窗口显示 `gpt-5.5` 连续失败，失败记录 token 计数均为 `0`，cost 为 `0`。从 `2026-04-29 05:02:17 UTC` 开始，usage 记录切回 `gpt-5.4` 并恢复成功。

### 3. 报错含义

当前证据支持的结论：

- 上游流式响应没有正常到达 `response.completed`
- 部分失败轮在 Claude 侧转换后形成了半截 tool_use
- 半截 `TodoWrite` 的 input 为空对象 `{}`，导致 Claude Code 客户端校验报 `todos` 必填字段缺失
- 截图里的 `max_output_tokens` 表明至少部分失败是上游返回 `response.incomplete`，reason 为输出上限耗尽

由于 raw SSE 原始文件已清理，无法进一步确认每个失败 request 的原始 SSE 终止事件、sequence、最后一帧内容或 scanner 状态。

## 告警缺失原因

Telegram 本身可达：

- bot `getMe` 成功
- provider/error/ops 三个 channel `getChat` 成功

告警缺失更可能是服务端触发条件问题，不是 Telegram 网络或权限问题。

当前代码行为：

- Telegram error-log hook 只接 `error/fatal/panic` 日志
- `200 + api_response_error` 在 Gin request log 中按 `info` 输出
- `408/429/404` 在 Gin request log 中按 `warn` 输出
- 普通 `500` Gin request line 还可能被 `isNoisyRequestLogEntry` 过滤
- `sendTelegram` 的返回错误在 `dispatch` 中被忽略，发送失败不会反向打日志

因此，这次用户侧流式失败虽然被 request log、error request log、session trajectory、billing usage 捕获，但没有进入 Telegram error-log alert 路由。

## 已完成止血

1. 用户 key `sk-YNA...C8Cs` 已切回 GPT-5.4。
2. 切回后 usage 显示请求恢复成功。
3. 线上 raw SSE 目录已清理。
4. Raw SSE 诊断开关已关闭并重启服务生效。

## 剩余风险与后续建议

1. 补告警规则：对 `API_RESPONSE_ERROR` / session trajectory `status=error` / billing failed usage 增加专门告警入口，不依赖 Gin log level。
2. 对流式请求中 `response.incomplete reason=max_output_tokens` 设独立 fingerprint 和节流，避免漏报也避免刷屏。
3. `dispatch` 不应静默吞掉 Telegram 发送错误，至少应打 debug/warn 级别的发送失败摘要。
4. Raw SSE 诊断后续只应短期开启，并增加自动清理或 size cap，避免再次堆积。
5. 针对半截 tool_use，应继续加保护：未收到 `response.completed` 的请求不应把半截 tool_use 当作成功 assistant 消息写回会话历史。

