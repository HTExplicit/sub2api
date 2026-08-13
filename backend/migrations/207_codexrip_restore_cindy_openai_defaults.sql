-- Restore the native OpenAI request modes for the strictly identified Cindy
-- API-key upstream. Explicit administrator changes made after this migration
-- remain supported by the normal account update path.

UPDATE accounts
SET extra = jsonb_set(
    jsonb_set(
        COALESCE(extra, '{}'::jsonb),
        '{openai_alpha_search_mode}',
        to_jsonb('direct'::text),
        TRUE
    ),
    '{openai_prompt_cache_key_mode}',
    to_jsonb('passthrough'::text),
    TRUE
)
WHERE platform = 'openai'
  AND type = 'apikey'
  AND jsonb_typeof(credentials->'base_url') = 'string'
  AND LOWER(BTRIM(credentials->>'base_url')) IN (
      'https://api.laxarouter.ai',
      'https://api.laxarouter.ai/'
  )
  AND (
      extra->>'openai_alpha_search_mode' IS DISTINCT FROM 'direct'
      OR extra->>'openai_prompt_cache_key_mode' IS DISTINCT FROM 'passthrough'
  );
