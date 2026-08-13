-- Cindy's Azure-compatible upstream rejects prompt_cache_key values longer
-- than 64 characters. Keep direct alpha search, but normalize long cache keys
-- to a deterministic 64-character SHA-256 value for the strict Cindy root.

UPDATE accounts
SET extra = jsonb_set(
    COALESCE(extra, '{}'::jsonb),
    '{openai_prompt_cache_key_mode}',
    to_jsonb('sha256_64'::text),
    TRUE
)
WHERE platform = 'openai'
  AND type = 'apikey'
  AND jsonb_typeof(credentials->'base_url') = 'string'
  AND LOWER(BTRIM(credentials->>'base_url')) IN (
      'https://api.laxarouter.ai',
      'https://api.laxarouter.ai/'
  )
  AND extra->>'openai_prompt_cache_key_mode' IS DISTINCT FROM 'sha256_64';
