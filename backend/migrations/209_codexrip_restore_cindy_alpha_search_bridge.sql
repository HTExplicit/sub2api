-- Production history confirms successful Cindy alpha-search requests while the
-- account-specific Responses web-search bridge was active. Restore that mode
-- for the strict Cindy root; bridge capability failures remain request-scoped
-- and may switch accounts without changing account health.

UPDATE accounts
SET extra = jsonb_set(
    COALESCE(extra, '{}'::jsonb),
    '{openai_alpha_search_mode}',
    to_jsonb('responses_web_search'::text),
    TRUE
)
WHERE platform = 'openai'
  AND type = 'apikey'
  AND jsonb_typeof(credentials->'base_url') = 'string'
  AND LOWER(BTRIM(credentials->>'base_url')) IN (
      'https://api.laxarouter.ai',
      'https://api.laxarouter.ai/'
  )
  AND extra->>'openai_alpha_search_mode' IS DISTINCT FROM 'responses_web_search';
