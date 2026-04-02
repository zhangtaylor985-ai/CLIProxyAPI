# 2026-04-02 PG 管理接口与 UI 回归记录

## 本轮目标

- 修复管理前端 `api key group store unavailable` / 用户组加载失败。
- 明确 `api-key-groups` 只走 PG，不再为该链路保留 SQLite fallback。
- 补齐 PG 定向测试，并用浏览器实测用户组、预算限额、使用统计页面。

## 代码结论

- 统一管理侧 PG 配置解析，`billing` / `api-key config store` / `daily limiter` / `api-key group store` 现在都通过同一组环境变量解析：
  - `APIKEY_POLICY_PG_DSN`
  - `APIKEY_POLICY_PG_SCHEMA`
  - 兼容旧的 `APIKEY_BILLING_PG_*` 与 `PGSTORE_*`
- 根因是此前 `groupStore` 没有识别 `APIKEY_POLICY_PG_DSN`，导致策略配置已启用 PG、但 group store 未初始化。
- 额外修复了使用统计时间窗问题：
  - PG 的按天聚合用量在转换为明细/看板条目时，过去固定落到当日 `23:59:59`
  - 这会让“当天”数据在默认 `24h` 视图下被当作未来时间过滤掉
  - 现在改为：历史天仍用日末；当天改为当前时刻

## 测试

- `go test ./internal/api/... ./internal/apikeygroup/... ./internal/policy/... ./internal/billing/...`
- `TEST_POSTGRES_DSN='postgres://postgres:root123@127.0.0.1:5432/postgres?sslmode=disable' go test ./internal/api/handlers/management -run 'TestPostgresManagement_|TestHistoricalDashboardTimestampAt_' -v`

## Chrome MCP 实测

- 前端：`http://localhost:5173`
- 后端：`http://127.0.0.1:8317`
- 管理密钥：本地临时值 `pg-pass-123`

### API Key 页面

- 用户组加载正常，不再出现 `api key group store unavailable`
- 能看到 5 个组，其中自定义组 `Team Alpha`
- `k-ui-1` 正常绑定 `Team Alpha`
- 预算与限额展示正常：
  - 组日额度 `$60`
  - 组周额度 `$180`
  - Token 包 `$30`
  - 模型限额 `gpt-5.4 = 100`
  - 当前已用 `3 / 100`

### 使用统计页面

- 请求明确打到 `http://127.0.0.1:8317/v0/management/usage`
- 修复后默认 `24h` 视图可直接看到 PG 数据
- 页面展示正常：
  - 总请求 `1`
  - 总 Tokens `4.5K`
  - 模型 `gpt-5.4`
  - 请求事件时间 `2026/4/2 12:16:14`

### 配额管理页面

- 页面加载正常，无接口报错
- 当前本地没有 Claude / Codex / Gemini CLI / Antigravity OAuth 凭证，所以只显示空态
- 该页面本轮未发现 PG 相关异常；它主要依赖认证文件与远端 quota 查询，不是本次 PG 存储链路核心

## 结论

- PG 路径下的用户组 CRUD、策略绑定、usage/quota 读取、管理前端展示已完成回归。
- 对 `api-key-groups` 而言，本轮按要求保持“无 DSN 不可用”，不再为该链路保留 SQLite fallback。
