# Session Trajectory Restore And Archive Plan

## Goal

1. Restore archived session trajectory rows for the window `2026-04-07T11:47:43Z` to `2026-04-07T15:29:42Z` into local PostgreSQL.
2. Export requirement-format session files from local PostgreSQL.
3. Start and monitor the next safe remote archive run that writes to `/Volumes/Storage`.

## Phases

| Phase | Status | Notes |
| --- | --- | --- |
| Audit current state and constraints | complete | Local PG reachable, prior remote archive verified complete |
| Implement local archive import tool | complete | Script and tests added |
| Restore local subset and run Go exporter | complete | Exported requirement-format files to /Volumes/Storage |
| Document end-to-end ops and cursors | complete | Added docs/superpowers/specs/2026-04-10-session-trajectory-ops-and-handoff-overview.md |
| Start next remote archive run | in_progress | Running as session-archive-20260409T122346Z |
| Verify outputs and report cursors | in_progress | Repo-local skills updated; handoff records persisted under /Volumes/Storage/CLIProxyAPI-session-archives/handoffs |

## Key Decisions

- Local requirement-format export will be produced from a local PG restore, not directly from raw archive files.
- Restore will target only sessions whose `last_activity_at` falls within `2026-04-07T11:47:43Z` to `2026-04-07T15:29:42Z`.
- Next remote archive run should execute from this machine so archive files land on `/Volumes/Storage`, avoiding extra pressure on server disk.
- Default path after a remote archive run completes is unattended handoff via `scripts/archive_export_handoff.py`, not a manual three-step operator flow.
