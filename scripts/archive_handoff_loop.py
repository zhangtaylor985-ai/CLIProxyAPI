#!/usr/bin/env python3
"""Continuously archive cold remote session data, hand it off locally, and persist a file cursor."""

from __future__ import annotations

import argparse
import fcntl
import json
import os
import pathlib
import select
import signal
import subprocess
import sys
import time
from datetime import datetime, timezone
from typing import Any


PSQL_CANDIDATE_PATHS = (
    "/opt/homebrew/opt/libpq/bin/psql",
    "/opt/homebrew/bin/psql",
    "/usr/local/bin/psql",
    "/usr/bin/psql",
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


def parse_dt(raw: str) -> datetime:
    return datetime.fromisoformat(raw.replace("Z", "+00:00")).astimezone(timezone.utc)


def resolve_psql() -> str:
    found = shutil_which("psql")
    if found:
        return found
    for candidate in PSQL_CANDIDATE_PATHS:
        if pathlib.Path(candidate).exists():
            return candidate
    raise SystemExit("required binary not found: psql")


def shutil_which(name: str) -> str | None:
    from shutil import which

    return which(name)


def write_json(path: pathlib.Path, payload: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def update_json(path: pathlib.Path, payload: dict[str, Any], **changes: Any) -> dict[str, Any]:
    payload.update(changes)
    payload["updated_at"] = utc_now()
    write_json(path, payload)
    return payload


def load_json(path: pathlib.Path) -> dict[str, Any]:
    return json.loads(path.read_text(encoding="utf-8"))


def load_json_if_exists(path: pathlib.Path) -> dict[str, Any] | None:
    if not path.exists():
        return None
    try:
        return load_json(path)
    except Exception:
        return None


def build_env(*, archive_pg_dsn: str, archive_dsn_env: str) -> dict[str, str]:
    env = os.environ.copy()
    path_parts = [part for part in env.get("PATH", "").split(":") if part]
    for part in reversed(EXTRA_PATHS):
        if part not in path_parts:
            path_parts.insert(0, part)
    env["PATH"] = ":".join(path_parts)
    env[archive_dsn_env] = archive_pg_dsn
    return env


def run_psql_scalar(*, dsn: str, sql: str) -> str:
    proc = subprocess.run(
        [resolve_psql(), dsn, "-Atc", sql],
        text=True,
        capture_output=True,
        check=False,
    )
    if proc.returncode != 0:
        raise SystemExit(proc.stderr.strip() or f"psql failed ({proc.returncode})")
    return proc.stdout.strip()


def query_remote_stats(*, dsn: str, inactive_hours: int) -> dict[str, Any]:
    eligible_count = int(
        run_psql_scalar(
            dsn=dsn,
            sql=(
                "SELECT count(*) "
                "FROM session_trajectory_sessions "
                f"WHERE last_activity_at < now() - interval '{inactive_hours} hours';"
            ),
        )
        or "0"
    )
    eligible_min = run_psql_scalar(
        dsn=dsn,
        sql=(
            "SELECT COALESCE(to_char(min(last_activity_at), 'YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"'), '') "
            "FROM session_trajectory_sessions "
            f"WHERE last_activity_at < now() - interval '{inactive_hours} hours';"
        ),
    )
    eligible_max = run_psql_scalar(
        dsn=dsn,
        sql=(
            "SELECT COALESCE(to_char(max(last_activity_at), 'YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"'), '') "
            "FROM session_trajectory_sessions "
            f"WHERE last_activity_at < now() - interval '{inactive_hours} hours';"
        ),
    )
    remote_min = run_psql_scalar(
        dsn=dsn,
        sql="SELECT COALESCE(to_char(min(last_activity_at), 'YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"'), '') FROM session_trajectory_sessions;",
    )
    remote_max = run_psql_scalar(
        dsn=dsn,
        sql="SELECT COALESCE(to_char(max(last_activity_at), 'YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"'), '') FROM session_trajectory_sessions;",
    )
    return {
        "eligible_sessions": eligible_count,
        "eligible_min_last_activity_at": eligible_min or None,
        "eligible_max_last_activity_at": eligible_max or None,
        "remote_min_last_activity_at": remote_min or None,
        "remote_max_last_activity_at": remote_max or None,
        "checked_at": utc_now(),
    }


def extract_cursor_candidate(record: dict[str, Any], path: pathlib.Path) -> dict[str, Any] | None:
    if record.get("task") == "task3_live_export_window_delete":
        end_time = record.get("window", {}).get("end_time")
        if not end_time:
            return None
        return {
            "cursor_type": "task3_live_export_delete",
            "processed_end_time": end_time,
            "cursor_file": str(path),
            "manifest_path": record.get("export", {}).get("manifest_path"),
            "export_root": record.get("export", {}).get("export_root"),
            "exported_sessions": record.get("export", {}).get("exported_sessions"),
            "exported_files": record.get("export", {}).get("exported_files"),
            "remote_min_last_activity_after_delete": record.get("remote_delete", {}).get("remote_min_last_activity_after_delete"),
        }
    source_cursor = record.get("source_cursor")
    export_cursor = record.get("export_cursor")
    if not isinstance(source_cursor, dict) or not isinstance(export_cursor, dict):
        return None
    end_time = source_cursor.get("archive_max_last_activity_at") or source_cursor.get("archive_cutoff_at")
    if not end_time:
        return None
    return {
        "cursor_type": "archive_handoff",
        "processed_end_time": end_time,
        "cursor_file": str(path),
        "archive_run_id": source_cursor.get("archive_run_id"),
        "archive_cutoff_at": source_cursor.get("archive_cutoff_at"),
        "archive_min_last_activity_at": source_cursor.get("archive_min_last_activity_at"),
        "archive_max_last_activity_at": source_cursor.get("archive_max_last_activity_at"),
        "manifest_path": export_cursor.get("manifest_path"),
        "export_root": export_cursor.get("export_root"),
        "exported_sessions": export_cursor.get("exported_sessions"),
        "exported_files": export_cursor.get("exported_files"),
    }


def choose_latest_cursor(records: list[dict[str, Any]]) -> dict[str, Any] | None:
    if not records:
        return None
    return max(records, key=lambda item: parse_dt(str(item["processed_end_time"])))


def discover_initial_cursor(*, handoff_dir: pathlib.Path) -> dict[str, Any] | None:
    candidates: list[dict[str, Any]] = []
    if not handoff_dir.exists():
        return None
    for path in sorted(handoff_dir.glob("*.json")):
        try:
            record = load_json(path)
        except Exception:
            continue
        candidate = extract_cursor_candidate(record, path)
        if candidate:
            candidates.append(candidate)
    latest = choose_latest_cursor(candidates)
    if latest is None:
        return None
    latest = dict(latest)
    latest["recorded_at"] = utc_now()
    return latest


def resolve_handoff_record_path(*, handoff_dir: pathlib.Path, run_id: str) -> pathlib.Path:
    return handoff_dir / f"{run_id}.json"


def recover_completed_chain_cursor(
    *,
    chain_state: dict[str, Any],
    handoff_dir: pathlib.Path,
) -> dict[str, Any] | None:
    run_id = str(chain_state.get("archive_run_id") or "").strip()
    if not run_id:
        return None
    if str(chain_state.get("current_stage") or "") != "completed":
        return None
    record_path = resolve_handoff_record_path(handoff_dir=handoff_dir, run_id=run_id)
    if not record_path.exists():
        return None
    try:
        record = load_json(record_path)
    except Exception:
        return None
    candidate = extract_cursor_candidate(record, record_path)
    if candidate is None:
        return None
    candidate = dict(candidate)
    candidate["recorded_at"] = utc_now()
    return candidate


def infer_resume_run_id(
    *,
    chain_state: dict[str, Any],
    current_cursor: dict[str, Any] | None,
) -> str:
    run_id = str(chain_state.get("archive_run_id") or "").strip()
    if not run_id:
        return ""
    current_stage = str(chain_state.get("current_stage") or "").strip()
    status = str(chain_state.get("status") or "").strip()
    if current_stage == "completed":
        return ""
    resumable = status in {"running", "interrupted", "failed"} or current_stage in {
        "archive_running",
        "archive_completed",
        "handoff_running",
        "archive_failed",
        "handoff_failed",
        "interrupted",
    }
    if not resumable:
        return ""
    if current_cursor and str(current_cursor.get("archive_run_id") or "").strip() == run_id:
        return ""
    return run_id


def archive_phase_progress_hint(*, phase: str, request_file_size: int) -> str:
    if phase == "initialized":
        return "0%"
    if phase == "candidates_materialized":
        return "10-50%" if request_file_size > 0 else "5-10%"
    if phase == "exported":
        return "50-60%"
    if phase in {"request_exports_deleted", "requests_deleted", "aliases_deleted", "sessions_deleted"}:
        return "60-90%"
    if phase == "vacuumed":
        return "90-99%"
    if phase == "completed":
        return "100%"
    return ""


def detect_request_export_progress(run_dir: pathlib.Path) -> tuple[str, int, str]:
    candidates = [
        (run_dir / "session_trajectory_requests.jsonl.gz", "final_gzip"),
        (run_dir / ".session_trajectory_requests.jsonl.tmp", "temp_plain"),
        (run_dir / ".session_trajectory_requests.jsonl.gz.tmp", "temp_gzip"),
    ]
    for path, stage in candidates:
        if not path.exists():
            continue
        return str(path), path.stat().st_size, stage
    return "", 0, ""


def collect_summary(
    *,
    archive_root: pathlib.Path,
    state_path: pathlib.Path,
    cursor_path: pathlib.Path,
    chain_state_path: pathlib.Path,
    summary_path: pathlib.Path,
) -> dict[str, Any]:
    now = utc_now()
    previous = load_json_if_exists(summary_path) or {}
    state = load_json_if_exists(state_path) or {}
    cursor = load_json_if_exists(cursor_path) or {}
    chain = load_json_if_exists(chain_state_path) or {}

    run_id = str(chain.get("archive_run_id") or state.get("resume_run_id") or "").strip()
    run_state: dict[str, Any] | None = None
    request_file_size = 0
    request_file_path = ""
    request_growth_bytes = 0
    request_growth_seconds = 0.0
    request_growth_rate = 0.0
    progress_hint = ""

    if run_id:
        run_state_path = archive_root / "runs" / run_id / "run-state.json"
        run_state = load_json_if_exists(run_state_path)
        request_file_path, request_file_size, request_file_stage = detect_request_export_progress(
            archive_root / "runs" / run_id
        )
        if run_state:
            progress_hint = archive_phase_progress_hint(
                phase=str(run_state.get("phase") or ""),
                request_file_size=request_file_size,
            )
    else:
        request_file_stage = ""

    prev_run_id = str(previous.get("run_id") or "").strip()
    prev_size = int(previous.get("request_file_size_bytes") or 0)
    prev_recorded_at = str(previous.get("recorded_at") or "").strip()
    if run_id and prev_run_id == run_id and request_file_size >= prev_size and prev_recorded_at:
        try:
            elapsed = (parse_dt(now) - parse_dt(prev_recorded_at)).total_seconds()
        except Exception:
            elapsed = 0.0
        if elapsed > 0:
            request_growth_bytes = request_file_size - prev_size
            request_growth_seconds = elapsed
            request_growth_rate = request_growth_bytes / elapsed

    summary = {
        "recorded_at": now,
        "status": state.get("status"),
        "current_stage": state.get("current_stage"),
        "child_pid": state.get("child_pid"),
        "run_id": run_id or None,
        "cursor_processed_end_time": cursor.get("processed_end_time"),
        "cursor_type": cursor.get("cursor_type"),
        "chain_stage": chain.get("current_stage"),
        "chain_status": chain.get("status"),
        "archive_cutoff_at": chain.get("archive_cutoff_at"),
        "archive_output_dir": chain.get("archive_output_dir"),
        "request_file_path": request_file_path or None,
        "request_file_stage": request_file_stage or None,
        "request_file_size_bytes": request_file_size,
        "request_growth_bytes": request_growth_bytes,
        "request_growth_seconds": round(request_growth_seconds, 3) if request_growth_seconds else 0,
        "request_growth_rate_bytes_per_sec": round(request_growth_rate, 2) if request_growth_rate else 0,
        "progress_hint": progress_hint or None,
    }
    if run_state:
        summary["run_state"] = {
            "phase": run_state.get("phase"),
            "completion_status": run_state.get("completion_status"),
            "counts": run_state.get("counts", {}),
            "cursor": run_state.get("cursor", {}),
            "updated_at": run_state.get("updated_at"),
        }
    if state.get("remote_stats"):
        summary["remote_stats"] = state.get("remote_stats")
    if state.get("cycle_window_hint"):
        summary["cycle_window_hint"] = state.get("cycle_window_hint")
    if state.get("last_completed_cursor"):
        summary["last_completed_cursor"] = state.get("last_completed_cursor")
    return summary


def refresh_summary(
    *,
    archive_root: pathlib.Path,
    state_path: pathlib.Path,
    cursor_path: pathlib.Path,
    chain_state_path: pathlib.Path,
    summary_path: pathlib.Path,
) -> dict[str, Any]:
    summary = collect_summary(
        archive_root=archive_root,
        state_path=state_path,
        cursor_path=cursor_path,
        chain_state_path=chain_state_path,
        summary_path=summary_path,
    )
    write_json(summary_path, summary)
    return summary


class LoopLock:
    def __init__(self, path: pathlib.Path) -> None:
        self.path = path
        self.handle: Any | None = None

    def __enter__(self) -> "LoopLock":
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self.handle = self.path.open("a+", encoding="utf-8")
        try:
            fcntl.flock(self.handle.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as exc:
            raise SystemExit(f"loop lock busy: {self.path}") from exc
        self.handle.seek(0)
        self.handle.truncate()
        self.handle.write(f"pid={os.getpid()} acquired_at={utc_now()}\n")
        self.handle.flush()
        return self

    def __exit__(self, exc_type: Any, exc: Any, tb: Any) -> None:
        if self.handle is None:
            return
        try:
            self.handle.seek(0)
            self.handle.truncate()
            self.handle.flush()
            fcntl.flock(self.handle.fileno(), fcntl.LOCK_UN)
        finally:
            self.handle.close()
            self.handle = None


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mode", choices=("run", "status"), default="run")
    parser.add_argument("--archive-pg-dsn", required=True)
    parser.add_argument("--archive-dsn-env", default="SESSION_TRAJECTORY_PG_DSN")
    parser.add_argument("--archive-root", type=pathlib.Path, default=pathlib.Path.home() / "CLIProxyAPI-session-archives-local")
    parser.add_argument("--handoff-dir", type=pathlib.Path, default=pathlib.Path.home() / "CLIProxyAPI-session-archives-local" / "handoffs")
    parser.add_argument("--manifest-dir", type=pathlib.Path, default=pathlib.Path.home() / "session-trajectory-export-manifests")
    parser.add_argument("--target-pg-dsn", required=True)
    parser.add_argument("--state-path", type=pathlib.Path, default=pathlib.Path.home() / "CLIProxyAPI-session-archives-local" / "handoffs" / "archive_handoff_loop.state.json")
    parser.add_argument("--cursor-path", type=pathlib.Path, default=pathlib.Path.home() / "CLIProxyAPI-session-archives-local" / "handoffs" / "archive_handoff_loop.cursor.json")
    parser.add_argument("--chain-state-path", type=pathlib.Path, default=pathlib.Path.home() / "CLIProxyAPI-session-archives-local" / "handoffs" / "active_archive_chain.json")
    parser.add_argument("--summary-path", type=pathlib.Path, default=pathlib.Path.home() / "CLIProxyAPI-session-archives-local" / "handoffs" / "archive_handoff_loop.summary.json")
    parser.add_argument("--export-root-prefix", default="session-trajectory-export-session-archive")
    parser.add_argument("--inactive-hours", type=int, default=24)
    parser.add_argument("--batch-size", type=int, default=500)
    parser.add_argument("--check-interval-seconds", type=int, default=1800)
    parser.add_argument("--failure-sleep-seconds", type=int, default=300)
    parser.add_argument("--summary-interval-seconds", type=int, default=15)
    parser.add_argument("--skip-request-exports", action="store_true")
    parser.add_argument("--require-storage-prefix", default="")
    parser.add_argument("--lock-path", type=pathlib.Path, default=pathlib.Path("/tmp/cliproxy-archive-handoff-loop.lock"))
    return parser.parse_args(argv)


def build_managed_cmd(args: argparse.Namespace, *, resume_run_id: str = "") -> list[str]:
    cmd = [
        sys.executable,
        "scripts/managed_archive_handoff.py",
        "--archive-pg-dsn",
        args.archive_pg_dsn,
        "--archive-dsn-env",
        args.archive_dsn_env,
        "--archive-root",
        str(args.archive_root),
        "--handoff-dir",
        str(args.handoff_dir),
        "--manifest-dir",
        str(args.manifest_dir),
        "--target-pg-dsn",
        args.target_pg_dsn,
        "--state-path",
        str(args.chain_state_path),
        "--export-root-prefix",
        args.export_root_prefix,
        "--inactive-hours",
        str(args.inactive_hours),
        "--batch-size",
        str(args.batch_size),
    ]
    if resume_run_id:
        cmd.extend(["--run-id", resume_run_id])
    if args.skip_request_exports:
        cmd.append("--skip-request-exports")
    if args.require_storage_prefix:
        cmd.extend(["--require-storage-prefix", args.require_storage_prefix])
    return cmd


def run_cycle_process(
    *,
    cmd: list[str],
    cwd: pathlib.Path,
    env: dict[str, str],
    state: dict[str, Any],
    state_path: pathlib.Path,
    archive_root: pathlib.Path,
    cursor_path: pathlib.Path,
    chain_state_path: pathlib.Path,
    summary_path: pathlib.Path,
    summary_interval_seconds: int,
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
    state["child_pid"] = proc.pid
    write_json(state_path, state)
    assert proc.stdout is not None
    os.set_blocking(proc.stdout.fileno(), False)
    buffer = ""
    next_summary_at = 0.0
    try:
        while True:
            now_monotonic = time.monotonic()
            if now_monotonic >= next_summary_at:
                refresh_summary(
                    archive_root=archive_root,
                    state_path=state_path,
                    cursor_path=cursor_path,
                    chain_state_path=chain_state_path,
                    summary_path=summary_path,
                )
                next_summary_at = now_monotonic + max(3, summary_interval_seconds)

            ready, _, _ = select.select([proc.stdout], [], [], 1.0)
            if ready:
                chunk = proc.stdout.read()
                if chunk:
                    buffer += chunk
                    while "\n" in buffer:
                        line, buffer = buffer.split("\n", 1)
                        print(line, flush=True)
            if proc.poll() is not None:
                tail = proc.stdout.read() or ""
                if tail:
                    buffer += tail
                if buffer:
                    for line in buffer.splitlines():
                        print(line, flush=True)
                break
    finally:
        proc.stdout.close()
    rc = proc.wait()
    refresh_summary(
        archive_root=archive_root,
        state_path=state_path,
        cursor_path=cursor_path,
        chain_state_path=chain_state_path,
        summary_path=summary_path,
    )
    return rc


def update_cursor_from_cycle(
    *,
    cursor_path: pathlib.Path,
    chain_state_path: pathlib.Path,
    handoff_dir: pathlib.Path,
    archive_pg_dsn: str,
    inactive_hours: int,
) -> dict[str, Any]:
    chain_state = load_json(chain_state_path)
    handoff_record_path = pathlib.Path(chain_state["handoff_record_path"])
    handoff_record = load_json(handoff_record_path)
    source_cursor = handoff_record["source_cursor"]
    export_cursor = handoff_record["export_cursor"]
    remote_stats = query_remote_stats(dsn=archive_pg_dsn, inactive_hours=inactive_hours)
    cursor = {
        "recorded_at": utc_now(),
        "cursor_type": "archive_handoff",
        "processed_end_time": source_cursor.get("archive_max_last_activity_at") or source_cursor.get("archive_cutoff_at"),
        "archive_run_id": source_cursor.get("archive_run_id"),
        "archive_cutoff_at": source_cursor.get("archive_cutoff_at"),
        "archive_min_last_activity_at": source_cursor.get("archive_min_last_activity_at"),
        "archive_max_last_activity_at": source_cursor.get("archive_max_last_activity_at"),
        "archive_counts": source_cursor.get("archive_counts", {}),
        "cursor_file": str(handoff_record_path),
        "manifest_path": export_cursor.get("manifest_path"),
        "export_root": export_cursor.get("export_root"),
        "exported_sessions": export_cursor.get("exported_sessions"),
        "exported_files": export_cursor.get("exported_files"),
        "remote_min_last_activity_after_delete": remote_stats.get("remote_min_last_activity_at"),
        "remote_max_last_activity_after_delete": remote_stats.get("remote_max_last_activity_at"),
    }
    write_json(cursor_path, cursor)
    return cursor


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    repo_root = pathlib.Path(__file__).resolve().parents[1]
    env = build_env(archive_pg_dsn=args.archive_pg_dsn, archive_dsn_env=args.archive_dsn_env)
    archive_root = args.archive_root.expanduser().resolve()
    handoff_dir = args.handoff_dir.expanduser().resolve()
    state_path = args.state_path.expanduser().resolve()
    cursor_path = args.cursor_path.expanduser().resolve()
    chain_state_path = args.chain_state_path.expanduser().resolve()
    summary_path = args.summary_path.expanduser().resolve()

    if args.mode == "status":
        summary = refresh_summary(
            archive_root=archive_root,
            state_path=state_path,
            cursor_path=cursor_path,
            chain_state_path=chain_state_path,
            summary_path=summary_path,
        )
        print(json.dumps(summary, indent=2, sort_keys=True))
        return 0

    state: dict[str, Any]
    if state_path.exists():
        state = load_json(state_path)
    else:
        state = {
            "status": "running",
            "current_stage": "initializing",
            "started_at": utc_now(),
            "updated_at": utc_now(),
            "archive_root": str(archive_root),
            "handoff_dir": str(handoff_dir),
            "manifest_dir": str(args.manifest_dir.expanduser().resolve()),
            "cursor_path": str(cursor_path),
            "chain_state_path": str(chain_state_path),
            "summary_path": str(summary_path),
        }
        write_json(state_path, state)

    if not cursor_path.exists():
        initial_cursor = discover_initial_cursor(handoff_dir=handoff_dir)
        if initial_cursor is not None:
            write_json(cursor_path, initial_cursor)
            update_json(state_path, state, current_stage="cursor_initialized", cursor_source=initial_cursor.get("cursor_file"))

    if chain_state_path.exists():
        try:
            chain_state = load_json(chain_state_path)
        except Exception:
            chain_state = {}
        current_cursor = load_json(cursor_path) if cursor_path.exists() else None
        recovered_cursor = recover_completed_chain_cursor(chain_state=chain_state, handoff_dir=handoff_dir)
        if recovered_cursor is not None:
            if current_cursor is None or parse_dt(str(recovered_cursor["processed_end_time"])) > parse_dt(str(current_cursor["processed_end_time"])):
                write_json(cursor_path, recovered_cursor)
                current_cursor = recovered_cursor
                update_json(
                    state_path,
                    state,
                    current_stage="cursor_recovered_from_completed_chain",
                    cursor_source=recovered_cursor.get("cursor_file"),
                    cursor=recovered_cursor,
                )
        inferred_resume = infer_resume_run_id(chain_state=chain_state, current_cursor=current_cursor)
        if inferred_resume:
            update_json(
                state_path,
                state,
                current_stage="startup_resuming_prior_cycle",
                resume_run_id=inferred_resume,
            )

    stop_requested = {"value": False}

    def on_signal(signum: int, _frame: Any) -> None:
        stop_requested["value"] = True
        update_json(
            state_path,
            state,
            status="interrupted",
            current_stage="interrupted",
            signal=signal.Signals(signum).name,
            error=f"received signal {signum}",
        )

    signal.signal(signal.SIGTERM, on_signal)
    signal.signal(signal.SIGINT, on_signal)
    if hasattr(signal, "SIGHUP"):
        signal.signal(signal.SIGHUP, on_signal)

    refresh_summary(
        archive_root=archive_root,
        state_path=state_path,
        cursor_path=cursor_path,
        chain_state_path=chain_state_path,
        summary_path=summary_path,
    )

    with LoopLock(args.lock_path.expanduser().resolve()):
        while not stop_requested["value"]:
            persisted_resume_run_id = str(state.get("resume_run_id") or "").strip()
            if persisted_resume_run_id:
                cycle_cmd = build_managed_cmd(args, resume_run_id=persisted_resume_run_id)
                update_json(
                    state_path,
                    state,
                    status="running",
                    current_stage="cycle_resuming",
                    current_command=cycle_cmd,
                )
                cycle_rc = run_cycle_process(
                    cmd=cycle_cmd,
                    cwd=repo_root,
                    env=env,
                    state=state,
                    state_path=state_path,
                    archive_root=archive_root,
                    cursor_path=cursor_path,
                    chain_state_path=chain_state_path,
                    summary_path=summary_path,
                    summary_interval_seconds=args.summary_interval_seconds,
                )
                if cycle_rc != 0:
                    update_json(
                        state_path,
                        state,
                        current_stage="cycle_failed_waiting_retry",
                        cycle_returncode=cycle_rc,
                    )
                    time.sleep(max(5, args.failure_sleep_seconds))
                    continue
                state["resume_run_id"] = None

            remote_stats = query_remote_stats(dsn=args.archive_pg_dsn, inactive_hours=args.inactive_hours)
            update_json(
                state_path,
                state,
                    status="running",
                    current_stage="polling_remote",
                    remote_stats=remote_stats,
                    cursor=load_json(cursor_path) if cursor_path.exists() else None,
                )
            refresh_summary(
                archive_root=archive_root,
                state_path=state_path,
                cursor_path=cursor_path,
                chain_state_path=chain_state_path,
                summary_path=summary_path,
            )

            if int(remote_stats["eligible_sessions"]) == 0:
                update_json(
                    state_path,
                    state,
                    current_stage="idle_waiting_for_cold_data",
                    last_idle_at=utc_now(),
                )
                time.sleep(max(5, args.check_interval_seconds))
                continue

            cycle_cmd = build_managed_cmd(args)
            update_json(
                state_path,
                state,
                current_stage="cycle_running",
                current_command=cycle_cmd,
                cycle_started_at=utc_now(),
                cycle_window_hint={
                    "eligible_min_last_activity_at": remote_stats.get("eligible_min_last_activity_at"),
                    "eligible_max_last_activity_at": remote_stats.get("eligible_max_last_activity_at"),
                },
            )
            cycle_rc = run_cycle_process(
                cmd=cycle_cmd,
                cwd=repo_root,
                env=env,
                state=state,
                state_path=state_path,
                archive_root=archive_root,
                cursor_path=cursor_path,
                chain_state_path=chain_state_path,
                summary_path=summary_path,
                summary_interval_seconds=args.summary_interval_seconds,
            )

            if stop_requested["value"]:
                break

            if cycle_rc != 0:
                resume_run_id = ""
                if chain_state_path.exists():
                    try:
                        resume_run_id = str(load_json(chain_state_path).get("archive_run_id") or "")
                    except Exception:
                        resume_run_id = ""
                update_json(
                    state_path,
                    state,
                    status="running",
                    current_stage="cycle_failed_waiting_retry",
                    cycle_returncode=cycle_rc,
                    resume_run_id=resume_run_id or None,
                    retry_at=utc_now(),
                )
                time.sleep(max(5, args.failure_sleep_seconds))
                if resume_run_id:
                    cycle_cmd = build_managed_cmd(args, resume_run_id=resume_run_id)
                    update_json(
                        state_path,
                        state,
                        current_stage="cycle_resuming",
                        current_command=cycle_cmd,
                    )
                    cycle_rc = run_cycle_process(
                        cmd=cycle_cmd,
                        cwd=repo_root,
                        env=env,
                        state=state,
                        state_path=state_path,
                        archive_root=archive_root,
                        cursor_path=cursor_path,
                        chain_state_path=chain_state_path,
                        summary_path=summary_path,
                        summary_interval_seconds=args.summary_interval_seconds,
                    )
                    if cycle_rc != 0:
                        update_json(
                            state_path,
                            state,
                            current_stage="cycle_failed_waiting_retry",
                            cycle_returncode=cycle_rc,
                        )
                        time.sleep(max(5, args.failure_sleep_seconds))
                        continue
                else:
                    continue

            if not chain_state_path.exists():
                update_json(
                    state_path,
                    state,
                    current_stage="cycle_completed_without_state",
                    error="chain state missing after successful cycle",
                )
                time.sleep(max(5, args.failure_sleep_seconds))
                continue

            chain_state = load_json(chain_state_path)
            if chain_state.get("current_stage") == "completed_noop":
                update_json(
                    state_path,
                    state,
                    current_stage="idle_waiting_for_cold_data",
                    last_noop_completed_at=utc_now(),
                )
                time.sleep(max(5, args.check_interval_seconds))
                continue

            cursor = update_cursor_from_cycle(
                cursor_path=cursor_path,
                chain_state_path=chain_state_path,
                handoff_dir=handoff_dir,
                archive_pg_dsn=args.archive_pg_dsn,
                inactive_hours=args.inactive_hours,
            )
            update_json(
                state_path,
                state,
                current_stage="cycle_completed",
                last_completed_at=utc_now(),
                last_completed_cursor=cursor,
                child_pid=None,
                current_command=None,
                resume_run_id=None,
            )
            refresh_summary(
                archive_root=archive_root,
                state_path=state_path,
                cursor_path=cursor_path,
                chain_state_path=chain_state_path,
                summary_path=summary_path,
            )

    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
