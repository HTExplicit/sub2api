-- Delete the system prompt binding of accounts that can never receive a prompt.
--
-- accounts.extra.system_prompt has an effect only where a request passes an
-- insertion point: the Messages, Chat Completions, Responses and Gemini
-- forwarders of the ten platforms in service.SystemPromptPlatforms
-- (service/system_prompt.go). An account on any other platform has none.
-- TypeSafe is the one such platform the account dialogs can create: System One
-- has no system or instructions field and its forwarder relays the request as
-- it is (service/gateway_systemone.go).
--
-- Such a row could still hold a binding. The binding endpoint wrote it to every
-- selected account whatever its platform (service/system_prompt.go SetBindings
-- before this release, reached from the account edit dialog, the row menu and
-- the bulk bar); account creation (data import and duplicate included), bulk
-- update and the extra merge stored a given key as it was
-- (service/admin_account.go before this release); and 259 turned a stored
-- prompt_skills "off" into {"mode": "off"} on any platform. It never changed a
-- request. This release writes no binding to these accounts, offers no control
-- for them and leaves them out of the counts of the system prompts page, so the
-- stored value is removed here once instead of being ignored in code.
--
-- Only the system_prompt key of those rows goes, soft-deleted rows included.
-- The system_prompts setting, every other extra key and every account on the
-- ten platforms are not touched; a row without the key is not written. The
-- platform list is the one of this release: a platform that gains an insertion
-- point later starts without bindings.
--
-- accounts has no updated_at trigger. Of the two extra triggers of 175, the
-- BEFORE one returns the row unchanged for a platform other than openai and the
-- AFTER one fires for openai only, so no other row is written and no
-- scheduler_outbox event is queued. Running the file again is a no-op.
-- No explicit BEGIN/COMMIT: the runner owns this file's transaction.
SET LOCAL lock_timeout = '10s';
SET LOCAL statement_timeout = '120s';

UPDATE accounts
SET extra = extra - 'system_prompt'
WHERE jsonb_typeof(extra) = 'object'
  AND extra ? 'system_prompt'
  AND platform NOT IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok',
                       'kimi', 'zhipu', 'deepseek', 'minimax', 'opencode_go');
