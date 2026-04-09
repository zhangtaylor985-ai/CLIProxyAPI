# Session Trajectory Archive 项目级 Skill 设计

## 目标

为当前仓库补一个可复用的项目级 skill，覆盖 session trajectory 归档清理的完整操作入口。

## 方案

采用单一总控 skill：

- 名称：`session-trajectory-archive-ops`
- 位置：`.codex/skills/session-trajectory-archive-ops`
- 主文件：`SKILL.md`
- 补充参考：`references/commands.md`
- 默认工作仓库：`/Users/taylor/code/tools/CLIProxyAPI-ori`

## 原因

- 后续最常见的操作不是“写代码”，而是“接手某个已有 run 继续做”
- 单 skill 入口最适合快速触发，不需要先判断选哪个 skill
- 归档、续跑、监控、验收天然属于同一条运维链路

## 内容范围

- 何时触发
- 关键安全约束
- 新建 run 与续跑 run 的标准命令
- 如何从 `run-state.json` 读取游标
- 如何给用户汇报阶段状态
- 常见误区与完成条件

## 不纳入本次 skill 的内容

- 前端会话导出 UI 行为
- 一般 usage / billing 导出
- PostgreSQL 分区设计
