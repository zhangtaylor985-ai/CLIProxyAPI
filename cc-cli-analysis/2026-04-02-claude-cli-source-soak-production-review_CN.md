# 2026-04-02 Claude CLI 源码 + 同一 PTY Soak + 生产准入复核

## 范围

- 仅关注 `claude cli -> gpt` 兼容链路
- 重点看：
  - `thinking` 历史清洗是否会引入新 bug
  - Claude CLI 工具进度展示是否污染真实 transcript
  - 是否贴近 `Target CC-Parity`

## 本地 Claude CLI 源码关键信号

源码目录：

- `/Users/taylor/sdk/claude-code`

本轮最有价值的约束：

1. `query.ts` 明确写了 thinking trajectory 规则
   - thinking block 只需要在 assistant trajectory 持续期间保留
   - 如果该 assistant turn 包含 `tool_use`，trajectory 才会延续到它后面的 `tool_result` 和再下一条 assistant
2. `query.ts` 明确写了 partial / orphan thinking 的风险
   - fallback 后的 orphan partial messages，尤其 thinking，会因为 signature 非法而被 tombstone
3. `query.ts` 明确写了 signature 是 model-bound
   - protected thinking 不能跨 fallback model 直接 replay
4. `cli/print.ts` / `main.tsx` 明确限制了 `--include-partial-messages`
   - 只允许在 `--print --output-format stream-json` 场景使用
   - 服务端不应伪装成“默认已开启”
5. `ccrClient.ts` / `HybridTransport.ts` 对 `stream_event` 本身就有缓冲和延迟
   - 这意味着“前台静默很久”不一定都是代理层 bug
   - 但也意味着服务端更不该用污染 transcript 的方式强行补体验

## 真实同一 PTY 样本

### 样本 A：改动前真实 soak

命令：

- `claude --dangerously-skip-permissions --debug-file /tmp/claude-pty-soak-20260402-round2.log`

观察：

- 在同一 PTY 内连续两轮追问
- 第一轮触发了 `Glob` / `Bash` / 多次 `Read` / `Grep`
- 第二轮继续触发了 `TaskCreate` / `TaskUpdate` / `Glob` / `Grep`
- 中途出现过一次真实工具错误：
  - `MaxFileReadTokenExceededError`
- 但未观察到：
  - `invalid signature in thinking block`
  - `502 Upstream request failed`

结论：

- 当前链路至少已能承受“同一 PTY + 多轮 + 多工具 + 一个工具错误恢复”的真实样本
- 但仍有明显体验问题：
  - 中间长时间只有工具 / thinking 状态，正文落地偏慢

### 样本 B：改动后 smoke

命令：

- `claude --dangerously-skip-permissions --debug-file /tmp/claude-pty-smoke-after-fix-20260402.log`

观察：

- 第一轮成功读取最新 `sanitizeThinkingHistory` 逻辑并返回答案
- 同一 PTY 内继续发起第二轮追问
- 日志中未出现：
  - `invalid signature`
  - `502`
  - 兼容层协议错误

结论：

- 本轮保守修正没有立即打坏 Claude CLI 真实终端交互

### 样本 C：改动后同一 PTY 长会话 + 中文 web search 边界混合样本

产物：

- `tasks/blackbox-samples/2026-04-02-claude-cli-pty/claude-pty-long-session-20260402.out`
- `tasks/blackbox-samples/2026-04-02-claude-cli-pty/claude-pty-long-session-20260402.debug.log`

会话结构：

1. 本地代码问题：`sanitizeThinkingHistory`
2. 同一 PTY 继续本地代码问题：`codex_claude_response.go`
3. 中文负样本：
   - `请只查看当前仓库...不要联网，不要 web search`
4. 中文正样本：
   - `请帮我搜索一下 OpenAI Codex CLI 最新官方文档更新...`
5. 搜索后继续追问：
   - `现在不要联网，只用刚才那次回答...`

观察：

- 第 1 轮成功返回答案，debug log 出现多次 `Stream started`，未见 `invalid signature` / `502`
- 第 1 轮中途出现一次真实工具参数错误：
  - `Grep tool input error: An unexpected parameter C was provided`
  - 但后续自动恢复并完成回答
- 第 3 轮中文负样本只触发本地 `Grep/Read`，clean transcript 未出现 `Web Search(...)`
- 第 3 轮答案明确指出：
  - `gptinclaude.go:29` 覆盖 `(?:请|帮我|麻烦|去)?搜索(?:一下)?`
  - `gptinclaude.go:30` 额外覆盖 `搜一下|查一下`
- 第 4 轮中文正样本明确触发了：
  - `Web Search("OpenAI CodexCLIlatestofficialdocumentationupdates2026 official Codex CLI docs")`
- 但第 4 轮搜索阶段尾部较长，第 5 轮追问在等待窗口不足时被 Claude CLI 视为 queued message，未形成干净的“搜索已结束后继续追问”样本

结论：

- 改动后同一 PTY 多轮连续追问未打出新的协议硬错误
- 中文 `不要联网 / 不要 web search` 负样本在真实 Claude CLI 下未误触发搜索
- 中文 `请帮我搜索一下` 正样本在真实 Claude CLI 下会触发搜索
- 当前更像“搜索回合长尾 + CLI 排队体验”问题，不是新的服务端协议回归

### 样本 D：中文显式搜索后继续追问

产物：

- `tasks/blackbox-samples/2026-04-02-claude-cli-pty/claude-pty-search-followup-20260402.out`
- `tasks/blackbox-samples/2026-04-02-claude-cli-pty/claude-pty-search-followup-20260402.debug.log`

prompt：

1. `请帮我搜索一下 OpenAI Codex 官方文档首页 URL，只给我 1 条来源并标注日期。只使用 web search，不要 bash、gh 或本地仓库命令。`
2. `现在不要联网，只用刚才那次回答，用一句话复述你给的链接域名。`

观察：

- clean transcript 明确出现：
  - `Web Search("OpenAICodexofficialdocumentation2026officialdocshomepage")`
- 搜索答案成功落地：
  - `OpenAI Codex 官方文档首页 URL：https://developers.openai.com/codex`
  - `日期：2026-04-02`
- 同一 PTY 继续追问成功回答：
  - `链接域名是 developers.openai.com。`
- debug log 未出现：
  - `invalid signature`
  - `thinking block`
  - `502`
- 额外噪音：
  - `NON-FATAL: Lock acquisition failed ... (expected in multi-process scenarios)`
  - 退出阶段 telemetry `No API key available`

结论：

- 已有真实同一 PTY 证据证明：
  - 中文显式 web search 能触发搜索
  - 搜索结束后继续追问仍可正常回答
- 当前未观察到“搜索后历史回放导致会话直接炸掉”的新回归

## 本轮生产判断

### 1. `thinking` 清洗：值得现在修

涉及文件：

- `internal/runtime/executor/claude_executor.go`

原因：

- 之前版本会保留“所有紧邻后续 `tool_result` 的 assistant thinking”
- 这比 Claude 源码里的 trajectory 规则更宽
- 长会话里会额外保留更多 model-bound thinking，反而更容易在后续 replay 中留下低频炸点

本轮采取的收敛方案：

- 只在“最新一条 replay 消息本身就是 user `tool_result`”时
- 保留紧邻前一条 assistant 的 thinking
- 一旦 tool trajectory 已结束、用户开始正常追问，就把更老的 thinking 全部去掉

为什么这更贴近 `Target CC-Parity`：

- 优先减少协议副作用
- 不为了修某个低频签名错误，长期改变更多历史上下文
- 与 Claude 源码里的 trajectory 边界更一致

仍需注意的副作用：

- 如果 Claude 某些未覆盖边界实际上还依赖更长的 thinking replay，这个收紧可能导致新的 400
- 所以后续仍要补：
  - 同一 PTY 多轮 soak
  - `resume` / `continue` 补充样本

### 2. Claude CLI 工具进度文本注入：不值得按当前方式上线

涉及文件：

- `internal/translator/codex/claude/codex_claude_response.go`
- `internal/translator/gptinclaude/gptinclaude.go`

原因：

- 之前版本把 `Reading relevant files.` / `Searching the codebase.` / `Running a verification command.` 作为普通 assistant text block 注入
- 这不是 out-of-band UI 信号，而是会进入真实 transcript 的消息内容
- 它会带来三类副作用：
  - 污染后续会话历史
  - 占 token
  - 误导后续模型对先前行为的理解

为什么不符合 `Target CC-Parity`：

- 目标写的是“过程可见性尽可能真实、连续、不过度伪造”
- Claude CLI 本身已有工具 UI；服务端再写普通 assistant 文本，会让体验更假而不是更真

本轮采取的收敛方案：

- 移除 Claude CLI 路径下的 assistant 文本型工具进度注入

代价：

- 某些长工具阶段会显得更安静

但这仍比继续污染 transcript 更可接受，因为：

- 这是显式的体验损失
- 不是隐蔽的协议风险
- 后续可以在 wrapper / UI 层继续补，而不是在服务端消息历史里造内容

## 本轮没有继续修的点

### 1. Web search 否定词 / 中文意图判断

原因：

- 当前真实样本里：
  - 中文负样本未误触发
  - 中文正样本能稳定触发
- 说明这块暂时没有出现值得立刻继续热修的生产级误判
- 若现在继续收窄，大概率会把“能搜到”改坏成“搜不到”
- 当前更值得修的是搜索长尾与搜索过程可见性，而不是再动 matcher

### 2. `web_search_call added` 可见性

原因：

- 当前 Claude CLI 路径抑制了 `added` 阶段的通用开始文案
- 这减少了噪音，但也可能让“模型中途自主去搜”时前台更安静
- 这一点更适合在更多真实样本后再决定是否改

## 本轮实际改动

- 收窄 `sanitizeThinkingHistory` 保留范围，只保留“最新 `tool_result` 仍在延续的 assistant trajectory”
- 移除 Claude CLI 路径的普通 assistant 工具进度文本注入
- 补充定向单测

## 验证

- `go test ./internal/runtime/executor ./internal/translator/gptinclaude ./internal/translator/codex/claude`
- `go test ./...`
- 真实 Claude CLI：
  - `/tmp/claude-pty-soak-20260402-round2.log`
  - `/tmp/claude-pty-smoke-after-fix-20260402.log`
  - `tasks/blackbox-samples/2026-04-02-claude-cli-pty/claude-pty-long-session-20260402.out`
  - `tasks/blackbox-samples/2026-04-02-claude-cli-pty/claude-pty-long-session-20260402.debug.log`
  - `tasks/blackbox-samples/2026-04-02-claude-cli-pty/claude-pty-search-followup-20260402.out`
  - `tasks/blackbox-samples/2026-04-02-claude-cli-pty/claude-pty-search-followup-20260402.debug.log`

## 当前结论

- 当前分支比之前更接近生产标准，但还不能直接宣称达到 release gate
- 已确认：
  - 低副作用地收紧了 `thinking` replay 边界
  - 去掉了会污染 transcript 的假工具进度
  - 同一 PTY 真实样本里未再出现 `invalid signature` / `502`
  - 中文 web search 负样本未误触发，正样本可触发，且搜索后还能在同一 PTY 继续追问
- 仍未完全收口：
  - CLI 长静默 / 正文偏晚
  - 搜索阶段长尾与 queued message 体验
  - 更长真实会话样本积累
