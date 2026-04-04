# 项目说明

- 我们项目的源后端：`/Users/taylor/code/tools/CLIProxyAPI-ori`
- 我们项目的源前端：`/Users/taylor/code/tools/Cli-Proxy-API-Management-Center-ori`
- 迁移来源后端：`/Users/taylor/code/tools/CLIProxyAPI`
- 迁移来源前端：`/Users/taylor/code/tools/Cli-Proxy-API-Management-Center`
- 凡是需要查询数据库，默认使用当前仓库 `.env` 中配置的数据库连接；除非任务明确指定其他连接，否则不要自行切换到别的库。
- 所有新的 PG 持久化能力，默认复用 `.env` 里的共享 PG 配置解析逻辑；不要为单个功能再发散出一套独立环境变量。
- 涉及 PG 新表 / 新索引 / 新 schema 变更时，除了运行时兜底初始化，还应补显式 migration 入口到 `scripts/`，上线步骤默认先执行 migration，再重启服务。

# 文档入口

- 本次 Claude -> GPT 黑盒迁移、API Key 策略、用量持久化、模型价格、远程 SQLite 数据迁移说明：
  `docs/claude-gpt-blackbox-migration_CN.md`
- Claude CLI 本地源码分析、真实 PTY soak 结论、生产准入判断：
  `cc-cli-analysis/2026-04-02-claude-cli-source-soak-production-review_CN.md`
- AI Gateway 日志下载、`session_id` 会话归并、中转轨迹格式说明：
  `docs/requirements/ai-gateway-session-trajectory-format_CN.md`

# Claude CLI 兼容补充守则

- Claude CLI 本地源码入口：`/Users/taylor/sdk/claude-code`
- 本地 Claude CLI 实测命令可直接用：`cc1`，其等价于 `CLAUDE_CONFIG_DIR=~/.claude_local claude --dangerously-skip-permissions`；需要做真实客户端验证时，优先直接用它。
- 改 `claude cli -> gpt` 兼容层前，优先对照 Claude 源码里的真实约束，而不是只看黑盒现象：
  - `query.ts` 的 thinking trajectory 规则
  - `query.ts` 对 fallback / orphan partial messages / thinking signature 的处理
  - `cli/print.ts` / `main.tsx` 对 `--include-partial-messages` 的真实限制
- 服务端不要把工具进度伪装成普通 assistant 文本写回 transcript；这类内容会污染后续会话历史。优先依赖 Claude CLI 原生工具 UI，或收敛到 wrapper / out-of-band 展示层。
- `thinking` 清洗策略必须保守且可解释，只按“最新一条 replay 消息是否仍在延续当前 assistant trajectory”判断；不要为了修单点 `invalid signature` 长期保留更多历史 thinking。
- 不要把 `sanitizeThinkingHistory` 再放宽回“凡是后面紧邻过 `tool_result` 的 assistant 都保留 thinking”；这会把更多 model-bound thinking 带回长会话历史，增加低频协议炸点。改这段逻辑时，必须同步看 `internal/runtime/executor/claude_executor_test.go` 的回归用例。
- Claude CLI 路径禁止在真实 `tool_use` 前注入普通 assistant 文本型进度，例如 `Reading relevant files.`；这类文本不是 UI hint，而是真实 transcript 内容。若需要补可见性，只能走 wrapper / UI 层或更严格的 out-of-band 方案。
- 调整中文 `web search` 意图判断或 `web_search_call added` 可见性时，默认分两步做，不要一把同时改 matcher 和展示层；每次改动后都要补中文正样本、负样本、搜索后继续追问的同一 PTY 真实回归。
- `--include-partial-messages` 仍只允许在 `-p --output-format stream-json --verbose` 场景由客户端 wrapper 条件补齐；服务端不得私自默认化。
- Claude CLI 主验收仍以“同一 PTY 连续多轮交互”真实终端样本为准；`resume` / `continue` 只做补充验证。
- AI Gateway session trajectory 当前生产验收口径：
  - 支持同一 TTY 连续对话的稳定记录、查询、导出、token-rounds 聚合。
  - `cc1/claude -p/-c/--resume` 在上游 `provider_session_id` 漂移且请求体不重放历史消息时，不承诺稳定归并；不要把这一路径当成主验收结论。

# 最终目标

- 最终目标名：`Target CC-Parity`
- 目标定义：让 `claude cli` 在当前代理兼容链路上，以“真实终端内同一 PTY 会话连续多轮对话、长时间聊天、复杂工具调用”为主场景时，其稳定性、可继续对话能力、流式过程可见性、错误率与整体体验尽可能接近 `codex` 上的 GPT 体验，并最终达到生产级稳定性。
- 当前优先级：
  - 先保证生产稳定，不再出现 `invalid signature in thinking block`、异常 `502`、否定式 prompt 误触发 web search 等已知硬错误。
  - 再持续收敛 `stream-json` 事件退化、最终正文整段晚吐出、长会话长尾与 CLI / VSCode 展示差异。
- 执行准则：
  - 每次改 `claude cli` / `vscode` 兼容代码前，先写清楚目标、假设、潜在副影响与回滚边界。
  - 每次改动后，至少补齐定向单测，并做真实黑盒回归：优先 `cc` 对 `codex`；`cc2` 仅作为可选外部参考，不再作为主验收基线。
  - Claude CLI 主验收场景是“同一 PTY 会话连续多轮交互”的真实终端长会话，不是 `--resume` 单点恢复。
  - `--resume` / `--continue` 只作为补充恢复链路验证项；即使该链路通过，也不代表达到 `Target CC-Parity`。
  - `--include-partial-messages` 只允许在 `-p --output-format stream-json` 场景由客户端 wrapper 条件补齐；服务端不得擅自伪造为“默认已开启”。
  - 生产变更默认保守，优先选“副作用更小、可解释、可验证”的方案，不接受为了修一个体验问题引入更隐蔽协议风险。
  - `Target CC-Parity` 的详细定义、验收口径、阶段方针见：`target/cc-parity_CN.md`
  - 达到阶段稳定后，再在 `releases/` 记录版本与验收结论；未达标阶段不要硬记 release。

# 任务进度

- `tasks/` 目录用于保存任务进度、阶段结论、未提交改动摘要、回归结果与风险边界。
- 每次新任务开始时，根据任务复杂度和连续性，自行决定是否先查看 `tasks/` 目录以继承上下文，避免重复分析。
- `todos/` 目录用于保存后续待办、未闭环生产问题、待验证项与上线前检查清单。
- 每次阶段工作结束时，如仍有明确未收口事项，应补到 `todos/`，便于后续继续推进与回归。
- `releases/` 目录用于记录已满足阶段验收标准的版本结论、验证范围、剩余风险与回滚说明。
- AI Gateway / 上层 AI 对话轨迹整理默认按 `session_id` 归并完整会话；单次交互落为一个 JSON 文件，格式定义见 `docs/requirements/ai-gateway-session-trajectory-format_CN.md`。
- Session trajectory 的 PG schema 初始化脚本：`go run ./scripts/migrate_session_trajectory_pg`；默认读取当前仓库 `.env` 导出的共享 PG DSN / schema。
