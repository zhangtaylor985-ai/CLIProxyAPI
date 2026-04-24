# Claude 客户端兼容经验总结

这份文档专门沉淀“这轮对话到底学到了什么”，方便后面继续维护 `Target CC-Parity` 时不再重复走弯路。

## 一、最重要的结论

### 1. `Failed to parse JSON` 不是单一问题

这次最大的教训就是：

- 同一个报错文案
- 可能对应多个完全不同的根因

如果不先拆根因，后面会不断陷入“明明修过了，为什么用户还在报同一句错”。

### 2. 单测很重要，但真实客户端黑盒不可替代

这轮里多次出现：

- 单测已经全绿
- 真实 `cc1` / Claude Code 用户仍然报错

原因很直接：

- 单测更容易覆盖 translator / handler 的局部逻辑
- 真实客户端才会暴露：
  - 同一 PTY 连续多轮
  - 工具返回后继续生成
  - hook / plugin / debug-file / UI 行为组合

### 3. 证据必须三方对齐

以后不要再只盯一处现象。

真正可靠的结论，必须来自：

1. `session jsonl`
2. 客户端 `--debug-file`
3. 服务端 `logs/main.log`

PTY 屏幕只能辅助理解用户感受，不能单独作为根因证据。

## 二、这轮真正踩过的坑

### 1. 看到 prompt 在屏幕上，不等于请求已经发出

ANSI 重绘、输入框回显、长思考期间的 UI 刷新，都会制造“像是已经提交”的假象。

真正的提交标准仍然是：

- debug-file 里出现 `Hook UserPromptSubmit`
- debug-file 里出现 `/v1/messages`
- `session jsonl` 里出现新的 `user`

### 2. `HTTP 200` 也不代表成功流没有问题

这次就明确碰到过：

- 首包前空流返回 `HTTP 200` 空 body
- 成功流没等到 `response.completed`
- `response.output_item.done` 没有 fallback
- tool-call block 关闭过早

所以以后看到 `200`，也不能直接排除协议兼容问题。

### 3. 请求侧和响应侧要同时看

本轮后半段的关键修复，其实一半在 request translator，一半在 response translator：

- response 侧缺 `output_item.done message fallback`
- request 侧会丢未知 `tool_result.content[]`

只盯一边，很容易误判成“看起来已经都修了”。

### 4. hook / plugin 问题会伪装成代理协议问题

像 `claude-mem`、自定义 hook、外部 wrapper 这类环境因素，可能会制造：

- 混合 stdout/stderr
- hook JSON 解析错误
- stop hook 尾延迟

这些问题未必是代理层 bug，但用户看到的表面现象可能一样。

### 5. 文档口径必须区分“历史已修复”和“当前仍在发生”

这轮后面我们又拿到了新的线上 session，证明：

- 历史修复是成立的
- 但线上仍可能出现新的活跃回归或外部环境风险

所以维护文档时不能只写“都修好了”，而要同时保留：

- 已修复历史 bug
- 活跃调查中的回归
- 环境/上游风险

## 三、维护上的长期原则

### 1. 以后统一按“bug 家族”维护，不按报错文案维护

推荐固定字段：

- 症状
- 根因
- 代码入口
- 修复提交
- 回归用例
- 当前状态

### 2. 每修一条线上 bug，都补一个更窄的最小回归

不要只加泛化 smoke。

更有效的做法是把线上证据直接转成最小场景，比如：

- 多 tool_use 同轮返回后继续总结
- `AskUserQuestion` 后继续生成
- `response.output_item.done.item.type=message`
- 未知 `tool_result.content[]`

### 3. 每日自动回归要尽量稳定，而不是尽量复杂

自动回归的职责是尽快发现明显回归，不是替代全部人工验证。

所以日常任务里优先保留：

- 真实 `cc1`
- 同一 PTY
- 多轮
- 至少一轮工具调用
- 至少一轮继续追问

### 4. 仓库内要长期保留统一入口

以后继续维护这套兼容能力时，入口统一放在：

- `docs/claude-client-compat/`
- `tasks/claude-client-compat/daily-regressions/`
- `scripts/run_cc1_daily_regression.sh`
- `scripts/cc1_tty_regression.expect`
- `.codex/skills/cc1-tty-blackbox-testing/SKILL.md`

这样不管是人工接手、自动回归还是继续查线上问题，都不用再从零开始。

### 5. 每日自动回归脚本本身也要像生产代码一样验收

这次整理里我们还踩到一个非常实际的坑：

- 如果 `expect` 只是盯着终端里的 `❯`
- 很容易把上一轮残留的 prompt 误判成“下一轮已经完成”

后面我们把自动化判定改成了看 debug-file 的真实计数：

- `Hook UserPromptSubmit`
- `[API REQUEST] /v1/messages`
- `Stream started - received first chunk`
- `Hook Stop`

这条经验很重要：

- 自动回归不是“脚本能跑起来”就算完成
- 必须确认它的完成判定本身不会制造假阳性
