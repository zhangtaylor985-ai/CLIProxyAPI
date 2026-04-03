# 2026-03-30 真实 Claude CLI 二次回归结论

## 目的

在本机代理 `:53841` 已正常监听的前提下，重新验证真实 `claude-cli`：

- 复杂仓库分析题是否仍只有聚合摘要
- 普通代码分析题是否会误报 `Searching the web.`
- 本轮是否还有新的报错或中断

## 本轮样本

复杂题 1：

- `2026-03-30T223418-complex-analysis-1.stream.jsonl`
- `2026-03-30T223418-complex-analysis-1.err`

复杂题 2：

- `2026-03-30T223715-complex-analysis-2.stream.jsonl`
- `2026-03-30T223715-complex-analysis-2.err`

普通题：

- `2026-03-30T223727-code-analysis-2.stream.jsonl`
- `2026-03-30T223727-code-analysis-2.err`

对应 request logs：

- `request-logs/v1-messages-2026-03-30T223806-5eb1bc23.log`
- `request-logs/v1-messages-2026-03-30T223811-6a182389.log`
- `request-logs/v1-messages-2026-03-30T223812-bb68b5d6.log`
- `request-logs/v1-messages-2026-03-30T223820-9d20da3c.log`
- `request-logs/v1-messages-2026-03-30T223831-57926b50.log`
- `request-logs/v1-messages-2026-03-30T223835-a8b4e91d.log`
- `request-logs/v1-messages-2026-03-30T223844-34661d67.log`
- `request-logs/v1-messages-2026-03-30T223851-2b397325.log`
- `request-logs/v1-messages-2026-03-30T223922-147159a5.log`

## 结论摘要

### 1. 复杂工具任务已经出现连续阶段提示

真实流里可见：

- `Searching the codebase.`
- `Reading relevant files.`
- `Running a verification command.`

这说明兼容层新增的短进度文本，已经被真实 `claude-cli` 正常展示出来。

### 2. 普通代码分析题未复现误报 websearch

普通题样本里：

- 出现了 `Searching the codebase.`
- 未出现 `Searching the web.`
- stderr 为空
- 正常返回最终分析结果

### 3. 本轮未出现新的连接错误

本轮三组样本均为：

- `rc=0`
- `stderr` 为空
- `result subtype=success`

### 4. 之前的挂起/连接失败样本需要单独看待

此前那批 `init` / timeout / `ConnectionRefused` 样本，主要前置条件是：

- `claude-cli` 指向了 `127.0.0.1:53841`
- 但当时本机代理并未监听该端口

因此它们更像是环境前置问题，而不是“阶段提示改动失效”。

## 对当前兼容层的判断

在代理正常监听的前提下，本轮真实回归支持下面这个结论：

- `claude-cli` 已明显比此前更接近 `codex cli` 的过程可见性
- 普通代码分析题没有再串出 `Searching the web.`
- 复杂工具调用题可以看到连续步骤，而不只是最后一条聚合摘要

## 仍需继续观察的点

- 更长会话下是否会回退成聚合摘要主导
- 不同 prompt 风格下，阶段提示是否仍稳定
- VSCode 扩展链路是否与 CLI 链路保持一致
