-- Add immutable source provenance to remote-skill versions, then remove the
-- two superseded prompt composition modes. Every destructive step is scoped
-- by live references and runs in this migration transaction.

ALTER TABLE system_prompt_skill_bundle_versions
    ADD COLUMN IF NOT EXISTS source_id VARCHAR(32),
    ADD COLUMN IF NOT EXISTS remote_root TEXT;

ALTER TABLE system_prompt_skill_sync_jobs
    ADD COLUMN IF NOT EXISTS source_id VARCHAR(32);

UPDATE system_prompt_skill_bundle_versions
SET source_id = COALESCE(source_id, 'github_official'),
    remote_root = COALESCE(
        remote_root,
        'https://raw.githubusercontent.com/zhaoxuya520/reverse-skill/' || source_commit || '/skills'
    );

UPDATE system_prompt_skill_sync_jobs AS j
SET source_id = COALESCE(v.source_id, 'github_official')
FROM system_prompt_skill_bundle_versions AS v
WHERE j.source_id IS NULL
  AND v.id = j.candidate_bundle_version_id;

UPDATE system_prompt_skill_sync_jobs
SET source_id = 'github_official'
WHERE source_id IS NULL;

ALTER TABLE system_prompt_skill_bundle_versions
    ALTER COLUMN source_id SET NOT NULL,
    ALTER COLUMN remote_root SET NOT NULL;

ALTER TABLE system_prompt_skill_sync_jobs
    ALTER COLUMN source_id SET DEFAULT 'github_official',
    ALTER COLUMN source_id SET NOT NULL;

ALTER TABLE system_prompt_skill_bundle_versions
    DROP CONSTRAINT IF EXISTS system_prompt_skill_bundle_versions_manifest_sha256_key;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'system_prompt_skill_source_manifest_unique'
          AND conrelid = 'system_prompt_skill_bundle_versions'::regclass
    ) THEN
        ALTER TABLE system_prompt_skill_bundle_versions
            ADD CONSTRAINT system_prompt_skill_source_manifest_unique
            UNIQUE (source_id, manifest_sha256);
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'system_prompt_skill_source_identity'
          AND conrelid = 'system_prompt_skill_bundle_versions'::regclass
    ) THEN
        ALTER TABLE system_prompt_skill_bundle_versions
            ADD CONSTRAINT system_prompt_skill_source_identity CHECK (
                (source_id = 'github_official'
                 AND remote_root =
                     'https://raw.githubusercontent.com/zhaoxuya520/reverse-skill/' || source_commit || '/skills')
                OR
                (source_id = 'moxinggang'
                 AND remote_root = 'https://moxinggang.com/skills/security-research/current')
            );
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'system_prompt_skill_sync_source_id'
          AND conrelid = 'system_prompt_skill_sync_jobs'::regclass
    ) THEN
        ALTER TABLE system_prompt_skill_sync_jobs
            ADD CONSTRAINT system_prompt_skill_sync_source_id
            CHECK (source_id IN ('github_official', 'moxinggang'));
    END IF;
END
$$;

CREATE OR REPLACE FUNCTION protect_system_prompt_skill_bundle_version()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.bundle_id IS DISTINCT FROM NEW.bundle_id
       OR OLD.source_id IS DISTINCT FROM NEW.source_id
       OR OLD.remote_root IS DISTINCT FROM NEW.remote_root
       OR OLD.source_commit IS DISTINCT FROM NEW.source_commit
       OR OLD.overlay_sha256 IS DISTINCT FROM NEW.overlay_sha256
       OR OLD.manifest_sha256 IS DISTINCT FROM NEW.manifest_sha256
       OR OLD.archive_sha256 IS DISTINCT FROM NEW.archive_sha256
       OR OLD.file_count IS DISTINCT FROM NEW.file_count
       OR OLD.total_bytes IS DISTINCT FROM NEW.total_bytes
       OR OLD.added_files IS DISTINCT FROM NEW.added_files
       OR OLD.modified_files IS DISTINCT FROM NEW.modified_files
       OR OLD.deleted_files IS DISTINCT FROM NEW.deleted_files
       OR OLD.script_changes IS DISTINCT FROM NEW.script_changes
       OR OLD.binary_changes IS DISTINCT FROM NEW.binary_changes
       OR OLD.created_by IS DISTINCT FROM NEW.created_by
       OR OLD.created_at IS DISTINCT FROM NEW.created_at THEN
        RAISE EXCEPTION 'system prompt skill bundle version is immutable';
    END IF;
    RETURN NEW;
END;
$$;

ALTER TABLE system_prompt_template_versions
    DROP CONSTRAINT IF EXISTS system_prompt_template_versions_composition;

CREATE TEMPORARY TABLE legacy_prompt_templates (
    template_id BIGINT PRIMARY KEY
) ON COMMIT DROP;

INSERT INTO legacy_prompt_templates (template_id)
SELECT DISTINCT template_id
FROM system_prompt_template_versions
WHERE composition_mode IN ('remote_skill', 'offline_bundle');

DO $legacy_runtime$
DECLARE
    current_active_template_id BIGINT;
    current_active_version_id BIGINT;
    active_mode VARCHAR(32);
    replacement_version_id BIGINT;
    replacement_count INTEGER;
BEGIN
    SELECT r.active_template_id, r.active_version_id, v.composition_mode
    INTO current_active_template_id, current_active_version_id, active_mode
    FROM system_prompt_runtime AS r
    LEFT JOIN system_prompt_template_versions AS v ON v.id = r.active_version_id
    WHERE r.id = 1
    FOR UPDATE OF r;

    IF active_mode IS NULL OR active_mode NOT IN ('remote_skill', 'offline_bundle') THEN
        RETURN;
    END IF;

    SELECT COUNT(*), MIN(v.id)
    INTO replacement_count, replacement_version_id
    FROM system_prompt_templates AS t
    JOIN system_prompt_template_versions AS v ON v.template_id = t.id
    WHERE t.id = current_active_template_id
      AND t.is_seed = TRUE
      AND t.slug = 'codexrip_reverse_skill'
      AND t.deleted_at IS NULL
      AND v.composition_mode = 'codex_skill_hybrid'
      AND v.bundle_id = 'codexrip-reverse-skill'
      AND v.bundle_manifest_sha256 IS NULL
      AND v.sha256 = '2107e252ef417561baa4c5349f0c34d4e767ad422dfc463b2eac07bf7bbcc931'
      AND v.byte_length = 6724
      AND encode(sha256(convert_to(v.body, 'UTF8')), 'hex') =
          '2107e252ef417561baa4c5349f0c34d4e767ad422dfc463b2eac07bf7bbcc931'
      AND octet_length(v.body) = 6724;

    IF replacement_count <> 1 OR replacement_version_id IS NULL THEN
        RAISE EXCEPTION 'unable to migrate active legacy system prompt';
    END IF;

    UPDATE system_prompt_runtime
    SET active_version_id = replacement_version_id,
        revision = revision + 1,
        updated_at = NOW()
    WHERE id = 1
      AND active_version_id = current_active_version_id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'unable to migrate active legacy system prompt';
    END IF;
END;
$legacy_runtime$;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM system_prompt_runtime AS r
        JOIN system_prompt_template_versions AS v ON v.id = r.active_version_id
        WHERE r.id = 1
          AND v.composition_mode IN ('remote_skill', 'offline_bundle')
    ) THEN
        RAISE EXCEPTION 'unable to migrate active legacy system prompt';
    END IF;
END
$$;

DROP TRIGGER IF EXISTS trg_prevent_system_prompt_version_delete
    ON system_prompt_template_versions;

DELETE FROM system_prompt_template_versions
WHERE composition_mode IN ('remote_skill', 'offline_bundle');

DELETE FROM system_prompt_templates AS t
USING legacy_prompt_templates AS legacy
WHERE t.id = legacy.template_id
  AND NOT EXISTS (
      SELECT 1 FROM system_prompt_template_versions AS v WHERE v.template_id = t.id
  )
  AND NOT EXISTS (
      SELECT 1 FROM system_prompt_runtime AS r WHERE r.active_template_id = t.id
  );

CREATE TRIGGER trg_prevent_system_prompt_version_delete
BEFORE DELETE ON system_prompt_template_versions
FOR EACH ROW
EXECUTE FUNCTION prevent_system_prompt_version_delete();

ALTER TABLE system_prompt_template_versions
    ADD CONSTRAINT system_prompt_template_versions_composition CHECK (
        composition_mode IN ('inline', 'codex_skill_hybrid')
        AND (
            (composition_mode = 'inline'
             AND bundle_id IS NULL
             AND bundle_manifest_sha256 IS NULL)
            OR
            (composition_mode = 'codex_skill_hybrid'
             AND bundle_id = 'codexrip-reverse-skill'
             AND bundle_manifest_sha256 IS NULL)
        )
    );
