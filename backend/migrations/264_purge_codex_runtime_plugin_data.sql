-- Purge what the Codex runtime plugin (codexrip.codex-runtime) left stored. Its
-- route acquisition, quality diagnostics, outbound observation and runtime host
-- are gone; the application reads nothing this migration deletes. The one
-- setting that lives on, request body compression, moves to a plain settings
-- row.
--
-- The migration raises instead of guessing, and then changes nothing, when
-- * the plugin's installation exists and settings.deplugin_retired_plugins has
--   no entry for it: the installation was never retired;
-- * a saved configuration exists (1) and the hash recorded for it is missing
--   or is neither value listed there;
-- * sub2api_plugin_state holds a row of another plugin key (2).
-- Starting v0.2.11-codexrip.6, .7 or .8 once retires the installation and
-- records the hash. A new database only loses the unused tables of 2.
--
-- 1. Compression setting. The saved configuration is ciphertext
--    (settings.codex_native_runtime_source, or the installation's
--    config_encrypted while no source row existed) and is not decrypted here.
--    Every start and every save of v0.2.11-codexrip.6, .7 and .8 recorded the
--    SHA-256 of the normalized configuration in
--    settings.codex_native_runtime_config, and the normalized configuration
--    has two possible values:
--      {"request_zstd":true}   7818a2a138b902a949b9fd77ba6bb62d8d77dcfbfa93ca1a16e778c609172304
--      {"request_zstd":false}  1ee89db3b3c06e8da7f0524370502e503df48204fa16813ded97b7981b653aaa
--    A saved configuration overrode gateway.openai_codex_request_zstd, so its
--    value is written to settings.codex_runtime_config. Without a saved
--    configuration the deployment value applied and still applies: no row is
--    written and the recorded hash is not read. An existing
--    codex_runtime_config row is kept. Both former settings rows are deleted.
-- 2. sub2api_plugin_state and sub2api_plugin_leases (246), the former with its
--    index sub2api_plugin_state_due and its column next_at: nothing reads or
--    writes them, so both tables are dropped. The state table held this
--    plugin's rows (quality-run.*, validation.*, spent.*, wire.* and anything
--    else). A row of another plugin key has no reader either; it raises so
--    that it is not dropped unseen.
-- 3. The codexrip.codex-runtime installation. A row the runtime host created
--    for itself on a database that never had the plugin (receipt entry
--    native_created) is deleted with its receipt entry. A retired plugin
--    installation keeps its row and its receipt entry, like the other retired
--    plugins, without the plugin's material: the saved configuration (which
--    held the acquisition proxy credential), the package bytes with their
--    hashes and paths, the manifest and description, the capability bindings,
--    a staged update and the bundle bootstrap record. Other installations and
--    their receipt entries are not touched.
-- 4. accounts.extra.codex_client_identity: the selectable client profile
--    (schema 2) is removed, so the identity is derived from the account's
--    fingerprint seed again, and the members source, originator and
--    client_version that came with it are dropped from the remaining
--    identities.
-- 5. accounts.extra.codex_fingerprint_mode: the account creation form could
--    leave the mode unset to follow the plugin's default. An OpenAI OAuth or
--    Setup Token account that is not deleted and stores no mode is written as
--    "device", which is what a missing mode means; a stored mode, "off"
--    included, is kept.
--
-- Audit logs, account credentials and files in the plugin directory are not
-- touched. An account row is written only when 4 or 5 changes it. accounts has
-- no updated_at trigger; writing extra fires the long-context billing triggers
-- of 175, which keep the stored flag and queue no scheduler_outbox event. Every
-- statement is a no-op once its rows or objects are gone, so the migration can
-- run again.
SET LOCAL lock_timeout = '10s';
SET LOCAL statement_timeout = '120s';

DO $$
DECLARE
    receipt  jsonb;
    entry    jsonb;
    recorded text;
    zstd     text;
    others   text;
BEGIN
    SELECT value::jsonb INTO receipt FROM settings WHERE key = 'deplugin_retired_plugins';
    entry := receipt -> 'plugins' -> 'codexrip.codex-runtime';

    IF EXISTS (SELECT 1 FROM sub2api_plugin_installations WHERE plugin_key = 'codexrip.codex-runtime')
       AND COALESCE(jsonb_typeof(entry), '') <> 'object' THEN
        RAISE EXCEPTION 'migration 264: the codexrip.codex-runtime plugin installation was never retired; start v0.2.11-codexrip.6, .7 or .8 once, then upgrade';
    END IF;

    IF to_regclass('sub2api_plugin_state') IS NOT NULL THEN
        SELECT string_agg(plugin_key, ', ' ORDER BY plugin_key) INTO others
        FROM (
            SELECT DISTINCT plugin_key
            FROM sub2api_plugin_state
            WHERE plugin_key <> 'codexrip.codex-runtime'
            ORDER BY plugin_key
            LIMIT 5
        ) AS other_keys;
        IF others IS NOT NULL THEN
            RAISE EXCEPTION 'migration 264: sub2api_plugin_state holds rows of other plugins (%); nothing reads them and the table is dropped, so export or delete them, then upgrade', others;
        END IF;
    END IF;

    IF EXISTS (SELECT 1 FROM settings WHERE key = 'codex_native_runtime_source')
       OR EXISTS (SELECT 1 FROM sub2api_plugin_installations
                  WHERE plugin_key = 'codexrip.codex-runtime' AND config_encrypted <> '') THEN
        SELECT value::jsonb ->> 'config_sha256' INTO recorded
        FROM settings WHERE key = 'codex_native_runtime_config';
        zstd := CASE recorded
            WHEN '7818a2a138b902a949b9fd77ba6bb62d8d77dcfbfa93ca1a16e778c609172304' THEN '{"request_zstd":true}'
            WHEN '1ee89db3b3c06e8da7f0524370502e503df48204fa16813ded97b7981b653aaa' THEN '{"request_zstd":false}'
        END;
        IF zstd IS NULL THEN
            RAISE EXCEPTION 'migration 264: a saved Codex runtime configuration exists but its recorded hash (%) is neither known value; start v0.2.11-codexrip.6, .7 or .8 once, then upgrade', COALESCE(recorded, 'none');
        END IF;
        INSERT INTO settings (key, value, updated_at)
        VALUES ('codex_runtime_config', zstd, NOW())
        ON CONFLICT (key) DO NOTHING;
    END IF;

    IF entry -> 'native_created' = 'true'::jsonb THEN
        DELETE FROM sub2api_plugin_installations WHERE plugin_key = 'codexrip.codex-runtime';
        UPDATE settings
        SET value = (receipt #- '{plugins,codexrip.codex-runtime}')::text,
            updated_at = NOW()
        WHERE key = 'deplugin_retired_plugins';
    END IF;
END $$;

DELETE FROM settings
WHERE key IN ('codex_native_runtime_source', 'codex_native_runtime_config');

DROP TABLE IF EXISTS sub2api_plugin_state;
DROP TABLE IF EXISTS sub2api_plugin_leases;

DELETE FROM sub2api_plugin_bindings
WHERE plugin_id IN (SELECT id FROM sub2api_plugin_installations WHERE plugin_key = 'codexrip.codex-runtime');

DELETE FROM sub2api_plugin_updates
WHERE plugin_id IN (SELECT id FROM sub2api_plugin_installations WHERE plugin_key = 'codexrip.codex-runtime');

DELETE FROM sub2api_plugin_bootstrap
WHERE plugin_key = 'codexrip.codex-runtime';

UPDATE sub2api_plugin_installations
SET description = '',
    manifest = '{}'::jsonb,
    artifact_path = '',
    install_path = '',
    binary_path = '',
    binary_sha256 = '',
    signature_status = 'unsigned',
    config_encrypted = '',
    last_error = '',
    artifact_data = NULL,
    package_sha256 = '',
    update_policy = 'pinned',
    updated_at = NOW()
WHERE plugin_key = 'codexrip.codex-runtime'
  AND (
      description <> ''
      OR manifest <> '{}'::jsonb
      OR artifact_path <> ''
      OR install_path <> ''
      OR binary_path <> ''
      OR binary_sha256 <> ''
      OR signature_status <> 'unsigned'
      OR config_encrypted <> ''
      OR last_error <> ''
      OR artifact_data IS NOT NULL
      OR package_sha256 <> ''
      OR update_policy <> 'pinned'
  );

UPDATE accounts
SET extra = extra - 'codex_client_identity'
WHERE jsonb_typeof(extra) = 'object'
  AND jsonb_typeof(extra -> 'codex_client_identity') = 'object'
  AND extra -> 'codex_client_identity' -> 'v' = '2'::jsonb;

UPDATE accounts
SET extra = jsonb_set(
        extra,
        '{codex_client_identity}',
        (extra -> 'codex_client_identity') - ARRAY['source', 'originator', 'client_version']
    )
WHERE jsonb_typeof(extra) = 'object'
  AND jsonb_typeof(extra -> 'codex_client_identity') = 'object'
  AND (extra -> 'codex_client_identity') ?| ARRAY['source', 'originator', 'client_version'];

UPDATE accounts
SET extra = jsonb_set(extra, '{codex_fingerprint_mode}', '"device"'::jsonb, true)
WHERE deleted_at IS NULL
  AND platform = 'openai'
  AND type IN ('oauth', 'setup-token')
  AND jsonb_typeof(extra) = 'object'
  AND COALESCE(btrim(extra ->> 'codex_fingerprint_mode'), '') = '';
