#!/usr/bin/env bash
set -Eeuo pipefail

CLI_ROOT="${CLI_ROOT:-/root/cliapp/CLIProxyAPI}"
AUTH_DIR="${AUTH_DIR:-${CLI_ROOT}/auths}"
SUB2API_DB_CONTAINER="${SUB2API_DB_CONTAINER:-sub2api-postgres}"
SUB2API_DB_USER="${SUB2API_DB_USER:-sub2api}"
SUB2API_DB_NAME="${SUB2API_DB_NAME:-sub2api}"
BACKUP_ROOT="${BACKUP_ROOT:-$(dirname "${CLI_ROOT}")/CLIProxyAPI-migration-backups}"

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
backup_dir="${BACKUP_ROOT}/sub2api-auth-export-${timestamp}"
accounts_jsonl="${backup_dir}/sub2api-accounts.jsonl"

mkdir -p "${backup_dir}" "${AUTH_DIR}"
chmod 700 "${backup_dir}"

if [[ -d "${AUTH_DIR}" ]]; then
  cp -a "${AUTH_DIR}" "${backup_dir}/auths.before"
fi

docker exec -i "${SUB2API_DB_CONTAINER}" psql -U "${SUB2API_DB_USER}" -d "${SUB2API_DB_NAME}" -At <<'SQL' > "${accounts_jsonl}"
SELECT jsonb_build_object(
  'id', id,
  'name', name,
  'platform', platform,
  'type', type,
  'status', status,
  'concurrency', COALESCE(NULLIF(concurrency, 0), 10),
  'credentials', credentials,
  'extra', extra,
  'expires_at', expires_at,
  'updated_at', updated_at
)::text
FROM accounts
WHERE deleted_at IS NULL
  AND lower(platform) = 'openai'
  AND lower(type) IN ('oauth', 'codex', 'auth_file')
ORDER BY id;
SQL
chmod 600 "${accounts_jsonl}"

python3 - "${AUTH_DIR}" "${accounts_jsonl}" <<'PY'
import json
import os
import re
import sys

auth_dir = sys.argv[1]
accounts_path = sys.argv[2]
written = 0
skipped = 0

def clean_part(value: str) -> str:
    value = (value or "").strip().lower()
    value = re.sub(r"[^a-z0-9._+-]+", "-", value)
    value = value.strip("-._")
    return value or "account"

def find_existing_auth_path(email, account_id):
    wanted_email = (email or "").strip().lower()
    wanted_account_id = str(account_id or "").strip()
    try:
        names = sorted(os.listdir(auth_dir))
    except FileNotFoundError:
        return None
    for name in names:
        if not name.endswith(".json"):
            continue
        path = os.path.join(auth_dir, name)
        try:
            with open(path, "r", encoding="utf-8") as f:
                existing = json.load(f)
        except Exception:
            continue
        if str(existing.get("sub2api_account_id") or "").strip() == wanted_account_id and wanted_account_id:
            return path
        existing_email = str(existing.get("email") or "").strip().lower()
        if wanted_email and existing_email == wanted_email:
            return path
    return None

with open(accounts_path, "r", encoding="utf-8") as accounts_file:
  for line in accounts_file:
    line = line.strip()
    if not line:
      continue
    row = json.loads(line)
    credentials = row.get("credentials") or {}
    if not isinstance(credentials, dict):
        skipped += 1
        continue
    if not credentials.get("refresh_token") and not credentials.get("access_token"):
        skipped += 1
        continue

    email = str(credentials.get("email") or row.get("name") or f"account-{row.get('id')}")
    path = find_existing_auth_path(email, row.get("id"))
    if not path:
        filename = f"codex-{clean_part(email)}-{row.get('id')}.json"
        path = os.path.join(auth_dir, filename)

    status = str(row.get("status") or "").strip().lower()
    payload = dict(credentials)
    payload["type"] = "codex"
    payload["disabled"] = status != "active"
    payload["concurrency_limit"] = int(row.get("concurrency") or 10)
    payload["sub2api_account_id"] = row.get("id")
    payload["sub2api_account_name"] = row.get("name")
    payload["sub2api_account_status"] = row.get("status")
    if "account_id" not in payload and payload.get("chatgpt_account_id"):
        payload["account_id"] = payload["chatgpt_account_id"]
    if row.get("expires_at"):
        payload["account_expires_at"] = row["expires_at"]
    if row.get("extra"):
        payload["sub2api_extra"] = row["extra"]

    tmp = path + ".tmp"
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as f:
        json.dump(payload, f, ensure_ascii=False, indent=2, sort_keys=True)
        f.write("\n")
    os.replace(tmp, path)
    written += 1

print(f"exported_codex_auth_files={written} skipped_accounts={skipped}")
PY

printf 'backup_dir=%s\n' "${backup_dir}"
