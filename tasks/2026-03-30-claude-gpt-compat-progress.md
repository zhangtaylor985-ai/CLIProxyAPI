# 2026-03-30 Claude GPT 兼容进度

## 1. 任务背景

目标是让 `Claude Code 客户端 -> GPT/Codex 后端` 的体验尽可能接近原生 `Codex -> GPT`，同时尽量不影响：

- `Codex 客户端 -> GPT/Codex`
- `Claude Code 客户端 -> Claude`

当前记录的是“截至本文件创建时，工作树内未提交代码”的阶段进度。

## 2. 当前未提交代码做了什么

### 2.1 新增 `gpt in claude` 隔离兼容层

新增文件：

- `internal/translator/gptinclaude/gptinclaude.go`

已落地内容：

- 识别 Claude 请求里的内建 `web_search` / Claude Code `WebSearch`
- 把这类工具映射到 Codex 内建 `web_search`
- 对 Claude -> GPT 的搜索请求压低 reasoning effort
- 对搜索请求的目标模型 suffix 做 `medium` 收敛，避免后续 thinking 应用把早期限制冲掉
- 构造早期 synthetic websearch 文本 tag

### 2.2 Claude 请求到 Codex 请求的搜索兼容

涉及文件：

- `internal/translator/codex/claude/codex_claude_request.go`
- `internal/translator/codex/claude/codex_claude_request_test.go`

已做内容：

- 把 Claude 的搜索工具声明翻译成 Codex `web_search`
- 对带内建搜索的 Claude 请求把 `reasoning.effort` 限到 `medium`

### 2.3 Codex 响应到 Claude 响应的 synthetic websearch tag

涉及文件：

- `internal/translator/codex/claude/codex_claude_response.go`
- `internal/translator/codex/claude/codex_claude_response_test.go`

已做内容：

- 在 `response.output_item.added` 阶段，提前注入 `Searching the web.` 和 `<tool_call>`
- 在 `response.output_item.done` 阶段，补 `Searched: ...` 和 `<tool_call>`
- 非流式响应也会把 `web_search_call` 转成文本 block

### 2.4 Claude -> GPT 搜索提示和目标模型收敛

涉及文件：

- `sdk/api/handlers/handlers.go`
- `sdk/api/handlers/handlers_masquerade_prompt_test.go`

已做内容：

- 对带内建搜索的 Claude -> GPT 请求使用更短的 masquerade prompt
- 在 handler 层对搜索请求的目标模型 suffix 做收敛

### 2.5 Codex 故障切换状态归一

涉及文件：

- `sdk/cliproxy/auth/conductor.go`
- `sdk/cliproxy/auth/conductor_overrides_test.go`

保留内容：

- `pool empty` -> `503`
- `Could not parse your authentication token` -> `401`
- `account has been deactivated` -> `403`

说明：

- 共享层的 `executionResultModel` suffix/base canonicalization 已回退，不再保留。

### 2.6 Claude CLI `thinking.signature` 兼容清洗

涉及文件：

- `internal/runtime/executor/claude_executor.go`
- `internal/runtime/executor/claude_executor_test.go`

已做内容：

- 在发往 Claude `/v1/messages` 前，仅移除空字符串或纯空白的 `thinking.signature`
- 不改写有效 signature
- 目标是规避偶发：
  - `Invalid signature in thinking block`

## 3. 解决了什么问题

已明显改善：

- `Claude CLI -> GPT` 的搜索工具反馈不再长时间完全无显示
- `Claude CLI -> GPT` 的搜索请求更容易更早进入 search tool，而不是前置过度思考
- 部分 Codex 授权失败文案可以更稳定地进入冷却/切换逻辑
- `thinking.signature` 空值形态导致的 Claude 上游 400 风险被收敛

## 4. 当前仍存在的问题

### 4.1 还没有达到“Codex 级别体验一致”

实测结论：

- 基础回复已可用
- 搜索结果基本正确
- 复杂分析可完成
- 但端到端时延仍有明显长尾

### 4.2 synthetic websearch tag 还不够稳

已观察到风险：

- 早期 synthetic query 偶发会过度抽取，把额外上下文甚至脚本内容带进 query
- 当前 synthetic tag 还没有按客户端类型分流

### 4.3 VSCode 扩展问题仍未完全收口

已确认 VSCode 与 CLI 是不同问题域，不能混测：

- `claude-cli/...`
- `codex_exec/... vscode/...`

当前需要单独处理的 VSCode 问题：

- websearch tag 展示
- `thinking` / content block type 兼容
- 长时间使用时自动中断或卡住

## 5. 副影响与影响边界

### 5.1 主要影响范围

主要影响：

- `Claude Code 客户端 -> GPT/Codex 后端`

### 5.2 窄影响范围

较小影响：

- `Claude Code 客户端 -> Claude`：
  - 仅新增空 `thinking.signature` 清洗
- `Codex 客户端 -> GPT/Codex`：
  - 保留已验证的失败状态归一

### 5.3 当前已知副影响

- synthetic websearch tag 可能对不同客户端一刀切，不适合作为最终形态
- query 抽取策略过于宽松，可能把多余内容误判为搜索词
- 回归分析脚本最初对日志时间戳的读取不够稳，现已修正

## 6. 已完成验证

- 单测：
  - `go test ./internal/runtime/executor`
  - `go test ./internal/translator/codex/claude`
  - `go test ./sdk/api/handlers`
  - `go test ./sdk/cliproxy/auth`
- 真实 `claude -p` 回归：
  - 基础回复
  - 搜索题
  - 复杂代码分析题
- request log 分析：
  - 首包时间
  - 多轮 API response
  - `web_search_call` 次数
  - `reasoning_summary_text.delta` 次数

## 7. 下一步任务

下一轮任务已明确：

1. 把 websearch synthetic tag 改成按客户端分流，至少拆：
   - `claude-cli`
   - `codex_exec/vscode`
2. 修正 `InferBuiltinWebSearchQuery` 的抽取策略，先收掉 query 过度污染
3. 跑第二轮：
   - CLI 回归
   - VSCode 路径回归

## 8. 本轮继续推进结果

### 8.1 已新增的进展管理与说明

- 新增任务进度文件：
  - `tasks/2026-03-30-claude-gpt-compat-progress.md`
- 更新 `AGENTS.md`：
  - 明确 `tasks/` 用于保存任务进度、阶段结论、回归结果与风险边界

### 8.2 已完成：websearch synthetic tag 按客户端分流

已实现：

- 基于上下文中的请求头识别客户端类型：
  - `claude-cli`
  - `codex_exec/vscode`
  - `unknown`
- synthetic websearch tag 现在只对 `claude-cli` 发出
- `codex_exec/vscode` 不再复用同一套 `Searching the web.` / `<tool_call>` 文本注入

主要落点：

- `internal/translator/gptinclaude/gptinclaude.go`
- `internal/translator/codex/claude/codex_claude_response.go`
- `internal/translator/codex/claude/codex_claude_response_test.go`

### 8.3 已完成：`InferBuiltinWebSearchQuery` 抽取策略收敛

已实现：

- 把多行 query 抽取改为“只取前部有效搜索语句”
- 遇到明显代码/脚本/命令噪音时立即截断
- 对纯代码噪音直接返回空 query
- 限制 query 长度，避免异常大段文本进入 synthetic tag

主要落点：

- `internal/translator/gptinclaude/gptinclaude.go`
- `internal/translator/gptinclaude/gptinclaude_test.go`

### 8.4 第二轮回归结果

已跑：

- 单测：
  - `go test ./internal/translator/gptinclaude`
  - `go test ./internal/translator/codex/claude`
  - `go test ./internal/runtime/executor`
  - `go test ./sdk/api/handlers`
  - `go test ./sdk/cliproxy/auth`
- 协议级流式回归：
  - 独立端口启动当前工作树服务
  - 分别模拟：
    - `claude-cli/2.1.76 (external, sdk-cli)`
    - `codex_exec/0.98.0 ... vscode/1.112.0`

观察结果：

- `claude-cli`：
  - 仍会收到 synthetic websearch tag
  - 早期 query 已不再带入后续脚本内容
  - 只保留首行：
    - `OpenAI Codex app 最新官方信息是什么？给我来源。`
- `codex_exec/vscode`：
  - 不再收到 synthetic `Searching the web.` / `<tool_call>`
  - 当前表现改为正常的 thinking / 后续响应流

## 9. 真实 VSCode 扩展黑盒验证补充

### 9.1 已确认可做真实扩展测试

本机已验证存在真实运行环境：

- VSCode 正在运行
- Claude VSCode 扩展已安装并启动
- 扩展原生日志可读：
  - `~/Library/Application Support/Code/logs/.../exthost/Anthropic.claude-code/Claude VSCode.log`
- 服务端 request log 可读：
  - `.cli-proxy-api/logs/v1-messages-*.log`

### 9.2 真实扩展请求暴露出的新分流边界

通过真实 request log 抓到一条 Claude VSCode 扩展请求：

- `User-Agent: claude-cli/2.1.76 (external, claude-vscode, agent-sdk/0.2.76)`

这说明：

- Claude VSCode 扩展并不总是走 `codex_exec/... vscode/...`
- 之前“只拆 `claude-cli` 和 `codex_exec/vscode`”还不够
- 真实扩展会伪装成 `claude-cli` 主前缀，但带有 `claude-vscode` 标识

### 9.3 已收敛的修正

已把客户端识别继续细化为：

- `claude-cli`
- `claude-vscode`
- `codex_exec/vscode`
- `unknown`

当前策略：

- synthetic websearch tag 只对真实 `claude-cli` 发出
- `claude-vscode` 与 `codex_exec/vscode` 都抑制 `Searching the web.` / `<tool_call>`

### 9.4 当前结论

当前可以明确说：

- “真实 VSCode 扩展测试”可以做，而且已经做到了日志级黑盒验证
- 这轮验证已经发现并修掉了一个仅靠协议模拟看不全的客户端识别问题
- 但还不能宣称 VSCode 扩展所有长期卡住 / 自动中断问题已完全收口，仍需继续做长时回归

### 9.5 针对最新 VSCode websearch 反馈的真实样本回放

用户最新反馈的异常形态是：

- VSCode 扩展里出现 `Searching the web.`
- 并伴随被 skill/system 包装污染的 `<tool_call>`
- 典型污染 query：
  - `读终端命令的输出... ARGUMENTS: web search latest headlines for today`

本轮已使用真实扩展抓包样本继续验证：

- 样本文件：
  - `/tmp/vscode-151047-request.json`
- 样本关键特征：
  - 首条 user message 包含大量 `system-reminder` / skill 列表
  - 后续 assistant 先调用：
    - `Skill`
    - `TodoWrite`
  - 最后一条 user text 为：
    - `ARGUMENTS: web search latest headlines for today`

验证结果：

- 当前 `InferBuiltinWebSearchQuery` 对该真实样本的抽取结果为：
  - `latest headlines for today`
- 回放到当前 `53841` 服务后：
  - 没有再出现 synthetic `Searching the web.`
  - 没有再出现 polluted `<tool_call>`
  - 流上只看到：
    - `message_start`
    - 周期性 `ping`
    - 后续 `thinking`
    - `TodoWrite`

可追溯证据：

- 回放输出：
  - `/tmp/vscode-151047-replay-current.out`
- 服务端日志：
  - `.cli-proxy-api/logs/v1-messages-2026-03-30T161230-944f28b3.log`

当前判断：

- 用户截图里的这类“脏 websearch query / synthetic tag”问题，在当前未提交代码上未复现
- 更可能是旧服务进程、旧代码路径，或修复前的历史行为

### 9.6 VSCode stall 现状

本轮真实 VSCode 请求回放表明：

- 长静默期间已经稳定收到 `event: ping`
- `ping` 间隔约 5 秒
- 这至少可以保证服务端不再完全无事件输出

但当前仍不能宣称 stall 已彻底解决，原因是：

- 现有 `Claude VSCode.log` 中最新 `Streaming stall detected` 记录仍停留在修复前样本
- 还缺一次“真实扩展 UI 发起请求”的新日志来确认扩展是否把 `ping` 计入活跃事件

因此当前对 VSCode stall 的结论应表述为：

- 已显著改善服务端长静默表征
- 尚待真实扩展 UI 日志做最终闭环确认

### 9.7 已完成：VSCode websearch 进度从“后置 fake thinking”切到“真实搜索进度”

在用户最新反馈里，VSCode 扩展的问题已经不只是 raw tag：

- 页面上会在较晚阶段一次性出现大量 `Thinking`
- 文案像是“搜索新闻源 / 考虑新闻源 / 总结新闻源”
- 这些内容更像 Codex 的后置 `reasoning_summary`
- 对终端体验来说是误导性的，因为看起来像“实时思考”，但实际上是搜索完成后才吐出的总结

这一轮已做的修正：

- 对 `claude-vscode` / `codex_exec_vscode`：
  - 不再把 `response.reasoning_summary_*` 映射成 Claude `thinking`
- 改为基于真实 `web_search_call` 事件发出简短进度：
  - `Searching the web for: ...`
- 并在 `response.created` 阶段预发首个 VSCode 搜索进度块

影响边界：

- 仅作用于 VSCode 路径
- `claude-cli` 仍保留现有 synthetic `<tool_call>` / `Searching the web.` 方案

代码落点：

- `internal/translator/gptinclaude/gptinclaude.go`
- `internal/translator/gptinclaude/gptinclaude_test.go`
- `internal/translator/codex/claude/codex_claude_response.go`
- `internal/translator/codex/claude/codex_claude_response_test.go`

已验证：

- 单测通过：
  - `go test ./internal/translator/gptinclaude ./internal/translator/codex/claude`
- 真实 VSCode 请求回放：
  - 请求样本：`/tmp/vscode-162415-request.json`
  - 结果文件：`/tmp/vscode-162415-fixed-timed.out`

本次真实回放观察到：

- 早期仍会先经历一段只有 `ping` 的阶段
- 但一旦进入真实搜索事件，VSCode 侧不再出现长段 fake `Thinking`
- 改为连续出现基于真实查询的短进度块，例如：
  - `Searching the web for: 用 websearch 搜索今天的新闻`
  - `Searching the web for: latest world news today Reuters March 30 2026`
  - `Searching the web for: site:reuters.com/world March 30 2026 Reuters world news`

当前结论更新为：

- “后置 fake thinking”这一展示问题，本轮已明显收敛
- 但 VSCode 扩展的 stall 检测仍未彻底通过，因为扩展显然不把 `ping` 计入有效进展
- 所以 VSCode 体验仍未达到“可直接宣称上线稳定”的标准
