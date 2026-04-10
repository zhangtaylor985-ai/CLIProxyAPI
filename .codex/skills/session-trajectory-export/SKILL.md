---
name: session-trajectory-export
description: 当用户要求导出 CLIProxyAPI 的 session trajectory、会话轨迹、session-data 导出文件，或询问前端“导出当前会话 / 批量导出筛选结果”后文件从哪里获取、如何从生产机拉取时使用。适用于本地仓库 `/Users/taylor/code/tools/CLIProxyAPI-ori` 和生产机 `/home/azureuser/cliapp/CLIProxyAPI` 的轨迹导出、全量导出、时间范围导出、导出路径定位与回传说明。
---

# Session Trajectory Export

## Overview

这个 skill 用于导出 CLIProxyAPI 的会话轨迹文件，并解释管理端 UI 导出后的真实落盘位置与获取方式。

优先复用仓库内原生脚本：

`/Users/taylor/code/tools/CLIProxyAPI-ori/scripts/export_session_trajectories/main.go`

当前默认原则：

- 新的长时间 live export 走托管启动
- 导出前先固化 `session id snapshot`
- 续跑时优先 `--skip-existing`

## Workflow

1. 先确认目标环境。
本地仓库默认是 `/Users/taylor/code/tools/CLIProxyAPI-ori`。
生产机默认通过 `ssh -i ~/.ssh/cohen.pem azureuser@20.240.219.163` 登录，再进入 `/home/azureuser/cliapp/CLIProxyAPI`。

2. 优先使用原生导出脚本，不直接手写 SQL 导出 JSON。
本地执行：

```bash
cd /Users/taylor/code/tools/CLIProxyAPI-ori
go run ./scripts/export_session_trajectories
```

按时间范围导出：

```bash
cd /Users/taylor/code/tools/CLIProxyAPI-ori
go run ./scripts/export_session_trajectories \
  --start-time 2026-04-01T00:00:00Z \
  --end-time 2026-04-05T23:59:59Z \
  --write-session-id-file /path/to/session-ids.txt \
  --skip-existing
```

仅统计不落文件：

```bash
cd /Users/taylor/code/tools/CLIProxyAPI-ori
go run ./scripts/export_session_trajectories --dry-run
```

如果用户要求：

- 长时间后台跑
- 断开当前会话后继续
- 在 macOS 上做托管运行

优先使用：

`scripts/managed_live_export.py`

3. 生产机导出默认用 `sudo` 执行。
原因：生产上的 `session-data/session-exports` 常见为 `root:root`，普通 `azureuser` 未必可写。

```bash
ssh -i ~/.ssh/cohen.pem azureuser@20.240.219.163 \
  'cd /home/azureuser/cliapp/CLIProxyAPI && sudo env PATH="$PATH" go run ./scripts/export_session_trajectories'
```

按时间范围导出：

```bash
ssh -i ~/.ssh/cohen.pem azureuser@20.240.219.163 \
  'cd /home/azureuser/cliapp/CLIProxyAPI && sudo env PATH="$PATH" go run ./scripts/export_session_trajectories \
    --start-time 2026-04-01T00:00:00Z \
    --end-time 2026-04-05T23:59:59Z'
```

4. 导出完成后，优先汇报三类结果。
导出的总 session 数。
导出的总文件数。
manifest 路径与 export root 路径。

如果导出还没完成，进度汇报优先包含：

- 时间窗
- `matched <N> sessions for export`
- `exported <M>/<N> sessions`
- 当前 `export_root`
- 当前 `.json` 文件数
- manifest 是否已生成

5. 如果用户问“前端 UI 点击导出后从哪里获取”。
明确说明当前 UI 只是调用 management API 触发服务端导出，不会把 JSON 下载到浏览器本地。
文件以服务端返回的 `export_dir` 为准；生产默认目录通常是：

`/home/azureuser/cliapp/CLIProxyAPI/session-data/session-exports`

6. 如果用户要求把导出结果拉回本地。
优先给 `scp` 或 `tar` 命令。

单目录拉取：

```bash
scp -i ~/.ssh/cohen.pem -r \
  azureuser@20.240.219.163:/home/azureuser/cliapp/CLIProxyAPI/session-data/session-exports/<dir> \
  /Users/taylor/Downloads/
```

整包压缩后拉取：

```bash
ssh -i ~/.ssh/cohen.pem azureuser@20.240.219.163 \
  'sudo tar -C /home/azureuser/cliapp/CLIProxyAPI/session-data -czf /tmp/session-exports.tgz session-exports'

scp -i ~/.ssh/cohen.pem \
  azureuser@20.240.219.163:/tmp/session-exports.tgz \
  /Users/taylor/Downloads/
```

## Notes

脚本支持 `user-id`、`source`、`call-type`、`status`、`provider`、`canonical-model-family`、`start-time`、`end-time` 过滤。

注意：

- `--start-time` / `--end-time` 过滤的是 `session_trajectory_sessions.last_activity_at`
- 不是单条 request 的 `started_at`
- 如果用户要导“某轮 archive 的完整冻结结果”，优先直接按同一 `run_id` 恢复整轮，不额外再切时间窗
- 对长任务，不要再把“按 `last_activity_at` 的实时查询结果”当作稳定集合
- 现在推荐先写出 `session id snapshot`，后续续跑直接复用这个 snapshot

## Resume-Safe Live Export

新的远端 / 本地长时间导出，默认用下面这组参数：

```bash
cd /Users/taylor/code/tools/CLIProxyAPI-ori
go run ./scripts/export_session_trajectories \
  --pg-dsn 'postgres://postgres:.../cliproxy?sslmode=require' \
  --start-time 2026-04-08T17:27:44Z \
  --end-time 2026-04-09T18:05:22Z \
  --export-root /Users/taylor/session-trajectory-export-live-20260408T172744Z-to-2026-04-09T180522Z \
  --manifest-dir /Users/taylor/session-trajectory-export-manifests-live \
  --write-session-id-file /Users/taylor/session-trajectory-export-manifests-live/session-ids-20260408T172744Z-to-20260409T180522Z.txt \
  --skip-existing
```

作用：

- `--write-session-id-file`
  - 先固化本次导出的稳定 session 集合
- `--skip-existing`
  - 若目录已完整存在则直接复用，不重写
- 同一时间窗中途中断后
  - 可以继续用同一个 `export_root`
  - 也可以显式改成 `--session-id-file <snapshot>` 复跑同一集合

若已经有快照文件，续跑建议直接改成：

```bash
cd /Users/taylor/code/tools/CLIProxyAPI-ori
go run ./scripts/export_session_trajectories \
  --pg-dsn 'postgres://postgres:.../cliproxy?sslmode=require' \
  --session-id-file /Users/taylor/session-trajectory-export-manifests-live/session-ids-20260408T172744Z-to-20260409T180522Z.txt \
  --export-root /Users/taylor/session-trajectory-export-live-20260408T172744Z-to-2026-04-09T180522Z \
  --manifest-dir /Users/taylor/session-trajectory-export-manifests-live \
  --skip-existing
```

## Export Progress Reporting

### Direct Export

如果当前运行的是：

```bash
go run ./scripts/export_session_trajectories ...
```

那么百分比优先按 stdout 中的：

- `matched <N> sessions for export`
- `exported <M>/<N> sessions`

直接计算。

推荐回报字段：

- `start-time`
- `end-time`
- `total_sessions`
- `exported_sessions`
- `exported_files`
- `export_root`
- `manifest_dir`
- `manifest 是否已生成`

如果是托管方式启动，还要补：

- `label`
- `state_path`

### Handoff Export

如果当前运行的是：

```bash
python3 scripts/archive_export_handoff.py ...
```

则把它拆成 3 个子阶段看：

- `migrate_session_trajectory_pg`
- `import_session_trajectory_archive.py`
- `export_session_trajectories`

不要只说“handoff 在跑”；要明确当前卡在哪一段。

### Quick Checks

看直接导出的 stdout：

```bash
tail -n 80 <export-root>/export.log
```

看当前 JSON 文件数：

```bash
find <export-root> -type f -name '*.json' | wc -l
```

看 session 目录数：

```bash
find <export-root> -mindepth 1 -maxdepth 1 -type d | wc -l
```

看 manifest 是否已生成：

```bash
find <manifest-dir> -type f | sort | tail
```

看托管导出状态：

```bash
python3 scripts/managed_live_export.py \
  --mode status \
  --label <launchctl-label> \
  --manifest-dir <manifest-dir> \
  --export-root <export-root>
```

### Report Template

```text
task 3
- 类型：live direct export
- 时间窗：start-time=... / end-time=...
- 阶段：exporting requirement-format files
- 关键计数：matched ... / exported ... / files ...
- 增长信号：5 秒内 JSON 文件数从 ... 到 ...
- 百分比：...
- 交付物：export_root=... / manifest_dir=...
```

## Archive Handoff Workflow

当某轮 archive 已经 `completed`，并且用户要把这轮 raw archive 转成需求方目录格式时，标准链路如下。

1. 起一个临时 PostgreSQL，数据目录放到 `/Volumes/Storage`。

```bash
docker rm -f cliproxy-export-pg >/dev/null 2>&1 || true
docker run -d \
  --name cliproxy-export-pg \
  -e POSTGRES_PASSWORD=root123 \
  -e POSTGRES_DB=cliproxy \
  -p 5433:5432 \
  -v /Volumes/Storage/cliproxy-export-pg-data:/var/lib/postgresql/data \
  postgres:17.6
```

2. 初始化 session trajectory 表结构。

```bash
cd /Users/taylor/code/tools/CLIProxyAPI-ori
go run ./scripts/migrate_session_trajectory_pg \
  --pg-dsn 'postgresql://postgres:root123@localhost:5433/cliproxy'
```

3. 把已完成归档恢复到临时 PG。

整轮恢复：

```bash
cd /Users/taylor/code/tools/CLIProxyAPI-ori
python3 scripts/import_session_trajectory_archive.py \
  --run-id <run-id> \
  --pg-dsn 'postgresql://postgres:root123@localhost:5433/cliproxy' \
  --truncate-target \
  --skip-request-exports \
  --require-storage-prefix /Volumes/Storage
```

若用户只要某个 `last_activity_at` 时间窗：

```bash
cd /Users/taylor/code/tools/CLIProxyAPI-ori
python3 scripts/import_session_trajectory_archive.py \
  --run-id <run-id> \
  --pg-dsn 'postgresql://postgres:root123@localhost:5433/cliproxy' \
  --truncate-target \
  --skip-request-exports \
  --require-storage-prefix /Volumes/Storage \
  --start-time 2026-04-07T11:47:43Z \
  --end-time 2026-04-07T15:29:42Z
```

4. 再从临时 PG 导出成需求方目录格式。

```bash
cd /Users/taylor/code/tools/CLIProxyAPI-ori
go run ./scripts/export_session_trajectories \
  --pg-dsn 'postgresql://postgres:root123@localhost:5433/cliproxy' \
  --export-root /Volumes/Storage/session-trajectory-export-after-<tag> \
  --manifest-dir /Volumes/Storage/session-trajectory-export-manifests
```

时间窗导出：

```bash
cd /Users/taylor/code/tools/CLIProxyAPI-ori
go run ./scripts/export_session_trajectories \
  --pg-dsn 'postgresql://postgres:root123@localhost:5433/cliproxy' \
  --start-time 2026-04-07T11:47:43Z \
  --end-time 2026-04-07T15:29:42Z \
  --export-root /Volumes/Storage/session-trajectory-export-after-20260407T114743Z \
  --manifest-dir /Volumes/Storage/session-trajectory-export-manifests
```

5. 完成后优先反馈：

- `run_id`
- 临时 PG DSN
- `export_root`
- manifest 路径
- `exported_sessions`
- `exported_files`

## One-Shot Handoff

如果用户明确要求“这轮 archive 完成后自动继续恢复并导出”，或者当前任务上下文已经把这条路径定为默认策略，优先直接用一条衔接脚本，不要手工分三段敲命令。

整轮自动衔接：

```bash
cd /Users/taylor/code/tools/CLIProxyAPI-ori
python3 scripts/archive_export_handoff.py \
  --run-id <run-id> \
  --wait-completed \
  --pg-dsn 'postgresql://postgres:root123@localhost:5433/cliproxy' \
  --export-root /Volumes/Storage/session-trajectory-export-after-<tag> \
  --manifest-dir /Volumes/Storage/session-trajectory-export-manifests \
  --skip-request-exports
```

时间窗自动衔接：

```bash
cd /Users/taylor/code/tools/CLIProxyAPI-ori
python3 scripts/archive_export_handoff.py \
  --run-id <run-id> \
  --wait-completed \
  --pg-dsn 'postgresql://postgres:root123@localhost:5433/cliproxy' \
  --start-time 2026-04-07T11:47:43Z \
  --end-time 2026-04-07T15:29:42Z \
  --export-root /Volumes/Storage/session-trajectory-export-after-20260407T114743Z \
  --manifest-dir /Volumes/Storage/session-trajectory-export-manifests \
  --skip-request-exports
```

这个脚本会持久化两类游标：

- 线上拉取游标
  - `archive_run_id`
  - `archive_cutoff_at`
  - `archive_output_dir`
  - `archive_counts`
- 生成导出游标
  - `manifest_path`
  - `export_root`
  - `filters`
  - `exported_sessions`
  - `exported_files`

持久化位置：

- `/Volumes/Storage/CLIProxyAPI-session-archives/handoffs/latest_handoff.json`
- `/Volumes/Storage/CLIProxyAPI-session-archives/handoffs/<run-id>.json`

默认行为约定：

- 当前远端这一轮 archive 只要成功完成，就继续自动执行这一段 handoff
- 不再额外等待人工确认
- 除非用户显式改时间窗、导出路径、临时 PG DSN，或运行环境异常

## Common Handoff Mistakes

- 只拿 `session_trajectory_requests.jsonl.gz` 就开始恢复
  - 错误：至少还需要同轮的 `session_trajectory_sessions.jsonl.gz`
- 归档还没 `completed` 就开始 handoff
  - 风险：边界未封口，后处理结果不稳定
- 把恢复数据灌回常用本地库
  - 风险：污染日常环境，也更容易把系统盘写满
- 把时间窗理解成 request 时间
  - 错误：这里默认按 session 的 `last_activity_at`

脚本会输出 manifest JSON，可用于后续前端异步导出任务或离线核对。

## Managed Live Export

如果用户要求“当前会话断开后也继续跑 live export”，不要直接把正在进行中的普通导出进程中途迁移为托管任务。

默认策略：

- 正在跑的 live export 保持现状，不中途接管
- 下一轮 live export 从启动时就使用托管脚本

原因：

- 当前原生 `export_session_trajectories` 没有中途接管/续跑语义
- 中途迁移更容易引入重复导出或状态不一致

启动新的托管 live export：

```bash
cd /Users/taylor/code/tools/CLIProxyAPI-ori
python3 scripts/managed_live_export.py \
  --mode submit \
  --pg-dsn 'postgres://postgres:.../cliproxy?sslmode=require' \
  --start-time 2026-04-08T17:27:44Z \
  --end-time 2026-04-09T18:05:22Z \
  --export-root /Users/taylor/session-trajectory-export-live-20260408T172744Z-to-2026-04-09T180522Z \
  --manifest-dir /Users/taylor/session-trajectory-export-manifests-live
```

它会：

- 用 `launchctl submit` 托管导出
- 写 `state.json`
- 完成后记录 manifest 路径和导出计数
- 新的默认 live export 应额外带上 `--write-session-id-file` 与 `--skip-existing`
- 如果 `managed_live_export.py` 还没封装这两个参数，直接在托管命令里调用原生 `go run` 版本即可

查看状态：

```bash
cd /Users/taylor/code/tools/CLIProxyAPI-ori
python3 scripts/managed_live_export.py \
  --mode status \
  --label <launchctl-label> \
  --export-root /Users/taylor/session-trajectory-export-live-... \
  --manifest-dir /Users/taylor/session-trajectory-export-manifests-live
```

当用户进一步要求“批量导出做成异步、可看进度、按时间范围先估算数据量再确认执行”时，先复用当前脚本与现有 management session trajectory 查询链路做方案分析，再决定是否新增后端异步任务接口。
