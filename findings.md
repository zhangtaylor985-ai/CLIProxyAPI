# Findings

- Previous archive run completed with `run_id=session-archive-20260408T152942Z` and `cutoff_at=2026-04-07T15:29:42Z`.
- Remote `public.session_trajectory_sessions` currently has no rows older than `2026-04-07T15:29:42Z`.
- Local PostgreSQL is reachable at `postgresql://postgres:root123@localhost:5432/cliproxy`.
- The Go exporter filters on `session_trajectory_sessions.last_activity_at`, not request timestamps.
- Existing repo skills cover export and archive operations, but not raw archive import back into PostgreSQL.
- The correct working repository for this project flow is `/Users/taylor/code/tools/CLIProxyAPI-ori`; scripts and repo-local skills must live there.
- The temp restore/export PostgreSQL currently runs as Docker container `cliproxy-export-pg` on `postgresql://postgres:root123@localhost:5433/cliproxy`, with data under `/Volumes/Storage/cliproxy-export-pg-data`.
- The completed requirement-format export for `2026-04-07T11:47:43Z` to `2026-04-07T15:29:42Z` is rooted at `/Volumes/Storage/session-trajectory-export-after-20260407T114743Z`, with manifest `/Volumes/Storage/session-trajectory-export-manifests/session-trajectory-export-20260409T130837Z.json`.
- Cursor persistence currently exists in `/Volumes/Storage/CLIProxyAPI-session-archives/handoffs/active_archive_cursor.json`, `/Volumes/Storage/CLIProxyAPI-session-archives/handoffs/latest_handoff.json`, and per-run files under the same directory.
- The active remote archive run is `session-archive-20260409T122346Z` with `cutoff_at=2026-04-08T12:23:46Z`; raw archive export completed and the run later advanced to `phase=request_exports_deleted`, with `request_exports=3354/3354` deleted and `requests=11500/22041` deleted at the latest check.
- The default operator path after archive completion is unattended handoff via `scripts/archive_export_handoff.py`, rather than manually running migrate/import/export as separate steps.
