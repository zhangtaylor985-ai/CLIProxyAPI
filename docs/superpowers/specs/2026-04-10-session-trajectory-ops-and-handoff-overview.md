# Session Trajectory 归档、恢复与需求格式导出总览

## 1. 文档目的

这份文档说明 CLIProxyAPI 当前针对 session trajectory 数据的完整运维链路，覆盖以下内容：

- 线上 PostgreSQL 如何做冷会话归档与清理
- raw archive 如何恢复到临时 PostgreSQL
- 如何再导出成需求方要求的目录与 JSON 格式
- 各脚本之间的关系
- 四张 session trajectory 表分别做什么
- 两类游标分别是什么、存在哪里、如何续跑
- 当前默认路径：`archive 完成后自动无人值守生成数据方文件`

当前默认仓库：

- `/Users/taylor/code/tools/CLIProxyAPI-ori`

当前默认大盘目录：

- `/Volumes/Storage`

需求格式依据：

- [ai-gateway-session-trajectory-format_CN.md](/Users/taylor/code/tools/CLIProxyAPI-ori/docs/requirements/ai-gateway-session-trajectory-format_CN.md)

## 2. 整体目标

核心目标只有两件事：

1. 把线上已经冷却的 `session_trajectory_*` 数据安全归档到本地大盘，随后清空线上对应数据，缓解 PostgreSQL 磁盘压力。
2. 当业务方需要交付目录化的会话文件时，不再回线上库扫历史，而是从已归档的 raw archive 回灌到临时 PG，再导出成需求方格式。

这里的关键原则是：

- 线上归档和需求格式导出是两段链路，但通过 `run_id` 可以稳定衔接
- 归档按“整会话”冻结，不按单条 request 硬切
- 需求格式导出过滤的是 `session_trajectory_sessions.last_activity_at`
- 默认所有大文件都落到 `/Volumes/Storage`，避免继续挤占系统盘

## 3. 核心脚本与关系

### 3.0 当前长期默认链路

当前默认长期链路已经明确恢复为：

- [archive_handoff_loop.py](/Users/taylor/code/tools/CLIProxyAPI-ori/scripts/archive_handoff_loop.py)

也就是：

1. 线上 `session_trajectory_archive.py` 冻结并导出 raw archive
2. 线上删除同一批冷会话
3. 本地自动执行 `archive_export_handoff.py`
4. 自动接 `migrate -> import -> export`
5. 写回 handoff 记录与游标，继续下一轮

当前口径：

- 默认常驻只跑旧模式
- `live_export_delete_loop.py` 仅保留为人工排障 / 对比验证备用
- 新旧模式的状态与游标时间统一使用 `UTC` / RFC3339 `Z`

时间换算示例：

- `2026-04-12 20:00:00 +08:00 = 2026-04-12T12:00:00Z`

### 3.1 线上归档清理脚本

- [session_trajectory_archive.py](/Users/taylor/code/tools/CLIProxyAPI-ori/scripts/session_trajectory_archive.py)

职责：

- 冻结一批 `last_activity_at < cutoff_at` 的冷会话
- 生成 `candidate_sessions.csv`
- 从线上 PG 导出 4 份 raw archive：
  - `session_trajectory_sessions.jsonl.gz`
  - `session_trajectory_requests.jsonl.gz`
  - `session_trajectory_session_aliases.jsonl.gz`
  - `session_trajectory_request_exports.jsonl.gz`
- 导出完成后按依赖顺序删除线上数据
- 持续更新 `run-state.json`

这个脚本是“线上减压”的入口，也是后续一切恢复与导出的 source of truth。

### 3.2 临时 PG 结构初始化脚本

- [main.go](/Users/taylor/code/tools/CLIProxyAPI-ori/scripts/migrate_session_trajectory_pg/main.go)

职责：

- 在目标 PostgreSQL 中确保 session trajectory 相关 schema / table 已存在
- 不负责导数，只负责把目标 PG 准备好

常见用法是对本地临时 PG 执行一次：

```bash
go run ./scripts/migrate_session_trajectory_pg \
  --pg-dsn 'postgresql://postgres:root123@localhost:5433/cliproxy'
```

### 3.3 raw archive 恢复脚本

- [import_session_trajectory_archive.py](/Users/taylor/code/tools/CLIProxyAPI-ori/scripts/import_session_trajectory_archive.py)

职责：

- 读取某个 `run_id` 对应的 4 份 `jsonl.gz`
- 按 `session_trajectory_sessions.last_activity_at` 过滤时间窗
- 生成中间 CSV
- 通过 `psql \copy` 导入临时 PG
- 支持 `--truncate-target`
- 支持 `--require-storage-prefix /Volumes/Storage`，避免误写到系统盘
- 支持 `--skip-request-exports`

这个脚本解决的是“raw archive 不能直接喂给 Go exporter”的问题。

### 3.4 需求格式导出脚本

- [main.go](/Users/taylor/code/tools/CLIProxyAPI-ori/scripts/export_session_trajectories/main.go)

职责：

- 从 PostgreSQL 中查询 session trajectory 数据
- 按需求方约定导出目录：
  - `export_root/[user_id]_[session_id]/[index]_[request_id].json`
- 输出 manifest

注意：

- 时间过滤用的是 `session_trajectory_sessions.last_activity_at`
- 它不是直接读 raw archive 文件，而是读数据库

### 3.5 一键衔接脚本

- [archive_export_handoff.py](/Users/taylor/code/tools/CLIProxyAPI-ori/scripts/archive_export_handoff.py)

职责：

- 等待某个 archive `run_id` 到达 `phase=completed`
- 自动执行 `migrate -> import -> export`
- 生成 handoff 记录

这是“archive 完成后自动无人值守生成数据方文件”的默认路径。

## 4. 四张表分别做什么

### 4.1 `session_trajectory_sessions`

会话主表。

主要记录：

- `id`
- `user_id`
- `source`
- `call_type`
- `provider`
- `canonical_model_family`
- `provider_session_id`
- `started_at`
- `last_activity_at`
- `status`
- `metadata`

作用：

- 表示“一个完整会话”
- 是导出目录粒度的主键来源
- 需求格式导出的时间过滤也是基于这张表的 `last_activity_at`

### 4.2 `session_trajectory_requests`

请求主表。

主要记录：

- `id`
- `request_id`
- `session_id`
- `request_index`
- `provider_request_id`
- `status`
- `started_at`
- `ended_at`
- token 与 cost 字段
- `request_json`
- `response_json`
- `normalized_json`
- `error_json`

作用：

- 表示会话内的一次模型交互
- 最终导出的单个 JSON 文件，主体内容主要来自这里
- 这是线上最重、最占空间的一张表

### 4.3 `session_trajectory_session_aliases`

会话别名映射表。

主要记录：

- `provider_session_id`
- `session_id`
- `user_id`
- `source`

作用：

- 维护上游 `provider_session_id` 到服务端 canonical `session_id` 的映射
- 用于归并同一会话的不同来源标识

### 4.4 `session_trajectory_request_exports`

请求导出映射表。

主要记录：

- `request_id`
- `session_id`
- `export_path`
- `export_index`
- `exported_at`
- `export_version`

作用：

- 记录某次请求此前是否已经被导出、落在哪个路径、排序序号是什么
- 对“重新恢复 raw archive 后再次导需求格式”不是绝对必需
- 当前默认 handoff 命令通常会带 `--skip-request-exports`

## 5. 从 0 到 1 的整体流程

### 阶段 A：线上冻结并归档冷会话

执行：

```bash
export APIKEY_POLICY_PG_DSN='postgres://.../cliproxy?sslmode=require'
cd /Users/taylor/code/tools/CLIProxyAPI-ori
python3 scripts/session_trajectory_archive.py \
  --output-root /Volumes/Storage/CLIProxyAPI-session-archives \
  --inactive-hours 24
```

脚本动作：

1. 计算 `cutoff_at = now - inactive_hours`
2. 从线上 `session_trajectory_sessions` 选出 `last_activity_at < cutoff_at` 的整会话
3. 写 `candidate_sessions.csv`
4. 导出 4 份 `jsonl.gz`
5. 校验导出计数
6. 依次删除：
   - `session_trajectory_request_exports`
   - `session_trajectory_requests`
   - `session_trajectory_session_aliases`
   - `session_trajectory_sessions`
7. `VACUUM (ANALYZE)`
8. 把 `run-state.json` 更新到 `completed`

### 阶段 B：把 raw archive 恢复到临时 PG

临时 PG 当前默认是：

- Docker 容器：`cliproxy-export-pg`
- DSN：`postgresql://postgres:root123@localhost:5433/cliproxy`
- 数据目录：`/Volumes/Storage/cliproxy-export-pg-data`

先初始化结构：

```bash
cd /Users/taylor/code/tools/CLIProxyAPI-ori
go run ./scripts/migrate_session_trajectory_pg \
  --pg-dsn 'postgresql://postgres:root123@localhost:5433/cliproxy'
```

再导入 raw archive：

```bash
cd /Users/taylor/code/tools/CLIProxyAPI-ori
python3 scripts/import_session_trajectory_archive.py \
  --run-id <run-id> \
  --pg-dsn 'postgresql://postgres:root123@localhost:5433/cliproxy' \
  --truncate-target \
  --skip-request-exports \
  --require-storage-prefix /Volumes/Storage
```

如果只恢复某个 `last_activity_at` 时间窗，则追加：

```bash
  --start-time 2026-04-07T11:47:43Z \
  --end-time 2026-04-07T15:29:42Z
```

### 阶段 C：从临时 PG 导出需求方格式

执行：

```bash
cd /Users/taylor/code/tools/CLIProxyAPI-ori
go run ./scripts/export_session_trajectories \
  --pg-dsn 'postgresql://postgres:root123@localhost:5433/cliproxy' \
  --start-time 2026-04-07T11:47:43Z \
  --end-time 2026-04-07T15:29:42Z \
  --export-root /Volumes/Storage/session-trajectory-export-after-20260407T114743Z \
  --manifest-dir /Volumes/Storage/session-trajectory-export-manifests
```

输出：

- 一个按 session 分目录的导出目录
- 一个 manifest JSON

### 阶段 D：默认无人值守衔接

默认路径已经定为：

- 当前远端这轮 archive 完成后，自动执行 `migrate -> import -> export`

对应脚本就是：

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

如果需要按时间窗导需求方文件，再加：

```bash
  --start-time 2026-04-07T11:47:43Z \
  --end-time 2026-04-07T15:29:42Z
```

如果要从 archive 启动开始就整条链路后台跑完，现在额外提供：

`scripts/managed_archive_handoff.py`

它负责：

1. 启动 `session_trajectory_archive.py`
2. 自动解析本轮 `run_id`
3. archive 完成后继续执行 `archive_export_handoff.py`
4. 把链路状态写入 `active_archive_chain.json`

如果要长期连续运行，不停处理新的冷数据批次，现在再提供：

`scripts/archive_handoff_loop.py`

它的默认设计是：

1. 查询远端当前可归档冷会话
2. 若存在冷会话，则执行一轮 `archive -> handoff`
3. 完成后更新文件游标
4. 继续等待下一轮

### 阶段 E：更简单的单任务长期链路

如果不需要保留 raw archive 中间产物，而是更关心：

- 持续出最终文件
- 持续清理远端冷数据
- 降低本地磁盘和流程复杂度

则默认推荐：

```bash
cd /Users/taylor/code/tools/CLIProxyAPI-ori
python3 scripts/live_export_delete_loop.py \
  --pg-dsn 'postgres://postgres:.../cliproxy?sslmode=require'
```

它会长期循环执行：

1. 快照 `last_activity_at < now() - inactive_hours` 的 session 集合
2. 用 `export_session_trajectories` 直接导最终目录
3. 删除同一批仍保持冷态的远端数据
4. 可选 `VACUUM (ANALYZE)`
5. 等待下一轮

默认状态文件：

- `~/CLIProxyAPI-session-live-export-loop/state.json`
- `~/CLIProxyAPI-session-live-export-loop/cursor.json`
- `~/CLIProxyAPI-session-live-export-loop/summary.json`

默认导出目录：

- `~/session-trajectory-export-live-delete/<run-id>`

默认 manifest 目录：

- `~/session-trajectory-export-manifests-direct`

实时查看：

```bash
cd /Users/taylor/code/tools/CLIProxyAPI-ori
python3 scripts/live_export_delete_loop.py \
  --mode status \
  --pg-dsn 'postgres://postgres:.../cliproxy?sslmode=require'
```

游标和状态默认都落文件，不写数据库：

- `/Users/taylor/CLIProxyAPI-session-archives-local/handoffs/archive_handoff_loop.state.json`
- `/Users/taylor/CLIProxyAPI-session-archives-local/handoffs/archive_handoff_loop.cursor.json`
- `/Users/taylor/CLIProxyAPI-session-archives-local/handoffs/archive_handoff_loop.summary.json`

实时查看长期链路状态，优先执行：

```bash
cd /Users/taylor/code/tools/CLIProxyAPI-ori
python3 scripts/archive_handoff_loop.py \
  --mode status \
  --archive-pg-dsn 'postgres://postgres:.../cliproxy?sslmode=require' \
  --archive-root /Users/taylor/CLIProxyAPI-session-archives-local \
  --handoff-dir /Users/taylor/CLIProxyAPI-session-archives-local/handoffs \
  --manifest-dir /Users/taylor/session-trajectory-export-manifests \
  --target-pg-dsn 'postgresql://postgres:root123@localhost:5434/cliproxy' \
  --skip-request-exports
```

说明：

- 当 `session_trajectory_requests.jsonl.gz` 还没落盘时，状态会改看 `.session_trajectory_requests.jsonl.tmp`
- 这能更早反映 archive 仍在推进，而不是误判成“没有进度”

推荐托管命令示例：

```bash
launchctl submit -l com.codex.archive_handoff_<tag> \
  -o /path/to/archive-handoff.log \
  -e /path/to/archive-handoff.log -- \
  /bin/zsh -lc 'cd /Users/taylor/code/tools/CLIProxyAPI-ori && \
    PATH="/opt/homebrew/bin:/opt/homebrew/opt/libpq/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:$PATH" \
    python3 scripts/managed_archive_handoff.py \
      --archive-pg-dsn "postgres://postgres:.../cliproxy?sslmode=require" \
      --archive-root /Users/taylor/CLIProxyAPI-session-archives-local \
      --handoff-dir /Users/taylor/CLIProxyAPI-session-archives-local/handoffs \
      --manifest-dir /Users/taylor/session-trajectory-export-manifests \
      --target-pg-dsn "postgresql://postgres:root123@localhost:5434/cliproxy" \
      --skip-request-exports'
```

## 6. 两类游标是什么

### 6.1 Source Cursor

这是“线上归档游标”，描述一轮 raw archive 的边界。

最小必要信息：

- `archive_run_id`
- `archive_cutoff_at`
- `archive_output_dir`
- `archive_phase`
- `archive_counts`
- `candidate_file`
- `candidate_sessions`
- `min_last_activity_at`
- `max_last_activity_at`

它回答的问题是：

- 线上这轮归档跑到哪了
- 这轮冻结了哪些 session
- 以后如果要续跑，应该接哪一个 `run_id`

### 6.2 Export Cursor

这是“需求格式导出游标”，描述某次 handoff 后的交付结果。

最小必要信息：

- `manifest_path`
- `export_root`
- `filters`
- `exported_sessions`
- `exported_files`

它回答的问题是：

- 这批需求方文件落在哪里
- 是按什么时间窗导的
- 导了多少 session / 文件

## 7. 游标当前持久化位置

当前已经持久化的关键文件如下。

### 7.1 当前活动 archive 游标

- `/Volumes/Storage/CLIProxyAPI-session-archives/handoffs/active_archive_cursor.json`

当前记录的是运行中的：

- `archive_run_id = session-archive-20260409T122346Z`
- `archive_cutoff_at = 2026-04-08T12:23:46Z`

### 7.2 最近一次完成的 handoff

- `/Volumes/Storage/CLIProxyAPI-session-archives/handoffs/latest_handoff.json`

### 7.3 按 run_id 归档的 handoff 记录

- `/Volumes/Storage/CLIProxyAPI-session-archives/handoffs/session-archive-20260408T152942Z.json`

### 7.4 原始 archive 状态文件

- `/Volumes/Storage/CLIProxyAPI-session-archives/runs/<run-id>/run-state.json`

这个文件是续跑 archive 的第一依据。

## 8. 当前已完成批次

当前已经完成并成功导成需求方格式的一批是：

- `archive run_id = session-archive-20260408T152942Z`
- `archive cutoff_at = 2026-04-07T15:29:42Z`

随后用这轮 raw archive 恢复并导出了时间窗：

- `2026-04-07T11:47:43Z` 到 `2026-04-07T15:29:42Z`

导出结果：

- `export_root = /Volumes/Storage/session-trajectory-export-after-20260407T114743Z`
- `manifest = /Volumes/Storage/session-trajectory-export-manifests/session-trajectory-export-20260409T130837Z.json`
- `exported_sessions = 470`
- `exported_files = 7873`

这里要注意：

- 这个时间窗过滤的是 `session_trajectory_sessions.last_activity_at`
- 所以 `/Volumes/Storage/session-trajectory-export-after-20260407T114743Z` 对应的是该时间窗内的 session 集合，不是 request `started_at` 的硬切片

## 9. 当前正在跑的远端批次

截至文档编写时，运行中的归档是：

- `run_id = session-archive-20260409T122346Z`
- `cutoff_at = 2026-04-08T12:23:46Z`
- `output_dir = /Volumes/Storage/CLIProxyAPI-session-archives/runs/session-archive-20260409T122346Z`
- `run-state.json phase = request_exports_deleted`

冻结范围：

- `candidate_sessions = 2338`
- `min_last_activity_at = 2026-04-07T15:30:06Z`
- `max_last_activity_at = 2026-04-08T12:23:36Z`

这轮 raw archive 预期计数：

- `sessions = 2338`
- `requests = 22041`
- `aliases = 365`
- `request_exports = 3354`

文档更新时的运行快照：

- 4 份 raw archive 文件已经全部落盘
- `request_exports` 删除已完成：`3354 / 3354`
- `requests` 删除进行中：`11500 / 22041`
- live PG 当前活动 SQL 已从 `COPY` 切到 `DELETE ... FROM session_trajectory_requests`

这说明这轮已经正式进入线上清理阶段，但还没有完成整轮删除与后续 vacuum。

## 10. 默认执行策略

当前默认策略已经明确为：

1. 线上 archive 正常跑完
2. 一旦 `run-state.json` 进入 `completed`
3. 自动执行 `archive_export_handoff.py`
4. 把 raw archive 恢复到 `/Volumes/Storage` 上的临时 PG
5. 再导出需求方格式目录
6. 写回 handoff 记录，便于后续继续执行

这个策略的好处是：

- 线上 archive 与需求格式导出自然衔接
- 不需要再回线上库读历史冷数据
- 本地交付可重复、可核对、可续跑

### 10.1 当前默认路径的操作约定

从现在起，默认把下面这条路径视为标准操作，而不是临时约定：

- “当前远端这轮完成后自动无人值守生成数据方文件”

也就是说：

- 只要当前远端 archive 这一轮成功进入 `phase=completed`
- 且目标导出路径、临时 PG 路径仍然指向 `/Volumes/Storage`
- 且没有用户明确要求改时间窗、改 PG DSN、改 export root

就直接继续执行 handoff，不再额外等待人工二次确认。

默认延续的内容包括：

- 使用同一 `run_id` 作为 source cursor
- 恢复到 `/Volumes/Storage` 上的临时 PG
- 生成需求方格式目录
- 更新 `latest_handoff.json`
- 更新 `<run-id>.json`

只有在以下情况才中断默认无人值守路径并改为人工确认：

- 当前远端 archive 失败或卡住
- 本地临时 PG 不可用
- `/Volumes/Storage` 空间不足
- 用户要求新的时间窗、不同导出目录或不同交付格式

## 11. 当前建议的运维认知

要把这套链路理解成三层，而不是一层：

### 第一层：线上减压层

- 工具：`session_trajectory_archive.py`
- 目标：把线上冷会话归档并删除

### 第二层：本地恢复层

- 工具：`migrate_session_trajectory_pg` + `import_session_trajectory_archive.py`
- 目标：把 raw archive 恢复成一个可查询、可再导出的临时 PG

### 第三层：交付导出层

- 工具：`export_session_trajectories`
- 目标：产出符合需求方格式的 session 目录与 JSON 文件

`archive_export_handoff.py` 只是把第二层和第三层串起来。

## 12. 后续执行时最重要的两个文件

如果后续要继续执行，优先先看这两个：

- `/Volumes/Storage/CLIProxyAPI-session-archives/runs/<run-id>/run-state.json`
- `/Volumes/Storage/CLIProxyAPI-session-archives/handoffs/latest_handoff.json`

前者告诉你 raw archive 跑到哪了，后者告诉你需求方导出已经交付到哪了。

## 13. 当前结论

当前链路已经具备以下能力：

- 可从线上 PG 归档并清理冷会话
- 可把已归档 raw archive 恢复到 `/Volumes/Storage` 上的临时 PG
- 可从临时 PG 导出需求方目录格式
- 可通过 `run_id` 与 handoff 记录实现续跑
- 默认路径已经明确为“archive 完成后自动无人值守生成数据方文件”

当前剩余工作重点不是流程设计，而是持续盯住运行中的 archive，并继续硬化大体量 `requests.jsonl.gz` 的导入稳定性。

## 14. 标准进度汇报口径

后续如果同一时间有多条 session trajectory 任务并行，统一按下面 3 类任务口径汇报：

- `task 1`
  - raw archive 恢复到临时 PG
- `task 2`
  - completed archive 的 handoff
  - 即 `migrate -> import -> export`
- `task 3`
  - 直接从 PG 导出需求方格式

每次汇报至少带这 8 个字段：

- `任务号`
- `任务类型`
- `时间窗或游标`
- `当前阶段`
- `关键计数`
- `增长信号`
- `百分比`
- `下一步`

### 14.1 百分比估算规则

#### Archive Mainline

- `initialized`
  - `0%`
- `candidates_materialized`
  - 但尚无导出文件增长
  - `5% - 10%`
- `candidates_materialized`
  - 且 `session_trajectory_requests.jsonl.gz` 正在增长
  - `10% - 50%`
- `exported`
  - `50% - 60%`
- 已进入删除阶段
  - `60% - 90%`
- `vacuumed`
  - `90% - 99%`
- `completed`
  - `100%`

#### Restore / Handoff Import

- 只完成 `candidate_sessions.csv`
  - `0% - 5%`
- 已准备 `session_trajectory_sessions.csv`
  - `5% - 10%`
- `session_trajectory_requests.csv` 正在增长
  - `10% - 35%`
- PG 表计数开始上涨
  - `40% - 75%`
- import 完成但 export 尚未开始
  - `75% - 85%`
- manifest / handoff 记录完成
  - `100%`

#### Direct Export

若 stdout 已拿到：

- `matched <N> sessions for export`
- `exported <M>/<N> sessions`

则直接用 `M / N` 作为百分比。

### 14.2 增长信号的最小要求

不要只凭进程存活就说“还在推进”；每次至少给一条增长证据：

- 文件大小在短采样窗口内继续增长
- PG 行数继续增长
- `exported M/N sessions` 中的 `M` 继续上涨
- manifest 从无到有

如果进程存在，但连续两次短采样都没有增长，应改口径为：

- `进程仍在，但当前采样窗口内未观察到增长`

### 14.3 推荐汇报模板

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
- 下一步：导入完成后自动转 export

task 3
- 类型：live direct export
- 时间窗：start-time=... / end-time=...
- 阶段：exporting requirement-format files
- 关键计数：exported ... / ... sessions / ... files
- 增长信号：5 秒内 JSON 文件数从 ... 到 ...
- 百分比：...
- 下一步：生成 manifest
```

## 15. 以后怎么用

后续如果你要我按这套口径汇报，不需要重复解释背景，直接说下面任一种都可以：

- `按 session-trajectory 进度模板汇报`
- `汇报 task1/task2/task3 当前进度`
- `继续盯这 3 个任务，按固定模板报`
- `告诉我当前游标、阶段、百分比`

如果你只关心某一类，也可以直接说：

- `只报 handoff 进度`
- `只报 live export 进度`
- `只看 run_id=<...> 这一轮 archive`

## 16. 这次定位出的稳定性规则

### 16.1 macOS 后台方式

如果任务需要脱离当前 agent / 终端会话长期运行，macOS 上优先用：

- `launchctl submit`

不要默认依赖：

- 临时 PTY 会话
- 普通 shell 后台 `&`

原因：

- 这两类方式更容易和当前会话生命周期绑在一起
- 长任务结束后通常也缺少系统级状态可追踪

### 16.2 launchd 默认 PATH

`launchd` 默认 PATH 很短，通常只有：

- `/usr/bin:/bin:/usr/sbin:/sbin`

所以像 Homebrew/libpq 的：

- `/opt/homebrew/opt/libpq/bin/psql`

不会天然在 PATH 里。

当前导入脚本已经改成：

- 优先走 PATH
- 找不到时自动探测常见 Homebrew/libpq 路径

### 16.3 restore/import 必须串行

当前 `scripts/import_session_trajectory_archive.py` 已默认使用全局锁：

- `/tmp/cliproxy-session-trajectory-import.lock`

这条规则的目标是：

- 同一台机器上同时只跑一条大 restore/import
- 避免多个 `session_trajectory_requests.csv` 生成任务并发
- 降低内存、swap、磁盘写放大导致的异常中断概率

如果日志里看到：

- `[wait] import lock busy ...`

表示当前任务在排队，不是卡住。

### 16.4 live export 的迁移原则

对于已经在运行中的 live export：

- 不要中途迁移成托管任务

默认做法是：

- 当前正在跑的普通进程保持现状
- 下一轮从启动时就改用托管脚本

原因：

- 当前原生 `export_session_trajectories` 没有“中途接管同一批输出”的语义
- 中途迁移更容易引入重复导出、部分覆盖或状态解释歧义

因此从这次开始，推荐的长期路径是：

- 运行中的 live export 不接管
- 新的 live export 使用 `scripts/managed_live_export.py`
