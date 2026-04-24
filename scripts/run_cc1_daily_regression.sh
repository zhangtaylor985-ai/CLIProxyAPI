#!/bin/zsh
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUTPUT_ROOT="$REPO_ROOT/tasks/claude-client-compat/daily-regressions"
RUN_ID="$(date '+%Y-%m-%dT%H%M%S')"
RUN_DIR="$OUTPUT_ROOT/$RUN_ID"
SESSION_DIR="$HOME/.claude_local/projects/-Users-taylor-code-tools-CLIProxyAPI-ori"

mkdir -p "$RUN_DIR"
ln -sfn "$RUN_DIR" "$OUTPUT_ROOT/latest"

SUMMARY_MD="$RUN_DIR/summary.md"
TRANSCRIPT="$RUN_DIR/cc1-tty.transcript"
DEBUG_LOG="$RUN_DIR/cc1-tty.debug.log"
FILTERED_DEBUG="$RUN_DIR/cc1-tty.debug.filtered.log"
DEBUG_KEY_LINES="$RUN_DIR/debug-key-lines.log"
DRIVER_STDOUT="$RUN_DIR/driver.stdout.log"
DRIVER_STDERR="$RUN_DIR/driver.stderr.log"
PORT_LISTEN="$RUN_DIR/port-listen.txt"
SESSION_BEFORE="$RUN_DIR/session-before.txt"
SESSION_AFTER="$RUN_DIR/session-after.txt"
LATEST_SESSION="$RUN_DIR/latest-session.txt"

ERROR_REGEX='Failed to parse JSON|undefined is not an object|Unexpected end of JSON input|empty or malformed response \(HTTP 200\)|stream closed before response.completed'

PROMPT_1='Reply with exactly DAILY_TTY_OK'
PROMPT_2="先查看当前改动，然后运行 \`go test ./internal/translator/codex/claude -run 'TestConvertCodexResponseToClaude_OutputItemDoneMessageFallsBackToTextEvents|TestConvertCodexResponseToClaude_OutputItemDoneMessageDoesNotDuplicateDeltaStream|TestConvertClaudeRequestToCodex_PreservesUnknownToolResultContentBlocksAsInputText' -v\`，最后只用一句中文总结结果。"
PROMPT_3='继续，用一句话说明这轮为什么没出现 parse JSON。'

{
  echo "# 每日 cc1 回归"
  echo
  echo "- started_at: $(date '+%Y-%m-%d %H:%M:%S %z')"
  echo "- repo: $REPO_ROOT"
  echo "- git_rev: $(git -C "$REPO_ROOT" rev-parse HEAD)"
  echo "- run_id: $RUN_ID"
  echo
} > "$SUMMARY_MD"

if ! command -v python3 >/dev/null 2>&1; then
  {
    echo "## 结果"
    echo
    echo "- status: FAIL"
    echo "- reason: 本机未安装 python3，无法驱动真实 cc1 TTY 回归。"
  } >> "$SUMMARY_MD"
  exit 1
fi

if ! command -v tmux >/dev/null 2>&1; then
  {
    echo "## 结果"
    echo
    echo "- status: FAIL"
    echo "- reason: 本机未安装 tmux，无法驱动真实 cc1 TTY 回归。"
  } >> "$SUMMARY_MD"
  exit 1
fi

if ! zsh -ic 'alias cc1 >/dev/null 2>&1 || command -v cc1 >/dev/null 2>&1'; then
  {
    echo "## 结果"
    echo
    echo "- status: FAIL"
    echo "- reason: 当前 shell 未找到 cc1 入口。"
  } >> "$SUMMARY_MD"
  exit 1
fi

if ! lsof -nP -iTCP:53841 -sTCP:LISTEN > "$PORT_LISTEN" 2>&1; then
  {
    echo "## 结果"
    echo
    echo "- status: FAIL"
    echo "- reason: 本地代理端口 53841 未监听，未执行 cc1 回归。"
  } >> "$SUMMARY_MD"
  exit 1
fi

ls -1t "$SESSION_DIR"/*.jsonl 2>/dev/null | head -n 10 > "$SESSION_BEFORE" || true

DRIVER_EXIT=0
if ! "$REPO_ROOT/scripts/cc1_tty_regression.expect" \
  "$REPO_ROOT" \
  "$DEBUG_LOG" \
  "$TRANSCRIPT" \
  "$PROMPT_1" \
  "$PROMPT_2" \
  "$PROMPT_3" >"$DRIVER_STDOUT" 2>"$DRIVER_STDERR"; then
  DRIVER_EXIT=$?
fi

ls -1t "$SESSION_DIR"/*.jsonl 2>/dev/null | head -n 10 > "$SESSION_AFTER" || true
comm -13 <(sort "$SESSION_BEFORE" 2>/dev/null || true) <(sort "$SESSION_AFTER" 2>/dev/null || true) | head -n 1 > "$LATEST_SESSION" || true
if [[ ! -s "$LATEST_SESSION" ]]; then
  head -n 1 "$SESSION_AFTER" > "$LATEST_SESSION" || true
fi

awk '/\[API REQUEST\] \/v1\/messages/{seen=1} seen{print}' "$DEBUG_LOG" > "$FILTERED_DEBUG" || true
rg -n "\\[API REQUEST\\]|Stream started|Hook Stop|$ERROR_REGEX" "$FILTERED_DEBUG" -S > "$DEBUG_KEY_LINES" || true

REQUEST_COUNT="$(grep -c "\\[API REQUEST\\] /v1/messages" "$FILTERED_DEBUG" 2>/dev/null || true)"
MAIN_REQUEST_COUNT="$(grep -c "\\[API REQUEST\\] /v1/messages source=repl_main_thread" "$FILTERED_DEBUG" 2>/dev/null || true)"
STREAM_COUNT="$(grep -c "Stream started - received first chunk" "$FILTERED_DEBUG" 2>/dev/null || true)"
STOP_COUNT="$(grep -c "Hook Stop (Stop) success" "$FILTERED_DEBUG" 2>/dev/null || true)"
SUBMIT_COUNT="$(grep -c "Hook UserPromptSubmit (UserPromptSubmit) success:" "$DEBUG_LOG" 2>/dev/null || true)"
SMOKE_OK="no"
if rg -q 'DAILY_TTY_OK' "$TRANSCRIPT" 2>/dev/null; then
  SMOKE_OK="yes"
fi

HAS_ERROR=0
if rg -n "$ERROR_REGEX" "$FILTERED_DEBUG" -S > "$RUN_DIR/error-matches.log" 2>/dev/null; then
  HAS_ERROR=1
fi

STATUS="PASS"
FAIL_REASON=""

if [[ "$DRIVER_EXIT" -ne 0 ]]; then
  STATUS="FAIL"
  FAIL_REASON="TTY 驱动退出码为 $DRIVER_EXIT"
elif [[ "$MAIN_REQUEST_COUNT" -lt 3 ]]; then
  STATUS="FAIL"
  FAIL_REASON="过滤后的 debug-file 里 repl_main_thread 请求次数少于 3"
elif [[ "$STREAM_COUNT" -lt 3 ]]; then
  STATUS="FAIL"
  FAIL_REASON="过滤后的 debug-file 里 Stream started 次数少于 3"
elif [[ "$STOP_COUNT" -lt 3 ]]; then
  STATUS="FAIL"
  FAIL_REASON="过滤后的 debug-file 里 Hook Stop 次数少于 3"
elif [[ "$HAS_ERROR" -ne 0 ]]; then
  STATUS="FAIL"
  FAIL_REASON="过滤后的 debug-file 里命中了已知错误关键字"
elif [[ "$SMOKE_OK" != "yes" ]]; then
  STATUS="FAIL"
  FAIL_REASON="transcript 里没有看到 DAILY_TTY_OK"
fi

{
  echo "## 结果"
  echo
  echo "- status: $STATUS"
  if [[ -n "$FAIL_REASON" ]]; then
    echo "- reason: $FAIL_REASON"
  fi
  echo
  echo "## 关键指标"
  echo
  echo "- driver_exit: $DRIVER_EXIT"
  echo "- user_submit_count: $SUBMIT_COUNT"
  echo "- request_count: $REQUEST_COUNT"
  echo "- repl_main_thread_request_count: $MAIN_REQUEST_COUNT"
  echo "- stream_started_count: $STREAM_COUNT"
  echo "- hook_stop_count: $STOP_COUNT"
  echo "- smoke_ok: $SMOKE_OK"
  echo "- latest_session: $(cat "$LATEST_SESSION" 2>/dev/null || echo 'N/A')"
  echo
  echo "## 产物"
  echo
  echo "- transcript: $TRANSCRIPT"
  echo "- debug_log: $DEBUG_LOG"
  echo "- filtered_debug: $FILTERED_DEBUG"
  echo "- debug_key_lines: $DEBUG_KEY_LINES"
  echo "- driver_stdout: $DRIVER_STDOUT"
  echo "- driver_stderr: $DRIVER_STDERR"
  echo "- port_listen: $PORT_LISTEN"
} >> "$SUMMARY_MD"

if [[ "$STATUS" != "PASS" ]]; then
  exit 1
fi
