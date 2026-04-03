# 2026-03-30 VSCode 扩展生产化待办

## 目标

让 `Claude VSCode 扩展 -> GPT/Codex 后端` 的长时间使用体验接近 `Codex`，并达到可上线的生产标准。

## 当前未闭环项

1. 真实扩展会话里继续验证“首个扩展认可的内容事件”是否足够早。
2. 收敛 `Streaming stall detected`，确认新日志里不再出现新的 stall 记录。
3. 继续核对 websearch 相关展示，确保 VSCode 不再出现 CLI 风格的 synthetic tag 串出。
4. 复盘扩展偶发自动中断 / 卡住问题，区分：
   - 协议层无有效增量
   - 扩展端自身状态机
   - 上游模型长时间无可见输出
5. 做独立回归：
   - Claude CLI
   - Claude VSCode 扩展
   - 搜索题
   - 非搜索代码分析题
   - 长会话 / 多工具调用题
6. 继续观察普通代码分析题是否仍会被模型自主触发 builtin `web_search`。
   - 若真实回归里仍偶发误上网，再补请求侧 `WebSearch` 意图门控，而不是只做展示层抑制。
7. 继续积累真实 `claude-cli --print --stream-json` 的复杂工具任务样本。
   - 重点看：
     - 首个非 init 事件延迟
     - 是否仍有长时间无有效增量
     - suppress fake thinking 后，tool_use 过程是否足够清晰
   - 2026-03-30 本轮补充结论：
     - 在本机代理正常监听 `:53841` 后，真实 `claude-cli` 已观察到连续阶段提示
     - 后续仍需继续观察更长会话是否退化回聚合摘要主导
8. 后续如再出现 `init` / timeout / `ConnectionRefused` 样本，先核对本机代理端口是否已监听，再判断是否属于兼容层问题。

## 验收标准

- 普通代码分析题不误报 `Searching the web.`
- 搜索题在 VSCode 中尽早出现可信进度
- 新一轮真实扩展日志中不再出现 `Streaming stall detected`
- CLI 与 VSCode 两条链路互不串扰
