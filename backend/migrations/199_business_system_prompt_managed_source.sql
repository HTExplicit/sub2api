-- Add immutable source provenance for built-in prompt templates. This migration
-- is structural only: application startup creates seeds in deterministic order.

ALTER TABLE system_prompt_templates
    ADD COLUMN IF NOT EXISTS managed_source VARCHAR(100);

ALTER TABLE system_prompt_template_versions
    ADD COLUMN IF NOT EXISTS source_repository VARCHAR(200),
    ADD COLUMN IF NOT EXISTS source_commit CHAR(40),
    ADD COLUMN IF NOT EXISTS source_version VARCHAR(32),
    ADD COLUMN IF NOT EXISTS source_artifact VARCHAR(255),
    ADD COLUMN IF NOT EXISTS source_artifact_sha256 CHAR(64),
    ADD COLUMN IF NOT EXISTS source_license_sha256 CHAR(64);

CREATE UNIQUE INDEX IF NOT EXISTS idx_system_prompt_templates_managed_source
    ON system_prompt_templates(managed_source)
    WHERE managed_source IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_system_prompt_source_version_identity
    ON system_prompt_template_versions(source_repository, source_commit, source_artifact_sha256)
    WHERE source_repository IS NOT NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'system_prompt_templates_managed_source_nonempty'
    ) THEN
        ALTER TABLE system_prompt_templates
            ADD CONSTRAINT system_prompt_templates_managed_source_nonempty
            CHECK (managed_source IS NULL OR BTRIM(managed_source) <> '');
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'system_prompt_source_fields_all_or_none'
    ) THEN
        ALTER TABLE system_prompt_template_versions
            ADD CONSTRAINT system_prompt_source_fields_all_or_none CHECK (
                (source_repository IS NULL
                 AND source_commit IS NULL
                 AND source_version IS NULL
                 AND source_artifact IS NULL
                 AND source_artifact_sha256 IS NULL
                 AND source_license_sha256 IS NULL)
                OR
                (source_repository IS NOT NULL
                 AND source_commit IS NOT NULL
                 AND source_version IS NOT NULL
                 AND source_artifact IS NOT NULL
                 AND source_artifact_sha256 IS NOT NULL
                 AND source_license_sha256 IS NOT NULL)
            );
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'system_prompt_source_commit_hex'
    ) THEN
        ALTER TABLE system_prompt_template_versions
            ADD CONSTRAINT system_prompt_source_commit_hex
            CHECK (source_commit IS NULL OR source_commit ~ '^[0-9a-f]{40}$');
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'system_prompt_source_version_format'
    ) THEN
        ALTER TABLE system_prompt_template_versions
            ADD CONSTRAINT system_prompt_source_version_format
            CHECK (source_version IS NULL OR source_version ~ '^v[1-9][0-9]*$');
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'system_prompt_source_artifact_sha256_hex'
    ) THEN
        ALTER TABLE system_prompt_template_versions
            ADD CONSTRAINT system_prompt_source_artifact_sha256_hex
            CHECK (source_artifact_sha256 IS NULL OR source_artifact_sha256 ~ '^[0-9a-f]{64}$');
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'system_prompt_source_license_sha256_hex'
    ) THEN
        ALTER TABLE system_prompt_template_versions
            ADD CONSTRAINT system_prompt_source_license_sha256_hex
            CHECK (source_license_sha256 IS NULL OR source_license_sha256 ~ '^[0-9a-f]{64}$');
    END IF;
END
$$;

CREATE OR REPLACE FUNCTION protect_system_prompt_template_managed_source()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.managed_source IS DISTINCT FROM NEW.managed_source THEN
        RAISE EXCEPTION 'system prompt managed source is immutable';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_protect_system_prompt_template_managed_source
    ON system_prompt_templates;
CREATE TRIGGER trg_protect_system_prompt_template_managed_source
BEFORE UPDATE ON system_prompt_templates
FOR EACH ROW
EXECUTE FUNCTION protect_system_prompt_template_managed_source();

CREATE OR REPLACE FUNCTION protect_system_prompt_version_content()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.template_id IS DISTINCT FROM NEW.template_id
       OR OLD.version IS DISTINCT FROM NEW.version
       OR OLD.body IS DISTINCT FROM NEW.body
       OR OLD.sha256 IS DISTINCT FROM NEW.sha256
       OR OLD.byte_length IS DISTINCT FROM NEW.byte_length
       OR OLD.composition_mode IS DISTINCT FROM NEW.composition_mode
       OR OLD.bundle_id IS DISTINCT FROM NEW.bundle_id
       OR OLD.bundle_manifest_sha256 IS DISTINCT FROM NEW.bundle_manifest_sha256
       OR OLD.source_repository IS DISTINCT FROM NEW.source_repository
       OR OLD.source_commit IS DISTINCT FROM NEW.source_commit
       OR OLD.source_version IS DISTINCT FROM NEW.source_version
       OR OLD.source_artifact IS DISTINCT FROM NEW.source_artifact
       OR OLD.source_artifact_sha256 IS DISTINCT FROM NEW.source_artifact_sha256
       OR OLD.source_license_sha256 IS DISTINCT FROM NEW.source_license_sha256
       OR OLD.note IS DISTINCT FROM NEW.note
       OR OLD.created_by IS DISTINCT FROM NEW.created_by
       OR OLD.created_at IS DISTINCT FROM NEW.created_at THEN
        RAISE EXCEPTION 'system prompt version content is immutable';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_protect_system_prompt_version_content
    ON system_prompt_template_versions;
CREATE TRIGGER trg_protect_system_prompt_version_content
BEFORE UPDATE ON system_prompt_template_versions
FOR EACH ROW
EXECUTE FUNCTION protect_system_prompt_version_content();
