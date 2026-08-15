-- Historical Cindy balance markers were created from one exact request-level
-- budget_exceeded event. That signal does not prove permanent account-wide
-- exhaustion, so only markers produced under that retired policy are reset.
-- Status, scheduling state, credentials, and all non-Cindy accounts remain
-- unchanged. New markers require independent control-model confirmation.
UPDATE accounts AS a
SET cindy_balance_insufficient_at = NULL
WHERE a.cindy_balance_insufficient_at IS NOT NULL
  AND a.platform = 'openai'
  AND a.type = 'apikey'
  AND jsonb_typeof(a.credentials->'base_url') = 'string'
  AND LOWER(BTRIM(a.credentials->>'base_url')) IN (
    'https://api.laxarouter.ai',
    'https://api.laxarouter.ai/'
  );
