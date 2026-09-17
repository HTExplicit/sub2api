-- Codex fingerprint convergence now defaults to "device" for OpenAI OAuth-like accounts:
-- one ChatGPT subscription must present a single installation to the upstream even when
-- several downstream users share it. Accounts that never chose a mode are written as
-- "device" explicitly; an explicit "off" is preserved. Every such account also gets the
-- system-managed seed that device convergence and the per-account Codex TUI identity
-- derive from. Idempotent: valid canonical seeds are preserved on rerun.
UPDATE accounts
SET extra = jsonb_set(
    COALESCE(extra, '{}'::jsonb),
    '{codex_fingerprint_mode}',
    '"device"'::jsonb,
    true
)
WHERE deleted_at IS NULL
  AND platform = 'openai'
  AND type IN ('oauth', 'setup_token')
  AND COALESCE(btrim(extra->>'codex_fingerprint_mode'), '') = '';

UPDATE accounts
SET extra = jsonb_set(
    COALESCE(extra, '{}'::jsonb),
    '{codex_fingerprint_seed}',
    to_jsonb(gen_random_uuid()::text),
    true
)
WHERE deleted_at IS NULL
  AND platform = 'openai'
  AND type IN ('oauth', 'setup_token')
  AND (
      extra->>'codex_fingerprint_seed' IS NULL
      OR btrim(extra->>'codex_fingerprint_seed') = ''
      OR NOT (
          extra->>'codex_fingerprint_seed' ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
          AND extra->>'codex_fingerprint_seed' <> '00000000-0000-0000-0000-000000000000'
      )
  );
