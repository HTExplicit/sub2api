-- Delete the two retired OpenAI API-key compatibility settings.
--
-- openai_apikey_alpha_search_responses_bridge_enabled and
-- openai_apikey_prompt_cache_key_normalization_enabled were the global switches
-- of the API-key Alpha Search bridge and of prompt_cache_key normalization.
-- Their gates and write fields were removed on 2026-09-01 (git cce166e21). The
-- previous release still loaded both rows, showed them read-only on the
-- settings page and wrote the stored value back on every save
-- (v0.2.11-codexrip.9, git 291d25e7f, backend/internal:
-- service/domain_constants.go:247-248, service/setting_update.go:469-470,
-- service/setting_gateway_runtime.go:251-252,
-- handler/admin/setting_handler.go:513-519). This release removes that code, so
-- nothing reads or writes the rows any more.
--
-- Only these two settings rows are deleted. The account option
-- openai_prompt_cache_key_mode in accounts.extra is live
-- (service/openai_apikey_compat.go) and is not touched; neither is any other
-- setting, nor the audit log.
--
-- Deleting an absent row is not an error, so the migration can run again.
-- No explicit BEGIN/COMMIT: the runner owns this file's transaction.
SET LOCAL lock_timeout = '10s';
SET LOCAL statement_timeout = '120s';

DELETE FROM settings
WHERE key IN (
    'openai_apikey_alpha_search_responses_bridge_enabled',
    'openai_apikey_prompt_cache_key_normalization_enabled'
);
