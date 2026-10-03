-- Keep `typesafe` in the per-user quota platform CHECK on every upgrade path.
--
-- Upstream's 241_add_typesafe_platform.sql adds `typesafe` to this CHECK and to
-- the composite route CHECK. The runner applies unrecorded files in filename
-- order, so a database that has not recorded it runs it before the downstream
-- files that re-create the quota CHECK without `typesafe`
-- (241_minimax_cindy_platform_quota_compat, 251, 260). A new database therefore
-- ended at 260's ten platforms, while a database already past 260 applies
-- upstream's 241 last and keeps its list: the eleven platforms and the `cindy`
-- value the downstream copy of that file carries for databases that have not
-- reached 260. With `typesafe` missing, a default TypeSafe quota makes the
-- multi-row quota snapshot of a new user violate the CHECK, and that user ends
-- up with no platform quota at all.
--
-- This file states the union once more at the tail, so every path ends with the
-- same eleven platforms (service.AllowedQuotaPlatforms) and without the retired
-- `cindy` value. The composite route CHECK needs nothing: no downstream file
-- re-creates it after upstream's 241.
--
-- Only the CHECK changes; no quota row is written. 260 removed every `cindy`
-- row together with that value, so each existing row already satisfies the new
-- constraint. Running it again is a no-op.
-- No explicit BEGIN/COMMIT: the runner owns this file's transaction.
SET LOCAL lock_timeout = '10s';
SET LOCAL statement_timeout = '120s';

ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok',
                        'kimi', 'zhipu', 'deepseek', 'minimax', 'opencode_go', 'typesafe'));
