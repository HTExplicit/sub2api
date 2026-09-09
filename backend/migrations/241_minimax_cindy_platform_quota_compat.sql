-- Reconcile the independently published Cindy quota migration with MiniMax.
-- Existing installations skip 237_user_platform_quotas_add_cindy.sql after
-- validating its original checksum; new installations apply it after
-- 237_add_minimax_platform.sql because migrations sort by the full filename.
-- Keep both 237 files and the historical ledger intact, then converge on the
-- union here. This changes only the quota constraint, never quota rows.
ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok',
                        'kimi', 'zhipu', 'deepseek', 'minimax', 'cindy'));
