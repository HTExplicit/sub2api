-- Remove the two per-account reasoning options from accounts.extra.
--
-- openai_chat_reasoning_replay_enabled switched Chat reasoning replay and
-- openai_reasoning_signature_recovery_enabled switched the recovery of invalid
-- reasoning ciphertext for one account (previous release, git 06e865f6e,
-- backend/internal: service/account.go:120-121, read by
-- service/plugin_codex_recovery.go:85-97). Replay is deleted. Recovery has one
-- global switch now (settings.reasoning_recovery_config, on while no row is
-- stored) and no account option, so nothing reads either key any more.
--
-- Only these two keys are removed, from every account row that holds one of
-- them, deleted accounts included. A stored value is not carried over to the
-- global switch. Rows that hold neither key are not written, and no other
-- table is touched.
--
-- accounts has no updated_at trigger. Writing extra fires the long-context
-- billing triggers of 175, which keep the stored flag and therefore queue no
-- scheduler_outbox event; the scheduler rebuild at startup reloads cached
-- account payloads, and its projection no longer lists the two keys. The
-- statement is a no-op once the keys are gone, so the migration can run again.
-- No explicit BEGIN/COMMIT: the runner owns this file's transaction.
SET LOCAL lock_timeout = '10s';
SET LOCAL statement_timeout = '120s';

UPDATE accounts
SET extra = extra - ARRAY['openai_chat_reasoning_replay_enabled', 'openai_reasoning_signature_recovery_enabled']
WHERE jsonb_typeof(extra) = 'object'
  AND extra ?| ARRAY['openai_chat_reasoning_replay_enabled', 'openai_reasoning_signature_recovery_enabled'];
