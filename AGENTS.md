# 项目说明

- 我们项目的源后端：`/Users/taylor/code/tools/CLIProxyAPI-ori`
- 我们项目的源前端：`/Users/taylor/code/tools/Cli-Proxy-API-Management-Center-ori`
- 管理端 UI 工作只改源前端；不要把后端内置 `/management.html` 当成主程序管理前端。
- 生产管理端前端入口是主程序 VPS `204.168.245.138` 上的 `/root/cliapp/Cli-Proxy-API-Management-Center`，由该目录内独立 Caddy 进程在 `127.0.0.1:5173` 服务 `dist/index.html`，系统 Caddy `/etc/caddy/Caddyfile` 的 `admin.claudepool.com` 再反代到 `127.0.0.1:5173`。
- `/root/cliapp/CLIProxyAPI/static/management.html` 是后端内置旧管理页资产，不是主程序管理前端；生产域名上的 `/management.html` 应重定向到 `admin.claudepool.com` 新前端，避免看到旧 UI。
- 迁移来源后端：`/Users/taylor/code/tools/CLIProxyAPI`
- 迁移来源前端：`/Users/taylor/code/tools/Cli-Proxy-API-Management-Center`
- 凡是需要查询数据库，默认使用当前仓库 `.env` 中配置的数据库连接；除非任务明确指定其他连接，否则不要自行切换到别的库。
- 所有新的 PG 持久化能力，默认复用 `.env` 里的共享 PG 配置解析逻辑；不要为单个功能再发散出一套独立环境变量。
- 管理端认证密码默认从当前仓库 `.env` 读取 `MANAGEMENT_PASSWORD`；不要在文档里再展开明文。
- 黑盒测试若需管理端用户名密码，直接读取当前仓库 `.env` 中的 `MANAGEMENT_TEST_ADMIN_*` / `MANAGEMENT_TEST_STAFF_*`；不要自行改数据库密码。
- 涉及 PG 新表 / 新索引 / 新 schema 变更时，除了运行时兜底初始化，还应补显式 migration 入口到 `scripts/`，上线步骤默认先执行 migration，再重启服务。

# 用户侧黑盒守则

- 面向普通用户、客户自助查询页、公开查询接口和客户端错误响应时，必须对内部 GPT / Codex 路由、目标模型、供应账号身份和上游凭据细节黑盒；不得返回或展示 `gpt-*`、`chatgpt-*`、内部 target family、model routing、fallback target、provider model usage、credential source、`Codex Provider API`、`auth file`、`auth_index`、供应账号邮箱等信息。
- `usage` / 客户自助用量查询 / API Key Insights 与 Claude Code API（含 `/v1/messages`、流式事件、count_tokens、客户端错误）全部按用户侧处理；只有 `Admin` / `/v0/management/*` 管理端才允许展示内部模型、路由、账号与运维明细。
- 用户侧接口只返回用户需要理解的聚合账单、额度、Token、状态与趋势；模型级拆分、路由策略、真实上游模型名只允许出现在已认证的管理端运维界面中。
- 新增或修改用户侧 API / UI 前，先检查响应 JSON、错误文本、前端表格和浏览器 Network 面板，确认没有泄露内部 GPT 模型、Codex / provider / auth file 文案或路由细节。

# Git 工作流

- 新需求默认使用独立 Git worktree 承载，并在该 worktree 内创建 `codex/<任务名>` feature branch；只有很小的只读检查或用户明确要求在当前目录处理时，才直接使用当前工作区。
- 创建新 worktree 前先检查当前仓库状态与现有 worktree，选择清晰目录名，避免复用已有任务目录；完成后在对应 worktree 内提交、测试、合并或推送。
- 新任务优先从最新 `origin/main` 新建干净 worktree：先 `git fetch origin main`，再基于 `origin/main` 创建 `codex/<任务名>` 分支；不要默认复用已经完成任务的旧 worktree。
- 如果必须回到旧任务 worktree 继续做新工作，先同步主线再改代码：`git status` 确认现场，必要时 `git stash push -u`，然后 `git fetch origin main && git rebase origin/main`；优先用 rebase 保持 feature branch 线性，只有明确需要保留合并历史时才 merge。
- push 到 `main` 前，必须先 `git fetch origin main` 并确认本地是否落后；如落后，先在最新 `origin/main` 基底上 rebase / pull 并完成必要测试，再 push。
- 当前仓库经常存在未提交和未跟踪的并行工作；push / pull / rebase 前必须保护现场，可用 `git stash push -u` 或临时 worktree。只 stage 本次任务相关文件，不要覆盖、删除、丢弃或顺手提交无关改动。
- 当用户明确要求“合并到 main / 发 main / 上主线 / 可以 push 主线”时，默认由助手自动完成：先在干净 worktree 中 `git fetch origin main`，确认主线无落后或先 rebase / merge 到最新主线，完成必要测试后再 push `main`。只有出现冲突、高风险部署窗口、测试失败或用户明确要求手动合并时，才停下来让用户处理。
- 默认不要把“push feature branch”理解成“已经进入主线”；如果只是为了备份或开 PR，可先推 feature branch。若用户目标是线上修复或主线生效，应继续合并并 push `main`，再按需部署。
- 后端与管理端前端默认作为同一发布单元处理。用户要求“合并到 main / 推主线 / 上线”时，必须同时检查后端 `/Users/taylor/code/tools/CLIProxyAPI-ori` 与前端 `/Users/taylor/code/tools/Cli-Proxy-API-Management-Center-ori`；本次任务涉及两边时，两边都必须完成 main 合并、测试、push 与按需部署后才算完成。标准流程优先使用项目 skill：`cliproxyapi-mainline-release-flow`。
- 若 `stash apply` 因远端新增同名文件导致 untracked 文件无法恢复，必须逐个比较 stash 内容与当前文件；确认内容一致后才可删除临时 stash，否则保留 stash 并向用户说明冲突文件。
- 在临时 worktree 推送到 `main` 后，原始目录不会自动更新；回到 `/Users/taylor/code/tools/CLIProxyAPI-ori` 前，先保护该目录未提交现场，再 `git fetch origin main && git pull --ff-only` 或等价安全流程同步。
- 一个完整任务闭环默认包含：新建/同步 worktree、记录计划或进度、实现、定向测试、必要黑盒、全量回归、fetch/rebase 到最新主线、只 stage 本任务文件、提交、push、回到主目录同步、按需部署、观察、最后清理临时 worktree。
- 临时 worktree 完成任务并确认生产发布与观察无回滚需求后再删除；删除前确认其中没有唯一的 debug 证据、未提交补丁或需保留 artifacts。清理可用 `git worktree remove <path>`，必要时再 `git worktree prune`。

# 部署与 systemd

- 当前这套 `/root/cliapp/CLIProxyAPI` 线上服务由 systemd 管理，unit 名称是 `cliproxyapi.service`。
- 当前已确认的 unit 配置要点：
  - `WorkingDirectory=/root/cliapp/CLIProxyAPI`
  - `EnvironmentFile=/root/cliapp/CLIProxyAPI/.env`
  - `ExecStart=/root/cliapp/CLIProxyAPI/bin/cliproxyapi -config /root/cliapp/CLIProxyAPI/config.yaml`
- 判断当前环境时，优先读取当前后端仓库 `.env` 的 `IS_PROD`：`IS_PROD=0` / `false` / 空值默认视为本地环境；只有 `IS_PROD=1` / `true` / `prod` / `production` 才按生产环境处理。
- 当 `.env` 判定为本地环境时，只做本地上线前回归、构建、测试和本地 smoke；不要自动执行线上 SSH、systemd 重启、生产部署或生产配置改动。
- 当 `.env` 判定为生产环境时，涉及重启/发布/确认运行状态，优先提示并使用这套 systemd 管理方式，不要假设是手工前台启动。
- 生产代码发布必须走 GitHub 主线流程：本地完成修改、测试、commit、`git fetch origin main`、确认不落后、push 到 `origin/main` 后，再登录线上服务器 `git fetch && git pull --ff-only`，由线上目录重新构建并重启服务。即使用户要求“直接改线上”或线上故障需要快速恢复，也不得用 `scp`、`rsync`、远程编辑器或 heredoc 直接覆盖线上源码作为默认发布方式；除非用户明确批准一次性应急热修，否则必须拒绝直接覆盖并改走 GitHub 发布流程。若发生经批准的应急热修，必须立刻补齐同内容的 commit/push，再让线上仓库 fast-forward 到该提交，保证 git HEAD、源码和运行二进制一致。
- 线上重启命令：
  - `sudo systemctl restart cliproxyapi && sleep 2 && systemctl status cliproxyapi --no-pager -l`
- 查看 unit 原始配置：
  - `systemctl cat cliproxyapi`
- 若文档记录与线上实例不一致，以 `systemctl cat cliproxyapi` 的当前输出为准，并同步回写仓库文档。
- 做了代码修改但要让线上生效时，先确认 `bin/cliproxyapi` 已重编译为最新二进制，再执行 systemd 重启；不要只重启旧二进制。

# Codex Worker 隔离部署记录

- 2026-05-11 已将 Codex 文件型 auth 从主程序拆到 worker VPS：主程序 `204.168.245.138`，worker VPS `178.105.98.15`。
- SSH 入口：主程序服务器 `ssh root@204.168.245.138`；worker 服务器 `ssh root@178.105.98.15`。
- 主程序错误日志入口：优先 `journalctl -u cliproxyapi --since '1 hour ago' --no-pager`；落盘错误样本在 `/root/cliapp/CLIProxyAPI/logs/error-v1-messages-*.log`；Codex raw SSE 诊断在 `/root/cliapp/CLIProxyAPI/logs/codex-raw-sse/`。
- worker 排查入口：`docker ps --filter name=cliproxy-worker`、`docker logs cliproxy-workerNN --tail 200`、`systemctl status cliproxy-workers-reverse-tunnel.service --no-pager -l`、`systemctl status cliproxy-workers-firewall.service --no-pager -l`。
- worker VPS 当前使用官方镜像 `eceasy/cli-proxy-api:latest` 运行 7 个 Docker 容器：`cliproxy-worker01` 到 `cliproxy-worker07`；每个容器只挂载 1 个 Codex auth file、1 份独立 `config.yaml`、1 个独立住宅代理。
- 2026-05-14 worker 侧滚动 prompt cache 最新候选提交为 `/Users/taylor/sdk/CLIProxyAPI-pure-worker-rolling-cache` 的 `ed0d6d74 codex: add worker rolling prompt cache`，包含上一轮 auth/WebSocket 隔离提交 `7166f3b7`。本地 `go test ./...`、Codex executor 定向测试、真实本地 worker HTTP/stream 黑盒均通过；worker VPS 已构建候选镜像 `cliproxy-api-worker:ed0d6d74`，构建上下文在 `/root/cliproxy-workers/custom-image-ed0d6d74/`，并保留 `source.patch`。生产 canary `worker04/worker06` 真实请求返回 `CANARY_OK`，但正式全量切换后线上 `empty_stream before first payload` 观察窗口未达干净上线标准，已回滚到官方镜像 `eceasy/cli-proxy-api:latest`；旧候选镜像 `3159b6b2` 也保留在 worker VPS 但不承载生产流量。
- 当前缓存策略：主程序负责 session-affinity，把同一会话尽量固定到同一个 worker；真正发 Codex 请求的 worker 根据 `metadata.user_id + base model + auth isolation key` 生成 prompt cache scope。OpenAI-compatible 主链路、Claude 直连链路和 Codex WebSocket 链路都会在看到上游 usage 后观察 `input_tokens + cache_read_input_tokens`，当已缓存前缀增长超过约 16k tokens 时滚动升级下一代 `prompt_cache_key`。worker 失败切换时接受缓存损失，由新 worker 重新建立自己的缓存层级。
- worker 目录统一在 `/root/cliproxy-workers/`；注册表为 `/root/cliproxy-workers/worker_registry.tsv`，包含敏感 API key，不能外传或写入仓库。
- worker 容器本机端口为 `18317-18323`；主程序不直接通过公网访问这些端口，而是通过 worker VPS 主动建立的 SSH 反向隧道访问主程序本机 `127.0.0.1:18317-18323`。
- worker VPS 的反向隧道 systemd unit：`cliproxy-workers-reverse-tunnel.service`；端口访问限制 unit：`cliproxy-workers-firewall.service`。
- 主程序 `config.yaml` 中的 worker provider 块用 `# BEGIN CODEX WORKER PROVIDERS` / `# END CODEX WORKER PROVIDERS` 标记；当前仅接入健康的 `worker02-worker07` 共 6 个 OpenAI-compatible provider，provider 名称采用 `codex-workerNN-shortlabel`，方便在主程序管理端按 provider 查看每个 auth file 的用量。
- 不含密钥和代理的用量映射表放在主程序 `/root/cliapp/CLIProxyAPI/codex-worker-usage-map.tsv`，worker VPS 同步一份在 `/root/cliproxy-workers/worker_usage_map.tsv`；敏感 API key 仍只在 worker 注册表和主程序配置中保存。
- `worker01` 对应 `codex-aritaser346@gmail.com-pro.json`，部署后 `/v1/models` 返回空列表且日志出现 `refresh_token_reused`，暂未接入主程序路由；需要重新登录或替换 auth 后再启用。
- 主程序本地 `auths/codex-*.json` 已从 `auths/` 移出，备份目录为 `/root/cliapp/CLIProxyAPI/auths.disabled-main-codex.20260511T142253Z`；主程序配置备份为 `/root/cliapp/CLIProxyAPI/config.yaml.bak.20260511T142035Z`。
- 验证命令示例：在 worker VPS 执行 `docker ps --filter name=cliproxy-worker` 和 `systemctl status cliproxy-workers-reverse-tunnel.service --no-pager -l`；在主程序 VPS 执行 `journalctl -u cliproxyapi --since '5 minutes ago' --no-pager | rg 'codex-worker|OpenAI-compat|auth files'`。
- 2026-05-11 线上恢复结论：`gpt-5.4` 不可用时，先区分“worker 真实冷却”和“worker 被误下线”。当日确认 `worker05`、`worker06` 对 `gpt-5.4` 仍可用，`worker02/03/04/07` 处于不同长度冷却；不得因为早期 `empty_stream` 就整体禁用 `worker05/06`，必须先做 worker 直连探测和主程序真实请求验证。
- 恢复生产时不要擅自把 `claude-to-gpt-target-family` 从 `gpt-5.4` 切到其它模型族；worker 冷却应由调度/冷却逻辑处理，临时改模型只允许在用户明确批准后执行。
- `cc2` 真实黑盒口径：`cc2` 等价于 `CLAUDE_CONFIG_DIR=~/.claude_cc claude2 --dangerously-skip-permissions`，其配置指向 `https://cc.claudepool.com/`。2026-05-11 回归命令使用 `~/.claude_cc` 和 `~/.local/bin/claude2 --debug-file`，最小提示 `Reply with exactly WORKER56_OK`，结果返回 `WORKER56_OK`，debug log 证实命中 `/v1/messages` 并收到首个 stream chunk。
- 2026-05-12 已上线 `empty_stream before first payload` 稳定性修复：单个 worker/模型在首包前空流时先做一次短 jitter 重试；连续空流才进入短暂 provider degraded 冷却，避免一次毛刺就永久下线好 worker，同时避免坏 worker 持续坑用户。第二次补丁覆盖“stream 已建立但第一条 chunk 就是 `upstream stream closed before first payload` 错误”的路径。上线后观察口径：`journalctl -u cliproxyapi.service --since '10 minutes ago' --no-pager | rg 'empty_stream|suppressing raw upstream error|/v1/messages'`。
- 2026-05-14 worker 稳定性策略更新：生产确认 `worker02/05/07` 为 `usage_limit_reached` 额度耗尽，`worker03/04/06` 仍可直连流式启动。主程序必须区分额度耗尽和首包空流：额度耗尽按整 worker/auth 长冷却；`empty_stream before first payload` 按 auth-level transient health 计数，第一次只记录并允许同 worker 短重试，连续失败达到阈值才整 worker 短冷却；成功请求清空连续空流计数。会话粘性只在绑定 worker 仍可用时复用，绑定 worker 不可用时必须 reselect 到可用 worker。
- worker 滚动 prompt cache 自定义镜像仍属于第二阶段优化。稳定性基线未干净前，不要全量上线 worker 自定义镜像；先保证主程序能稳定绕开额度耗尽/连续空流 worker，再做缓存命中优化。

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

# [CLIProxyAPI-ori] recent context, 2026-04-25 7:40pm PDT

Legend: 🎯session 🔴bugfix 🟣feature 🔄refactor ✅change 🔵discovery ⚖️decision
Format: ID TIME TYPE TITLE
Fetch details: get_observations([IDs]) | Search: mem-search skill

Stats: 50 obs (24,952t read) | 845,925t work | 97% savings

### Apr 22, 2026
S41 Local Memory Hit Liveness Probe — "Reply with exactly LOCAL_HIT" (Apr 22 at 2:56 AM)
S80 User Requested Bug Registry + Daily Cron for Claude Code "Failed to parse JSON" Debugging Workflow (Apr 22 at 3:31 AM)
S93 Daily TTY Health Check — Claude Code cc1 session responded DAILY_TTY_OK to ping (Apr 22 at 5:42 AM)
S101 Daily TTY Regression Probe: cc1 Session Responded DAILY_TTY_OK (Apr 22 at 6:05 AM)
S143 Daily TTY Regression Liveness Probe — cc1 Session Responded DAILY_TTY_OK (Apr 22 at 6:11 AM)
S158 Inspect working tree changes and run 3 specific Go translator tests in CLIProxyAPI-ori (Apr 22 at 7:05 AM)
S159 Daily TTY Regression Liveness Probe — cc1 Session Responded DAILY_TTY_OK (Apr 22 at 7:12 AM)
S161 CLIProxyAPI Codex/Claude Translator: All 3 Regression Tests Pass (Apr 22 at 7:18 AM)
S196 Daily TTY health check + initiate Claude translator test run in CLIProxyAPI-ori (Apr 22 at 7:19 AM)
S199 Daily TTY check + CLIProxyAPI-ori Claude translator regression test run (Apr 22 at 7:39 PM)
### Apr 24, 2026
1512 7:37p 🔵 CC1 Daily Regression Apr 25 — FAIL: Startup Hook Timeout, Zero Requests Issued
### Apr 25, 2026
1657 4:33a 🟣 Codex Auth File SOCKS Proxy Fallback on Unavailability
1659 4:35a 🔵 CLIProxyAPI Proxy Architecture Mapped for Auth-File Proxy Fallback Feature
1661 " ⚖️ Codex Auth File SOCKS Proxy Fallback — Design Intent
1663 " 🔵 CLIProxyAPI Codex WebSocket Proxy Architecture — No Fallback Path Exists
1664 4:37a ⚖️ CLIProxyAPI — SOCKS Proxy Fallback to Direct Connection Design Intent
1665 " ⚖️ CLIProxyAPI — SOCKS Proxy Fallback to Direct Connection Per Auth File
1667 " 🟣 CLIProxyAPI — WebSocket Dial Proxy Fallback to Direct Connection Implemented
1669 4:39a 🟣 CLIProxyAPI — Per-Auth-File Proxy Fallback Tests Added for HTTP Client and WebSocket Dialer
1671 " ⚖️ CLIProxyAPI — Per-Auth-File SOCKS Proxy Fallback to Direct Connection
1674 4:40a ⚖️ Codex Auth File SOCKS Proxy Fallback — Automatic Direct Connection on Proxy Unreachable
1676 " 🟣 CLIProxyAPI — SOCKS Proxy Fallback to Direct Connection Implemented for Codex WebSocket Executor
1677 4:41a 🟣 CLIProxyAPI — Auth-File SOCKS Proxy Fallback to Direct Connection Implemented and Tests Passing
1678 4:42a ⚖️ CLIProxyAPI — Per-Auth-File SOCKS Proxy Fallback to Direct Connection Feature Request
1679 4:44a ⚖️ CLIProxyAPI — Per-Auth-File SOCKS Proxy Fallback to Direct Connection Feature Request
1680 " ⚖️ Codex Auth File SOCKS Proxy Fallback — Feature Request Scoped
1681 4:45a 🔵 CLIProxyAPI Auth Proxy Fallback — Worktree and Implementation Scope Identified
1682 " 🔵 CLIProxyAPI — CodexExecutor Credential and Config Resolution Code Path
1685 4:47a 🔵 CLIProxyAPI Live Test — Codex API Request Constraints Discovered via TestLiveCodexAuthProxyDirectFallback
1686 4:48a 🟣 CLIProxyAPI — SOCKS Proxy Fallback to Direct Connection Passes Live Test
1691 4:51a 🟣 CLIProxyAPI — Auth Proxy Direct Fallback Implementation: Build Passes, Changed Files Identified
1695 4:56a ⚖️ CLIProxyAPI — Per-auth-file Proxy Freeze / Exponential Backoff Design Inquiry
1696 4:57a 🟣 CLIProxyAPI — Per-auth-file Proxy Freeze with Exponential Backoff Implemented and Tested
1697 4:58a 🟣 CLIProxyAPI — Auth Proxy Freeze / Exponential Backoff Circuit Breaker Implemented
1698 " 🟣 CLIProxyAPI — Freeze + Exponential Backoff Regression Tests Added
1703 5:00a 🔵 CLIProxyAPI + Management Center — Auth File Health Pipeline Traced for Proxy Freeze Status UI
1704 5:02a 🟣 CLIProxyAPI — New `internal/proxyhealth` Package Extracted with Rich Stats and API Snapshot
1705 5:03a 🔄 proxyhealth — Sliding Window Replaced with Fixed 60-Bucket Circular Ring Buffer
1710 5:05a 🔄 proxy_helpers.go + codex_websockets_executor.go — Inline Freeze State Migrated to proxyhealth Package
1714 5:08a 🟣 proxyhealth — Today-scoped Failure Counter, SHA256 Key Hashing, Persistence Store Interface Added
1717 5:10a 🟣 proxyhealth — Store Interface + Postgres Backend Implemented for Freeze State Persistence
1719 5:12a 🟣 CLIProxyAPI — Proxy Health State Wired into Server Startup, Auth File API, and Management Test
1739 5:27a 🟣 Per-Auth-File Proxy Freeze Circuit-Breaker with Exponential Backoff
1740 " ⚖️ Fixed 60-Bucket Circular Ring Buffer for Sliding Window Stats
1741 " ⚖️ SHA256-Hashed Proxy URL as Registry Map Key
1742 " 🟣 Write-Behind Async Postgres Persistence for Proxy Health State
1743 " 🟣 Management API `proxy_health` Field in Auth File Responses
1744 " 🟣 Frontend Proxy Health Panel in `AuthFileCard.tsx`
1746 5:29a 🔵 AuthFileCard.tsx Dual-Key Field Normalization for proxy_health API Response
1747 " 🔵 Exponential Backoff Formula and Memory Cleanup in proxyhealth.go
1748 " 🔵 PostgresStore Auto-Migrates Schema on Connection; EnsureSchema Standalone Helper Pattern
1750 " 🔵 ESLint Passes Clean; Frontend Dev Server Started for Browser Verification
1751 " 🔵 Proxy Health Panel Correctly Suppressed When Backend Lacks `proxy_health` Field
1752 " 🟣 Complete Changeset Ready to Commit — Both Repos Verified Clean
1754 5:30a 🔴 Debug Log Credential Leak Fixed — Proxy URL Sanitized Before Logging
1755 " 🔵 `initProxyHealthStore` Placement and Non-Fatal Failure Pattern in server.go
1756 5:31a 🟣 CLIProxyAPI Auth Proxy Freeze Feature — Final Backend Changeset Ready to Commit
1769 6:04a ✅ CLIProxyAPI Systemd Deploy Skill Updated with PG Migration Gate
1777 6:06a 🔵 scripts/deploy_systemd.sh Does Not Run Migrations — Manual Step Required
1783 6:08a 🟣 deploy_systemd.sh Now Auto-Runs PG Migrations Before Binary Rebuild

Access 846k tokens of past work via get_observations([IDs]) or mem-search skill.
</claude-mem-context>
