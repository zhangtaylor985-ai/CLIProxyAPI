# 项目说明

- 我们项目的源后端：`/Users/taylor/code/tools/CLIProxyAPI-ori`
- 我们项目的源前端：`/Users/taylor/code/tools/Cli-Proxy-API-Management-Center-ori`
- 迁移来源后端：`/Users/taylor/code/tools/CLIProxyAPI`
- 迁移来源前端：`/Users/taylor/code/tools/Cli-Proxy-API-Management-Center`
- 凡是需要查询数据库，默认使用当前仓库 `.env` 中配置的数据库连接；除非任务明确指定其他连接，否则不要自行切换到别的库。
- 所有新的 PG 持久化能力，默认复用 `.env` 里的共享 PG 配置解析逻辑；不要为单个功能再发散出一套独立环境变量。
- 管理端认证密码默认从当前仓库 `.env` 读取 `MANAGEMENT_PASSWORD`；不要在文档里再展开明文。
- 黑盒测试若需管理端用户名密码，直接读取当前仓库 `.env` 中的 `MANAGEMENT_TEST_ADMIN_*` / `MANAGEMENT_TEST_STAFF_*`；不要自行改数据库密码。
- 涉及 PG 新表 / 新索引 / 新 schema 变更时，除了运行时兜底初始化，还应补显式 migration 入口到 `scripts/`，上线步骤默认先执行 migration，再重启服务。

# Git 工作流

- 新需求默认使用独立 Git worktree 承载，并在该 worktree 内创建 `codex/<任务名>` feature branch；只有很小的只读检查或用户明确要求在当前目录处理时，才直接使用当前工作区。
- 创建新 worktree 前先检查当前仓库状态与现有 worktree，选择清晰目录名，避免复用已有任务目录；完成后在对应 worktree 内提交、测试、合并或推送。
- push 到 `main` 前，必须先 `git fetch origin main` 并确认本地是否落后；如落后，先在最新 `origin/main` 基底上 rebase / pull 并完成必要测试，再 push。
- 当前仓库经常存在未提交和未跟踪的并行工作；push / pull / rebase 前必须保护现场，可用 `git stash push -u` 或临时 worktree。只 stage 本次任务相关文件，不要覆盖、删除、丢弃或顺手提交无关改动。
- 若 `stash apply` 因远端新增同名文件导致 untracked 文件无法恢复，必须逐个比较 stash 内容与当前文件；确认内容一致后才可删除临时 stash，否则保留 stash 并向用户说明冲突文件。

# 部署与 systemd

- 当前这套 `/root/cliapp/CLIProxyAPI` 线上服务由 systemd 管理，unit 名称是 `cliproxyapi.service`。
- 当前已确认的 unit 配置要点：
  - `WorkingDirectory=/root/cliapp/CLIProxyAPI`
  - `EnvironmentFile=/root/cliapp/CLIProxyAPI/.env`
  - `ExecStart=/root/cliapp/CLIProxyAPI/bin/cliproxyapi -config /root/cliapp/CLIProxyAPI/config.yaml`
- 如果当前仓库 `.env` 中 `IS_PROD=true`，默认按线上环境处理；涉及重启/发布/确认运行状态时，优先提示并使用这套 systemd 管理方式，不要假设是手工前台启动。
- 线上重启命令：
  - `sudo systemctl restart cliproxyapi && sleep 2 && systemctl status cliproxyapi --no-pager -l`
- 查看 unit 原始配置：
  - `systemctl cat cliproxyapi`
- 若文档记录与线上实例不一致，以 `systemctl cat cliproxyapi` 的当前输出为准，并同步回写仓库文档。
- 做了代码修改但要让线上生效时，先确认 `bin/cliproxyapi` 已重编译为最新二进制，再执行 systemd 重启；不要只重启旧二进制。

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


<claude-mem-context>
# Memory Context

# [CLIProxyAPI-ori] recent context, 2026-04-24 10:21am GMT+8

Legend: 🎯session 🔴bugfix 🟣feature 🔄refactor ✅change 🔵discovery ⚖️decision
Format: ID TIME TYPE TITLE
Fetch details: get_observations([IDs]) | Search: mem-search skill

Stats: 50 obs (21,089t read) | 1,491,595t work | 99% savings

### Apr 22, 2026
S41 Local Memory Hit Liveness Probe — "Reply with exactly LOCAL_HIT" (Apr 22 at 5:56 PM)
S80 User Requested Bug Registry + Daily Cron for Claude Code "Failed to parse JSON" Debugging Workflow (Apr 22 at 6:31 PM)
S93 Daily TTY Health Check — Claude Code cc1 session responded DAILY_TTY_OK to ping (Apr 22 at 8:42 PM)
S101 Daily TTY Regression Probe: cc1 Session Responded DAILY_TTY_OK (Apr 22 at 9:05 PM)
S143 Daily TTY Regression Liveness Probe — cc1 Session Responded DAILY_TTY_OK (Apr 22 at 9:11 PM)
227 9:34p 🔵 Smoke Test Still Hangs: "bypass permissions on" Pattern Broken by ANSI Escape Codes Between Words
228 9:36p ⚖️ TTY Regression Scripts Excluded from Stable Baseline; Docs Retained
247 9:47p 🔵 Exa Code Context Tool Unavailable: Credits Limit Exceeded
249 9:48p 🔵 cc1 Marketplace Cache Miss Root Cause: Stale /Users/zhangbo Path in Plugin Config
250 9:50p 🔴 cc1_tty_regression.expect: Startup Detection Rewritten to ANSI-Safe + Debug-Log Verified
251 " 🔴 run_cc1_daily_regression.sh: awk Filter Direction Fixed + Hook Stop Threshold Raised to 3
S158 Inspect working tree changes and run 3 specific Go translator tests in CLIProxyAPI-ori (Apr 22 at 10:05 PM)
255 10:11p 🔵 CLIProxyAPI-ori Working Tree State: Doc Updates + Untracked Regression Scripts
S159 Daily TTY Regression Liveness Probe — cc1 Session Responded DAILY_TTY_OK (Apr 22 at 10:12 PM)
256 10:17p 🔵 Daily TTY Regression Liveness Probe — cc1 Session Responded DAILY_TTY_OK
S161 CLIProxyAPI Codex/Claude Translator: All 3 Regression Tests Pass (Apr 22 at 10:18 PM)
258 10:19p 🔵 CLIProxyAPI Codex/Claude Translator: All 3 Regression Tests Pass
S196 Daily TTY health check + initiate Claude translator test run in CLIProxyAPI-ori (Apr 22 at 10:19 PM)
263 11:42p 🔵 API Key `created_at` Storage Architecture in CLIProxyAPI-ori
264 11:43p ✅ API Key `created_at` Updated to 2026-03-24 in PostgreSQL
### Apr 23, 2026
368 10:33a 🔵 cc1 Daily Regression Script: Structure, Prompts, and PASS/FAIL Criteria
369 " 🔵 cc1-tty-blackbox-testing Skill: Critical Pitfalls for TTY Evidence Interpretation
370 " 🔵 CLIProxyAPI-ori Repo: Untracked Regression Infrastructure Files Not Yet Committed
372 10:34a ✅ cc1 Daily Regression Run 2026-04-23T103302 Started Successfully
374 10:35a 🔵 cc1 Regression Run 2026-04-23T103302: Prompt 1 Passed, Prompt 2 In Progress
376 10:36a 🔵 Expect Driver (Session 67459) Exited While cc1 Process Still Running — Possible Premature TTY Handoff
377 10:38a 🔵 cc1 Regression 2026-04-23T103302: Prompt 2 Complete, Prompt 3 Now Streaming — No Errors Detected
S199 Daily TTY check + CLIProxyAPI-ori Claude translator regression test run (Apr 23 at 10:39 AM)
378 10:39a ✅ cc1 Daily Regression 2026-04-23T103302: PASS — All 5 Target Error Classes Absent, Automation Memory Written
381 4:54p 🔵 JSON Parse Error Multi-Root-Cause Analysis: Production Users Still Reporting After Partial Fixes
382 4:56p 🔵 Production User Session 582c04dc: 4 Synthetic Parse Errors Pinpointed to Large Parallel Context Moments
383 " 🔵 Production PostgreSQL: session_trajectory Tables Confirmed Live in cliproxy_business DB
386 5:03p ✅ cc1 (claude2) Settings: claude-mem and Hooks Removed from ~/.claude_local/settings.json
387 5:06p ✅ cc1 (claude2) ~/.claude_local/settings.json: claude-mem Removed, No Hooks Were Present
388 5:11p 🟣 CLIProxyAPI Session Recording: Global and Per-Key Disable Toggles
390 5:12p 🔵 CLIProxyAPI Session Trajectory Toggle Architecture: Global and Per-Key Extension Points
394 5:16p ⚖️ CLIProxyAPI Session Recording: Two-Level Disable Toggle Architecture
395 " 🟣 CLIProxyAPI Per-API-Key Session Trajectory Disable: Backend + Frontend Types
448 5:29p ⚖️ CLIProxyAPI: Production Regression Test Initiated for Per-API-Key Session Trajectory Disable
452 5:30p 🔵 CLIProxyAPI: JSON Parse Errors Recurring in Production Despite Prior Fix
453 5:31p 🔴 CLIProxyAPI: Fix "avoid rewriting committed claude stream status" Landed on origin/main
454 " 🟣 CLIProxyAPI Management Center: Session Trajectory Toggle UI Added to Policy Editor
455 " 🟣 CLIProxyAPI: SessionTrajectoryDisabled Added to Paginated List Lite View (api_key_records_list.go)
456 " ✅ CLIProxyAPI config.example.yaml: session-trajectory-disabled Documented
457 " 🟣 CLIProxyAPI Production Regression Passed: Per-API-Key Session Trajectory Disable Feature Production-Ready
458 5:33p 🟣 CLIProxyAPI: End-to-End Finalize() Test Added for Per-API-Key Session Trajectory Disable
459 " 🔴 CLIProxyAPI: Mid-Stream Status Override Causing JSON Parse Errors — Root Cause and Fix Confirmed
460 " 🔵 CLIProxyAPI: OpenAI/Gemini Handlers Still Lack c.Writer.Written() Guard in WriteTerminalError
461 5:34p 🔵 CLIProxyAPI: Full Test Suite Passes on Commit d432bbef — Fix Verified Green
463 5:36p 🔵 CLIProxyAPI: SessionTrajectoryDisabled Persists via policy_json JSONB Column — No Schema Migration Required
466 5:40p 🟣 CLIProxyAPI: New Skill `cliproxyapi-production-regression` Created as Production Release Decision Framework
473 5:47p ✅ CLIProxyAPI: SESSION_TRAJECTORY_PG_DSN Configured in Local .env
476 5:49p 🔵 CLIProxyAPI Local Dev: No Native Postgres — Docker via OrbStack Required
477 " 🟣 CLIProxyAPI Local Dev: Docker Container cliproxy-postgres-local Created with postgres:16
479 5:51p 🔵 CLIProxyAPI Local Dev: Port 5432 Already Occupied by docker-db-1 (postgres:17.6)
480 " 🟣 CLIProxyAPI: Local cliproxy_session_live Database Created with Full Schema
481 " ✅ CLIProxyAPI Triage Skill: DSN Fallback Chain and Schema-Variable psql Queries
517 8:38p 🔵 CLIProxyAPI Billing Time Zone: Daily Limits Use UTC+8, Storage Timestamps Use UTC
518 8:39p 🔵 CLIProxyAPI Billing Time Zone Full Audit: All Quota Enforcement Uses UTC+8 Day Boundaries
532 8:46p 🔵 CLIProxyAPI: JSON Parse Errors Still Occurring in Production Despite Prior Fix

Access 1492k tokens of past work via get_observations([IDs]) or mem-search skill.
</claude-mem-context>
