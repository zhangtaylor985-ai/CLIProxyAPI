# 2026-05-14 Worker 滚动 Prompt Cache 回归与发布记录

## 结论

- worker 滚动 prompt cache 代码本地验证通过，但未保留在线上生产流量中。
- 生产当前仍运行官方镜像 `eceasy/cli-proxy-api:latest`。
- 候选镜像 `cliproxy-api-worker:ed0d6d74` 已构建在 worker VPS，可用于后续继续 canary，但不得直接全量切换。

## 候选版本

- 本地 worktree：`/Users/taylor/sdk/CLIProxyAPI-pure-worker-rolling-cache`
- 分支：`codex/worker-rolling-cache`
- 最新提交：`ed0d6d74 codex: add worker rolling prompt cache`
- 包含上一轮隔离提交：`7166f3b7 isolate codex cache and websocket sessions by auth`
- worker VPS 构建目录：`/root/cliproxy-workers/custom-image-ed0d6d74/`
- worker VPS 镜像：`cliproxy-api-worker:ed0d6d74`

## 本地验证

- `go test ./internal/runtime/executor -run 'TestCodexExecutorCacheHelper|TestApplyCodexPromptCacheHeaders'`：通过。
- `go test ./...`：通过。
- 本地真实 worker 黑盒：通过。
  - 启动 fake Codex `/responses` upstream。
  - 启动本地 worker `/v1/chat/completions`。
  - 非流式 4 次请求：第 1/2 次复用同一 `prompt_cache_key`，第 3 次滚动新 key，第 4 次稳定复用新 key。
  - 流式 4 次请求：同样符合滚动策略。
  - `Session_id` header 与 `prompt_cache_key` 对齐。

## 生产验证与回滚

- worker VPS canary：
  - `worker04` canary 真实请求返回 `CANARY_OK`。
  - `worker06` canary 真实请求返回 `CANARY_OK`。
  - `worker05` canary 返回 `usage_limit_reached`，判定为账号额度状态，不作为候选镜像失败依据。
- 正式全量切换：
  - 7 个 worker 曾短暂切到 `cliproxy-api-worker:ed0d6d74`。
  - `/v1/models` 均返回 200。
  - 线上观察窗口仍持续出现 `empty_stream: upstream stream closed before first payload`，主要集中在 worker03/worker04/worker06。
- 回滚：
  - 7 个正式 worker 已回滚到 `eceasy/cli-proxy-api:latest`。
  - 回滚后 `/v1/models` 均返回 200。
  - 回滚后官方镜像仍有同类 `empty_stream`，说明该问题不完全由候选镜像引入，但当前线上基线不干净，不适合再次全量上线滚动缓存。

## 后续建议

- 不要直接全量上线 `ed0d6d74`。
- 下一步应先排查持续 `empty_stream` 的根因：区分账号真实额度/冷却、worker 到 Codex 上游 streaming、主程序重试策略、以及高并发同 session 请求。
- 重新发布滚动缓存前，建议只把一个不承载客户主流量的专用 canary provider 接入主程序，用专用 API key 跑长会话缓存命中验证。

## 2026-05-14 稳定性修复计划

用户要求本轮不再采用临时线上试错方案，必须按长期稳定、可维护、可回归的方式处理。

当前判断：

- 部分 worker 不可用是账号额度或上游冷却状态，不应被视为主程序代码错误。
- 但主程序必须做到：坏 worker 被快速隔离，可用 worker 继续承接请求；不能让一个坏 worker 持续把客户请求打成 `empty_stream` / `model_cooldown`。
- worker 滚动缓存是第二阶段优化；在稳定性基线不干净前，不再全量上线 worker 自定义镜像。

工程计划：

1. 只读确认生产配置与 worker 状态：主程序 retry / session-affinity / provider 列表、worker 直连流式探测、主程序日志中每个 worker 的失败分布。
2. 本地复现调度路径：构造多个 codex-worker provider，其中部分返回 `usage_limit_reached`、部分返回 `empty_stream`、部分成功，验证请求能否自动绕开坏 worker。
3. 修复主程序长期策略：额度类错误进入整 worker 长冷却；首包前空流进入整 worker 短冷却并可 failover；会话粘性只在绑定 worker 可用时复用。
4. 补单测覆盖：同会话绑定坏 worker 后会重新选择好 worker；多个坏 worker 与至少一个好 worker 并存时不向客户暴露 5xx；全员冷却时才返回明确 cooldown。
5. 上线前回归：定向 Go 测试、相关包测试、本地黑盒流式测试；通过后才允许走 GitHub 主线发布和 systemd 部署。
6. 上线后观察：按 worker 统计 `empty_stream`、`model_cooldown`、`usage_limit_reached`、session-affinity reselect，确认可用 worker 被持续使用。

## 2026-05-14 稳定性排查结论

生产只读探测结果：

- 主程序配置：`request-retry=3`、`max-retry-credentials=0`、`routing.strategy=round-robin`、`session-affinity=true`、`session-affinity-ttl=1h`。因此不是“只尝试一个 worker 就放弃”的配置错误。
- worker 直连流式探测：
  - `worker02`：`usage_limit_reached`，账号额度耗尽。
  - `worker03`：流式可启动。
  - `worker04`：流式可启动。
  - `worker05`：`usage_limit_reached`，账号额度耗尽。
  - `worker06`：流式可启动。
  - `worker07`：`usage_limit_reached`，账号额度耗尽。
- 结论：当前至少 `worker03/04/06` 是可用 worker；生产不能整体不可用。

已定位的主程序策略问题：

- Codex worker 需要按“整个 worker/auth”冷却，而不是按单个模型冷却。
- 额度耗尽类错误应继续整 worker 长冷却。
- 但 `empty_stream: upstream stream closed before first payload` 属于首包前流式毛刺，不应第一次出现就把整个 worker 踢出可用池。
- 在只有 3 个可用 worker、并发较高时，如果每个可用 worker 偶发一次 `empty_stream` 就被短冷却，容易出现短窗口内 3 个好 worker 都不可选，客户看到 `model_cooldown` 或 5xx。

本地修复方向：

- 给 whole-auth worker 增加 auth-level transient health 计数。
- Codex worker 第一次 `empty_stream`：记录 streak，但 worker 仍可选；同请求仍允许一次短 jitter 重试。
- Codex worker 连续 `empty_stream` 达阈值：整 worker 短冷却。
- 成功请求：清空连续 `empty_stream` streak。
- 额度耗尽/401/403/404 等非空流错误：仍按整 worker 冷却处理，不降低保护力度。

当前本地验证：

- `go test ./sdk/cliproxy/auth -run 'TestManagerMarkResult_CodexWorker|TestManagerExecuteStream_CodexWorker|TestManagerExecuteStream_RetriesEmptyStream|TestSessionAffinitySelector'`：通过。

## 2026-05-14 第二阶段稳定性根因

第一阶段整 worker/auth 冷却策略上线后，生产仍看到少量客户侧 `empty_stream before first payload`。继续对照请求 ID 与 provider 日志后确认：

- 这些请求已经选到可用 worker，例如 `worker03`。
- worker 侧有时不是用非 2xx HTTP 状态返回失败，而是先建立 `text/event-stream`，再在第一条 SSE `data:` 中返回 JSON 错误，例如 `{"error":{"message":"empty_stream: upstream stream closed before first payload"}}`。
- 主程序 OpenAI-compatible executor 原先只在 HTTP 非 2xx 或 scanner IO 错误时返回 `StreamChunk.Err`。
- 当错误藏在 200/SSE 的 `data:` 帧里时，主程序会把它当成普通流内容交给 translator/handler，导致上层 auth manager 无法执行既有的重试、冷却和 worker failover。

长期修复策略：

- 在 OpenAI-compatible streaming executor 中显式识别 SSE 错误帧。
- 识别到 `data: {"error":...}` 或 `data: {"type":"error","error":...}` 时，转换成带 HTTP 状态语义的 executor error。
- 该 error 保留原始 `empty_stream` 文案，使上层 `providerErrorToResultError` 能继续归类为首包空流。
- 正常 `chat.completion.chunk` 和 `[DONE]` 不受影响。
- 这样 worker 返回方式无论是非 2xx HTTP，还是 200/SSE 错误帧，都会走同一套生产调度策略：请求内 failover、auth-level transient health、连续失败短冷却、额度耗尽长冷却。

新增本地验证：

- `TestParseOpenAICompatStreamErrorFromErrorPayload`：覆盖 `data: {"error":...}`。
- `TestParseOpenAICompatStreamErrorFromTypedErrorPayload`：覆盖 `data: {"type":"error","error":...}` 和状态码透传。
- `TestParseOpenAICompatStreamErrorIgnoresNormalChunk`：确认普通 chunk / `[DONE]` 不被误判。
- `TestOpenAICompatExecuteStreamErrorFrameEmitsChunkError`：用本地 `httptest` 模拟 worker 200/SSE 错误帧，确认 executor 输出 `StreamChunk.Err`，从而可被上层调度器重试和切换 worker。

上线前回归：

- `go test ./internal/runtime/executor ./sdk/cliproxy/auth -run 'TestParseOpenAICompatStreamError|TestOpenAICompatExecuteStreamErrorFrameEmitsChunkError|TestManagerExecuteStream_CodexWorkersFailOverToAvailableWorker|TestManagerExecuteStream_CodexWorker'`：通过。
- `go test ./sdk/cliproxy/auth ./internal/runtime/executor ./internal/watcher/synthesizer ./internal/config`：通过。
- `NO_PROXY=127.0.0.1,localhost,::1 no_proxy=127.0.0.1,localhost,::1 go test ./...`：通过。
- 本地 Claude 客户端黑盒：`~/.local/bin/claude` + `~/.claude_local` + `http://127.0.0.1:53841` 已确认真实命中本地 `/v1/messages`；但本地配置不是生产 worker provider 拓扑，当前返回 503 供应不可用，因此只作为“客户端命中本地”证据，不作为 worker 生产可用性验收。生产 worker 验收以上线后 systemd 服务、生产日志与 worker provider 分布为准。
