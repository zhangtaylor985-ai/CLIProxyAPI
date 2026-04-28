# GPT-5.5 Claude 长期兼容策略

负责人：Codex

## 目标定义

长期目标不是“减少几条报警”，而是让显式选择 `gpt-5.5` 的 Claude Code / Claude CLI 用户达到接近 `gpt-5.4` 的生产稳定性：

- 同一 PTY 连续多轮稳定。
- 工具调用、工具结果续写、大工具输出稳定。
- `stream-json` / text / 交互式 TTY 都不出现协议级硬错误。
- 不产生半截 `tool_use`、空 input、`stop_reason=null` 等 transcript 污染。
- 错误出现时能在一次排查内归类到明确层级，而不是靠猜。

生产边界：

- Codex 负责方案、实现、测试、文档、main push。
- 生产部署默认由 Taylor 执行。
- Codex 只有在 Taylor 明确要求“现在帮我部署 / 重启 / 上线”时才操作生产 systemd。

## 当前方案定位

`gpt-5.5(high)` 工具请求降到 `gpt-5.5(medium)` 是生产 mitigation，不是最终答案。

它解决的是：

- 降低已观测到的 `response.incomplete/max_output_tokens` 触发概率。
- 避免半截工具参数继续进入 Claude transcript。
- 不影响 `gpt-5.4` 和其他模型。

它没有解决的是：

- 为什么 `gpt-5.5(high)` 在 Claude 工具链路中更容易提前 `response.incomplete`。
- 是否存在更好的 upstream 参数组合，例如 token budget、parallel tool calls、reasoning summary、tool schema 压缩。
- 是否能让 `gpt-5.5(high)` 工具路径最终也达到稳定。

因此当前策略必须保持可回滚、可度量、可比较。

## 问题分层

每个失败样本必须先归入一个层级：

1. **请求前失败**
   - prompt context preflight 超限。
   - model 为空或 provider lookup 失败。
   - API key policy / target mapping 异常。

2. **上游 HTTP 失败**
   - Codex upstream 非 2xx。
   - 鉴权失败、限流、账号不可用。
   - proxy / SOCKS / HTTP2 连接错误。

3. **上游 SSE 非成功终止**
   - `response.incomplete`
   - `incomplete_details.reason=max_output_tokens`
   - `incomplete_details.reason` 其他值
   - 有 terminal event，但不是 success。

4. **传输层 / scanner 失败**
   - scanner error。
   - HTTP2 `INTERNAL_ERROR`。
   - EOF 前没有任何 terminal event。

5. **翻译层协议失败**
   - `response.completed` 已到，但 Claude event 生命周期不合法。
   - tool delta / tool done 顺序不合法。
   - usage / stop_reason / content block 缺失导致客户端解析失败。

6. **客户端表现失败**
   - Claude CLI `Failed to parse JSON`。
   - `undefined is not an object`。
   - UI API Error，但服务端实际已写出半截流。

## 样本证据包

每个线上样本至少记录：

- 报警 Request ID。
- Claude JSONL `sessionId`。
- `session_trajectory_sessions.id`。
- `provider_session_id` / alias 命中方式。
- `request_index`。
- `request_id`。
- `provider_request_id`。
- 原始 Claude model。
- 最终内部 target model 与 effort。
- target 来源：global / API key policy / explicit request / fallback。
- 是否带 tools。
- 是否带 `tool_use` / `tool_result` 历史。
- upstream HTTP status。
- raw SSE terminal event。
- incomplete reason。
- scanner error。
- downstream Claude response 摘要：`stop_reason`、usage、content block 类型。
- 客户端表现：API Error / JSON parse / UI 卡死 / 可继续。

缺少 raw SSE 时，只能说“定位到失败轮和失败形态”，不能说“定位到根因”。

## 日志与诊断改造路线

### 第一阶段：当前已完成

- raw upstream SSE 默认关闭、按环境变量开启。
- raw SSE footer 记录：
  - `eof`
  - `saw_completion_event`
  - `saw_terminal_event`
  - `terminal_event`
  - `incomplete_reason`
  - `scanner_error`
- executor 对 `response.incomplete` 给出准确错误。

### 第二阶段：结构化错误日志

目标：报警里直接看到层级，不再靠人工拼日志。

新增或补齐字段：

- `component`
- `stage`
- `request_id`
- `provider_request_id`
- `handler_type`
- `client_kind`
- `source_format`
- `requested_model`
- `effective_target_model`：只进入内部日志 / 管理端，不进入用户响应。
- `target_family`
- `reasoning_effort`
- `target_source`
- `has_tools`
- `has_tool_result_history`
- `has_tool_use_history`
- `upstream_status`
- `upstream_terminal_event`
- `upstream_incomplete_reason`
- `scanner_error`
- `downstream_committed`

错误分类建议：

- `claude_gpt_preflight_context_limit`
- `claude_gpt_model_resolution_failed`
- `codex_upstream_http_error`
- `codex_upstream_response_incomplete`
- `codex_upstream_missing_terminal_event`
- `codex_upstream_scanner_error`
- `claude_stream_protocol_violation`
- `claude_client_downstream_write_failed`

### 第三阶段：诊断索引

raw SSE 文件太大，不适合作为主要观察入口。需要生成轻量索引：

- 文件路径。
- request id。
- model。
- status。
- terminal event。
- incomplete reason。
- scanner error。
- raw log truncated。
- output item 类型摘要。
- 是否发现 incomplete function_call。

索引可以由脚本生成，先本地脚本，后续再考虑管理端只读页面。

## 修复路线

### 路线 A：路由与 effort 控制

当前状态：

- `gpt-5.5(high)` + 工具风险请求降到 `gpt-5.5(medium)`。
- `gpt-5.4`、其他模型、非工具 `gpt-5.5(high)` 不受影响。

下一步：

- 统计降级前后 `response.incomplete` 率。
- 按 request 类型分桶：
  - no tools
  - tools available
  - tool_result continuation
  - large tool_result
  - long context
- 如果 `gpt-5.5(medium)` 工具路径稳定，再小范围恢复部分 high 场景做 A/B。

### 路线 B：上游参数验证

需要黑盒验证这些参数是否影响 `response.incomplete`：

- `max_output_tokens` 是否仍被 Codex upstream reject。
- 是否能设置更大的工具参数输出预算。
- `parallel_tool_calls=false` 是否降低半截 tool call。
- `reasoning.summary` 是否影响输出预算。
- 工具 schema 压缩是否降低预算压力。
- 长上下文下 cache / compaction 是否影响 incomplete。

规则：

- 每个参数只做单变量测试。
- 每轮同时跑 `gpt-5.4` 对照。
- 只有 raw SSE 与客户端 JSONL 同时支持，才记录为结论。

### 路线 C：翻译层健壮性

必须保持：

- 不能把 `response.incomplete` 当成功。
- 不能补造半截 tool 参数。
- 不能用普通 assistant 文本冒充工具进度。
- 不能污染 transcript。

可研究：

- 如果 upstream incomplete 发生在纯 text 且没有 tool call，是否可以安全给出 Claude error + partial visibility。
- 如果 downstream 已写出 message_start，如何让 terminal error 对 Claude CLI 更可控。
- 对 `stream-json` 的 partial message 行为做更细校验。

### 路线 D：配置与发布安全

- 默认 Claude -> GPT target family 保持 `gpt-5.4`。
- `gpt-5.5` 仅显式选择。
- `gpt-5.5(high)` 工具路径未稳定前不作为默认。
- 所有生产配置修改由 Taylor 部署确认。
- Codex push main 后只给部署说明，不默认执行生产命令。

## 黑盒验证矩阵

每次改 GPT-5.5 兼容链路，至少跑：

1. **最小文本**
   - `gpt-5.5`
   - `gpt-5.4` 对照
   - text output。

2. **stream-json**
   - `--output-format stream-json --verbose --include-partial-messages`
   - JSONL 全部可 parse。
   - 最终 result 非 error。
   - usage 非零。

3. **同一 PTY 多轮**
   - 记忆写入。
   - 记忆追问。
   - 中文 prompt。
   - 普通工具调用。
   - 工具后继续追问。

4. **大工具输出**
   - 2k 行。
   - 10k 行。
   - 大 `Read`。
   - 大 `Bash`。
   - 大工具后继续一轮。

5. **tool_use 压力**
   - `Read`。
   - `Write`。
   - `Edit`。
   - 多工具串联。
   - tool 参数较长。

6. **长上下文**
   - 50k。
   - 150k。
   - 接近 preflight 阈值但不超限。

7. **失败路径**
   - 人工 mock `response.incomplete`。
   - 人工 mock missing terminal event。
   - 人工 mock scanner error。
   - 确认不会写入成功 transcript。

## 验收指标

短期稳定指标：

- `gpt-5.5` 工具路径 `response.incomplete/max_output_tokens` 显著下降。
- 默认流量不再误走 `gpt-5.5(high)`。
- 所有新错误都有明确分类字段。

中期兼容指标：

- GPT-5.5 medium 工具路径连续黑盒通过。
- GPT-5.5 high 非工具路径连续黑盒通过。
- GPT-5.5 high 工具路径能通过至少一个受控子集。

最终准入：

- GPT-5.5 在主验收矩阵中的失败率接近 GPT-5.4。
- 失败样本能稳定归类，不产生 transcript 污染。
- 至少 3 轮不同日期黑盒回归通过。
- 至少 1 轮线上短期开启 raw SSE 观察无新增高频错误。

## 回滚边界

立即回滚或禁用 GPT-5.5 显式入口：

- 出现大面积 Claude CLI `Failed to parse JSON`。
- 出现半截 tool_use 被当成功写入 transcript。
- `response.incomplete` 率没有下降。
- GPT-5.4 或其他模型受到影响。
- 错误黑盒化泄露内部 GPT target 到用户侧响应。

## 当前下一步

1. 增加结构化错误分类字段，尤其是 route source、tool risk、terminal event。
2. 写 raw SSE 索引脚本，批量统计 `response.incomplete`、scanner error、missing terminal。
3. 设计 GPT-5.5 medium vs high 工具黑盒 A/B。
4. 验证 Codex upstream 是否仍拒绝 `max_output_tokens`。
5. 跑一轮不部署的本地黑盒矩阵，生成可比较报告。
