# 2026-03-30 Claude CLI 稳定性后续待办

## 未完全收口

### 1. `resume` 首轮长尾

已观测样本：

- `tasks/blackbox-samples/2026-03-30-claude-cli-vscode/claude-cli/stability-2026-03-30/2026-03-30T232040-negation-fix-check/summary.tsv`

现象：

- `resume-negated-turn-1` 最终成功
- 但耗时达到 `463s`

当前判断：

- 更像长尾时延，而不是中途断流

后续应补：

- request log 级别定位首个 `resume` 轮次慢在哪一段
- 区分是 Claude CLI 自身调度、代理首包慢，还是 tool 执行长尾

### 2. 继续积累真实长会话样本

目标：

- 继续观察是否存在极低频“聊着聊着突然断掉”
- 不要只依赖短会话或单轮样本

建议：

- 优先使用“同一 PTY 会话连续多轮交互 + transcript 落盘”的方式
- 保持使用同一目录继续追加：
  - `tasks/blackbox-samples/2026-03-30-claude-cli-vscode/claude-cli/stability-2026-03-30/`
- 额外保留本轮真实交互样本：
  - `tmp/cc-long-session-20260331.typescript`

### 3. `stream-json` 事件退化 / 最终正文整段晚吐出

2026-03-31 新观察：

- 当前 `cc` 与 `cc2` 的真实样本都主要输出：
  - `system`
  - `assistant`
  - `user`
  - `result`
- 历史同版本 `claude_code_version=2.1.76` 样本曾出现大量：
  - `stream_event`
  - `content_block_delta`

当前判断：

- 这不是本地未提交热修单独引入的问题
- 进一步对照发现：
  - 同一服务、同一 prompt、同一 CLI 版本下
  - 只要加 `--include-partial-messages`
  - 就会立即恢复 `stream_event content_block_delta`
- 因此 `cc` 已可通过 wrapper 在 print + stream-json 场景条件带该参数收敛
- 剩余边界主要是裸 `claude -p --output-format stream-json` 的默认聚合输出
- 服务端层面不应擅自把它当作默认行为补上

后续应补：

- 继续确认是否存在“即使带了 `--include-partial-messages` 仍然整段晚吐出”的极端样本
- 若再出现，优先保留：
  - 原始 `stream-json`
  - `.cli-proxy-api/logs/v1-messages-*.log`
  - 具体命令行参数

### 4. 继续观察旧链路低频 `invalid signature` 复现条件

2026-03-31 本轮结论：

- 本地 `cc` 热修后，多轮 `resume` 样本未复现该错误
- 远端 `cc2` 在本轮短样本里也未稳定复现

当前判断：

- 更像低频长会话问题
- 可能与 extended thinking、特定会话历史形态、或 tool_result 邻接 turn 有关

后续应补：

- 专门积累：
  - 长会话
  - 多次 `resume`
  - 含复杂工具链路的失败样本
- 一旦复现，优先保留：
  - 原始 `stream-json`
  - request log
  - 对应 session id

### 5. `cc` 与 `codex` 的持续对照

当前判断：

- `cc` 与 `codex` 都能完成复杂仓库分析和定向测试任务
- 但 `codex` 的结构化轨迹更天然稳定，复盘成本更低
- `cc` 的体验波动更多集中在：
  - CLI 输出模式
  - 长会话长尾
  - 复杂任务中的阶段可见性

后续应补：

- 固定一组复杂黑盒题，持续比较：
  - wall time
  - 首包时间
  - 工具阶段提示质量
  - 最终答案稳定性

### 6. 收窄 `thinking` replay 后继续积累同一 PTY 样本

2026-04-02 新变化：

- 已把 `sanitizeThinkingHistory` 收窄到“只保留最新 `tool_result` 正在延续的 assistant trajectory”

后续应补：

- 同一 PTY 连续多轮长会话样本，观察是否出现新的 400 / 上下文错位
- `resume` / `continue` 补充样本，确认没有因为过度收紧而重新引入问题

### 7. Web search 中文意图与搜索开始阶段仍需继续收敛

2026-04-02 当前判断：

- 已补真实 Claude CLI 中文边界样本：
  - 负样本 `不要联网 / 不要 web search` 未误触发
  - 正样本 `请帮我搜索一下` 会真实触发 `Web Search(...)`
- 搜索结束后在同一 PTY 继续追问也已验证可行
- 当前主要风险已不再是 matcher 明显误判，而更偏向：
  - 搜索回合长尾
  - 中途可见性偏弱
  - 等待窗口不足时 CLI 会把下一轮追问排成 queued message

后续应补：

- 继续积累更复杂的中文混合 prompt：
  - repo-search + 明确否定联网
  - 自主中途搜索而非显式 `请帮我搜索`
- 除非再观测到新的真实误判，否则不要继续热修 matcher
- 优先研究是否要在不污染 transcript 的前提下，改善 `web_search_call added` 或搜索长尾阶段的前台可见性

### 8. 防止 `sanitizeThinkingHistory` 回归性放宽

2026-04-02 当前判断：

- 旧风险点不是“没保留 thinking”，而是“保留得过宽”
- 一旦回退到“所有后面紧邻过 `tool_result` 的 assistant 都保留 thinking”，就会把更多 model-bound thinking 带回长会话历史
- 这类副作用平时不容易立刻炸，但更容易在长会话 / fallback / replay 时变成低频协议问题

后续应补：

- 保持当前只保留“最新 `tool_result` 正在延续的 assistant trajectory”的边界，不要再放宽
- 后续若必须改 `sanitizeThinkingHistory`，至少补：
  - 完整 tool trajectory 结束后旧 thinking 被清掉的单测
  - blank signature 仍会被剥离的单测
  - 同一 PTY 连续多轮真实回归
- 评审时优先检查：
  - `internal/runtime/executor/claude_executor.go`
  - `internal/runtime/executor/claude_executor_test.go`

### 9. 防止 Claude CLI transcript 再次混入假工具进度文本

2026-04-02 当前判断：

- 之前在 `tool_use` 前注入 `Reading relevant files.` 这类普通 assistant 文本，属于高风险假进度
- 它不是 UI 提示，而是会进入真实 transcript、占 token，并影响后续模型行为
- 这类问题短样本不一定明显，但在同一 PTY 多轮对话里会持续累积

后续应补：

- 保持 Claude CLI 路径不再注入普通 assistant 文本型工具进度
- 如果要改善前台静默，只能优先考虑：
  - wrapper / UI 层展示
  - 基于真实事件的 out-of-band 可见性
- 后续若有人想恢复这类文案，必须先说明：
  - 为什么不能在 UI 层解决
  - 文本是否进入 transcript
  - 对同一 PTY 多轮历史和 token 的副作用
  - 回滚边界与黑盒样本
