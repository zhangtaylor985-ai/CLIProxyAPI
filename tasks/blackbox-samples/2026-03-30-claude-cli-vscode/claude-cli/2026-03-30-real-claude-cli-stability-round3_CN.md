# 2026-03-30 真实 Claude CLI 稳定性补充结论

## 目的

继续用真实 `claude-cli` 验证两件事：

- 能否稳定复现报错、突然中断、聊着聊着断掉
- 修复“否定式提到 web search 仍误报 `Searching the web.`”后，真实 CLI 是否恢复正常

## 本轮样本

稳定性批次：

- `stability-2026-03-30/2026-03-30T225633-batch-fixed/summary.tsv`
- `stability-2026-03-30/2026-03-30T231143-stability-round2/summary.tsv`

否定式 websearch 修复验证：

- `stability-2026-03-30/2026-03-30T232040-negation-fix-check/summary.tsv`
- `stability-2026-03-30/2026-03-30T233225-resume-negation-smoke/summary.tsv`

## 结论摘要

### 1. 独立单轮任务未复现突然中断

两轮稳定性批次里，独立单轮样本均拿到：

- `rc=0`
- `stderr=0`
- `result subtype=success`

覆盖了：

- 普通代码分析题
- 复杂仓库分析题
- 显式 websearch 题

当前没有复现“输出进行到一半突然断掉但没有结果块”的现象。

### 2. 正确的多轮会话恢复方式是 `-r/--resume`

此前已复现：

- 直接重复使用同一个 `--session-id`
- 会立即报：
  - `Session ID ... is already in use.`

这更像 `claude-cli` 的会话锁语义，不是代理兼容层异常。

而用正确方式：

- 首轮：`--session-id`
- 后续轮次：`-r/--resume`

本地真实样本已连续成功。

### 3. 修复前确认过一个真实误判

在 `2026-03-30T231143-stability-round2` 里，黑盒真实输出表明：

- 普通代码分析题如果提示词包含：
  - `不要用 web search`
- 或多轮提示里包含：
  - `do not use web search`

仍会真的出现前置：

- `Searching the web.`

这不是统计误判，而是兼容层搜索意图识别把“否定式提到搜索”错判成了“显式要求搜索”。

### 4. 修复后，否定式 prompt 已收敛

修复后重新回归：

- `negated-cn`
- `negated-en`
- `resume-negated-turn-1`
- `resume-negated-turn-2`
- `turn-1`
- `turn-2`

结论一致：

- `rc=0`
- `stderr=0`
- `has_result_success=1`
- `actual_web_progress=0`

同时显式搜索对照题：

- `search-control`

仍保持：

- `actual_web_progress=1`

说明这次修复命中问题本身，没有把真实搜索题一起拦掉。

### 5. 当前更像“长尾时延”，不是“中途断流”

本轮有一条样本：

- `resume-negated-turn-1`

耗时达到：

- `463s`

但它最终仍然：

- `rc=0`
- `stderr=0`
- `has_result_success=1`
- 未出现 `Searching the web.`

因此当前更需要把它归类为：

- 长尾时延明显

而不是：

- 会话中途无声断流

## 风险边界

需要继续区分三类现象：

1. 代理未监听导致的连接失败或挂起
2. CLI 会话锁语义导致的 `session-id already in use`
3. 代理兼容层自身的显示/稳定性问题

截至本轮：

- 第 1 类不是兼容层 bug
- 第 2 类是 CLI 用法限制
- 第 3 类里已确认并修复一个真实问题：
  - 否定式 websearch 误判

## 当前判断

在本机代理正常监听、且多轮会话使用 `-r/--resume` 的前提下：

- 真实 `claude-cli` 当前总体可用，未复现突然中断
- 已修复的 `Searching the web.` 误报问题，在真实黑盒里也已收口
- 仍需继续观察的是长尾时延，而不是突然断流
