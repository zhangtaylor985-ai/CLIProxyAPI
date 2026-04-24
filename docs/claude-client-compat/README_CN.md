# Claude 客户端兼容维护目录

这个目录用于长期维护 `Claude Code / cc1 / claude2 -> CLIProxyAPI -> GPT/Codex` 兼容链路。

当前目标不是只记一批零散 bug，而是把下面四件事稳定沉淀下来：

1. 统一记录已发现的兼容 bug、症状、根因、修复提交和当前状态。
2. 固化黑盒调试流程，避免下次再从头摸索。
3. 保留每日自动回归入口，尽早发现真实回归。
4. 为后续 `Target CC-Parity` 继续推进保留一个清晰入口。

## 当前快照

截至 `2026-04-22`，我们已经确认：

- `13` 类问题家族，其中：
  - `9` 类历史代理兼容 bug，已经有明确修复提交。
  - `1` 类当前仍在调查中的活跃代理兼容回归。
  - `3` 类环境/插件/上游风险，需要持续监控。

当前最重要的结论有两个：

- `API Error: Failed to parse JSON` 只是症状桶，不是单一根因。
- 真正可信的排查证据，必须同时看 `session jsonl + 客户端 debug-file + 服务端 logs/main.log`。

## 文档索引

- [Bug 台账](./bug-registry_CN.md)
- [活跃调查](./active-investigations_CN.md)
- [调试 Runbook](./debug-runbook_CN.md)
- [经验总结](./lessons-learned_CN.md)
- [每日回归任务](./daily-regression-task_CN.md)

## 关联资料

- 主目标定义：[target/cc-parity_CN.md](/Users/taylor/code/tools/CLIProxyAPI-ori/target/cc-parity_CN.md)
- 真实 TTY 黑盒 skill：[.codex/skills/cc1-tty-blackbox-testing/SKILL.md](/Users/taylor/code/tools/CLIProxyAPI-ori/.codex/skills/cc1-tty-blackbox-testing/SKILL.md)
- 当前连续问题跟踪：[todos/2026-04-22-cc1-tty-multiturn-api-error_CN.md](/Users/taylor/code/tools/CLIProxyAPI-ori/todos/2026-04-22-cc1-tty-multiturn-api-error_CN.md)
- 历史兼容推进记录：[tasks/2026-03-30-claude-gpt-compat-progress.md](/Users/taylor/code/tools/CLIProxyAPI-ori/tasks/2026-03-30-claude-gpt-compat-progress.md)
- 生产评审结论：[cc-cli-analysis/2026-04-02-claude-cli-source-soak-production-review_CN.md](/Users/taylor/code/tools/CLIProxyAPI-ori/cc-cli-analysis/2026-04-02-claude-cli-source-soak-production-review_CN.md)

## 目录约定

- `docs/claude-client-compat/`
  - 维护方法、bug 台账、回归策略、经验总结。
- `tasks/claude-client-compat/daily-regressions/`
  - 每日自动回归产物目录，只保留 `README` 和 `.gitignore`，实际运行结果不入库。
- `scripts/run_cc1_daily_regression.sh`
  - 每日回归入口。
- `scripts/cc1_tty_regression.expect`
  - 真实 `cc1` 同一 PTY 自动驱动脚本，当前底层基于 `tmux + python3`。

## 本轮经验总结

### 1. 不要把同一条报错文案当成同一根因

这轮最典型的就是：

- `API Error: Failed to parse JSON`

它背后至少对应过这些不同根因：

- Claude 风格错误包不兼容
- SSE 半截 JSON / 组帧错误
- 成功流里 `response.completed` 缺失
- tool call SSE block 关闭时机错误
- `response.output_item.done` message fallback 缺失
- `tool_result.content[]` 里的未知 block 被静默丢掉
- 上游 TLS / 证书校验错误被 UI 合成成通用 API 错误
- `Agent/Explore` 子代理链路在生产长会话里仍有活跃回归待继续收口

### 2. PTY 屏幕现象只能辅助理解，不能直接下结论

看到输入框里有 prompt，不等于这轮 prompt 已经真实提交。

真实判断标准仍然是：

- debug-file 里有 `Hook UserPromptSubmit`
- debug-file 里有 `/v1/messages`
- `session jsonl` 里新增了真实 `user` 记录

### 3. 自动回归要优先做“稳定可重复”的真实用例

目前最稳定、最适合每日自动跑的回归场景是：

- `cc1` 启动真实本地客户端
- 同一 PTY 做最小 smoke
- 再做一次会触发工具调用和继续总结的任务
- 回归驱动以 debug-file 里的真实计数为准：
  - `Hook UserPromptSubmit`
  - `[API REQUEST] /v1/messages`
  - `Stream started - received first chunk`
  - `Hook Stop`

这比只跑单测更接近真实用户，也比过于复杂的人工交互更适合每天自动执行。

## 调度口径

定时任务的目标是固定的：

- 每天 `10:30`
- 实际调用 `cc1`
- 命中本地代理
- 生成可复核的 transcript、debug-file、summary

当前优先级是：

1. 优先使用 Codex 自带 automation
2. 如果当前运行环境没有暴露 automation 接口，就继续保留仓库内脚本作为统一执行入口
3. 不再继续维护额外的本机定时 fallback，避免和主方案并存造成混淆
