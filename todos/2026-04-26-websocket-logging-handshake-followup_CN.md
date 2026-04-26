# WebSocket 日志与握手记录后续评估

## 背景

上游 `CLIProxyAPI-pure` 在 `v6.9.8..v6.9.31` 之间有两项 WebSocket 可观测性改动：

- `34339f61`: WebSocket 请求 / 响应时间线日志。
- `4f8acec2`: WebSocket 握手失败记录。

本次生产稳定性移植未直接合入这两项，因为它们改动面覆盖 OpenAI Responses WebSocket / Codex WebSocket 转发链路，容易和当前的 tool-call repair、compaction reset、request log、session trajectory 逻辑交叉。当前优先先落低风险稳定性补丁。

## 价值判断

- 对 Codex WebSocket、OpenAI Responses WebSocket 长尾问题有排障价值，尤其是握手失败、半开连接、上游断流、客户端提前断开、`response.completed` 缺失等场景。
- 对生产稳定性本身不是直接修复，但能显著降低复盘成本，适合作为 tool repair 稳定后的一项可运维增强。
- 需要严格控制敏感信息，用户侧日志和错误响应不能泄露内部 provider、auth、真实模型、账号邮箱、上游凭据或 auth index。

## 建议实施顺序

1. 先补握手失败记录，范围更小，优先记录状态码、响应头白名单、截断后的响应体、request_id、session_id，不记录 Authorization / Cookie / auth file 路径。
2. 再补 WebSocket 时间线日志，默认受 debug / request log 开关控制，并设置硬性大小上限与截断标记。
3. 最后把 WebSocket 时间线与现有 request log / session trajectory 做关联，只存排障必要摘要，避免把完整工具输出重复写入多处存储。

## 验收建议

- 单测覆盖握手失败响应体截断、敏感 header 过滤、正常 completed 事件不改变转发语义。
- 真实黑盒覆盖 Codex WebSocket 正常流、握手失败、客户端提前断开、上游断流四类场景。
- 检查浏览器 Network、客户端错误响应、普通用户 API 响应，确认无内部 GPT / provider / auth 细节泄露。
