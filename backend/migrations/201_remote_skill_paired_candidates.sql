-- Replace the old content-addressed GitHub/ZIP registry with one fixed
-- Moxinggang source and an immutable prompt/tree pair. Legacy rows remain
-- nullable until application startup activates the embedded pair and removes
-- them under the cleanup gate.

DROP TRIGGER IF EXISTS trg_protect_system_prompt_template_managed_source
    ON system_prompt_templates;

DO $remote_skill_prompt_owner$
BEGIN
    IF EXISTS (
        SELECT 1 FROM system_prompt_templates
        WHERE slug = 'codexrip_reverse_skill' AND is_seed = TRUE
          AND managed_source IS NOT NULL
          AND managed_source <> 'remote_skill_registry'
    ) THEN
        RAISE EXCEPTION 'unexpected managed source on remote skill prompt';
    END IF;

    UPDATE system_prompt_templates
    SET managed_source = 'remote_skill_registry',
        name = 'Security Research Remote Skill Prompt',
        description = 'ModelGang current security-research Skill tree with the paired managed prompt.',
        updated_at = NOW()
    WHERE slug = 'codexrip_reverse_skill' AND is_seed = TRUE;
END;
$remote_skill_prompt_owner$;

CREATE TRIGGER trg_protect_system_prompt_template_managed_source
BEFORE UPDATE ON system_prompt_templates
FOR EACH ROW
EXECUTE FUNCTION protect_system_prompt_template_managed_source();

CREATE TABLE IF NOT EXISTS system_prompt_skill_prompt_versions (
    id BIGSERIAL PRIMARY KEY,
    raw_sha256 CHAR(64) NOT NULL,
    effective_sha256 CHAR(64) NOT NULL,
    raw_body TEXT NOT NULL,
    effective_body TEXT NOT NULL,
    diff TEXT NOT NULL,
    created_by BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT system_prompt_skill_prompt_raw_sha256_hex CHECK (raw_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT system_prompt_skill_prompt_effective_sha256_hex CHECK (effective_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT system_prompt_skill_prompt_body_limit CHECK (
        octet_length(raw_body) > 0 AND octet_length(raw_body) <= 65536 AND
        octet_length(effective_body) > 0 AND octet_length(effective_body) <= 65536
    ),
    CONSTRAINT system_prompt_skill_prompt_identity UNIQUE (raw_sha256, effective_sha256)
);

ALTER TABLE system_prompt_skill_bundle_versions
    DROP CONSTRAINT IF EXISTS system_prompt_skill_bundle_id,
    DROP CONSTRAINT IF EXISTS system_prompt_skill_source_commit_hex,
    DROP CONSTRAINT IF EXISTS system_prompt_skill_overlay_sha256_hex,
    DROP CONSTRAINT IF EXISTS system_prompt_skill_manifest_sha256_hex,
    DROP CONSTRAINT IF EXISTS system_prompt_skill_archive_sha256_hex,
    DROP CONSTRAINT IF EXISTS system_prompt_skill_source_manifest_unique,
    DROP CONSTRAINT IF EXISTS system_prompt_skill_bundle_versions_manifest_sha256_key,
    DROP CONSTRAINT IF EXISTS system_prompt_skill_source_identity;

ALTER TABLE system_prompt_skill_sync_jobs
    DROP CONSTRAINT IF EXISTS system_prompt_skill_sync_source_id;

ALTER TABLE system_prompt_skill_bundle_versions
    DROP COLUMN IF EXISTS bundle_id,
    DROP COLUMN IF EXISTS source_id,
    DROP COLUMN IF EXISTS remote_root,
    DROP COLUMN IF EXISTS source_commit,
    DROP COLUMN IF EXISTS overlay_sha256,
    DROP COLUMN IF EXISTS manifest_sha256,
    DROP COLUMN IF EXISTS archive_sha256,
    DROP COLUMN IF EXISTS total_bytes;

ALTER TABLE system_prompt_skill_sync_jobs
    DROP COLUMN IF EXISTS source_id,
    DROP COLUMN IF EXISTS source_commit;

ALTER TABLE system_prompt_skill_bundle_versions
    ADD COLUMN IF NOT EXISTS upstream_source_id VARCHAR(32),
    ADD COLUMN IF NOT EXISTS upstream_root TEXT,
    ADD COLUMN IF NOT EXISTS public_root TEXT,
    ADD COLUMN IF NOT EXISTS raw_tree_sha256 CHAR(64),
    ADD COLUMN IF NOT EXISTS effective_tree_sha256 CHAR(64),
    ADD COLUMN IF NOT EXISTS prompt_version_id BIGINT,
    ADD COLUMN IF NOT EXISTS raw_total_bytes BIGINT,
    ADD COLUMN IF NOT EXISTS effective_total_bytes BIGINT,
    ADD COLUMN IF NOT EXISTS file_changes JSONB,
    ADD COLUMN IF NOT EXISTS fetched_at TIMESTAMPTZ;

ALTER TABLE system_prompt_skill_sync_jobs
    ADD COLUMN IF NOT EXISTS prompt_capture_provided BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE system_prompt_skill_runtime
    ADD COLUMN IF NOT EXISTS active_prompt_version_id BIGINT;

ALTER TABLE system_prompt_skill_bundle_versions
    ADD CONSTRAINT system_prompt_skill_prompt_version_fk
        FOREIGN KEY (prompt_version_id) REFERENCES system_prompt_skill_prompt_versions(id) ON DELETE RESTRICT;

ALTER TABLE system_prompt_skill_runtime
    ADD CONSTRAINT system_prompt_skill_runtime_prompt_fk
        FOREIGN KEY (active_prompt_version_id) REFERENCES system_prompt_skill_prompt_versions(id) ON DELETE RESTRICT;

ALTER TABLE system_prompt_skill_bundle_versions
    ADD CONSTRAINT system_prompt_skill_upstream_identity CHECK (
        upstream_source_id IS NULL OR (
            upstream_source_id = 'moxinggang' AND
            upstream_root = 'https://moxinggang.com/skills/security-research/current' AND
            public_root = 'https://codexrip.vip/skills/security-research/current'
        )
    ),
    ADD CONSTRAINT system_prompt_skill_tree_sha256_hex CHECK (
        (raw_tree_sha256 IS NULL OR raw_tree_sha256 ~ '^[0-9a-f]{64}$') AND
        (effective_tree_sha256 IS NULL OR effective_tree_sha256 ~ '^[0-9a-f]{64}$')
    ),
    ADD CONSTRAINT system_prompt_skill_tree_size CHECK (
        (raw_total_bytes IS NULL OR raw_total_bytes > 0 AND raw_total_bytes <= 268435456) AND
        (effective_total_bytes IS NULL OR effective_total_bytes > 0 AND effective_total_bytes <= 268435456)
    );

DROP INDEX IF EXISTS idx_system_prompt_skill_paired_identity;
CREATE INDEX IF NOT EXISTS idx_system_prompt_skill_paired_content
    ON system_prompt_skill_bundle_versions(upstream_source_id, effective_tree_sha256, prompt_version_id, fetched_at DESC)
    WHERE upstream_source_id IS NOT NULL AND effective_tree_sha256 IS NOT NULL AND prompt_version_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_system_prompt_skill_prompt_versions_created
    ON system_prompt_skill_prompt_versions(created_at DESC, id DESC);

DO $remote_skill_bundle_sequence$
DECLARE
    sequence_name TEXT;
    sequence_value BIGINT;
    sequence_called BOOLEAN;
    maximum_id BIGINT;
BEGIN
    sequence_name := pg_get_serial_sequence('system_prompt_skill_bundle_versions', 'id');
    IF sequence_name IS NULL THEN
        RAISE EXCEPTION 'system prompt skill bundle version sequence is missing';
    END IF;

    SELECT COALESCE(MAX(id), 0)
    INTO maximum_id
    FROM system_prompt_skill_bundle_versions;

    EXECUTE format('SELECT last_value, is_called FROM %s', sequence_name::regclass)
    INTO sequence_value, sequence_called;

    IF maximum_id > sequence_value THEN
        PERFORM setval(sequence_name::regclass, maximum_id, TRUE);
    ELSIF maximum_id > 0 AND NOT sequence_called THEN
        PERFORM setval(sequence_name::regclass, sequence_value, TRUE);
    END IF;
END;
$remote_skill_bundle_sequence$;

CREATE OR REPLACE FUNCTION protect_system_prompt_skill_prompt_version()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.raw_sha256 IS DISTINCT FROM NEW.raw_sha256
       OR OLD.effective_sha256 IS DISTINCT FROM NEW.effective_sha256
       OR OLD.raw_body IS DISTINCT FROM NEW.raw_body
       OR OLD.effective_body IS DISTINCT FROM NEW.effective_body
       OR OLD.diff IS DISTINCT FROM NEW.diff
       OR OLD.created_by IS DISTINCT FROM NEW.created_by
       OR OLD.created_at IS DISTINCT FROM NEW.created_at THEN
        RAISE EXCEPTION 'system prompt skill prompt version is immutable';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_protect_system_prompt_skill_prompt_version
    ON system_prompt_skill_prompt_versions;
CREATE TRIGGER trg_protect_system_prompt_skill_prompt_version
BEFORE UPDATE ON system_prompt_skill_prompt_versions
FOR EACH ROW
EXECUTE FUNCTION protect_system_prompt_skill_prompt_version();

CREATE OR REPLACE FUNCTION protect_system_prompt_skill_bundle_version()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.file_count IS DISTINCT FROM NEW.file_count
       OR OLD.raw_tree_sha256 IS DISTINCT FROM NEW.raw_tree_sha256
       OR OLD.effective_tree_sha256 IS DISTINCT FROM NEW.effective_tree_sha256
       OR OLD.prompt_version_id IS DISTINCT FROM NEW.prompt_version_id
       OR OLD.raw_total_bytes IS DISTINCT FROM NEW.raw_total_bytes
       OR OLD.effective_total_bytes IS DISTINCT FROM NEW.effective_total_bytes
       OR OLD.file_changes IS DISTINCT FROM NEW.file_changes
       OR OLD.upstream_source_id IS DISTINCT FROM NEW.upstream_source_id
       OR OLD.upstream_root IS DISTINCT FROM NEW.upstream_root
       OR OLD.public_root IS DISTINCT FROM NEW.public_root
       OR OLD.fetched_at IS DISTINCT FROM NEW.fetched_at
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

DROP TRIGGER IF EXISTS trg_protect_system_prompt_skill_bundle_version
    ON system_prompt_skill_bundle_versions;
CREATE TRIGGER trg_protect_system_prompt_skill_bundle_version
BEFORE UPDATE ON system_prompt_skill_bundle_versions
FOR EACH ROW
EXECUTE FUNCTION protect_system_prompt_skill_bundle_version();

CREATE OR REPLACE FUNCTION prevent_system_prompt_skill_bundle_version_delete()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF COALESCE(current_setting('sub2api.remote_skill_cleanup', TRUE), '') <> 'on' THEN
        RAISE EXCEPTION 'system prompt skill bundle versions cannot be deleted';
    END IF;
    RETURN OLD;
END;
$$;

DROP TRIGGER IF EXISTS trg_prevent_system_prompt_skill_bundle_version_delete
    ON system_prompt_skill_bundle_versions;
CREATE TRIGGER trg_prevent_system_prompt_skill_bundle_version_delete
BEFORE DELETE ON system_prompt_skill_bundle_versions
FOR EACH ROW
EXECUTE FUNCTION prevent_system_prompt_skill_bundle_version_delete();

CREATE OR REPLACE FUNCTION prevent_system_prompt_skill_prompt_version_delete()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF COALESCE(current_setting('sub2api.remote_skill_cleanup', TRUE), '') <> 'on' THEN
        RAISE EXCEPTION 'system prompt skill prompt versions cannot be deleted';
    END IF;
    RETURN OLD;
END;
$$;

DROP TRIGGER IF EXISTS trg_prevent_system_prompt_skill_prompt_version_delete
    ON system_prompt_skill_prompt_versions;
CREATE TRIGGER trg_prevent_system_prompt_skill_prompt_version_delete
BEFORE DELETE ON system_prompt_skill_prompt_versions
FOR EACH ROW
EXECUTE FUNCTION prevent_system_prompt_skill_prompt_version_delete();
