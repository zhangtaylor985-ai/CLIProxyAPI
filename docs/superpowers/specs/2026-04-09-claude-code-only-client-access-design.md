# Claude Code Only Client Access Design

## 背景

当前管理端已经支持：

- 全局运行时配置开关
- API Key 级别的显式策略与有效策略
- 基于请求头与请求体的 API Key 中间件限制

本次要新增一层“客户端访问约束”，主要目标不是防御恶意伪造，而是避免普通用户直接拿 API Key 通过通用 API / SDK 访问与观察接口行为。

## 目标

- 新增“仅允许 Claude Code 客户端访问”的能力。
- 默认对全局开启。
- 支持每个 API Key 单独覆盖全局。
- 全局关闭时，单个 API Key 可以单独开启限制。
- 全局开启时，单个 API Key 可以单独关闭限制。
- UI 管理端可管理全局开关与 API Key 覆盖开关。

## 本期范围

本期只支持 `Claude Code`。

明确不纳入本期：

- OpenCode
- OpenClaw
- 其他 IDE/SDK/脚本客户端
- 基于签名、mTLS、设备证明等强身份认证

后续如果要扩展到多客户端白名单，应在后端保留可扩展结构，但 UI 和产品语义先只暴露 `Claude Code`。

## 用户语义

### 1. 全局开关

新增全局布尔开关：

- 名称：`claude-code-only-enabled`
- 默认值：`true`

语义：

- `true`：默认所有 API Key 仅允许 Claude Code 客户端访问，除非该 API Key 显式关闭该限制。
- `false`：默认所有 API Key 不启用该限制，除非该 API Key 显式开启该限制。

### 2. API Key 覆盖

新增 API Key 级别的三态覆盖：

- `inherit`：跟随全局
- `enabled`：该 API Key 仅允许 Claude Code 客户端访问
- `disabled`：该 API Key 不启用该限制

这样可以完整表达：

- 全局关，单 Key 开
- 全局开，单 Key 关

## 配置与持久化设计

### 全局配置

全局开关进入当前共享配置体系，和已有全局开关保持一致：

- 写入 `internal/config` 的全局配置结构
- 通过 management basic config API 读写
- 通过 `/config` 返回给管理端
- 通过现有 config reload / save 机制持久化

### API Key 策略

在 `APIKeyPolicy` 中新增三态字段，建议使用指针布尔表达显式覆盖：

- `ClaudeCodeOnly *bool`

语义：

- `nil`：inherit
- `true`：enabled
- `false`：disabled

原因：

- 和当前 `EnableClaudeModels`、`EnableClaudeOpus1M` 的模式一致
- 可直接复用现有 explicit/effective policy 处理方式

## 运行时判定

### 1. 有效策略计算

对任意 API Key，运行时需要得到一个最终布尔值：

- `RequiresClaudeCodeClient(apiKey) bool`

规则：

1. 若 API Key 显式设置 `ClaudeCodeOnly != nil`，使用其值。
2. 否则回退到全局 `claude-code-only-enabled`。

### 2. 客户端识别

本期只识别 Claude Code。

识别应比当前单纯 `User-Agent` 前缀更严格，但仍保持可解释、低副作用。

建议规则：

- 主匹配：`User-Agent` 以 `claude-cli/` 开头
- 辅助约束：请求路径属于 Claude Code 实际使用路径，例如：
  - `/v1/messages`
  - `/v1/models`
- 辅助信号：当存在 Claude 常见请求头时提高置信度，但不把易变 header 作为硬依赖

不建议：

- 把 package version / runtime version 等易漂移 header 作为强必需条件
- 把 VSCode / 第三方包装层误判为 Claude Code

本期策略采取“保守放行真实 Claude CLI，保守拒绝通用 API 客户端”。

## 拒绝行为

当某个请求命中“仅允许 Claude Code 客户端访问”且客户端不符合判定规则时：

- 返回 `403 Forbidden`
- 错误文案简洁明确，例如：
  - `api key is restricted to Claude Code clients`

不伪装成鉴权失败，不返回 `401`，避免和 API Key 本身无效混淆。

## 管理端设计

### 1. 全局开关入口

在系统页新增一张卡片：

- 标题：仅允许 Claude Code 客户端
- 描述：开启后，默认只允许 Claude Code 使用 API Key；可在 API Key 策略里单独覆盖。

### 2. API Key 工作台入口

在 API Key 工作台新增一张策略卡：

- 字段：Claude Code 客户端限制
- 形式：三选一
  - 跟随全局
  - 仅允许 Claude Code
  - 不限制客户端

这样比单一 toggle 更准确表达覆盖关系。

## 测试要求

### 后端

至少覆盖：

- 全局默认开启时，未显式覆盖的 API Key 拒绝普通 API 请求
- 全局默认开启时，显式关闭的 API Key 允许普通 API 请求
- 全局默认关闭时，未显式覆盖的 API Key 允许普通 API 请求
- 全局默认关闭时，显式开启的 API Key 拒绝普通 API 请求
- 符合 Claude Code 指纹的请求在限制开启时可通过
- `/v1/models` 也能被同样限制

### 前端

至少覆盖：

- 全局开关读写联通
- API Key 三态字段能正确显示 explicit/effective 值
- 提交 payload 时三态能正确映射为 `nil/true/false`

## 风险与边界

- 这是“客户端指纹约束”，不是强身份认证。
- 有能力伪造完整请求头的人，理论上仍可能绕过。
- 但对“直接拿 API Key 做通用 API 调试/观察”的常见场景，能显著提高门槛。
- 本期只做 Claude Code，避免把 OpenCode/OpenClaw 的不稳定识别过早做进生产逻辑。

## 回滚边界

若上线后发现误伤：

1. 可先在 UI 里关闭全局开关止血。
2. 或对关键 API Key 单独关闭限制。
3. 若需代码回滚，变更应集中在 client detection helper、API key middleware、management UI 三处，便于快速撤回。
