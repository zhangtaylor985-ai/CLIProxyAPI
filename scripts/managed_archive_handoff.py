#!/usr/bin/env python3
"""Run archive first, then automatically hand off to temp PG export."""

from __future__ import annotations

import argparse
import json
import os
import pathlib
import re
import signal
import subprocess
import sys
from datetime import datetime, timezone
from typing import Any


ARCHIVE_START_RE = re.compile(
    r"^\[start\] run_id=(?P<run_id>\S+) schema=(?P<schema>\S+) cutoff_at=(?P<cutoff_at>\S+) output_dir=(?P<output_dir>\S+)$"
)

EXTRA_PATHS = [
    "/opt/homebrew/bin",
    "/opt/homebrew/opt/libpq/bin",
    "/usr/local/bin",
    "/usr/bin",
    "/bin",
    "/usr/sbin",
    "/sbin",
]


def utc_now() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def write_state(path: pathlib.Path, state: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(state, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def update_state(state: dict[str, Any], path: pathlib.Path, **changes: Any) -> None:
    state.update(changes)
    state["updated_at"] = utc_now()
    write_state(path, state)


def build_env(*, archive_pg_dsn: str, archive_dsn_env: str) -> dict[str, str]:
    env = os.environ.copy()
    path_parts = [part for part in env.get("PATH", "").split(":") if part]
    for part in reversed(EXTRA_PATHS):
        if part not in path_parts:
            path_parts.insert(0, part)
    env["PATH"] = ":".join(path_parts)
    env[archive_dsn_env] = archive_pg_dsn
    return env


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--archive-pg-dsn", required=True, help="source PostgreSQL DSN for archive")
    parser.add_argument("--archive-dsn-env", default="SESSION_TRAJECTORY_PG_DSN")
    parser.add_argument("--run-id", default="", help="resume an existing archive run instead of creating a new one")
    parser.add_argument("--archive-root", type=pathlib.Path, default=pathlib.Path.home() / "CLIProxyAPI-session-archives-local")
    parser.add_argument("--handoff-dir", type=pathlib.Path, default=pathlib.Path.home() / "CLIProxyAPI-session-archives-local" / "handoffs")
    parser.add_argument("--manifest-dir", type=pathlib.Path, default=pathlib.Path.home() / "session-trajectory-export-manifests")
    parser.add_argument("--export-root", type=pathlib.Path, help="optional fixed requirement-format export root")
    parser.add_argument("--export-root-prefix", default="session-trajectory-export")
    parser.add_argument("--target-pg-dsn", required=True, help="temporary PostgreSQL DSN used for restore/export")
    parser.add_argument("--state-path", type=pathlib.Path, default=pathlib.Path.home() / "CLIProxyAPI-session-archives-local" / "handoffs" / "active_archive_chain.json")
    parser.add_argument("--schema", default="public")
    parser.add_argument("--inactive-hours", type=int, default=24)
    parser.add_argument("--batch-size", type=int, default=500)
    parser.add_argument("--skip-vacuum", action="store_true")
    parser.add_argument("--vacuum-timeout-seconds", type=int, default=600)
    parser.add_argument("--start-time", default="")
    parser.add_argument("--end-time", default="")
    parser.add_argument("--skip-request-exports", action="store_true")
    parser.add_argument("--import-work-dir", type=pathlib.Path)
    parser.add_argument("--keep-import-work-dir", action="store_true")
    parser.add_argument("--require-storage-prefix", default="", help="optional storage prefix guard forwarded to archive_export_handoff.py")
    return parser.parse_args(argv)


def build_archive_cmd(args: argparse.Namespace) -> list[str]:
    cmd = [
        sys.executable,
        "scripts/session_trajectory_archive.py",
        "--output-root",
        str(args.archive_root),
        "--dsn-env",
        args.archive_dsn_env,
    ]
    if args.run_id:
        cmd.extend(["--run-id", args.run_id])
    else:
        cmd.extend(
            [
                "--schema",
                args.schema,
                "--inactive-hours",
                str(args.inactive_hours),
                "--batch-size",
                str(args.batch_size),
                "--vacuum-timeout-seconds",
                str(args.vacuum_timeout_seconds),
            ]
        )
    if args.skip_vacuum:
        cmd.append("--skip-vacuum")
    return cmd


def build_handoff_cmd(args: argparse.Namespace, *, run_id: str, export_root: pathlib.Path) -> list[str]:
    cmd = [
        sys.executable,
        "scripts/archive_export_handoff.py",
        "--archive-root",
        str(args.archive_root),
        "--run-id",
        run_id,
        "--pg-dsn",
        args.target_pg_dsn,
        "--export-root",
        str(export_root),
        "--manifest-dir",
        str(args.manifest_dir),
        "--handoff-dir",
        str(args.handoff_dir),
        "--wait-completed",
    ]
    if args.start_time:
        cmd.extend(["--start-time", args.start_time])
    if args.end_time:
        cmd.extend(["--end-time", args.end_time])
    if args.skip_request_exports:
        cmd.append("--skip-request-exports")
    if args.import_work_dir:
        cmd.extend(["--import-work-dir", str(args.import_work_dir)])
    if args.keep_import_work_dir:
        cmd.append("--keep-import-work-dir")
    if args.require_storage_prefix:
        cmd.extend(["--require-storage-prefix", args.require_storage_prefix])
    return cmd


def load_json(path: pathlib.Path) -> dict[str, Any]:
    return json.loads(path.read_text(encoding="utf-8"))


def run_streaming(
    *,
    cmd: list[str],
    cwd: pathlib.Path,
    env: dict[str, str],
    on_line: Any = None,
) -> int:
    proc = subprocess.Popen(
        cmd,
        cwd=str(cwd),
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        bufsize=1,
    )
    assert proc.stdout is not None
    try:
        for line in proc.stdout:
            print(line, end="", flush=True)
            if on_line is not None:
                on_line(line.rstrip("\n"))
    finally:
        proc.stdout.close()
    return proc.wait()


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    repo_root = pathlib.Path(__file__).resolve().parents[1]
    archive_root = args.archive_root.expanduser().resolve()
    handoff_dir = args.handoff_dir.expanduser().resolve()
    manifest_dir = args.manifest_dir.expanduser().resolve()
    state_path = args.state_path.expanduser().resolve()
    env = build_env(archive_pg_dsn=args.archive_pg_dsn, archive_dsn_env=args.archive_dsn_env)

    state: dict[str, Any] = {
        "status": "running",
        "current_stage": "archive_running",
        "started_at": utc_now(),
        "updated_at": utc_now(),
        "archive_root": str(archive_root),
        "handoff_dir": str(handoff_dir),
        "manifest_dir": str(manifest_dir),
        "target_pg_dsn": args.target_pg_dsn,
        "inactive_hours": args.inactive_hours,
        "requested_run_id": args.run_id or None,
    }
    write_state(state_path, state)

    current_proc: dict[str, subprocess.Popen[str] | None] = {"proc": None}

    def on_signal(signum: int, _frame: Any) -> None:
        proc = current_proc.get("proc")
        if proc is not None and proc.poll() is None:
            proc.terminate()
        update_state(
            state,
            state_path,
            status="interrupted",
            current_stage="interrupted",
            signal=signal.Signals(signum).name,
            error=f"received signal {signum}",
        )
        raise SystemExit(f"received signal {signum}")

    signal.signal(signal.SIGTERM, on_signal)
    signal.signal(signal.SIGINT, on_signal)
    if hasattr(signal, "SIGHUP"):
        signal.signal(signal.SIGHUP, on_signal)

    archive_cmd = build_archive_cmd(args)
    parsed: dict[str, str] = {}

    def on_archive_line(line: str) -> None:
        if parsed:
            return
        match = ARCHIVE_START_RE.match(line.strip())
        if not match:
            return
        parsed.update(match.groupdict())
        update_state(
            state,
            state_path,
            archive_run_id=parsed["run_id"],
            archive_cutoff_at=parsed["cutoff_at"],
            archive_output_dir=parsed["output_dir"],
        )

    archive_proc = subprocess.Popen(
        archive_cmd,
        cwd=str(repo_root),
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        bufsize=1,
    )
    current_proc["proc"] = archive_proc
    assert archive_proc.stdout is not None
    try:
        for line in archive_proc.stdout:
            print(line, end="", flush=True)
            on_archive_line(line.rstrip("\n"))
    finally:
        archive_proc.stdout.close()
    archive_rc = archive_proc.wait()
    current_proc["proc"] = None
    if archive_rc != 0:
        update_state(
            state,
            state_path,
            status="failed",
            current_stage="archive_failed",
            archive_returncode=archive_rc,
            error=f"archive command failed ({archive_rc})",
        )
        return archive_rc

    run_id = parsed.get("run_id")
    if not run_id:
        update_state(
            state,
            state_path,
            status="failed",
            current_stage="archive_failed",
            error="archive completed but run_id was not parsed from output",
        )
        return 1

    run_state_path = archive_root / "runs" / run_id / "run-state.json"
    run_state = load_json(run_state_path)
    update_state(
        state,
        state_path,
        current_stage="archive_completed",
        archive_phase=run_state.get("phase"),
        archive_counts=run_state.get("counts", {}),
        archive_cursor=run_state.get("cursor", {}),
        archive_completion_status=run_state.get("completion_status"),
    )
    if run_state.get("phase") != "completed":
        update_state(
            state,
            state_path,
            status="failed",
            current_stage="archive_failed",
            error=f"archive run did not complete cleanly: phase={run_state.get('phase')}",
        )
        return 1

    candidate_sessions = int(run_state.get("cursor", {}).get("candidate_sessions") or 0)
    if candidate_sessions == 0:
        update_state(
            state,
            state_path,
            status="completed",
            current_stage="completed_noop",
            message="archive found no eligible sessions; handoff skipped",
        )
        return 0

    export_root = (
        args.export_root.expanduser().resolve()
        if args.export_root
        else pathlib.Path.home() / f"{args.export_root_prefix}-{run_id}"
    )
    update_state(state, state_path, current_stage="handoff_running", export_root=str(export_root))

    handoff_cmd = build_handoff_cmd(args, run_id=run_id, export_root=export_root)
    handoff_proc = subprocess.Popen(
        handoff_cmd,
        cwd=str(repo_root),
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        bufsize=1,
    )
    current_proc["proc"] = handoff_proc
    assert handoff_proc.stdout is not None
    try:
        for line in handoff_proc.stdout:
            print(line, end="", flush=True)
    finally:
        handoff_proc.stdout.close()
    handoff_rc = handoff_proc.wait()
    current_proc["proc"] = None
    if handoff_rc != 0:
        update_state(
            state,
            state_path,
            status="failed",
            current_stage="handoff_failed",
            handoff_returncode=handoff_rc,
            error=f"handoff command failed ({handoff_rc})",
        )
        return handoff_rc

    handoff_record_path = handoff_dir / f"{run_id}.json"
    handoff_record = load_json(handoff_record_path)
    update_state(
        state,
        state_path,
        status="completed",
        current_stage="completed",
        handoff_record_path=str(handoff_record_path),
        manifest_path=handoff_record.get("export_cursor", {}).get("manifest_path"),
        exported_sessions=handoff_record.get("export_cursor", {}).get("exported_sessions"),
        exported_files=handoff_record.get("export_cursor", {}).get("exported_files"),
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
