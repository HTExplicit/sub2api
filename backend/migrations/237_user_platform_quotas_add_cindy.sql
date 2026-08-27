-- Add first-class Cindy billing ownership to per-user platform quotas.
--
-- Cindy accounts are now persisted as platform='cindy' rather than as the
-- temporary OpenAI projection.  Without this constraint update, the billing
-- path cannot persist Cindy quota usage and new-user default quota snapshots
-- silently lose the Cindy row.  The new constraint is a strict superset of
-- the previous 224 constraint and is safe to reapply.
ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok',
                        'kimi', 'zhipu', 'deepseek', 'cindy'));
