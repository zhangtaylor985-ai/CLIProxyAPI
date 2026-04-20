# Session Trajectory 本地落盘索引

时间窗：

- `2026-04-09T18:05:22Z` 到 `2026-04-11T12:47:29Z`

说明：

- 这段时间的数据不是落在一个单独目录里，而是分批落在多个 `export_root`
- 每一批都可以通过对应的 `record_file` 与 `manifest_path` 反查
- 中间看到的时间缝隙，不默认表示丢数据；这里按一批 session 的 `last_activity_at` 集合处理，不是按秒连续分区

## 1. Task 3 收尾点

这批对应你之前确认过的 Task 3：

- 时间窗：`2026-04-08T17:27:44Z` 到 `2026-04-09T18:05:22Z`
- 导出目录：`/Users/taylor/session-trajectory-export-live-20260408T172744Z-to-2026-04-09T180522Z`
- manifest：`/Users/taylor/session-trajectory-export-manifests-live/session-trajectory-export-20260411T040118Z.json`
- 记录文件：`/Users/taylor/CLIProxyAPI-session-archives-local/handoffs/task3-live-export-delete-20260408T172744Z-to-20260409T180522Z.json`

Task 3 删除验证后的远端剩余最早时间：

- `remote_min_last_activity_after_delete = 2026-04-09T19:24:09Z`

这意味着：

- `2026-04-09T18:05:23Z` 到 `2026-04-09T19:24:08Z` 这段，在当时远端没有剩余可处理 session

## 2. 旧模式目录规则

旧模式的需求格式文件都在：

- `/Users/taylor/session-trajectory-export-session-archive-session-archive-<run_id>`

旧模式的记录文件都在：

- `/Users/taylor/CLIProxyAPI-session-archives-local/handoffs/session-archive-<run_id>.json`

旧模式的 manifest 都在：

- `/Users/taylor/session-trajectory-export-manifests/session-trajectory-export-*.json`

## 3. 新模式目录规则

新模式那几轮的需求格式文件都在：

- `/Users/taylor/session-trajectory-export-live-delete/session-live-export-delete-<run_id>`

新模式记录文件都在：

- `/Users/taylor/CLIProxyAPI-session-live-export-loop/records/session-live-export-delete-<run_id>.json`

新模式 manifest 都在：

- `/Users/taylor/session-trajectory-export-manifests-direct/session-trajectory-export-*.json`

## 4. 这段时间的主要接力点

### 4.1 第一大段旧模式

- 时间窗：`2026-04-09T19:24:09Z` 到 `2026-04-10T09:49:02Z`
- run_id：`session-archive-20260411T094922Z`
- export_root：`/Users/taylor/session-trajectory-export-session-archive-session-archive-20260411T094922Z`
- manifest：`/Users/taylor/session-trajectory-export-manifests/session-trajectory-export-20260411T162227Z.json`
- record_file：`/Users/taylor/CLIProxyAPI-session-archives-local/handoffs/session-archive-20260411T094922Z.json`

### 4.2 中间大量小批次旧模式

从下面开始直到 `2026-04-11T11:42:51Z`，都是旧模式小批次接力：

- 记录目录：`/Users/taylor/CLIProxyAPI-session-archives-local/handoffs`
- 文件模式：`session-archive-*.json`
- 对应导出目录模式：`/Users/taylor/session-trajectory-export-session-archive-session-archive-*`

这段里你只要：

1. 先打开某个 `session-archive-*.json`
2. 看里面的 `source_cursor.archive_min_last_activity_at` 和 `source_cursor.archive_max_last_activity_at`
3. 再看同文件里的 `export_cursor.export_root` 与 `export_cursor.manifest_path`

就能定位那一小段数据的本地目录。

这段最后一轮旧模式是：

- 时间窗：`2026-04-11T11:42:32Z` 到 `2026-04-11T11:42:51Z`
- run_id：`session-archive-20260412T114416Z`
- export_root：`/Users/taylor/session-trajectory-export-session-archive-session-archive-20260412T114416Z`
- manifest：`/Users/taylor/session-trajectory-export-manifests/session-trajectory-export-20260412T114525Z.json`
- record_file：`/Users/taylor/CLIProxyAPI-session-archives-local/handoffs/session-archive-20260412T114416Z.json`

### 4.3 中间新模式接力段

从 `2026-04-11T11:44:50Z` 到 `2026-04-11T12:13:16Z`，是新模式接力：

- 导出根目录：`/Users/taylor/session-trajectory-export-live-delete`
- 记录目录：`/Users/taylor/CLIProxyAPI-session-live-export-loop/records`

这段最后一轮是：

- 时间窗：`2026-04-11T12:13:16Z` 到 `2026-04-11T12:13:16Z`
- run_id：`session-live-export-delete-20260412T121323Z`
- export_root：`/Users/taylor/session-trajectory-export-live-delete/session-live-export-delete-20260412T121323Z`
- manifest：`/Users/taylor/session-trajectory-export-manifests-direct/session-trajectory-export-20260412T121359Z.json`
- record_file：`/Users/taylor/CLIProxyAPI-session-live-export-loop/records/session-live-export-delete-20260412T121323Z.json`

### 4.4 当前已切回旧模式后的继续接力

从 `2026-04-11T12:15:19Z` 到 `2026-04-11T12:47:29Z`，已经重新回到旧模式：

- 第一轮：
  - 时间窗：`2026-04-11T12:15:19Z` 到 `2026-04-11T12:23:42Z`
  - run_id：`session-archive-20260412T122344Z`
  - export_root：`/Users/taylor/session-trajectory-export-session-archive-session-archive-20260412T122344Z`
  - manifest：`/Users/taylor/session-trajectory-export-manifests/session-trajectory-export-20260412T123342Z.json`
  - record_file：`/Users/taylor/CLIProxyAPI-session-archives-local/handoffs/session-archive-20260412T122344Z.json`

- 第二轮：
  - 时间窗：`2026-04-11T12:23:54Z` 到 `2026-04-11T12:33:35Z`
  - run_id：`session-archive-20260412T123409Z`
  - export_root：`/Users/taylor/session-trajectory-export-session-archive-session-archive-20260412T123409Z`
  - manifest：`/Users/taylor/session-trajectory-export-manifests/session-trajectory-export-20260412T123836Z.json`
  - record_file：`/Users/taylor/CLIProxyAPI-session-archives-local/handoffs/session-archive-20260412T123409Z.json`

- 第三轮：
  - 时间窗：`2026-04-11T12:34:46Z` 到 `2026-04-11T12:37:06Z`
  - run_id：`session-archive-20260412T123904Z`
  - export_root：`/Users/taylor/session-trajectory-export-session-archive-session-archive-20260412T123904Z`
  - manifest：`/Users/taylor/session-trajectory-export-manifests/session-trajectory-export-20260412T124142Z.json`
  - record_file：`/Users/taylor/CLIProxyAPI-session-archives-local/handoffs/session-archive-20260412T123904Z.json`

- 第四轮：
  - 时间窗：`2026-04-11T12:40:46Z` 到 `2026-04-11T12:40:46Z`
  - run_id：`session-archive-20260412T124210Z`
  - export_root：`/Users/taylor/session-trajectory-export-session-archive-session-archive-20260412T124210Z`
  - manifest：`/Users/taylor/session-trajectory-export-manifests/session-trajectory-export-20260412T124337Z.json`
  - record_file：`/Users/taylor/CLIProxyAPI-session-archives-local/handoffs/session-archive-20260412T124210Z.json`

- 第五轮：
  - 时间窗：`2026-04-11T12:42:37Z` 到 `2026-04-11T12:42:37Z`
  - run_id：`session-archive-20260412T124405Z`
  - export_root：`/Users/taylor/session-trajectory-export-session-archive-session-archive-20260412T124405Z`
  - manifest：`/Users/taylor/session-trajectory-export-manifests/session-trajectory-export-20260412T124523Z.json`
  - record_file：`/Users/taylor/CLIProxyAPI-session-archives-local/handoffs/session-archive-20260412T124405Z.json`

- 第六轮：
  - 时间窗：`2026-04-11T12:44:59Z` 到 `2026-04-11T12:44:59Z`
  - run_id：`session-archive-20260412T124551Z`
  - export_root：`/Users/taylor/session-trajectory-export-session-archive-session-archive-20260412T124551Z`
  - manifest：`/Users/taylor/session-trajectory-export-manifests/session-trajectory-export-20260412T124713Z.json`
  - record_file：`/Users/taylor/CLIProxyAPI-session-archives-local/handoffs/session-archive-20260412T124551Z.json`

- 第七轮：
  - 时间窗：`2026-04-11T12:46:20Z` 到 `2026-04-11T12:47:29Z`
  - run_id：`session-archive-20260412T124741Z`
  - export_root：`/Users/taylor/session-trajectory-export-session-archive-session-archive-20260412T124741Z`
  - manifest：`/Users/taylor/session-trajectory-export-manifests/session-trajectory-export-20260412T124906Z.json`
  - record_file：`/Users/taylor/CLIProxyAPI-session-archives-local/handoffs/session-archive-20260412T124741Z.json`

## 5. 你后续最省事的找法

如果你想找某个时间点附近的数据，按这个顺序找：

1. 先看这份索引，判断大致属于 Task 3、旧模式还是新模式
2. 打开对应的 `record_file`
3. 直接取里面的 `export_root`
4. 到 `export_root` 下找具体 session 目录和 JSON 文件
5. 需要总览时，再打开对应 `manifest_path`

## 6. 当前最重要的三个总入口

- 旧模式记录目录：`/Users/taylor/CLIProxyAPI-session-archives-local/handoffs`
- 新模式记录目录：`/Users/taylor/CLIProxyAPI-session-live-export-loop/records`
- 旧模式当前总状态：`/Users/taylor/CLIProxyAPI-session-archives-local/handoffs/archive_handoff_loop.summary.json`
