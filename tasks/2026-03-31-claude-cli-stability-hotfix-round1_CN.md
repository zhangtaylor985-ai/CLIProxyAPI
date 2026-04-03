# 2026-03-31 Claude CLI 稳定性热修 Round 1

## 本轮目标

先处理两个生产问题：

1. `invalid signature in thinking block` / 继续追问时偶发 `502 Upstream request failed`
2. `web search` 否定式 prompt 误判与相关硬编码问题

同时补做真实 `cc` 回归，并以 `codex` 作为主参考对照；`cc2` 仅保留为外部参考，不再作为主验收基线。另补一组同一 PTY 会话连续多轮的 Claude CLI 长会话验证。

## 本轮复核到的未提交兼容改动

### 1. `internal/translator/gptinclaude/gptinclaude.go`

已存在的兼容方向包括：

- 客户端识别：`claude-cli` / `claude-vscode` / `codex_exec vscode`
- 抑制 `claude-cli` / `vscode` 的 fake reasoning summary thinking
- 给 `claude-cli` 补简短工具阶段提示

本轮新增热修：

- 去掉原先基于整句片段表的 `web search` 否定式硬编码
- 改成：
  - 显式 query 提取
  - 搜索关键词匹配
  - 邻近否定判断

### 2. `internal/translator/codex/claude/codex_claude_response.go`

当前未提交兼容改动的主线是：

- 对 `claude-cli`：
  - suppress reasoning summary thinking
  - function call 时补 `Searching the codebase.` / `Reading relevant files.` / `Running a verification command.`
  - suppress `web_search_call added` 的重复 generic start，保留更具体的完成提示

### 3. `internal/runtime/executor/claude_executor.go`

本轮把原来的：

- `stripEmptyThinkingSignatures`

改成：

- `sanitizeThinkingHistory`

策略变为：

- 对非 `tool_result` 邻接的历史 assistant `thinking` block，直接从回放历史中剥离
- 对紧邻 `tool_result` 的 assistant turn，保留 `thinking`，但去掉空白 signature

目标是避免继续对话时把不再必要、又可能无效的历史 `thinking.signature` 转发给 Anthropic。

## 副影响评估

### 已确认可接受的副影响

- 普通历史回放中，旧的 assistant `thinking` 不再继续上送
- 这会减少“延续思维痕迹”，但能显著降低 resume/continue 时的 signature 校验风险

### 当前仍需关注的副影响

- 对 `web_search_call added` 的 suppress 会让某些隐式搜索场景少掉一条泛化的 `Searching the web.` 提示
- 但这比在 CLI 中重复刷屏、或误触发否定式 prompt 更可控

## 真实黑盒回归

### 定向单测

已通过：

- `go test ./internal/translator/gptinclaude ./internal/runtime/executor`
- `go test ./internal/translator/codex/claude`

### 本地 `cc` 回归

样本：

- `tmp/cc-fix-20260331/cc-turn1.stream.jsonl`
- `tmp/cc-fix-20260331/cc-resume-local-turn1.stream.jsonl`
- `tmp/cc-fix-20260331/cc-resume-local-turn2.stream.jsonl`
- `tmp/cc-fix-20260331/retest/cc-default.stream.jsonl`
- `tmp/cc-fix-20260331/retest/cc-partial.stream.jsonl`
- `tmp/cc-fix-20260331/retest/cc-alias-after.stream.jsonl`

观察：

- 单轮普通代码分析题不再误触发 `Searching the web.`
- 本轮真实 `resume` 样本里，尚未复现 `invalid signature in thinking block`
- 也未出现对应的 `502 Upstream request failed`
- 同一 prompt 下：
  - 默认 `stream-json` 只有聚合后的 `assistant/user/result`
  - 加 `--include-partial-messages` 后立即恢复大量 `stream_event content_block_delta`
- 已在本机 `~/.zshrc` 把 `cc` 从危险 alias 改为安全 shell function：
  - 只有在 `-p/--print` 且 `--output-format stream-json` 时，才自动追加 `--include-partial-messages`
  - 交互式 `cc` 保持原样，不再因为该参数直接报错退出

### 远端 `cc2` 对照

样本：

- `tmp/cc-fix-20260331/cc2-turn1.stream.jsonl`
- `tmp/cc-fix-20260331/cc2-short-turn1.stream.jsonl`
- `tmp/cc-fix-20260331/cc2-short-turn2.stream.jsonl`
- `tmp/cc-fix-20260331/retest/cc2-default.stream.jsonl`
- `tmp/cc-fix-20260331/retest/cc2-partial.stream.jsonl`
- `tmp/cc-fix-20260331/retest/cc2-alias-after.stream.jsonl`

观察：

- 在本轮有限样本里，旧链路也没有稳定复现 `invalid signature`
- 说明这个问题目前更像低频长会话问题，而不是所有 `resume` 必现
- 远端 `cc2` 与本地 `cc` 一致：
  - 默认 `stream-json` 聚合
  - `--include-partial-messages` 后恢复 `stream_event`
- 已在本机 `~/.zshrc` 把 `cc2` 同步改为安全 shell function：
  - 只有在 `-p/--print` 且 `--output-format stream-json` 时，才自动追加 `--include-partial-messages`
  - 该命令保留为外部参考入口，不再作为主流程验收基线

### `cc` vs `codex` 复杂场景对照

样本：

- `tmp/cc-codex-compare-20260331/cc-complex.stream.jsonl`
- `tmp/cc-codex-compare-20260331/codex-complex.jsonl`

观察：

- `cc` 复杂单轮样本最终成功，结果字段显示：
  - `duration_ms=524978`
  - `duration_api_ms=520429`
  - `num_turns=20`
- 对同题 `codex` 结果文件的创建与最终修改时间为：
  - `2026-03-31 16:19:38` -> `2026-03-31 16:33:04`
  - 粗略 wall time 约 `806s`
- 两者都完成了目标问题并能定位到：
  - HTTP server 入口
  - `Claude <- Codex` 流式翻译器
  - function/tool call streaming 测试
- 本轮样本里，`cc` 的最终 wall time 并不比 `codex` 更差；但 `cc` 的可见过程更依赖 CLI 输出模式，`codex` 的结构化轨迹天然更稳定、更易复盘

### 同一 PTY 会话连续多轮长会话验证

样本：

- `tmp/cc-long-session-20260331.typescript`

观察：

- 已确认可以在同一个真实交互 `cc` 会话里持续发送新问题，不必每轮都走 `resume`
- TUI 场景下，程序化发送文本后需要显式补一次回车提交；提交后会在同一上下文里继续读取文件、搜索、推进任务
- 该方式更接近真实使用，也更适合复现：
  - 长会话长尾
  - 复杂命令调用稳定性
  - 低频 continue/resume 相关异常

## 关键发现

### 1. `thinking signature` 热修方向成立

虽然本轮没有在黑盒中稳定复现旧报错，但：

- 单测已覆盖上游请求体清洗
- 本地 `cc` 真实 `resume` 已连续通过样本验证
- 现有策略比“只删空 signature”更接近 Anthropic 对历史 thinking 的要求边界

### 2. “最后正文整段吐出”根因已进一步收敛

当前样本：

- `tmp/cc-fix-20260331/cc-turn1.stream.jsonl`
- `tmp/cc-fix-20260331/cc2-turn1.stream.jsonl`
- `tmp/cc-fix-20260331/retest/cc-default.stream.jsonl`
- `tmp/cc-fix-20260331/retest/cc-partial.stream.jsonl`
- `tmp/cc-fix-20260331/retest/cc2-default.stream.jsonl`
- `tmp/cc-fix-20260331/retest/cc2-partial.stream.jsonl`

默认模式类型统计主要是：

- `system`
- `assistant`
- `user`
- `result`

但在同一服务、同一 prompt、同一 CLI 版本下，只要加上：

- `--include-partial-messages`

就会立即恢复大量：

- `stream_event`
- `content_block_delta`
- `text_delta`

这说明“最后正文整段吐出”至少主要不是代理没流式，而是 Claude CLI 在 `stream-json` 下默认不输出 partial message。

### 3. 这不是本地未提交代码单独引入的问题

对照结论：

- `cc` 与 `cc2` 当前都表现为聚合消息流
- 同一 `claude_code_version=2.1.76` 的历史样本里，曾经存在大量 `stream_event`
- 本轮新增对照里，`cc` / `cc2` 都可被 `--include-partial-messages` 立即恢复

可直接对比：

- 当前：`tmp/cc-fix-20260331/cc2-turn1.stream.jsonl`
- 历史：`tasks/blackbox-samples/2026-03-30-claude-cli-vscode/claude-cli/2026-03-30T223727-code-analysis-2.stream.jsonl`

因此更新后的更合理结论是：

- 这不是本轮代理热修造成的新增副作用
- 对 `cc` 命令本身，最小副作用修法是客户端 wrapper 在 print + stream-json 场景条件补上 `--include-partial-messages`
- 若用户直接调用裸 `claude -p --output-format stream-json`，仍可能看到聚合输出
- 服务端不能把这个行为当成“默认开启”的协议能力，因为客户端请求里并没有可靠信号说明应强制改写 CLI 呈现模式

## 当前结论

### 已收口

- 否定式 `web search` 误判的硬编码已替换为更通用规则
- `thinking signature` 请求体清洗已从“只删空签名”升级为“剥离非必要历史 thinking”
- 本机 `cc` wrapper 已修到仅在 print + stream-json 场景默认输出 partial message，且不破坏交互式 `cc`
- 已确认真实 Claude CLI 可通过同一 PTY 会话做连续多轮长会话测试，不必只依赖 `resume`

### 提供日志样本结论

- `tmp/64cedef6-7a29-45e3-8d2f-c2a69d10c2e4.jsonl` 里的 `502 Upstream request failed` 只是外层表现
- 真实上游根因是：
  - `API Error: 400 ... Invalid signature in thinking block`
- 该样本里还能直接看到历史 assistant `thinking.signature` 为空字符串
- 当前未提交热修对这个样本形态已具备针对性：
  - 剥离非必要历史 `thinking`
  - 去掉空白 `signature`
- 但若未来出现“非空但已失效”的签名，仍需继续积累样本验证

### 未收口

- `resume` 首轮长尾
- 旧链路上的低频 `invalid signature` 还需继续积累复现样本
- 裸 `claude` 命令若不带 `--include-partial-messages`，仍会保留聚合输出这一 CLI 自身边界
