#!/usr/bin/env python3
"""Project-local stdio MCP server for delegating frontend tasks to Claude Code."""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys
import time
import uuid
from pathlib import Path
from typing import Any, Dict, Iterable, List, Optional, Tuple


SCRIPT_DIR = Path(__file__).resolve().parent
RUNNER_PATH = SCRIPT_DIR / "claude_delegate_runner.py"
SERVER_NAME = "claude-delegate"
SERVER_VERSION = "0.2.0"


TOOLS: List[Dict[str, Any]] = [
    {
        "name": "start_frontend_task",
        "description": "Start a bounded frontend implementation task in Claude Code and persist its progress under <cwd>/.codex/claude-runs/<run-id>/.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "cwd": {"type": "string", "description": "Absolute project path for the Claude run."},
                "task": {"type": "string", "description": "Concrete frontend task for Claude to execute."},
                "context_summary": {"type": "string"},
                "design_direction": {"type": "string"},
                "context_files": {"type": "array", "items": {"type": "string"}},
                "read_files": {"type": "array", "items": {"type": "string"}},
                "write_scope": {"type": "array", "items": {"type": "string"}},
                "constraints": {"type": "array", "items": {"type": "string"}},
                "acceptance_criteria": {"type": "array", "items": {"type": "string"}},
                "add_dirs": {"type": "array", "items": {"type": "string"}},
                "timeout_sec": {"type": "integer", "minimum": 60, "maximum": 7200},
                "model": {"type": "string"},
                "claude_bin": {"type": "string"},
                "effort": {"type": "string", "enum": ["low", "medium", "high", "max"]},
                "permission_mode": {
                    "type": "string",
                    "enum": ["acceptEdits", "auto", "bypassPermissions", "default", "dontAsk", "plan"],
                },
            },
            "required": ["cwd", "task"],
            "additionalProperties": False,
        },
    },
    {
        "name": "get_task_status",
        "description": "Read current status for a previously started Claude frontend task.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "cwd": {"type": "string"},
                "run_id": {"type": "string"},
            },
            "required": ["cwd", "run_id"],
            "additionalProperties": False,
        },
    },
    {
        "name": "tail_task_events",
        "description": "Return the latest simplified event entries from a Claude frontend task.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "cwd": {"type": "string"},
                "run_id": {"type": "string"},
                "limit": {"type": "integer", "minimum": 1, "maximum": 200},
            },
            "required": ["cwd", "run_id"],
            "additionalProperties": False,
        },
    },
    {
        "name": "get_task_result",
        "description": "Return the final handoff and metadata for a completed Claude frontend task.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "cwd": {"type": "string"},
                "run_id": {"type": "string"},
            },
            "required": ["cwd", "run_id"],
            "additionalProperties": False,
        },
    },
]


class MCPError(Exception):
    def __init__(self, message: str, code: int = -32000) -> None:
        super().__init__(message)
        self.code = code


def send_message(payload: Dict[str, Any]) -> None:
    raw = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    header = f"Content-Length: {len(raw)}\r\n\r\n".encode("ascii")
    sys.stdout.buffer.write(header)
    sys.stdout.buffer.write(raw)
    sys.stdout.buffer.flush()


def read_message() -> Optional[Dict[str, Any]]:
    content_length: Optional[int] = None
    while True:
        header = sys.stdin.buffer.readline()
        if not header:
            return None
        if header in (b"\r\n", b"\n"):
            break
        text = header.decode("utf-8").strip()
        if not text:
            break
        if ":" not in text:
            continue
        key, value = text.split(":", 1)
        if key.lower() == "content-length":
            content_length = int(value.strip())
    if content_length is None:
        return None
    body = sys.stdin.buffer.read(content_length)
    if not body:
        return None
    return json.loads(body.decode("utf-8"))


def ensure_absolute_dir(path_str: str) -> Path:
    path = Path(path_str).expanduser().resolve()
    if not path.is_absolute():
        raise MCPError(f"cwd must be an absolute path, got: {path_str}", code=-32602)
    if not path.exists():
        raise MCPError(f"cwd does not exist: {path}", code=-32602)
    if not path.is_dir():
        raise MCPError(f"cwd is not a directory: {path}", code=-32602)
    return path


def resolve_context_file(cwd: Path, path_str: str) -> Path:
    candidate = Path(path_str).expanduser()
    if not candidate.is_absolute():
        candidate = cwd / candidate
    candidate = candidate.resolve()
    if not candidate.exists() or not candidate.is_file():
        raise MCPError(f"context file not found: {path_str}", code=-32602)
    return candidate


def run_root(cwd: Path) -> Path:
    return cwd / ".codex" / "claude-runs"


def run_dir_from_id(cwd: Path, run_id: str) -> Path:
    return run_root(cwd) / run_id


def write_json(path: Path, payload: Dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def read_json(path: Path) -> Dict[str, Any]:
    if not path.exists():
        raise MCPError(f"Missing file: {path}", code=-32004)
    return json.loads(path.read_text(encoding="utf-8"))


def read_jsonl_tail(path: Path, limit: int) -> List[Dict[str, Any]]:
    if not path.exists():
        return []
    lines = path.read_text(encoding="utf-8").splitlines()
    items: List[Dict[str, Any]] = []
    for raw in lines[-limit:]:
        raw = raw.strip()
        if not raw:
            continue
        try:
            items.append(json.loads(raw))
        except json.JSONDecodeError:
            items.append({"type": "raw", "text": raw})
    return items


def load_context_sections(cwd: Path, path_values: Iterable[str]) -> List[Tuple[str, str]]:
    sections: List[Tuple[str, str]] = []
    for value in path_values:
        resolved = resolve_context_file(cwd, value)
        text = resolved.read_text(encoding="utf-8")
        sections.append((str(resolved.relative_to(cwd)), text))
    return sections


def build_prompt(data: Dict[str, Any], run_id: str, session_id: str) -> str:
    cwd = Path(data["cwd"])

    def section(title: str, items: Iterable[str]) -> str:
        values = [f"- {item}" for item in items if item]
        return f"## {title}\n" + ("\n".join(values) if values else "- None")

    parts = [
        "You are Claude Code acting as a bounded frontend specialist inside a Codex-orchestrated workflow.",
        "Codex remains the lead agent and will review your result.",
        "Stay strictly inside the declared write scope. If something outside that scope must change, mention it in the handoff instead of editing it.",
        "",
        f"Run ID: {run_id}",
        f"Session ID: {session_id}",
        "",
        "## Task",
        data["task"].strip(),
        "",
        "## Context Summary",
        data.get("context_summary", "").strip() or "None",
        "",
        "## Design Direction",
        data.get("design_direction", "").strip() or "None",
        "",
        section("Read Files First", data.get("read_files", [])),
        "",
        section("Write Scope", data.get("write_scope", [])),
        "",
        section("Constraints", data.get("constraints", [])),
        "",
        section("Acceptance Criteria", data.get("acceptance_criteria", [])),
    ]

    context_sections = load_context_sections(cwd, data.get("context_files", []))
    if context_sections:
        parts.extend(["", "## Project Context Files"])
        for relative_path, text in context_sections:
            parts.extend(
                [
                    "",
                    f"### {relative_path}",
                    "```md",
                    text.strip(),
                    "```",
                ]
            )

    parts.extend(
        [
            "",
            "## Execution Rules",
            "- Prefer reading relevant files before editing.",
            "- Keep the visual direction intentional and avoid generic UI choices.",
            "- Preserve existing design-system patterns when they already exist.",
            "- If you run checks, keep them targeted and mention them explicitly in the final handoff.",
            "- Do not output hidden reasoning. Output only concise visible progress and the final handoff.",
            "",
            "## Final Response Format",
            "Use exactly these markdown headings in this order:",
            "### Summary",
            "### Changed Files",
            "### Tests Run",
            "### Risks",
            "### Next Steps For Codex",
        ]
    )
    return "\n".join(parts).strip() + "\n"


def start_frontend_task(arguments: Dict[str, Any]) -> Dict[str, Any]:
    cwd = ensure_absolute_dir(arguments["cwd"])
    if not RUNNER_PATH.exists():
        raise MCPError(f"Runner script not found: {RUNNER_PATH}", code=-32001)
    claude_bin = arguments.get("claude_bin") or os.environ.get("CLAUDE_BIN") or "claude"
    if shutil.which(claude_bin) is None:
        raise MCPError(f"Claude executable not found on PATH: {claude_bin}", code=-32002)

    run_id = time.strftime("%Y%m%d-%H%M%S") + "-" + uuid.uuid4().hex[:8]
    session_id = str(uuid.uuid4())
    root = run_root(cwd)
    run_dir = run_dir_from_id(cwd, run_id)
    root.mkdir(parents=True, exist_ok=True)
    run_dir.mkdir(parents=True, exist_ok=True)

    task_payload = {
        "run_id": run_id,
        "session_id": session_id,
        "cwd": str(cwd),
        "task": arguments["task"],
        "context_summary": arguments.get("context_summary", ""),
        "design_direction": arguments.get("design_direction", ""),
        "context_files": arguments.get("context_files", []),
        "read_files": arguments.get("read_files", []),
        "write_scope": arguments.get("write_scope", []),
        "constraints": arguments.get("constraints", []),
        "acceptance_criteria": arguments.get("acceptance_criteria", []),
        "add_dirs": arguments.get("add_dirs", []),
        "timeout_sec": int(arguments.get("timeout_sec", 1800)),
        "model": arguments.get("model", ""),
        "claude_bin": claude_bin,
        "effort": arguments.get("effort", "high"),
        "permission_mode": arguments.get("permission_mode", "bypassPermissions"),
        "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    }
    prompt = build_prompt(task_payload, run_id=run_id, session_id=session_id)
    write_json(run_dir / "task.json", task_payload)
    (run_dir / "prompt.md").write_text(prompt, encoding="utf-8")
    write_json(
        run_dir / "status.json",
        {
            "run_id": run_id,
            "session_id": session_id,
            "state": "queued",
            "cwd": str(cwd),
            "run_dir": str(run_dir),
            "created_at": task_payload["created_at"],
            "updated_at": task_payload["created_at"],
        },
    )

    command = [sys.executable, str(RUNNER_PATH), "--run-dir", str(run_dir)]
    subprocess.Popen(
        command,
        cwd=str(cwd),
        stdin=subprocess.DEVNULL,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        start_new_session=True,
        env=os.environ.copy(),
    )
    time.sleep(0.15)
    status = read_json(run_dir / "status.json")
    return {
        "run_id": run_id,
        "session_id": session_id,
        "state": status.get("state", "queued"),
        "cwd": str(cwd),
        "run_dir": str(run_dir),
    }


def get_task_status(arguments: Dict[str, Any]) -> Dict[str, Any]:
    cwd = ensure_absolute_dir(arguments["cwd"])
    run_dir = run_dir_from_id(cwd, arguments["run_id"])
    status = read_json(run_dir / "status.json")
    status["recent_events"] = read_jsonl_tail(run_dir / "events.jsonl", limit=5)
    return status


def tail_task_events(arguments: Dict[str, Any]) -> Dict[str, Any]:
    cwd = ensure_absolute_dir(arguments["cwd"])
    run_dir = run_dir_from_id(cwd, arguments["run_id"])
    limit = int(arguments.get("limit", 20))
    return {"run_id": arguments["run_id"], "events": read_jsonl_tail(run_dir / "events.jsonl", limit=limit)}


def get_task_result(arguments: Dict[str, Any]) -> Dict[str, Any]:
    cwd = ensure_absolute_dir(arguments["cwd"])
    run_dir = run_dir_from_id(cwd, arguments["run_id"])
    status = read_json(run_dir / "status.json")
    result = read_json(run_dir / "result.json") if (run_dir / "result.json").exists() else {}
    handoff = (run_dir / "handoff.md").read_text(encoding="utf-8") if (run_dir / "handoff.md").exists() else ""
    changed_files = read_json(run_dir / "changed_files.json") if (run_dir / "changed_files.json").exists() else {"files": []}
    return {
        "status": status,
        "result": result,
        "handoff": handoff,
        "changed_files": changed_files.get("files", []),
        "run_dir": str(run_dir),
    }


def tool_result(data: Dict[str, Any]) -> Dict[str, Any]:
    pretty = json.dumps(data, ensure_ascii=False, indent=2)
    return {
        "content": [{"type": "text", "text": pretty}],
        "structuredContent": data,
        "isError": False,
    }


def handle_tool_call(name: str, arguments: Dict[str, Any]) -> Dict[str, Any]:
    if name == "start_frontend_task":
        return tool_result(start_frontend_task(arguments))
    if name == "get_task_status":
        return tool_result(get_task_status(arguments))
    if name == "tail_task_events":
        return tool_result(tail_task_events(arguments))
    if name == "get_task_result":
        return tool_result(get_task_result(arguments))
    raise MCPError(f"Unknown tool: {name}", code=-32601)


def handle_request(request: Dict[str, Any]) -> Tuple[str, Dict[str, Any]]:
    method = request.get("method", "")
    req_id = request.get("id")
    params = request.get("params", {})

    if method == "initialize":
        return (
            req_id,
            {
                "protocolVersion": "2024-11-05",
                "serverInfo": {"name": SERVER_NAME, "version": SERVER_VERSION},
                "capabilities": {"tools": {}},
            },
        )
    if method == "notifications/initialized":
        return ("", {})
    if method == "ping":
        return (req_id, {})
    if method == "tools/list":
        return (req_id, {"tools": TOOLS})
    if method == "tools/call":
        return (req_id, handle_tool_call(params.get("name"), params.get("arguments", {})))
    raise MCPError(f"Unsupported method: {method}", code=-32601)


def main() -> int:
    while True:
        request = read_message()
        if request is None:
            return 0
        req_id = request.get("id")
        try:
            reply_id, result = handle_request(request)
            if request.get("method") == "notifications/initialized":
                continue
            send_message({"jsonrpc": "2.0", "id": reply_id, "result": result})
        except MCPError as exc:
            send_message({"jsonrpc": "2.0", "id": req_id, "error": {"code": exc.code, "message": str(exc)}})
        except Exception as exc:  # pragma: no cover
            send_message({"jsonrpc": "2.0", "id": req_id, "error": {"code": -32099, "message": f"Internal error: {exc}"}})


if __name__ == "__main__":
    raise SystemExit(main())

