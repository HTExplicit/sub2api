-- Earlier Cindy balance classification accepted generic 402 responses and
-- incomplete 429 budget envelopes. Keep only markers backed by the same
-- structured fields now required at runtime; do not probe accounts or infer
-- recovery from later traffic.

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
      WHERE e.account_id = a.id
        AND e.created_at BETWEEN a.cindy_balance_insufficient_at - INTERVAL '2 minutes'
                             AND a.cindy_balance_insufficient_at + INTERVAL '2 minutes'
        AND COALESCE(e.upstream_status_code, e.status_code) = 429
        AND e.provider_error_type = 'budget_exceeded'
        AND e.provider_error_code = '429'
  );
