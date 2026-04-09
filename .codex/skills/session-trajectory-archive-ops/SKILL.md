---
name: session-trajectory-archive-ops
description: Use when CLIProxyAPI session trajectory data is consuming too much PostgreSQL storage, or when you need to inspect, resume, verify, or complete a local archive-and-prune run driven by run-state.json and a stable run_id.
---

# Session Trajectory Archive Ops

## Overview

这个 skill 用于处理 CLIProxyAPI 的 session trajectory 归档清理。

目标只有四个：
- 冻结一批可安全归档的冷会话
- 导出到本地盘
- 按依赖顺序清理数据库
- 用 `run_id` 和 `run-state.json` 保证可以续跑

默认仓库：

`/Users/taylor/code/tools/CLIProxyAPI-ori`

默认归档根目录：

`/Volumes/Storage/CLIProxyAPI-session-archives`

核心脚本：

`scripts/session_trajectory_archive.py`

## When to Use

在这些场景触发：

- PostgreSQL 中 `session_trajectory_requests`、`session_trajectory_sessions` 或相关 TOAST 膨胀
- 用户要求把 session 数据导出到本地后清空一部分
- 已经有 `run_id`，需要续跑归档
- 需要核对某次归档是否已经进入删除阶段
- 需要给出后续如何继续执行的精确游标

不要在这些场景使用：

- 只是导出前端 UI 里的 session 文件，不涉及清库
- 只是查询 usage / billing
- 只是想看某个 session 的业务内容，不需要归档或清理

## Safety Rules

- 归档粒度必须是整会话，不按单条 request 或单日硬切
- 默认只处理 `last_activity_at < now() - 24h` 的冷会话
- 一旦某个 `run_id` 已经物化了候选集，后续必须继续使用同一个 `run_id`
- 删除顺序必须固定：
  - `session_trajectory_request_exports`
  - `session_trajectory_requests`
  - `session_trajectory_session_aliases`
  - `session_trajectory_sessions`
- 在看到导出文件与 `run-state.json` 计数之前，不要声称“可以清理”

## Standard Workflow

1. 先确认当前正在跑的归档。

```bash
ps -Ao pid,command | rg 'session_trajectory_archive.py'
```

2. 如果没有运行中的归档，检查最近游标。

```bash
find /Volumes/Storage/CLIProxyAPI-session-archives/runs -maxdepth 2 -name run-state.json | sort
```

3. 读取状态文件，先看三项：
- `run_id`
- `phase`
- `counts`

```bash
cat /Volumes/Storage/CLIProxyAPI-session-archives/runs/<run-id>/run-state.json
```

4. 若要新开一轮：

```bash
export APIKEY_POLICY_PG_DSN='postgres://.../cliproxy?sslmode=require'
cd /Users/taylor/code/tools/CLIProxyAPI-ori
python3 scripts/session_trajectory_archive.py \
  --output-root /Volumes/Storage/CLIProxyAPI-session-archives \
  --inactive-hours 24
```

5. 若要续跑同一轮：

```bash
export APIKEY_POLICY_PG_DSN='postgres://.../cliproxy?sslmode=require'
cd /Users/taylor/code/tools/CLIProxyAPI-ori
python3 scripts/session_trajectory_archive.py \
  --output-root /Volumes/Storage/CLIProxyAPI-session-archives \
  --run-id <run-id>
```

6. 监控时优先看三类信号：
- `run-state.json` 的 `phase`
- 归档目录里的文件体积是否继续增长
- PG `pg_stat_activity` 是否还在跑 `COPY` 或已经进入 `DELETE`

## Phase Meanings

- `initialized`
  - 刚创建 run，还没冻结候选集
- `candidates_materialized`
  - 候选 session 已冻结；这是最关键的续跑基点
- `exported`
  - 四类本地归档文件已生成并完成计数校验
- `request_exports_deleted`
  - 已开始真正清理数据库
- `requests_deleted`
  - 主体大表 request 已删完候选范围
- `aliases_deleted`
  - alias 已删完
- `sessions_deleted`
  - session 主记录已删完
- `vacuumed`
  - 已做 `VACUUM (ANALYZE)`
- `completed`
  - 本轮已结束，可准备下一轮

## What to Report

每次给用户回报进度时，至少要包含：

- 当前 `run_id`
- 当前 `phase`
- 当前输出目录
- 当前最大文件
- 是否已经进入删除阶段
- 如果现在中断，下一条续跑命令是什么

## Quick Reference

继续监控目录大小：

```bash
ls -lah /Volumes/Storage/CLIProxyAPI-session-archives/runs/<run-id>
du -sh /Volumes/Storage/CLIProxyAPI-session-archives/runs/<run-id>
```

看 PG 侧当前在干什么：

```bash
export APIKEY_POLICY_PG_DSN='postgres://.../cliproxy?sslmode=require'
psql "$APIKEY_POLICY_PG_DSN" -At -F $'\t' -c \
  "select state, wait_event_type, wait_event, now()-query_start as age
   from pg_stat_activity
   where datname='cliproxy' and application_name='psql'
   order by query_start desc limit 5;"
```

继续执行同一游标：

```bash
python3 scripts/session_trajectory_archive.py \
  --output-root /Volumes/Storage/CLIProxyAPI-session-archives \
  --run-id <run-id>
```

更多命令见：

`.codex/skills/session-trajectory-archive-ops/references/commands.md`

## Common Mistakes

- 按 `started_at` 直接删老 request
  - 风险：切断仍在续写的活跃 session
- 重新新开一轮而不是复用旧 `run_id`
  - 风险：同一批数据边界漂移，导致重复导出或难以核对
- 看到本地文件在增长就以为数据库已经开始删
  - 错误：导出和删除是两个阶段
- 只做 `DELETE` 就声称磁盘已经回收给系统
  - 错误：通常只是回收到库内可复用空间，是否还给操作系统要看后续 `VACUUM FULL` 或 `pg_repack`

## Completion Checklist

只有同时满足这些条件，才能说本轮完成：

- `run-state.json` 的 `phase` 为 `completed`
- 四类输出文件都已落盘
- `deleted` 中四类删除计数均存在
- 如未使用 `--skip-vacuum`，则 `phase` 先经过 `vacuumed`
- 已把 `run_id`、状态文件路径和续跑命令反馈给用户
