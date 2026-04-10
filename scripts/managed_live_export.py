#!/usr/bin/env python3
"""Managed launcher for session trajectory live exports on macOS."""

from __future__ import annotations

import argparse
import json
import os
import pathlib
import re
import signal
import subprocess
import sys
import traceback
from datetime import datetime, timezone
from typing import Any


GO_CANDIDATE_PATHS = (
    "/usr/local/go/bin/go",
    "/opt/homebrew/bin/go",
    "/usr/bin/go",
)


def utc_now() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


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


def sanitize_label_fragment(value: str) -> str:
    normalized = re.sub(r"[^A-Za-z0-9._-]+", "-", value.strip())
    normalized = normalized.strip(".-")
    return normalized or "export"


def default_label(*, start_time: str, end_time: str) -> str:
    start_part = sanitize_label_fragment(start_time or "none")
    end_part = sanitize_label_fragment(end_time or "open")
    return f"com.codex.session_live_export.{start_part}.to.{end_part}"[:200]


def default_state_path(*, manifest_dir: pathlib.Path, label: str) -> pathlib.Path:
    slug = sanitize_label_fragment(label)
    return manifest_dir / f"{slug}.state.json"


def write_state(path: pathlib.Path, state: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(state, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def update_state(state: dict[str, Any], path: pathlib.Path, **changes: Any) -> None:
    state.update(changes)
    state["updated_at"] = utc_now()
    write_state(path, state)


def find_latest_manifest(manifest_dir: pathlib.Path, *, started_at: float) -> pathlib.Path:
    candidates = [p for p in manifest_dir.glob("session-trajectory-export-*.json") if p.stat().st_mtime >= started_at]
    if not candidates:
        candidates = list(manifest_dir.glob("session-trajectory-export-*.json"))
    if not candidates:
        raise SystemExit(f"no manifest found in {manifest_dir}")
    return max(candidates, key=lambda p: p.stat().st_mtime)


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mode", choices=("submit", "run", "status"), default="submit")
    parser.add_argument("--label", default="", help="launchctl label for the managed export job")
    parser.add_argument("--state-path", type=pathlib.Path, help="explicit state json path")
    parser.add_argument("--pg-dsn", required=False, default="", help="Postgres DSN for export_session_trajectories")
    parser.add_argument("--pg-schema", default="public")
    parser.add_argument("--export-root", type=pathlib.Path, required=True)
    parser.add_argument("--manifest-dir", type=pathlib.Path, required=True)
    parser.add_argument("--user-id", default="")
    parser.add_argument("--source", default="")
    parser.add_argument("--call-type", default="")
    parser.add_argument("--status-filter", default="")
    parser.add_argument("--provider", default="")
    parser.add_argument("--canonical-model-family", default="")
    parser.add_argument("--start-time", default="")
    parser.add_argument("--end-time", default="")
    parser.add_argument("--page-size", type=int, default=100)
    parser.add_argument("--connect-timeout", default="30s")
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--stdout-log", type=pathlib.Path, help="explicit stdout/stderr log path used in submit mode")
    return parser.parse_args(argv)


def build_export_cmd(args: argparse.Namespace) -> list[str]:
    go_bin = resolve_binary("go", GO_CANDIDATE_PATHS)
    cmd = [
        go_bin,
        "run",
        "./scripts/export_session_trajectories",
        "--pg-schema",
        args.pg_schema,
        "--export-root",
        str(args.export_root),
        "--manifest-dir",
        str(args.manifest_dir),
        "--page-size",
        str(args.page_size),
        "--connect-timeout",
        args.connect_timeout,
    ]
    if args.pg_dsn:
        cmd.extend(["--pg-dsn", args.pg_dsn])
    if args.user_id:
        cmd.extend(["--user-id", args.user_id])
    if args.source:
        cmd.extend(["--source", args.source])
    if args.call_type:
        cmd.extend(["--call-type", args.call_type])
    if args.status_filter:
        cmd.extend(["--status", args.status_filter])
    if args.provider:
        cmd.extend(["--provider", args.provider])
    if args.canonical_model_family:
        cmd.extend(["--canonical-model-family", args.canonical_model_family])
    if args.start_time:
        cmd.extend(["--start-time", args.start_time])
    if args.end_time:
        cmd.extend(["--end-time", args.end_time])
    if args.dry_run:
        cmd.append("--dry-run")
    return cmd


def build_state(args: argparse.Namespace, *, label: str, state_path: pathlib.Path) -> dict[str, Any]:
    return {
        "label": label,
        "status": "submitted" if args.mode == "submit" else "running",
        "current_stage": "submitted" if args.mode == "submit" else "export_running",
        "started_at": utc_now(),
        "updated_at": utc_now(),
        "state_path": str(state_path),
        "export_root": str(args.export_root),
        "manifest_dir": str(args.manifest_dir),
        "filters": {
            "user_id": args.user_id or None,
            "source": args.source or None,
            "call_type": args.call_type or None,
            "status": args.status_filter or None,
            "provider": args.provider or None,
            "canonical_model_family": args.canonical_model_family or None,
            "start_time": args.start_time or None,
            "end_time": args.end_time or None,
            "page_size": args.page_size,
            "dry_run": args.dry_run,
        },
    }


def launchctl_submit(args: argparse.Namespace, *, label: str, state_path: pathlib.Path, repo_root: pathlib.Path) -> None:
    log_path = (args.stdout_log.expanduser().resolve() if args.stdout_log else (args.export_root / "export.log"))
    log_path.parent.mkdir(parents=True, exist_ok=True)
    args.export_root.mkdir(parents=True, exist_ok=True)
    args.manifest_dir.mkdir(parents=True, exist_ok=True)
    cmd = [
        "launchctl",
        "submit",
        "-l",
        label,
        "-o",
        str(log_path),
        "-e",
        str(log_path),
        "--",
        sys.executable,
        str(pathlib.Path(__file__).resolve()),
        "--mode",
        "run",
        "--label",
        label,
        "--state-path",
        str(state_path),
        "--pg-schema",
        args.pg_schema,
        "--export-root",
        str(args.export_root),
        "--manifest-dir",
        str(args.manifest_dir),
        "--page-size",
        str(args.page_size),
        "--connect-timeout",
        args.connect_timeout,
    ]
    optional_pairs = [
        ("--pg-dsn", args.pg_dsn),
        ("--user-id", args.user_id),
        ("--source", args.source),
        ("--call-type", args.call_type),
        ("--status-filter", args.status_filter),
        ("--provider", args.provider),
        ("--canonical-model-family", args.canonical_model_family),
        ("--start-time", args.start_time),
        ("--end-time", args.end_time),
    ]
    for flag, value in optional_pairs:
        if value:
            cmd.extend([flag, value])
    if args.dry_run:
        cmd.append("--dry-run")

    state = build_state(args, label=label, state_path=state_path)
    state["log_path"] = str(log_path)
    update_state(state, state_path, status="submitted", current_stage="submitted")

    remove_cmd = ["launchctl", "remove", label]
    subprocess.run(remove_cmd, check=False, capture_output=True, text=True)
    proc = subprocess.run(cmd, check=False, cwd=str(repo_root), capture_output=True, text=True)
    if proc.returncode != 0:
        update_state(
            state,
            state_path,
            status="failed",
            current_stage="submit_failed",
            error=f"launchctl submit failed ({proc.returncode})",
            stdout=proc.stdout,
            stderr=proc.stderr,
        )
        raise SystemExit(f"launchctl submit failed ({proc.returncode})")

    update_state(state, state_path, status="running", current_stage="launchd_submitted")


def run_export(args: argparse.Namespace, *, label: str, state_path: pathlib.Path, repo_root: pathlib.Path) -> int:
    state = build_state(args, label=label, state_path=state_path)
    update_state(state, state_path, status="running", current_stage="export_running", current_command=build_export_cmd(args))

    def on_signal(signum: int, _frame: Any) -> None:
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

    started_at = datetime.now().timestamp()
    try:
        proc = subprocess.run(build_export_cmd(args), check=False, cwd=str(repo_root))
        if proc.returncode != 0:
            raise SystemExit(f"export command failed ({proc.returncode})")
        manifest_path = find_latest_manifest(args.manifest_dir, started_at=started_at)
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        update_state(
            state,
            state_path,
            status="completed",
            current_stage="completed",
            manifest_path=str(manifest_path),
            exported_sessions=manifest.get("exported_sessions"),
            exported_files=manifest.get("exported_files"),
            token_totals=manifest.get("token_totals", {}),
        )
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


def print_status(state_path: pathlib.Path, *, label: str) -> int:
    if not state_path.exists():
        raise SystemExit(f"state file not found: {state_path}")
    print(state_path.read_text(encoding="utf-8"), end="")
    if label:
        print("--- launchctl ---")
        subprocess.run(["launchctl", "print", f"gui/{os.getuid()}/{label}"], check=False)
    return 0


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    repo_root = pathlib.Path(__file__).resolve().parents[1]
    args.export_root = args.export_root.expanduser().resolve()
    args.manifest_dir = args.manifest_dir.expanduser().resolve()
    label = args.label.strip() or default_label(start_time=args.start_time, end_time=args.end_time)
    state_path = args.state_path.expanduser().resolve() if args.state_path else default_state_path(manifest_dir=args.manifest_dir, label=label)

    if args.mode == "submit":
        launchctl_submit(args, label=label, state_path=state_path, repo_root=repo_root)
        print(f"[submitted] label={label}")
        print(f"[submitted] state_path={state_path}")
        return 0
    if args.mode == "run":
        return run_export(args, label=label, state_path=state_path, repo_root=repo_root)
    return print_status(state_path, label=label)


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
