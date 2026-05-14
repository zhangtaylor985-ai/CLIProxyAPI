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
- 生产继续观察后发现，真实 auth id 形如 `openai-compatibility:codex-worker03-...`，旧 `isCodexWorkerAuth` 只匹配以 `codex-worker` 开头的字段，导致生产 worker 未进入整 auth 冷却策略。修复为识别字段中包含 `codex-worker` 的生产 auth id，并补单测覆盖。

新增本地验证：

- `TestParseOpenAICompatStreamErrorFromErrorPayload`：覆盖 `data: {"error":...}`。
- `TestParseOpenAICompatStreamErrorFromTypedErrorPayload`：覆盖 `data: {"type":"error","error":...}` 和状态码透传。
- `TestParseOpenAICompatStreamErrorIgnoresNormalChunk`：确认普通 chunk / `[DONE]` 不被误判。
- `TestOpenAICompatExecuteStreamErrorFrameEmitsChunkError`：用本地 `httptest` 模拟 worker 200/SSE 错误帧，确认 executor 输出 `StreamChunk.Err`，从而可被上层调度器重试和切换 worker。
- `TestIsCodexWorkerAuthMatchesProductionOpenAICompatID`：覆盖生产真实 `openai-compatibility:codex-workerNN-...` auth id，确保整 worker/auth 冷却策略不会漏判。

上线前回归：

- `go test ./internal/runtime/executor ./sdk/cliproxy/auth -run 'TestParseOpenAICompatStreamError|TestOpenAICompatExecuteStreamErrorFrameEmitsChunkError|TestManagerExecuteStream_CodexWorkersFailOverToAvailableWorker|TestManagerExecuteStream_CodexWorker'`：通过。
- `go test ./sdk/cliproxy/auth ./internal/runtime/executor ./internal/watcher/synthesizer ./internal/config`：通过。
- `NO_PROXY=127.0.0.1,localhost,::1 no_proxy=127.0.0.1,localhost,::1 go test ./...`：通过。
- 本地 Claude 客户端黑盒：`~/.local/bin/claude` + `~/.claude_local` + `http://127.0.0.1:53841` 已确认真实命中本地 `/v1/messages`；但本地配置不是生产 worker provider 拓扑，当前返回 503 供应不可用，因此只作为“客户端命中本地”证据，不作为 worker 生产可用性验收。生产 worker 验收以上线后 systemd 服务、生产日志与 worker provider 分布为准。

生产发布记录：

- `d3ac1b77 fix: classify openai compat stream errors` 已发布后观察到仍有客户侧 `empty_stream` 外溢，继续定位发现生产真实 auth id 未命中 Codex worker 检测。
- `c1744f6f fix: detect codex worker auth ids` 已发布到主程序 VPS，`cliproxyapi.service` 重新编译并重启成功，健康探测通过。
- 上线后观察窗口自 `2026-05-14 03:30:00 UTC` 起，统计结果：`suppress=0`、`empty=0`、`model_cooldown=0`、`reselect=4`、`suspended=4`、`resumed=1`、`hit=90`、`miss=21`。
- 日志确认 `openai-compatibility:codex-worker03-...` 在 transient 后被 suspended，后续 `session-affinity` 对不可用 auth 执行 reselect 到其他 worker；符合预期。

继续观察后的第三个根因：

- 后续仍出现少量客户侧外溢，定位为 worker 已先返回 Claude/OpenAI 协议壳事件，例如 `message_start` / role-only chunk，但还没有真实内容 token；随后才返回 `empty_stream`。
- 旧 bootstrap 判断把任意非空 payload 都当作“流已开始”，导致协议壳之后的错误无法再 failover。
- 修复方向：bootstrap 阶段只把真正内容/工具增量算作 first activity；`message_start`、普通 `content_block_start`、role-only OpenAI chunk、`[DONE]` 不算 first activity。这样首个真实内容前的错误仍保留在可重试窗口内。
- 新增 `TestManagerExecuteStream_RetriesAfterProtocolOnlyChunkError`，覆盖“先协议壳、再 empty_stream、然后重试成功”的路径。
- `944cef4e fix: delay stream bootstrap until content` 已发布到主程序 VPS，`cliproxyapi.service` 重新编译并重启成功。
- 上线后 150 秒观察窗口自 `2026-05-14 03:40:24 UTC` 起，统计结果：`suppress=0`、`empty=0`、`model_cooldown=0`、`reselect=3`、`suspended=3`、`hit=47`、`miss=21`。当前观察符合预期。

## 2026-05-14 Worker 缓存命中二次修复与生产观察

用户提供新的 JSONL 后确认，客户侧 `cache_read_input_tokens` 曾长期停在 `18432`。复查发现：

- 主程序已上线会话粘性，但旧 worker 镜像只能在请求体存在 `metadata.user_id` 时按会话生成滚动缓存 scope。
- Claude -> OpenAI-compatible worker 链路不会把 Claude `metadata.user_id` 原样透传给 worker。
- 结果是 worker 退化成按 worker API key 级别共享 cache namespace，容易只命中 Claude Code 公共前缀，无法稳定滚动到客户会话自己的更长前缀。

已完成修复：

- 主程序提交 `3419889d fix: forward codex worker prompt cache identity` 已部署到生产。
  - 仅对 `codex-worker*` 的 Claude -> OpenAI-compatible 请求注入 opaque `prompt_cache_key`。
  - key 由 Claude 会话身份、base model、入站 API key hash 派生，不暴露明文客户身份。
- worker 提交 `09528a02 fix: honor forwarded prompt cache keys in codex workers` 已构建并全量替换 worker VPS 容器镜像 `cliproxy-api-worker:09528a02`。
  - worker 优先使用转发的 `prompt_cache_key` 作为滚动缓存身份。
  - OpenAI chat 和 WebSocket 入口均覆盖测试。
- 本地回归：
  - 主程序 `go test ./internal/runtime/executor ./sdk/cliproxy/auth` 通过。
  - worker `go test ./internal/runtime/executor` 通过。

生产调整：

- `cliproxy-worker01` 到 `cliproxy-worker08` 均已运行镜像 `cliproxy-api-worker:09528a02`。
- 主程序配置已摘除不健康或额度耗尽 worker：
  - 摘除：`worker01/02/05/07`。
  - 第一轮保留：`worker03/04/06/08`。
  - 二次观察发现 `worker08` 连续出现上游 `context canceled` / HTTP2 `INTERNAL_ERROR` 类 500，已在 `2026-05-14 09:30 UTC` 继续从主程序路由池摘除。
  - 当前保留：`worker03/04/06`。
- 主程序配置备份：
  - `config.yaml.bak.disable-worker01.20260514T090654Z`
  - `config.yaml.bak.disable-quota-workers.20260514T091606Z`
  - `config.yaml.bak.disable-worker08.20260514T092956Z`

观察结论：

- 旧 JSONL 的 `18432` 固定值发生在 worker 仍跑旧镜像阶段，不能代表新链路。
- 新链路上线后，PG `usage_events` 已出现多条明显大于 `18432` 的 `cached_tokens`，包括约 `37k`、`100k`、`139k`、`466k`、`481k`，说明滚动缓存已经开始生效。
- 从仅保留 `worker03/04/06/08` 后的窗口看，缓存继续有命中，但 `worker08` 有连续 500，不适合继续承载生产主流量。
- 摘除 `worker08` 后 90 秒观察窗口内，主程序服务 `active`，无 `empty_stream`、`auth_unavailable`、`model_cooldown`、`usage_limit`、HTTP2 `INTERNAL_ERROR` 外溢；`worker03/04/06` 的 DB 失败数均为 0，缓存比例约 33% 到 44%，最大单次命中约 `538k` cached tokens。
- 重启切流瞬间 worker 容器曾记录几条 500；按 `17:30:35 +08:00` 之后过滤，`worker03/04/06` 未继续出现错误。
- 仍需后续用客户新 session JSONL 复核：同一 session 在数轮后 `cache_read_input_tokens` 应不再长期固定在 `18432`。

## 2026-05-14 主程序滚动缓存修复

用户继续提供新的 JSONL 后，确认样本本身发生在主程序滚动缓存修复上线前；该样本中同一 session 固定在同一 worker/auth，但 `cache_read_input_tokens` 在短窗口内停在 `18432`。

根因修正：

- 上一版主程序只负责给 worker 转发一个稳定 `prompt_cache_key`。
- worker 已修复为“收到主程序转发 key 时不再二次 rolling”，避免主程序和 worker 两边同时升级 cache key。
- 因此 rolling generation 必须回到主程序侧完成，否则同一会话会长期复用同一个稳定 key，只能命中早期前缀。

已上线修复：

- 主程序提交 `ef1c8fe7 fix: roll codex worker prompt cache keys` 已发布到生产。
- 对 Codex worker OpenAI-compatible 请求，主程序按 `auth isolation key + base model + 入站 API key hash + 会话身份` 计算 rolling cache scope。
- 主程序观察 worker 返回的 usage；当 `input_tokens + cached_tokens` 相比上一滚动点增长超过约 `16k`，且已经看到过 `cached_tokens > 0` 后，升级下一代 `prompt_cache_key`。
- worker 继续只消费主程序转发的 key，不再对 forwarded key 二次 rolling。

本地回归：

- `go test ./internal/runtime/executor -run 'TestOpenAICompatExecutorCodexWorker|TestCodexExecutorCacheHelper|TestApplyCodexPromptCacheHeaders'`：通过。
- `go test ./internal/runtime/executor ./sdk/cliproxy/auth`：通过。
- `go test ./...`：通过。

生产发布与观察：

- 生产主程序已 fast-forward 到 `ef1c8fe7`，重新编译 `bin/cliproxyapi` 并重启 `cliproxyapi.service`，`/healthz` 探测通过。
- 上线后短窗口内，PG `usage_events` 已出现多条高于 `18432` 的 Codex worker 缓存命中，例如 `27136`、`94720`、`96768`、`143872`、`192000` 等，说明 rolling key 已经不再固定在旧的 `18432` 层。
- `worker01-08` 容器均在运行，反向隧道与防火墙 unit 均为 `active`。
- 上线后曾在 `10:38 UTC` 附近观察到 4 次 `gpt-5.4(high)` 首包前 `empty_stream`，集中在重启后早期窗口；之后短窗口内未继续刷同类 `empty_stream`。另有一次 `unknown provider for model` 非流式 503，不属于 worker 缓存链路。
- 当前仍需继续观察客户长会话：如果某个会话发生 worker failover，新的 worker 会重新建立缓存层级，短期 cache 下降属于预期；如果同一 worker/auth 内长时间仍只停在 `18432`，再继续按 session-affinity scope 与 usage 事件排查。

## 2026-05-14 滚动条件修正

继续按 K 客户 API key 前缀排查生产 `usage_events` 后发现：

- 全局已有多个 Codex worker 请求命中高于 `18432` 的缓存，说明主程序代码已生效。
- 但 K 客户部分请求出现 `18432 -> 0 -> 18432 -> 0` 的节奏。
- 这说明旧 rolling 条件过于激进：只要 `input_tokens + cached_tokens` 总 prompt 增长超过 16k 就换下一代 key，即使真实 `cached_tokens` 仍停在 `18432`。
- 对这类会话，频繁换 key 反而会制造冷启动，使新 key 还没暖好就被再次升级。

修正策略：

- rolling 不再按总 prompt 增长触发。
- 第一次看到 `cached_tokens > 0` 时只记录 baseline，不立即升级。
- 只有当真实 `cached_tokens` 相比上一滚动点增长超过约 `16k` token 时，才升级下一代 key。
- 如果 `cached_tokens` 一直停在 `18432`，继续复用当前 key，避免反复掉到 0。

本地修复范围：

- 主程序：`internal/runtime/executor/cache_helpers.go`、相关 executor cache tests。
- worker：`internal/runtime/executor/helps/cache_helpers.go`、相关 Codex HTTP / WebSocket cache tests。

当前本地回归：

- 主程序 `go test ./internal/runtime/executor -run 'TestOpenAICompatExecutorCodexWorker|TestCodexExecutorCacheHelper|TestApplyCodexPromptCacheHeaders|TestCodexWebsocketPromptCacheHeaders'`：通过。
- 主程序 `go test ./internal/runtime/executor ./sdk/cliproxy/auth`：通过。
- worker `go test ./internal/runtime/executor -run 'TestCodexExecutorCacheHelper|TestApplyCodexPromptCacheHeaders'`：通过。
- worker `go test ./internal/runtime/executor ./sdk/cliproxy/auth`：通过。
