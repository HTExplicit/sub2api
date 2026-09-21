-- Restore the complete supported quota-platform union after the published
-- 241 compatibility migration. Keep all historical migration bytes and ledger
-- entries intact; this changes only the CHECK and never rewrites quota rows.
-- A tail migration cannot bypass an earlier, unrecorded 241 that already fails
-- on existing OpenCode Go rows; that older upgrade path needs separate handling.
ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok',
                        'kimi', 'zhipu', 'deepseek', 'minimax', 'cindy', 'opencode_go'));
