# 2026-03-30 账户组与 PG 迁移进度

## 1. 目标

- 增加账户组能力。
- 修正基础额度与流量包扣减顺序。
- 将 billing 数据从 SQLite 迁移到 Postgres。

## 2. 已确认产品规则

- 一个 API Key 只能绑定一个账户组。
- 基础额度优先，流量包兜底。
- 账户组定义使用 Postgres 表管理。
- API Key 与组的绑定仍沿用现有策略配置体系，通过 `group-id` 关联。

## 3. 当前现状核对

### 3.1 SQLite 现状

- 文件：`api_key_policy_limits.sqlite`
- 体积：约 `20MB`
- `usage_events`：约 `62,249` 行
- `api_key_model_daily_usage`：约 `174` 行

### 3.2 当前策略规模

- `config.yaml` 中约 `98` 条 API Key policy

### 3.3 当前主要问题

- 预算配置是 API Key 扁平字段，缺少账户组抽象。
- 当前逻辑是流量包优先，和目标规则相反。
- SQLite 仅单连接写入，复杂账本逻辑和多实例扩展性较弱。

## 4. 实施拆分

### 阶段 A

- 文档与设计落地
- 账户组表与管理 API
- API Key `group-id` 字段接入

### 阶段 B

- 额度计算改为基础额度优先
- 流量包改为兜底消耗
- 管理端展示同步调整

### 阶段 C

- Postgres billing store
- SQLite -> Postgres 数据迁移脚本
- 启动时根据环境变量切换 SQLite / PG

## 5. 当前完成情况

- 已新增 `api_key_groups` PG 表与 4 个系统账户组 seed。
- 已新增管理 API：账户组列表、新建、编辑、删除。
- 已支持 API Key 策略绑定 `group-id`，并在详情接口返回组信息。
- 已将扣减逻辑改为“基础额度优先，流量包兜底”。
- 已完成 `Postgres billing store`，并支持运行时 `PG 优先、SQLite 回退`。
- 已新增迁移脚本：`scripts/migrate_billing_sqlite_to_pg.go`。
- 已完成前端工作台改造：账户组 CRUD、账户组选择、组预算接管提示。

## 6. 待回归项

- 日额度刚好耗尽
- 周额度刚好耗尽
- 同时存在日额度、周额度、流量包
- 流量包未来时间生效
- anchored weekly window
- 未绑定账户组的旧 key 回退逻辑

## 7. 风险边界

- 本轮不迁移全量配置主存储。
- 本轮不引入 Redis。
- 本轮账户组成员关系不单独入表，仍由 API Key policy 的 `group-id` 表示。

## 8. 本地真实迁移验证

- 本地创建数据库：`cliproxyapi_billing`
- 使用 schema：`billing`
- 迁移源文件：`/Users/taylor/code/tools/CLIProxyAPI-ori/api_key_policy_limits.sqlite`
- 使用脚本：`scripts/migrate_billing_sqlite_to_pg.go`

核对结果：

- SQLite `model_prices` = `10`，PG `billing.model_prices` = `10`
- SQLite `api_key_model_daily_usage` = `174`，PG `billing.api_key_model_daily_usage` = `174`
- SQLite `usage_events` = `62278`，PG `billing.usage_events` = `62278`
- SQLite 日汇总费用和 = `3824075408`，PG 日汇总费用和 = `3824075408`
- SQLite 事件费用和 = `3824085769`，PG 事件费用和 = `3824085769`
- SQLite 最新事件时间 = `1774881531`，PG 最新事件时间 = `1774881531`
- PG `billing.api_key_groups` 已写入 4 个系统组
- PG `billing.usage_events_id_seq` 已推进到 `62278`

推荐环境变量：

- `APIKEY_BILLING_PG_DSN=postgres://postgres:root123@localhost:5432/cliproxyapi_billing?sslmode=disable`
- `APIKEY_BILLING_PG_SCHEMA=billing`

## 9. 运行态联调验证

运行方式：

- 使用 `config.yaml`
- 运行端口：`53841`
- 注入环境变量：
  - `MANAGEMENT_PASSWORD=pg-e2e-20260330`
  - `APIKEY_BILLING_PG_DSN=postgres://postgres:root123@localhost:5432/cliproxyapi_billing?sslmode=disable`
  - `APIKEY_BILLING_PG_SCHEMA=billing`

验证结果：

- 运行中服务已切换到 PG billing store
- `GET /v0/management/api-key-groups` 返回 4 个系统组
- `GET /v0/management/api-key-records` 能返回已有账本数据，说明运行态已读到 PG 用量
- 管理 API 已验证账户组 CRUD
- 管理 API 已验证 `group-id` 绑定
- 管理 API 已验证“组被 API Key 引用时不可删除”
- 前端管理台已做 headless 登录联调
- 前端 `/#/api-keys` 页面已确认显示账户组区和 4 个系统组
- 前端已验证“新建账户组 -> 显示 -> 删除账户组”闭环
