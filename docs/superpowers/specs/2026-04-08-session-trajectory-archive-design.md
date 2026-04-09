# Session Trajectory 归档清理设计

## 背景

线上 PostgreSQL 中 `public.session_trajectory_requests` 已膨胀到约 74 GB，且主要空间来自大体积 JSON/TOAST。现阶段先通过“整会话归档 + 安全清理”止血，后续再考虑分区。

## 目标

- 将冷会话数据导出到本地 `/Volumes/Storage/` 新目录
- 清理 PG 中已归档的冷会话数据，释放可复用空间
- 不影响仍在续写的活跃会话
- 生成可续跑的游标与状态文件，支持中断恢复

## 设计

- 归档粒度使用“整会话”，不按 request 单日硬切
- 判定条件使用 `last_activity_at < cutoff_at`
- 首轮默认 `cutoff_at = now() - 24h`
- 导出对象包含四张表：
  - `session_trajectory_sessions`
  - `session_trajectory_session_aliases`
  - `session_trajectory_requests`
  - `session_trajectory_request_exports`
- 本地归档格式使用压缩 CSV，便于后续再导入、排查和差异核对
- 运行状态保存在每次归档目录下的 `run-state.json`

## 安全性

- 导出前先物化候选 session 列表，确保本次运行的归档范围固定
- 导出后校验本地行数和数据库计数一致
- 删除顺序遵守外键依赖：
  - `request_exports`
  - `requests`
  - `session_aliases`
  - `sessions`
- 删除采用分批执行，降低长事务和 WAL 压力
- 中断后可通过 `--run-id` 读取状态继续执行同一批数据

## 游标

- 主游标：`run_id`
- 辅助游标：
  - `cutoff_at`
  - `candidate_sessions`
  - `min_last_activity_at`
  - `max_last_activity_at`
- 这些信息写入 `run-state.json` 和 `latest_completed.json`

## 后续

- 当前 `DELETE + VACUUM (ANALYZE)` 只能先回收为“库内可复用空间”
- 若要立刻归还磁盘给操作系统，后续需安排 `VACUUM FULL` 或 `pg_repack`
- 长期建议为 session 轨迹表做按时间分区和保留策略
