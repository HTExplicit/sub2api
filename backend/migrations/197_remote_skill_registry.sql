-- Content-addressed remote skill candidates and the independently revisioned
-- public active pointer. Sync jobs never change the active pointer.

CREATE TABLE IF NOT EXISTS system_prompt_skill_bundle_versions (
    id BIGSERIAL PRIMARY KEY,
    bundle_id VARCHAR(128) NOT NULL,
    source_commit CHAR(40) NOT NULL,
    overlay_sha256 CHAR(64) NOT NULL,
    manifest_sha256 CHAR(64) NOT NULL UNIQUE,
    archive_sha256 CHAR(64) NOT NULL,
    file_count INTEGER NOT NULL CHECK (file_count > 0 AND file_count <= 2000),
    total_bytes BIGINT NOT NULL CHECK (total_bytes > 0 AND total_bytes <= 268435456),
    added_files INTEGER NOT NULL DEFAULT 0 CHECK (added_files >= 0),
    modified_files INTEGER NOT NULL DEFAULT 0 CHECK (modified_files >= 0),
    deleted_files INTEGER NOT NULL DEFAULT 0 CHECK (deleted_files >= 0),
    script_changes INTEGER NOT NULL DEFAULT 0 CHECK (script_changes >= 0),
    binary_changes INTEGER NOT NULL DEFAULT 0 CHECK (binary_changes >= 0),
    created_by BIGINT,
    published_at TIMESTAMPTZ,
    published_by BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT system_prompt_skill_bundle_id CHECK (bundle_id = 'codexrip-reverse-skill'),
    CONSTRAINT system_prompt_skill_source_commit_hex CHECK (source_commit ~ '^[0-9a-f]{40}$'),
    CONSTRAINT system_prompt_skill_overlay_sha256_hex CHECK (overlay_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT system_prompt_skill_manifest_sha256_hex CHECK (manifest_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT system_prompt_skill_archive_sha256_hex CHECK (archive_sha256 ~ '^[0-9a-f]{64}$')
);

CREATE TABLE IF NOT EXISTS system_prompt_skill_runtime (
    id SMALLINT PRIMARY KEY DEFAULT 1,
    active_bundle_version_id BIGINT REFERENCES system_prompt_skill_bundle_versions(id) ON DELETE RESTRICT,
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_by BIGINT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (id = 1)
);

CREATE TABLE IF NOT EXISTS system_prompt_skill_sync_jobs (
    id BIGSERIAL PRIMARY KEY,
    status VARCHAR(16) NOT NULL DEFAULT 'queued',
    progress_stage VARCHAR(64) NOT NULL DEFAULT 'queued',
    source_commit CHAR(40),
    candidate_bundle_version_id BIGINT REFERENCES system_prompt_skill_bundle_versions(id) ON DELETE RESTRICT,
    error_code VARCHAR(100),
    created_by BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    CONSTRAINT system_prompt_skill_sync_status CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
    CONSTRAINT system_prompt_skill_sync_source_commit_hex CHECK (
        source_commit IS NULL OR source_commit ~ '^[0-9a-f]{40}$'
    )
);

CREATE INDEX IF NOT EXISTS idx_system_prompt_skill_bundle_versions_created
    ON system_prompt_skill_bundle_versions(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_system_prompt_skill_sync_jobs_created
    ON system_prompt_skill_sync_jobs(created_at DESC);

INSERT INTO system_prompt_skill_runtime (id, revision)
VALUES (1, 1)
ON CONFLICT (id) DO NOTHING;

CREATE OR REPLACE FUNCTION protect_system_prompt_skill_bundle_version()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.bundle_id IS DISTINCT FROM NEW.bundle_id
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
    RAISE EXCEPTION 'system prompt skill bundle versions cannot be deleted';
END;
$$;

DROP TRIGGER IF EXISTS trg_prevent_system_prompt_skill_bundle_version_delete
    ON system_prompt_skill_bundle_versions;
CREATE TRIGGER trg_prevent_system_prompt_skill_bundle_version_delete
BEFORE DELETE ON system_prompt_skill_bundle_versions
FOR EACH ROW
EXECUTE FUNCTION prevent_system_prompt_skill_bundle_version_delete();
