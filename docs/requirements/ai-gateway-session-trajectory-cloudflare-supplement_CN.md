# AI Gateway 对话轨迹 Cloudflare 映射与导出补充说明

本文档承接 [AI Gateway 对话轨迹中转格式需求](./ai-gateway-session-trajectory-format_CN.md) 原第 6 章及之后内容，聚焦 Cloudflare AI Gateway 的映射约定、实测结论、当前脚本落点与导出约束。

## 6. Cloudflare AI Gateway 映射约定

Cloudflare AI Gateway 当前可用的关键接口：

- `GET /accounts/{account_id}/ai-gateway/gateways/{gateway_id}/logs`
- `GET /accounts/{account_id}/ai-gateway/gateways/{gateway_id}/logs/{id}`
- `GET /accounts/{account_id}/ai-gateway/gateways/{gateway_id}/logs/{id}/request`
- `GET /accounts/{account_id}/ai-gateway/gateways/{gateway_id}/logs/{id}/response`

映射建议：

- `request_id`：优先取请求体内 `request_id`，否则回退到 Gateway `log.id`
- `start_time`：优先取请求体 `start_time`，否则回退到 `log.created_at`
- `end_time`：优先取请求体 `end_time`，否则用 `start_time + duration`
- `status`：优先取请求体显式状态，否则由 `log.success` 推导为 `success` / `error`
- `call_type`：优先取请求体 `call_type`，否则回退到 `log.request_type` 或 `log.path`
- `user_agent`：优先取请求体 `user_agent`，否则尝试从请求头提取
- `system`、`tools`、`messages`：优先直接取请求体
- `response`：直接取 `/response` 返回体

## 6.1 2026-04-03 实测结论

基于账号 `84e59e86095c64e979b0faa16ed38cb1`、网关 `cc-gateway` 的真实下载结果，当前确认如下：

- `GET /logs` 可正常返回成功日志列表。
- 真实对话主路径当前主要表现为：
  - `v1/messages?beta=true`
- 还会混入非对话日志，例如：
  - `v1/messages/count_tokens?beta=true`
  - `api/event_logging/batch`
- 其中：
  - `count_tokens` 不属于用户与上层 AI 的正文对话轨迹，应默认排除。
  - `event_logging` 属于埋点/遥测事件，也不应作为正文对话轨迹导出。
- 实测可下载的对话日志中：
  - `/request` 返回的是原始请求体
  - `/response` 返回的是原始模型响应体
  - 这与手工样本 `ai-gateway-data/user.json` / `ai-gateway-data/answer.json` 的结构是一致的
- `metadata.user_id` 真实形态通常类似：
  - `user_xxx_account_xxx_session_xxx`
  - 因此脚本需要从中拆出：
    - `user_id`
    - `session_id`

## 7. 当前脚本落点

当前下载/归档脚本：

- `scripts/download_ai_gateway_logs.py`

脚本职责：

- 从 Cloudflare AI Gateway 批量拉取日志
- 逐条下载 request / response / detail
- 自动提取 `session_id`
- 按会话目录落盘
- 默认仅导出 `success=true` 的成功样本，错误日志默认过滤
- 默认排除 `count_tokens` 与 `event_logging` 这类非正文对话日志
- 支持使用手工下载的 `user.json` + `answer.json` 直接做本地归一化验证
- 支持 `--resume` 续跑，避免中断后重头开始
- 支持 `--skip-offset` 做人工分段导出
- 支持对 `1010 Access denied`、`timeout` 等明细下载失败做重试

### 推荐批量导出方式

小批量验证：

```bash
python3 scripts/download_ai_gateway_logs.py fetch-gateway \
  --account-id 84e59e86095c64e979b0faa16ed38cb1 \
  --gateway-id cc-gateway \
  --max-logs 20 \
  --output-dir ai-gateway-data/session-exports \
  --direction asc \
  --workers 2 \
  --http-timeout 20
```

中断后续跑：

```bash
python3 scripts/download_ai_gateway_logs.py fetch-gateway \
  --account-id 84e59e86095c64e979b0faa16ed38cb1 \
  --gateway-id cc-gateway \
  --max-logs 500 \
  --output-dir ai-gateway-data/session-exports \
  --direction asc \
  --workers 3 \
  --http-timeout 20 \
  --resume
```

如果需要人工分段：

```bash
python3 scripts/download_ai_gateway_logs.py fetch-gateway \
  --account-id 84e59e86095c64e979b0faa16ed38cb1 \
  --gateway-id cc-gateway \
  --max-logs 100 \
  --skip-offset 100 \
  --output-dir ai-gateway-data/session-exports-part2 \
  --direction asc \
  --workers 3 \
  --http-timeout 20
```

## 8. 当前约束

- 本机当前 `wrangler whoami` 未通过，说明不能依赖本地 Wrangler OAuth 作为稳定下载前提。
- 已验证：Wrangler OAuth 登录虽可访问 `/user`、`/accounts`，但 `ai-gateway` 日志接口可能仍返回 `403 Authentication error`。
- 批量下载优先走 `CLOUDFLARE_API_TOKEN`，并确保具备 AI Gateway 读取权限。
- 如果只有浏览器手工下载样本，也应能通过本地归一化模式先验证格式是否满足需求。
