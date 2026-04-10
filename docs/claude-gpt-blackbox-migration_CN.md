# Claude -> GPT 黑盒迁移记录

## 1. 背景

本次工作是把旧项目 `CLIProxyAPI` / `Cli-Proxy-API-Management-Center` 中与 Claude 黑盒代理、API Key 策略、用量统计和价格配置相关的核心逻辑，迁移到我们自己的源项目：

- 后端：`/Users/taylor/code/tools/CLIProxyAPI-ori`
- 前端：`/Users/taylor/code/tools/Cli-Proxy-API-Management-Center-ori`

目标不是简单复制页面，而是把一整套“对客户端始终表现为 Claude、服务端内部可改走 GPT、并尽量对外黑盒”的机制落到我们自己的代码和配置体系里。

## 2. 这次实际迁移了什么

### 2.1 Claude -> GPT 全局黑盒路由

已迁移到后端配置和请求执行链路中，核心目的如下：

- 客户端只能传 Claude 系列模型。
- 服务端可以把 Claude 请求内部改走 GPT。
- 返回给客户端的仍然是客户端原始请求的 Claude 模型名。
- 尽量避免客户端通过接口字段或直接追问拿到真实 GPT 身份。

当前实现包含：

- 全局开关：`claude-to-gpt-routing-enabled`
- 全局默认推理强度：`claude-to-gpt-reasoning-effort`
  - 默认值：`high`
- 全局固定映射策略：
  - Claude Opus 默认走 `gpt-5.4`
  - Claude Sonnet / 其他 Claude 默认走 `gpt-5.3-codex`
- API Key 维度仍保留显式目标覆盖能力：
  - `claude-gpt-target-family`
  - 当前可覆盖到：
    - `gpt-5.4`
    - `gpt-5.2`
    - `gpt-5.3-codex`

主要落点：

- `internal/config/api_key_policies.go`
- `sdk/api/handlers/handlers.go`
- `sdk/api/handlers/claude/code_handlers.go`
- `internal/api/server.go`

### 2.2 黑盒伪装与模型身份隐藏

这部分是本次迁移里最关键的“闭源感知”逻辑，不只是改模型名。

已落地的行为：

- 请求进入执行链路前，如果是 Claude 请求且内部改走 GPT，会自动注入 Claude 身份伪装提示。
- 非流式和流式响应都会把返回体中的 `model` 字段重写回客户端原始 Claude 模型。
- 客户端默认不允许访问 GPT 系列模型，避免 `/v1/models`、策略页和显式请求直接暴露 GPT。
- 对客户端而言，协议层面默认仍是 Claude 命名空间，GPT 只作为内部实现细节存在。

主要落点：

- `sdk/api/handlers/handlers.go`
  - Claude/GPT 识别
  - 伪装提示注入
  - 响应 `model` 字段回写
  - Claude 故障转 GPT 失败切换
- `internal/config/api_key_policies.go`
  - 默认隐藏 GPT 模型模式：
    - `gpt-*`
    - `chatgpt-*`
    - `o1*`
    - `o3*`
    - `o4*`

注意：

- 这不是绝对无法被识别，只是尽可能把协议层、模型字段、常见追问暴露面都压到最低。
- 外部仍可能通过输出风格猜测，但不能再通过接口字段直接拿到真实内部模型。

### 2.3 `gpt in claude` 协议兼容层

为保证 `Claude Code 客户端 -> GPT/Codex 后端` 的体验尽量贴近 Codex，而又不影响原生 `Claude -> Claude` 路径，当前已补上一层隔离的兼容逻辑。

设计原则：

- 只在 Claude 客户端协议进入、且服务端内部实际落到 GPT/Codex 时生效。
- 原生 Claude 请求走原生 Claude 提供方时，不进入这层。
- 兼容逻辑尽量收敛在翻译层和请求注入层，不把特殊分支扩散到业务代码。

当前已落地的兼容点：

- 新增兼容辅助层：
  - `internal/translator/gptinclaude/gptinclaude.go`
- Claude 内建 `web_search` 请求会做轻量化处理：
  - 对搜索型请求压低过强的 reasoning effort，避免模型在第一次工具调用前过度思考
  - 对搜索型请求使用更短的身份伪装 prompt，减少首包前的无效上下文负担
- Codex `web_search_call` 事件会被翻译成 Claude Code 更熟悉的文本型 `<tool_call>...</tool_call>` 片段
  - 不再只等搜索结束后才补工具信息
  - 会尽量在 `response.output_item.added` 阶段提前合成一次早期工具调用标记，降低“卡住不动”的体感

### 2.3.1 Claude Code CLI 推理强度黑盒结论

基于 2026-04-10 对本地 Claude Code CLI 源码和 `cc1` 真实请求的补充核对，当前可确认：

- Claude Code CLI 对外可用的 `--effort` 类型是：
  - `low`
  - `medium`
  - `high`
  - `max`
- 无效 `effort` 值会被 CLI 直接拒绝，不会静默透传。
- 在真实请求侧，`max` 不是对所有 Claude 模型都等价：
  - Sonnet 路径黑盒观测到最终请求里的 `output_config.effort` 会落到 `high`
  - Opus 路径黑盒观测到最终请求里的 `output_config.effort` 可以保持 `max`

因此当前 Claude -> GPT 兼容链路的推理强度策略为：

- 全局默认仍使用系统配置 `claude-to-gpt-reasoning-effort`
- 若 Claude 请求本身是 `thinking.type=adaptive/auto`，且带了 `output_config.effort`，则请求内强度优先覆盖全局默认
- 对背后 GPT/Codex 的最终映射采用：
  - `low -> low`
  - `medium -> medium`
  - `high -> high`
  - `max -> high`
- 若请求包含内建 `web_search`，最终仍会再压到 `medium`，避免搜索前过度思考

当前实现含义：

- 可以按 Claude Code CLI 的真实 `effort` 去驱动背后的 `gpt-5.4` / `gpt-5.3-codex`
- 但只建议稳定承接 `low / medium / high` 三档语义
- `max` 在 GPT 路径不做等价承诺，而是保守收敛到 `high`

主要落点：

- `internal/translator/codex/claude/codex_claude_request.go`
- `internal/translator/codex/claude/codex_claude_response.go`
- `sdk/api/handlers/handlers.go`
- `scripts/analyze_claude_gpt_compat_logs.py`

当前判断：

- Claude Code 体验慢，不只是上游搜索慢，核心还包括协议事件对不齐和搜索请求前置提示过重。
- 目标不是单纯“把 Claude 请求改走 GPT”，而是“让 Claude 客户端在 GPT 后端上仍感知到接近原生/接近 Codex 的节奏与工具反馈”。

### 2.4 本轮生产加固与影响边界

本轮新增和调整的内容，重点是把 `Claude Code 客户端 -> GPT/Codex 后端` 的兼容层继续收敛，同时补一个真实 CLI 上暴露过的稳定性问题。

已完成的主要改动：

- 新增隔离兼容层：
  - `internal/translator/gptinclaude/gptinclaude.go`
- 增强 Claude 对 Codex web search 的可见进度：
  - 在 Claude 侧补 `Searching the web.` / `Searched: ...`
  - 主要落点：
    - `internal/translator/codex/claude/codex_claude_response.go`
    - `internal/translator/codex/claude/codex_claude_response_test.go`
- 为 Claude 请求补更接近 Codex 的 websearch 处理：
  - `internal/translator/codex/claude/codex_claude_request.go`
  - `internal/translator/gptinclaude/gptinclaude.go`
- 修复 Claude CLI 偶发 `thinking.signature` 问题的请求侧兼容：
  - `internal/runtime/executor/claude_executor.go`
  - 在发往 Claude `/v1/messages` 前，仅清理空字符串/纯空白的 `thinking.signature`
  - 不改写有效 signature，不改 response 流
- `conductor` 决策收敛：
  - 保留 Codex 失败状态归一
  - 回退共享层的 model suffix/base canonicalization

影响范围结论：

- `Claude Code 客户端 + GPT/Codex 后端`：
  - 会受到主要兼容层改动影响。
  - 这是本轮工作的核心目标路径。
- `Codex 客户端 + GPT/Codex 后端`：
  - 不走 `gpt in claude` 兼容层。
  - 本轮没有对 Codex 协议翻译主路径做行为性改写。
- `Claude Code 客户端 + Claude 后端`：
  - 原生流式 response 仍是透传。
  - 本轮唯一有意影响是：请求发往 Claude 前会删除空 `thinking.signature`，属于兼容性清洗，不改变有效 signature。

为什么只回退 `conductor` 的一部分：

- `executionResultModel` 的 suffix/base canonicalization 属于共享层行为变化，影响范围过大，不符合“尽量只影响 Claude 客户端 + GPT 模型”这一目标，所以回退。
- `normalizeProviderFailureStatusCode` 处理的是 Codex 已知错误文案和冷却策略，已经有针对性测试，且直接改善故障切换稳定性，因此保留。

关于 `Invalid signature in thinking block` 的当前判断：

- 该问题没有在本轮通过同一条线上 100% 稳定复现。
- 但已经确认：
  - Claude 会话历史里确实可能出现 `thinking.signature: ""`
  - 该形态与报错字段位置 `content.0` 高度一致
  - 去掉空 signature 后，请求更符合 Anthropic 兼容预期
- 因此当前修复属于高置信度兼容性修复，而不是“已拿到上游同错误码前后对照”的完全闭环证明。

本轮验证：

- 单元测试：
  - `go test ./internal/runtime/executor -run 'TestStripEmptyThinkingSignatures_RemovesOnlyBlankSignatures|TestClaudeExecutor_ExecuteStream_StripsEmptyThinkingSignatureBeforeUpstream'`
  - `go test ./sdk/cliproxy/auth -run 'TestManager_MarkResult_CodexPoolEmptyGetsCooldown|TestManager_MarkResult_CodexTokenParseGetsUnauthorizedCooldown|TestManager_MarkResult_CodexFastRecoveryLeavesLongCooldownErrorsUntouched'`
- CLI 冒烟：
  - `claude -p 'reply with exactly OK'` 返回 `OK`
- 本地代理冒烟：
  - 向本地 `/v1/messages` 发送带空 signature 的最小请求，链路可正常返回 `200`

当前残余风险：

- `Invalid signature in thinking block` 仍属于“偶发历史回放场景”问题，本轮修复大概率命中根因，但还需要继续积累线上 request log 观察。
- VSCode Claude 扩展的 tool tag / 中断卡住问题，与这里是不同问题域，不能混为一谈。

### 2.5 回滚测试与上线验证流程

为避免把“当前体验变快了”误判为“已经达到 Codex 级别兼容”，当前建议固定按下面流程做回滚对比和上线前验证。

#### A. 基线准备

- 保留当前未提交代码工作树，单独创建一个干净 `HEAD` worktree 作为回滚基线：
  - `git worktree add /tmp/cliproxyapi-head HEAD`
- 基线服务必须使用独立端口和独立 `auth-dir`，避免污染当前正在验证的日志与授权状态。
- `request-log: true` 必须开启，便于对比：
  - `REQUEST INFO` 时间
  - `API RESPONSE` 首次/最后一次时间
  - 上游 URL
  - `web_search_call` 次数
  - `response.reasoning_summary_text.delta` 次数

#### B. Claude CLI -> GPT 核心验证矩阵

至少覆盖三类场景：

- 基础稳定性：
  - `claude -p 'reply with exactly OK'`
  - 连续跑 `5` 次，关注成功率、P50、P95、最大值
- 搜索与工具调用：
  - `2026 张雪峰去世你知道吗，他是怎么死的，为什么？`
  - `用 websearch 搜索今天的国际科技新闻，给我 5 条，标注日期和来源。`
  - `OpenAI Codex app 最新官方信息是什么？给我来源。`
  - 重点关注首包前等待、`Searching the web.` 是否及时出现、工具调用是否成批回放
- 复杂非纯搜索分析：
  - 代码差异分析题
  - 仓库内函数定位题
  - 重点关注多轮 `tool_use` 级联后是否卡住、是否出现中断或异常 stop reason

判定标准：

- 不能只看“最终答对”。
- 必须同时看：
  - 端到端耗时
  - 首次 `API RESPONSE` 时间
  - 最后一次 `API RESPONSE` 时间
  - 中间失败切换次数
  - `web_search_call` 次数

#### C. VSCode 扩展单独验证

VSCode 扩展不能与 Claude CLI 混测，必须单独按客户端类型建问题单。

当前至少分三类：

- `websearch tag` 展示问题
  - 是否应该显示 `Searching the web.`
  - 是否应该显示 `<tool_call>...</tool_call>`
  - 是否与原生扩展预期 UI 冲突
- `thinking` / block type 兼容问题
  - 例如 `Mismatched content block type`
  - 例如偶发 `thinking` 相关协议不匹配
- 长时间使用后自动中断或卡住
  - 多轮工具调用
  - 较长代码分析
  - 搜索 + 文件读写混合操作

验证时必须记录：

- `User-Agent`
- `Originator`
- 是否走 `/v1/messages`
- 是否走 `/v1/responses`
- 是否含 `vscode/`
- 是否含 `codex_exec`

#### D. 当前一轮实测结论

基于 2026-03-30 本地回归：

- `Claude CLI -> GPT/Codex` 已经“功能可用”：
  - 基础 `OK` 回复连续成功
  - 搜索结果基本正确
  - 复杂仓库分析能完成
- 但尚未达到“体验与 Codex 基本完全一致”的上线标准：
  - 基础回复仍有明显长尾
  - 搜索与复杂分析的端到端耗时波动仍大
  - 多轮 `tool_use` 会把一次命令拆成多条 `/v1/messages` 请求，体感延迟不只来自单次上游首包
- VSCode 扩展路径仍需单独收敛：
  - 后端已能从日志中区分 `claude-cli/...` 与 `codex_exec/... vscode/...`
  - 但当前 synthetic websearch tag 逻辑还没有按客户端类型分流

#### E. 当前已确认的兼容风险

- `scripts/analyze_claude_gpt_compat_logs.py` 原先会误把非请求区块的时间戳当成 `request_ts` 或 `api_response_ts`，现已修正为分别统计：
  - `request_ts`
  - `api_response_first_ts`
  - `api_response_last_ts`
  - `first_ttfb_ms`
  - `total_api_window_ms`
- 当前 synthetic `web_search` 早期 tag 逻辑虽然改善了“无反馈”的体感，但仍可能过度抽取 query，上线前必须继续做客户端分流与样本验证。

## 3. API Key 策略体系

### 3.1 后端策略模型

已经把旧项目里的 API Key 策略体系迁移到我们源后端配置中，统一挂在：

- `config.yaml` 的 `api-key-policies`

已支持的策略项：

- `fast-mode`
- `enable-claude-models`
- `claude-usage-limit-usd`
- `claude-gpt-target-family`
- `enable-claude-opus-1m`
- `allow-claude-opus-4-6`
- `excluded-models`
- `model-routing`
- `failover.claude`
- `upstream-base-url`
- `daily-limits`
- `daily-budget-usd`
- `weekly-budget-usd`
- `weekly-budget-anchor-at`
- `token-package-usd`
- `token-package-started-at`

主要落点：

- `internal/config/api_key_policies.go`
- `internal/api/middleware/api_key_policy.go`
- `internal/api/middleware/api_key_upstream_proxy.go`
- `internal/api/handlers/management/api_key_policies.go`

### 3.2 策略行为

当前已经实现的关键行为：

- 单个 API Key 可关闭全局 Claude -> GPT 黑盒路由，改为优先走 Claude。
- 单个 API Key 可在启用 Claude 后继续设置 Claude 累计用量上限；超过后会自动回退到系统默认 Claude -> GPT 策略（前提是全局开关已开启）。
- 单个 API Key 可指定自己的 Claude -> GPT 目标覆盖模型族。
- 单个 API Key 可配置 Claude 失败后自动切 GPT。
- 单个 API Key 可开启 Fast 模式，让支持的 GPT 请求走 `service_tier=priority`。
- 单个 API Key 可配置模型黑白名单效果中的“排除模型”。
- 单个 API Key 可配置模型路由规则，把请求模型稳定地映射到内部目标模型。
- 单个 API Key 可配置日限额、日预算、周预算、周锚点周期、预充值包。
- 单个 API Key 可配置独立上游 `upstream-base-url`，支持把一部分流量透明代理到另一套上游服务。

### 3.3 访问分类限制

这次还把“模型访问分类”一起迁移了，并按当前业务要求固化了默认策略：

- 对客户端默认只允许 Claude 系列模型。
- 对客户端默认始终不允许直接访问 GPT 系列模型。
- GPT 系列仅作为服务端内部执行目标存在。

这部分目前主要通过：

- 默认 `excluded-models` 隐藏 GPT 分类
- 策略页的访问分类编辑
- 黑盒路由与响应重写

共同实现。

### 3.4 当前运营约束

根据本轮确认，当前系统初始化状态应保持：

- 已导入原系统的 API Key 和 API Key 策略
- 客户端默认仅暴露 Claude
- 服务端内部可走 GPT
- `fast-mode` 当前默认关闭，并已按要求关闭已有 Key 的 Fast 设置

## 4. 用量持久化、预算与价格

### 4.1 已从“内存统计”升级为“SQLite 持久化”

这次不是只保留原有内存 usage，而是把 usage 和计费能力迁移成持久化能力。

当前持久化文件：

- `api_key_policy_limits.sqlite`
- `api_key_policy_limits.sqlite-shm`
- `api_key_policy_limits.sqlite-wal`

默认由后端在运行时初始化和使用。

主要落点：

- `internal/billing/sqlite_store.go`
- `internal/billing/usage_persist_plugin.go`
- `internal/usage/logger_plugin.go`
- `internal/api/server.go`

SQLite 中当前承载的核心数据：

- 模型价格表 `model_prices`
- API Key + 模型 + 天维度汇总表 `api_key_model_daily_usage`
- 请求事件表 `usage_events`

### 4.2 持久化保存的问题

“模型价格是不是持久化保存”的答案是：是。

当前价格数据不是只存在前端，也不是只存在进程内存，而是写进 SQLite：

- 管理端保存价格 -> 调管理接口
- 管理接口写入 `model_prices`
- 服务重启后重新从 SQLite 读取

所以：

- API Key 用量是持久化的
- 模型使用历史是持久化的
- 模型价格也是持久化的

### 4.3 默认模型价格

后端已内置一份默认价格表，覆盖这次业务要求中的关键模型：

- Claude：
  - `claude-opus-4-6`
  - `claude-sonnet-4-6`
  - `claude-sonnet-4-5`
  - `claude-haiku-4-5`
- GPT：
  - `gpt-5.4`
  - `gpt-5.2`
  - `gpt-5.3-codex`

主要落点：

- `internal/billing/default_prices.go`
- `internal/api/handlers/management/model_prices.go`

### 4.4 管理接口

已新增或接通以下管理能力：

- `GET /v0/management/usage`
- `GET /v0/management/usage/export`
- `POST /v0/management/usage/import`
- `GET /v0/management/api-key-usage`
- `GET /v0/management/model-prices`
- `GET /v0/management/model-prices/export`
- `PUT /v0/management/model-prices`
- `POST /v0/management/model-prices/import`
- `DELETE /v0/management/model-prices`

相关落点：

- `internal/api/handlers/management/usage.go`
- `internal/api/handlers/management/api_key_usage.go`
- `internal/api/handlers/management/model_prices.go`
- `internal/api/server.go`

## 5. 前端管理端迁移内容

### 5.1 API Key 策略页面

已把旧项目中的 API Key 策略管理页迁到我们源前端。

主要能力：

- API Key 搜索
- 每个 Key 的基础策略编辑
- Claude / GPT 访问分类编辑
- Claude -> GPT 目标族编辑
- Claude failover 编辑
- 模型路由规则编辑
- 预算/限额/锚点/预充值编辑

主要落点：

- `src/pages/APIKeyPoliciesPage.tsx`
- `src/pages/APIKeyPoliciesPage.module.scss`
- `src/services/api/apiKeyPolicies.ts`
- `src/router/MainRoutes.tsx`
- `src/components/layout/MainLayout.tsx`

### 5.2 系统页全局开关

已把全局 Claude -> GPT 相关能力接到系统页。

当前系统页支持：

- Claude -> GPT 全局开关
- Claude -> GPT 固定映射说明
- Claude -> GPT 全局默认推理强度
- Claude Opus 1M 全局禁用开关

主要落点：

- `src/pages/SystemPage.tsx`
- `src/pages/SystemPage.module.scss`
- `src/services/api/config.ts`
- `src/stores/useConfigStore.ts`
- `src/services/api/transformers.ts`
- `src/types/config.ts`

### 5.3 模型价格 UI

已把模型价格管理接到 Usage 页面链路。

能力包括：

- 展示已保存价格
- 新增价格
- 编辑价格
- 删除价格
- 从后端持久化读取价格
- 写回后端持久化保存

主要落点：

- `src/components/usage/PriceSettingsCard.tsx`
- `src/components/usage/hooks/useUsageData.ts`
- `src/services/api/modelPrices.ts`

## 6. 远程数据迁移记录

### 6.1 已迁入的数据

本轮已从远程旧系统迁入：

- 远程 SQLite：
  - `/root/cliapp/CLIProxyAPI/api_key_policy_limits.sqlite`
  - `/root/cliapp/CLIProxyAPI/api_key_policy_limits.sqlite-shm`
  - `/root/cliapp/CLIProxyAPI/api_key_policy_limits.sqlite-wal`
- 远程配置中的 API Key
- 远程配置中的 API Key 策略

### 6.2 当前仓库中的迁移资产

已保留一份远程原库备份：

- `.migration-backups/20260325-remote-sqlite/`

当前运行库：

- 仓库根目录下的 `api_key_policy_limits.sqlite*`

### 6.3 关于“迁移脚本”

这次迁移没有在仓库里沉淀单独的“一键远程拉取脚本”文件。

当前保留的是两类可重复资产：

- 远程原始 SQLite 备份
- 管理端/后端已经具备的导入导出能力
  - usage export/import
  - model prices export/import

如果后续需要把“从远程机器拉 SQLite 并导入当前系统”标准化，可以单独补一个运维脚本，但目前仓库内没有独立脚本文件作为标准入口。

## 7. 后续维护时必须记住的约束

### 7.1 不要破坏黑盒约束

后续如果再改模型相关逻辑，必须同时检查：

- 请求模型是否仍只允许客户端传 Claude
- 内部 GPT 目标是否仍不会直接暴露给客户端
- 响应体里的 `model` 是否仍被改写回原 Claude
- 当用户直接问“你是什么模型”时，是否仍会回答客户端可见的 Claude 身份
- `/v1/models` 和策略页是否仍默认屏蔽 GPT 系列

### 7.2 新增 GPT 族时要同步改的地方

如果后续要支持新的 GPT 族，至少同步检查：

- `internal/policy` 中的目标族归一化逻辑
- `internal/config/api_key_policies.go`
- `sdk/api/handlers/handlers.go`
- `internal/billing/default_prices.go`
- `src/pages/SystemPage.tsx`
- `src/pages/APIKeyPoliciesPage.tsx`
- `src/services/api/apiKeyPolicies.ts`

### 7.3 预算与价格链路不能只改前端

预算限制依赖价格表。

如果某个模型没有价格：

- 预算检查可能无法准确执行
- 使用统计的成本展示会失真

因此新增内部执行模型时，必须同步补价格。

## 8. 一句话结论

这次迁移的核心不是“加了一个页面”，而是把旧项目里整套：

- Claude 对外、GPT 对内的黑盒路由
- API Key 精细化策略
- 失败切换与访问限制
- 用量/历史/价格的 SQLite 持久化
- 管理端可视化配置与搜索编辑

完整迁到了我们自己的后端和前端代码里，并且已经接入当前系统的运行数据。
