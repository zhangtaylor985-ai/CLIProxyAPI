# GPT-5.5 Claude Code 兼容修复计划

## 目标

让 `gpt-5.5` 在当前 `Claude Code / cc1 -> CLIProxyAPI -> Codex/GPT` 兼容链路中达到 `gpt-5.4` 现有兼容水平。

上线后用户使用 `cc1`、Claude Code 交互式 TTY、`-p` 非交互、工具调用、多轮对话时，不应出现协议级硬错误、客户端解析崩溃、流式中断或次生 UI 异常。

## 工作隔离

- 工作目录：`/Users/taylor/code/tools/CLIProxyAPI-gpt55-claude-code-compat`
- 分支：`codex/gpt55-claude-code-compat`
- 基线：`origin/main` at `0c7a4d42`
- 原主工作区的黑盒日志、未提交 skill 改动与其他现场不进入本 worktree。

## 已知现象

初步黑盒结果：

- `cc1 -p "Reply with exactly GPT55_CC1_OK" --model gpt-5.5 --output-format text --tools ""`
  - 失败，退出码 `1`
  - stdout: `undefined is not an object (evaluating '_.input_tokens')`
  - Claude debug log: 先收到 stream chunk，随后报 `stream disconnected before completion: stream closed before response.completed`
  - CLIProxyAPI error log: 最终返回 `408` JSON error
- 对照：
  - `cc1 -p "Reply with exactly GPT54_CC1_OK" --model gpt-5.4 --output-format text --tools ""`
  - 成功，stdout: `GPT54_CC1_OK`

当前判断：

- 问题高度集中在 `gpt-5.5` 的 Codex Responses streaming 事件完整性、终止事件、usage 字段或服务端错误映射。
- `gpt-5.4` 作为兼容基线必须保持不回归。
- 不能把 Claude CLI 的 `undefined input_tokens` 当根因；它更像服务端返回了 Claude Code 无法稳妥处理的异常流/异常响应后的次生崩溃。

## 工作进度安排

### 阶段 0：证据与环境收敛

状态：已完成

工作项：

- 复现并保存最小证据链：
  - `cc1 -p` 的 stdout/stderr/debug log 摘要
  - CLIProxyAPI `logs/main.log` request id 摘要
  - error request log 摘要，敏感头与 token 必须脱敏
- 确认测试服务使用当前源码还是旧二进制，避免旧 binary 误导结论。
- 固定对照组：
  - `gpt-5.4` 必须通过
  - `gpt-5.5` 当前必须可复现失败
- 将原始大日志放入 `tasks/gpt55-claude-code-compat/artifacts/`，默认不提交；提交只保留脱敏摘要。

交付物：

- `progress_CN.md` 记录每次复现的命令、时间、结论和证据路径。

### 阶段 1：协议差异定位

状态：已完成

工作项：

- 对比 `gpt-5.4` 与 `gpt-5.5` 的上游 Codex SSE 原始事件序列：
  - 是否出现 `response.completed`
  - `response.completed.response.usage` 是否存在
  - `response.output_text.done` / `response.output_item.done` 是否完整
  - 是否存在 upstream status、finish reason、tool call 或 reasoning block 差异
- 对照 CLIProxyAPI 当前执行链：
  - `internal/runtime/executor/codex_executor.go`
  - `sdk/api/handlers/handlers.go`
  - `sdk/api/handlers/claude/code_handlers.go`
  - `internal/translator/codex/...`
  - `internal/translator/openai/claude/...`
- 判断错误属于哪一类：
  - 上游真实缺 `response.completed`
  - 上游有 `response.completed`，但本地 scanner/framer 丢失
  - translator 丢弃了终止事件或 usage
  - 已写入部分响应后又写 terminal error，导致 Claude CLI fallback 崩溃
  - auth 轮换与模型可用性把成功/失败路径混在同一轮

交付物：

- 根因分类结论。
- 需要修改的最小文件列表。
- 回滚边界。

### 阶段 2：修复设计

状态：已完成

设计原则：

- 优先保持 `gpt-5.4` 现有行为不变。
- 不伪造普通 assistant 文本进度，不污染 Claude transcript。
- 对 Claude Code stream，必须维持合法 Claude 消息生命周期：
  - `message_start`
  - `content_block_start/delta/stop`
  - `message_delta`，包含稳定 `usage.input_tokens` / `usage.output_tokens`
  - `message_stop`
- 如果上游在已经输出内容后缺少 `response.completed`：
  - 不能再把 `408` error 直接推给 Claude CLI 造成次生崩溃。
  - 只有在已有足够完成信号时，才考虑合成安全的 terminal event。
  - usage 优先来自上游；缺失时使用可解释的 fallback，并在日志中标记。
- 如果上游在首包前失败：
  - 继续使用现有 retry/auth failover 逻辑。
- 如果是账号/模型可用性问题：
  - 修 auth 选择和暂停逻辑，不用流式兼容层掩盖真实无可用账号。

候选修复方向：

1. Codex executor 层修复 GPT-5.5 缺失 terminal event 的处理。
2. Claude Code handler 层修复已提交 stream 后 terminal error 的安全收尾。
3. Translator 层补齐缺失 usage / stop event 的保守转换。
4. Auth manager 层避免 GPT-5.5 在已知不可用凭据间反复轮换。

最终只能选择能被黑盒和单测共同证明的最小组合。

### 阶段 3：实现

状态：已完成

工作项：

- 先补单测锁定当前失败模式。
- 再做最小代码修复。
- 保持日志可诊断：
  - request id
  - upstream model
  - auth id
  - 是否合成 terminal event
  - usage fallback 来源
- 不引入新 schema；除非最终根因明确需要持久化字段。

### 阶段 4：测试流程

状态：已完成

#### A. 单元与 handler 测试

必须覆盖：

- `gpt-5.5` 上游 stream 有文本输出但缺 `response.completed`
- 上游缺 usage
- 上游在首包前失败
- 上游在首包后失败
- 多 auth 中前几个 401，后一个成功
- 没有可用 auth 时保持明确错误，不伪装成功
- `gpt-5.4` 既有 `response.completed` 路径不变
- Claude Code stream 不出现状态码已提交后改写 terminal error 的回归

命令：

```bash
go test ./internal/runtime/executor ./internal/translator/... ./sdk/api/handlers ./sdk/api/handlers/claude
```

#### B. 后端全量回归

命令：

```bash
go test ./...
```

通过要求：

- 本次改动相关包无失败。
- 如出现历史 flaky，必须记录包名、错误、是否与本次改动相关；不能默默放行。

#### C. 本地二进制回归

必须用当前源码重编译 binary，再用 binary 启动本地代理：

```bash
go build -o bin/cliproxyapi ./cmd/server
./bin/cliproxyapi -config config.yaml
```

通过要求：

- 确认 `127.0.0.1:53841` 监听。
- 确认请求命中当前新 binary，而不是旧进程。

#### D. `cc1 -p` 黑盒矩阵

每条都需要 debug log + server log 证据。

```bash
cc1 --debug-file <path> -p "Reply with exactly GPT55_CC1_OK" --model gpt-5.5 --output-format text --tools ""
cc1 --debug-file <path> -p "Reply with exactly GPT54_CC1_OK" --model gpt-5.4 --output-format text --tools ""
cc1 --debug-file <path> -p "Reply with exactly CLAUDE_MAPPED_OK" --model claude-sonnet-4-6 --output-format text --tools ""
```

通过要求：

- stdout 精确匹配。
- exit code 为 `0`。
- debug log 无：
  - `undefined is not an object`
  - `Failed to parse JSON`
  - `stream closed before response.completed`
  - `invalid signature`

#### E. `cc1` 同一 TTY 多轮黑盒

必须覆盖：

1. 简单问答。
2. 上下文连续追问。
3. 工具调用：读文件 / 搜索文件。
4. 工具调用后继续追问。
5. 长 prompt 或较大上下文输入。
6. 中文 prompt，不应误触发 web search。

通过要求：

- 同一 TTY 至少 5 轮稳定完成。
- 无 CLI synthetic error。
- 无 JSON parse / input_tokens / speed / content undefined。
- session jsonl、debug log、server log 三者能对齐。

#### F. 流式格式专项

覆盖：

- `--output-format text`
- `--output-format stream-json --verbose`
- 必要时覆盖 wrapper 条件补 `--include-partial-messages` 的现有约束，不能服务端私自默认化。

通过要求：

- stream-json 每行可被解析。
- final message 完整。
- 不出现 partial/orphan/fallback 协议破坏。

#### G. 生产相邻风险

覆盖：

- billing usage 写入不阻塞主请求。
- session trajectory 记录正常，不泄漏敏感头。
- API key policy 的全局 Claude -> GPT 默认 GPT-5.5 路径正常。
- per-key 选择 GPT-5.4 时仍按 GPT-5.4 执行。
- 原生 Claude 失败后全局 fallback 路径不回归。

### 阶段 5：上线标准

状态：本地已达到，待发布后观察

达到以下全部条件才可上线：

- `gpt-5.5` 通过 `cc1 -p` 最小黑盒。
- `gpt-5.5` 通过同一 TTY 多轮黑盒。
- `gpt-5.5` 工具调用与工具后追问稳定。
- `gpt-5.4` 对照组全部通过。
- Claude 模型映射到默认 GPT-5.5 的路径通过。
- API Key 单独配置 GPT-5.4 的路径通过。
- 日志中没有新增：
  - `stream closed before response.completed`
  - `undefined is not an object`
  - `Failed to parse JSON`
  - `invalid signature`
  - 状态码已提交后的错误改写
- `go test ./...` 通过。
- 新增/修改的定向测试覆盖失败模式。
- 本地使用重编译 binary 验证通过。
- 若要发布生产：
  - 先 `git fetch origin main` 并确认不落后。
  - rebase 到最新 `origin/main`。
  - 重新跑必要测试。
  - 重编译 `bin/cliproxyapi`。
  - 再按 systemd 流程重启线上服务。

### 阶段 6：发布后观察

状态：待发布后执行

上线后观察窗口：

- 至少 30 分钟基础日志观察。
- 至少 1 次真实 `cc1` 生产链路 smoke。
- 检查 request log / main log 是否出现上述硬错误。
- 如出现新的 GPT-5.5 专属异常，立即回滚到 GPT-5.4 全局 target family 或回滚代码。

## 回滚方案

如果修复无法在安全时间内完成：

- 临时配置全局 `claude-to-gpt-target-family: "gpt-5.4"`。
- 前端保留 GPT-5.5 可选项，但生产默认切回 GPT-5.4。
- 保留本分支继续修 GPT-5.5 兼容。

如果代码上线后出现生产异常：

- 回滚本次提交。
- 或切回 GPT-5.4 全局 target family。
- 保留错误 request id、debug log 摘要和 session id，继续离线分析。
