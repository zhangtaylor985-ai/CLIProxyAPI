# GPT-5.5 Claude 完全兼容任务计划

负责人：Codex

## 目标

让显式选择 `gpt-5.5` 的 Claude Code / Claude CLI 用户，在当前 CLIProxyAPI 兼容链路上达到生产级稳定：

- 不再出现 `stream closed before response.completed`
- 不再出现 `Failed to parse JSON`
- 不再出现 `undefined is not an object`
- 不再出现半截 `tool_use` 污染 transcript
- 同一 PTY 多轮、长上下文、大工具输出、子任务和继续追问均稳定

生产默认策略保持不变：

- 默认 Claude -> GPT target family 继续固定 `gpt-5.4`
- 仅显式选择 `gpt-5.5` 的 key / 用户走 `gpt-5.5`

## 当前新样本

- JSONL：`412723a8-41d3-4e3e-b962-12b089a30b6a.jsonl`
- sessionId：`412723a8-41d3-4e3e-b962-12b089a30b6a`
- 客户端版本：`2.1.112`
- 失败轮：PG request_index=8，request_id=`63249d54`
- provider_request_id：`resp_0b2fc16dc196703e0169f074a7d81481918ee356ba93f46333`
- 现象：assistant 已产出文本和一个未完整 tool_use，随后 `response.completed` 前断流。

## 阶段拆分

### P0 证据链

1. 对齐 JSONL、PG session trajectory、服务端日志和客户端 debug-file。
2. 明确历史样本是否已有原始上游 SSE 可回溯。
3. 若不可回溯，落地下一次复现必抓的 raw upstream SSE 诊断能力。

### P1 诊断能力

1. 为 Codex HTTP SSE executor 增加默认关闭的 raw SSE 落盘。
2. 记录每个 upstream attempt 的：
   - request id
   - upstream response status
   - raw SSE line
   - scanner EOF / scanner error
   - 是否见到 `response.completed` / `response.done`
3. 诊断日志不得记录敏感 header / auth token。
4. 默认关闭，只有显式环境变量开启时写入本地诊断目录。

### P2 定向回归

1. 单测覆盖：
   - raw SSE 正常完成
   - raw SSE 出现 `response.done`
   - raw SSE 缺失完成事件后 EOF
   - scanner error
2. 现有 executor / translator / handler 定向测试不回归。

### P3 真实黑盒矩阵

1. `claude2 -p` 最小文本：
   - `gpt-5.5`
   - `gpt-5.4` 对照
2. `stream-json --verbose --include-partial-messages`：
   - JSON 可解析
   - usage 非零
   - 无 synthetic API Error
3. 同一 PTY 连续多轮：
   - 简单上下文记忆
   - 中文 prompt
   - 工具调用
   - 工具后继续追问
4. 长链路压力：
   - 50k+ cache / context
   - 大 `tool_result`
   - 多次 `Read` / `Bash`
   - tool_use 生成后继续一轮
5. 子代理 / Task 类路径：
   - Agent / TaskCreate / TaskUpdate
   - 结构化输出失败不污染主链路

### P4 退出标准

必须全部满足：

1. 本地定向 Go 测试通过。
2. 全量 Go 测试通过，或明确记录不能跑全量的原因。
3. GPT-5.5 黑盒矩阵连续多轮通过。
4. 失败复现时 raw SSE 能明确区分：
   - 上游真实 EOF
   - 本地 scanner / framer 问题
   - 网络 / 凭据连接问题
5. 仍未收敛前，不把 GPT-5.5 设为默认 Claude -> GPT family。
