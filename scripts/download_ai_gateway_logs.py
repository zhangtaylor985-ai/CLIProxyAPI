#!/usr/bin/env python3
"""Download and normalize Cloudflare AI Gateway logs into session folders."""

from __future__ import annotations

import argparse
import json
import os
import re
import socket
import sys
import time
from collections import defaultdict
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any
from urllib import error, parse, request


API_BASE = "https://api.cloudflare.com/client/v4"
DEFAULT_OUTPUT_DIR = "ai-gateway-data/session-exports"
DEFAULT_HTTP_TIMEOUT = 20
STATE_FILENAME = ".export-state.json"
SESSION_PATTERN = re.compile(r"(?:^|[_:-])session[_:-]?([A-Za-z0-9-]{8,})")
USER_SEGMENT_SPLIT_PATTERNS = ("_account_", "_session_")
WRANGLER_AUTH_CONFIG = Path.home() / "Library/Preferences/.wrangler/config/default.toml"


@dataclass
class NormalizedInteraction:
    session_id: str
    user_id: str | None
    request_id: str | None
    start_time: str | None
    payload: dict[str, Any]


class CloudflareAPIError(RuntimeError):
    pass


@dataclass
class ResumeState:
    completed_log_ids: set[str]
    non_conversation_log_ids: set[str]
    session_counts: dict[str, int]
    session_dirs: dict[str, Path]
    written_files: int
    skipped_non_conversation: int
    skipped_errors: int


def load_local_env_if_present() -> None:
    for env_name in (".env", ".envrc"):
        env_path = Path.cwd() / env_name
        if not env_path.exists():
            continue
        for raw_line in env_path.read_text(encoding="utf-8").splitlines():
            line = raw_line.strip()
            if not line or line.startswith("#"):
                continue
            if line.startswith("export "):
                line = line[len("export ") :].strip()
            if "=" not in line:
                continue
            key, value = line.split("=", 1)
            key = key.strip()
            value = value.strip().strip('"').strip("'")
            if key and key not in os.environ:
                os.environ[key] = value


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Download Cloudflare AI Gateway logs and export them by session_id."
    )
    subparsers = parser.add_subparsers(dest="command", required=True)

    fetch_parser = subparsers.add_parser(
        "fetch-gateway",
        help="Download logs from Cloudflare AI Gateway and export normalized session files.",
    )
    fetch_parser.add_argument("--account-id", required=True, help="Cloudflare account id.")
    fetch_parser.add_argument("--gateway-id", required=True, help="AI Gateway id.")
    fetch_parser.add_argument(
        "--token-env",
        default="CLOUDFLARE_API_TOKEN",
        help="Environment variable that stores the Cloudflare API token.",
    )
    fetch_parser.add_argument(
        "--output-dir",
        default=DEFAULT_OUTPUT_DIR,
        help="Directory used to write normalized session files.",
    )
    fetch_parser.add_argument(
        "--start-date",
        help="Optional RFC3339 or YYYY-MM-DD lower bound for created_at filtering.",
    )
    fetch_parser.add_argument(
        "--end-date",
        help="Optional RFC3339 or YYYY-MM-DD upper bound for created_at filtering.",
    )
    fetch_parser.add_argument(
        "--per-page",
        type=int,
        default=50,
        help="List page size when calling Cloudflare logs API.",
    )
    fetch_parser.add_argument(
        "--max-logs",
        type=int,
        default=0,
        help="Optional hard limit for processed log entries. 0 means no limit.",
    )
    fetch_parser.add_argument(
        "--path-substring",
        default="",
        help="Keep only logs whose path contains this substring. Empty keeps all paths.",
    )
    fetch_parser.add_argument(
        "--direction",
        choices=("asc", "desc"),
        default="asc",
        help="Sort direction when listing gateway logs.",
    )
    fetch_parser.add_argument(
        "--workers",
        type=int,
        default=6,
        help="Concurrent workers used for detail/request/response downloads.",
    )
    fetch_parser.add_argument(
        "--http-timeout",
        type=int,
        default=DEFAULT_HTTP_TIMEOUT,
        help="Per-request HTTP timeout in seconds for Cloudflare API calls.",
    )
    fetch_parser.add_argument(
        "--include-errors",
        action="store_true",
        help="Include non-successful gateway logs. Default behavior exports success=true only.",
    )
    fetch_parser.add_argument(
        "--directory-style",
        choices=("auto", "session-only", "user-session"),
        default="auto",
        help="How session directories are named.",
    )
    fetch_parser.add_argument(
        "--resume",
        action="store_true",
        help="Resume from an existing output directory without breaking session numbering.",
    )
    fetch_parser.add_argument(
        "--skip-offset",
        type=int,
        default=0,
        help="Skip the first N filtered logs before processing.",
    )
    fetch_parser.add_argument(
        "--retry-attempts",
        type=int,
        default=4,
        help="Retry attempts for retryable AI Gateway detail downloads.",
    )
    fetch_parser.add_argument(
        "--retry-backoff-seconds",
        type=float,
        default=2.0,
        help="Base backoff in seconds for retryable AI Gateway detail downloads.",
    )

    normalize_parser = subparsers.add_parser(
        "normalize-pair",
        help="Normalize a manually downloaded request/response pair into the session layout.",
    )
    normalize_parser.add_argument("--request-file", required=True, help="Path to user.json.")
    normalize_parser.add_argument("--response-file", required=True, help="Path to answer.json.")
    normalize_parser.add_argument(
        "--output-dir",
        default=DEFAULT_OUTPUT_DIR,
        help="Directory used to write normalized session files.",
    )
    normalize_parser.add_argument("--request-id", help="Optional override for request_id.")
    normalize_parser.add_argument("--session-id", help="Optional override for session_id.")
    normalize_parser.add_argument("--user-id", help="Optional override for user_id.")
    normalize_parser.add_argument("--start-time", help="Optional override for start_time.")
    normalize_parser.add_argument("--end-time", help="Optional override for end_time.")
    normalize_parser.add_argument(
        "--directory-style",
        choices=("auto", "session-only", "user-session"),
        default="auto",
        help="How session directories are named.",
    )

    return parser.parse_args()


def load_json(path: str | Path) -> Any:
    with Path(path).open("r", encoding="utf-8") as handle:
        return json.load(handle)


def cloudflare_get_json(url: str, token: str, timeout: int = DEFAULT_HTTP_TIMEOUT) -> Any:
    req = request.Request(
        url,
        headers={
            "Authorization": f"Bearer {token}",
            "Accept": "application/json",
        },
    )
    try:
        with request.urlopen(req, timeout=timeout) as response:
            body = response.read().decode("utf-8")
    except error.HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        raise CloudflareAPIError(f"{exc.code} {exc.reason}: {detail}") from exc
    except (TimeoutError, socket.timeout) as exc:
        raise CloudflareAPIError(f"timeout after {timeout}s while fetching {url}") from exc
    except error.URLError as exc:
        raise CloudflareAPIError(str(exc)) from exc

    try:
        payload = json.loads(body)
    except json.JSONDecodeError as exc:
        raise CloudflareAPIError(f"Invalid JSON response from {url}: {exc}") from exc

    if isinstance(payload, dict) and payload.get("success") is False:
        errors_payload = payload.get("errors") or payload
        raise CloudflareAPIError(f"Cloudflare API error for {url}: {errors_payload}")
    return payload


def iter_gateway_logs(
    account_id: str,
    gateway_id: str,
    token: str,
    per_page: int,
    direction: str,
    start_date: str | None,
    end_date: str | None,
    path_substring: str,
    max_logs: int,
    include_errors: bool,
    http_timeout: int,
) -> list[dict[str, Any]]:
    page = 1
    collected: list[dict[str, Any]] = []
    per_page = max(1, min(per_page, 50))

    while True:
        query = {
            "page": page,
            "per_page": per_page,
            "order_by": "created_at",
            "order_by_direction": direction,
            "direction": direction,
        }
        if start_date:
            query["start_date"] = start_date
        if end_date:
            query["end_date"] = end_date
        if not include_errors:
            query["success"] = "true"
        url = (
            f"{API_BASE}/accounts/{account_id}/ai-gateway/gateways/{gateway_id}/logs?"
            f"{parse.urlencode(query)}"
        )
        payload = cloudflare_get_json(url, token, timeout=http_timeout)
        result = payload.get("result") or []
        if not result:
            break

        for item in result:
            path = str(item.get("path") or "")
            if path_substring and path_substring not in path:
                continue
            collected.append(item)
            if max_logs > 0 and len(collected) >= max_logs:
                return collected

        result_info = payload.get("result_info") or {}
        total_count = result_info.get("total_count")
        if len(result) < per_page:
            break
        if isinstance(total_count, int) and page * per_page >= total_count:
            break
        page += 1

    return collected


def get_gateway_log_detail(
    account_id: str,
    gateway_id: str,
    token: str,
    log_id: str,
    http_timeout: int,
) -> dict[str, Any]:
    url = f"{API_BASE}/accounts/{account_id}/ai-gateway/gateways/{gateway_id}/logs/{log_id}"
    payload = cloudflare_get_json(url, token, timeout=http_timeout)
    return payload.get("result") or {}


def get_gateway_log_request(
    account_id: str,
    gateway_id: str,
    token: str,
    log_id: str,
    http_timeout: int,
) -> Any:
    url = f"{API_BASE}/accounts/{account_id}/ai-gateway/gateways/{gateway_id}/logs/{log_id}/request"
    return cloudflare_get_json(url, token, timeout=http_timeout)


def get_gateway_log_response(
    account_id: str,
    gateway_id: str,
    token: str,
    log_id: str,
    http_timeout: int,
) -> Any:
    url = f"{API_BASE}/accounts/{account_id}/ai-gateway/gateways/{gateway_id}/logs/{log_id}/response"
    return cloudflare_get_json(url, token, timeout=http_timeout)


def summarize_error_message(message: str, limit: int = 220) -> str:
    single_line = " ".join(message.split())
    if len(single_line) <= limit:
        return single_line
    return f"{single_line[:limit]}..."


def is_retryable_error(message: str) -> bool:
    lowered = message.lower()
    retry_markers = (
        "1010",
        "access denied",
        "timeout",
        "timed out",
        "429",
        "500",
        "502",
        "503",
        "504",
        "temporarily unavailable",
        "connection reset",
    )
    return any(marker in lowered for marker in retry_markers)


def load_resume_state(output_dir: str | Path) -> ResumeState:
    base_dir = Path(output_dir)
    ensure_directory(base_dir)

    completed_log_ids: set[str] = set()
    non_conversation_log_ids: set[str] = set()
    session_counts: dict[str, int] = {}
    session_dirs: dict[str, Path] = {}

    state_path = base_dir / STATE_FILENAME
    if state_path.exists():
        try:
            state_payload = json.loads(state_path.read_text(encoding="utf-8"))
            completed_log_ids.update(state_payload.get("completed_log_ids") or [])
            non_conversation_log_ids.update(state_payload.get("non_conversation_log_ids") or [])
            session_counts.update(
                {
                    str(key): int(value)
                    for key, value in (state_payload.get("session_counts") or {}).items()
                }
            )
            session_dirs.update(
                {
                    str(key): base_dir / str(value)
                    for key, value in (state_payload.get("session_dirs") or {}).items()
                }
            )
        except (ValueError, TypeError):
            pass

    for file_path in sorted(base_dir.glob("*/*.json")):
        try:
            payload = json.loads(file_path.read_text(encoding="utf-8"))
        except (OSError, ValueError):
            continue
        session_id = payload.get("session_id")
        gateway_log = payload.get("gateway_log") or {}
        log_id = gateway_log.get("id")
        if isinstance(log_id, str) and log_id:
            completed_log_ids.add(log_id)
        if not isinstance(session_id, str) or not session_id:
            continue
        session_dir = file_path.parent
        session_dirs[session_id] = session_dir
        session_counts[session_id] = max(session_counts.get(session_id, 0), len(list(session_dir.glob("*.json"))))

    return ResumeState(
        completed_log_ids=completed_log_ids,
        non_conversation_log_ids=non_conversation_log_ids,
        session_counts=session_counts,
        session_dirs=session_dirs,
        written_files=sum(session_counts.values()),
        skipped_non_conversation=len(non_conversation_log_ids),
        skipped_errors=0,
    )


def save_resume_state(
    output_dir: str | Path,
    completed_log_ids: set[str],
    non_conversation_log_ids: set[str],
    session_counts: dict[str, int],
    session_dirs: dict[str, Path],
    skipped_errors: int,
) -> None:
    base_dir = Path(output_dir)
    ensure_directory(base_dir)
    state_path = base_dir / STATE_FILENAME
    payload = {
        "version": 1,
        "completed_log_ids": sorted(completed_log_ids),
        "non_conversation_log_ids": sorted(non_conversation_log_ids),
        "session_counts": session_counts,
        "session_dirs": {key: value.name for key, value in session_dirs.items()},
        "stats": {
            "written_files": sum(session_counts.values()),
            "skipped_non_conversation": len(non_conversation_log_ids),
            "skipped_errors": skipped_errors,
        },
    }
    tmp_path = state_path.with_suffix(".tmp")
    tmp_path.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    tmp_path.replace(state_path)


def parse_timestamp(value: str | None) -> datetime | None:
    if not value:
        return None
    normalized = value.strip()
    if not normalized:
        return None
    if normalized.endswith("Z"):
        normalized = normalized[:-1] + "+00:00"
    try:
        parsed = datetime.fromisoformat(normalized)
    except ValueError:
        return None
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=timezone.utc)
    return parsed


def format_timestamp(value: datetime | None) -> str | None:
    if value is None:
        return None
    return value.astimezone(timezone.utc).isoformat().replace("+00:00", "Z")


def derive_end_time(start_time: str | None, duration_ms: Any) -> str | None:
    start_dt = parse_timestamp(start_time)
    if start_dt is None:
        return None
    if not isinstance(duration_ms, (int, float)):
        return None
    return format_timestamp(start_dt + timedelta(milliseconds=float(duration_ms)))


def extract_first_string(candidates: list[Any]) -> str | None:
    for candidate in candidates:
        if isinstance(candidate, str):
            value = candidate.strip()
            if value:
                return value
    return None


def maybe_unwrap_api_result(payload: Any) -> Any:
    if isinstance(payload, dict) and set(payload.keys()) >= {"result", "success"} and len(payload) <= 4:
        return payload.get("result")
    return payload


def looks_like_conversation_payload(payload: Any, detail_payload: dict[str, Any]) -> bool:
    payload = maybe_unwrap_api_result(payload)
    if not isinstance(payload, dict):
        return False

    path = str(detail_payload.get("path") or "")
    if "event_logging" in path:
        return False
    if "count_tokens" in path:
        return False

    if isinstance(payload.get("messages"), list):
        return True
    if isinstance(payload.get("input"), list):
        return True
    if isinstance(payload.get("contents"), list):
        return True
    if isinstance(payload.get("system"), (list, str)):
        return True
    if isinstance(payload.get("tools"), list):
        return True
    if payload.get("model") and payload.get("role"):
        return True
    if isinstance(payload.get("choices"), list):
        return True

    return False


def extract_session_id(request_payload: Any, response_payload: Any, detail_payload: dict[str, Any], override: str | None) -> str:
    if override:
        return override

    request_obj = maybe_unwrap_api_result(request_payload)
    response_obj = maybe_unwrap_api_result(response_payload)
    metadata = request_obj.get("metadata") if isinstance(request_obj, dict) else None

    direct_candidates = [
        response_obj.get("session_id") if isinstance(response_obj, dict) else None,
        request_obj.get("session_id") if isinstance(request_obj, dict) else None,
        metadata.get("session_id") if isinstance(metadata, dict) else None,
    ]
    direct = extract_first_string(direct_candidates)
    if direct:
        return direct

    pattern_candidates = [
        metadata.get("user_id") if isinstance(metadata, dict) else None,
        detail_payload.get("metadata"),
    ]
    for candidate in pattern_candidates:
        if not isinstance(candidate, str):
            continue
        matched = SESSION_PATTERN.search(candidate)
        if matched:
            return matched.group(1)

    return "unknown-session"


def extract_user_id(request_payload: Any, detail_payload: dict[str, Any], override: str | None) -> str | None:
    if override:
        return override

    request_obj = maybe_unwrap_api_result(request_payload)
    metadata = request_obj.get("metadata") if isinstance(request_obj, dict) else None
    candidates = [
        request_obj.get("user_id") if isinstance(request_obj, dict) else None,
        metadata.get("user_id") if isinstance(metadata, dict) else None,
        detail_payload.get("metadata"),
    ]

    for candidate in candidates:
        if not isinstance(candidate, str):
            continue
        value = candidate.strip()
        if not value:
            continue
        trimmed = value
        for pattern in USER_SEGMENT_SPLIT_PATTERNS:
            if pattern in trimmed:
                trimmed = trimmed.split(pattern, 1)[0]
                break
        trimmed = trimmed.strip("_")
        return trimmed or value
    return None


def extract_user_agent(request_payload: Any, detail_payload: dict[str, Any]) -> str | None:
    request_obj = maybe_unwrap_api_result(request_payload)
    direct = request_obj.get("user_agent") if isinstance(request_obj, dict) else None
    if isinstance(direct, str) and direct.strip():
        return direct.strip()

    request_head = detail_payload.get("request_head")
    if not isinstance(request_head, str):
        return None

    for line in request_head.splitlines():
        if line.lower().startswith("user-agent:"):
            return line.split(":", 1)[1].strip()
    return None


def sanitize_component(value: str) -> str:
    sanitized = re.sub(r"[^A-Za-z0-9._-]+", "_", value).strip("._")
    return sanitized or "unknown"


def build_session_dir_name(session_id: str, user_id: str | None, directory_style: str) -> str:
    session_component = sanitize_component(session_id)
    if directory_style == "session-only":
        return session_component

    if user_id is None:
        return session_component

    user_component = sanitize_component(user_id)
    if directory_style == "user-session":
        return f"{user_component}_{session_component}"

    if session_component in user_component or len(user_component) > 96:
        return session_component
    return f"{user_component}_{session_component}"


def extract_status(request_payload: Any, response_payload: Any, detail_payload: dict[str, Any]) -> str:
    request_obj = maybe_unwrap_api_result(request_payload)
    response_obj = maybe_unwrap_api_result(response_payload)

    direct = request_obj.get("status") if isinstance(request_obj, dict) else None
    if isinstance(direct, str) and direct.strip():
        return direct.strip()

    if isinstance(detail_payload.get("success"), bool):
        return "success" if detail_payload["success"] else "error"

    if isinstance(response_obj, dict) and response_obj.get("error"):
        return "error"

    return "unknown"


def normalize_interaction(
    request_payload: Any,
    response_payload: Any,
    detail_payload: dict[str, Any],
    *,
    account_id: str | None = None,
    gateway_id: str | None = None,
    request_id_override: str | None = None,
    session_id_override: str | None = None,
    user_id_override: str | None = None,
    start_time_override: str | None = None,
    end_time_override: str | None = None,
) -> NormalizedInteraction:
    request_obj = maybe_unwrap_api_result(request_payload)
    response_obj = maybe_unwrap_api_result(response_payload)

    session_id = extract_session_id(request_obj, response_obj, detail_payload, session_id_override)
    user_id = extract_user_id(request_obj, detail_payload, user_id_override)

    request_id = extract_first_string(
        [
            request_id_override,
            request_obj.get("request_id") if isinstance(request_obj, dict) else None,
            detail_payload.get("id"),
            response_obj.get("request_id") if isinstance(response_obj, dict) else None,
            response_obj.get("id") if isinstance(response_obj, dict) else None,
        ]
    )

    start_time = extract_first_string(
        [
            start_time_override,
            request_obj.get("start_time") if isinstance(request_obj, dict) else None,
            detail_payload.get("created_at"),
        ]
    )

    end_time = extract_first_string(
        [
            end_time_override,
            request_obj.get("end_time") if isinstance(request_obj, dict) else None,
            derive_end_time(start_time, detail_payload.get("duration")),
        ]
    )

    status = extract_status(request_obj, response_obj, detail_payload)

    payload = {
        "request_id": request_id,
        "session_id": session_id,
        "user_id": user_id,
        "start_time": start_time,
        "end_time": end_time,
        "user_agent": extract_user_agent(request_obj, detail_payload),
        "call_type": extract_first_string(
            [
                request_obj.get("call_type") if isinstance(request_obj, dict) else None,
                detail_payload.get("request_type"),
                detail_payload.get("path"),
            ]
        ),
        "status": status,
        "provider": extract_first_string([detail_payload.get("provider")]),
        "model": extract_first_string(
            [
                response_obj.get("model") if isinstance(response_obj, dict) else None,
                request_obj.get("model") if isinstance(request_obj, dict) else None,
                detail_payload.get("model"),
            ]
        ),
        "system": request_obj.get("system", []) if isinstance(request_obj, dict) else [],
        "tools": request_obj.get("tools", []) if isinstance(request_obj, dict) else [],
        "messages": request_obj.get("messages", []) if isinstance(request_obj, dict) else [],
        "response": response_obj,
        "gateway_log": {
            "id": detail_payload.get("id"),
            "path": detail_payload.get("path"),
            "created_at": detail_payload.get("created_at"),
            "duration": detail_payload.get("duration"),
            "status_code": detail_payload.get("status_code"),
            "success": detail_payload.get("success"),
            "cached": detail_payload.get("cached"),
            "model_type": detail_payload.get("model_type"),
            "request_content_type": detail_payload.get("request_content_type"),
            "response_content_type": detail_payload.get("response_content_type"),
        },
        "source": {
            "type": "cloudflare_ai_gateway",
            "account_id": account_id,
            "gateway_id": gateway_id,
        },
    }

    return NormalizedInteraction(
        session_id=session_id,
        user_id=user_id,
        request_id=request_id,
        start_time=start_time,
        payload=payload,
    )


def sort_key(interaction: NormalizedInteraction) -> tuple[str, str]:
    start = interaction.start_time or ""
    request_id = interaction.request_id or ""
    return (start, request_id)


def ensure_directory(path: Path) -> None:
    path.mkdir(parents=True, exist_ok=True)


def write_session_exports(
    interactions: list[NormalizedInteraction],
    output_dir: str | Path,
    directory_style: str,
) -> list[Path]:
    base_dir = Path(output_dir)
    ensure_directory(base_dir)

    grouped: dict[str, list[NormalizedInteraction]] = defaultdict(list)
    for interaction in interactions:
        grouped[interaction.session_id].append(interaction)

    written_files: list[Path] = []
    for session_id, items in grouped.items():
        ordered = sorted(items, key=sort_key)
        session_dir_name = build_session_dir_name(session_id, ordered[0].user_id, directory_style)
        session_dir = base_dir / session_dir_name
        ensure_directory(session_dir)

        for index, item in enumerate(ordered, start=1):
            request_id = sanitize_component(item.request_id) if item.request_id else ""
            file_name = f"{index:04d}.json" if not request_id else f"{index:04d}_{request_id}.json"
            file_path = session_dir / file_name
            with file_path.open("w", encoding="utf-8") as handle:
                json.dump(item.payload, handle, ensure_ascii=False, indent=2)
                handle.write("\n")
            written_files.append(file_path)

    return written_files


def write_interaction(
    interaction: NormalizedInteraction,
    output_dir: str | Path,
    directory_style: str,
    session_counts: dict[str, int],
    session_dirs: dict[str, Path],
) -> Path:
    base_dir = Path(output_dir)
    ensure_directory(base_dir)

    session_dir = session_dirs.get(interaction.session_id)
    if session_dir is None:
        session_dir_name = build_session_dir_name(
            interaction.session_id,
            interaction.user_id,
            directory_style,
        )
        session_dir = base_dir / session_dir_name
        ensure_directory(session_dir)
        session_dirs[interaction.session_id] = session_dir

    session_counts[interaction.session_id] = session_counts.get(interaction.session_id, 0) + 1
    index = session_counts[interaction.session_id]
    request_id = sanitize_component(interaction.request_id) if interaction.request_id else ""
    file_name = f"{index:04d}.json" if not request_id else f"{index:04d}_{request_id}.json"
    file_path = session_dir / file_name
    with file_path.open("w", encoding="utf-8") as handle:
        json.dump(interaction.payload, handle, ensure_ascii=False, indent=2)
        handle.write("\n")
    return file_path


def fetch_gateway_mode(args: argparse.Namespace) -> int:
    token = os.environ.get(args.token_env)
    if not token:
        if WRANGLER_AUTH_CONFIG.exists():
            print(
                "Wrangler OAuth login was detected, but Cloudflare AI Gateway log endpoints may reject "
                "Wrangler OAuth tokens with 403 Authentication error. Please set a dedicated "
                f"{args.token_env} value with AI Gateway read permissions.",
                file=sys.stderr,
            )
        print(
            f"Missing Cloudflare API token in environment variable {args.token_env}.",
            file=sys.stderr,
        )
        return 2

    logs = iter_gateway_logs(
        account_id=args.account_id,
        gateway_id=args.gateway_id,
        token=token,
        per_page=args.per_page,
        direction=args.direction,
        start_date=args.start_date,
        end_date=args.end_date,
        path_substring=args.path_substring,
        max_logs=args.max_logs,
        include_errors=args.include_errors,
        http_timeout=args.http_timeout,
    )
    if args.skip_offset > 0:
        logs = logs[args.skip_offset :]

    if args.resume:
        resume_state = load_resume_state(args.output_dir)
    else:
        resume_state = ResumeState(
            completed_log_ids=set(),
            non_conversation_log_ids=set(),
            session_counts={},
            session_dirs={},
            written_files=0,
            skipped_non_conversation=0,
            skipped_errors=0,
        )

    filtered_logs: list[dict[str, Any]] = []
    skipped_completed = 0
    for log_item in logs:
        log_id = str(log_item.get("id") or "")
        if not log_id:
            filtered_logs.append(log_item)
            continue
        if log_id in resume_state.completed_log_ids or log_id in resume_state.non_conversation_log_ids:
            skipped_completed += 1
            continue
        filtered_logs.append(log_item)

    written_files = resume_state.written_files
    session_counts = dict(resume_state.session_counts)
    session_dirs = dict(resume_state.session_dirs)
    completed_log_ids = set(resume_state.completed_log_ids)
    non_conversation_log_ids = set(resume_state.non_conversation_log_ids)
    skipped_non_conversation = resume_state.skipped_non_conversation
    skipped_errors = resume_state.skipped_errors
    workers = max(1, args.workers)
    retry_attempts = max(1, args.retry_attempts)
    retry_backoff_seconds = max(0.1, args.retry_backoff_seconds)

    def process_log(log_item: dict[str, Any]) -> tuple[str, str | None, NormalizedInteraction | None]:
        log_id = str(log_item.get("id") or "")
        if not log_id:
            return ("missing-id", None, None)
        last_error: CloudflareAPIError | None = None
        for attempt in range(1, retry_attempts + 1):
            try:
                detail_payload = get_gateway_log_detail(
                    args.account_id, args.gateway_id, token, log_id, args.http_timeout
                )
                request_payload = get_gateway_log_request(
                    args.account_id, args.gateway_id, token, log_id, args.http_timeout
                )
                response_payload = get_gateway_log_response(
                    args.account_id, args.gateway_id, token, log_id, args.http_timeout
                )
                break
            except CloudflareAPIError as exc:
                last_error = exc
                if attempt >= retry_attempts or not is_retryable_error(str(exc)):
                    return ("error", f"{log_id}: {summarize_error_message(str(exc))}", None)
                time.sleep(retry_backoff_seconds * (2 ** (attempt - 1)))
        else:
            if last_error is not None:
                return ("error", f"{log_id}: {summarize_error_message(str(last_error))}", None)
            return ("error", f"{log_id}: unknown detail download failure", None)

        if not looks_like_conversation_payload(request_payload, detail_payload):
            return ("non-conversation", log_id, None)

        interaction = normalize_interaction(
            request_payload,
            response_payload,
            detail_payload,
            account_id=args.account_id,
            gateway_id=args.gateway_id,
        )
        return ("ok", log_id, interaction)

    save_every = 10
    state_updates = 0
    with ThreadPoolExecutor(max_workers=workers) as executor:
        for status, message, interaction in executor.map(process_log, filtered_logs):
            if status == "missing-id":
                continue
            if status == "error":
                skipped_errors += 1
                print(f"Skipping log {message}", file=sys.stderr)
                continue
            if status == "non-conversation":
                skipped_non_conversation += 1
                if message:
                    non_conversation_log_ids.add(message)
                    state_updates += 1
                continue
            if interaction is None:
                continue

            write_interaction(
                interaction=interaction,
                output_dir=args.output_dir,
                directory_style=args.directory_style,
                session_counts=session_counts,
                session_dirs=session_dirs,
            )
            gateway_log = interaction.payload.get("gateway_log") or {}
            log_id = gateway_log.get("id")
            if isinstance(log_id, str) and log_id:
                completed_log_ids.add(log_id)
            written_files += 1
            state_updates += 1
            if state_updates >= save_every:
                save_resume_state(
                    args.output_dir,
                    completed_log_ids,
                    non_conversation_log_ids,
                    session_counts,
                    session_dirs,
                    skipped_errors,
                )
                state_updates = 0
            if written_files % 25 == 0:
                print(
                    f"Progress: exported {written_files} interactions, "
                    f"{len(session_dirs)} sessions, skipped {skipped_non_conversation} non-conversation, "
                    f"{skipped_errors} failed-detail, {skipped_completed} already-completed.",
                    flush=True,
                )

    save_resume_state(
        args.output_dir,
        completed_log_ids,
        non_conversation_log_ids,
        session_counts,
        session_dirs,
        skipped_errors,
    )

    session_count = len(session_dirs)
    print(
        f"Exported {written_files} interactions into {session_count} session directories under {args.output_dir}. "
        f"Skipped {skipped_non_conversation} non-conversation logs, {skipped_errors} failed-detail logs, "
        f"and {skipped_completed} already-completed logs."
    )
    return 0


def normalize_pair_mode(args: argparse.Namespace) -> int:
    request_payload = load_json(args.request_file)
    response_payload = load_json(args.response_file)
    interaction = normalize_interaction(
        request_payload,
        response_payload,
        detail_payload={},
        request_id_override=args.request_id,
        session_id_override=args.session_id,
        user_id_override=args.user_id,
        start_time_override=args.start_time,
        end_time_override=args.end_time,
    )
    written_files = write_session_exports(
        interactions=[interaction],
        output_dir=args.output_dir,
        directory_style=args.directory_style,
    )
    print(f"Exported sample pair to {written_files[0]}")
    return 0


def main() -> int:
    load_local_env_if_present()
    args = parse_args()
    try:
        if args.command == "fetch-gateway":
            return fetch_gateway_mode(args)
        if args.command == "normalize-pair":
            return normalize_pair_mode(args)
        raise ValueError(f"Unsupported command: {args.command}")
    except CloudflareAPIError as exc:
        print(f"Cloudflare API error: {exc}", file=sys.stderr)
        return 1
    except FileNotFoundError as exc:
        print(f"File not found: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
