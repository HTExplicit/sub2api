-- Remove what the seven first-party plugins left stored. Their features are
-- built in and the application reads nothing this migration removes. A new
-- database only loses the schema of 3.
--
-- Removed:
-- 1. The installations codexrip.account-tools, codexrip.admin-observability,
--    codexrip.cindy-provider, codexrip.codex-runtime, codexrip.image-tools,
--    codexrip.model-policy and codexrip.prompt-skills, each with its saved
--    configuration, package bytes, manifest and capability bindings.
-- 2. settings.deplugin_retired_plugins, the receipt of their retirement.
-- 3. The schema only those plugins used: the tables sub2api_plugin_bootstrap
--    (248, 249) and sub2api_plugin_updates (250) with any row they hold; the
--    columns revision, runtime_generation, package_sha256 and update_policy,
--    the state 'updating' and the trigger and function sub2api_plugin_revision
--    (250); the limit of the unique index on enabled bindings to two
--    capabilities (246). sub2api_plugin_installations and
--    sub2api_plugin_bindings are then as 229 and 230 define them.
--
-- Left: every other installation with its bindings, of which no row is
-- written, and settings.admin_observability_config. A migration does not
-- reach what the seven plugins stored outside the database: their package
-- files <plugin dir>/packages/codexrip.*.s2plugin, their unpacked trees
-- <plugin dir>/installed/codexrip.*/ and any Redis key
-- plugin:kv:v1:codexrip.*. Nothing reads these once the rows are gone; they
-- are deleted by hand at the release that applies this migration.
--
-- The migration raises, and then changes nothing, when
-- * one of the seven installations exists and no receipt is stored: that
--   installation was never retired. Starting v0.2.13-codexrip.8 once retires
--   it;
-- * one of them exists and the stored receipt is not completed or has no
--   entry for it. No release writes such a receipt and none repairs it;
-- * another installation is in a state 229 does not have ('updating', which
--   250 added), or two enabled bindings share a capability, platform and
--   account type: the check and the unique index of 229 do not admit them.
--   Disabling or uninstalling the plugin in v0.2.13-codexrip.8 clears both.
-- Migrations 270 and 271 are committed by then. DOWNSTREAM.md leads to what
-- v0.2.13-codexrip.8 needs on such a database and how a receipt no release
-- repairs is resolved.
--
-- Both plugin tables stay locked until the migration commits; lock_timeout
-- bounds the wait. There is no down path: an earlier image finds no receipt,
-- reads the dropped column runtime_generation and exits at start. A second
-- run removes nothing and rebuilds the same check and index. No explicit
-- BEGIN/COMMIT: the runner owns this file's transaction.
SET LOCAL lock_timeout = '10s';
SET LOCAL statement_timeout = '120s';

LOCK TABLE sub2api_plugin_installations, sub2api_plugin_bindings IN ACCESS EXCLUSIVE MODE;

DO $$
DECLARE
    first_party CONSTANT text[] := ARRAY[
        'codexrip.account-tools', 'codexrip.admin-observability', 'codexrip.cindy-provider',
        'codexrip.codex-runtime', 'codexrip.image-tools', 'codexrip.model-policy',
        'codexrip.prompt-skills'
    ];
    receipt jsonb;
    refused text;
BEGIN
    IF EXISTS (SELECT 1 FROM sub2api_plugin_installations WHERE plugin_key = ANY (first_party)) THEN
        SELECT value::jsonb INTO receipt FROM settings WHERE key = 'deplugin_retired_plugins';
        SELECT string_agg(plugin_key, ', ' ORDER BY plugin_key) INTO refused
        FROM sub2api_plugin_installations
        WHERE plugin_key = ANY (first_party)
          AND (receipt -> 'completed' IS DISTINCT FROM 'true'::jsonb
               OR COALESCE(jsonb_typeof(receipt -> 'plugins' -> plugin_key), '') <> 'object');
        -- settings.value is NOT NULL: receipt is NULL only while no row is stored.
        IF refused IS NOT NULL AND receipt IS NULL THEN
            RAISE EXCEPTION 'migration 272: first-party plugin installations were never retired (%); start v0.2.13-codexrip.8 once as DOWNSTREAM.md describes, then upgrade', refused;
        ELSIF refused IS NOT NULL THEN
            RAISE EXCEPTION 'migration 272: the retirement receipt is not completed or has no entry for first-party plugin installations (%); no release repairs it, resolve it as DOWNSTREAM.md describes, then upgrade', refused;
        END IF;
    END IF;

    SELECT string_agg(plugin_key || ' is ' || state, ', ' ORDER BY plugin_key) INTO refused
    FROM sub2api_plugin_installations
    WHERE plugin_key <> ALL (first_party)
      AND state NOT IN ('disabled', 'starting', 'enabled', 'error', 'incompatible');
    IF refused IS NOT NULL THEN
        RAISE EXCEPTION 'migration 272: plugin installations are in a state the plugin manager does not have (%); disable or uninstall them in v0.2.13-codexrip.8 as DOWNSTREAM.md describes, then upgrade', refused;
    END IF;

    SELECT string_agg(scope, ', ' ORDER BY scope) INTO refused
    FROM (
        SELECT b.capability || ' ' || b.platform || '/' || b.account_type AS scope
        FROM sub2api_plugin_bindings AS b
        JOIN sub2api_plugin_installations AS p ON p.id = b.plugin_id
        WHERE b.enabled AND p.plugin_key <> ALL (first_party)
        GROUP BY b.capability, b.platform, b.account_type
        HAVING count(*) > 1
        ORDER BY 1
        LIMIT 5
    ) AS shared;
    IF refused IS NOT NULL THEN
        RAISE EXCEPTION 'migration 272: more than one enabled plugin binding shares a scope (%); disable all but one of the plugins in v0.2.13-codexrip.8 as DOWNSTREAM.md describes, then upgrade', refused;
    END IF;

    DELETE FROM sub2api_plugin_installations WHERE plugin_key = ANY (first_party);
END $$;

DELETE FROM settings
WHERE key = 'deplugin_retired_plugins';

DROP TABLE IF EXISTS sub2api_plugin_updates;
DROP TABLE IF EXISTS sub2api_plugin_bootstrap;

DROP TRIGGER IF EXISTS sub2api_plugin_revision ON sub2api_plugin_installations;
DROP FUNCTION IF EXISTS sub2api_plugin_revision();

ALTER TABLE sub2api_plugin_installations
    DROP COLUMN IF EXISTS revision,
    DROP COLUMN IF EXISTS runtime_generation,
    DROP COLUMN IF EXISTS package_sha256,
    DROP COLUMN IF EXISTS update_policy;

ALTER TABLE sub2api_plugin_installations
    DROP CONSTRAINT IF EXISTS sub2api_plugin_installations_state_check,
    ADD CONSTRAINT sub2api_plugin_installations_state_check
        CHECK (state IN ('disabled', 'starting', 'enabled', 'error', 'incompatible'));

DROP INDEX IF EXISTS idx_sub2api_plugin_bindings_one_enabled_scope;
CREATE UNIQUE INDEX idx_sub2api_plugin_bindings_one_enabled_scope
    ON sub2api_plugin_bindings(capability, platform, account_type)
    WHERE enabled = TRUE;
