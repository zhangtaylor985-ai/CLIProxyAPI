#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="/root/cliapp/CLIProxyAPI"
STATUS_DIR="${REPO_ROOT}/logs/migrations"
STATUS_FILE="${STATUS_DIR}/session_trajectory_backup_monitor.status"
STATUS_JSON="${STATUS_DIR}/session_trajectory_backup_monitor.json"

mkdir -p "${STATUS_DIR}"
cd "${REPO_ROOT}"

set -a
source "${REPO_ROOT}/.env"
set +a

LOCAL_DSN="postgres://postgres:$(cat /root/.cliproxy_pg_postgres_password)@127.0.0.1:5432/cliproxy_archive?sslmode=disable"
TIMESTAMP="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

psql_local() {
  psql "${LOCAL_DSN}" -v ON_ERROR_STOP=1 -At "$@"
}

psql_source() {
  psql "${APIKEY_POLICY_PG_DSN}" -v ON_ERROR_STOP=1 -At "$@"
}

ACTIVE_UNIT="$(
  systemctl list-units --type=service --all --plain --no-legend 2>/dev/null \
    | awk '/session-trajectory-backup/ && $4 == "running" {print $1}' \
    | sort \
    | tail -n 1
)"

SOURCE_SESSIONS="$(psql_source -c "SELECT COUNT(*) FROM public.session_trajectory_sessions;")"
SOURCE_REQUESTS="$(psql_source -c "SELECT COUNT(*) FROM public.session_trajectory_requests;")"
SOURCE_ALIASES="$(psql_source -c "SELECT COUNT(*) FROM public.session_trajectory_session_aliases;")"
SOURCE_EXPORTS="$(psql_source -c "SELECT COUNT(*) FROM public.session_trajectory_request_exports;")"

ARCHIVE_SESSIONS="$(psql_local -c "SELECT COUNT(*) FROM public.session_trajectory_sessions;")"
ARCHIVE_REQUESTS="$(psql_local -c "SELECT COUNT(*) FROM public.session_trajectory_requests;")"
ARCHIVE_ALIASES="$(psql_local -c "SELECT COUNT(*) FROM public.session_trajectory_session_aliases;")"
ARCHIVE_EXPORTS="$(psql_local -c "SELECT COUNT(*) FROM public.session_trajectory_request_exports;")"

REQUEST_PERCENT="$(python3 - <<PY
source_requests = int("${SOURCE_REQUESTS}")
archive_requests = int("${ARCHIVE_REQUESTS}")
print(f"{(archive_requests / source_requests * 100) if source_requests else 100:.2f}")
PY
)"

REQUEST_REMAINING="$((SOURCE_REQUESTS - ARCHIVE_REQUESTS))"
SESSIONS_REMAINING="$((SOURCE_SESSIONS - ARCHIVE_SESSIONS))"
ALIASES_REMAINING="$((SOURCE_ALIASES - ARCHIVE_ALIASES))"
EXPORTS_REMAINING="$((SOURCE_EXPORTS - ARCHIVE_EXPORTS))"

CURRENT_QUERY="$(
  psql_local -c "
  SELECT regexp_replace(query, E'\\s+', ' ', 'g')
  FROM pg_stat_activity
  WHERE datname = 'cliproxy_archive'
    AND state = 'active'
    AND query LIKE '%session_trajectory_%'
  ORDER BY query_start
  LIMIT 1;
  " | tr -d '\n'
)"

CURRENT_QUERY_RUNNING_FOR="$(
  psql_local -c "
  SELECT COALESCE((now() - query_start)::text, '')
  FROM pg_stat_activity
  WHERE datname = 'cliproxy_archive'
    AND state = 'active'
    AND query LIKE '%session_trajectory_%'
  ORDER BY query_start
  LIMIT 1;
  " | tr -d '\n'
)"

STATUS="running"
if [[ -z "${ACTIVE_UNIT}" ]]; then
  if [[ "${REQUEST_REMAINING}" -le 0 && "${SESSIONS_REMAINING}" -le 0 && "${ALIASES_REMAINING}" -le 0 && "${EXPORTS_REMAINING}" -le 0 ]]; then
    STATUS="completed"
  else
    STATUS="attention"
  fi
fi

LAST_JOURNAL=""
if [[ -n "${ACTIVE_UNIT}" ]]; then
  LAST_JOURNAL="$(journalctl -u "${ACTIVE_UNIT}" --no-pager -n 8 2>/dev/null | tail -n 8 | tr '\n' '\t' | sed 's/[[:space:]]\+/ /g' | sed 's/"/\\"/g')"
else
  LAST_JOURNAL="$(journalctl --since '-4 hours' --no-pager 2>/dev/null | awk '/session-trajectory-backup/ { line = $0 } END { print line }' | sed 's/"/\\"/g')"
fi

cat > "${STATUS_FILE}" <<EOF
timestamp=${TIMESTAMP}
status=${STATUS}
active_unit=${ACTIVE_UNIT}
source_sessions=${SOURCE_SESSIONS}
archive_sessions=${ARCHIVE_SESSIONS}
source_requests=${SOURCE_REQUESTS}
archive_requests=${ARCHIVE_REQUESTS}
source_aliases=${SOURCE_ALIASES}
archive_aliases=${ARCHIVE_ALIASES}
source_exports=${SOURCE_EXPORTS}
archive_exports=${ARCHIVE_EXPORTS}
request_percent=${REQUEST_PERCENT}
request_remaining=${REQUEST_REMAINING}
sessions_remaining=${SESSIONS_REMAINING}
aliases_remaining=${ALIASES_REMAINING}
exports_remaining=${EXPORTS_REMAINING}
current_query=${CURRENT_QUERY}
current_query_running_for=${CURRENT_QUERY_RUNNING_FOR}
last_journal=${LAST_JOURNAL}
EOF

cat > "${STATUS_JSON}" <<EOF
{
  "timestamp": "${TIMESTAMP}",
  "status": "${STATUS}",
  "active_unit": "${ACTIVE_UNIT}",
  "source_sessions": ${SOURCE_SESSIONS},
  "archive_sessions": ${ARCHIVE_SESSIONS},
  "source_requests": ${SOURCE_REQUESTS},
  "archive_requests": ${ARCHIVE_REQUESTS},
  "source_aliases": ${SOURCE_ALIASES},
  "archive_aliases": ${ARCHIVE_ALIASES},
  "source_exports": ${SOURCE_EXPORTS},
  "archive_exports": ${ARCHIVE_EXPORTS},
  "request_percent": ${REQUEST_PERCENT},
  "request_remaining": ${REQUEST_REMAINING},
  "sessions_remaining": ${SESSIONS_REMAINING},
  "aliases_remaining": ${ALIASES_REMAINING},
  "exports_remaining": ${EXPORTS_REMAINING},
  "current_query": "${CURRENT_QUERY}",
  "current_query_running_for": "${CURRENT_QUERY_RUNNING_FOR}",
  "last_journal": "${LAST_JOURNAL}"
}
EOF

systemd-cat -t session-trajectory-backup-monitor <<EOF
timestamp=${TIMESTAMP} status=${STATUS} active_unit=${ACTIVE_UNIT} request_percent=${REQUEST_PERCENT} archive_requests=${ARCHIVE_REQUESTS} source_requests=${SOURCE_REQUESTS} request_remaining=${REQUEST_REMAINING}
EOF
