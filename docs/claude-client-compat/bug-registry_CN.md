# Claude 客户端兼容 Bug 台账

## 统计口径

截至 `2026-04-22`，当前台账分三层：

- 已确认且已有修复提交的代理兼容 bug：`9` 类
- 当前仍在调查中的活跃代理兼容回归：`1` 类
- 非代理但高相关的环境/插件/上游风险：`3` 类

这里的“类”是按根因家族统计，不按用户报错次数统计。历史已修复问题和活跃调查问题分开维护，避免台账口径过度乐观。

## A. 已确认并已修复的代理兼容 bug

| ID | 类型 | 典型用户症状 | 根因摘要 | 主要修复提交 | 当前状态 |
| --- | --- | --- | --- | --- | --- |
| CC-BUG-001 | Claude 错误体 schema 不兼容 | `API Error: Failed to parse JSON` | `/v1/messages` 首包前失败时返回了 OpenAI 风格错误 JSON，而不是 Claude 风格错误体 | `51cb1f1b` | 已修复 |
| CC-BUG-002 | 结构化 JSON 输出映射缺失 | `Agent/code-reviewer` 工具结果内 `Failed to parse JSON` | Claude Responses 请求路径缺少 `response_format/json_schema -> text.format` 映射 | `98ee82dc` | 已修复 |
| CC-BUG-003 | 成功流 EOF 误判为成功 | `stream closed before response.completed`，随后再衍生 `undefined is not an object` / parse 错 | 上游流在 `response.completed` 前断开，却被当成成功关闭 | `98ee82dc` | 已修复 |
| CC-BUG-004 | bootstrap retry 覆盖具体错误 | 用户看到泛化 `500 auth_not_found` 或模糊失败，而不是最早的断流错误 | 首包前重试失败时，把更具体的流错误降级成泛化错误 | `98ee82dc` | 已修复 |
| CC-BUG-005 | Responses SSE 组帧不安全 | `Failed to parse JSON`、半截 JSON、空 chunk 误读 | handler 把上游 chunk 直接写给下游，没有先完成 `event/data` 组帧和 JSON 完整性判断 | `8c17516b` | 已修复 |
| CC-BUG-006 | 首包前空流返回 `HTTP 200` 空 body | `API returned an empty or malformed response (HTTP 200)` | 上游空流在首个 payload 前关闭时，Claude handler 仍返回了 `200` 空响应 | `6303ed05` | 已修复 |
| CC-BUG-007 | tool-call SSE 状态机兼容缺口 | `undefined is not an object (reading 'speed' / 'content' / 'input_tokens')`、`Unexpected end of JSON input` | 成功流里 tool block 的开启/关闭时机不稳，空 `input_json_delta`、usage 默认字段不完整 | `6303ed05` | 已修复 |
| CC-BUG-008 | `response.output_item.done` message fallback 缺失 | 工具执行已成功，但随后 UI 合成 `Failed to parse JSON` | 上游直接在 `response.output_item.done` 里给完整 `message` 时，Claude translator 没补 text block fallback | `96f01f9c` | 已修复 |
| CC-BUG-009 | `tool_result.content[]` 未知 block 被静默丢弃 | `AskUserQuestion` / `TaskUpdate` / 非标准 tool_result 后续写时再炸 | request translator 只保留 `text/image`，把未知子类型直接丢掉，导致上下文失真 | `96f01f9c` | 已修复 |

## B. 活跃调查中的代理兼容回归

| ID | 类型 | 典型用户症状 | 当前判断 | 当前状态 |
| --- | --- | --- | --- | --- |
| ACT-001 | `Agent/Explore` 子代理链路间歇性失败 | `Failed to parse JSON`、`empty or malformed response (HTTP 200)`、`stream closed before response.completed` | 在真实线上 plan mode + 子代理探索场景里仍能看到，说明这条链路还没有完全收口 | 调查中 |

详细排查入口见：[活跃调查](./active-investigations_CN.md)

## C. 环境/插件侧风险

| ID | 类型 | 典型表现 | 说明 | 当前状态 |
| --- | --- | --- | --- | --- |
| CC-RISK-001 | 多 hook 叠加态污染 | `SessionStart` 或 hook 输出里混入多段 JSON / 文本，debug-file 出现 `Failed to parse hook output as JSON` | 常见于 `claude-mem`、自定义 `cmux` hook、外部 wrapper 同时存在时；不等同于代理兼容 bug | 监控中 |
| CC-RISK-002 | 插件尾延迟 | `Stop hook` 长时间不收尾，UI 看起来像卡住 | 目前最常见的是 `claude-mem` summarize/stop hook 慢，不直接等于协议错误，但会影响用户感知 | 监控中 |
| CC-RISK-003 | 上游 TLS / 证书校验失败 | UI 端先看到通用 API 错，session 里真实原因是 `UNKNOWN_CERTIFICATE_VERIFICATION_ERROR` | 这类问题不应误记成 translator bug；需要优先确认实际请求目标、证书链和中间代理 | 监控中 |

## 详细整理

### CC-BUG-001 `/v1/messages` 错误体 schema 不兼容

- 主要症状：
  - `API Error: Failed to parse JSON`
- 根因：
  - Claude 客户端期待的是 Claude 风格错误体：
    - `{"type":"error","error":{"type":"api_error","message":"..."}}`
  - 但服务端部分路径返回了 OpenAI 风格错误体：
    - `{"error":{"message":"...","type":"...","code":"..."}}`
- 主要修复：
  - Claude handler 在首包前失败路径改用 Claude 风格错误 JSON。
- 提交：
  - `51cb1f1b`

### CC-BUG-002 结构化 JSON 输出映射缺失

- 主要症状：
  - `Agent/code-reviewer` 的 `tool_result` 内出现 `Failed to parse JSON`
- 根因：
  - `response_format/json_schema` 没有映射到 Claude 侧 `text.format`
- 主要修复：
  - 在 `internal/translator/claude/openai/responses/claude_openai-responses_request.go` 补映射。
- 提交：
  - `98ee82dc`

### CC-BUG-003 成功流 EOF 误判为成功

- 主要症状：
  - `stream closed before response.completed`
  - 后续再炸成 `undefined is not an object`
- 根因：
  - `response.completed` 缺失时，流被错误当成正常结束
- 主要修复：
  - executor 明确把“未见 `response.completed` 即 EOF”视作终止错误
- 提交：
  - `98ee82dc`

### CC-BUG-004 bootstrap retry 覆盖具体错误

- 主要症状：
  - 用户最终看到泛化系统错误，不利于定位
- 根因：
  - 首包前 retry 失败时，把第一次更具体的错误覆盖掉了
- 主要修复：
  - 优先保留第一次更具体的断流/上游错误
- 提交：
  - `98ee82dc`

### CC-BUG-005 Responses SSE 组帧不安全

- 主要症状：
  - `Failed to parse JSON`
  - 半截 `data:`、拆开的 event/data、客户端空响应误读
- 根因：
  - 原实现直接透传 chunk，没有先校验 `data:` 是否已是完整合法 JSON
- 主要修复：
  - 引入保守 SSE re-framer，只在形成完整合法事件后再向下游发
- 提交：
  - `8c17516b`

### CC-BUG-006 首包前空流返回 `HTTP 200` 空 body

- 主要症状：
  - `API returned an empty or malformed response (HTTP 200)`
- 根因：
  - 上游流在首个 payload 前就关闭，handler 仍然维持 `200` 并结束响应
- 主要修复：
  - 改为显式返回服务端错误，不再返回空 `200`
- 提交：
  - `6303ed05`

### CC-BUG-007 tool-call SSE 状态机兼容缺口

- 主要症状：
  - `undefined is not an object (reading 'speed')`
  - `undefined is not an object (evaluating 'eH.content')`
  - `Unexpected end of JSON input`
- 根因：
  - tool block 关闭过早
  - arguments 未完整前就 stop
  - usage 兼容字段不完整，客户端直接读到 `null` / 缺 key
- 主要修复：
  - 避免空 `input_json_delta`
  - 等 arguments 到齐或 `response.completed` 再关闭 block
  - 补齐更保守的 usage 默认字段
- 提交：
  - `6303ed05`

### CC-BUG-008 `response.output_item.done` message fallback 缺失

- 主要症状：
  - `Bash + TaskUpdate` 或其他多工具返回后，工具都已成功，但随后正文继续生成时炸掉
- 根因：
  - 上游直接把完整正文放在 `response.output_item.done.item.type=message`
  - 当前 translator 没补 `content_block_start/delta/stop` fallback
- 主要修复：
  - 在 Claude translator 里补 message fallback
  - 同时用 `HasTextDelta` 避免和已有 delta 流重复
- 提交：
  - `96f01f9c`

### CC-BUG-009 `tool_result.content[]` 未知 block 被静默丢弃

- 主要症状：
  - `AskUserQuestion` / `TaskUpdate` 返回后继续生成正文时，最终合成 `Failed to parse JSON`
- 根因：
  - `tool_result.content[]` 只处理了 `text/image`
  - 其他子类型被静默丢弃，导致传给上游的 `function_call_output` 不完整
- 主要修复：
  - 未知子类型保守降级为 `input_text`
  - 至少保留原始 JSON 文本，不再直接丢失
- 提交：
  - `96f01f9c`

## 当前维护口径

后续如果再有新问题，统一按下面两类入账：

- 代理兼容 bug
  - 能落到具体转换器、handler、executor、流式协议状态机
- 环境/插件风险
  - 发生在 hook、plugin、外部 wrapper、MCP、客户端版本漂移
- 活跃调查
  - 线上仍在发生，但根因和代码入口还没完全闭环的回归

不要再把所有问题统一记成“又一个 parse JSON bug”。这会让问题重新变得不可维护。
