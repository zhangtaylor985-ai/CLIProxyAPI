# Codex Worker、缓存与冷却策略

本文记录当前主程序接入 Codex worker 的运行方式，以及会话缓存和失败冷却策略。

## Worker 架构

当前生产架构把 Codex 文件型 auth 从主程序中拆出，放到独立 worker VPS 上运行。

- 主程序服务器：`204.168.245.138`
- worker 服务器：`178.105.98.15`
- worker 服务器运行多个 Docker 容器，每个容器只挂载一个 Codex auth file 和一份独立配置。
- 每个 worker 容器绑定一个独立代理。
- 主程序不直接保存这些 Codex 文件型 auth，而是把每个 worker 当作一个 OpenAI-compatible provider 接入。
- 主程序通过 worker 主动建立的 SSH 反向隧道访问 worker 容器，不直接开放 worker 容器公网端口。

这套架构的目标是把不同 Codex auth file 的运行环境、配置、用量统计和失败状态拆开，避免主程序内单实例直接复用同一批 auth file 导致互相影响。

## 会话绑定策略

主程序必须启用 session affinity，才能让同一会话稳定落到同一个 worker。生产配置应包含：

```yaml
routing:
  strategy: "round-robin"
  session-affinity: true
  session-affinity-ttl: "1h"
```

启用后，主程序会从请求里提取会话身份，例如：

- Claude Code 的 `metadata.user_id`
- 显式 `X-Session-ID`
- 请求里的 `conversation_id`
- 无显式会话时，根据消息内容计算的稳定 hash

同一个会话会优先固定到同一个 worker。绑定 key 是：

```text
provider + session_id
```

这里不包含模型名。原因是同一个会话在不同轮次可能会出现 `gpt-5.4(high)`、`gpt-5.4(medium)` 这类模型后缀变化，但仍然应该尽量留在同一个 worker 上，避免无意义切换。

如果已绑定的 worker 进入冷却或不可用，调度器会选择新的可用 worker，并把这个会话重新绑定到新 worker。

如果没有启用 `routing.session-affinity`，请求会继续按普通轮询分散到多个 worker。由于 prompt cache 又按 worker/auth 隔离，同一客户的连续请求就会跨 worker 打散，表现为缓存命中率低、输入 token 成本偏高。

## Prompt Cache 策略

Codex 的 prompt cache 只允许在同一个 worker/auth 内复用。缓存 key 的逻辑范围是：

```text
auth isolation key + base model + 会话/用户身份
```

其中：

- `auth isolation key` 由 auth ID、provider、label、proxy URL、base URL、compat name、provider key、API key hash 等信息组成。
- `base model` 会去掉 thinking/effort 后缀，例如 `gpt-5.4(high)` 和 `gpt-5.4(medium)` 都归到 `gpt-5.4`。
- 会话/用户身份来自 Claude Code 的 `metadata.user_id` 或 OpenAI 路径中的 API key / prompt cache 信息。

因此缓存命中规则是：

```text
同 worker + 同 base model + 同会话 => 复用 cache
不同 worker => 不共享 cache
不同 base model => 不共享 cache
```

如果同一个会话原来绑定 `worker02`，后来 `worker02` 失败并切到 `worker05`，新请求会使用 `worker05` 的 auth isolation key。由于 cache key 已变化，旧的 `worker02` prompt cache 会自然失效，不会跨 worker 复用。

这就是“worker 失败才切换，切换后接受 cache 损失”的实现方式。

## 滚动缓存策略

长会话不能永远只用同一个 prompt cache key。否则第一次命中的稳定前缀可能只有早期的十几 K token，后续会话继续增长时，新增的大段历史不会自动进入更大的缓存层，表现为 `input_tokens` 持续上涨，但 `cached_tokens` 长时间停在同一个数值。

当前 Claude 到 Codex 路径使用滚动缓存：

- 同一个会话仍然固定在同一个 worker 上。
- 同一个缓存层仍然按 `auth isolation key + base model + 会话/用户身份` 隔离。
- 初始请求使用第 0 代 cache key。
- 只有当上游已经返回过 `cached_tokens > 0`，说明当前 cache key 已经真正命中过，才允许滚动升级。
- 当 `input_tokens + cached_tokens` 相比上一次滚动点增长超过约 `16k` token 时，生成下一代 cache key。
- 低于阈值时继续复用当前 cache key，避免频繁换 key 造成缓存还没暖好就失效。
- 如果 worker 失败并切换，新 worker 会重新从自己的缓存层开始，旧 worker 的缓存不跨 auth 复用。

这套策略的目标是让长会话逐步把更长的稳定前缀放进缓存，而不是永远只省最早一小段 token。它不会让每一轮立刻都满命中；升级新 cache key 后需要后续请求把新一代缓存暖起来。

## 冷却策略

Codex worker provider 使用整 worker/auth 级冷却，而不是单模型冷却。

原因是当前每个 worker 容器本质上代表一个 Codex auth file。对这类 worker 来说，一个模型请求出现 429、认证错误、上游不可用或连续空流，通常表示这个 auth/worker 当前不适合继续接流量。继续把同一个 worker 分配给其他模型名，容易造成更多用户请求失败。

当前策略：

- `codex-worker*` 失败时，主程序对整个 worker/auth 设置不可用状态和 `NextRetryAfter`。
- 冷却期间，这个 worker 不参与任何 Codex 模型调度。
- 成功请求会清除这个 worker 的 auth 级错误状态。
- 普通非 worker OpenAI-compatible provider 仍保留原来的模型级状态，避免误伤其他真实多模型供应商。

## 解决的问题

这套策略主要解决三个问题：

1. 同一会话频繁切 worker，导致 prompt cache 命中率下降。
2. 某个 worker 已经失败或冷却，却继续接其他模型名请求，导致线上出现连续失败。
3. 不同 worker/auth 之间误共享 prompt cache key 或 Conversation ID，造成跨 auth 状态污染。
4. 长会话只命中早期缓存层，后续输入越来越长但缓存 token 不继续增长。

最终效果是：同一个会话稳定留在同一个可用 worker 上；worker 正常时尽量吃到 cache；worker 失败时及时切走，并明确接受这次 worker 切换带来的 cache 损失。

## 观测口径

- 检查主程序配置：`routing.session-affinity` 必须为 `true`。
- 检查日志：开启后应能看到 `session-affinity: cache hit`、`cache miss, new binding` 或 `cache hit but auth unavailable, reselected`。
- 检查单个客户用量：同一会话的成功请求应主要集中在一个 worker；只有 worker 冷却或失败时才切到其他 worker。
- 检查缓存命中：`cached_tokens / input_tokens` 应随同一会话连续请求逐步上升；若请求分散到多个 worker，命中率通常会偏低。
