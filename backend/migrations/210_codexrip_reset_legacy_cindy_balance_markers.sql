-- Earlier Cindy balance classification accepted generic 402 responses and
-- incomplete 429 budget envelopes. Keep only markers backed by the exact
-- structured fields stored in the sanitized upstream response detail; do not
-- probe accounts or infer recovery from message text.

UPDATE accounts AS a
SET cindy_balance_insufficient_at = NULL
WHERE a.cindy_balance_insufficient_at IS NOT NULL
  AND a.platform = 'openai'
  AND a.type = 'apikey'
  AND jsonb_typeof(a.credentials->'base_url') = 'string'
  AND LOWER(BTRIM(a.credentials->>'base_url')) IN (
      'https://api.laxarouter.ai',
      'https://api.laxarouter.ai/'
  )
  AND NOT EXISTS (
      SELECT 1
      FROM ops_error_logs AS e
      CROSS JOIN LATERAL (
          SELECT CASE
              WHEN e.upstream_error_detail IS JSON
              THEN e.upstream_error_detail::jsonb
          END AS payload
      ) AS parsed
      WHERE e.account_id = a.id
        AND e.created_at BETWEEN a.cindy_balance_insufficient_at - INTERVAL '2 minutes'
                             AND a.cindy_balance_insufficient_at + INTERVAL '2 minutes'
        AND COALESCE(e.upstream_status_code, e.status_code) = 429
        AND jsonb_typeof(parsed.payload #> '{error,type}') = 'string'
        AND parsed.payload #>> '{error,type}' = 'budget_exceeded'
        AND jsonb_typeof(parsed.payload #> '{error,code}') = 'string'
        AND parsed.payload #>> '{error,code}' = '429'
  );
