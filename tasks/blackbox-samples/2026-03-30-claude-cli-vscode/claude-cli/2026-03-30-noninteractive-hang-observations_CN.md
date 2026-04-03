# 2026-03-30 Claude CLI 非交互挂起样本

## 目标

验证真实 `claude-cli` 在当前环境下，是否能稳定执行非交互黑盒回归：

- `claude -p`
- `claude -p --output-format stream-json --verbose`

## 结论摘要

当前自动化环境下，`claude-cli` 非交互模式存在明显不稳定现象：

1. 复杂仓库分析题：
   - `stream-json` 输出仅出现首条 `system/init`
   - 长时间没有后续有效事件
   - 没有生成新的 `.cli-proxy-api/logs/v1-messages-*.log`
   - 说明请求很可能尚未真正进入代理后端

2. 极简题：
   - prompt：`2+2=? just answer with number`
   - `claude -p --verbose --model opus ...`
   - 20 秒内无 stdout / stderr
   - 最终由外层测试超时杀掉

## 样本文件

### 复杂题 stream-json 卡在 init

- `claude-cli-complex-tooling-1.stream.jsonl`
- `claude-cli-complex-tooling-1.err`

当前观察：

- `stream.jsonl` 仅 1 行
- 内容为 `system/init`
- `err` 为空

### 极简题超时

- `claude-cli-minimal-hang-prompt.txt`
- `claude-cli-minimal-hang.stdout`
- `claude-cli-minimal-hang.stderr`
- `claude-cli-minimal-hang-meta.json`

当前观察：

- stdout 为空
- stderr 为空
- meta 记录：
  - `result = timeout`
  - `elapsed_seconds ≈ 20`

## 边界判断

这批样本更像是：

- `claude-cli` 在当前自动化执行环境下的本地挂起/卡住问题

而不是：

- 代理服务端返回了错误结果
- `Claude -> Codex` 兼容层文本进度没有发出

原因：

- 未观察到新的代理 request log
- 复杂题卡在本地 `init`
- 极简题甚至没有任何输出

## 对后续回归的意义

后续继续做真实 CLI 黑盒时，需要区分两类问题：

1. `claude-cli` 本地非交互模式是否稳定发起请求
2. 请求真正进入代理后，兼容层是否提供了足够友好的实时进度

因此当前目录里的这批样本应保留，用来避免把“CLI 本地挂起”误判成“后端兼容层没工作”。
