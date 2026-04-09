# 账户组与 Postgres 架构设计

## 1. 目标

本轮改造解决三类问题：

- 为 API Key 引入可管理的账户组，统一承载日额度与周额度。
- 修正额度扣减顺序：基础额度优先，流量包兜底。
- 将用量、价格、事件与账户组定义迁移到 Postgres，降低 SQLite 在多实例与复杂账本场景下的限制。

## 2. 设计结论

### 2.1 账户组

账户组定义落到 Postgres 表 `api_key_groups`，而不是继续放在 `config.yaml`。

原因：

- 账户组属于业务配置，不属于路由执行层的静态大配置。
- 需要支持 UI 直接增删改查。
- 后续可能需要更多组元数据，例如状态、备注、排序、系统组标记。

API Key 与账户组的绑定仍放在现有 `api-key-policies` 中，通过 `group-id` 字段关联。

原因：

- 当前 API Key 策略体系已经完整接入配置热更新与管理端保存。
- 不一次性把全部 API Key 策略从配置体系迁出，降低改造风险。

### 2.2 扣减规则

统一规则：

- API Key 先消耗所属账户组的基础额度。
- 基础额度同时受每日额度与每周额度约束。
- 单次请求的基础额度可用量为：
  - `min(单日剩余额度, 单周剩余额度, 本次请求费用)`
- 超出基础额度的部分，才记为流量包消耗。
- 流量包是 API Key 级别资产，不并入账户组。

这意味着：

- 同一天内超过日额度的部分会开始消耗流量包。
- 这部分不会继续占用周基础额度。
- 因而周基础额度会保留给后续日期继续使用。

## 3. Postgres 表结构

### 3.1 账户组

#### `api_key_groups`

字段：

- `id text primary key`
- `name text not null unique`
- `daily_budget_micro_usd bigint not null`
- `weekly_budget_micro_usd bigint not null`
- `is_system boolean not null default false`
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`

说明：

- 预算统一存 micro-USD，避免浮点误差。
- 系统组用于内置套餐保护，默认不允许删除。

预置系统组：

- 独享车：`300/day`，`1000/week`
- 双人车：`150/day`，`500/week`
- 三人车：`100/day`，`300/week`
- 四人车：`60/day`，`250/week`

### 3.2 模型价格

#### `model_prices`

字段：

- `model text primary key`
- `prompt_micro_usd_per_1m bigint not null`
- `completion_micro_usd_per_1m bigint not null`
- `cached_micro_usd_per_1m bigint not null`
- `updated_at bigint not null`

说明：

- 当前运行时实现统一使用 Unix 秒级时间戳，便于与现有 SQLite 账本迁移保持一致。

### 3.3 每日汇总

#### `api_key_model_daily_usage`

字段：

- `api_key text not null`
- `model text not null`
- `day text not null`
- `requests bigint not null`
- `failed_requests bigint not null`
- `input_tokens bigint not null`
- `output_tokens bigint not null`
- `reasoning_tokens bigint not null`
- `cached_tokens bigint not null`
- `total_tokens bigint not null`
- `cost_micro_usd bigint not null`
- `updated_at bigint not null`

约束：

- `primary key (api_key, model, day)`

索引：

- `(api_key, day)`

### 3.4 请求事件

#### `usage_events`

字段：

- `id bigserial primary key`
- `requested_at bigint not null`
- `api_key text not null`
- `source text not null`
- `auth_index text not null`
- `model text not null`
- `failed boolean not null`
- `input_tokens bigint not null`
- `output_tokens bigint not null`
- `reasoning_tokens bigint not null`
- `cached_tokens bigint not null`
- `total_tokens bigint not null`
- `cost_micro_usd bigint not null`
- `updated_at bigint not null`

索引：

- `(api_key, requested_at desc)`
- `(requested_at desc)`
- `(source, requested_at desc)`
- `(auth_index, requested_at desc)`

说明：

- 本阶段先保存原始事件费用。
- 基础额度与流量包分摊通过事件回放实时计算，不在本阶段额外落分摊列。
- 如果后续数据量继续上升，再增加分摊快照表或事件分摊列。
- 事件表暂不冗余存 `group_id`，因为当前组绑定仍存在 `api-key-policies` 配置中。

## 4. 基础额度与流量包计算

### 4.1 基础额度回放算法

对某个 API Key，从流量包起始时间开始按 `requested_at asc, id asc` 回放事件：

1. 找到事件所属日窗口。
2. 找到事件所属周窗口。
3. 计算当前日剩余额度。
4. 计算当前周剩余额度。
5. 本次基础额度覆盖值：
   - `base_covered = min(cost, day_remaining, week_remaining)`
6. 本次流量包覆盖值：
   - `package_covered = cost - base_covered`
7. 仅把 `base_covered` 计入日/周基础额度累计。

### 4.2 请求放行规则

请求进入前：

- 若当前基础额度仍有空间，则按基础额度放行。
- 若基础额度已无空间，但流量包剩余额度大于 `0`，则放行。
- 否则拒绝。

## 5. Redis 结论

本阶段不引入 Redis。

原因：

- 当前需求核心是精确账本与可审计回放，不是高吞吐热点缓存。
- Redis 会额外引入一致性、回放、补偿与持久化复杂度。
- 现阶段用户规模和请求规模，Postgres 足够承载。

Redis 只在以下场景再考虑：

- 多实例下需要极低延迟热点额度缓存。
- 需要大规模异步聚合与队列消费。
- 需要把事件回放结果做短 TTL 缓存以进一步降低查询压力。

## 6. 迁移范围

### 6.1 本轮落地

- 账户组表与管理 API
- 管理端 UI：账户组 CRUD 与 API Key 绑定选择
- API Key `group-id` 绑定
- 基础额度优先、流量包兜底
- Postgres billing store
- SQLite -> Postgres 迁移脚本

### 6.2 暂不迁移

- 全量 API Key 策略本体
- Auth 文件体系
- 配置主存储模式

## 7. 兼容策略

- 未配置 `group-id` 的 API Key，继续使用原有 key 级别 `daily-budget-usd` / `weekly-budget-usd`。
- 配置了 `group-id` 的 API Key，以组预算为准。
- `token-package-usd` / `token-package-started-at` 继续保留在 key 级别。
- 未配置 Postgres DSN 时，仍允许继续使用 SQLite billing store。

## 8. 风险点

- 基础额度与流量包从“绕过式逻辑”改为“回放式逻辑”，需要重点回归边界时间窗口。
- 同一个 API Key 若频繁切换组，历史请求仍按请求发生时的组预算语义计算，本阶段不做历史重写。
- 若要严格支持“组预算后来修改但历史不变”，后续需要增加组预算快照字段。

## 9. 迁移脚本

脚本：

- `scripts/migrate_billing_sqlite_to_pg.go`

示例：

```bash
go run ./scripts/migrate_billing_sqlite_to_pg.go \
  --sqlite /path/to/api_key_policy_limits.sqlite \
  --pg-dsn 'postgres://user:pass@host:5432/dbname?sslmode=disable' \
  --pg-schema cli_proxy
```

说明：

- 脚本会自动补齐 PG billing 表结构与默认价格。
- 脚本会自动创建 `api_key_groups` 表并写入 4 个系统账户组。
- 脚本会迁移 `model_prices`、`api_key_model_daily_usage`、`usage_events`。
- `usage_events.id` 会保留原 SQLite 主键，并在导入后修正 PG 序列。
- 推荐对新的 PG schema 执行首次迁移，避免和已有线上事件 ID 混用。
