# Session Trajectory Restore And Archive Plan

## Goal

1. Restore archived session trajectory rows for the window `2026-04-07T11:47:43Z` to `2026-04-07T15:29:42Z` into local PostgreSQL.
2. Export requirement-format session files from local PostgreSQL.
3. Start and monitor the next safe remote archive run that writes to `/Volumes/Storage`.

## Phases

| Phase | Status | Notes |
| --- | --- | --- |
| Audit current state and constraints | complete | Local PG reachable, prior remote archive verified complete |
| Implement local archive import tool | in_progress | Needs tests first |
| Restore local subset and run Go exporter | pending | After import tool verification |
| Start next remote archive run | pending | Independent from local restore/export |
| Verify outputs and report cursors | pending | Include resume commands and artifact paths |

## Key Decisions

- Local requirement-format export will be produced from a local PG restore, not directly from raw archive files.
- Restore will target only sessions whose `last_activity_at` falls within `2026-04-07T11:47:43Z` to `2026-04-07T15:29:42Z`.
- Next remote archive run should execute from this machine so archive files land on `/Volumes/Storage`, avoiding extra pressure on server disk.
