# GPT-5.5 Claude 完全兼容任务进度

## 2026-04-28 启动

- 负责人：Codex
- worktree：`/Users/taylor/code/tools/CLIProxyAPI-gpt55-claude-parity-sse`
- 分支：`codex/gpt55-claude-parity-sse`
- 基线：`origin/main` at `df89929f`

### 已确认

- 生产默认 Claude -> GPT target family 当前已固定 `gpt-5.4`。
- 本任务只处理显式选择 `gpt-5.5` 的兼容闭环。
- 新线上样本不是“会话完全不可恢复”，而是单轮流式响应在 `response.completed` 前断流；后续“继续”可恢复。

### 初步判断

- 历史 PG session trajectory 能证明失败轮的下游形态：`usage=0`、`stop_reason=null`、半截 tool_use。
- 但当前常规 session trajectory 不足以还原“未归一化的上游原始 SSE 每一行”。
- 下一步需要补默认关闭的 raw SSE 诊断落盘，供下一次复现精确判断根因。

## 2026-04-28 Raw SSE 诊断能力

已实现：

- 新增环境变量 `CLIPROXY_CODEX_RAW_SSE_LOG_DIR`：
  - 未设置时完全不写 raw SSE 诊断日志。
  - 设置后，每个 Codex HTTP streaming upstream attempt 写一个 `codex-raw-sse-<request_id>-*.log`。
- 新增环境变量 `CLIPROXY_CODEX_RAW_SSE_MAX_BYTES`：
  - 限制单个 raw SSE 诊断文件最大字节数。
  - 默认 `50MiB`。
- 诊断文件只记录：
  - request id
  - model
  - upstream HTTP status
  - raw upstream SSE line
  - scanner error
  - EOF 与是否见到 completion event
- 诊断文件不记录请求 header、auth token 或 upstream request body。

已通过测试：

```bash
NO_PROXY=127.0.0.1,localhost,::1 no_proxy=127.0.0.1,localhost,::1 \
HTTP_PROXY= HTTPS_PROXY= http_proxy= https_proxy= \
go test ./internal/runtime/executor \
  -run 'TestCodexExecuteStream_(RawSSEDiagnostics|AcceptsResponseDone|ReturnsErrorWhenStreamEndsBeforeCompleted)' \
  -count=1

NO_PROXY=127.0.0.1,localhost,::1 no_proxy=127.0.0.1,localhost,::1 \
HTTP_PROXY= HTTPS_PROXY= http_proxy= https_proxy= \
go test ./internal/runtime/executor -count=1
```

后续补充通过：

```bash
NO_PROXY=127.0.0.1,localhost,::1 no_proxy=127.0.0.1,localhost,::1 \
HTTP_PROXY= HTTPS_PROXY= http_proxy= https_proxy= \
go test ./internal/runtime/executor ./internal/translator/codex/claude \
  ./sdk/api/handlers ./sdk/api/handlers/claude -count=1

NO_PROXY=127.0.0.1,localhost,::1 no_proxy=127.0.0.1,localhost,::1 \
HTTP_PROXY= HTTPS_PROXY= http_proxy= https_proxy= \
go test ./... -count=1
```

## 2026-04-28 GPT-5.5 黑盒回归

本地服务：

- 使用本分支编译出的 `bin/cliproxyapi`。
- 工作目录、`.env`、`config.yaml` 复用源仓库 `/Users/taylor/code/tools/CLIProxyAPI-ori`。
- 开启 `CLIPROXY_CODEX_RAW_SSE_LOG_DIR=/tmp/cliproxy-gpt55-raw-sse-20260428`。
- 本地访问显式绕过全局代理。

已通过：

- `claude2 -p --model gpt-5.5 --output-format text --tools ''`
  - 返回 `GPT55_MINIMAL_OK`
  - raw SSE 见到 completion event
- `claude2 -p --model gpt-5.5 --output-format stream-json --verbose --include-partial-messages --tools ''`
  - JSONL 可解析
  - 最终 `type=result`
  - usage 非零
  - raw SSE 见到 completion event
- 同一真实 TTY 多轮交互：
  - 简单回复：`TTY55CLEAN_R1_OK`
  - 记忆写入：`TTY55CLEAN_R2_OK`
  - 记忆追问：`TTY55CLEAN_R3_LIME_ONYX`
  - `Read` 工具读取 `/tmp/gpt55_tool_marker2.txt`：`TTY55_TOOL2_JADE`
  - 工具后继续追问：`TTY55_TOOL2_COPPER`
  - `Bash` 输出 2500 行后继续回复：`TTY55CLEAN_LARGE_TOOL_OK`

诊断结论：

- 新一轮干净 TTY debug-file 未出现：
  - `API Error`
  - `stream closed`
  - `stream disconnected`
  - `Failed to parse JSON`
  - `Connection error`
  - `invalid signature`
- 诊断目录内本轮 GPT-5.5 raw upstream SSE attempts 均为 HTTP 200，均见到 completion event，未见 scanner error，未触发日志截断。
- 早前同类大工具输出 TTY 中出现过一次 `Connection error`，已确认是测试 harness 的本地服务进程被会话生命周期结束导致，不计入 GPT-5.5 上游断流结论。

剩余风险：

- 历史线上失败样本无法从现有 PG session trajectory 还原未归一化的上游 raw SSE，只能证明下游形态为半截 tool_use + 缺失 completion。
- 当前新增的 raw SSE 诊断能覆盖下一次复现：可以区分上游真实 EOF、本地 scanner/framer 问题、网络或凭据断流。
- GPT-5.5 在某些工具轮仍可能先输出普通 assistant 文本再发起 tool_use；本轮干净 TTY 未复现，但此前样本复现过一次。它不是这次 `response.completed` 缺失硬错误，但仍属于 Target CC-Parity 的 transcript 污染风险，需要后续专项收敛。
