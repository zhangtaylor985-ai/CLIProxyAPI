# 2026-04-04 PG 存储生产前测试计划与结果

## 目标

- 面向 PG-only 生产上线前，验证 `model_prices` / `api_key_model_daily_usage` / `usage_events` / `daily limiter` / `management usage` 相关链路。
- 覆盖自动化测试、管理端接口验证、预算/限额行为验证、真实 Claude CLI 黑盒回归。
- 所有默认数据库连接以当前仓库 `.env` 为准；自动化 PG 测试仅使用同一 DSN 下的临时 schema，避免污染正式业务表。

## 环境基线

- 仓库：`/Users/taylor/code/tools/CLIProxyAPI-ori`
- 当前日期：`2026-04-04`
- PG 配置来源：仓库根目录 `.env`
- 关键实现现状：
  - 运行时 `billing store` 仅初始化 PG，不再有 SQLite fallback
  - `daily limiter` 仅初始化 PG，不再有 SQLite fallback
  - `usage statistics` / `API 详细统计` / `model prices` 均通过 `billingStore` 从 PG 持久化数据重建
  - 查询结果仍可能叠加少量内存 pending usage overlay，这不是 SQLite fallback

## 测试范围

### 1. 自动化回归

- `internal/billing`
  - `model_prices` 默认值/覆盖值
  - `api_key_model_daily_usage` 成本累计
  - `usage snapshot` 重建
- `internal/policy`
  - PG `daily limiter` 持久化与多次 consume 行为
- `internal/api/handlers/management`
  - PG 管理端：group、model prices、usage statistics、session trajectories
- `internal/api/middleware`
  - request-time policy：日限额、日预算、周预算、token package、预算回放
- `internal/usage` / `internal/usagedashboard` / `internal/usagetargets`
  - usage 聚合、dashboard 统计口径

### 2. 手工/集成验证

- 本地启动 PG-only server
- 用临时配置压低预算与限额，验证：
  - 首次请求后 `usage_events` 与 `api_key_model_daily_usage` 是否写 PG
  - `model_prices` 是否参与 cost 计算
  - 次轮请求是否正确触发日限额 / 日预算 / 周预算拒绝
  - 管理端 usage / api-key-records 是否反映 tokens / cost / recent events

### 3. 真实 CLI 黑盒验证

- 使用 `claude -p` 代替当前 shell 中不存在的 `cc1`
- 通过 `--settings` 注入：
  - `ANTHROPIC_BASE_URL`
  - `ANTHROPIC_AUTH_TOKEN`
- 以最小 prompt 执行：
  - 成功请求落库验证
  - 预算或限额命中验证

## 通过标准

- 自动化测试全部通过，无新增失败
- PG 临时 schema 集成验证中：
  - usage event 成功入库
  - daily aggregate 成功累计
  - management usage 与 api-key-records 返回值和 PG 一致
  - 命中的限额/预算返回符合预期
- 真实 CLI 至少完成一轮：
  - 成功请求
  - 命中限制后的失败请求

## 执行记录

### 自动化回归

- 使用当前仓库 `.env` 中的 PG DSN 作为 `TEST_POSTGRES_DSN`，在临时 schema 下执行。
- 已执行：
  - `go test ./internal/billing ./internal/policy ./internal/usage ./internal/usagedashboard ./internal/usagetargets ./internal/api/middleware -count=1`
  - `go test ./internal/api/handlers/management -count=1 -run 'TestPostgresManagement_GroupCRUDAndRecordBudgets|TestPostgresManagement_UsageStatisticsAndModelPrices' -v`
  - `go test ./internal/api/handlers/management -count=1 -run 'TestHistoricalDashboardTimestampAt_' -v`
  - `go test ./internal/api/handlers/management -count=1 -run 'TestPostgresManagement_SessionTrajectoriesQueryAndExport' -v`
- 结果：
  - `internal/billing` 通过
  - `internal/policy` 通过
  - `internal/usage` 通过
  - `internal/usagedashboard` 通过
  - `internal/usagetargets` 通过
  - `internal/api/middleware` 通过
  - `internal/api/handlers/management` PG 定向用例通过

### 手工/集成验证

- 临时配置：
  - 文件：`/tmp/cliproxyapi-pg-e2e-20260404_215228.yaml`
  - 临时 schema：`it_20260404_215228`
  - 空 `auth-dir`：`/tmp/cliproxyapi-pg-e2e-auth-20260404_215228`
- 启动命令：
  - `go run ./cmd/server -config /tmp/cliproxyapi-pg-e2e-20260404_215228.yaml -local-model`
- 启动日志确认：
  - `api key config store enabled (postgres)`
  - `api key policy daily limiter enabled (postgres)`
  - `billing store enabled (postgres)`
  - `api key group store enabled (postgres)`
  - `session trajectory store enabled (postgres)`
  - `API server started successfully on: 127.0.0.1:18317`
- 实际建表确认：
  - `api_key_config_store`
  - `api_key_groups`
  - `model_prices`
  - `api_key_model_daily_usage`
  - `usage_events`
  - `session_trajectory_*`
- 真实请求成功落库：
  - HTTP 请求成功返回 `PG_E2E_OK`
  - `usage_events` 中出现成功事件：
    - `api_key = sk-pg-e2e-pass`
    - `model = gpt-5.4`
    - `total_tokens = 160`
    - `cost_micro_usd = 813`
    - `failed = false`
  - `api_key_model_daily_usage` 聚合到：
    - `requests = 3`
    - `failed_requests = 2`
    - `total_tokens = 160`
    - `cost_micro_usd = 813`
  - 管理端 `/v0/management/usage` 与 `/v0/management/api-key-records/:apiKey` 返回值和 PG 一致
- 价格来源确认：
  - `model_prices.gpt-5.4`
    - `prompt_micro_usd_per_1m = 2500000`
    - `completion_micro_usd_per_1m = 15000000`
    - `cached_micro_usd_per_1m = 250000`

### 真实 CLI 黑盒验证

- 当前 shell 无 `cc1` 别名，改用等价 `claude -p`。
- 成功链路命令：
  - `claude -p 'Reply with exactly CLI_PG_OK' --model claude-sonnet-4-6 --output-format text --tools '' --no-session-persistence --permission-mode bypassPermissions --settings '{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:18317","ANTHROPIC_AUTH_TOKEN":"sk-pg-e2e-pass","DISABLE_AUTOUPDATER":"1"}}'`
- 成功结果：
  - CLI 返回 `CLI_PG_OK`
- 日模型限额首轮命令：
  - `claude -p 'Reply with exactly CLI_LIMIT_OK_1' ... ANTHROPIC_AUTH_TOKEN=sk-pg-e2e-daily-limit`
- 日模型限额首轮结果：
  - CLI 返回 `CLI_LIMIT_OK_1`
- 日预算首轮命令：
  - `claude -p 'Reply with exactly CLI_DAILY_BUDGET_1' ... ANTHROPIC_AUTH_TOKEN=sk-pg-e2e-daily-budget`
- 日预算首轮结果：
  - CLI 返回 `CLI_DAILY_BUDGET_1`
- 周预算首轮命令：
  - `claude -p 'Reply with exactly CLI_WEEKLY_BUDGET_1' ... ANTHROPIC_AUTH_TOKEN=sk-pg-e2e-weekly-budget`
- 周预算首轮结果：
  - CLI 返回 `CLI_WEEKLY_BUDGET_1`
- 限额/预算命中结果通过直接 HTTP 明确确认：
  - 日模型限额二次请求返回 `429`：
    - `{"error":{"message":"daily model limit exceeded","type":"rate_limit_error","code":"rate_limit_exceeded"}}`
  - 日预算二次请求返回 `429`：
    - `{"error":{"message":"daily budget exceeded","type":"rate_limit_error","code":"rate_limit_exceeded"}}`
  - 周预算二次请求返回 `429`：
    - `{"error":{"message":"weekly budget exceeded","type":"rate_limit_error","code":"rate_limit_exceeded"}}`

## 结果摘要

- 结论：
  - PG store / limiter / management / session trajectory 的自动化测试均通过。
  - 本地真实 server 在临时 PG schema 下可正常启动并落库。
  - 实际 HTTP / Claude CLI 请求可成功写入 `usage_events` 与 `api_key_model_daily_usage`。
  - `tokens` / `cost` / `model price` / `management api-key-records` 口径一致。
  - 日模型限额、日预算、周预算都能稳定返回正确的 `429` 和错误文案。

## 新发现与风险

- 风险 1：本地 `429` 预检拦截不会写入 `usage_events`
  - 现象：
    - 第二次命中的 `daily budget exceeded` / `weekly budget exceeded` / `daily model limit exceeded` 请求，没有新增 `usage_events` 行。
    - 对应 `api_key_model_daily_usage.failed_requests` 也不会增加。
  - 影响：
    - 当前管理端 usage / api-key-records 展示的是“进入下游/进入 usage persist 插件后的失败”，不是“所有被本地策略拦截的失败”。
    - 如果生产期望“本地 budget/limit 拦截也算 request event”，当前实现与该期望不一致。
- 风险 2：观察到 `billing usage persist` 间歇性超时告警
  - 现象：
    - 真实请求期间日志出现过：
      - `billing usage persist: failed to flush pending usage batch error=billing postgres: begin usage batch: timeout: context deadline exceeded`
      - `billing usage persist: failed to flush pending usage batch error=billing postgres: add usage event: timeout: context deadline exceeded`
  - 当前判断：
    - 成功事件随后仍可见于 PG，说明不是稳定性必现丢数。
    - 但这属于生产前必须继续压测和定位的信号，至少要确认：
      - 是 PG 连接/事务超时过短
      - 还是 flush 时序与并发导致的偶发写入抖动
- 风险 3：真实上游凭据可用性会影响黑盒回归稳定性
  - 现象：
    - 第一把 codex key 在真实请求中直接命中上游 `DAILY_LIMIT_EXCEEDED`
    - 允许跨凭据重试后，第二把 codex key 才成功完成请求
  - 影响：
    - 生产验收不能只看单 key，需要把“上游凭据轮换后 PG 是否仍正确计费与聚合”纳入验收口径

## 上线建议

- 可以认为：
  - PG-only 的核心存储、统计、管理查询、限额拒绝链路已经达到可上线验证水平。
- 但上线前建议补两项：
  - 补一轮针对 `usage_persist_plugin` flush timeout 的专项压测与日志排查
  - 明确产品口径：
    - “被本地 budget/limit 中间件拒绝的请求是否应该进入 `usage_events` / `failed_requests`”
    - 若答案是“应该”，当前实现需要补持久化与展示逻辑

## 风险关注点

- 真实 CLI 回归依赖上游可用 credential；若临时运行配置无法稳定命中有效上游，需要退回到 HTTP 直接请求验证预算/计费链路。
- `usage statistics` 查询会合并内存 pending overlay；验证时需要区分“已落 PG”与“查询结果可见”的时序。
- 当前 `.env` 指向共享 PG，虽然自动化测试使用临时 schema，但真实 server 集成验证若直接使用 `public` schema 会污染现有数据，必须改用临时 schema。
