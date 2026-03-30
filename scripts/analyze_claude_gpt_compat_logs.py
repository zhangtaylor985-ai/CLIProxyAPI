#!/usr/bin/env python3
"""Summarize Claude Code vs GPT-in-Claude request logs.

This script is intentionally lightweight so operators can quickly compare:
- request timestamp
- first upstream response timestamp / TTFB
- upstream provider
- built-in web search event count
- reasoning summary delta count

Usage:
  python3 scripts/analyze_claude_gpt_compat_logs.py .cli-proxy-api/logs/v1-messages-*.log
"""

from __future__ import annotations

import datetime as dt
import pathlib
import re
import sys


REQUEST_TS_RE = re.compile(r"^Timestamp: (?P<ts>\S+)$")
UPSTREAM_RE = re.compile(r"^Auth: provider=(?P<provider>[^,]+)")


def parse_log(path: pathlib.Path) -> dict[str, object]:
    request_ts = None
    api_response_first_ts = None
    api_response_last_ts = None
    provider = ""
    web_search_calls = 0
    reasoning_deltas = 0

    lines = path.read_text(encoding="utf-8", errors="replace").splitlines()
    in_request_info = False
    in_api_request = False
    in_api_response = False

    for line in lines:
        if line.startswith("=== REQUEST INFO"):
            in_request_info = True
            in_api_request = False
            in_api_response = False
            continue
        if line.startswith("=== API REQUEST"):
            in_request_info = False
            in_api_request = True
            in_api_response = False
            continue
        if line.startswith("=== API RESPONSE"):
            in_request_info = False
            in_api_request = False
            in_api_response = True
            continue
        if line.startswith("=== ") and not line.startswith("=== API RESPONSE"):
            in_api_response = False

        if in_request_info and request_ts is None and line.startswith("Timestamp: "):
            match = REQUEST_TS_RE.match(line)
            if match:
                request_ts = dt.datetime.fromisoformat(match.group("ts"))
            continue

        if in_api_request and not provider:
            match = UPSTREAM_RE.match(line)
            if match:
                provider = match.group("provider")
                continue

        if in_api_response and line.startswith("Timestamp: "):
            match = REQUEST_TS_RE.match(line)
            if match:
                ts = dt.datetime.fromisoformat(match.group("ts"))
                if api_response_first_ts is None:
                    api_response_first_ts = ts
                api_response_last_ts = ts
            continue

        if '"type":"web_search_call"' in line:
            web_search_calls += 1
        if '"type":"response.reasoning_summary_text.delta"' in line:
            reasoning_deltas += 1

    first_ttfb_ms = None
    total_api_window_ms = None
    if request_ts and api_response_first_ts:
        first_ttfb_ms = int((api_response_first_ts - request_ts).total_seconds() * 1000)
    if request_ts and api_response_last_ts:
        total_api_window_ms = int((api_response_last_ts - request_ts).total_seconds() * 1000)

    return {
        "file": str(path),
        "provider": provider or "unknown",
        "request_ts": request_ts.isoformat() if request_ts else "",
        "api_response_first_ts": api_response_first_ts.isoformat() if api_response_first_ts else "",
        "api_response_last_ts": api_response_last_ts.isoformat() if api_response_last_ts else "",
        "first_ttfb_ms": first_ttfb_ms if first_ttfb_ms is not None else "",
        "total_api_window_ms": total_api_window_ms if total_api_window_ms is not None else "",
        "web_search_calls": web_search_calls,
        "reasoning_deltas": reasoning_deltas,
    }


def main(argv: list[str]) -> int:
    if len(argv) < 2:
        print("usage: python3 scripts/analyze_claude_gpt_compat_logs.py <log> [<log> ...]", file=sys.stderr)
        return 1

    for arg in argv[1:]:
        for match in sorted(pathlib.Path().glob(arg)):
            summary = parse_log(match)
            print(
                f"{summary['file']}\n"
                f"  provider: {summary['provider']}\n"
                f"  request_ts: {summary['request_ts']}\n"
                f"  api_response_first_ts: {summary['api_response_first_ts']}\n"
                f"  api_response_last_ts: {summary['api_response_last_ts']}\n"
                f"  first_ttfb_ms: {summary['first_ttfb_ms']}\n"
                f"  total_api_window_ms: {summary['total_api_window_ms']}\n"
                f"  web_search_calls: {summary['web_search_calls']}\n"
                f"  reasoning_deltas: {summary['reasoning_deltas']}"
            )
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
