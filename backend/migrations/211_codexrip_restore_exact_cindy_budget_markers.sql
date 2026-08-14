-- Migration 210 intentionally removed legacy Cindy markers that were not
-- backed by exact structured evidence. Restore unresolved accounts from the
-- actual sanitized upstream JSON fields. A later successful usage record wins;
-- no account is probed and message text is never classified.

WITH cindy_accounts AS (
    SELECT a.id
    FROM accounts AS a
    WHERE a.deleted_at IS NULL
      AND a.platform = 'openai'
      AND a.type = 'apikey'
      AND jsonb_typeof(a.credentials->'base_url') = 'string'
      AND LOWER(BTRIM(a.credentials->>'base_url')) IN (
          'https://api.laxarouter.ai',
          'https://api.laxarouter.ai/'
      )
), candidate_logs AS (
    SELECT e.account_id,
           e.created_at,
           CASE
               WHEN e.upstream_error_detail IS JSON
               THEN e.upstream_error_detail::jsonb
           END AS payload
    FROM ops_error_logs AS e
    JOIN cindy_accounts AS a ON a.id = e.account_id
    WHERE COALESCE(e.upstream_status_code, e.status_code) = 429
), exact_latest AS (
    SELECT account_id, MAX(created_at) AS latest_exact_at
    FROM candidate_logs
    WHERE jsonb_typeof(payload #> '{error,type}') = 'string'
      AND payload #>> '{error,type}' = 'budget_exceeded'
      AND jsonb_typeof(payload #> '{error,code}') = 'string'
      AND payload #>> '{error,code}' = '429'
    GROUP BY account_id
), unresolved AS (
    SELECT x.account_id, x.latest_exact_at
    FROM exact_latest AS x
    WHERE NOT EXISTS (
        SELECT 1
        FROM usage_logs AS u
        WHERE u.account_id = x.account_id
          AND u.created_at > x.latest_exact_at
    )
)
UPDATE accounts AS a
SET cindy_balance_insufficient_at = u.latest_exact_at
FROM unresolved AS u
WHERE a.id = u.account_id
  AND a.cindy_balance_insufficient_at IS NULL;
