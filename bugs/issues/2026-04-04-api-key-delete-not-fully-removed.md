# 2026-04-04 API Key 工作台删除不彻底，导致“已存在但无法鉴权”

## 现象

- 在前端 API Keys 工作台删除某条 API Key 后，再尝试添加同一条 key，界面提示 `api key already exists`。
- 但该 key 实际请求代理接口时又返回 `401 Invalid API key`。

## 本次定位结论

- 当前运行环境以 PG `api_key_config_store` 为 API Key 配置生效源。
- 某些 key 会残留在 `api_key_policies` 中，但已经不在 `api_keys` 鉴权列表中。
- 管理侧“已知 key”视图会把 `APIKeys + APIKeyPolicies` 合并展示，因此残留 policy 的 key 仍会被识别为“存在”。
- 实际鉴权只看 `APIKeys`，因此这类 key 又会被判定为 `Invalid API key`。

## 影响

- 前端/管理台对“是否存在”的判断口径，与运行时鉴权口径不一致。
- 用户会看到：
  - 工作台里该 key 似乎还“存在”
  - 重新添加时冲突
  - 实际请求时又无法使用

## 复现要点

1. 创建一条同时拥有 `api_keys` 身份与 `api_key_policies` 策略的 key。
2. 在前端 API Keys 工作台执行删除。
3. 删除后检查 PG：
   - `api_keys` 中已不存在
   - `api_key_policies` 中仍残留
4. 再次通过工作台添加同一 key，会出现“已存在”。
5. 使用该 key 请求 `/v1/models` 等接口，会返回 `401 Invalid API key`。

## 预期行为

- 删除 API Key record 时，应同时删除：
  - `api_keys` 中的注册身份
  - `api_key_policies` 中的对应策略
- 管理侧“已存在”判断与运行时鉴权判断应保持一致。

## 排查方向

- 核对前端 API Keys 工作台实际调用的是哪一个删除接口。
- 核对后端删除链路是否有分支只删除了 `api_keys` 或只删除了 policy。
- 若保留“policy-only key”能力，则前端展示和“已存在”判断必须明确区分：
  - `registered`
  - `policy-only`

## 临时修复建议

- 对已受影响的数据，直接在 PG `api_key_config_store` 中补齐或清理对应 key。
- 修改后重启服务，避免进程内缓存继续使用旧状态。
