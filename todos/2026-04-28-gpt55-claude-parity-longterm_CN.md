# 2026-04-28 GPT-5.5 Claude 长期兼容待办

负责人：Codex

## 原则

- 生产部署默认由 Taylor 执行。
- Codex 负责方案、实现、测试、文档、main push。
- 当前 `gpt-5.5(high)` 工具请求降到 `medium` 是 mitigation，不是最终方案。
- 长期目标是让 `gpt-5.5` 兼容性接近 `gpt-5.4`。

## P0 生产边界

- [ ] 后续回复和任务记录中明确：除非 Taylor 显式要求，否则 Codex 不操作生产 systemd。
- [ ] main push 后只给部署说明和验证命令。
- [ ] 若需要生产观察，由 Taylor 部署后提供 request id / raw SSE / journal 时间窗。

## P1 结构化错误分类

- [ ] 为 Claude -> GPT 路由补内部结构化字段：
  - `target_source`
  - `effective_target_model`
  - `reasoning_effort`
  - `has_tools`
  - `has_tool_result_history`
  - `has_tool_use_history`
- [ ] 为 Codex executor 错误补分类：
  - `codex_upstream_response_incomplete`
  - `codex_upstream_missing_terminal_event`
  - `codex_upstream_scanner_error`
- [ ] 确认这些字段只进入内部日志 / 管理端，不泄露给用户侧响应。

## P2 raw SSE 索引工具

- [ ] 写本地脚本扫描 `logs/codex-raw-sse`。
- [ ] 输出 TSV / JSON：
  - request id
  - model
  - upstream status
  - terminal event
  - incomplete reason
  - scanner error
  - raw log truncated
  - 是否发现 incomplete function_call
- [ ] 能按模型和 effort 聚合错误率。
- [ ] 能对比 `gpt-5.4(high)`、`gpt-5.5(high)`、`gpt-5.5(medium)`。

## P3 上游参数黑盒

- [ ] 验证 Codex upstream 是否仍拒绝 `max_output_tokens`。
- [ ] 验证 `parallel_tool_calls=false` 是否降低 incomplete tool call。
- [ ] 验证工具 schema 压缩是否降低输出预算压力。
- [ ] 验证 `reasoning.summary` 设置是否影响 `response.incomplete`。
- [ ] 每个参数单变量测试，并保留 raw SSE 证据。

## P4 黑盒矩阵

- [ ] 最小 text：`gpt-5.5` vs `gpt-5.4`。
- [ ] stream-json：可 parse、result success、usage 非零。
- [ ] 同一 PTY 多轮：记忆、中文、工具、工具后追问。
- [ ] 大工具输出：2k / 10k 行。
- [ ] 长上下文：50k / 150k / 接近 preflight 阈值。
- [ ] 失败注入：incomplete / missing terminal / scanner error。

## P5 当前 mitigation 复核

- [ ] 统计 `gpt-5.5(high)` 工具请求降级后 `response.incomplete` 是否下降。
- [ ] 确认 `gpt-5.4` 和其他模型完全不受影响。
- [ ] 确认非工具 `gpt-5.5(high)` 仍尊重 high。
- [ ] 如果 medium 工具路径稳定，设计小范围 high 工具 A/B。

## P6 验收与 release

- [ ] 连续 3 轮不同日期黑盒矩阵通过后，才考虑阶段 release。
- [ ] release 文档必须包含：
  - 测试矩阵
  - 错误率对比
  - raw SSE 观察
  - 剩余风险
  - 回滚方式
- [ ] 未达到阶段稳定前，不把 GPT-5.5 设为默认 Claude -> GPT target family。
