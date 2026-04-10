#!/usr/bin/env python3
"""Wait for an archive run to complete, then restore it into a temp PG and export requirement-format files."""

from __future__ import annotations

import argparse
import json
import os
import pathlib
import signal
import subprocess
import time
import traceback
from datetime import datetime, timezone
from typing import Any


def utc_now() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def run_command(cmd: list[str], *, cwd: pathlib.Path) -> None:
    proc = subprocess.run(cmd, check=False, cwd=str(cwd))
    if proc.returncode != 0:
        raise SystemExit(f"command failed ({proc.returncode}): {' '.join(cmd)}")


def load_json(path: pathlib.Path) -> dict[str, Any]:
    with path.open("r", encoding="utf-8") as handle:
        return json.load(handle)


def is_completed_run_state(run_state: dict[str, Any]) -> bool:
    return run_state.get("phase") == "completed"


def resolve_run_state_path(*, archive_root: pathlib.Path, run_id: str) -> pathlib.Path:
    path = archive_root / "runs" / run_id / "run-state.json"
    if not path.exists():
        raise SystemExit(f"run-state.json not found: {path}")
    return path


def wait_for_completed_run_state(*, run_state_path: pathlib.Path, poll_seconds: int) -> dict[str, Any]:
    while True:
        run_state = load_json(run_state_path)
        if is_completed_run_state(run_state):
            return run_state
        print(
            f"[wait] run_id={run_state.get('run_id')} phase={run_state.get('phase')} updated_at={run_state.get('updated_at', '')}",
            flush=True,
        )
        time.sleep(poll_seconds)


def find_latest_manifest(manifest_dir: pathlib.Path, *, started_at: float) -> pathlib.Path:
    candidates = [p for p in manifest_dir.glob("session-trajectory-export-*.json") if p.stat().st_mtime >= started_at]
    if not candidates:
        candidates = list(manifest_dir.glob("session-trajectory-export-*.json"))
    if not candidates:
        raise SystemExit(f"no manifest found in {manifest_dir}")
    return max(candidates, key=lambda p: p.stat().st_mtime)


def build_handoff_record(*, run_state: dict[str, Any], manifest: dict[str, Any], target_pg_dsn: str) -> dict[str, Any]:
    return {
        "recorded_at": utc_now(),
        "source_cursor": {
            "archive_run_id": run_state.get("run_id"),
            "archive_cutoff_at": run_state.get("cutoff_at"),
            "archive_output_dir": run_state.get("output_dir"),
            "archive_completion_status": run_state.get("completion_status", "unknown"),
            "archive_counts": run_state.get("counts", {}),
            "archive_candidate_sessions": run_state.get("cursor", {}).get("candidate_sessions"),
            "archive_candidate_file": run_state.get("cursor", {}).get("candidate_file"),
            "archive_vacuum": run_state.get("vacuum", {}),
            "archive_warnings": run_state.get("warnings", []),
        },
        "export_cursor": {
            "manifest_path": manifest.get("manifest_path"),
            "export_root": manifest.get("export_root"),
            "filters": manifest.get("filters", {}),
            "exported_sessions": manifest.get("exported_sessions"),
            "exported_files": manifest.get("exported_files"),
        },
        "target_pg": {
            "dsn": target_pg_dsn,
        },
    }


def write_handoff_record(*, handoff_dir: pathlib.Path, record: dict[str, Any]) -> tuple[pathlib.Path, pathlib.Path]:
    handoff_dir.mkdir(parents=True, exist_ok=True)
    run_id = record["source_cursor"]["archive_run_id"]
    latest_path = handoff_dir / "latest_handoff.json"
    per_run_path = handoff_dir / f"{run_id}.json"
    payload = json.dumps(record, indent=2, sort_keys=True) + "\n"
    latest_path.write_text(payload, encoding="utf-8")
    per_run_path.write_text(payload, encoding="utf-8")
    return latest_path, per_run_path


def completed_handoff_record_path(*, handoff_dir: pathlib.Path, run_id: str) -> pathlib.Path:
    return handoff_dir / f"{run_id}.json"


def default_state_path(*, handoff_dir: pathlib.Path, run_id: str) -> pathlib.Path:
    return handoff_dir / f"{run_id}.state.json"


def resolve_completed_handoff(
    *,
    handoff_dir: pathlib.Path,
    run_id: str,
) -> dict[str, Any] | None:
    record_path = completed_handoff_record_path(handoff_dir=handoff_dir, run_id=run_id)
    if not record_path.exists():
        return None
    record = load_json(record_path)
    if record.get("source_cursor", {}).get("archive_run_id") != run_id:
        return None
    manifest_path_raw = record.get("export_cursor", {}).get("manifest_path")
    if not manifest_path_raw:
        return None
    manifest_path = pathlib.Path(str(manifest_path_raw)).expanduser()
    if not manifest_path.exists():
        return None
    return {
        "record": record,
        "record_path": record_path,
        "manifest_path": manifest_path.resolve(),
    }


def write_state(path: pathlib.Path, state: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    payload = json.dumps(state, indent=2, sort_keys=True) + "\n"
    path.write_text(payload, encoding="utf-8")


def update_state(state: dict[str, Any], state_path: pathlib.Path, **changes: Any) -> None:
    state.update(changes)
    state["updated_at"] = utc_now()
    write_state(state_path, state)


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--archive-root", type=pathlib.Path, default=pathlib.Path("/Volumes/Storage/CLIProxyAPI-session-archives"))
    parser.add_argument("--run-id", required=True, help="completed archive run id")
    parser.add_argument("--pg-dsn", required=True, help="temporary PostgreSQL DSN used for restore/export")
    parser.add_argument("--export-root", type=pathlib.Path, required=True, help="requirement-format export root")
    parser.add_argument("--manifest-dir", type=pathlib.Path, required=True, help="manifest output directory")
    parser.add_argument("--start-time", default="", help="optional RFC3339 lower bound on session last_activity_at")
    parser.add_argument("--end-time", default="", help="optional RFC3339 upper bound on session last_activity_at")
    parser.add_argument("--poll-seconds", type=int, default=60, help="poll interval while waiting for archive completion")
    parser.add_argument("--wait-completed", action="store_true", help="wait until run-state phase becomes completed")
    parser.add_argument("--skip-request-exports", action="store_true", help="skip restoring request_exports into temp PG")
    parser.add_argument("--handoff-dir", type=pathlib.Path, default=pathlib.Path("/Volumes/Storage/CLIProxyAPI-session-archives/handoffs"))
    parser.add_argument("--state-path", type=pathlib.Path, help="explicit handoff state json path")
    parser.add_argument("--import-work-dir", type=pathlib.Path, help="fixed work dir used by import_session_trajectory_archive.py")
    parser.add_argument("--keep-import-work-dir", action="store_true", help="preserve import work dir for inspection")
    parser.add_argument("--force-rerun", action="store_true", help="rerun import/export even if this run_id already has a completed handoff record")
    return parser.parse_args(argv)


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    repo_root = pathlib.Path(__file__).resolve().parents[1]
    archive_root = args.archive_root.expanduser().resolve()
    export_root = args.export_root.expanduser().resolve()
    manifest_dir = args.manifest_dir.expanduser().resolve()
    handoff_dir = args.handoff_dir.expanduser().resolve()
    run_state_path = resolve_run_state_path(archive_root=archive_root, run_id=args.run_id)
    state_path = (args.state_path.expanduser().resolve() if args.state_path else default_state_path(handoff_dir=handoff_dir, run_id=args.run_id))
    existing_handoff = None if args.force_rerun else resolve_completed_handoff(handoff_dir=handoff_dir, run_id=args.run_id)
    if existing_handoff is not None:
        existing_record = existing_handoff["record"]
        state = {
            "run_id": args.run_id,
            "status": "completed",
            "current_stage": "already_completed",
            "started_at": utc_now(),
            "updated_at": utc_now(),
            "archive_root": str(archive_root),
            "export_root": str(export_root),
            "manifest_dir": str(manifest_dir),
            "target_pg_dsn": args.pg_dsn,
            "manifest_path": str(existing_handoff["manifest_path"]),
            "handoff_record_path": str(existing_handoff["record_path"]),
            "message": "existing completed handoff record found; skipping rerun",
            "archive_cutoff_at": existing_record.get("source_cursor", {}).get("archive_cutoff_at"),
            "archive_counts": existing_record.get("source_cursor", {}).get("archive_counts", {}),
        }
        write_state(state_path, state)
        print(
            "[skip] completed handoff already exists "
            f"run_id={args.run_id} manifest={existing_handoff['manifest_path']}",
            flush=True,
        )
        return 0
    state: dict[str, Any] = {
        "run_id": args.run_id,
        "status": "running",
        "current_stage": "initializing",
        "started_at": utc_now(),
        "updated_at": utc_now(),
        "archive_root": str(archive_root),
        "export_root": str(export_root),
        "manifest_dir": str(manifest_dir),
        "target_pg_dsn": args.pg_dsn,
    }
    write_state(state_path, state)

    def on_signal(signum: int, _frame: Any) -> None:
        update_state(
            state,
            state_path,
            status="interrupted",
            signal=signal.Signals(signum).name,
            error=f"received signal {signum}",
        )
        raise SystemExit(f"received signal {signum}")

    signal.signal(signal.SIGTERM, on_signal)
    signal.signal(signal.SIGINT, on_signal)
    if hasattr(signal, "SIGHUP"):
        signal.signal(signal.SIGHUP, on_signal)

    try:
        if args.wait_completed:
            update_state(state, state_path, current_stage="waiting_archive_completed")
            run_state = wait_for_completed_run_state(run_state_path=run_state_path, poll_seconds=max(5, args.poll_seconds))
        else:
            run_state = load_json(run_state_path)
            if not is_completed_run_state(run_state):
                raise SystemExit(f"run {args.run_id} is not completed yet: phase={run_state.get('phase')}")
        update_state(
            state,
            state_path,
            current_stage="archive_completed",
            archive_cutoff_at=run_state.get("cutoff_at"),
            archive_counts=run_state.get("counts", {}),
        )

        migrate_cmd = [
            "go",
            "run",
            "./scripts/migrate_session_trajectory_pg",
            "--pg-dsn",
            args.pg_dsn,
        ]
        update_state(state, state_path, current_stage="migrate_running", current_command=migrate_cmd)
        print(f"[run] {' '.join(migrate_cmd)}", flush=True)
        run_command(migrate_cmd, cwd=repo_root)
        update_state(state, state_path, current_stage="migrate_completed")

        import_cmd = [
            "python3",
            "scripts/import_session_trajectory_archive.py",
            "--run-id",
            args.run_id,
            "--archive-root",
            str(archive_root),
            "--pg-dsn",
            args.pg_dsn,
            "--truncate-target",
            "--require-storage-prefix",
            "/Volumes/Storage",
        ]
        if args.skip_request_exports:
            import_cmd.append("--skip-request-exports")
        if args.start_time:
            import_cmd.extend(["--start-time", args.start_time])
        if args.end_time:
            import_cmd.extend(["--end-time", args.end_time])
        if args.import_work_dir:
            import_cmd.extend(["--work-dir", str(args.import_work_dir.expanduser().resolve())])
        if args.keep_import_work_dir:
            import_cmd.append("--keep-work-dir")
        update_state(state, state_path, current_stage="import_running", current_command=import_cmd)
        print(f"[run] {' '.join(import_cmd)}", flush=True)
        run_command(import_cmd, cwd=repo_root)
        update_state(state, state_path, current_stage="import_completed")

        export_cmd = [
            "go",
            "run",
            "./scripts/export_session_trajectories",
            "--pg-dsn",
            args.pg_dsn,
            "--export-root",
            str(export_root),
            "--manifest-dir",
            str(manifest_dir),
        ]
        if args.start_time:
            export_cmd.extend(["--start-time", args.start_time])
        if args.end_time:
            export_cmd.extend(["--end-time", args.end_time])
        started_at = time.time()
        update_state(state, state_path, current_stage="export_running", current_command=export_cmd)
        print(f"[run] {' '.join(export_cmd)}", flush=True)
        run_command(export_cmd, cwd=repo_root)

        manifest_path = find_latest_manifest(manifest_dir, started_at=started_at)
        manifest = load_json(manifest_path)
        record = build_handoff_record(run_state=run_state, manifest=manifest, target_pg_dsn=args.pg_dsn)
        latest_path, per_run_path = write_handoff_record(handoff_dir=handoff_dir, record=record)
        update_state(
            state,
            state_path,
            status="completed",
            current_stage="completed",
            manifest_path=str(manifest_path),
            handoff_latest_path=str(latest_path),
            handoff_record_path=str(per_run_path),
        )
        print(f"[done] manifest={manifest_path}", flush=True)
        print(f"[done] handoff_latest={latest_path}", flush=True)
        print(f"[done] handoff_record={per_run_path}", flush=True)
        return 0
    except BaseException as exc:
        status = state.get("status", "running")
        if status not in {"interrupted", "completed"}:
            status = "failed"
        update_state(
            state,
            state_path,
            status=status,
            current_stage=state.get("current_stage", "unknown"),
            error_type=type(exc).__name__,
            error=str(exc),
            traceback=traceback.format_exc(),
        )
        raise


if __name__ == "__main__":
    raise SystemExit(main(os.sys.argv[1:]))
