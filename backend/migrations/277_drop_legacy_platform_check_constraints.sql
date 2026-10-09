-- Official 242 runs before immutable downstream migrations 251, 260 and 265.
-- Those historical migrations restore the quota whitelist on a fresh install.
-- Finish with the official application-catalog validation contract in both
-- fresh installations and existing databases, without rewriting old checksums.
ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

ALTER TABLE composite_model_routes
    DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check;
