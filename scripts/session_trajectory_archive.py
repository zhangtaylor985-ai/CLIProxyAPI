#!/usr/bin/env python3
"""Archive and prune cold session trajectory data from PostgreSQL.

The script exports complete, inactive sessions to local compressed CSV files,
then removes those sessions from PostgreSQL in dependency order. Each run keeps
its own state file so the same run can be resumed safely with --run-id.
"""

from __future__ import annotations

import argparse
import csv
import gzip
import json
import os
import pathlib
import shutil
import subprocess
import sys
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
from typing import Any


PHASES = [
    "initialized",
    "candidates_materialized",
    "exported",
    "request_exports_deleted",
    "requests_deleted",
    "aliases_deleted",
    "sessions_deleted",
    "vacuumed",
    "completed",
]


def utc_now() -> datetime:
    return datetime.now(timezone.utc).replace(microsecond=0)


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


@dataclass
class RunState:
    run_id: str
    schema: str
    inactive_hours: int
    cutoff_at: datetime
    output_dir: pathlib.Path
    phase: str = "initialized"
    started_at: datetime = field(default_factory=utc_now)
    updated_at: datetime = field(default_factory=utc_now)
    cursor: dict[str, Any] = field(default_factory=dict)
    counts: dict[str, int] = field(default_factory=dict)
    deleted: dict[str, int] = field(default_factory=dict)
    files: dict[str, str] = field(default_factory=dict)

    def to_dict(self) -> dict[str, Any]:
        return {
            "run_id": self.run_id,
            "schema": self.schema,
            "inactive_hours": self.inactive_hours,
            "cutoff_at": format_dt(self.cutoff_at),
            "phase": self.phase,
            "started_at": format_dt(self.started_at),
            "updated_at": format_dt(self.updated_at),
            "output_dir": str(self.output_dir),
            "cursor": self.cursor,
            "counts": self.counts,
            "deleted": self.deleted,
            "files": self.files,
        }

    @classmethod
    def from_dict(cls, payload: dict[str, Any]) -> "RunState":
        return cls(
            run_id=payload["run_id"],
            schema=payload["schema"],
            inactive_hours=int(payload["inactive_hours"]),
            cutoff_at=parse_dt(payload["cutoff_at"]),
            output_dir=pathlib.Path(payload["output_dir"]),
            phase=payload.get("phase", "initialized"),
            started_at=parse_dt(payload["started_at"]),
            updated_at=parse_dt(payload["updated_at"]),
            cursor=dict(payload.get("cursor", {})),
            counts={k: int(v) for k, v in payload.get("counts", {}).items()},
            deleted={k: int(v) for k, v in payload.get("deleted", {}).items()},
            files=dict(payload.get("files", {})),
        )


def write_run_state(path: pathlib.Path, state: RunState) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    payload = state.to_dict()
    payload["updated_at"] = format_dt(utc_now())
    temp_path = path.with_suffix(".tmp")
    temp_path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    temp_path.replace(path)


def read_run_state(path: pathlib.Path) -> RunState:
    return RunState.from_dict(json.loads(path.read_text(encoding="utf-8")))


def count_csv_rows(path: pathlib.Path) -> int:
    with path.open("r", encoding="utf-8", newline="") as handle:
        reader = csv.reader(handle)
        try:
            next(reader)
        except StopIteration:
            return 0
        return sum(1 for _ in reader)


def count_plain_rows(path: pathlib.Path) -> int:
    with path.open("r", encoding="utf-8", newline="") as handle:
        return sum(1 for _ in handle)


def count_gzip_text_rows(path: pathlib.Path) -> int:
    with gzip.open(path, "rt", encoding="utf-8", newline="") as handle:
        return sum(1 for _ in handle)


def gzip_file(path: pathlib.Path, *, remove_source: bool = True) -> pathlib.Path:
    gz_path = path.with_suffix(path.suffix + ".gz")
    with path.open("rb") as src, gzip.open(gz_path, "wb") as dst:
        shutil.copyfileobj(src, dst)
    if remove_source:
        path.unlink()
    return gz_path


def build_run_id(now: datetime) -> str:
    return "session-archive-" + now.astimezone(timezone.utc).strftime("%Y%m%dT%H%M%SZ")


def require_binary(name: str) -> None:
    if shutil.which(name) is None:
        raise SystemExit(f"required binary not found in PATH: {name}")


def run_command(cmd: list[str], *, input_text: str | None = None) -> str:
    proc = subprocess.run(
        cmd,
        input=input_text,
        text=True,
        capture_output=True,
        check=False,
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
    return ["psql", dsn, "-q", "-X", "-v", "ON_ERROR_STOP=1", "-P", "pager=off"]


def run_psql_sql(dsn: str, sql: str) -> str:
    cmd = psql_base_cmd(dsn) + ["-At", "-F", "\t", "-c", sql]
    return run_command(cmd).strip()


def run_psql_script(dsn: str, script: str) -> str:
    cmd = psql_base_cmd(dsn) + ["-f", "-"]
    return run_command(cmd, input_text=script)


def export_candidate_sessions(dsn: str, state: RunState, candidate_file: pathlib.Path) -> int:
    sessions_table = f"{quote_ident(state.schema)}.{quote_ident('session_trajectory_sessions')}"
    cutoff_literal = quote_literal(state.cutoff_at.isoformat())
    candidate_literal = quote_literal(str(candidate_file))
    query = one_line_sql(
        f"""
        SELECT id, last_activity_at
        FROM {sessions_table}
        WHERE last_activity_at < {cutoff_literal}::timestamptz
        ORDER BY last_activity_at ASC, id ASC
        """
    )
    script = f"""
\\copy ({query}) TO {candidate_literal} WITH (FORMAT csv)
"""
    run_psql_script(dsn, script)
    count = count_plain_rows(candidate_file)
    return count


def query_target_counts(dsn: str, state: RunState, candidate_file: pathlib.Path) -> dict[str, int]:
    schema = quote_ident(state.schema)
    file_literal = quote_literal(str(candidate_file))
    script = f"""
\\pset tuples_only on
\\pset format unaligned
\\f '\\t'
CREATE TEMP TABLE archive_sessions (
  session_id uuid PRIMARY KEY,
  last_activity_at timestamptz NOT NULL
);
\\copy archive_sessions (session_id, last_activity_at) FROM {file_literal} WITH (FORMAT csv)
SELECT
  (SELECT count(*) FROM {schema}.session_trajectory_sessions s JOIN archive_sessions a ON a.session_id = s.id),
  (SELECT count(*) FROM {schema}.session_trajectory_session_aliases sa JOIN archive_sessions a ON a.session_id = sa.session_id),
  (SELECT count(*) FROM {schema}.session_trajectory_requests r JOIN archive_sessions a ON a.session_id = r.session_id),
  (SELECT count(*) FROM {schema}.session_trajectory_request_exports e JOIN archive_sessions a ON a.session_id = e.session_id),
  COALESCE((SELECT to_char(max(last_activity_at) AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM archive_sessions), ''),
  COALESCE((SELECT to_char(min(last_activity_at) AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM archive_sessions), '');
"""
    line = run_psql_script(dsn, script).strip().splitlines()[-1]
    sessions, aliases, requests, request_exports, max_ts, min_ts = line.split("\t")
    return {
        "sessions": int(sessions),
        "aliases": int(aliases),
        "requests": int(requests),
        "request_exports": int(request_exports),
        "max_last_activity_at": max_ts,
        "min_last_activity_at": min_ts,
    }


def export_table_to_csv(
    dsn: str,
    *,
    candidate_file: pathlib.Path,
    output_file: pathlib.Path,
    query: str,
) -> int:
    file_literal = quote_literal(str(candidate_file))
    flat_query = one_line_sql(query)
    script = f"""
CREATE TEMP TABLE archive_sessions (
  session_id uuid PRIMARY KEY,
  last_activity_at timestamptz NOT NULL
);
\\copy archive_sessions (session_id, last_activity_at) FROM {file_literal} WITH (FORMAT csv)
COPY (SELECT row_to_json(row_payload)::text FROM ({flat_query}) AS row_payload) TO STDOUT;
"""
    cmd = psql_base_cmd(dsn) + ["-At", "-f", "-"]
    with gzip.open(output_file, "wb") as gz_handle:
        proc = subprocess.Popen(cmd, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        assert proc.stdin is not None
        assert proc.stdout is not None
        proc.stdin.write(script.encode("utf-8"))
        proc.stdin.close()
        shutil.copyfileobj(proc.stdout, gz_handle)
        proc.stdout.close()
        stderr = proc.stderr.read().decode("utf-8", errors="replace") if proc.stderr is not None else ""
        return_code = proc.wait()
    if return_code != 0:
        raise RuntimeError(
            "command failed\n"
            f"cmd: {' '.join(cmd)}\n"
            f"stderr:\n{stderr}"
        )
    return count_gzip_text_rows(output_file)


def delete_batch(
    dsn: str,
    *,
    state: RunState,
    candidate_file: pathlib.Path,
    sql: str,
) -> int:
    file_literal = quote_literal(str(candidate_file))
    script = f"""
\\pset tuples_only on
\\pset format unaligned
CREATE TEMP TABLE archive_sessions (
  session_id uuid PRIMARY KEY,
  last_activity_at timestamptz NOT NULL
);
\\copy archive_sessions (session_id, last_activity_at) FROM {file_literal} WITH (FORMAT csv)
{sql}
"""
    output = run_psql_script(dsn, script).strip().splitlines()
    if not output:
        return 0
    return int(output[-1])


def delete_in_batches(
    dsn: str,
    *,
    state: RunState,
    state_path: pathlib.Path,
    candidate_file: pathlib.Path,
    stage_name: str,
    sql_factory,
) -> int:
    total_deleted = int(state.deleted.get(stage_name, 0))
    batches = 0
    while True:
        deleted = delete_batch(
            dsn,
            state=state,
            candidate_file=candidate_file,
            sql=sql_factory(),
        )
        if deleted == 0:
            break
        total_deleted += deleted
        batches += 1
        state.deleted[stage_name] = total_deleted
        state.updated_at = utc_now()
        write_run_state(state_path, state)
        if batches % 10 == 0:
            print(f"[progress] {stage_name}: deleted {total_deleted} rows after {batches} batches", flush=True)
    return total_deleted


def vacuum_table(dsn: str, schema: str, table_name: str) -> None:
    target = f"{quote_ident(schema)}.{quote_ident(table_name)}"
    run_psql_sql(dsn, f"VACUUM (ANALYZE) {target};")


def write_latest_completed(output_root: pathlib.Path, state: RunState) -> None:
    latest_path = output_root / "latest_completed.json"
    latest_payload = {
        "run_id": state.run_id,
        "schema": state.schema,
        "inactive_hours": state.inactive_hours,
        "cutoff_at": format_dt(state.cutoff_at),
        "phase": state.phase,
        "output_dir": str(state.output_dir),
        "cursor": state.cursor,
        "counts": state.counts,
        "deleted": state.deleted,
        "updated_at": format_dt(utc_now()),
    }
    temp_path = latest_path.with_suffix(".tmp")
    temp_path.write_text(json.dumps(latest_payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    temp_path.replace(latest_path)


def resolve_state(args: argparse.Namespace) -> tuple[RunState, pathlib.Path]:
    output_root = args.output_root.resolve()
    runs_dir = output_root / "runs"
    if args.run_id:
        state_path = runs_dir / args.run_id / "run-state.json"
        if not state_path.exists():
            raise SystemExit(f"run state not found: {state_path}")
        return read_run_state(state_path), state_path

    run_started_at = utc_now()
    run_id = build_run_id(run_started_at)
    output_dir = runs_dir / run_id
    cutoff_at = run_started_at - timedelta(hours=args.inactive_hours)
    state = RunState(
        run_id=run_id,
        schema=args.schema,
        inactive_hours=args.inactive_hours,
        cutoff_at=cutoff_at,
        output_dir=output_dir,
    )
    state_path = output_dir / "run-state.json"
    return state, state_path


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--schema", default="public", help="session trajectory schema")
    parser.add_argument("--inactive-hours", type=int, default=24, help="archive sessions inactive longer than this")
    parser.add_argument("--batch-size", type=int, default=500, help="delete batch size per statement")
    parser.add_argument(
        "--output-root",
        type=pathlib.Path,
        default=pathlib.Path("/Volumes/Storage/CLIProxyAPI-session-archives"),
        help="root directory for local exports and state files",
    )
    parser.add_argument("--run-id", help="resume an existing run by run id")
    parser.add_argument("--dsn-env", default="APIKEY_POLICY_PG_DSN", help="environment variable holding the PostgreSQL DSN")
    parser.add_argument("--skip-vacuum", action="store_true", help="skip VACUUM (ANALYZE) after deletion")
    return parser.parse_args(argv)


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    require_binary("psql")
    dsn = os.environ.get(args.dsn_env, "").strip()
    if not dsn:
        raise SystemExit(f"environment variable {args.dsn_env} is required")

    state, state_path = resolve_state(args)
    state.output_dir.mkdir(parents=True, exist_ok=True)
    write_run_state(state_path, state)

    candidate_file = state.output_dir / "candidate_sessions.csv"
    export_paths = {
        "sessions": state.output_dir / "session_trajectory_sessions.jsonl.gz",
        "aliases": state.output_dir / "session_trajectory_session_aliases.jsonl.gz",
        "requests": state.output_dir / "session_trajectory_requests.jsonl.gz",
        "request_exports": state.output_dir / "session_trajectory_request_exports.jsonl.gz",
    }

    schema = quote_ident(state.schema)

    print(
        f"[start] run_id={state.run_id} schema={state.schema} cutoff_at={format_dt(state.cutoff_at)} output_dir={state.output_dir}",
        flush=True,
    )

    if not phase_at_least(state.phase, "candidates_materialized"):
        candidate_count = export_candidate_sessions(dsn, state, candidate_file)
        state.phase = "candidates_materialized"
        state.cursor["candidate_file"] = str(candidate_file)
        state.cursor["candidate_sessions"] = candidate_count
        if candidate_count == 0:
            state.counts = {"sessions": 0, "aliases": 0, "requests": 0, "request_exports": 0}
            state.phase = "completed"
            state.updated_at = utc_now()
            write_run_state(state_path, state)
            write_latest_completed(args.output_root.resolve(), state)
            print("[done] no eligible sessions found", flush=True)
            return 0
        counts = query_target_counts(dsn, state, candidate_file)
        state.counts.update({k: int(v) for k, v in counts.items() if k in {"sessions", "aliases", "requests", "request_exports"}})
        state.cursor["min_last_activity_at"] = counts["min_last_activity_at"]
        state.cursor["max_last_activity_at"] = counts["max_last_activity_at"]
        state.updated_at = utc_now()
        write_run_state(state_path, state)
        print(
            "[progress] candidates sessions={sessions} requests={requests} aliases={aliases} request_exports={request_exports}".format(
                **state.counts
            ),
            flush=True,
        )

    if not phase_at_least(state.phase, "exported"):
        exported_counts = {
            "sessions": export_table_to_csv(
                dsn,
                candidate_file=candidate_file,
                output_file=export_paths["sessions"],
                query=f"""
  SELECT s.*
  FROM {schema}.session_trajectory_sessions s
  JOIN archive_sessions a ON a.session_id = s.id
  ORDER BY s.last_activity_at ASC, s.id ASC
""".strip(),
            ),
            "aliases": export_table_to_csv(
                dsn,
                candidate_file=candidate_file,
                output_file=export_paths["aliases"],
                query=f"""
  SELECT sa.*
  FROM {schema}.session_trajectory_session_aliases sa
  JOIN archive_sessions a ON a.session_id = sa.session_id
  ORDER BY sa.session_id ASC, sa.provider_session_id ASC
""".strip(),
            ),
            "requests": export_table_to_csv(
                dsn,
                candidate_file=candidate_file,
                output_file=export_paths["requests"],
                query=f"""
  SELECT r.*
  FROM {schema}.session_trajectory_requests r
  JOIN archive_sessions a ON a.session_id = r.session_id
  ORDER BY r.session_id ASC, r.request_index ASC
""".strip(),
            ),
            "request_exports": export_table_to_csv(
                dsn,
                candidate_file=candidate_file,
                output_file=export_paths["request_exports"],
                query=f"""
  SELECT e.*
  FROM {schema}.session_trajectory_request_exports e
  JOIN archive_sessions a ON a.session_id = e.session_id
  ORDER BY e.session_id ASC, e.export_index ASC, e.request_id ASC
""".strip(),
            ),
        }
        for key, expected in state.counts.items():
            if key not in exported_counts:
                continue
            if exported_counts[key] != expected:
                raise RuntimeError(f"exported row mismatch for {key}: expected {expected}, got {exported_counts[key]}")
        for key, path in export_paths.items():
            state.files[key] = str(path)
        state.phase = "exported"
        state.updated_at = utc_now()
        write_run_state(state_path, state)
        print("[progress] exports finished and verified", flush=True)

    request_exports_sql = lambda: f"""
WITH doomed AS (
  SELECT e.request_id
  FROM {schema}.session_trajectory_request_exports e
  JOIN archive_sessions a ON a.session_id = e.session_id
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
  JOIN archive_sessions a ON a.session_id = r.session_id
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
  JOIN archive_sessions a ON a.session_id = sa.session_id
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
  JOIN archive_sessions a ON a.session_id = s.id
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

    if not phase_at_least(state.phase, "request_exports_deleted"):
        total = delete_in_batches(
            dsn,
            state=state,
            state_path=state_path,
            candidate_file=candidate_file,
            stage_name="request_exports",
            sql_factory=request_exports_sql,
        )
        state.phase = "request_exports_deleted"
        state.deleted["request_exports"] = total
        state.updated_at = utc_now()
        write_run_state(state_path, state)
        print(f"[progress] request_exports deleted={total}", flush=True)

    if not phase_at_least(state.phase, "requests_deleted"):
        total = delete_in_batches(
            dsn,
            state=state,
            state_path=state_path,
            candidate_file=candidate_file,
            stage_name="requests",
            sql_factory=requests_sql,
        )
        state.phase = "requests_deleted"
        state.deleted["requests"] = total
        state.updated_at = utc_now()
        write_run_state(state_path, state)
        print(f"[progress] requests deleted={total}", flush=True)

    if not phase_at_least(state.phase, "aliases_deleted"):
        total = delete_in_batches(
            dsn,
            state=state,
            state_path=state_path,
            candidate_file=candidate_file,
            stage_name="aliases",
            sql_factory=aliases_sql,
        )
        state.phase = "aliases_deleted"
        state.deleted["aliases"] = total
        state.updated_at = utc_now()
        write_run_state(state_path, state)
        print(f"[progress] aliases deleted={total}", flush=True)

    if not phase_at_least(state.phase, "sessions_deleted"):
        total = delete_in_batches(
            dsn,
            state=state,
            state_path=state_path,
            candidate_file=candidate_file,
            stage_name="sessions",
            sql_factory=sessions_sql,
        )
        state.phase = "sessions_deleted"
        state.deleted["sessions"] = total
        state.updated_at = utc_now()
        write_run_state(state_path, state)
        print(f"[progress] sessions deleted={total}", flush=True)

    if not args.skip_vacuum and not phase_at_least(state.phase, "vacuumed"):
        for table_name in (
            "session_trajectory_request_exports",
            "session_trajectory_requests",
            "session_trajectory_session_aliases",
            "session_trajectory_sessions",
        ):
            vacuum_table(dsn, state.schema, table_name)
        state.phase = "vacuumed"
        state.updated_at = utc_now()
        write_run_state(state_path, state)
        print("[progress] vacuum analyze finished", flush=True)

    state.phase = "completed"
    state.updated_at = utc_now()
    write_run_state(state_path, state)
    write_latest_completed(args.output_root.resolve(), state)
    print(f"[done] completed run_id={state.run_id}", flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
