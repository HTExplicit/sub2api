-- Remove what Image Studio left stored. The feature is deleted and the
-- application reads nothing this migration removes.
--
-- The migration raises, and then changes nothing, when the codexrip.image-tools
-- plugin installation exists and settings.deplugin_retired_plugins has no entry
-- for it: the installation was never retired. Starting v0.2.13-codexrip.6 once
-- retires it. A new database only loses the empty tables of 3.
--
-- 1. settings.image_tools_config, the Image Studio switch.
-- 2. The codexrip.image-tools installation, whose two functions (Image Studio
--    and the Responses image bridge removed in 260) are both gone. A retired
--    plugin installation keeps its row and its receipt entry, like the other
--    retired plugins, without the plugin's material: the saved configuration,
--    the package bytes with their hashes and paths, the manifest and
--    description, the capability bindings, a staged update and the bundle
--    bootstrap record. Other installations and their receipt entries are not
--    touched.
-- 3. image_studio_artifacts, image_studio_items and image_studio_jobs (233),
--    children first, with their rows, indexes, constraints and sequences: the
--    job history with its prompts and the records of the stored image files.
--    The foreign keys from image_studio_jobs to users and api_keys go with the
--    table and no row of users or api_keys is written, but removing them locks
--    both tables exclusively until the migration commits. The tables are
--    therefore dropped last, and lock_timeout bounds the wait for that lock.
--
-- Usage logs, Ops error logs and audit logs are not touched. The image files
-- Image Studio wrote under <data dir>/image-studio are not in the database and
-- stay on disk. Every statement is a no-op once its rows or objects are gone,
-- so the migration can run again.
SET LOCAL lock_timeout = '10s';
SET LOCAL statement_timeout = '120s';

DO $$
DECLARE
    entry jsonb;
BEGIN
    SELECT value::jsonb -> 'plugins' -> 'codexrip.image-tools' INTO entry
    FROM settings WHERE key = 'deplugin_retired_plugins';

    IF EXISTS (SELECT 1 FROM sub2api_plugin_installations WHERE plugin_key = 'codexrip.image-tools')
       AND COALESCE(jsonb_typeof(entry), '') <> 'object' THEN
        RAISE EXCEPTION 'migration 270: the codexrip.image-tools plugin installation was never retired; start v0.2.13-codexrip.6 once, then upgrade';
    END IF;
END $$;

DELETE FROM settings
WHERE key = 'image_tools_config';

DELETE FROM sub2api_plugin_bindings
WHERE plugin_id IN (SELECT id FROM sub2api_plugin_installations WHERE plugin_key = 'codexrip.image-tools');

DELETE FROM sub2api_plugin_updates
WHERE plugin_id IN (SELECT id FROM sub2api_plugin_installations WHERE plugin_key = 'codexrip.image-tools');

DELETE FROM sub2api_plugin_bootstrap
WHERE plugin_key = 'codexrip.image-tools';

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
WHERE plugin_key = 'codexrip.image-tools'
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

DROP TABLE IF EXISTS image_studio_artifacts;
DROP TABLE IF EXISTS image_studio_items;
DROP TABLE IF EXISTS image_studio_jobs;
