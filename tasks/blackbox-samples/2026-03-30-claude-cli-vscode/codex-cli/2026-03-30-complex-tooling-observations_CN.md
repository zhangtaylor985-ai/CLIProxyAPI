# 2026-03-30 Codex CLI 复杂工具调用对比样本

## 目标

给 `claude-cli -> GPT/Codex` 兼容层提供一个可复用的 `codex cli` 真实过程参考，重点看：

- 首个有效进度是否足够早
- 是否持续显示正在工作
- 多工具调用时是否暴露额外 fake thinking

## 复现命令

```bash
codex exec --json --skip-git-repo-check -C /Users/taylor/code/tools/CLIProxyAPI-ori -s danger-full-access --color never -c model_reasoning_effort="high" "$(cat tasks/blackbox-samples/2026-03-30-claude-cli-vscode/codex-cli/2026-03-30-complex-tooling-prompt.txt)"
```

## 观察摘要

- 可见事件直接是结构化过程事件：
  - `agent_message`
  - `command_execution started/completed`
- 本次样本统计：
  - `agent_message`: 6
  - `command_execution`: 30 组 started/completed
- 未观察到额外 reasoning / thinking 外露

## 用法

后续如果要判断 `claude-cli` 复杂工具任务是否还“不像 codex”，优先对照这个样本看：

1. 首个有效进度是否更晚
2. 中间是否长时间只有聚合摘要
3. 是否出现额外 `thinking`
4. 工具调用过程是否足够清晰

## 当前结论

- `codex cli` 的过程感重点不在 verbose thinking，而在：
  - 简短 agent message
  - 明确工具执行
  - 工具结果持续可见
- `claude-cli` 兼容层的后续收口，应继续围绕“真实 tool call 的短进度”推进
