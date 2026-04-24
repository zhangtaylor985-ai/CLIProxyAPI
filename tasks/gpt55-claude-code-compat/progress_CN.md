# GPT-5.5 Claude Code 兼容修复进度

## 2026-04-24 初始化

- 创建独立 worktree：
  - `/Users/taylor/code/tools/CLIProxyAPI-gpt55-claude-code-compat`
- 创建分支：
  - `codex/gpt55-claude-code-compat`
- 基线：
  - `origin/main` at `0c7a4d42`
- 当前目标：
  - 修复 `gpt-5.5` 在 `cc1` / Claude Code 兼容链路中的 stream 终止和 usage 兼容问题。

## 初步复现摘要

来源：2026-04-24 本地手工黑盒。

### GPT-5.5 失败样本

命令形态：

```bash
cc1 --debug-file <debug-log> \
  -p "Reply with exactly GPT55_CC1_OK" \
  --model gpt-5.5 \
  --output-format text \
  --tools ""
```

结果：

- exit code: `1`
- stdout: `undefined is not an object (evaluating '_.input_tokens')`
- debug log 关键线索：
  - `Stream started - received first chunk`
  - `Error streaming, falling back to non-streaming mode`
  - `stream disconnected before completion: stream closed before response.completed`
  - 后续出现 `auth_unavailable: no auth available`
  - 最终 `TypeError: undefined is not an object (evaluating '_.input_tokens')`
- server log 关键线索：
  - `gpt-5.5` 多个 Codex auth 先返回 401 并被 suspend
  - 最终请求返回 `408`
  - error body: `stream error: stream disconnected before completion: stream closed before response.completed`

初步判断：

- Claude CLI 看到的 `input_tokens` undefined 是次生错误。
- 服务端对 `gpt-5.5` upstream stream 缺失 `response.completed` 的处理方式不符合 Claude Code 稳定消费预期。

### GPT-5.4 对照样本

命令形态：

```bash
cc1 --debug-file <debug-log> \
  -p "Reply with exactly GPT54_CC1_OK" \
  --model gpt-5.4 \
  --output-format text \
  --tools ""
```

结果：

- exit code: `0`
- stdout: `GPT54_CC1_OK`
- debug log 出现正常 stream start，无上述 TypeError。

结论：

- 当前失败具有 GPT-5.5 专属性。
- GPT-5.4 是本次修复的强制不回归基线。

## 下一步

1. 在新 worktree 中复现并保存脱敏证据。
2. 对比 GPT-5.5 与 GPT-5.4 的原始上游 SSE 事件。
3. 写单测固化 `response.completed` 缺失和 usage 缺失场景。
4. 做最小修复。
5. 跑完整测试矩阵与真实 `cc1` 黑盒。

## 2026-04-24 线上用户 JSONL 与 Session DB 排查

来源：

- 用户反馈 JSONL：`1bb4ab9670d5076c892040721b6c0132.jsonl`
- Claude sessionId：`ace76e04-099c-42a9-8aec-c4a37f3d1e8d`
- Claude Code client：`2.1.114`
- entrypoint：`cli`
- cwd：`D:\workPro\react-h5-componet`
- JSONL 时间范围：`2026-04-24T02:23:29.635Z` 到 `2026-04-24T03:34:58.959Z`

JSONL 关键发现：

- 文件共有 469 行，JSON 解析错误为 0。
- 可见模型主要是 `claude-opus-4-7`，另有 3 条 `<synthetic>` assistant 错误。
- 没有字面量 `API Error: Failed to parse JSON`。
- 03:25:22 附近，`Agent` 工具返回错误：
  - `undefined is not an object (evaluating 'H.input_tokens')`
- 随后三次继续请求生成 synthetic assistant 错误：
  - `undefined is not an object (evaluating '$.input_tokens')`

Session DB 对齐：

- provider session id 映射到内部 session：`d37e7b54-9bca-465e-87eb-88ca87a8b440`
- DB 当前记录从 `2026-04-24 03:08:11+00` 开始，早于该时间的 JSONL 开头错误不在当前 session trajectory 覆盖范围内。
- 03:23 前主链路持续成功。
- 03:24 起出现 Agent 子任务相关失败：
  - `a0ecbd96`，`claude-sonnet-4-6`，`stream closed before response.completed`
  - 多条 `auth_not_found: no auth available`
  - `afb32ec2` / `7eb7e20e` / `a994b966`，`claude-opus-4-7`，同样为 `stream closed before response.completed`
- 多条“成功但空响应”记录包含：
  - `usage.input_tokens=0`
  - `usage.output_tokens=0`
  - `content=[]`
  - `stop_reason=null`

当前根因假设：

- 线上用户现象与本地 GPT-5.5 黑盒错误属于同一类客户端次生错误：Claude Code 收到不完整或异常的 stream 生命周期后，进入内部 usage 读取路径，最终暴露 `input_tokens` undefined。
- 代码层定位到 HTTP Codex executor 只认 `response.completed`；而同包 WebSocket executor 已经支持把 `response.done` 规范化为 `response.completed`。
- 因此 GPT-5.5 / 新 Codex 通道若在 HTTP SSE 里发出 `response.done`，当前 HTTP executor 会误判为缺少完成事件，向 Claude Code 推送终端错误。

已完成修复：

- `internal/runtime/executor/codex_executor.go`
  - HTTP non-stream / stream 路径均识别 `response.done`。
  - 在交给下游 translator 前，将 `response.done` 规范化为 `response.completed`。
  - 真正没有完成事件的流仍保持原有 408 诊断，不伪装成功。
- `internal/runtime/executor/codex_websockets_executor.go`
  - 抽出共享 `normalizeCodexCompletionPayload`，保持 WebSocket 现有行为不变。
- `internal/runtime/executor/codex_executor_fastmode_test.go`
  - 新增 `TestCodexExecuteStream_AcceptsResponseDoneAsCompleted`。

已通过测试：

```bash
go test ./internal/runtime/executor -run 'TestCodexExecute(Stream)?_.*(ResponseDone|BeforeCompleted)|TestCodexExecuteProviderFastModeSetsFastServiceTier' -count=1
go test ./internal/runtime/executor -count=1
```

下一步：

1. 补更贴近 Claude handler 的集成回归，确认 `response.done` 经过注册 translator 后会产出合法 Claude SSE lifecycle。
2. 跑 handler / translator 定向测试。
3. 准备本地当前源码 binary，执行 `cc1` 黑盒矩阵：`gpt-5.5`、`gpt-5.4`、Claude 映射路径。
4. 根据黑盒结果决定是否还需要处理“已开流后真实断流”的安全收尾策略。

## 2026-04-24 GPT-5.5 修复后黑盒回归

本地服务：

- 使用当前 worktree 重编译 `bin/cliproxyapi`。
- 使用临时脱敏外置测试配置启用 `gpt-5.5` / `gpt-5.4` 模型路由，并将 Claude target family 临时设为 `gpt-5.5`。
- 所有测试均通过本地 `ANTHROPIC_BASE_URL=http://127.0.0.1:53841` 命中当前修复版服务。

`claude2 -p` 黑盒结果：

- `--model gpt-5.5`，纯文本输出：通过，输出 `GPT55_CC1_OK`。
- `--model gpt-5.4`，纯文本输出：通过，输出 `GPT54_CC1_OK`。
- `--model claude-sonnet-4-6`，经 Claude -> GPT-5.5 映射：通过，输出 `CLAUDE_MAPPED_OK`。
- `--model gpt-5.5 --output-format stream-json --verbose`：通过，stdout 共 3 行合法 JSON，JSON parse errors 为 0，最终 result usage 非零。
- `--model gpt-5.5` + `Read` 工具：通过，工具读取后输出 `GPT55_TOOL_OK`。

同一 PTY 连续交互回归：

- 使用真实 TTY/tmux 驱动 `claude2 --model gpt-5.5`。
- 连续三轮 prompt：
  - 第一轮输出 `TTY55_ONE_OK`
  - 第二轮要求记忆 codeword，并输出 `TTY55_TWO_OK`
  - 第三轮追问上一轮 codeword，输出 `TTY55_THREE_BLUE_COPPER`
- debug log 命中多次 `source=repl_main_thread` 请求和正常 `Stream started`。
- 已扫描 debug / transcript，未发现：
  - `Failed to parse JSON`
  - `undefined is not an object`
  - `stream closed before response.completed`
  - `invalid signature`
  - `API Error`

同一 PTY 扩展严格回归：

- 重新使用真实 TTY/tmux 驱动 `claude2 --model gpt-5.5`，避免触发 Claude Code memory 写入流程造成断言扰动。
- 连续七轮同一会话：
  - 简单输出：`R1_SIMPLE_OK`
  - 上下文种子：`R2_SEED_SAFFRON`
  - 上下文追问：`R3_CONTEXT_SAFFRON`
  - `Read` 工具读取文件第一标记：`R4_TOOL_JADE_LANTERN`
  - 工具后继续追问同一文件第二标记：`R5_AFTER_TOOL_COPPER_HARBOR`
  - 长上下文局部计算：`R6_LONG_42`
  - 中文 prompt，明确禁止联网 / web search：`R7_CN_NO_SEARCH_OK`
- debug log 可见 `ANTHROPIC_BASE_URL=http://127.0.0.1:53841`、多次 `/v1/messages source=repl_main_thread` 与 `Stream started`。
- 独立扫描 debug / transcript，未发现：
  - `Failed to parse JSON`
  - `undefined is not an object`
  - `stream closed before response.completed`
  - `invalid signature`
  - `API Error`
  - `web_search` / `web_search_call`

当前上线判断：

- GPT-5.5 的 Claude Code 基础文本、stream-json、工具调用、Claude 模型映射、同一 PTY 多轮连续会话均已通过修复版黑盒。
- GPT-5.4 对照路径仍通过。
- 定向 Go 回归通过：
  - `go test ./internal/runtime/executor ./internal/translator/codex/claude ./sdk/api/handlers ./sdk/api/handlers/claude -count=1`
- 全量 Go 回归通过：
  - `go test ./... -count=1`
- 本地上线标准已满足；还需完成 git 变更审查，确认没有把临时测试配置、DB DSN 或 debug 产物纳入提交。

## 2026-04-24 Rebase 后最终验收

push 前按项目规则执行 `git fetch origin main`，发现远端 `origin/main` 已从 `0c7a4d42` 前进到 `7d76e677`。

已将当前修复 rebase 到最新 `origin/main`，无冲突。

rebase 后重新执行：

```bash
go test ./internal/runtime/executor ./internal/translator/codex/claude ./sdk/api/handlers ./sdk/api/handlers/claude -count=1
go test ./... -count=1
```

结果均通过。

rebase 后重新 build 当前源码二进制并执行轻量 GPT-5.5 黑盒：

```bash
claude2 --debug-file <debug-log> \
  -p "Return a token made from POSTREBASE, CLEAN, GPT55, and OK joined with underscores. Output only the token." \
  --model gpt-5.5 \
  --output-format text \
  --tools ""
```

结果：

- exit code: `0`
- stdout: `POSTREBASE_CLEAN_GPT55_OK`
- debug log 确认命中 `ANTHROPIC_BASE_URL=http://127.0.0.1:53841`
- debug log 可见 `/v1/messages` 与 `Stream started`
- 未发现：
  - `Failed to parse JSON`
  - `undefined is not an object`
  - `stream closed before response.completed`
  - `invalid signature`
  - `API Error`
  - `Connection error`
