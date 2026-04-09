# 2026-04-05 Session Trajectory 异步批量导出与 UI 进度反馈待办

## 目标

- 管理端“批量导出筛选结果”改为按时间范围导出。
- 用户先看到目标时间范围内的数据量评估，再点击确认执行。
- 导出过程异步执行，UI 能持续看到实时进度。
- 导出完成后，UI 显示最终导出路径，不要求浏览器直接下载 JSON。

## 建议交互

1. 筛选区主入口改成：
   - `start_time`
   - `end_time`
2. 用户点击“估算导出量”后，先返回：
   - `session_count`
   - `request_count`
   - `estimated_file_count`
   - 可选 `latest_activity_at / earliest_activity_at`
3. 用户点击“确认开始导出”后，创建异步任务。
4. UI 默认每 `10s` 轮询一次任务状态。
5. 任务结束后展示：
   - `status`
   - `exported_sessions`
   - `exported_files`
   - `manifest_path`
   - `export_root`
   - 若只导出一个目录，也展示 `export_dir`

## 为什么先用轮询，不先上 WS

- 当前管理端已经有稳定的 axios / 轮询式加载模式，接入成本最低。
- 导出任务是低频后台任务，不是高频 token 流事件。
- `10s` 轮询足够看进度，也能避免额外维护 websocket 生命周期、断线重连、权限复用。
- 真要做到秒级刷新或多客户端同时观察，再考虑补 WS/SSE。

## 后端建议

- 保留现有同步导出接口不动，新增异步任务接口，避免影响当前已上线能力。
- 新增接口建议：
  - `POST /v0/management/session-trajectories/export-jobs/estimate`
  - `POST /v0/management/session-trajectories/export-jobs`
  - `GET /v0/management/session-trajectories/export-jobs/:jobId`
  - `GET /v0/management/session-trajectories/export-jobs`
- estimate 只做统计，不真正落文件。
- create job 后立即返回 `job_id`。
- job status 至少包含：
  - `pending`
  - `running`
  - `success`
  - `error`
  - `cancelled` 可选

## 进度字段建议

- `total_sessions`
- `completed_sessions`
- `failed_sessions`
- `total_requests`
- `completed_files`
- `started_at`
- `updated_at`
- `finished_at`
- `current_session_id`
- `current_export_dir`
- `error_message`

## 存储建议

- 不建议只放内存。
- 既然是生产异步任务，建议加 PG 表持久化任务元数据和进度。
- 若落 PG：
  - 需要 runtime 初始化
  - 需要显式 migration 脚本到 `scripts/`
  - 需要考虑任务重启恢复或至少可见失败态

## 实现顺序建议

1. 先做 estimate 接口与 UI 时间范围确认。
2. 再做异步 job 表与 job runner。
3. 再接 UI 轮询进度。
4. 最后再决定是否补 WS/SSE。

## 风险边界

- 大范围导出会持续写大量 JSON 文件，必须限制并发，避免把磁盘 IO 打满。
- 导出根目录当前生产上是 `session-data/session-exports`，目录权限要和运行用户保持一致。
- 若后续支持“浏览器直接下载压缩包”，需要再单独设计压缩、清理和过期策略。
