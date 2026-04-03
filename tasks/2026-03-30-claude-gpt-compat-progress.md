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

### 9.8 已完成：Claude CLI 多轮 websearch 去掉重复 generic start，普通 tools 回归通过

用户最新反馈针对的是 `claude-cli` 搜索题：

- 一次“今天的新闻”类问题里会连续出现很多次 `Searching the web.`
- 中间夹着少量 `Searched: ...`
- 整体观感明显比 `codex cli` 更碎，更像协议模拟痕迹，而不是自然的搜索进度

本轮没有调整请求侧 `WebSearch -> web_search` 工具映射，只收敛响应展示层：

- `claude-cli` 保留：
  - `response.created` 阶段的首个早期搜索提示
  - `response.output_item.done` 阶段的真实 `Searched: ...`
- `claude-cli` 去掉：
  - 每个 `response.output_item.added` 上重复发出的 generic `Searching the web.`

这样处理后的目标形态更接近 `codex cli`：

- 只保留一次通用“开始搜索”信号
- 后续主要展示带 query 的真实完成态
- 避免多轮搜索时被一串 generic start 刷屏

本轮额外做了本机侧对比核查：

- 本机 `codex-cli 0.117.0` 安装目录内未检出 `Searching the web` / `<tool_call>` 这类字符串
- 当前项目里这些文案来自 Claude 兼容层的 synthetic 注入逻辑，而不是 Codex 原生 CLI 行为

新增回归覆盖：

- `claude-cli`：
  - `web_search_call added` 默认静默
  - 多轮搜索只保留一次 generic `Searching the web.`
  - 后续 `Searched: query` 仍正常输出
- 普通 tool call：
  - `function_call -> tool_use`
  - arguments delta / done
  - `content_block_stop`
  - 均保持不变
- 非搜索代码分析题：
  - 仍不会在 `response.created` 阶段误发 `Searching the web.`

本轮验证：

- `go test ./internal/translator/gptinclaude ./internal/translator/codex/claude`
- `go test ./internal/translator/codex/...`
- 定向用例通过：
  - `TestConvertCodexResponseToClaude_ClaudeCLIMultiSearchKeepsSingleGenericStart`
  - `TestConvertCodexResponseToClaude_FunctionCallStreamingUnaffected`

影响边界：

- 仅修改 `Claude -> Codex` 兼容路径里 `claude-cli` 的 websearch start 展示
- 未改动原生 `codex cli -> GPT` 路径
- 未改动原生 `claude -> Claude` 路径
- 未改动请求侧搜索工具透传策略

### 9.9 已完成：复杂工具调用任务对齐 Codex，关闭 Claude CLI 的 fake thinking 暴露

本轮目标不是 websearch，而是继续把 `claude-cli -> GPT/Codex` 的复杂工具调用体验向 `codex cli` 收敛。

#### 9.9.1 真实 CLI 黑盒对比方式

使用本机真实安装版本直接对比：

- `codex-cli 0.117.0`
- `claude code 2.1.76`

当前本机真实路由：

- `codex`
  - `~/.codex/config.toml`
  - provider: `syntra`
  - base url: `https://syntra-staging.trustdev.network/v1`
- `claude`
  - `~/.claude/settings.json`
  - base url 指向本机代理 `127.0.0.1:53841`

对比 prompt 采用“复杂但低风险的多工具仓库分析题”：

- 读取 `AGENTS.md`
- 定位：
  - HTTP server 入口
  - Claude -> Codex websearch response 翻译
  - 一个 function/tool call streaming 测试
- 再运行一条最小 translator test 命令

#### 9.9.2 真实黑盒观察结果

`codex exec --json` 的可见事件形态：

- 直接输出结构化事件：
  - `item:agent_message`
  - `item:command_execution started/completed`
- 本次样本统计：
  - `agent_message`: 6
  - `command_execution`: 30 组 started/completed
- 未观察到额外的 reasoning / thinking 事件外露
- 体感上更像：
  - 简短计划
  - 明确工具执行
  - 工具结果
  - 最终答复

`claude -p --output-format stream-json --verbose` 的可见事件形态：

- 首包先输出一个很重的 `system/init`
- 包含：
  - tools 列表
  - skills / plugins / slash commands
  - session metadata
- 在更收敛的工具集合测试里，前 15 秒内未观察到后续有效事件

当前可以明确确认的“兼容层偏离点”：

- `Claude -> Codex` 兼容路径此前仍会把 Codex `response.reasoning_summary_*` 映射成 Claude `thinking`
- 这类内容本质更接近 Codex 的后置 reasoning summary，而不是原生 Claude 的实时 thinking
- 在复杂工具调用题上会表现成：
  - 额外的 `Thinking`
  - 更碎的可见过程
  - 比 `codex cli` 更吵

#### 9.9.3 本轮收敛策略

为把副影响压到最小，本轮没有改 tool_use / function_call / websearch / ping，只改一条映射：

- 对 `claude-cli`：
  - 不再把 Codex `reasoning_summary_*` 转成 Claude `thinking`

同时补齐非流式一致性：

- `ConvertCodexResponseToClaudeNonStream` 也对 `claude-cli` 同步抑制 reasoning block

代码落点：

- `internal/translator/gptinclaude/gptinclaude.go`
- `internal/translator/gptinclaude/gptinclaude_test.go`
- `internal/translator/codex/claude/codex_claude_response.go`
- `internal/translator/codex/claude/codex_claude_response_test.go`

#### 9.9.4 为什么这次改动风险较低

因为只影响下面这个很窄的条件交集：

- 仅 `Claude -> Codex` 兼容路径
- 仅客户端识别为 `claude-cli`
- 仅 Codex `reasoning_summary` 到 Claude `thinking` 的展示映射

明确未改：

- `function_call -> tool_use`
- `response.function_call_arguments.delta/done`
- websearch synthetic text 逻辑
- VSCode 已有抑制策略
- 原生 `codex cli -> GPT`
- 原生 `claude -> Claude`

#### 9.9.5 本轮验证

定向回归：

- `go test ./internal/translator/gptinclaude ./internal/translator/codex/claude`
- `go test ./internal/translator/codex/claude -run 'ReasoningSummarySuppressedForClaudeCLI|ReasoningSummarySuppressedForClaudeVSCode|NonStream_SuppressesReasoningForClaudeCLI|FunctionCallStreamingUnaffected' -v`

全量 translator 回归：

- `go test ./internal/translator/...`

新增/更新用例覆盖：

- `TestShouldSurfaceReasoningSummaryAsThinking`
- `TestConvertCodexResponseToClaude_ReasoningSummarySuppressedForClaudeCLI`
- `TestConvertCodexResponseToClaudeNonStream_SuppressesReasoningForClaudeCLI`
- 继续保留：
  - `TestConvertCodexResponseToClaude_FunctionCallStreamingUnaffected`

当前结论：

- 对复杂工具调用任务，`claude-cli` 的可见行为已进一步向 `codex cli` 收敛
- 最大的“fake thinking”偏离点已收掉
- 在当前测试覆盖下，未观察到对普通 tool streaming 的回归

### 9.10 已完成：Claude CLI 复杂工具任务补步骤级进度，黑盒样本目录落盘

用户进一步指出的真实体验差异不是搜索，而是普通复杂任务：

- `claude-cli` 往往长时间只显示类似：
  - `Searched for 9 patterns, read 4 files`
- 用户会误以为：
  - Claude CLI 更慢
  - 后端没有实时工作
- 对比 `codex cli`，用户更能看到：
  - 正在搜代码
  - 正在读文件
  - 正在跑验证命令

#### 9.10.1 本轮设计原则

继续坚持“副影响尽可能小”：

- 不恢复 fake thinking
- 不改 tool 协议
- 不改 websearch 逻辑
- 不改原生 `codex cli -> GPT`
- 不改原生 `claude -> Claude`

只在 `Claude -> Codex` 兼容路径里，对真实 `claude-cli` 的常见工具类别补非常短的阶段提示。

#### 9.10.2 当前补的进度类型

仅对下面几类高频工具补短文本进度：

- `Grep` / `Glob`
  - `Searching the codebase.`
- `Read`
  - `Reading relevant files.`
- `Bash` / `shell`
  - `Running a verification command.`

并且按“工具类别”去重：

- 连续多个 `Read`
  - 只提示一次 `Reading relevant files.`
- 从 `Read` 切到 `Bash`
  - 再提示一次 `Running a verification command.`

这样做的目的：

- 让 `claude-cli` 更早、更持续地显示“正在做事”
- 但又不把每个 tool call 都展开成噪音
- 更接近 `codex cli` 的步骤级过程感

#### 9.10.3 代码落点

- `internal/translator/gptinclaude/gptinclaude.go`
  - 新增 `BuildClaudeCLIToolProgressText`
- `internal/translator/codex/claude/codex_claude_response.go`
  - 在 `function_call added` 阶段，为 `claude-cli` 注入短文本进度
  - 同时保留原有 `tool_use` 事件

#### 9.10.4 为什么风险仍然可控

因为这次改动依然只命中很窄的交集：

- 仅 `Claude -> Codex` 兼容路径
- 仅 `claude-cli`
- 仅 `function_call added` 阶段
- 仅特定高频工具类别

明确未改：

- `tool_use` 本身
- arguments delta / done
- stop reason
- websearch synthetic tag
- VSCode 路径

#### 9.10.5 本轮验证

定向回归：

- `go test ./internal/translator/gptinclaude ./internal/translator/codex/claude`
- `go test ./internal/translator/codex/claude -run 'ClaudeCLIToolProgressTextEmittedOnCategoryTransition|FunctionCallStreamingUnaffected|ReasoningSummarySuppressedForClaudeCLI' -v`

全量 translator 回归：

- `go test ./internal/translator/...`

新增覆盖：

- `TestBuildClaudeCLIToolProgressText`
- `TestConvertCodexResponseToClaude_ClaudeCLIToolProgressTextEmittedOnCategoryTransition`

结论：

- `claude-cli` 在复杂工具调用题上的“过程可见性”进一步改善
- 当前兼容层策略已从“靠 fake thinking 填空”切到“基于真实 tool call 补短进度”
- 在现有测试下，未观察到对普通 `tool_use` 流程的回归

#### 9.10.6 黑盒样本目录

为后续继续积累真实 CLI 长会话样本，统一归档到：

- `tasks/blackbox-samples/2026-03-30-claude-cli-vscode/`

本轮新增 codex 对比样本：

- `tasks/blackbox-samples/2026-03-30-claude-cli-vscode/codex-cli/2026-03-30-complex-tooling-prompt.txt`
- `tasks/blackbox-samples/2026-03-30-claude-cli-vscode/codex-cli/2026-03-30-complex-tooling-observations_CN.md`

后续新样本统一追加到这个黑盒目录下，避免样本目录分叉。

### 9.11 已完成：真实 Claude CLI 再跑复杂仓库分析题，确认已出现连续阶段提示

前面那批“只见 `init` / timeout / `ConnectionRefused`”样本，需要补一个前置条件澄清：

- 当时 `claude-cli` 配置的 base URL 仍指向 `http://127.0.0.1:53841`
- 但本机 `53841` 端口并未监听
- 因此那批样本不能直接用来判断“兼容层有没有把阶段提示发出来”

本轮先把本地代理重新启动到 `:53841`，再用真实 `claude-cli` 重新做黑盒回归。

#### 9.11.1 本轮真实回归样本

复杂工具调用题 1：

- `tasks/blackbox-samples/2026-03-30-claude-cli-vscode/claude-cli/2026-03-30T223418-complex-analysis-1.stream.jsonl`
- `tasks/blackbox-samples/2026-03-30-claude-cli-vscode/claude-cli/2026-03-30T223418-complex-analysis-1.err`

复杂工具调用题 2：

- `tasks/blackbox-samples/2026-03-30-claude-cli-vscode/claude-cli/2026-03-30T223715-complex-analysis-2.stream.jsonl`
- `tasks/blackbox-samples/2026-03-30-claude-cli-vscode/claude-cli/2026-03-30T223715-complex-analysis-2.err`

普通代码分析题：

- `tasks/blackbox-samples/2026-03-30-claude-cli-vscode/claude-cli/2026-03-30T223727-code-analysis-2.stream.jsonl`
- `tasks/blackbox-samples/2026-03-30-claude-cli-vscode/claude-cli/2026-03-30T223727-code-analysis-2.err`

对应 request logs 已一起归档到：

- `tasks/blackbox-samples/2026-03-30-claude-cli-vscode/claude-cli/request-logs/`

#### 9.11.2 复杂题下看到的真实阶段提示

在真实 `claude-cli` 输出流里，已经能稳定看到短阶段提示，而不再只有最后的聚合摘要：

- `Searching the codebase.`
- `Reading relevant files.`
- `Running a verification command.`

同时还观察到真实 CLI 自己也会穿插更具体的自然语言步骤提示，例如：

- `Reading startup wiring.`
- `Running the narrow test.`

这说明当前体验已不再是“长时间只有一句 `Searched for 9 patterns, read 4 files`”。

#### 9.11.3 普通代码分析题的边界结论

普通题 `看一下当前的项目代码，帮我简单分析一下即可` 的真实样本表现为：

- 会出现 `Searching the codebase.` / `Reading relevant files.`
- 未出现 `Searching the web.`
- stderr 为空
- 任务正常结束并返回最终分析结果

结合本轮 request log 复核：

- 未观察到真实 `web_search_call` 事件
- 说明“普通代码分析题误报 websearch”这一条，在本轮真实 CLI 回归里没有复现

#### 9.11.4 复杂题的稳定性结论

两轮复杂仓库分析题都正常完成：

- 返回码 `rc=0`
- stderr 为空
- 能看到多轮 `tool_use`
- 能看到阶段提示文本
- 能看到最终结果块 `result subtype=success`

因此至少在“本地代理已正常监听”的前提下，本轮 `Claude -> Codex` 兼容路径已经具备：

- 连续阶段提示
- 不误报 websearch
- 普通复杂工具流可正常结束

#### 9.11.5 当前仍保留的风险边界

仍需区分两类问题，不应混为一谈：

1. 本机代理未监听时，`claude-cli` 会表现成连接失败或挂起。
2. 代理正常监听后，兼容层是否能提供足够接近 Codex 的过程可见性。

本轮验证已经覆盖第 2 类，并给出正向结果；此前负向样本主要属于第 1 类。

### 9.12 已完成：真实 Claude CLI 稳定性补跑与否定式 websearch 误判修复

这一轮继续做真实 `claude-cli` 黑盒，目标不是看答案内容，而是区分：

- 会不会突然中断
- 会不会多轮聊着聊着断掉
- 会不会把“不要用 web search”误判成真实搜索

#### 9.12.1 稳定性批次结果

新增样本：

- `tasks/blackbox-samples/2026-03-30-claude-cli-vscode/claude-cli/stability-2026-03-30/2026-03-30T225633-batch-fixed/summary.tsv`
- `tasks/blackbox-samples/2026-03-30-claude-cli-vscode/claude-cli/stability-2026-03-30/2026-03-30T231143-stability-round2/summary.tsv`
- `tasks/blackbox-samples/2026-03-30-claude-cli-vscode/claude-cli/2026-03-30-real-claude-cli-stability-round3_CN.md`

结论：

- 多组独立单轮任务均为：
  - `rc=0`
  - `stderr=0`
  - `result subtype=success`
- 当前没有复现“流在中途突然结束但没有 result”的情况

同时确认：

- 直接重复使用同一个 `--session-id` 会报：
  - `Session ID ... is already in use.`
- 这更像 CLI 会话锁限制，不是代理兼容层异常

#### 9.12.2 真实多轮 `resume` 结论

多轮样本已验证：

- 首轮 `--session-id`
- 后续轮次 `-r/--resume`

可以稳定成功结束。

其中一条样本：

- `resume-negated-turn-1`

耗时达到：

- `463s`

但仍满足：

- `rc=0`
- `stderr=0`
- `has_result_success=1`

因此当前更需要把它归类为：

- 明显长尾时延

而不是：

- 突然中断

#### 9.12.3 新发现并修复的真实问题

在补跑黑盒时，重新观察到一个真实误判：

- 普通代码分析题如果提示词带：
  - `不要用 web search`
- 或多轮提示带：
  - `do not use web search`

真实输出前面仍会出现：

- `Searching the web.`

这说明当前的 built-in websearch 意图识别，把“否定式提到搜索”误判成了“显式要求搜索”。

修复落点：

- `internal/translator/gptinclaude/gptinclaude.go`
- `internal/translator/gptinclaude/gptinclaude_test.go`

修复方式：

- 对常见中英文否定片段做早期拦截
- 保持显式搜索题仍能正常进入 websearch 预进度

#### 9.12.4 修复后黑盒回归结果

新增样本：

- `tasks/blackbox-samples/2026-03-30-claude-cli-vscode/claude-cli/stability-2026-03-30/2026-03-30T232040-negation-fix-check/summary.tsv`
- `tasks/blackbox-samples/2026-03-30-claude-cli-vscode/claude-cli/stability-2026-03-30/2026-03-30T233225-resume-negation-smoke/summary.tsv`

修复后观察到：

- `negated-cn`：
  - `actual_web_progress=0`
- `negated-en`：
  - `actual_web_progress=0`
- `resume-negated-turn-1`：
  - `actual_web_progress=0`
- `resume-negated-turn-2`：
  - `actual_web_progress=0`
- `search-control`：
  - `actual_web_progress=1`

这说明：

- 否定式 prompt 的误报已经在真实 CLI 黑盒里收住
- 显式搜索题没有被一起误伤

#### 9.12.5 本轮验证命令

- `go test ./internal/translator/gptinclaude`
- `go test ./internal/translator/codex/claude`

#### 9.12.6 当前判断

截至本轮：

- 真实 `claude-cli` 在当前代理兼容层上总体稳定，可连续完成单轮和正确 `resume` 多轮
- 尚未复现“聊着聊着突然断了”
- 已修复一个真实生产体验问题：
  - 否定式 websearch 误报
- 当前最需要继续观察的不是突然断流，而是：
  - 首个 `resume` 轮次偶发明显长尾
