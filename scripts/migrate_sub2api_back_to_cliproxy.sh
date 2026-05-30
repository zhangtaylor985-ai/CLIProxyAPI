#!/usr/bin/env bash
set -Eeuo pipefail

CLI_ROOT="${CLI_ROOT:-/root/cliapp/CLIProxyAPI}"
SUB2API_DB_CONTAINER="${SUB2API_DB_CONTAINER:-sub2api-postgres}"
SUB2API_DB_USER="${SUB2API_DB_USER:-sub2api}"
SUB2API_DB_NAME="${SUB2API_DB_NAME:-sub2api}"
BACKUP_ROOT="${BACKUP_ROOT:-${CLI_ROOT}/migration-backups}"
MIGRATION_TAG="${MIGRATION_TAG:-sub2api-return-$(date -u +%Y%m%dT%H%M%SZ)}"
DRY_RUN="${DRY_RUN:-0}"

cd "${CLI_ROOT}"

if [[ -f .env ]]; then
  set -a
  # shellcheck disable=SC1091
  source .env
  set +a
fi

TARGET_DSN="${APIKEY_POLICY_PG_DSN:-${APIKEY_BILLING_PG_DSN:-${PGSTORE_DSN:-}}}"
TARGET_SCHEMA="${APIKEY_POLICY_PG_SCHEMA:-${APIKEY_BILLING_PG_SCHEMA:-${PGSTORE_SCHEMA:-public}}}"
TARGET_SCHEMA="${TARGET_SCHEMA:-public}"

if [[ -z "${TARGET_DSN}" ]]; then
  printf 'migrate_sub2api_back_to_cliproxy: APIKEY_POLICY_PG_DSN/APIKEY_BILLING_PG_DSN/PGSTORE_DSN is required\n' >&2
  exit 1
fi

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
work_dir="${BACKUP_ROOT}/sub2api-return-${timestamp}"
mkdir -p "${work_dir}"
chmod 700 "${work_dir}"

api_keys_jsonl="${work_dir}/sub2api-api-keys.jsonl"
usage_jsonl="${work_dir}/sub2api-usage-logs-new.jsonl"
target_backup="${work_dir}/cliproxy-policy-before.dump"
sub2api_backup="${work_dir}/sub2api-source-before.dump"

pg_dump "${TARGET_DSN}" -Fc -f "${target_backup}" \
  -t "${TARGET_SCHEMA}.api_key_config_entries" \
  -t "${TARGET_SCHEMA}.api_key_groups" \
  -t "${TARGET_SCHEMA}.usage_events" \
  -t "${TARGET_SCHEMA}.api_key_model_daily_usage"
chmod 600 "${target_backup}"

docker exec -i "${SUB2API_DB_CONTAINER}" pg_dump -U "${SUB2API_DB_USER}" -d "${SUB2API_DB_NAME}" -Fc \
  -t api_keys -t groups -t usage_logs -t accounts \
  -t cliproxy_legacy_api_key_migration -t cliproxy_legacy_group_migration -t cliproxy_legacy_usage_event_migration \
  > "${sub2api_backup}"
chmod 600 "${sub2api_backup}"

go run ./scripts/migrate_api_key_concurrency_pg -pg-dsn "${TARGET_DSN}" -pg-schema "${TARGET_SCHEMA}"

docker exec -i "${SUB2API_DB_CONTAINER}" psql -U "${SUB2API_DB_USER}" -d "${SUB2API_DB_NAME}" -At <<'SQL' > "${api_keys_jsonl}"
SELECT jsonb_build_object(
  'id', k.id,
  'api_key', k.key,
  'name', k.name,
  'status', k.status,
  'created_at', k.created_at,
  'updated_at', k.updated_at,
  'deleted_at', k.deleted_at,
  'expires_at', k.expires_at,
  'rate_limit_1d', k.rate_limit_1d,
  'rate_limit_7d', k.rate_limit_7d,
  'quota', k.quota,
  'quota_used', k.quota_used,
  'group_id', k.group_id,
  'group_name', g.name,
  'group_subscription_type', g.subscription_type,
  'group_is_exclusive', g.is_exclusive,
  'legacy_policy', l.source_policy_json,
  'token_packages', l.token_packages,
  'owner_username', l.source_owner_username,
  'owner_role', l.source_owner_role
)::text
FROM api_keys k
LEFT JOIN groups g ON g.id = k.group_id
LEFT JOIN cliproxy_legacy_api_key_migration l ON l.api_key_id = k.id
WHERE k.key IS NOT NULL AND btrim(k.key) <> ''
  AND k.deleted_at IS NULL
ORDER BY k.id;
SQL
chmod 600 "${api_keys_jsonl}"

docker exec -i "${SUB2API_DB_CONTAINER}" psql -U "${SUB2API_DB_USER}" -d "${SUB2API_DB_NAME}" -At <<'SQL' > "${usage_jsonl}"
SELECT jsonb_build_object(
  'source_usage_log_id', u.id,
  'api_key', k.key,
  'requested_at', EXTRACT(EPOCH FROM u.created_at)::bigint,
  'day', to_char(u.created_at AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD'),
  'model', COALESCE(NULLIF(u.requested_model, ''), NULLIF(u.upstream_model, ''), NULLIF(u.model, ''), 'unknown'),
  'source', 'sub2api',
  'auth_index', CASE WHEN u.account_id IS NULL THEN '' ELSE 'sub2api-account-' || u.account_id::text END,
  'failed', false,
  'latency_ms', COALESCE(u.duration_ms, 0),
  'input_tokens', COALESCE(u.input_tokens, 0),
  'output_tokens', COALESCE(u.output_tokens, 0) + COALESCE(u.image_output_tokens, 0),
  'reasoning_tokens', 0,
  'cached_tokens', COALESCE(u.cache_read_tokens, 0),
  'total_tokens',
    COALESCE(u.input_tokens, 0) +
    COALESCE(u.output_tokens, 0) +
    COALESCE(u.cache_creation_tokens, 0) +
    COALESCE(u.cache_read_tokens, 0) +
    COALESCE(u.cache_creation_5m_tokens, 0) +
    COALESCE(u.cache_creation_1h_tokens, 0) +
    COALESCE(u.image_output_tokens, 0),
  'cost_micro_usd', round(COALESCE(u.total_cost, 0) * 1000000)::bigint
)::text
FROM usage_logs u
JOIN api_keys k ON k.id = u.api_key_id
LEFT JOIN cliproxy_legacy_usage_event_migration m ON m.usage_log_id = u.id
WHERE k.key IS NOT NULL AND btrim(k.key) <> ''
  AND u.created_at IS NOT NULL
  AND m.usage_log_id IS NULL
ORDER BY u.id;
SQL
chmod 600 "${usage_jsonl}"

psql "${TARGET_DSN}" \
  -v ON_ERROR_STOP=1 \
  -v target_schema="${TARGET_SCHEMA}" \
  -v api_keys_file="${api_keys_jsonl}" \
  -v usage_file="${usage_jsonl}" \
  -v migration_tag="${MIGRATION_TAG}" \
  -v dry_run="${DRY_RUN}" <<'SQL'
SET search_path TO :"target_schema", public;
BEGIN;
SELECT set_config('sub2api.migration_tag', :'migration_tag', true);

CREATE TABLE IF NOT EXISTS sub2api_api_key_migration (
  source_api_key_id BIGINT PRIMARY KEY,
  api_key TEXT NOT NULL,
  migration_tag TEXT NOT NULL,
  migrated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sub2api_usage_log_migration (
  source_usage_log_id BIGINT PRIMARY KEY,
  target_usage_event_id BIGINT NOT NULL,
  api_key TEXT NOT NULL,
  migration_tag TEXT NOT NULL,
  migrated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE api_key_groups
  ADD COLUMN IF NOT EXISTS concurrency_limit INTEGER NOT NULL DEFAULT 0;

INSERT INTO api_key_groups (id, name, daily_budget_micro_usd, weekly_budget_micro_usd, concurrency_limit, is_system)
VALUES
  ('dedicated', '独享车', 300000000, 1000000000, 3, true),
  ('double', '双人车', 150000000, 500000000, 3, true),
  ('triple', '三人车', 100000000, 300000000, 2, true),
  ('quad', '四人车', 60000000, 250000000, 1, true)
ON CONFLICT (id) DO UPDATE SET
  name = EXCLUDED.name,
  daily_budget_micro_usd = EXCLUDED.daily_budget_micro_usd,
  weekly_budget_micro_usd = EXCLUDED.weekly_budget_micro_usd,
  concurrency_limit = EXCLUDED.concurrency_limit,
  is_system = EXCLUDED.is_system,
  updated_at = now();

CREATE TEMP TABLE sub2api_api_key_stage (payload jsonb) ON COMMIT DROP;
\copy sub2api_api_key_stage(payload) FROM :'api_keys_file'

WITH rows AS (
  SELECT
    (payload->>'id')::bigint AS source_api_key_id,
    btrim(payload->>'api_key') AS api_key,
    NULLIF(btrim(COALESCE(payload->>'name', '')), '') AS name,
    lower(COALESCE(payload->>'status', '')) AS status,
    NULLIF(payload->>'created_at', '')::timestamptz AS created_at,
    NULLIF(payload->>'updated_at', '')::timestamptz AS updated_at,
    NULLIF(payload->>'deleted_at', '')::timestamptz AS deleted_at,
    NULLIF(payload->>'expires_at', '')::timestamptz AS expires_at,
    COALESCE((payload->>'rate_limit_1d')::numeric, 0) AS rate_limit_1d,
    COALESCE((payload->>'rate_limit_7d')::numeric, 0) AS rate_limit_7d,
    COALESCE(NULLIF(payload->>'group_name', ''), '') AS group_name,
    COALESCE(NULLIF(payload->>'group_subscription_type', ''), '') AS group_subscription_type,
    COALESCE((payload->>'group_is_exclusive')::boolean, false) AS group_is_exclusive,
    CASE WHEN jsonb_typeof(payload->'legacy_policy') = 'object' THEN payload->'legacy_policy' ELSE '{}'::jsonb END AS legacy_policy,
    CASE WHEN jsonb_typeof(payload->'token_packages') = 'array' THEN payload->'token_packages' ELSE NULL END AS token_packages,
    COALESCE(NULLIF(payload->>'owner_username', ''), 'admin') AS owner_username,
    COALESCE(NULLIF(payload->>'owner_role', ''), 'admin') AS owner_role
  FROM sub2api_api_key_stage
  WHERE btrim(COALESCE(payload->>'api_key', '')) <> ''
), mapped AS (
  SELECT
    rows.*,
    CASE
      WHEN lower(group_name) ~ '(四人|4人|quad)' THEN 'quad'
      WHEN lower(group_name) ~ '(三人|3人|triple)' THEN 'triple'
      WHEN lower(group_name) ~ '(双人|二人|2人|double)' THEN 'double'
      WHEN group_is_exclusive OR lower(group_name) ~ '(独享|exclusive|dedicated)' OR lower(group_subscription_type) ~ '(exclusive|dedicated)' THEN 'dedicated'
      ELSE ''
    END AS mapped_group_id
  FROM rows
), prepared AS (
  SELECT
    source_api_key_id,
    api_key,
    created_at,
    updated_at,
    expires_at,
    (status <> 'active' OR deleted_at IS NOT NULL) AS disabled,
    owner_username,
    owner_role,
    jsonb_strip_nulls(
      legacy_policy ||
      jsonb_build_object(
        'api-key', api_key,
        'name', COALESCE(name, ''),
        'group-id', mapped_group_id,
        'concurrency-limit', CASE
          WHEN COALESCE(legacy_policy->>'concurrency-limit', '') ~ '^[0-9]+$' THEN (legacy_policy->>'concurrency-limit')::int
          ELSE 0
        END,
        'daily-budget-usd', CASE WHEN mapped_group_id = '' THEN rate_limit_1d ELSE 0 END,
        'weekly-budget-usd', CASE WHEN mapped_group_id = '' THEN rate_limit_7d ELSE 0 END,
        'token-packages', COALESCE(token_packages, legacy_policy->'token-packages', '[]'::jsonb)
      )
    ) AS policy_json
  FROM mapped
)
INSERT INTO api_key_config_entries (
  api_key, policy_json, created_at, expires_at, disabled, owner_username, owner_role, updated_at
)
SELECT
  api_key,
  policy_json,
  COALESCE(created_at, now()),
  expires_at,
  disabled,
  owner_username,
  owner_role,
  COALESCE(updated_at, now())
FROM prepared
ON CONFLICT (api_key) DO UPDATE SET
  policy_json = EXCLUDED.policy_json,
  created_at = EXCLUDED.created_at,
  expires_at = EXCLUDED.expires_at,
  disabled = EXCLUDED.disabled,
  owner_username = EXCLUDED.owner_username,
  owner_role = EXCLUDED.owner_role,
  updated_at = now();

INSERT INTO sub2api_api_key_migration (source_api_key_id, api_key, migration_tag, migrated_at)
SELECT (payload->>'id')::bigint, btrim(payload->>'api_key'), :'migration_tag', now()
FROM sub2api_api_key_stage
WHERE btrim(COALESCE(payload->>'api_key', '')) <> ''
ON CONFLICT (source_api_key_id) DO UPDATE SET
  api_key = EXCLUDED.api_key,
  migration_tag = EXCLUDED.migration_tag,
  migrated_at = now();

CREATE TEMP TABLE sub2api_usage_stage (payload jsonb) ON COMMIT DROP;
\copy sub2api_usage_stage(payload) FROM :'usage_file'

DO $$
DECLARE
  rec record;
  target_id bigint;
  day_key text;
BEGIN
  FOR rec IN
    SELECT
      (payload->>'source_usage_log_id')::bigint AS source_usage_log_id,
      btrim(payload->>'api_key') AS api_key,
      COALESCE((payload->>'requested_at')::bigint, 0) AS requested_at,
      COALESCE(NULLIF(payload->>'day', ''), to_char(to_timestamp(COALESCE((payload->>'requested_at')::bigint, 0)) AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD')) AS day,
      lower(COALESCE(NULLIF(payload->>'model', ''), 'unknown')) AS model,
      COALESCE(NULLIF(payload->>'source', ''), 'sub2api') AS source,
      COALESCE(payload->>'auth_index', '') AS auth_index,
      COALESCE((payload->>'failed')::boolean, false) AS failed,
      GREATEST(COALESCE((payload->>'latency_ms')::bigint, 0), 0) AS latency_ms,
      GREATEST(COALESCE((payload->>'input_tokens')::bigint, 0), 0) AS input_tokens,
      GREATEST(COALESCE((payload->>'output_tokens')::bigint, 0), 0) AS output_tokens,
      GREATEST(COALESCE((payload->>'reasoning_tokens')::bigint, 0), 0) AS reasoning_tokens,
      GREATEST(COALESCE((payload->>'cached_tokens')::bigint, 0), 0) AS cached_tokens,
      GREATEST(COALESCE((payload->>'total_tokens')::bigint, 0), 0) AS total_tokens,
      GREATEST(COALESCE((payload->>'cost_micro_usd')::bigint, 0), 0) AS cost_micro_usd
    FROM sub2api_usage_stage
    WHERE btrim(COALESCE(payload->>'api_key', '')) <> ''
  LOOP
    IF EXISTS (SELECT 1 FROM sub2api_usage_log_migration WHERE source_usage_log_id = rec.source_usage_log_id) THEN
      CONTINUE;
    END IF;

    INSERT INTO usage_events (
      requested_at, api_key, source, auth_index, model, failed, latency_ms,
      input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens, cost_micro_usd, updated_at
    )
    VALUES (
      rec.requested_at, rec.api_key, rec.source, rec.auth_index, rec.model, rec.failed, rec.latency_ms,
      rec.input_tokens, rec.output_tokens, rec.reasoning_tokens, rec.cached_tokens, rec.total_tokens, rec.cost_micro_usd,
      EXTRACT(EPOCH FROM now())::bigint
    )
    RETURNING id INTO target_id;

    INSERT INTO sub2api_usage_log_migration (source_usage_log_id, target_usage_event_id, api_key, migration_tag, migrated_at)
    VALUES (rec.source_usage_log_id, target_id, rec.api_key, current_setting('sub2api.migration_tag'), now());

    day_key := rec.day;
    INSERT INTO api_key_model_daily_usage (
      api_key, model, day, requests, failed_requests, input_tokens, output_tokens,
      reasoning_tokens, cached_tokens, total_tokens, cost_micro_usd, updated_at
    )
    VALUES (
      rec.api_key, rec.model, day_key, 1, CASE WHEN rec.failed THEN 1 ELSE 0 END,
      rec.input_tokens, rec.output_tokens, rec.reasoning_tokens, rec.cached_tokens, rec.total_tokens, rec.cost_micro_usd,
      EXTRACT(EPOCH FROM now())::bigint
    )
    ON CONFLICT (api_key, model, day) DO UPDATE SET
      requests = api_key_model_daily_usage.requests + EXCLUDED.requests,
      failed_requests = api_key_model_daily_usage.failed_requests + EXCLUDED.failed_requests,
      input_tokens = api_key_model_daily_usage.input_tokens + EXCLUDED.input_tokens,
      output_tokens = api_key_model_daily_usage.output_tokens + EXCLUDED.output_tokens,
      reasoning_tokens = api_key_model_daily_usage.reasoning_tokens + EXCLUDED.reasoning_tokens,
      cached_tokens = api_key_model_daily_usage.cached_tokens + EXCLUDED.cached_tokens,
      total_tokens = api_key_model_daily_usage.total_tokens + EXCLUDED.total_tokens,
      cost_micro_usd = api_key_model_daily_usage.cost_micro_usd + EXCLUDED.cost_micro_usd,
      updated_at = EXCLUDED.updated_at;
  END LOOP;
END $$;

SELECT 'api_keys_staged=' || count(*) FROM sub2api_api_key_stage;
SELECT 'new_usage_logs_staged=' || count(*) FROM sub2api_usage_stage;
SELECT 'usage_logs_imported_total=' || count(*) FROM sub2api_usage_log_migration WHERE migration_tag = :'migration_tag';

\if :dry_run
ROLLBACK;
SELECT 'dry_run=rolled_back';
\else
COMMIT;
SELECT 'committed=1';
\endif
SQL

printf 'work_dir=%s\n' "${work_dir}"
