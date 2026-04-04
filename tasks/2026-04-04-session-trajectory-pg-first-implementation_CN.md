# 2026-04-04 Session Trajectory PG-First 实现与回归记录

## 本轮目标

- 按 `docs/requirements/ai-gateway-session-trajectory-format_CN.md` 落地自有会话轨迹存储。
- 不使用 Cloudflare AI Gateway。
- 不引入外部 MQ，优先保证聊天主链路稳定。
- 提供管理端查询 / 导出接口，以及“会话轮次 tokens 数量”查询接口。
- 做浏览器侧验证、真实 Claude CLI 黑盒验证，并把结果沉淀到 `tasks/`。

## 本轮实现

### 1. 运行时 PG 主存储

- 新增 `internal/sessiontrajectory/`
  - `types.go`
  - `normalize.go`
  - `async_recorder.go`
  - `postgres_store.go`
  - `query.go`
  - `export.go`
- 服务启动时初始化 PG trajectory store，并在关闭时释放。
- 在线写入走进程内有界异步队列，默认不阻塞用户聊天响应。
- canonical `session_id` 由服务端生成，`provider_session_id` 仅作辅助 alias。

### 2. 请求归一化与会话归并

- 当前归一化字段：
  - `user_id`
  - `source`
  - `call_type`
  - `provider`
  - `model`
  - `system`
  - `tools`
  - `messages`
  - `usage`
- 归并规则：
  - 先按 `provider_session_id -> canonical session` alias 命中
  - 命不中时，在同一 `user_id + source + call_type` 最近活跃会话中尝试归并
  - 仅当 `system_hash` / `tools_hash` 一致时继续比较消息连续性
  - 支持 `messages` 完全相同或前缀延续

### 3. 管理端接口

- 已新增：
  - `GET /v0/management/session-trajectories/sessions`
  - `GET /v0/management/session-trajectories/sessions/:sessionId`
  - `GET /v0/management/session-trajectories/sessions/:sessionId/requests`
  - `GET /v0/management/session-trajectories/sessions/:sessionId/token-rounds`
  - `POST /v0/management/session-trajectories/sessions/:sessionId/export`
  - `POST /v0/management/session-trajectories/export`

### 4. 导出视图

- 导出目录符合需求：
  - `session-data/session-exports/[user_id]_[session_id]/[index]_[request_id].json`
- 单文件保留：
  - `request_id`
  - `session_id`
  - `canonical_session_id`
  - `user_id`
  - `start_time`
  - `end_time`
  - `user_agent`
  - `call_type`
  - `status`
  - `provider`
  - `model`
  - `provider_session_id`
  - `provider_request_id`
  - `upstream_log_id`
  - `system`
  - `tools`
  - `messages`
  - `response`

## 本轮修复的真实阻塞问题

### 1. `provider_session_id = "id"` 误提取

- 现象：
  - 管理端真实会话里出现 `provider_session_id: "id"`
- 根因：
  - 原实现直接用正则在 `metadata.user_id` 原始 JSON 字符串上扫 `session_*`
  - 错把键名 `session_id` 的 `id` 当成了值
- 修复：
  - 改为结构化解析 `metadata.user_id`
  - 优先提取内层稳定的 `session_id` / `sessionId`
  - 仅在结构化字段不存在时，才对字符串叶子值做 `session_<id>` 模式提取

### 2. 第二轮 continue 落库失败

- 现象：
  - `claude -c` 用户侧成功
  - 第二轮请求未写入 `session_trajectory_requests`
- 根因：
  - `matchRecentSessionTx` 扫描 `provider_session_id` 时直接用 `string`
  - PG `NULL` 转 `string` 报错：
    - `sql: Scan error ... converting NULL to string is unsupported`
- 修复：
  - 改为 `sql.NullString`

### 3. 流式请求 `response_json` 丢失、tokens 为 0

- 现象：
  - 真实 Claude CLI 路径下 `token-rounds` 返回结构正确，但 tokens 全为 `0`
  - `response_json` 为 `null`
- 根因：
  - recorder 对流式路径优先拿了请求日志聚合文本，而非真实客户端响应
  - `response_json` 无法 compact 为 JSON
- 修复：
  - recorder 在流式响应场景额外缓冲真实客户端输出，限额 `2 MiB`
  - `normalize.go` 新增 Anthropic SSE 压缩逻辑
  - 将 Claude SSE 压成可落 `jsonb` 的 message/usage 结构

## 单测与回归

### 1. 单测

- 新增 / 扩展：
  - `internal/sessiontrajectory/normalize_test.go`
  - `internal/sessiontrajectory/postgres_store_test.go`
  - `internal/api/handlers/management/postgres_management_test.go`
- 覆盖点：
  - `messages` 前缀归并
  - alias 归并
  - `metadata.user_id` 结构化 `session_id` 提取
  - Anthropic SSE usage 压缩
  - 结构化 `session_id` 驱动的 PG 会话复用

### 2. 全量测试

- 执行时间：2026-04-04
- 命令：
  - `go test ./...`
- 结果：
  - 全量通过

## 上线准备

### 1. 显式 PG migration

- 新增脚本：
  - `go run ./scripts/migrate_session_trajectory_pg`
- 默认行为：
  - 复用当前仓库 `.env` 中的共享 PG 配置解析逻辑
  - 默认读取 `APIKEY_POLICY_PG_DSN / APIKEY_POLICY_PG_SCHEMA`
  - 若未设置，再回退到已有共享变量别名
- 上线建议：
  - 先执行 migration，确认 session trajectory 相关表与索引存在
  - 再重启服务
  - 服务内保留 `ensureSchema` 作为兜底，不作为主要发布手段

### 2. 生产副作用评估

- 写入链路：
  - 通过进程内有界异步队列落库
  - 队列满时优先丢弃轨迹写入，不阻塞用户聊天主响应
- 存储链路：
  - PG `jsonb + 索引` 方案已能满足当前 50~100 用户规模
  - 导出按需执行，不在主请求链路写单文件
- 兼容性边界：
  - 不要求普通 `cc1` 用户额外配置环境变量或 wrapper
  - 当前生产可承诺的是“同一 TTY 连续对话”归并
  - 不把 `-p/-c/--resume` 的漂移场景包装成已完全支持

## 真实黑盒验证

### 1. 验证环境

- 服务：
  - 本地当前代码 `go run ./cmd/server -config tmp/validation-config-pg-c.yaml`
- 端口：
  - `53951`
- 管理口令：
  - `MANAGEMENT_PASSWORD=mgmt-e2e`
- PG schema：
  - 最终有效回归 schema：`sessiontraj_e2e_20260404_g`
- 入站 API Key：
  - `sk-PXx2PfMdZawG2J0599uMIfh3iTXhowUUSCBMgdbp3M2Aw`

### 2. Claude CLI 命令

- 第一轮：

```bash
CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 /Users/taylor/.local/bin/claude \
  -p 'Reply with exactly FINAL_OK' \
  --model gpt-5.4 \
  --output-format text \
  --tools '' \
  --settings '{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:53951","ANTHROPIC_AUTH_TOKEN":"sk-PXx2PfMdZawG2J0599uMIfh3iTXhowUUSCBMgdbp3M2Aw","DISABLE_AUTOUPDATER":"1"}}'
```

- 输出：

```text
FINAL_OK
```

- 第二轮：

```bash
CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 /Users/taylor/.local/bin/claude \
  -c \
  -p 'What was my previous instruction? Reply with exactly: FINAL_PREV' \
  --model gpt-5.4 \
  --output-format text \
  --tools '' \
  --settings '{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:53951","ANTHROPIC_AUTH_TOKEN":"sk-PXx2PfMdZawG2J0599uMIfh3iTXhowUUSCBMgdbp3M2Aw","DISABLE_AUTOUPDATER":"1"}}'
```

- 输出：

```text
FINAL_PREV
```

### 3. 黑盒结论

- 用户真实 continue 对话成功。
- 服务端成功把两轮归并到同一个 canonical session。
- 最新验证会话：
  - `session_id`: `583be797-0eb1-44b7-91ea-9c4b2da35b63`
  - `provider_session_id`: `650573de-794b-4df2-aabe-eafbb53a4e87`
  - `request_count`: `2`

### 4. 轮次 tokens 查询结果

- 管理接口：
  - `GET /v0/management/session-trajectories/sessions/583be797-0eb1-44b7-91ea-9c4b2da35b63/token-rounds`
- 返回摘要：
  - `round_count = 2`
  - `input_tokens = 5901`
  - `output_tokens = 276`
  - `cached_tokens = 5504`
  - `total_tokens = 6177`
- 每轮：
  - round 1:
    - `request_id = 5b891e53`
    - `total_tokens = 5938`
  - round 2:
    - `request_id = 7ea55661`
    - `total_tokens = 239`

### 5. 补充黑盒：真实 `cc1` alias 与强校验 secret 测试

- 补充时间：2026-04-04 15:40 之后
- 结论先行：
  - 之前 `FINAL_OK -> FINAL_PREV` 的提示词设计过弱，不能证明真实上下文已被服务端连续归并。
  - 用真实 `cc1` alias 做更强校验后，发现 `cc1 -p` / `cc1 -c` 不能作为“服务端一定能拿到连续会话”的验收依据。

#### 5.1 `cc1` alias 事实

- 本机 `cc1` 不是独立二进制，而是 `~/.zshrc` 中的 alias：

```text
cc1='CLAUDE_CONFIG_DIR=~/.claude_local claude --dangerously-skip-permissions'
```

#### 5.2 强校验 secret 测试

- 测试目录：
  - `/tmp/cc1-secret-h`
- 服务 schema：
  - `sessiontraj_e2e_20260404_h`
- 第一轮：

```bash
zsh -ic 'cd /tmp/cc1-secret-h && export CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 ANTHROPIC_BASE_URL=http://127.0.0.1:53951 ANTHROPIC_AUTH_TOKEN=sk-PXx2PfMdZawG2J0599uMIfh3iTXhowUUSCBMgdbp3M2Aw DISABLE_AUTOUPDATER=1; cc1 -p "Remember this secret exactly: LUNAR-58291. Reply with exactly ACK_SECRET" --model gpt-5.4 --output-format text --tools ""'
```

- 第一轮输出：

```text
ACK_SECRET
```

- 第二轮：

```bash
zsh -ic 'cd /tmp/cc1-secret-h && export CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 ANTHROPIC_BASE_URL=http://127.0.0.1:53951 ANTHROPIC_AUTH_TOKEN=sk-PXx2PfMdZawG2J0599uMIfh3iTXhowUUSCBMgdbp3M2Aw DISABLE_AUTOUPDATER=1; cc1 -c -p "What secret did I tell you to remember? Reply only with the secret." --model gpt-5.4 --output-format text --tools ""'
```

- 第二轮输出：

```text

```

#### 5.3 服务端观测结果

- 管理端列出的会话不是 1 个 2-request session，而是两个独立 session。
- 两次请求的 `metadata.user_id.session_id` 不同：
  - 第一轮：`03554aad-07be-47d6-9456-4c78c0af5e68`
  - 第二轮：`7614b402-57ce-4579-b79b-99533e7a4e07`
- 两次请求的 `normalized.messages` 都只包含各自当轮 user prompt，没有前缀延续历史。
- 本地 `~/.claude_local/projects/-private-tmp-cc1-secret-h/` 也确实生成了两个不同的 transcript 文件。

#### 5.4 对 Claude 源码的交叉核对

- Claude 本地源码里，请求 `metadata.user_id` 的 `session_id` 确实来自 `getSessionId()`：
  - `/Users/taylor/sdk/claude-code/services/api/claude.ts`
- `-p` 模式下的 `--continue` 逻辑理论上会尝试恢复上一会话并复用 session：
  - `/Users/taylor/sdk/claude-code/cli/print.ts`
- 但本轮真实黑盒结果说明：
  - 在当前 `cc1 -p` / `cc1 -c` 组合下，CLI 最终实际发到服务端的 `metadata.user_id.session_id` 仍可能变化。

#### 5.5 这条结论对服务端设计的影响

- 对 `cc1 -p` / `cc1 -c` 这一路径，不能把 `provider_session_id` 当成稳定主锚点。
- 如果请求体里既没有稳定 `provider_session_id`，也没有前缀延续的 `messages`，服务端无法仅靠当前可见字段无歧义地把第二轮归并回第一轮。
- 因此，这不是当前 PG recorder 的单点解析 bug，而是“客户端是否把可归并线索透传到服务端”的能力边界问题。

### 6. 补充黑盒：真实同一 TTY 连续对话

- 补充时间：2026-04-04 18:42 之后
- 验证目标：
  - 不再使用 `-p` / `-c`
  - 改为真实交互式 Claude/cc1 同一 TTY 内连续输入两轮
  - 只验证服务端是否能把同一 TTY 连续对话归并为单一 session

#### 6.1 环境

- 服务 schema：
  - `sessiontraj_e2e_20260404_i`
- 交互工作目录：
  - `/tmp/claude-tty-gw`
- 网关 settings 文件：
  - `tmp/claude-gateway-settings.json`

#### 6.2 结果

- 服务端最终生成单一 canonical session：
  - `session_id = be488e55-ab0f-4b8d-b575-e269848e33c5`
- 同一会话内累计记录：
  - `request_count = 8`
- 所有已落库请求的 `metadata.user_id.session_id` 一致：
  - `ec3e592c-9f4c-408f-b19f-1244eb530340`
- 管理端请求列表显示：
  - 所有 request 都落在同一个 `session_id`
  - 中间虽然有多次 error / interrupt / retry，但没有被拆成多个 session

#### 6.3 结论

- 对“同一交互 TTY 内连续对话”这条主路径，当前会话轨迹归并是成立的。
- 当前真正不稳定的是：
  - `cc1 -p` / `cc1 -c` 这类 print / continue 恢复链路
- 因此若按生产优先级排序：
  - 可以先把“同一 TTY 连续对话可稳定记录与归并”作为当前主验收路径
  - 再单独收敛 `-p` / `-c` / `--resume` 路径

## Chrome MCP 浏览器验证

### 1. 验证方式

- 先导航到：
  - `http://127.0.0.1:53951/`
- 再在同源页面内用 `fetch` 带 `Authorization: Bearer mgmt-e2e` 调管理接口

### 2. 浏览器侧结果

- `sessions` 查询：
  - `status = 200`
  - `session_count = 2`
  - `target_session_id = 583be797-0eb1-44b7-91ea-9c4b2da35b63`
  - `target_request_count = 2`
  - `target_provider_session_id = 650573de-794b-4df2-aabe-eafbb53a4e87`
- `token-rounds` 查询：
  - `round_count = 2`
  - `total_tokens = 6177`
  - `first_round_tokens = 5938`
  - `second_round_tokens = 239`
- `export` 调用：
  - `status = 200`
  - `file_count = 2`
  - `export_dir = /Users/taylor/code/tools/CLIProxyAPI-ori/session-data/session-exports/api_key_494d442492be272b6c55e0f2_583be797-0eb1-44b7-91ea-9c4b2da35b63`

## 管理端 UI 支持与验证

### 1. 前端实现范围

- 前端仓库：
  - `/Users/taylor/code/tools/Cli-Proxy-API-Management-Center-ori`
- 已新增会话轨迹管理页与 API 接入：
  - `src/pages/SessionTrajectoriesPage.tsx`
  - `src/pages/SessionTrajectoriesPage.module.scss`
  - `src/services/api/sessionTrajectories.ts`
- 已接入现有管理端导航、路由与多语言：
  - `src/router/MainRoutes.tsx`
  - `src/components/layout/MainLayout.tsx`
  - `src/services/api/index.ts`
  - `src/i18n/locales/zh-CN.json`
  - `src/i18n/locales/en.json`
  - `src/i18n/locales/ru.json`

### 2. UI 能力

- 支持会话列表筛选：
  - `user_id`
  - `source`
  - `call_type`
  - `provider`
  - `canonical_model_family`
  - `status`
- 支持会话详情展示：
  - session overview
  - token rounds
  - request list
  - JSON payload modal
  - export result list
- 不新增后端接口，直接消费已落地的 management session trajectory API。

### 3. 前端构建与静态校验

- `npm run build`
  - 通过
- `npm run lint`
  - 无新增 error
  - 仅存在一个既有 warning：
    - `src/pages/APIKeysWorkbenchPage.tsx:468`

### 4. 真实浏览器验证

- 预览地址：
  - `http://127.0.0.1:4173/#/session-trajectories`
- 浏览器已登录管理端后验证通过：
  - 侧边栏出现 `会话轨迹`
  - 路由跳转正常
  - 会话列表加载正常
  - 详情面板渲染正常
  - `批量导出筛选结果` / `导出当前会话` 可触发服务端导出
  - `包含 JSON 载荷` 开关可用
  - `查看载荷` 可打开 modal，并成功展示 `request_json`
  - `Provider=anthropic` 筛选输入与 `应用筛选` 按钮可正常触发加载
- 本轮浏览器连接的后端是：
  - `http://127.0.0.1:53941`
- 该实例里看到的 `provider_session_id = id` 与 token rounds 为 `0`，属于所连后端实例/数据状态问题，不是管理端 UI 渲染 bug。

## 性能与副作用评估

### 1. 对聊天主链路的影响

- 轨迹写入仍是异步入队，不阻塞最终响应返回。
- 新增的流式响应缓冲仅用于 recorder，不改变对客户端的返回时序。
- 流式响应缓冲做了 `2 MiB` 上限，避免超长输出导致单请求内存无限增长。

### 2. 存储与格式取舍

- 运行时主存储仍以 PG 为准，满足查询、筛选、导出需求。
- 单文件 JSON 导出仍按需求格式保留核心字段。
- 对 Claude SSE 做压缩后再落 `jsonb`，避免把不可查询的原始流文本直接灌进 PG。
- 这个方案比“原样保存整段流文本”更省存储，也更利于管理端按 tokens / 请求轮次查询。

### 3. 当前已验证无明显副作用

- 真实 `claude` continue 不受影响
- 管理查询接口可用
- token-rounds 接口可用
- export 接口可用
- 全量 `go test ./...` 通过
- 但这条结论仅覆盖已经验证通过的请求路径，不覆盖后来补测失败的 `cc1 -p` / `cc1 -c` 强校验场景
- 同一交互 TTY 连续对话场景已再次验证可稳定归并

## 当前结论

- 本轮已达到：
  - PG-first 自有会话轨迹存储
  - 管理端查询 / 导出
  - 轮次 tokens 查询
  - Chrome MCP 浏览器验证
  - 基础 Claude CLI 黑盒验证
- 但当前还不能宣称：
  - `cc1 -p` / `cc1 -c` continue 路径已经达到生产级会话归并
  - `docs/requirements/ai-gateway-session-trajectory-format_CN.md` 中“以 `session_id` 识别和归并一个完整会话”对所有 Claude CLI 继续对话路径都已满足
- 当前更准确的结论是：
  - PG-first 存储、管理查询、导出、token-rounds 已实现并可用
  - 对同一交互 TTY 主路径已经验证通过
  - 但对真实 `cc1` alias 的 `-p` / `-c` 继续对话，仍存在上游 session 漂移导致无法稳定归并的生产阻塞项

## 剩余边界

- 当前流式 `response_json` 压缩已明确验证 Claude / Anthropic Messages 路径。
- OpenAI 风格流式响应的同类压缩与黑盒验证，本轮未单独展开；如后续要把“所有 client path 的 token-rounds 都作为生产承诺”，建议补一轮 OpenAI SDK / Responses 路径真实回归。
- `cc1 -p` / `cc1 -c` 在当前环境下会出现 `metadata.user_id.session_id` 漂移，且请求体不带历史消息前缀；这意味着服务端目前无法仅靠现有可见字段稳定归并。
- 若要把 `cc1` continue 也纳入生产承诺，必须先补齐以下至少一项：
  - 找到客户端额外透传的稳定 session 标识并接入提取
  - 让客户端 / wrapper 显式把本地 session ID 透传到服务端
  - 或把验收范围严格限定为“同一 PTY 连续交互”而不包含 `-p` / `-c` 续聊

## 2026-04-04 20:00 之后补充回归

### 1. 新发现并修复的核心 bug

- 问题：
  - `messages` 前缀归并相关 PG 测试实际失败。
  - 标准两轮对话在没有显式 `provider_session_id` 复用时，会被拆成两个 canonical session。
- 根因：
  - `normalized_json` 在 `user_id` 从入站 API key 推导之前就已序列化入库。
  - 后续 `matchRecentSessionTx` 用落库的 `normalized_json.user_id` 做兼容性判断，导致上一轮请求的 `user_id` 为空，归并永远失败。
  - 同时，上一轮 assistant 回复未纳入 prefix 归并候选，标准“user -> assistant -> user” 续聊覆盖不完整。
- 修复：
  - `Record` 中在补齐 `normalized.UserID` 后重新序列化 `normalized_json`。
  - `matchRecentSessionTx` 从 session 表读取 `user_id`，兼容历史空值 `normalized_json.user_id`。
  - 追加“上一轮 response 还原为 assistant message 后参与 prefix 匹配”的逻辑。
  - 新增回归测试：
    - `TestMessagesPrefixMatchAnthropicStyle`
    - `TestAppendAssistantResponseToMessages`

### 2. 测试结果

- `TEST_POSTGRES_DSN='postgres://postgres:root123@127.0.0.1:5432/postgres?sslmode=disable' go test ./internal/sessiontrajectory ./internal/api/handlers/management -v`
  - 通过
- `go test ./internal/api/middleware -v`
  - 通过
- `TEST_POSTGRES_DSN='postgres://postgres:root123@127.0.0.1:5432/postgres?sslmode=disable' go test ./...`
  - 通过

### 3. 本轮真实服务回归环境

- 服务启动命令：
  - `APIKEY_POLICY_PG_DSN='postgres://postgres:root123@127.0.0.1:5432/postgres?sslmode=disable' APIKEY_POLICY_PG_SCHEMA='sessiontraj_e2e_20260404_201115' MANAGEMENT_PASSWORD='mgmt-e2e' go run ./cmd/server -config tmp/validation-config-pg-c.yaml`
- 管理密钥：
  - `mgmt-e2e`
- 管理接口基址：
  - `http://127.0.0.1:53951`

### 4. Chrome MCP 管理端验证

- 在浏览器中访问：
  - `http://127.0.0.1:53951/`
- 浏览器内 `fetch` 结果：
  - `GET /v0/management/session-trajectories/sessions?limit=10`
    - `status = 200`
    - `session_count = 7`
    - `first_session_id = 9aac070f-3b6e-41ae-993d-5ae80c756ea3`
  - `GET /v0/management/session-trajectories/sessions/9aac070f-3b6e-41ae-993d-5ae80c756ea3/token-rounds`
    - `status = 200`
    - `round_count = 11`
    - `total_tokens = 0`
    - 这组是上游错误重试会话，tokens 为 0 符合现状
  - `POST /v0/management/session-trajectories/sessions/9aac070f-3b6e-41ae-993d-5ae80c756ea3/export`
    - `status = 200`
    - `export_file_count = 11`
    - `export_dir = /Users/taylor/code/tools/CLIProxyAPI-ori/session-data/session-exports/api_key_494d442492be272b6c55e0f2_9aac070f-3b6e-41ae-993d-5ae80c756ea3`

### 5. 真实黑盒：干净目录串行 `claude -p/-c`

- 目录：
  - `/tmp/claude-clean-e2e-seq`
- 命令 1：
  - `claude -p 'Reply with exactly SEQ_OK' --model gpt-5.4 --output-format text --tools '' --settings ...`
- 输出 1：
  - `SEQ_OK`
- 命令 2：
  - `claude -c -p 'What was my previous instruction? Reply with exactly SEQ_PREV' --model gpt-5.4 --output-format text --tools '' --settings ...`
- 输出 2：
  - `SEQ_PREV`
- PG 核对结果：
  - 第一轮：
    - `request_id = 976de353`
    - `session_id = 900ac6f9-70bc-495e-98d0-73ee734181a0`
    - `provider_session_id = e43122db-7da8-4883-867a-e029c58a8a70`
    - `request_messages = 1`
  - 第二轮：
    - `request_id = d2b378e2`
    - `session_id = 174c3123-7d2e-48d7-9a97-28fe1ae00887`
    - `provider_session_id = bf2601b2-10be-46ad-b557-a3e2a71d54df`
    - `request_messages = 1`
- 结论：
  - 干净目录下串行 `claude -p/-c` 仍然是两个不同的 `provider_session_id`、两个不同的 canonical `session_id`。
  - 请求体不重放历史消息时，服务端仍无法把两轮无歧义地合并回同一 session。

### 6. 真实黑盒：同一 PTY 连续对话

- 目录：
  - `/tmp/claude-tty-prod`
- 方式：
  - 真实 TTY 交互启动 `claude --model gpt-5.4 --settings ...`
  - 第一轮输入：
    - `Reply with exactly TTY_OK`
  - 第二轮输入：
    - `What was my previous instruction? Reply with exactly TTY_PREV`
- CLI 实际输出：
  - 第一轮：
    - `TTY_OK`
  - 第二轮：
    - `TTY_PREV`
  - 退出时 CLI 给出：
    - `claude --resume 09294d3d-9e99-4fc2-9c21-a7ede257a04b`
- PG 核对结果：
  - 所有命中 `TTY_OK` / `TTY_PREV` 的请求都落到同一个 canonical session：
    - `session_id = 79447db5-280c-4870-9ff5-048673ae17ba`
    - `provider_session_id = 09294d3d-9e99-4fc2-9c21-a7ede257a04b`
    - `request_count = 10`
  - 其中成功的正文轮次：
    - `request_id = fbaced07`
      - `model = gpt-5.4`
      - `total_tokens = 21620`
    - `request_id = 7a776724`
      - `model = gpt-5.4`
      - `total_tokens = 137`
  - `token-rounds` 汇总：
    - `round_count = 10`
    - `input_tokens = 21719`
    - `output_tokens = 38`
    - `cached_tokens = 26368`
    - `total_tokens = 21757`
- 结论：
  - 同一 PTY 连续交互的主路径，在当前代码和当前环境下再次实测通过。
  - CLI 自身暴露的 resume session id 已被服务端捕获为 `provider_session_id`。

### 7. 最新上线判断

- 可以宣称已达到：
  - PG-first 会话轨迹存储可用
  - 管理端查询 / token-rounds / 导出接口可用
  - 浏览器侧管理接口验证通过
  - 全量 Go 测试通过
  - 同一 PTY 连续对话主路径可稳定追踪并归并
- 仍不能宣称已达到：
  - 干净 `claude/cc1 -p/-c` continue/resume 链路的生产级稳定归并
- 因此当前更准确的发布口径应为：
  - 可以按“主路径 = 同一 PTY 连续交互”的范围上线
  - 但必须把 `-p/-c/--resume` 继续对话的归并能力标记为已知边界，不纳入本次生产承诺
