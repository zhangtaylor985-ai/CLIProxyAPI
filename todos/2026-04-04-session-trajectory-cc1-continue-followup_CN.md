# 2026-04-04 cc1 continue 会话归并后续

## 背景

- `docs/requirements/ai-gateway-session-trajectory-format_CN.md` 要求以 `session_id` 识别并归并完整会话。
- 已实现 PG-first 会话轨迹存储、管理查询、导出与 token-rounds。
- 但 2026-04-04 补充黑盒发现：真实 `cc1` alias 的 `-p` / `-c` 路径下，第二轮请求可能携带新的 `metadata.user_id.session_id`，且请求体不重放历史消息。

## 当前阻塞

- `cc1 -p` 首轮与 `cc1 -c` 次轮在服务端会被识别为两个独立 session。
- 在当前服务端可见字段里：
  - `provider_session_id` 可能漂移
  - `messages` 不具备前缀延续
  - 没有额外稳定业务会话键
- 因此当前无法无歧义归并，不适合宣称这条路径已达到生产级。

## 后续动作

- 继续核对 Claude 本地源码，确认 `print mode continue` 何时会复用 `getSessionId()`，何时会重新起 session。
- 抓一次更底层的入站头与原始请求，排除是否存在 body 外的稳定 session 标识尚未被提取。
- 若客户端本身不稳定透传 session：
  - 评估在 wrapper 层显式透传本地 session ID
  - 或把生产验收边界收敛到“同一 PTY 连续交互”主路径
- 补一轮真实同一 PTY 连续交互的自动化或半自动化黑盒，避免再用 `-p` / `-c` 的弱提示词误判。
