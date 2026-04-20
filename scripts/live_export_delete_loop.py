#!/usr/bin/env python3
"""Continuously export cold sessions to final files, then delete the same verified snapshot from PostgreSQL."""

from __future__ import annotations

import argparse
import csv
import fcntl
import json
import os
import pathlib
import re
import select
import signal
import subprocess
import sys
import time
from datetime import datetime, timedelta, timezone
from typing import Any


PHASES = [
    "initialized",
    "snapshot_materialized",
    "exported",
    "request_exports_deleted",
    "requests_deleted",
    "aliases_deleted",
    "sessions_deleted",
    "vacuumed",
    "completed",
]

GO_CANDIDATE_PATHS = (
    "/usr/local/go/bin/go",
    "/opt/homebrew/bin/go",
    "/usr/bin/go",
)

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

MATCHED_RE = re.compile(r"matched (\d+) sessions for export")
PROGRESS_RE = re.compile(r"(reused|exported) (\d+)/(\d+) sessions \((\d+) files\)")


def utc_now() -> datetime:
    return datetime.now(timezone.utc).replace(microsecond=0)


def utc_now_str() -> str:
    return format_dt(utc_now())


def format_dt(value: datetime) -> str:
    return value.astimezone(timezone.utc).isoformat().replace("+00:00", "Z")


def parse_dt(raw: str) -> datetime:
    return datetime.fromisoformat(raw.replace("Z", "+00:00")).astimezone(timezone.utc)


def quote_ident(value: str) -> str:
    return '"' + value.replace('"', '""') + '"'


def quote_literal(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def one_line_sql(sql: str) -> str:
    return " ".join(sql.split())


def phase_index(name: str) -> int:
    try:
        return PHASES.index(name)
    except ValueError as exc:
        raise ValueError(f"unknown phase: {name}") from exc


def phase_at_least(current: str, target: str) -> bool:
    return phase_index(current) >= phase_index(target)


def sanitize_fragment(value: str) -> str:
    normalized = re.sub(r"[^A-Za-z0-9._-]+", "-", value.strip())
    normalized = normalized.strip(".-")
    return normalized or "live-export-delete"


def write_json(path: pathlib.Path, payload: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temp_path = path.with_suffix(path.suffix + ".tmp")
    temp_path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    temp_path.replace(path)


def load_json(path: pathlib.Path) -> dict[str, Any]:
    return json.loads(path.read_text(encoding="utf-8"))


def load_json_if_exists(path: pathlib.Path) -> dict[str, Any] | None:
    if not path.exists():
        return None
    try:
        return load_json(path)
    except Exception:
        return None


def resolve_binary(name: str, candidates: tuple[str, ...]) -> str:
    found = shutil_which(name)
    if found:
        return found
    for candidate in candidates:
        if pathlib.Path(candidate).exists():
            return candidate
    raise SystemExit(f"required binary not found: {name}")


def shutil_which(name: str) -> str | None:
    from shutil import which

    return which(name)


def build_env() -> dict[str, str]:
    env = os.environ.copy()
    path_parts = [part for part in env.get("PATH", "").split(":") if part]
    for part in reversed(EXTRA_PATHS):
        if part not in path_parts:
            path_parts.insert(0, part)
    env["PATH"] = ":".join(path_parts)
    return env


def run_command(cmd: list[str], *, input_text: str | None = None, env: dict[str, str] | None = None) -> str:
    proc = subprocess.run(
        cmd,
        input=input_text,
        text=True,
        capture_output=True,
        check=False,
        env=env,
    )
    if proc.returncode != 0:
        raise RuntimeError(
            "command failed\n"
            f"cmd: {' '.join(cmd)}\n"
            f"stdout:\n{proc.stdout}\n"
            f"stderr:\n{proc.stderr}"
        )
    return proc.stdout


def psql_base_cmd(dsn: str) -> list[str]:
    return [resolve_binary("psql", PSQL_CANDIDATE_PATHS), dsn, "-q", "-X", "-v", "ON_ERROR_STOP=1", "-P", "pager=off"]


def run_psql_script(dsn: str, script: str, *, env: dict[str, str]) -> str:
    cmd = psql_base_cmd(dsn) + ["-f", "-"]
    return run_command(cmd, input_text=script, env=env)


def count_plain_rows(path: pathlib.Path) -> int:
    if not path.exists():
        return 0
    with path.open("r", encoding="utf-8", newline="") as handle:
        return sum(1 for _ in handle)


def parse_export_progress_line(line: str) -> dict[str, int | str] | None:
    matched = MATCHED_RE.search(line)
    if matched:
        return {"kind": "matched", "matched_sessions": int(matched.group(1))}
    progress = PROGRESS_RE.search(line)
    if progress:
        return {
            "kind": str(progress.group(1)),
            "done_sessions": int(progress.group(2)),
            "total_sessions": int(progress.group(3)),
            "exported_files": int(progress.group(4)),
        }
    return None


def build_run_id(now: datetime) -> str:
    return "session-live-export-delete-" + now.astimezone(timezone.utc).strftime("%Y%m%dT%H%M%SZ")


def append_warning(run_state: dict[str, Any], *, code: str, message: str, detail: str = "") -> None:
    warning = {"code": code, "message": message}
    if detail:
        warning["detail"] = detail
    run_state.setdefault("warnings", []).append(warning)


def finalize_completion(run_state: dict[str, Any]) -> None:
    if run_state.get("completion_status") != "in_progress":
        return
    vacuum_summary = str(run_state.get("vacuum", {}).get("summary") or "").strip()
    if vacuum_summary in {"unknown", "failed"} or run_state.get("warnings"):
        run_state["completion_status"] = "completed_with_warnings"
        return
    run_state["completion_status"] = "ok"


def query_remote_stats(*, dsn: str, schema: str, inactive_hours: int, env: dict[str, str]) -> dict[str, Any]:
    schema_table = f"{quote_ident(schema)}.{quote_ident('session_trajectory_sessions')}"
    sql = f"""
WITH eligible AS (
  SELECT last_activity_at
  FROM {schema_table}
  WHERE last_activity_at < now() - interval '{inactive_hours} hours'
)
SELECT
  (SELECT count(*) FROM eligible),
  COALESCE((SELECT to_char(min(last_activity_at) AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM eligible), ''),
  COALESCE((SELECT to_char(max(last_activity_at) AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM eligible), ''),
  COALESCE((SELECT to_char(min(last_activity_at) AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM {schema_table}), ''),
  COALESCE((SELECT to_char(max(last_activity_at) AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM {schema_table}), '');
"""
    output = run_command(psql_base_cmd(dsn) + ["-At", "-F", "\t", "-c", one_line_sql(sql)], env=env).strip().splitlines()
    line = output[-1] if output else "0\t\t\t\t"
    eligible_count, eligible_min, eligible_max, remote_min, remote_max = line.split("\t")
    return {
        "eligible_sessions": int(eligible_count or "0"),
        "eligible_min_last_activity_at": eligible_min or None,
        "eligible_max_last_activity_at": eligible_max or None,
        "remote_min_last_activity_at": remote_min or None,
        "remote_max_last_activity_at": remote_max or None,
        "checked_at": utc_now_str(),
    }


def snapshot_candidates(
    *,
    dsn: str,
    schema: str,
    cutoff_at: datetime,
    candidate_file: pathlib.Path,
    session_id_file: pathlib.Path,
    env: dict[str, str],
) -> dict[str, Any]:
    schema_table = f"{quote_ident(schema)}.{quote_ident('session_trajectory_sessions')}"
    cutoff_literal = quote_literal(format_dt(cutoff_at))
    candidate_literal = quote_literal(str(candidate_file))
    query = one_line_sql(
        f"""
        SELECT id, last_activity_at
        FROM {schema_table}
        WHERE last_activity_at < {cutoff_literal}::timestamptz
        ORDER BY last_activity_at ASC, id ASC
        """
    )
    run_psql_script(dsn, f"\\copy ({query}) TO {candidate_literal} WITH (FORMAT csv)\n", env=env)
    stats = rewrite_session_id_file_from_candidate(candidate_file=candidate_file, session_id_file=session_id_file)
    return stats


def rewrite_session_id_file_from_candidate(*, candidate_file: pathlib.Path, session_id_file: pathlib.Path) -> dict[str, Any]:
    session_ids: list[str] = []
    min_last_activity_at = ""
    max_last_activity_at = ""
    if candidate_file.exists():
        with candidate_file.open("r", encoding="utf-8", newline="") as handle:
            reader = csv.reader(handle)
            for row in reader:
                if not row:
                    continue
                session_id = str(row[0]).strip()
                last_activity_at = str(row[1]).strip() if len(row) > 1 else ""
                if not session_id:
                    continue
                session_ids.append(session_id)
                if last_activity_at:
                    if not min_last_activity_at:
                        min_last_activity_at = last_activity_at
                    max_last_activity_at = last_activity_at
    session_id_file.parent.mkdir(parents=True, exist_ok=True)
    payload = "\n".join(session_ids)
    if payload:
        payload += "\n"
    session_id_file.write_text(payload, encoding="utf-8")
    return {
        "candidate_sessions": len(session_ids),
        "min_last_activity_at": min_last_activity_at,
        "max_last_activity_at": max_last_activity_at,
    }


def prune_reactivated_candidates(
    *,
    dsn: str,
    schema: str,
    cutoff_at: datetime,
    candidate_file: pathlib.Path,
    session_id_file: pathlib.Path,
    env: dict[str, str],
) -> dict[str, Any]:
    temp_candidate = candidate_file.with_name(f".{candidate_file.name}.tmp")
    schema_table = f"{quote_ident(schema)}.{quote_ident('session_trajectory_sessions')}"
    file_literal = quote_literal(str(candidate_file))
    temp_literal = quote_literal(str(temp_candidate))
    cutoff_literal = quote_literal(format_dt(cutoff_at))
    retained_query = one_line_sql(
        f"""
        SELECT s.id,
               to_char(s.last_activity_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
        FROM {schema_table} s
        JOIN live_export_sessions a ON a.session_id = s.id
        WHERE s.last_activity_at < {cutoff_literal}::timestamptz
        ORDER BY s.last_activity_at ASC, s.id ASC
        """
    )
    script = f"""
\\pset tuples_only on
\\pset format unaligned
\\f '\\t'
CREATE TEMP TABLE live_export_sessions (
  session_id uuid PRIMARY KEY,
  last_activity_at timestamptz NOT NULL
);
\\copy live_export_sessions (session_id, last_activity_at) FROM {file_literal} WITH (FORMAT csv)
\\copy ({retained_query}) TO {temp_literal} WITH (FORMAT csv)
SELECT
  (SELECT count(*) FROM live_export_sessions),
  (SELECT count(*) FROM {schema_table} s JOIN live_export_sessions a ON a.session_id = s.id WHERE s.last_activity_at < {cutoff_literal}::timestamptz),
  (SELECT count(*) FROM {schema_table} s JOIN live_export_sessions a ON a.session_id = s.id WHERE s.last_activity_at >= {cutoff_literal}::timestamptz),
  COALESCE((SELECT to_char(min(s.last_activity_at) AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM {schema_table} s JOIN live_export_sessions a ON a.session_id = s.id WHERE s.last_activity_at < {cutoff_literal}::timestamptz), ''),
  COALESCE((SELECT to_char(max(s.last_activity_at) AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM {schema_table} s JOIN live_export_sessions a ON a.session_id = s.id WHERE s.last_activity_at < {cutoff_literal}::timestamptz), '');
"""
    if temp_candidate.exists():
        temp_candidate.unlink()
    try:
        line = run_psql_script(dsn, script, env=env).strip().splitlines()[-1]
        original_count, retained_count, pruned_count, min_ts, max_ts = line.split("\t")
        temp_candidate.replace(candidate_file)
        rewrite_session_id_file_from_candidate(candidate_file=candidate_file, session_id_file=session_id_file)
        return {
            "original_sessions": int(original_count),
            "candidate_sessions": int(retained_count),
            "pruned_sessions": int(pruned_count),
            "min_last_activity_at": min_ts,
            "max_last_activity_at": max_ts,
        }
    finally:
        if temp_candidate.exists():
            temp_candidate.unlink()


def query_target_counts(*, dsn: str, schema: str, candidate_file: pathlib.Path, env: dict[str, str]) -> dict[str, int | str]:
    schema_q = quote_ident(schema)
    file_literal = quote_literal(str(candidate_file))
    script = f"""
\\pset tuples_only on
\\pset format unaligned
\\f '\\t'
CREATE TEMP TABLE live_export_sessions (
  session_id uuid PRIMARY KEY,
  last_activity_at timestamptz NOT NULL
);
\\copy live_export_sessions (session_id, last_activity_at) FROM {file_literal} WITH (FORMAT csv)
SELECT
  (SELECT count(*) FROM {schema_q}.session_trajectory_sessions s JOIN live_export_sessions a ON a.session_id = s.id),
  (SELECT count(*) FROM {schema_q}.session_trajectory_session_aliases sa JOIN live_export_sessions a ON a.session_id = sa.session_id),
  (SELECT count(*) FROM {schema_q}.session_trajectory_requests r JOIN live_export_sessions a ON a.session_id = r.session_id),
  (SELECT count(*) FROM {schema_q}.session_trajectory_request_exports e JOIN live_export_sessions a ON a.session_id = e.session_id),
  COALESCE((SELECT to_char(min(last_activity_at) AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM live_export_sessions), ''),
  COALESCE((SELECT to_char(max(last_activity_at) AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM live_export_sessions), '');
"""
    line = run_psql_script(dsn, script, env=env).strip().splitlines()[-1]
    sessions, aliases, requests, request_exports, min_ts, max_ts = line.split("\t")
    return {
        "sessions": int(sessions),
        "aliases": int(aliases),
        "requests": int(requests),
        "request_exports": int(request_exports),
        "min_last_activity_at": min_ts,
        "max_last_activity_at": max_ts,
    }


def delete_batch(*, dsn: str, candidate_file: pathlib.Path, sql: str, env: dict[str, str]) -> int:
    file_literal = quote_literal(str(candidate_file))
    script = f"""
\\pset tuples_only on
\\pset format unaligned
CREATE TEMP TABLE live_export_sessions (
  session_id uuid PRIMARY KEY,
  last_activity_at timestamptz NOT NULL
);
\\copy live_export_sessions (session_id, last_activity_at) FROM {file_literal} WITH (FORMAT csv)
{sql}
"""
    output = run_psql_script(dsn, script, env=env).strip().splitlines()
    if not output:
        return 0
    return int(output[-1])


def delete_in_batches(
    *,
    dsn: str,
    candidate_file: pathlib.Path,
    run_state: dict[str, Any],
    run_state_path: pathlib.Path,
    stage_name: str,
    sql_factory,
    env: dict[str, str],
) -> int:
    total_deleted = int(run_state.get("deleted", {}).get(stage_name, 0))
    batches = 0
    while True:
        deleted = delete_batch(dsn=dsn, candidate_file=candidate_file, sql=sql_factory(), env=env)
        if deleted == 0:
            break
        total_deleted += deleted
        batches += 1
        run_state.setdefault("deleted", {})[stage_name] = total_deleted
        run_state["updated_at"] = utc_now_str()
        write_json(run_state_path, run_state)
        if batches % 10 == 0:
            print(f"[progress] {stage_name}: deleted {total_deleted} rows after {batches} batches", flush=True)
    return total_deleted


def vacuum_table(*, dsn: str, schema: str, table_name: str, timeout_seconds: int, env: dict[str, str]) -> dict[str, Any]:
    target = f"{quote_ident(schema)}.{quote_ident(table_name)}"
    cmd = psql_base_cmd(dsn) + ["-c", f"VACUUM (ANALYZE) {target};"]
    started = time.monotonic()
    proc = subprocess.Popen(
        cmd,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        env=env,
    )
    try:
        stdout, stderr = proc.communicate(timeout=timeout_seconds if timeout_seconds > 0 else None)
    except subprocess.TimeoutExpired:
        proc.kill()
        stdout, stderr = proc.communicate()
        return {
            "table": table_name,
            "status": "timed_out",
            "elapsed_seconds": round(time.monotonic() - started, 3),
            "detail": f"VACUUM exceeded timeout of {timeout_seconds}s and was terminated",
            "stdout": stdout.strip(),
            "stderr": stderr.strip(),
        }
    if proc.returncode != 0:
        detail = stderr.strip() or stdout.strip() or f"psql exited with code {proc.returncode}"
        return {
            "table": table_name,
            "status": "failed",
            "elapsed_seconds": round(time.monotonic() - started, 3),
            "detail": detail,
            "stdout": stdout.strip(),
            "stderr": stderr.strip(),
        }
    return {
        "table": table_name,
        "status": "ok",
        "elapsed_seconds": round(time.monotonic() - started, 3),
        "detail": "",
    }


def summarize_vacuum_results(results: list[dict[str, Any]]) -> str:
    if not results:
        return "skipped"
    if all(result.get("status") == "ok" for result in results):
        return "ok"
    if any(result.get("status") == "timed_out" for result in results):
        return "unknown"
    return "failed"


def find_latest_manifest_for_export(*, manifest_dir: pathlib.Path, export_root: pathlib.Path, started_at: float) -> pathlib.Path:
    candidates = [p for p in manifest_dir.glob("session-trajectory-export-*.json") if p.stat().st_mtime >= started_at - 2]
    if not candidates:
        candidates = list(manifest_dir.glob("session-trajectory-export-*.json"))
    matches: list[pathlib.Path] = []
    target_export_root = str(export_root.resolve())
    for path in candidates:
        try:
            payload = json.loads(path.read_text(encoding="utf-8"))
        except Exception:
            continue
        if payload.get("export_root") == target_export_root:
            matches.append(path)
    if not matches:
        raise RuntimeError(f"no manifest matched export_root={target_export_root}")
    return max(matches, key=lambda p: p.stat().st_mtime)


def build_export_cmd(*, repo_root: pathlib.Path, args: argparse.Namespace, session_id_file: pathlib.Path, export_root: pathlib.Path) -> list[str]:
    go_bin = resolve_binary("go", GO_CANDIDATE_PATHS)
    return [
        go_bin,
        "run",
        "./scripts/export_session_trajectories",
        "--pg-dsn",
        args.pg_dsn,
        "--pg-schema",
        args.pg_schema,
        "--session-id-file",
        str(session_id_file),
        "--export-root",
        str(export_root),
        "--manifest-dir",
        str(args.manifest_dir),
        "--page-size",
        str(args.page_size),
        "--connect-timeout",
        args.connect_timeout,
        "--skip-existing",
    ]


def run_export(
    *,
    repo_root: pathlib.Path,
    args: argparse.Namespace,
    env: dict[str, str],
    run_state: dict[str, Any],
    run_state_path: pathlib.Path,
    export_root: pathlib.Path,
    session_id_file: pathlib.Path,
    export_log_path: pathlib.Path,
) -> dict[str, Any]:
    cmd = build_export_cmd(repo_root=repo_root, args=args, session_id_file=session_id_file, export_root=export_root)
    run_state["current_command"] = cmd
    run_state["updated_at"] = utc_now_str()
    write_json(run_state_path, run_state)

    started_at = time.time()
    export_log_path.parent.mkdir(parents=True, exist_ok=True)
    with export_log_path.open("a", encoding="utf-8") as log_handle:
        proc = subprocess.Popen(
            cmd,
            cwd=str(repo_root),
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            bufsize=1,
        )
        run_state["export_pid"] = proc.pid
        write_json(run_state_path, run_state)
        assert proc.stdout is not None
        os.set_blocking(proc.stdout.fileno(), False)
        buffer = ""
        while True:
            ready, _, _ = select.select([proc.stdout], [], [], 1.0)
            if ready:
                chunk = proc.stdout.read()
                if chunk:
                    buffer += chunk
                    while "\n" in buffer:
                        line, buffer = buffer.split("\n", 1)
                        log_handle.write(line + "\n")
                        log_handle.flush()
                        print(line, flush=True)
                        progress = parse_export_progress_line(line)
                        if progress:
                            run_state.setdefault("export_progress", {}).update(progress)
                            run_state["updated_at"] = utc_now_str()
                            write_json(run_state_path, run_state)
            if proc.poll() is not None:
                tail = proc.stdout.read() or ""
                if tail:
                    buffer += tail
                if buffer:
                    for line in buffer.splitlines():
                        log_handle.write(line + "\n")
                        print(line, flush=True)
                        progress = parse_export_progress_line(line)
                        if progress:
                            run_state.setdefault("export_progress", {}).update(progress)
                log_handle.flush()
                break
        proc.stdout.close()
        rc = proc.wait()
    run_state["export_pid"] = None
    run_state["updated_at"] = utc_now_str()
    write_json(run_state_path, run_state)
    if rc != 0:
        raise RuntimeError(f"export command failed ({rc})")

    manifest_path = find_latest_manifest_for_export(
        manifest_dir=args.manifest_dir,
        export_root=export_root,
        started_at=started_at,
    )
    return json.loads(manifest_path.read_text(encoding="utf-8"))


def cycle_progress_hint(run_state: dict[str, Any]) -> str:
    phase = str(run_state.get("phase") or "")
    if phase == "initialized":
        return "0%"
    if phase == "snapshot_materialized":
        export_progress = run_state.get("export_progress", {})
        done = int(export_progress.get("done_sessions") or 0)
        total = int(export_progress.get("total_sessions") or 0)
        if total > 0 and done > 0:
            return f"{round(done / total * 100, 1)}%"
        return "5-10%"
    if phase == "exported":
        return "50-60%"
    if phase in {"request_exports_deleted", "requests_deleted", "aliases_deleted", "sessions_deleted"}:
        return "60-90%"
    if phase == "vacuumed":
        return "90-99%"
    if phase == "completed":
        return "100%"
    return ""


def count_export_root(export_root: pathlib.Path) -> dict[str, int]:
    if not export_root.exists():
        return {"session_dirs": 0, "json_files": 0}
    return {
        "session_dirs": sum(1 for path in export_root.iterdir() if path.is_dir()),
        "json_files": sum(1 for _ in export_root.rglob("*.json")),
    }


def build_run_state(*, run_id: str, cutoff_at: datetime, run_dir: pathlib.Path, export_root: pathlib.Path, session_id_file: pathlib.Path, candidate_file: pathlib.Path, export_log_path: pathlib.Path) -> dict[str, Any]:
    return {
        "run_id": run_id,
        "phase": "initialized",
        "completion_status": "in_progress",
        "started_at": utc_now_str(),
        "updated_at": utc_now_str(),
        "cutoff_at": format_dt(cutoff_at),
        "run_dir": str(run_dir),
        "export_root": str(export_root),
        "candidate_file": str(candidate_file),
        "session_id_file": str(session_id_file),
        "export_log_path": str(export_log_path),
        "counts": {},
        "deleted": {},
        "warnings": [],
        "vacuum": {},
        "export_progress": {},
    }


def record_cursor(
    *,
    work_root: pathlib.Path,
    run_state: dict[str, Any],
    manifest_path: pathlib.Path | None,
    remote_stats: dict[str, Any],
) -> dict[str, Any]:
    records_dir = work_root / "records"
    record_path = records_dir / f"{run_state['run_id']}.json"
    cursor = {
        "recorded_at": utc_now_str(),
        "cursor_type": "live_export_delete",
        "processed_end_time": run_state.get("counts", {}).get("max_last_activity_at"),
        "run_id": run_state["run_id"],
        "cutoff_at": run_state.get("cutoff_at"),
        "min_last_activity_at": run_state.get("counts", {}).get("min_last_activity_at"),
        "max_last_activity_at": run_state.get("counts", {}).get("max_last_activity_at"),
        "counts": {
            key: run_state.get("counts", {}).get(key, 0)
            for key in ("sessions", "aliases", "requests", "request_exports")
        },
        "deleted": run_state.get("deleted", {}),
        "completion_status": run_state.get("completion_status"),
        "manifest_path": str(manifest_path) if manifest_path else None,
        "export_root": run_state.get("export_root"),
        "exported_sessions": run_state.get("export_manifest", {}).get("exported_sessions"),
        "exported_files": run_state.get("export_manifest", {}).get("exported_files"),
        "warnings": run_state.get("warnings", []),
        "record_file": str(record_path),
        "remote_min_last_activity_after_delete": remote_stats.get("remote_min_last_activity_at"),
        "remote_max_last_activity_after_delete": remote_stats.get("remote_max_last_activity_at"),
    }
    write_json(record_path, cursor)
    write_json(work_root / "cursor.json", cursor)
    write_json(work_root / "latest_record.json", cursor)
    return cursor


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
        self.handle.write(f"pid={os.getpid()} acquired_at={utc_now_str()}\n")
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


def collect_summary(*, args: argparse.Namespace, state_path: pathlib.Path, cursor_path: pathlib.Path) -> dict[str, Any]:
    state = load_json_if_exists(state_path) or {}
    cursor = load_json_if_exists(cursor_path) or {}
    run_state: dict[str, Any] | None = None
    run_id = str(state.get("current_run_id") or "").strip()
    if run_id:
        run_state_path = args.work_root / "runs" / run_id / "run-state.json"
        run_state = load_json_if_exists(run_state_path)
    summary = {
        "recorded_at": utc_now_str(),
        "status": state.get("status"),
        "current_stage": state.get("current_stage"),
        "run_id": run_id or cursor.get("run_id"),
        "cursor_processed_end_time": cursor.get("processed_end_time"),
        "cursor_type": cursor.get("cursor_type"),
        "remote_stats": state.get("remote_stats"),
        "last_completed_cursor": state.get("last_completed_cursor"),
    }
    if run_state:
        export_root_counts = count_export_root(pathlib.Path(str(run_state.get("export_root") or "")))
        summary["run_state"] = {
            "phase": run_state.get("phase"),
            "completion_status": run_state.get("completion_status"),
            "counts": run_state.get("counts", {}),
            "deleted": run_state.get("deleted", {}),
            "export_progress": run_state.get("export_progress", {}),
            "export_root_counts": export_root_counts,
            "updated_at": run_state.get("updated_at"),
        }
        progress_hint = cycle_progress_hint(run_state)
        total_sessions = int(run_state.get("export_progress", {}).get("matched_sessions") or run_state.get("counts", {}).get("sessions") or 0)
        session_dirs = int(export_root_counts["session_dirs"])
        if progress_hint == "5-10%" and total_sessions > 0 and session_dirs > 0:
            progress_hint = f"{round(min(session_dirs, total_sessions) / total_sessions * 100, 1)}%"
        summary["progress_hint"] = progress_hint
        summary["export_root"] = run_state.get("export_root")
        summary["candidate_file"] = run_state.get("candidate_file")
    return summary


def refresh_summary(*, args: argparse.Namespace, state_path: pathlib.Path, cursor_path: pathlib.Path, summary_path: pathlib.Path) -> dict[str, Any]:
    summary = collect_summary(args=args, state_path=state_path, cursor_path=cursor_path)
    write_json(summary_path, summary)
    return summary


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mode", choices=("run", "status"), default="run")
    parser.add_argument("--pg-dsn", required=True)
    parser.add_argument("--pg-schema", default="public")
    parser.add_argument("--inactive-hours", type=int, default=24)
    parser.add_argument("--batch-size", type=int, default=500)
    parser.add_argument("--page-size", type=int, default=100)
    parser.add_argument("--connect-timeout", default="30s")
    parser.add_argument("--work-root", type=pathlib.Path, default=pathlib.Path.home() / "CLIProxyAPI-session-live-export-loop")
    parser.add_argument("--export-root-base", type=pathlib.Path, default=pathlib.Path.home() / "session-trajectory-export-live-delete")
    parser.add_argument("--manifest-dir", type=pathlib.Path, default=pathlib.Path.home() / "session-trajectory-export-manifests-direct")
    parser.add_argument("--state-path", type=pathlib.Path)
    parser.add_argument("--cursor-path", type=pathlib.Path)
    parser.add_argument("--summary-path", type=pathlib.Path)
    parser.add_argument("--check-interval-seconds", type=int, default=600)
    parser.add_argument("--failure-sleep-seconds", type=int, default=300)
    parser.add_argument("--summary-interval-seconds", type=int, default=15)
    parser.add_argument("--skip-vacuum", action="store_true")
    parser.add_argument("--vacuum-timeout-seconds", type=int, default=600)
    parser.add_argument("--label", default="com.codex.live_export_delete_loop")
    parser.add_argument("--lock-path", type=pathlib.Path, default=pathlib.Path("/tmp/cliproxy-live-export-delete-loop.lock"))
    return parser.parse_args(argv)


def run_cycle(*, args: argparse.Namespace, repo_root: pathlib.Path, env: dict[str, str], state: dict[str, Any], state_path: pathlib.Path, cursor_path: pathlib.Path, summary_path: pathlib.Path, run_id: str, stop_requested: dict[str, bool]) -> dict[str, Any]:
    run_dir = args.work_root / "runs" / run_id
    candidate_file = run_dir / "candidate_sessions.csv"
    session_id_file = run_dir / "session_ids.txt"
    export_log_path = run_dir / "export.log"
    run_state_path = run_dir / "run-state.json"
    export_root = args.export_root_base / run_id
    run_state = load_json_if_exists(run_state_path)
    if run_state is None:
        cutoff_at = utc_now() - timedelta(hours=args.inactive_hours)
        run_state = build_run_state(
            run_id=run_id,
            cutoff_at=cutoff_at,
            run_dir=run_dir,
            export_root=export_root,
            session_id_file=session_id_file,
            candidate_file=candidate_file,
            export_log_path=export_log_path,
        )
        write_json(run_state_path, run_state)
    else:
        cutoff_at = parse_dt(str(run_state["cutoff_at"]))
        export_root = pathlib.Path(str(run_state["export_root"]))

    def persist_run_state() -> None:
        run_state["updated_at"] = utc_now_str()
        write_json(run_state_path, run_state)
        refresh_summary(args=args, state_path=state_path, cursor_path=cursor_path, summary_path=summary_path)

    if not phase_at_least(str(run_state["phase"]), "snapshot_materialized"):
        snapshot = snapshot_candidates(
            dsn=args.pg_dsn,
            schema=args.pg_schema,
            cutoff_at=cutoff_at,
            candidate_file=candidate_file,
            session_id_file=session_id_file,
            env=env,
        )
        run_state["phase"] = "snapshot_materialized"
        run_state["counts"] = {
            "sessions": snapshot["candidate_sessions"],
            "aliases": 0,
            "requests": 0,
            "request_exports": 0,
            "min_last_activity_at": snapshot["min_last_activity_at"],
            "max_last_activity_at": snapshot["max_last_activity_at"],
        }
        persist_run_state()
        print(
            f"[progress] snapshot sessions={snapshot['candidate_sessions']} min={snapshot['min_last_activity_at']} max={snapshot['max_last_activity_at']}",
            flush=True,
        )

    if not phase_at_least(str(run_state["phase"]), "exported"):
        pruned = prune_reactivated_candidates(
            dsn=args.pg_dsn,
            schema=args.pg_schema,
            cutoff_at=cutoff_at,
            candidate_file=candidate_file,
            session_id_file=session_id_file,
            env=env,
        )
        if pruned["pruned_sessions"] > 0:
            append_warning(
                run_state,
                code="reactivated_candidates_pruned_before_export",
                message="Reactivated sessions were pruned before direct export",
                detail=f"pruned_sessions={pruned['pruned_sessions']}",
            )
            print(f"[warn] pruned reactivated sessions before export: {pruned['pruned_sessions']}", flush=True)
        if pruned["candidate_sessions"] == 0:
            run_state["counts"] = {
                "sessions": 0,
                "aliases": 0,
                "requests": 0,
                "request_exports": 0,
                "min_last_activity_at": "",
                "max_last_activity_at": "",
            }
            run_state["vacuum"] = {"summary": "skipped", "tables": {}, "results": []}
            run_state["phase"] = "completed"
            finalize_completion(run_state)
            persist_run_state()
            return run_state
        counts = query_target_counts(dsn=args.pg_dsn, schema=args.pg_schema, candidate_file=candidate_file, env=env)
        run_state["counts"].update(counts)
        persist_run_state()
        manifest = run_export(
            repo_root=repo_root,
            args=args,
            env=env,
            run_state=run_state,
            run_state_path=run_state_path,
            export_root=export_root,
            session_id_file=session_id_file,
            export_log_path=export_log_path,
        )
        run_state["export_manifest"] = {
            "manifest_path": manifest.get("manifest_path"),
            "export_root": manifest.get("export_root"),
            "exported_sessions": manifest.get("exported_sessions"),
            "exported_files": manifest.get("exported_files"),
            "filters": manifest.get("filters", {}),
        }
        run_state["phase"] = "exported"
        persist_run_state()
        print("[progress] direct export finished and verified", flush=True)

    if not phase_at_least(str(run_state["phase"]), "request_exports_deleted"):
        pruned = prune_reactivated_candidates(
            dsn=args.pg_dsn,
            schema=args.pg_schema,
            cutoff_at=cutoff_at,
            candidate_file=candidate_file,
            session_id_file=session_id_file,
            env=env,
        )
        if pruned["pruned_sessions"] > 0:
            append_warning(
                run_state,
                code="reactivated_candidates_skipped_at_delete",
                message="Some exported sessions reactivated before delete and were skipped from remote delete",
                detail=f"pruned_sessions={pruned['pruned_sessions']}",
            )
            print(f"[warn] skipped reactivated sessions at delete: {pruned['pruned_sessions']}", flush=True)
        counts = query_target_counts(dsn=args.pg_dsn, schema=args.pg_schema, candidate_file=candidate_file, env=env)
        run_state["counts"].update(counts)
        persist_run_state()
        if int(counts["sessions"]) == 0:
            run_state["deleted"] = {"sessions": 0, "aliases": 0, "requests": 0, "request_exports": 0}
            run_state["vacuum"] = {"summary": "skipped", "tables": {}, "results": []}
            run_state["phase"] = "completed"
            finalize_completion(run_state)
            persist_run_state()
            return run_state

        schema = quote_ident(args.pg_schema)
        cutoff_literal = quote_literal(format_dt(cutoff_at))
        request_exports_sql = lambda: f"""
WITH doomed AS (
  SELECT e.request_id
  FROM {schema}.session_trajectory_request_exports e
  JOIN live_export_sessions a ON a.session_id = e.session_id
  JOIN {schema}.session_trajectory_sessions s ON s.id = e.session_id
  WHERE s.last_activity_at < {cutoff_literal}::timestamptz
  LIMIT {args.batch_size}
),
deleted AS (
  DELETE FROM {schema}.session_trajectory_request_exports e
  USING doomed d
  WHERE e.request_id = d.request_id
  RETURNING 1
)
SELECT count(*) FROM deleted;
""".strip()
        requests_sql = lambda: f"""
WITH doomed AS (
  SELECT r.id
  FROM {schema}.session_trajectory_requests r
  JOIN live_export_sessions a ON a.session_id = r.session_id
  JOIN {schema}.session_trajectory_sessions s ON s.id = r.session_id
  WHERE s.last_activity_at < {cutoff_literal}::timestamptz
  LIMIT {args.batch_size}
),
deleted AS (
  DELETE FROM {schema}.session_trajectory_requests r
  USING doomed d
  WHERE r.id = d.id
  RETURNING 1
)
SELECT count(*) FROM deleted;
""".strip()
        aliases_sql = lambda: f"""
WITH doomed AS (
  SELECT sa.ctid
  FROM {schema}.session_trajectory_session_aliases sa
  JOIN live_export_sessions a ON a.session_id = sa.session_id
  JOIN {schema}.session_trajectory_sessions s ON s.id = sa.session_id
  WHERE s.last_activity_at < {cutoff_literal}::timestamptz
  LIMIT {args.batch_size}
),
deleted AS (
  DELETE FROM {schema}.session_trajectory_session_aliases sa
  USING doomed d
  WHERE sa.ctid = d.ctid
  RETURNING 1
)
SELECT count(*) FROM deleted;
""".strip()
        sessions_sql = lambda: f"""
WITH doomed AS (
  SELECT s.id
  FROM {schema}.session_trajectory_sessions s
  JOIN live_export_sessions a ON a.session_id = s.id
  WHERE s.last_activity_at < {cutoff_literal}::timestamptz
  LIMIT {args.batch_size}
),
deleted AS (
  DELETE FROM {schema}.session_trajectory_sessions s
  USING doomed d
  WHERE s.id = d.id
  RETURNING 1
)
SELECT count(*) FROM deleted;
""".strip()

        total = delete_in_batches(
            dsn=args.pg_dsn,
            candidate_file=candidate_file,
            run_state=run_state,
            run_state_path=run_state_path,
            stage_name="request_exports",
            sql_factory=request_exports_sql,
            env=env,
        )
        run_state["phase"] = "request_exports_deleted"
        run_state.setdefault("deleted", {})["request_exports"] = total
        persist_run_state()
        total = delete_in_batches(
            dsn=args.pg_dsn,
            candidate_file=candidate_file,
            run_state=run_state,
            run_state_path=run_state_path,
            stage_name="requests",
            sql_factory=requests_sql,
            env=env,
        )
        run_state["phase"] = "requests_deleted"
        run_state.setdefault("deleted", {})["requests"] = total
        persist_run_state()
        total = delete_in_batches(
            dsn=args.pg_dsn,
            candidate_file=candidate_file,
            run_state=run_state,
            run_state_path=run_state_path,
            stage_name="aliases",
            sql_factory=aliases_sql,
            env=env,
        )
        run_state["phase"] = "aliases_deleted"
        run_state.setdefault("deleted", {})["aliases"] = total
        persist_run_state()
        total = delete_in_batches(
            dsn=args.pg_dsn,
            candidate_file=candidate_file,
            run_state=run_state,
            run_state_path=run_state_path,
            stage_name="sessions",
            sql_factory=sessions_sql,
            env=env,
        )
        run_state["phase"] = "sessions_deleted"
        run_state.setdefault("deleted", {})["sessions"] = total
        persist_run_state()

    if not args.skip_vacuum and not phase_at_least(str(run_state["phase"]), "vacuumed"):
        vacuum_results: list[dict[str, Any]] = []
        vacuum_tables: dict[str, str] = {}
        for table_name in (
            "session_trajectory_request_exports",
            "session_trajectory_requests",
            "session_trajectory_session_aliases",
            "session_trajectory_sessions",
        ):
            result = vacuum_table(
                dsn=args.pg_dsn,
                schema=args.pg_schema,
                table_name=table_name,
                timeout_seconds=args.vacuum_timeout_seconds,
                env=env,
            )
            vacuum_results.append(result)
            vacuum_tables[table_name] = result["status"]
            if result["status"] == "ok":
                print(f"[progress] vacuum {table_name} finished in {result['elapsed_seconds']}s", flush=True)
            else:
                append_warning(
                    run_state,
                    code="vacuum_warning",
                    message=f"VACUUM for {table_name} did not confirm success",
                    detail=result["detail"],
                )
                print(f"[warn] vacuum {table_name} {result['status']}: {result['detail']}", flush=True)
        run_state["vacuum"] = {
            "summary": summarize_vacuum_results(vacuum_results),
            "timeout_seconds": args.vacuum_timeout_seconds,
            "tables": vacuum_tables,
            "results": vacuum_results,
        }
        if run_state["vacuum"]["summary"] == "ok":
            run_state["phase"] = "vacuumed"
        persist_run_state()
    elif args.skip_vacuum and not run_state.get("vacuum"):
        run_state["vacuum"] = {"summary": "skipped", "tables": {}, "results": []}

    run_state["phase"] = "completed"
    finalize_completion(run_state)
    persist_run_state()
    return run_state


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    repo_root = pathlib.Path(__file__).resolve().parents[1]
    env = build_env()
    args.work_root = args.work_root.expanduser().resolve()
    args.export_root_base = args.export_root_base.expanduser().resolve()
    args.manifest_dir = args.manifest_dir.expanduser().resolve()
    state_path = (args.state_path.expanduser().resolve() if args.state_path else args.work_root / "state.json")
    cursor_path = (args.cursor_path.expanduser().resolve() if args.cursor_path else args.work_root / "cursor.json")
    summary_path = (args.summary_path.expanduser().resolve() if args.summary_path else args.work_root / "summary.json")
    args.work_root.mkdir(parents=True, exist_ok=True)
    args.export_root_base.mkdir(parents=True, exist_ok=True)
    args.manifest_dir.mkdir(parents=True, exist_ok=True)

    if args.mode == "status":
        summary = refresh_summary(args=args, state_path=state_path, cursor_path=cursor_path, summary_path=summary_path)
        print(json.dumps(summary, indent=2, sort_keys=True))
        return 0

    state = load_json_if_exists(state_path) or {
        "status": "running",
        "current_stage": "initializing",
        "started_at": utc_now_str(),
        "updated_at": utc_now_str(),
        "label": args.label,
        "work_root": str(args.work_root),
        "export_root_base": str(args.export_root_base),
        "manifest_dir": str(args.manifest_dir),
    }
    write_json(state_path, state)

    stop_requested = {"value": False}

    def on_signal(signum: int, _frame: Any) -> None:
        stop_requested["value"] = True
        state["status"] = "interrupted"
        state["current_stage"] = "interrupted"
        state["updated_at"] = utc_now_str()
        state["signal"] = signal.Signals(signum).name
        write_json(state_path, state)

    signal.signal(signal.SIGTERM, on_signal)
    signal.signal(signal.SIGINT, on_signal)
    if hasattr(signal, "SIGHUP"):
        signal.signal(signal.SIGHUP, on_signal)

    refresh_summary(args=args, state_path=state_path, cursor_path=cursor_path, summary_path=summary_path)

    with LoopLock(args.lock_path.expanduser().resolve()):
        while not stop_requested["value"]:
            current_run_id = str(state.get("current_run_id") or "").strip()
            if current_run_id:
                run_state_path = args.work_root / "runs" / current_run_id / "run-state.json"
                run_state = load_json_if_exists(run_state_path)
                if run_state and not phase_at_least(str(run_state.get("phase") or ""), "completed"):
                    state["status"] = "running"
                    state["current_stage"] = "resuming_cycle"
                    state["updated_at"] = utc_now_str()
                    write_json(state_path, state)
                    run_state = run_cycle(
                        args=args,
                        repo_root=repo_root,
                        env=env,
                        state=state,
                        state_path=state_path,
                        cursor_path=cursor_path,
                        summary_path=summary_path,
                        run_id=current_run_id,
                        stop_requested=stop_requested,
                    )
                    remote_stats = query_remote_stats(dsn=args.pg_dsn, schema=args.pg_schema, inactive_hours=args.inactive_hours, env=env)
                    cursor = record_cursor(
                        work_root=args.work_root,
                        run_state=run_state,
                        manifest_path=pathlib.Path(run_state.get("export_manifest", {}).get("manifest_path", "")) if run_state.get("export_manifest", {}).get("manifest_path") else None,
                        remote_stats=remote_stats,
                    )
                    state["last_completed_cursor"] = cursor
                    state["current_run_id"] = None
                    state["status"] = "running"
                    state["current_stage"] = "cycle_completed"
                    state["remote_stats"] = remote_stats
                    state["updated_at"] = utc_now_str()
                    write_json(state_path, state)
                    refresh_summary(args=args, state_path=state_path, cursor_path=cursor_path, summary_path=summary_path)
                    if stop_requested["value"]:
                        break
                else:
                    state["current_run_id"] = None
                    write_json(state_path, state)

            remote_stats = query_remote_stats(dsn=args.pg_dsn, schema=args.pg_schema, inactive_hours=args.inactive_hours, env=env)
            state["remote_stats"] = remote_stats
            state["updated_at"] = utc_now_str()
            if int(remote_stats["eligible_sessions"]) == 0:
                state["status"] = "running"
                state["current_stage"] = "idle_waiting_for_cold_data"
                write_json(state_path, state)
                refresh_summary(args=args, state_path=state_path, cursor_path=cursor_path, summary_path=summary_path)
                time.sleep(max(5, args.check_interval_seconds))
                continue

            run_id = build_run_id(utc_now())
            state["current_run_id"] = run_id
            state["status"] = "running"
            state["current_stage"] = "cycle_running"
            state["cycle_window_hint"] = {
                "eligible_min_last_activity_at": remote_stats.get("eligible_min_last_activity_at"),
                "eligible_max_last_activity_at": remote_stats.get("eligible_max_last_activity_at"),
            }
            state["updated_at"] = utc_now_str()
            write_json(state_path, state)
            refresh_summary(args=args, state_path=state_path, cursor_path=cursor_path, summary_path=summary_path)

            try:
                run_state = run_cycle(
                    args=args,
                    repo_root=repo_root,
                    env=env,
                    state=state,
                    state_path=state_path,
                    cursor_path=cursor_path,
                    summary_path=summary_path,
                    run_id=run_id,
                    stop_requested=stop_requested,
                )
            except Exception as exc:
                state["status"] = "running"
                state["current_stage"] = "cycle_failed_waiting_retry"
                state["error"] = str(exc)
                state["updated_at"] = utc_now_str()
                write_json(state_path, state)
                refresh_summary(args=args, state_path=state_path, cursor_path=cursor_path, summary_path=summary_path)
                time.sleep(max(5, args.failure_sleep_seconds))
                continue

            remote_after_delete = query_remote_stats(dsn=args.pg_dsn, schema=args.pg_schema, inactive_hours=args.inactive_hours, env=env)
            cursor = record_cursor(
                work_root=args.work_root,
                run_state=run_state,
                manifest_path=pathlib.Path(run_state.get("export_manifest", {}).get("manifest_path", "")) if run_state.get("export_manifest", {}).get("manifest_path") else None,
                remote_stats=remote_after_delete,
            )
            state["last_completed_cursor"] = cursor
            state["current_run_id"] = None
            state["status"] = "running"
            state["current_stage"] = "idle_waiting_for_cold_data"
            state["remote_stats"] = remote_after_delete
            state["updated_at"] = utc_now_str()
            write_json(state_path, state)
            refresh_summary(args=args, state_path=state_path, cursor_path=cursor_path, summary_path=summary_path)
            if int(remote_after_delete.get("eligible_sessions") or 0) > 0:
                time.sleep(2)
                continue
            time.sleep(max(5, args.check_interval_seconds))

    refresh_summary(args=args, state_path=state_path, cursor_path=cursor_path, summary_path=summary_path)
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
