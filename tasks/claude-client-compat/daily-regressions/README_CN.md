# 每日 `cc1` 回归产物目录

这个目录用于保存每日 `cc1` 回归的运行产物。

调度优先级是：

1. 优先由 Codex automation 调用
2. 当前没有 automation 接口时，可人工执行
3. 不再维护额外本机调度 fallback，避免和主方案并存

## 目录规则

- 每次运行生成一个独立目录：
  - `YYYY-MM-DDTHHMMSS`
- `latest` 软链接指向最近一次运行结果

## 执行入口

- 统一入口：
  - [scripts/run_cc1_daily_regression.sh](/Users/taylor/code/tools/CLIProxyAPI-ori/scripts/run_cc1_daily_regression.sh)
- PTY 驱动：
  - [scripts/cc1_tty_regression.expect](/Users/taylor/code/tools/CLIProxyAPI-ori/scripts/cc1_tty_regression.expect)

## 每次运行至少包含

- `summary.md`
- `cc1-tty.transcript`
- `cc1-tty.debug.log`
- `cc1-tty.debug.filtered.log`
- `debug-key-lines.log`
- `driver.stdout.log`
- `driver.stderr.log`

## 入库策略

这个目录默认不提交实际运行结果，只提交：

- `README_CN.md`
- `.gitignore`

原因：

- 真实 TTY transcript 和 debug log 容量大
- 每日任务结果更适合本地长期留存，而不是进入 git 历史
