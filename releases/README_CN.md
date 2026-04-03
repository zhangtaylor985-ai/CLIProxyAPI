# Releases 说明

`releases/` 只记录已经满足阶段验收标准的版本，不记录“还在观察”的未稳定状态。

## 每个 release 建议包含

- 版本名或日期
- 目标范围
- 已验证矩阵
- 通过项
- 已知剩余风险
- 回滚边界

## 当前状态

- 截至 `2026-03-31`，`Target CC-Parity` 仍未完成阶段验收
- 原因：
  - `thinking signature` 热修已落地并通过定向验证
  - 否定式 `web search` 误判已收敛
  - 但 `stream-json` 事件退化 / 最终正文整段晚吐出问题仍未收口

因此当前先不新增 release 记录，继续以 `tasks/` 和 `todos/` 推进。
