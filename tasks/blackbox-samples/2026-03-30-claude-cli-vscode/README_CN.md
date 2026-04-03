# 2026-03-30 Claude CLI / VSCode 扩展黑盒样本归档

## 目录目的

归档本轮真实黑盒验证中产生的 `Claude CLI` 与 `Claude VSCode 扩展` 会话样本，便于后续：

- 回归复用
- 协议级排查
- 进度总结
- 对比新旧实现差异

本目录保存的是“当前会话已实际使用过”的原始样本与衍生输出，不是伪造数据。

## 目录结构

- `codex-cli/`
  - 真实 `codex exec --json` 对比样本与观察说明
- `claude-cli/`
  - 真实 `claude -p` 黑盒请求对应的输出与 request log
- `vscode/`
  - 真实 VSCode 扩展会话请求样本、回放输出、定时输出、扩展日志

## Codex CLI 对比样本

文件：

- `codex-cli/2026-03-30-complex-tooling-prompt.txt`
- `codex-cli/2026-03-30-complex-tooling-observations_CN.md`

场景：

- 使用复杂但低风险的仓库分析题，对比 `codex-cli` 与 `claude-cli` 在多工具调用任务里的可见行为

当前结论：

- `codex exec --json` 会持续输出结构化过程事件：
  - `agent_message`
  - `command_execution started/completed`
- 这类样本用于给 `claude-cli` 兼容层提供“应尽量靠近的过程感”参考
- 后续复杂长任务的兼容收口，应优先参考这里，而不是只参考最终答案文本

## Claude CLI 样本

### 1. 普通代码分析题

文件：

- `claude-cli/claude-cli-code-analysis.txt`
- `claude-cli/claude-cli-code-analysis.err`
- `claude-cli/v1-messages-2026-03-30T174619-a6ac0702.log`

场景：

- 提示词：`看一下当前的项目代码，帮我简单分析一下即可`

当前结论：

- 该样本用于验证“普通工具调用题不应误报 `Searching the web.`”
- 对应 request log 内未观察到 `web_search_call`
- 用于确认 CLI 的“假 websearch”问题已被收掉

### 2. 显式 websearch 题

文件：

- `claude-cli/claude-cli-websearch.txt`
- `claude-cli/claude-cli-websearch.err`
- `claude-cli/v1-messages-2026-03-30T174755-350b12a5.log`

场景：

- 提示词：`用 websearch 搜索今天的新闻`

当前结论：

- 该样本用于验证“显式搜索题应真实进入 `web_search_call`”
- 对应 request log 内可见真实多轮 `web_search_call`
- 用于确认现在已经做到：
  - 该搜的时候搜
  - 不该搜的时候不乱搜

### 3. 非交互挂起样本

文件：

- `claude-cli/claude-cli-complex-tooling-1.stream.jsonl`
- `claude-cli/claude-cli-complex-tooling-1.err`
- `claude-cli/claude-cli-minimal-hang-prompt.txt`
- `claude-cli/claude-cli-minimal-hang.stdout`
- `claude-cli/claude-cli-minimal-hang.stderr`
- `claude-cli/claude-cli-minimal-hang-meta.json`
- `claude-cli/2026-03-30-noninteractive-hang-observations_CN.md`

场景：

- 在当前自动化环境里，尝试用真实 `claude -p` / `claude -p --stream-json` 跑复杂工具任务与极简题

当前结论：

- 这批样本用于记录：
  - `stream-json` 可能只吐 `system/init` 就长时间无后续事件
  - 极简 `claude -p` 也可能在本地直接超时
- 这说明当前自动化黑盒环境下，`claude-cli` 非交互模式本身存在挂起风险
- 不能把这类现象直接归因到代理兼容层

### 4. 代理已监听后的真实二次回归样本

文件：

- `claude-cli/2026-03-30T223418-complex-analysis-1.stream.jsonl`
- `claude-cli/2026-03-30T223418-complex-analysis-1.err`
- `claude-cli/2026-03-30T223715-complex-analysis-2.stream.jsonl`
- `claude-cli/2026-03-30T223715-complex-analysis-2.err`
- `claude-cli/2026-03-30T223727-code-analysis-2.stream.jsonl`
- `claude-cli/2026-03-30T223727-code-analysis-2.err`
- `claude-cli/2026-03-30-real-claude-cli-regression-2_CN.md`
- `claude-cli/request-logs/`

场景：

- 先确认本机代理重新监听 `127.0.0.1:53841`
- 再用真实 `claude -p --verbose --output-format stream-json` 复跑复杂仓库分析题与普通代码分析题

当前结论：

- 复杂题真实流里已能看到：
  - `Searching the codebase.`
  - `Reading relevant files.`
  - `Running a verification command.`
- 普通代码分析题未出现 `Searching the web.`
- 三组样本均 `rc=0` 且 `stderr` 为空
- 说明“复杂任务只有聚合摘要”这一问题在本轮真实 CLI 回归里已明显收敛

### 5. 稳定性批次与否定式 websearch 修复样本

文件：

- `claude-cli/stability-2026-03-30/2026-03-30T225633-batch-fixed/summary.tsv`
- `claude-cli/stability-2026-03-30/2026-03-30T231143-stability-round2/summary.tsv`
- `claude-cli/stability-2026-03-30/2026-03-30T232040-negation-fix-check/summary.tsv`
- `claude-cli/stability-2026-03-30/2026-03-30T233225-resume-negation-smoke/summary.tsv`
- `claude-cli/2026-03-30-real-claude-cli-stability-round3_CN.md`

场景：

- 连续补跑多组真实 `claude-cli` 单轮和多轮 `resume`
- 区分“突然中断”“session-id 用法限制”“长尾时延”
- 定向验证：
  - `不要用 web search`
  - `do not use web search`

当前结论：

- 独立单轮样本未复现突然中断，均正常返回 `result success`
- 正确的多轮方式是首轮 `--session-id`，后续 `-r/--resume`
- 直接重用同一个 `--session-id` 报 `already in use`，属于 CLI 语义，不是代理中断
- 已确认并修复一个真实问题：
  - 否定式提到 websearch 时仍误报 `Searching the web.`
- 修复后：
  - 非搜索 negated prompt 不再出现 `Searching the web.`
  - 显式搜索题仍保留真实搜索进度
- 仍需继续观察长尾时延，尤其首个 `resume` 轮次偶发耗时较长

## VSCode 扩展样本

### 1. 原始请求样本

文件：

- `vscode/vscode-151047-request.json`
- `vscode/vscode-151047-request-only.json`
- `vscode/vscode-153028-request.json`
- `vscode/vscode-162415-request.json`

用途：

- 作为真实扩展请求的协议级输入样本
- 用于复现 query 抽取、thinking 展示、websearch tag 与 stall 相关问题

### 2. 回放 / 定时输出样本

文件：

- `vscode/vscode-151047-replay.out`
- `vscode/vscode-151047-replay-current.out`
- `vscode/vscode-153028-replay-current.out`
- `vscode/vscode-154248-timed.out`
- `vscode/vscode-162415-timed.out`
- `vscode/vscode-162415-fixed-timed.out`
- `vscode/vscode-verify-current.out`

用途：

- 保存当前会话中对真实 VSCode 请求做协议回放后的输出
- 用于对比修复前后：
  - query 污染
  - fake thinking
  - 首个可见进度事件
  - stall 相关现象

说明：

- `vscode-162415-fixed-timed.out` 是本轮用于验证“把后置 fake thinking 改成更早、更真实的搜索进度”后的关键样本

### 3. 扩展完整日志

文件：

- `vscode/Claude VSCode.log`

用途：

- 保存真实扩展端日志
- 用于检索：
  - `Streaming stall detected`
  - `Stream started - received first chunk`
  - 其他扩展侧异常和状态变化

## 使用建议

后续如需继续做 `Claude CLI / VSCode -> GPT` 兼容回归，建议优先使用：

1. `codex-cli/2026-03-30-complex-tooling-prompt.txt`
2. `codex-cli/2026-03-30-complex-tooling-observations_CN.md`
3. `claude-cli/v1-messages-2026-03-30T174619-a6ac0702.log`
4. `claude-cli/v1-messages-2026-03-30T174755-350b12a5.log`
5. `vscode/vscode-151047-request.json`
6. `vscode/vscode-162415-request.json`
7. `vscode/vscode-162415-fixed-timed.out`
8. `vscode/Claude VSCode.log`
9. `claude-cli/2026-03-30-real-claude-cli-regression-2_CN.md`
10. `claude-cli/request-logs/`

## 备注

- 本目录是归档快照，不应手动覆盖原始含义不清的文件名。
- 若后续新增新的真实黑盒样本，建议按日期继续新建平级目录，避免混淆阶段结论。
