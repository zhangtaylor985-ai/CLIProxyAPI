# PG Only 上线与迁移说明

本文针对当前仓库的 PG-only 版本。

目标：

- 生产运行时不再依赖 SQLite
- `api-keys` / `api-key-policies` / 日额度 / 计费 / usage event / group 全部走 Postgres
- 保留 SQLite 读取能力仅用于一次性迁移脚本
- 生产启动方式以 `go run` 为主，不要求 Docker

## 1. 当前迁移范围

已经迁移到 Postgres 的内容：

- `api-keys`
- `api-key-policies`
- `daily limit` 计数
- `daily-budget-usd` / `weekly-budget-usd` 的运行时消耗统计依赖数据
- `model_prices`
- `api_key_model_daily_usage`
- `usage_events`
- `api_key_groups`

运行时已经移除的 SQLite 实现：

- `internal/billing/sqlite_store.go`
- `internal/policy/sqlite_daily_limiter.go`

仍然保留 SQLite 的地方：

- 仅剩迁移脚本读取旧 SQLite 文件
- `modernc.org/sqlite` 仍是迁移脚本依赖，不再是生产运行时依赖

## 2. 仍然留在 config.yaml 的配置

当前没有迁移到 PG 的，仍由 `config.yaml` 管理：

- 服务基础配置：`host`、`port`、`tls`
- 管理端配置：`remote-management`
- 本地目录配置：`auth-dir`
- provider / upstream 配置：
  - `gemini-api-key`
  - `codex-api-key`
  - `claude-api-key`
  - `openai-compatibility`
  - `vertex-*`
  - 其他 provider 相关配置
- 日志、代理、重试、路由、debug 等全局行为配置
- OAuth / auth file 相关配置
- 非 API key 持久化配置项

当前设计是：

- `config.yaml` 仍然是全局配置文件
- 其中 `api-keys` 和 `api-key-policies` 会被 PG overlay 覆盖
- 管理端更新这两块时，会同时写回 `config.yaml` 与 PG，便于兼容现有配置流程

## 3. 生产前准备

建议至少准备：

- 一套独立 Postgres 库
- 一个专用 schema，例如 `billing`
- 原 `config.yaml` 备份
- 原 SQLite 文件备份

推荐环境变量：

```bash
export APIKEY_POLICY_PG_DSN='postgres://user:pass@127.0.0.1:5432/cliproxy?sslmode=disable'
export APIKEY_POLICY_PG_SCHEMA='billing'

export APIKEY_BILLING_PG_DSN='postgres://user:pass@127.0.0.1:5432/cliproxy?sslmode=disable'
export APIKEY_BILLING_PG_SCHEMA='billing'
```

当前代码会优先复用这两套 PG 配置；实际部署中建议两者指向同一库同一 schema。

## 4. 建库与建 schema

示例：

```bash
psql "$ADMIN_DSN" -v ON_ERROR_STOP=1 \
  -c 'CREATE DATABASE cliproxy;'

psql 'postgres://user:pass@127.0.0.1:5432/cliproxy?sslmode=disable' -v ON_ERROR_STOP=1 \
  -c 'CREATE SCHEMA IF NOT EXISTS billing;'
```

## 5. 从旧 SQLite 迁移数据

假设旧文件是仓库根目录下的 `api_key_policy_limits.sqlite`。

### 5.1 迁移 billing / usage 数据

```bash
go run ./scripts/migrate_billing_sqlite_to_pg.go \
  --sqlite ./api_key_policy_limits.sqlite \
  --pg-dsn 'postgres://user:pass@127.0.0.1:5432/cliproxy?sslmode=disable' \
  --pg-schema billing
```

会迁移：

- `model_prices`
- `api_key_model_daily_usage`
- `usage_events`

### 5.2 迁移 config.yaml 中的 api key 与 policy

```bash
go run ./scripts/migrate_api_key_config_yaml_to_pg \
  --config ./config.yaml \
  --pg-dsn 'postgres://user:pass@127.0.0.1:5432/cliproxy?sslmode=disable' \
  --pg-schema billing
```

会迁移：

- `api-keys`
- `api-key-policies`

### 5.3 迁移旧 daily limiter 计数

```bash
go run ./scripts/migrate_api_key_daily_limiter_sqlite_to_pg \
  --sqlite ./api_key_policy_limits.sqlite \
  --pg-dsn 'postgres://user:pass@127.0.0.1:5432/cliproxy?sslmode=disable' \
  --pg-schema billing
```

## 6. 生产启动

你当前的部署习惯是直接 `go run`，这在当前项目里是可行的。

示例：

```bash
export MANAGEMENT_PASSWORD='replace-with-strong-password'
export APIKEY_POLICY_PG_DSN='postgres://user:pass@127.0.0.1:5432/cliproxy?sslmode=disable'
export APIKEY_POLICY_PG_SCHEMA='billing'
export APIKEY_BILLING_PG_DSN='postgres://user:pass@127.0.0.1:5432/cliproxy?sslmode=disable'
export APIKEY_BILLING_PG_SCHEMA='billing'

go run ./cmd/server -config ./config.yaml
```

如果你希望减少远端模型探测影响，也可以按需使用：

```bash
go run ./cmd/server -config ./config.yaml -local-model
```

## 7. 启动后必须看到的日志

正常情况下应出现：

```text
api key config store enabled (postgres)
api key policy daily limiter enabled (postgres)
billing store enabled (postgres)
api key group store enabled (postgres)
```

如果 PG 没配好，服务虽然可能还能启动，但配额/预算/计费相关能力会不可用，这种状态不能视为迁移完成。

## 8. 生产验收步骤

### 8.1 管理接口验收

```bash
curl -sS \
  -H 'x-management-password: YOUR_PASSWORD' \
  http://127.0.0.1:8317/v0/management/api-keys | jq 'length'

curl -sS \
  -H 'x-management-password: YOUR_PASSWORD' \
  http://127.0.0.1:8317/v0/management/api-key-policies | jq 'length'

curl -sS \
  -H 'x-management-password: YOUR_PASSWORD' \
  http://127.0.0.1:8317/v0/management/api-key-records | jq 'length'
```

### 8.2 直接请求接口并检查 PG 落库

```bash
curl -sS -X POST http://127.0.0.1:8317/v1/messages \
  -H 'Authorization: Bearer YOUR_INBOUND_API_KEY' \
  -H 'content-type: application/json' \
  -H 'anthropic-version: 2023-06-01' \
  --data '{
    "model": "claude-sonnet-4-6",
    "max_tokens": 64,
    "messages": [
      { "role": "user", "content": "Reply with exactly: PG validation ok" }
    ]
  }'
```

检查 PG：

```bash
psql 'postgres://user:pass@127.0.0.1:5432/cliproxy?sslmode=disable' -P pager=off -c "
select requested_at, api_key, model, total_tokens, cost_micro_usd
from billing.usage_events
where api_key='YOUR_INBOUND_API_KEY'
order by requested_at desc
limit 5;"
```

### 8.3 Claude CLI 黑盒验收

实测中，单纯覆盖环境变量不一定稳定；建议直接用 `--settings` 注入临时 base URL。

```bash
claude -p 'Reply with exactly: PG validation ok' \
  --model claude-sonnet-4-6 \
  --output-format text \
  --tools '' \
  --no-session-persistence \
  --settings '{
    "env": {
      "ANTHROPIC_BASE_URL": "http://127.0.0.1:8317",
      "ANTHROPIC_AUTH_TOKEN": "YOUR_INBOUND_API_KEY",
      "DISABLE_AUTOUPDATER": "1"
    }
  }'
```

然后再次查 PG：

```bash
psql 'postgres://user:pass@127.0.0.1:5432/cliproxy?sslmode=disable' -P pager=off -c "
select api_key, model, day, requests, total_tokens, cost_micro_usd, updated_at
from billing.api_key_model_daily_usage
where api_key='YOUR_INBOUND_API_KEY'
order by updated_at desc
limit 5;"
```

### 8.4 前端验收

前端仓库：

```bash
cd /path/to/Cli-Proxy-API-Management-Center-ori
npm run type-check
npm run build
npm run preview -- --host 127.0.0.1 --port 4173
```

浏览器连接时填：

- API Base: `http://127.0.0.1:8317`
- Management Password: 生产管理密码

至少确认：

- 登录成功
- 仪表盘能加载管理数据
- 配额页能加载各 provider 卡片
- 不再出现未翻译 key

## 9. 2026-04-02 实测结论

本地已完成一轮真实 PG-only 验证：

- 后端 `go test ./...` 通过
- PG 定向测试通过
- 管理接口真实读写通过
- `/v1/messages` 真实请求后，`usage_events` 与 `api_key_model_daily_usage` 已写入 PG
- `claude` CLI 黑盒请求已打通，并确认 PG 计数增长
- 前端 `npm run type-check` / `npm run build` 通过
- 浏览器 UI 已成功登录到 PG-only 后端并读取配额页数据

## 10. 回滚说明

当前版本已经移除运行时 SQLite 实现，因此不存在“切回 SQLite runtime”的原地回滚路径。

如果必须回滚：

- 回滚到迁移前的旧代码版本
- 恢复旧 `config.yaml`
- 恢复旧 SQLite 文件
- 停止当前 PG-only 版本服务

如果只是数据迁移失败：

- 删除本次新建 PG 库或 schema
- 修正迁移命令后重新导入
- 不要在半迁移状态直接上线
