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

默认长期链路状态根目录：

`/Users/taylor/CLIProxyAPI-session-archives-local/handoffs`

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
- 所有游标与状态文件统一记录 `UTC` / RFC3339 `Z` 时间
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

如果当前跑的是长期托管链路，而不是单个 archive 进程，优先再看：

```bash
cd /Users/taylor/code/tools/CLIProxyAPI-ori
python3 scripts/archive_handoff_loop.py \
  --mode status \
  --archive-pg-dsn 'postgres://.../cliproxy?sslmode=require' \
  --archive-root /Users/taylor/CLIProxyAPI-session-archives-local \
  --handoff-dir /Users/taylor/CLIProxyAPI-session-archives-local/handoffs \
  --manifest-dir /Users/taylor/session-trajectory-export-manifests \
  --target-pg-dsn 'postgresql://postgres:root123@localhost:5434/cliproxy' \
  --skip-request-exports
```

对应 summary 文件默认在：

`/Users/taylor/CLIProxyAPI-session-archives-local/handoffs/archive_handoff_loop.summary.json`

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

如果当前不是单一 archive，而是“archive / handoff / live export”多任务并行，统一按下面格式回报：

- `任务号`
- `任务类型`
- `时间窗或游标`
- `当前阶段`
- `关键计数`
- `增长信号`
- `百分比估计`
- `后续衔接点`

推荐固定成这 3 类任务名，避免口径漂移：

- `task 1`
  - 已完成 raw archive 的恢复导入
- `task 2`
  - 已完成 archive 的 handoff
  - 即 `migrate -> import -> export`
- `task 3`
  - 直接从 PG 导需求方格式

当前默认长期链路已经收敛为：

- `archive_handoff_loop.py`

`live_export_delete_loop.py` 只作为备用脚本保留，不再默认常驻。

## Progress Estimation

回报百分比时，不要拍脑袋；优先按实际阶段给区间。

### Archive Mainline

- `initialized`
  - `0%`
- `candidates_materialized`
  - 如果只有候选集，没有导出文件增长
  - `5% - 10%`
- `candidates_materialized`
  - 且 `session_trajectory_requests.jsonl.gz` 正在增长
  - `10% - 50%`
- `exported`
  - 四类 raw archive 文件已落盘，但还没开始删除
  - `50% - 60%`
- `request_exports_deleted` / `requests_deleted` / `aliases_deleted` / `sessions_deleted`
  - 已进入线上清理
  - `60% - 90%`
- `vacuumed`
  - `90% - 99%`
- `completed`
  - `100%`

### Import / Restore

- 仅完成 `candidate_sessions.csv`
  - `0% - 5%`
- 已准备 `session_trajectory_sessions.csv`
  - `5% - 10%`
- `session_trajectory_requests.csv` 正在增长
  - `10% - 35%`
- 开始 `\copy` 入 PG，且本地 PG 表行数上涨
  - `40% - 75%`
- PG 内数据已齐，但需求方格式导出还没开始
  - `75% - 85%`
- manifest / handoff 记录已生成
  - `100%`

### Direct Export

如果 stdout 能拿到：

- `matched <N> sessions for export`
- `exported <M>/<N> sessions`

则直接按 `M / N` 给百分比，这是优先级最高的口径。

如果 stdout 暂时拿不到，再降级看：

- session 目录数
- `.json` 文件数
- export root 体积是否继续增长
- manifest 是否已生成

### Growth Signals

进度汇报时，至少给一个“还在前进”的证据：

- 某个 `.jsonl.gz` 或 `.csv` 文件 10 秒采样内继续变大
- 若最终 `.gz` 还没出现，则看 `.session_trajectory_requests.jsonl.tmp` 是否继续变大
- PG `n_live_tup` 继续上涨
- `exported M/N sessions` 中的 `M` 继续增加
- manifest 从不存在变成已生成

如果进程还活着，但 2 次短采样都没有增长信号，不要说“稳定推进”，应改成：

- `进程仍在，但当前采样窗口内未观察到增长`
- `需要继续确认是否卡住或只是处于单个大 session 的长尾阶段`

## Standard Progress Reply

给用户汇报时，优先按下面模板：

```text
task 1
- 类型：raw archive restore -> temp PG
- 游标：run_id=... / min_last_activity_at=... / max_last_activity_at=... / cutoff_at=...
- 阶段：正在生成 session_trajectory_requests.csv
- 关键计数：selected sessions=... / requests.csv=...
- 增长信号：10 秒内从 ... 增长到 ...
- 百分比：约 ...
- 下一步：进入 \copy 入 PG

task 2
- 类型：archive handoff
- 游标：run_id=... / archive_cutoff_at=...
- 阶段：migrate 已完成，import 进行中
- 关键计数：selected sessions=... / requests.csv=...
- 增长信号：...
- 百分比：约 ...
- 下一步：导入 5434 后自动转 export

task 3
- 类型：live direct export
- 时间窗：start-time=... / end-time=...
- 阶段：exporting requirement-format files
- 关键计数：exported ... / ... sessions / ... files
- 增长信号：5 秒内 JSON 文件数从 ... 到 ...
- 百分比：...
- 下一步：生成 manifest
```

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

## macOS Background Jobs

在 macOS 上，如果任务需要脱离当前 agent / 终端会话长期运行，优先使用 `launchctl submit`，不要默认依赖临时 PTY 或普通后台 `&`。

示例：

```bash
launchctl submit -l com.codex.session_handoff_<run-id> \
  -o /path/to/handoff.log \
  -e /path/to/handoff.log -- \
  /bin/zsh -lc 'cd /Users/taylor/code/tools/CLIProxyAPI-ori && python3 scripts/archive_export_handoff.py ...'
```

注意：

- `launchd` 的默认 PATH 很短
- 当前导入脚本已自动探测 Homebrew/libpq 的 `psql`
- 但如果你自己写包装命令，仍应假设环境变量比交互 shell 更少
- `scripts/archive_export_handoff.py` 现在默认对同一个 `run_id` 幂等
- 如果对应的 handoff record 和 manifest 已存在，它会直接 `skip`，不会重复 `truncate/import/export`
- 只有在明确需要重做时，才加 `--force-rerun`

## Import Lock

当前 `scripts/import_session_trajectory_archive.py` 默认会拿一个全局锁：

- `/tmp/cliproxy-session-trajectory-import.lock`

目的只有一个：

- 避免多条大 restore/import 同时生成超大 `session_trajectory_requests.csv`
- 减少本机内存、swap、磁盘写放大和互相抢资源导致的中断

如果看到：

- `[wait] import lock busy ...`

这不是卡死，而是在等前一条大恢复任务完成。

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

## Post-Archive Handoff

当用户后续要把某轮归档继续转成“数据方要求的会话目录格式”时，不要重新回线上库读老数据，直接把这轮 `run_id` 交给导出链路。

只有在这些条件都满足后，才允许 handoff：

- `phase=completed`
- 四类归档文件都在同一 `output_dir`
- `run_id`、`cutoff_at`、`counts` 已核对

交接时至少要给出：

- `run_id`
- `output_dir`
- `cutoff_at`
- `counts`
- 是否需要整轮导出，还是只导某个 `last_activity_at` 时间窗

后续具体恢复到临时 PG 并导成需求格式时，切到：

`.codex/skills/session-trajectory-export/SKILL.md`

如果用户要求“本轮 archive 一完成就自动继续恢复并导出”，直接使用：

`scripts/archive_export_handoff.py`

这条路径现在就是默认路径。

如果用户要求“从启动 archive 开始就整条链路自动跑完”，优先使用：

`scripts/managed_archive_handoff.py`

它会自动完成：

- 启动 `session_trajectory_archive.py`
- 从 archive stdout 解析本轮 `run_id`
- archive 完成后继续执行 `archive_export_handoff.py`
- 把当前链路状态写到 `active_archive_chain.json`

如果用户要求“长期一直跑，完成一轮后继续下一轮”，优先使用：

`scripts/archive_handoff_loop.py`

这条长期任务默认：

- 用文件持久化游标，不写数据库
- 每轮只处理 `last_activity_at < now() - 24h` 的冷会话
- 远端 archive 完成后自动接 handoff
- handoff 完成后更新 cursor 文件，再继续轮询下一轮
- 若单轮失败，会优先续同一个 `run_id`

默认状态文件：

- `/Users/taylor/CLIProxyAPI-session-archives-local/handoffs/archive_handoff_loop.state.json`
- `/Users/taylor/CLIProxyAPI-session-archives-local/handoffs/archive_handoff_loop.cursor.json`

如果用户没有明确要求停在 raw archive、也没有改目标时间窗或目标目录，就不要在 `phase=completed` 后停住等待，再次确认；应直接继续 handoff。

这个脚本会在归档完成后继续执行：

- `scripts/migrate_session_trajectory_pg`
- `scripts/import_session_trajectory_archive.py`
- `scripts/export_session_trajectories`

并把两类游标写入：

- `/Volumes/Storage/CLIProxyAPI-session-archives/handoffs/active_archive_cursor.json`
- `/Volumes/Storage/CLIProxyAPI-session-archives/handoffs/latest_handoff.json`
- `/Volumes/Storage/CLIProxyAPI-session-archives/handoffs/<run-id>.json`

只有在以下情况才暂停默认 handoff 并回到人工确认：

- `run-state.json` 未进入 `completed`
- `/Volumes/Storage` 可用空间不足
- 临时 PG 不可用
- 用户显式要求修改时间窗、PG DSN 或导出根目录
