#!/usr/bin/env python3
"""Watch a long-running live export, notify on schedule, relaunch if needed, and record completion."""

from __future__ import annotations

import argparse
import json
import pathlib
import re
import subprocess
import time
from datetime import datetime, timezone
from typing import Any


PROGRESS_RE = re.compile(
    r"^(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}) (reused|exported) (\d+)/(\d+) sessions"
)


def utc_now() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--task-label", required=True, help="launchctl label for the live export task")
    parser.add_argument("--task-command", required=True, help="shell command used to launch or relaunch the export task")
    parser.add_argument("--process-match", default="", help="optional process substring used to detect the running export task")
    parser.add_argument("--task-log", type=pathlib.Path, required=True, help="shared stdout/stderr log path for the export task")
    parser.add_argument("--export-root", type=pathlib.Path, required=True)
    parser.add_argument("--manifest-dir", type=pathlib.Path, required=True)
    parser.add_argument("--expected-total", type=int, default=0, help="expected total sessions, optional")
    parser.add_argument("--first-check-seconds", type=int, default=1800)
    parser.add_argument("--repeat-check-seconds", type=int, default=3600)
    parser.add_argument("--state-path", type=pathlib.Path, required=True)
    parser.add_argument("--summary-path", type=pathlib.Path, required=True)
    return parser.parse_args(argv)


def write_json(path: pathlib.Path, payload: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def list_matching_manifests(manifest_dir: pathlib.Path, export_root: pathlib.Path) -> list[pathlib.Path]:
    if not manifest_dir.exists():
        return []
    matches: list[pathlib.Path] = []
    for path in sorted(manifest_dir.glob("session-trajectory-export-*.json")):
        try:
            data = json.loads(path.read_text(encoding="utf-8"))
        except Exception:
            continue
        if data.get("export_root") == str(export_root):
            matches.append(path)
    return matches


def latest_progress(log_path: pathlib.Path) -> dict[str, Any]:
    result: dict[str, Any] = {}
    if not log_path.exists():
        return result
    lines = log_path.read_text(errors="replace").splitlines()
    for line in reversed(lines):
        match = PROGRESS_RE.match(line)
        if not match:
            continue
        result["timestamp"] = match.group(1)
        result["mode"] = match.group(2)
        result["done"] = int(match.group(3))
        result["total"] = int(match.group(4))
        result["line"] = line
        break
    return result


def count_export_root(export_root: pathlib.Path) -> dict[str, int]:
    if not export_root.exists():
        return {"session_dirs": 0, "json_files": 0}
    session_dirs = sum(1 for path in export_root.iterdir() if path.is_dir())
    json_files = sum(1 for _ in export_root.rglob("*.json"))
    return {"session_dirs": session_dirs, "json_files": json_files}


def maybe_complete_from_manifest(
    *,
    manifest_dir: pathlib.Path,
    export_root: pathlib.Path,
    summary_path: pathlib.Path,
    state: dict[str, Any],
    state_path: pathlib.Path,
) -> bool:
    manifests = list_matching_manifests(manifest_dir, export_root)
    if not manifests:
        return False
    manifest_path = manifests[-1]
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    summary = {
        "status": "completed",
        "completed_at": utc_now(),
        "manifest_path": str(manifest_path),
        "exported_sessions": manifest.get("exported_sessions"),
        "total_sessions": manifest.get("total_sessions"),
        "exported_files": manifest.get("exported_files"),
        "export_root": str(export_root),
    }
    write_json(summary_path, summary)
    state.update(summary)
    state["updated_at"] = utc_now()
    write_json(state_path, state)
    notify(f"task3已完成 {manifest.get('exported_sessions')}/{manifest.get('total_sessions')}")
    return True


def is_task_running(process_match: str) -> bool:
    proc = subprocess.run(
        ["ps", "-Ao", "command"],
        text=True,
        capture_output=True,
        check=False,
    )
    if proc.returncode != 0:
        return False
    needle = process_match.strip()
    return any(needle in line for line in proc.stdout.splitlines())


def relaunch_task(*, label: str, task_log: pathlib.Path, task_command: str) -> None:
    subprocess.run(["launchctl", "remove", label], check=False)
    task_log.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run(
        [
            "launchctl",
            "submit",
            "-l",
            label,
            "-o",
            str(task_log),
            "-e",
            str(task_log),
            "--",
            "/bin/zsh",
            "-lc",
            task_command,
        ],
        check=True,
    )


def notify(message: str) -> None:
    subprocess.run(
        ["/usr/bin/afplay", str(pathlib.Path.home() / ".claude/sounds/gentle_chime.mp3")],
        check=False,
    )
    subprocess.run(
        ["/usr/bin/osascript", "-e", f'display notification "{message}" with title "Codex"'],
        check=False,
    )


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    export_root = args.export_root.expanduser().resolve()
    manifest_dir = args.manifest_dir.expanduser().resolve()
    task_log = args.task_log.expanduser().resolve()
    state_path = args.state_path.expanduser().resolve()
    summary_path = args.summary_path.expanduser().resolve()

    state: dict[str, Any] = {
        "status": "watching",
        "started_at": utc_now(),
        "updated_at": utc_now(),
        "task_label": args.task_label,
        "task_command": args.task_command,
        "process_match": args.process_match or args.task_command,
        "task_log": str(task_log),
        "export_root": str(export_root),
        "manifest_dir": str(manifest_dir),
        "expected_total": args.expected_total,
        "first_check_seconds": args.first_check_seconds,
        "repeat_check_seconds": args.repeat_check_seconds,
        "checks": [],
    }
    write_json(state_path, state)

    if maybe_complete_from_manifest(
        manifest_dir=manifest_dir,
        export_root=export_root,
        summary_path=summary_path,
        state=state,
        state_path=state_path,
    ):
        return 0

    delay = max(1, args.first_check_seconds)
    while True:
        time.sleep(delay)
        delay = max(1, args.repeat_check_seconds)

        if maybe_complete_from_manifest(
            manifest_dir=manifest_dir,
            export_root=export_root,
            summary_path=summary_path,
            state=state,
            state_path=state_path,
        ):
            return 0

        running = is_task_running(args.process_match or args.task_command)
        progress = latest_progress(task_log)
        counts = count_export_root(export_root)
        check_record = {
            "checked_at": utc_now(),
            "running": running,
            "progress": progress,
            "counts": counts,
        }
        state["checks"].append(check_record)
        state["checks"] = state["checks"][-24:]
        state["updated_at"] = utc_now()

        done = int(progress.get("done") or 0)
        total = int(progress.get("total") or args.expected_total or 0)
        if not running:
            relaunch_task(label=args.task_label, task_log=task_log, task_command=args.task_command)
            state["last_action"] = "relaunch"
            write_json(state_path, state)
            if total > 0:
                pct = round(done / total * 100, 1)
                notify(f"task3掉线已重拉 {done}/{total} ({pct}%)")
            else:
                notify("task3掉线已重拉")
            continue

        write_json(state_path, state)
        if total > 0:
            pct = round(done / total * 100, 1)
            notify(f"task3进度 {done}/{total} ({pct}%)")
        else:
            notify("task3仍在运行")


if __name__ == "__main__":
    raise SystemExit(main(__import__("sys").argv[1:]))
