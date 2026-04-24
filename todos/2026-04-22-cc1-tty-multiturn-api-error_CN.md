## 背景

- 日期：2026-04-22
- 验证方式：真实 `cc1` 同一 PTY 多轮 TTY 黑盒测试
- 参考旧样本：`/Users/taylor/Downloads/c25bffae-30dd-45f6-94cc-902af0b23d61(1).jsonl`

## 本轮结论

- 简单多轮问答已通过：
  - 第 1 轮：`Reply with exactly TTY_OK`
  - 第 2 轮：`What was my previous instruction? Reply with exactly TTY_PREV`
- 带当前本地修复版本再次回归后，旧的第 3 轮复现路径已不再复现：
  - 第 3 轮：`Review the recent changes in sdk/api/handlers/claude/code_handlers.go for regressions only. Be concise.`
  - 已经历多次 `Read/Grep/Bash` 工具往返、`task_reminder` attachment、继续推理与最终结论输出
  - 未再出现：
    - `API Error: Failed to parse JSON`
    - `undefined is not an object (evaluating 'eH.content')`
    - `stream disconnected before completion`

## 新鲜样本与当前通过样本

- 成功样本 session：
  - `~/.claude_local/projects/-Users-taylor-code-tools-CLIProxyAPI-ori/19b3616f-e6f7-4d0e-bb34-e333c67aead9.jsonl`
- 带本地补丁后的同 PTY 多轮通过样本：
  - `~/.claude_local/projects/-Users-taylor-code-tools-CLIProxyAPI-ori/6f60f603-cce6-44e5-a211-e7d03b0e7e8e.jsonl`
- 启动失败后恢复样本 session：
  - `~/.claude_local/projects/-Users-taylor-code-tools-CLIProxyAPI-ori/236009a2-ebf7-4d7f-972f-b08f7ed34ef8.jsonl`

## 关键证据

- 当前回归中，`/v1/messages?beta=true` 的同 PTY 多轮请求全部返回 `200`，且第三轮复杂工具调用最终正常完成：
  - `ba3c2347` -> `200` at 2026-04-22 17:18:52
  - `54ed7e47` -> `200` at 2026-04-22 17:19:16
  - `4aefc763` -> `200` at 2026-04-22 17:19:33
  - `988834c6` -> `200` at 2026-04-22 17:20:07
  - `6f60f603-...jsonl` 显示第三轮已完整经过：
  - 多次 `Bash/Read/Grep` 的 `tool_use/tool_result`
  - `task_reminder` attachment
  - 继续推理
  - 最终 assistant 结论与后续工具通知
- 在同一 PTY 紧接着继续追问中文 prompt：
  - `继续，用中文一句话说明刚才的结论，再补一句为什么这次不是 Failed to parse JSON。`
  - 也已正常完成，关键尾部为：
    - assistant 先发一条说明 + 本地通知工具
    - 再次经过 `task_reminder`
    - 最终正常输出中文正文并落 `turn_duration`
- 这说明“复杂工具调用完成后继续追问”在当前补丁上也已通过，不只是单轮 review prompt 通过。
- 客户端 debug-file `/tmp/claude-tty-53842.log` 中可见：
  - `[API REQUEST] /v1/messages source=repl_main_thread`
  - `Stream started - received first chunk`
  - 本轮未出现 `Failed to parse JSON` / `Unexpected end of JSON input` / `undefined is not an object`
- 这说明此前“复杂工具往返 + task_reminder 后崩掉”的主复现路径，在当前补丁集上已被实测打穿。

## 当前判断

- 已修复的三处问题共同覆盖了此前线上混合症状的主入口：
  - `/v1/messages` 首包前错误体 schema 不兼容
  - `response.completed` 前断流却被误当成功流关闭
  - `Agent` 子代理 `response_format/json_schema` 映射缺失
- 当前主复现路径在真实 `cc1` 同 PTY 黑盒中已通过，进入“扩大回归覆盖”阶段，而不是继续停留在“稳定复现”阶段。
- 当前同一 PTY 已通过四段连续链路：
  - 简单英文问答
  - 记忆上一轮指令
  - 复杂工具调用 review
  - 中文 `继续` 追问
- 仍需继续观察的剩余风险：
  - OAuth/auth 池波动导致的独立 `auth_not_found`
  - 极端长会话里是否还有别的成功流兼容尾巴
  - 其他子代理类型是否还存在未覆盖的结构化输出约束

## 新增证据与修复

- 通过 session trajectory PG 查询，定位到同一会话后两次异常请求：
  - `request_index=7` / `request_id=1aa31dcc`
    - 被记录成 `status=success`
    - 但 `response_json` 只有空 assistant message：`content=[]`、`stop_reason=null`、`usage=0`
  - `request_index=8` / `request_id=110d994c`
    - 被记录成 `status=success`
    - 但没有 `provider_request_id`，也没有 `response_json`
- 这说明旧代码路径里存在“上游流在 `response.completed` 前正常 EOF，但服务端仍把它当成功流关闭”的问题。
- 已在本地补丁中修复：
  - `internal/runtime/executor/codex_executor.go`
    - `ExecuteStream()` 现在要求流式路径必须见到 `response.completed`
    - 若 scanner 正常结束但从未收到 `response.completed`，返回终止错误：
      - `stream error: stream disconnected before completion: stream closed before response.completed`
  - 回归测试：
    - `internal/runtime/executor/codex_executor_fastmode_test.go`
    - 覆盖“只有 `response.created` 就 EOF”时，必须返回终止错误
- 用户补充的线上样本 `c25bffae-30dd-45f6-94cc-902af0b23d61(2).jsonl` 进一步证明这条问题在线上真实发生：
  - 见 `stream disconnected before completion` 错误
  - 随后客户端再炸成 `undefined is not an object (evaluating '__.content')`
- 另有独立老问题也在同文件中被再次证实：
  - `Agent/code-reviewer` 子代理仍可能报 `API Error: Failed to parse JSON`
  - 现已补 `internal/translator/claude/openai/responses/claude_openai-responses_request.go`
    的 `response_format/json_schema -> text.format` 映射，并新增对应单测
- 为避免 handler 自身把更具体的首包前断流错误降级成泛化 `500 auth_not_found`，本地又补了一层保真逻辑：
  - `sdk/api/handlers/handlers.go`
    - `statusFromError()` 改为 `errors.As(...)` 展开取状态码
    - 新增 `preferSpecificStreamRetryError()`，当 bootstrap retry 失败只返回泛化 `500` 时，保留第一次更具体的错误（如 `408 stream closed before response.completed`）
  - 回归测试：
    - `sdk/api/handlers/handlers_stream_bootstrap_test.go`
    - 覆盖“首包前断流 + handler 自动重试后 auth manager 只剩 `auth_not_found`”时，最终仍应保留原始 `408` 终止错误

## 新线上样本补充

- 用户新增样本：
  - `/Users/taylor/Downloads/dcc6e04eeaffd70e8b4386a3ef6bd801.jsonl`
- 样本里同时出现三类症状：
  - 普通主会话 synthetic error：
    - `undefined is not an object (evaluating '$.input_tokens')`
  - 主会话 synthetic error：
    - `API Error: Failed to parse JSON`
  - `Agent/general-purpose` 子代理 `tool_result`：
    - `API Error: Failed to parse JSON`
- 该样本还能说明：
  - `auth_not_found: no auth available` 在线上会真实出现在主会话系统 `api_error` 中
  - 随后客户端仍可能把它展示成 `Failed to parse JSON`
  - 这说明“客户端表面报 parse JSON”与“服务端真实根因是 auth 不可用 / 半截流 / 子代理结构化输出失败”仍在混杂出现
- 关键行位：
  - 第 123 行：`undefined is not an object (evaluating '$.input_tokens')`
  - 第 183 / 286 / 323 行：`API Error: Failed to parse JSON`
  - 第 292 行：`Agent` 工具结果内含 `API Error: Failed to parse JSON`
  - 第 284-285 / 293-295 / 320-322 行：底层真实系统错误是 `500 auth_not_found: no auth available`

## 2026-04-22 新增样本 1fac0114

- 用户新增样本：
  - `/Users/taylor/Downloads/1fac0114-f300-4118-8555-9e6ad812f2ca.jsonl`
- 该样本的重要性在于：它进一步证明线上并不只是 `claude-mem`，而是“多 hook 叠加态”：
  - `SessionStart` 有纯文本 caveman mode hook 输出
  - `SessionStart` 同时还有 `claude-mem` hook 输出
  - 还叠加了 `cmux claude-hook ...` 这一层自定义 hook
- 样本中的关键现象：
  - 第 2 行：纯文本 caveman mode 输出直接进入 `hook_success.content/stdout`
  - 第 3 行：`claude-mem` 返回 JSON 形式的 `hookSpecificOutput`
  - 第 4 / 238 / 249 行：`cmux claude-hook ...` 返回 `OK`
  - 第 243-247 行：存在异步 hook 回写 attachment
  - 第 251 行：最终 assistant synthetic error 为 `API Error: Failed to parse JSON`
- 这个样本说明：
  - 线上用户的失败场景很可能发生在“代理兼容链路 + 本地多 hook/custom wrapper”叠加态
  - 不能再把所有 `Failed to parse JSON` 都简单归因到单一代理响应格式问题
  - 需要把“本地 hooks 输出形状、异步 hook attachment、工具回合状态机”纳入同一证据链分析

## 2026-04-22 本地 claude2 + claude-mem 新回归

- 已明确改用 `claude2` 作为 `cc1` 真正二进制，不再把 `~/.cac/bin/claude` 当主入口。
- 已在 `~/.claude_local` 下真实安装并启用：
  - `claude-mem@thedotmack`
  - `bun` 已加入执行路径，`plugin:claude-mem:mcp-search` 可以正常连接
- 本地当前通过样本：
  - `~/.claude_local/projects/-Users-taylor-code-tools-CLIProxyAPI-ori/b66dbc92-c994-476c-890d-d09248f69af4.jsonl`
- 已通过的真实链路：
  - 第 1 轮：`Reply with exactly TTY_MEM_OK`
  - 第 2 轮：`What was my previous instruction? Reply with exactly TTY_MEM_PREV`
  - 第 3 轮已进入工具回合：
    - `Review the recent changes in sdk/api/handlers/openai/openai_responses_handlers.go for regressions only. Be concise.`
    - 已正常出现 `TaskCreate` 的 `tool_use/tool_result`
    - `PostToolUse:TaskCreate` hook 也返回了合法 `{}` JSON
- 当前这条本地 `claude2 + claude-mem + 本地代理` 路径下，尚未再现：
  - `API Error: Failed to parse JSON`
  - `Unexpected end of JSON input`
  - `undefined is not an object (reading 'speed' / 'content' / 'input_tokens')`
- 当前判断更新为：
  - 纯 `claude2 + ~/.claude_local + claude-mem + CLIProxyAPI` 主干路径，至少在短多轮和工具起步阶段已明显稳定
  - 线上残余报错更像需要额外叠加：
    - 用户自己的 `cmux` / caveman / 其他 hooks
    - 旧版本客户端（线上样本里仍出现 `2.1.112`）
    - 更长的工具往返或异步 hook 回写链路

## 建议下一步

- 再补一轮真实 `cc1` 同 PTY 继续追问黑盒，覆盖：
  - 上一轮复杂工具调用完成后继续 `继续`
  - 中文 prompt
  - 子代理 review / agent 类请求
- 若新一轮仍不复现，可把当前补丁集视为达到“可上线验证”门槛，再做 push / 生产灰度。
- 若后续还能复现 `content` / `input_tokens` / `speed` 空指针，再继续抓最终一跳 SSE 原始事件，重点看：
  - `internal/translator/codex/claude/codex_claude_response.go`
  - Claude Code 本地源码 `services/api/claude.ts` / `QueryEngine.ts`

## 本轮黑盒踩坑补充

- PTY 屏幕内容是有价值的，可以帮助判断用户当时实际看到什么。
- 但它不能单独用来判断“下一轮 prompt 是否真的提交出去了”。
- 这轮就踩到过一次：prompt 文本已经显示在输入框里，但 session jsonl 里并没有新增真实 `type=user` 记录。
- 当前最稳的注入节奏是：
  1. 等上一轮在 session jsonl 中出现 `system` / `subtype="turn_duration"`
  2. 再写入 prompt 文本
  3. 单独补一个 `\\r`
  4. 再确认 jsonl 真的新增了该轮 `user_prompt`
- 结论：
  - “屏幕上看见了”不等于“客户端已经提交了”
  - 黑盒调试的 source of truth 仍是 `session jsonl + --debug-file + server log`

## 2026-04-22 下午追加收敛

- 本地真实 `claude2 + ~/.claude_local + claude-mem + CLIProxyAPI` 回归继续推进后，第三轮 review 没有在“工具刚起步”时中断，而是继续健康进入第二段文件读取：
  - `2026-04-22T10:01:43Z`：继续发起 `/v1/messages`，读取 `sdk/api/handlers/openai/openai_responses_handlers_stream_test.go`
  - `2026-04-22T10:02:48Z`：再次发起 `/v1/messages`，读取 `sdk/api/handlers/openai/openai_responses_handlers_stream_error_test.go`
- 到这一步仍未出现：
  - `API Error: Failed to parse JSON`
  - `Unexpected end of JSON input`
  - `undefined is not an object`
- 这说明当前主干路径相比之前已经明显稳定，不再是“第三轮复杂工具调用很容易就炸”的状态。

## 2026-04-22 新样本进一步收敛

- 线上样本 `1fac0114-f300-4118-8555-9e6ad812f2ca.jsonl` 的失败轮前，出现了高密度本地 hook 活动：
  - 全文件共有 `50` 条 `async_hook_response`
  - 失败轮前直接相邻的是 `Stop` / `PreToolUse:Edit` / `PreToolUse:TaskUpdate` 等异步 hook attachment
  - 随后才是 `UserPromptSubmit` 成功，再落 `<synthetic>` 的 `API Error: Failed to parse JSON`
- 同一文件客户端版本是 `2.1.112`，而本地当前稳定回归使用的是 `2.1.114`。
- 失败前一个正常 assistant（第 `236` 行）仍有完整 `usage.speed=standard`；失败 assistant（第 `251` 行）是 CLI 合成的 `<synthetic>` 消息，`speed`/`iterations` 等为 `null`。这与用户截图里后续继续冒出 `Cannot read properties of undefined (reading 'speed')` 的次生 UI 错误是一致的。

## 当前判断更新

- 现在更接近的结论不是“代理主通路仍然稳定复现 parse JSON”，而是：
  - 主干代理链路在本地真实 `claude2` 路径下已明显稳定
  - 线上残留问题更像旧版客户端、额外自定义 hook 栈、异步 hook 回写和长工具轮次叠加后的组合问题
- 如果要继续逼近线上同类报错，下一步优先级应是：
  - 在本地显式模拟 `cmux` / caveman / 高频 async hook 负载
  - 而不是继续只拿“纯 `claude-mem` 主路径”做回归

## 2026-04-22 async hook 显式模拟结果

- 为了更接近线上 `1fac0114` 这类样本，临时在 `~/.claude_local/settings.json` 注入了最小 async hooks：
  - `UserPromptSubmit`
  - `PreToolUse(Read|Grep|Bash|Edit|TaskUpdate|TaskCreate)`
  - `Stop`
- hook 命令统一采用：
  - 第一行输出 `{"async":true}`
  - 短暂 `sleep`
  - 再输出 `{}` 完成
- 新本地 session：
  - `~/.claude_local/projects/-Users-taylor-code-tools-CLIProxyAPI-ori/f036f546-b984-4e5d-b8c2-7730d8628652.jsonl`
  - debug log: `/tmp/claude2-asynchooks-53841.log`
- 结果：
  - 第 1 轮简单 prompt 正常成功，`UserPromptSubmit` 与 `Stop` 都被背景化为 `async_hook_response`
  - 第 2 轮 review prompt 正常继续，先后出现：
    - `UserPromptSubmit` async attachment
    - 两条 `PreToolUse:Bash` async attachment
    - 多次正常 `/v1/messages` 请求
    - 正常 `assistant` 文本与 `tool_use/tool_result`
  - 到当前观测点仍未出现：
    - `API Error: Failed to parse JSON`
    - `Unexpected end of JSON input`
    - `undefined is not an object`
- 结论：
  - “存在大量 async_hook_response attachment”本身，不足以在当前 `2.1.114 + 代理修复版` 环境下复现线上报错
  - 线上残留更像还依赖：
    - 更复杂的自定义 hook 实现（例如 `cmux` / caveman）
    - 或旧版客户端 `2.1.112`
    - 或两者叠加
