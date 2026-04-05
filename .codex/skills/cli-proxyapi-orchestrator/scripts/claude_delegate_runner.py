#!/usr/bin/env python3
"""Background runner for a delegated Claude frontend task."""

from __future__ import annotations

import argparse
import json
import os
import re
import selectors
import signal
import subprocess
import time
from pathlib import Path
from typing import Any, Dict, List


SECTION_PATTERN = re.compile(
    r"^###\s+(Summary|Changed Files|Tests Run|Risks|Next Steps For Codex)\s*$",
    re.MULTILINE,
)


def read_json(path: Path) -> Dict[str, Any]:
    return json.loads(path.read_text(encoding="utf-8"))


def write_json(path: Path, payload: Dict[str, Any]) -> None:
    path.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def append_jsonl(path: Path, payload: Dict[str, Any]) -> None:
    with path.open("a", encoding="utf-8") as fh:
        fh.write(json.dumps(payload, ensure_ascii=False) + "\n")


def now_utc() -> str:
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def update_status(path: Path, **kwargs: Any) -> Dict[str, Any]:
    payload = read_json(path)
    payload.update(kwargs)
    payload["updated_at"] = now_utc()
    write_json(path, payload)
    return payload


def build_command(task: Dict[str, Any], prompt_path: Path) -> List[str]:
    command = [
        task.get("claude_bin", "claude"),
        "-p",
        "--output-format",
        "stream-json",
        "--verbose",
        "--include-partial-messages",
        "--permission-mode",
        task.get("permission_mode", "bypassPermissions"),
        "--session-id",
        task["session_id"],
        "--append-system-prompt",
        (
            "You are a frontend specialist working inside a Codex-orchestrated workflow. "
            "Respect the declared write scope and end with the requested markdown handoff."
        ),
    ]
    effort = task.get("effort")
    if effort:
        command.extend(["--effort", effort])
    model = task.get("model")
    if model:
        command.extend(["--model", model])
    for add_dir in task.get("add_dirs", []):
        command.extend(["--add-dir", add_dir])
    command.append(prompt_path.read_text(encoding="utf-8"))
    return command


def flatten_text(value: Any) -> List[str]:
    found: List[str] = []
    if isinstance(value, str):
        text = value.strip()
        if text:
            found.append(text)
        return found
    if isinstance(value, list):
        for item in value:
            found.extend(flatten_text(item))
        return found
    if isinstance(value, dict):
        for key in ("text", "message", "delta", "content", "result", "summary"):
            if key in value:
                found.extend(flatten_text(value[key]))
        return found
    return found


def event_preview(payload: Dict[str, Any]) -> str:
    texts = flatten_text(payload)
    if texts:
        merged = " ".join(texts)
        merged = re.sub(r"\s+", " ", merged).strip()
        return merged[:280]
    event_type = payload.get("type") or payload.get("event") or payload.get("subtype") or "event"
    return str(event_type)


def extract_final_text(raw_stream_path: Path) -> str:
    if not raw_stream_path.exists():
        return ""
    lines = raw_stream_path.read_text(encoding="utf-8").splitlines()
    candidates: List[str] = []
    for raw in lines:
        raw = raw.strip()
        if not raw:
            continue
        try:
            payload = json.loads(raw)
        except json.JSONDecodeError:
            continue
        texts = flatten_text(payload)
        if texts:
            candidates.append("\n".join(texts))
    if not candidates:
        return ""
    for candidate in reversed(candidates):
        if SECTION_PATTERN.search(candidate):
            return candidate.strip()
    return candidates[-1].strip()


def extract_changed_files(handoff_text: str) -> List[str]:
    match = re.search(
        r"###\s+Changed Files\s*(.*?)(?:^###\s+Tests Run|\Z)",
        handoff_text,
        flags=re.MULTILINE | re.DOTALL,
    )
    if not match:
        return []
    block = match.group(1)
    files: List[str] = []
    for line in block.splitlines():
        candidate = line.strip().lstrip("-").strip().strip("`")
        if candidate:
            files.append(candidate)
    seen: List[str] = []
    for item in files:
        if item not in seen:
            seen.append(item)
    return seen


def record_output_line(
    line: str,
    raw_stream_path: Path,
    stdout_log,
    events_path: Path,
    status_path: Path,
) -> None:
    if not line:
        return
    stdout_log.write(line)
    stdout_log.flush()
    with raw_stream_path.open("a", encoding="utf-8") as raw_stream:
        raw_stream.write(line)
    try:
        payload = json.loads(line)
    except json.JSONDecodeError:
        append_jsonl(events_path, {"timestamp": now_utc(), "type": "raw_line", "preview": line.strip()[:280]})
    else:
        append_jsonl(events_path, {"timestamp": now_utc(), "type": payload.get("type", "event"), "preview": event_preview(payload)})
    update_status(status_path, state="running", last_event_at=now_utc())


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--run-dir", required=True)
    args = parser.parse_args()

    run_dir = Path(args.run_dir).resolve()
    task = read_json(run_dir / "task.json")
    prompt_path = run_dir / "prompt.md"
    status_path = run_dir / "status.json"
    raw_stream_path = run_dir / "raw-stream.jsonl"
    events_path = run_dir / "events.jsonl"
    stdout_log_path = run_dir / "claude.stdout.log"
    stderr_log_path = run_dir / "claude.stderr.log"

    command = build_command(task, prompt_path)
    env = os.environ.copy()
    raw_stream_path.touch(exist_ok=True)

    with stdout_log_path.open("w", encoding="utf-8") as stdout_log, stderr_log_path.open("w", encoding="utf-8") as stderr_log:
        process = subprocess.Popen(
            command,
            cwd=task["cwd"],
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=stderr_log,
            text=True,
            bufsize=1,
            env=env,
            start_new_session=True,
        )
        update_status(
            status_path,
            state="running",
            pid=process.pid,
            started_at=now_utc(),
            command=command[:-1] + ["<prompt omitted>"],
        )
        append_jsonl(events_path, {"timestamp": now_utc(), "type": "runner_status", "state": "running", "pid": process.pid})

        timed_out = False
        deadline = time.time() + int(task.get("timeout_sec", 1800))

        try:
            assert process.stdout is not None
            selector = selectors.DefaultSelector()
            selector.register(process.stdout, selectors.EVENT_READ)

            while True:
                if time.time() > deadline:
                    timed_out = True
                    os.killpg(process.pid, signal.SIGTERM)
                    break
                ready = selector.select(timeout=1.0)
                if ready:
                    line = process.stdout.readline()
                    record_output_line(line, raw_stream_path, stdout_log, events_path, status_path)
                    continue
                if process.poll() is not None:
                    break
        finally:
            if process.stdout is not None:
                remaining = process.stdout.read()
                if remaining:
                    for extra_line in remaining.splitlines(keepends=True):
                        record_output_line(extra_line, raw_stream_path, stdout_log, events_path, status_path)
            try:
                rc = process.wait(timeout=30)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                rc = process.wait(timeout=30)

    final_text = extract_final_text(raw_stream_path)
    handoff_text = final_text if SECTION_PATTERN.search(final_text) else (final_text.strip() or "Claude did not produce a structured handoff.")
    (run_dir / "handoff.md").write_text(handoff_text + "\n", encoding="utf-8")
    changed_files = extract_changed_files(handoff_text)
    write_json(run_dir / "changed_files.json", {"files": changed_files})

    result_payload = {
        "run_id": task["run_id"],
        "session_id": task["session_id"],
        "exit_code": rc,
        "timed_out": timed_out,
        "completed_at": now_utc(),
        "handoff_path": str(run_dir / "handoff.md"),
        "raw_stream_path": str(raw_stream_path),
        "changed_files_path": str(run_dir / "changed_files.json"),
    }
    write_json(run_dir / "result.json", result_payload)
    update_status(
        status_path,
        state="failed" if rc != 0 or timed_out else "completed",
        completed_at=result_payload["completed_at"],
        exit_code=rc,
        timed_out=timed_out,
    )
    append_jsonl(
        events_path,
        {
            "timestamp": now_utc(),
            "type": "runner_status",
            "state": "failed" if rc != 0 or timed_out else "completed",
            "exit_code": rc,
            "timed_out": timed_out,
        },
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
