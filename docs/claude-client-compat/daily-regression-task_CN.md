# 每日 `cc1` 回归任务

## 目标

每天自动跑一次真实 `cc1` 黑盒，尽早发现这些已知症状是否重新出现：

- `API Error: Failed to parse JSON`
- `undefined is not an object`
- `Unexpected end of JSON input`
- `API returned an empty or malformed response (HTTP 200)`
- `stream closed before response.completed`

## 调度方式

- 优先：Codex 自带 automation
- 当前仓库保底：统一调用仓库脚本
- 时间：每天 `10:30`

## 当前可用性说明

这次整理时，当前 Codex 会话没有暴露可直接注册 automation 的工具接口，所以还不能在本会话里把“每天 10:30”真正注册成 Codex 内置定时任务。

但为了不阻塞维护，这里已经先把下面这套入口沉淀好了：

1. 仓库内统一执行入口：
   - [scripts/run_cc1_daily_regression.sh](/Users/taylor/code/tools/CLIProxyAPI-ori/scripts/run_cc1_daily_regression.sh)
2. 真实同一 PTY 驱动：
   - [scripts/cc1_tty_regression.expect](/Users/taylor/code/tools/CLIProxyAPI-ori/scripts/cc1_tty_regression.expect)

后续一旦 Codex automation 接口在当前环境可用，应该直接让它每天 `10:30` 调用这套脚本，而不是再发散出第二套本机调度方案。

## 代码与配置位置

- 任务脚本：
  - [scripts/run_cc1_daily_regression.sh](/Users/taylor/code/tools/CLIProxyAPI-ori/scripts/run_cc1_daily_regression.sh)
- PTY 驱动：
  - [scripts/cc1_tty_regression.expect](/Users/taylor/code/tools/CLIProxyAPI-ori/scripts/cc1_tty_regression.expect)
- 每日产物目录：
  - [tasks/claude-client-compat/daily-regressions/README_CN.md](/Users/taylor/code/tools/CLIProxyAPI-ori/tasks/claude-client-compat/daily-regressions/README_CN.md)

## 每天实际执行的场景

当前这条定时任务会在真实同一 PTY 内跑 3 轮：

1. smoke
   - `Reply with exactly DAILY_TTY_OK`
2. 工具调用回归
   - 触发文件查看 + `go test`，覆盖 “工具执行后继续总结” 这条高风险链路
3. 继续追问
   - 再做一轮 follow-up，覆盖“同一 PTY 后续继续生成”

## 判定标准

脚本会自动做下面这些判断：

### 必须满足

- 本地代理 `:53841` 正在监听
- `tmux + python3` 驱动的 `cc1` TTY 会话成功结束
- debug-file 在第一条真实 `/v1/messages` 之后，至少出现：
  - `3` 次 `repl_main_thread` 请求
  - `3` 次 `Stream started`
  - `3` 次 `Hook Stop`

### 必须不出现

- `Failed to parse JSON`
- `undefined is not an object`
- `Unexpected end of JSON input`
- `empty or malformed response (HTTP 200)`
- `stream closed before response.completed`

## 为什么这里选择真实同 PTY，而不是只跑 `cc1 -p`

因为当前主验收口径本来就是：

- 真实终端
- 同一 PTY
- 连续多轮

仅跑 `-p` smoke 当然更稳定，但覆盖不到这轮最关键的问题：

- 工具返回后继续生成
- 同一 PTY follow-up

所以这里选择：

- 自动任务里也跑真实 PTY
- 但只保留稳定、可重复、能落到本地日志的最小场景

## 产物说明

每次运行都会在：

- `tasks/claude-client-compat/daily-regressions/<run_id>/`

生成这些文件：

- `summary.md`
- `cc1-tty.transcript`
- `cc1-tty.debug.log`
- `cc1-tty.debug.filtered.log`
- `debug-key-lines.log`
- `driver.stdout.log`
- `driver.stderr.log`
- `port-listen.txt`
- `session-before.txt`
- `session-after.txt`
- `latest-session.txt`

## 维护建议

如果以后新增了新的 bug 家族，维护动作固定是：

1. 先补到 [Bug 台账](./bug-registry_CN.md)
2. 再判断是否值得加入每日自动回归
3. 若值得，优先追加一个稳定的最小场景，而不是把任务堆得越来越重

## 未来注册为 Codex automation 时的目标配置

如果后面当前环境暴露出 Codex automation 接口，注册目标应该固定为：

- 执行时间：每天上午 `10:30`
- 工作目录：`/Users/taylor/code/tools/CLIProxyAPI-ori`
- 实际执行：`scripts/run_cc1_daily_regression.sh`
- 验收输出：
  - `tasks/claude-client-compat/daily-regressions/latest/summary.md`
  - 失败时同时查看同目录下 transcript、debug-file 和 key lines
