# Target CC-Parity

## 定义

`Target CC-Parity` 指当前项目在 `claude cli` 兼容链路上的最终目标：

- 稳定性尽可能接近 `codex` 上的 GPT 体验
- 真实终端内同一 PTY 会话的连续多轮对话、长时间聊天、复杂工具调用，不因代理层兼容问题中断、错乱或明显退化
- 流式过程可见性尽可能真实、连续、不过度伪造
- 整体体验尽可能接近 `codex`：响应节奏、工具阶段感知、连续追问承接、长会话稳定性都不明显掉队
- 最终达到可生产使用的稳定性，而不是“偶尔可用”的实验状态
- `claude cli`、`claude vscode`、`codex_exec vscode` 三条体验分流清晰，不互相污染

## 成功标准

### P0 稳定性

- 不再复现 `invalid signature in thinking block`
- 不因兼容层中转导致 `502 Upstream request failed`
- 否定式 prompt 不误触发 `web search` 进度或工具路径
- Claude 原生上游请求在 `Execute` / `ExecuteStream` / `CountTokens` 三条路径都保持一致清洗策略
- 真实终端同一 PTY 会话内连续多轮追问、长时间聊天、复杂工具调用时，不因兼容层问题出现异常中断、上下文错位、协议错误或明显长尾失控

### P1 体验

- `claude -p --output-format stream-json --verbose` 下，过程可见性尽量接近历史稳定样本
- 工具调用有真实、简短、不过度噪声的阶段提示
- 最终正文不应长期退化成“前面沉默很久，最后一次性整段吐出”
- 多轮追问时上下文承接自然，不出现明显“上一轮还能聊，下一轮就突然失忆或行为异常”
- 长时间真实使用后，稳定性仍应接近 `codex`，而不是只在短样本里看起来正常
- 客户端未显式请求 partial message 时，服务端不做私自默认化；相关体验修正应收敛在客户端 wrapper 层

### P2 回归纪律

- 每次兼容层变更都有：
  - 定向单测
  - `cc` 黑盒回归
  - `codex` 参考对照
  - 必要时再做 `cc2` 外部参考回归
- `resume` / `continue` 只作为补充恢复链路覆盖项，不作为主验收结论来源
- 每次任务结束都更新 `tasks/` 与 `todos/`

## 当前阶段方针

### Phase 1. Hotfix First

- 先修硬错误与明显误判
- 方案优先保守，避免放大协议副作用

### Phase 2. Streaming Parity

- 定位 `stream-json` 从 `stream_event` 退化到 `assistant/user/result` 聚合消息的原因
- 明确是 CLI 自身模式变化、代理返回形态变化，还是兼容翻译链路退化

### Phase 3. Long Session Soak

- 持续积累“同一 PTY 会话连续多轮交互”的真实终端长会话样本，作为主验收基线
- `resume` / `continue` 样本仅作为补充恢复链路验证，不替代主验收
- 重点观察真实长会话中断、复杂命令调用稳定性、长尾、上下文承接质量、CLI / VSCode 差异，以及补充恢复链路表现

### Phase 4. Release Gate

- 只有当阶段目标、验证矩阵、剩余风险都清楚时，才在 `releases/` 记录版本

## 每次改动的最低检查项

- 写清楚改动目标与潜在副影响
- 跑定向 `go test`
- 做至少一组真实 `cc` 回归
- 做至少一组 `codex` 对照
- 涉及长会话问题时，至少补一组同一 PTY 会话的连续多轮 soak 样本，作为主样本
- 仅在需要验证恢复能力时，再补 `resume` / `continue` 补充样本
- 判断是否达到阶段目标时，以真实终端连续多轮样本结论为准
- 结果补到 `tasks/`
- 未收口项补到 `todos/`
