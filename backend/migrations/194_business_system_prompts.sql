-- Versioned, globally active business system prompts. The runtime row is a
-- singleton and defaults to fully disabled so deploying this migration cannot
-- alter any upstream request until an administrator explicitly enables it.

CREATE TABLE IF NOT EXISTS system_prompt_templates (
    id BIGSERIAL PRIMARY KEY,
    slug VARCHAR(100) NOT NULL UNIQUE,
    name VARCHAR(200) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    is_seed BOOLEAN NOT NULL DEFAULT FALSE,
    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT system_prompt_templates_slug_nonempty CHECK (BTRIM(slug) <> ''),
    CONSTRAINT system_prompt_templates_name_nonempty CHECK (BTRIM(name) <> '')
);

CREATE TABLE IF NOT EXISTS system_prompt_template_versions (
    id BIGSERIAL PRIMARY KEY,
    template_id BIGINT NOT NULL REFERENCES system_prompt_templates(id) ON DELETE RESTRICT,
    version BIGINT NOT NULL CHECK (version > 0),
    body TEXT NOT NULL,
    sha256 CHAR(64) NOT NULL,
    byte_length INTEGER NOT NULL CHECK (byte_length > 0 AND byte_length <= 65536),
    note VARCHAR(500) NOT NULL DEFAULT '',
    created_by BIGINT,
    published_at TIMESTAMPTZ,
    published_by BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (template_id, version),
    UNIQUE (id, template_id),
    CONSTRAINT system_prompt_template_versions_sha256_hex CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT system_prompt_template_versions_body_nonempty CHECK (BTRIM(body) <> '')
);

CREATE TABLE IF NOT EXISTS system_prompt_runtime (
    id SMALLINT PRIMARY KEY DEFAULT 1,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    expose_server_prompt BOOLEAN NOT NULL DEFAULT FALSE,
    compact_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    active_template_id BIGINT REFERENCES system_prompt_templates(id) ON DELETE RESTRICT,
    active_version_id BIGINT,
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_by BIGINT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (id = 1),
    CONSTRAINT system_prompt_runtime_active_pair CHECK (
        (active_template_id IS NULL AND active_version_id IS NULL)
        OR (active_template_id IS NOT NULL AND active_version_id IS NOT NULL)
    ),
    CONSTRAINT system_prompt_runtime_version_template_fk
        FOREIGN KEY (active_version_id, active_template_id)
        REFERENCES system_prompt_template_versions(id, template_id)
        ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_system_prompt_templates_deleted_at
    ON system_prompt_templates(deleted_at);
CREATE INDEX IF NOT EXISTS idx_system_prompt_template_versions_template_created
    ON system_prompt_template_versions(template_id, version DESC);

INSERT INTO system_prompt_runtime (
    id, enabled, expose_server_prompt, compact_enabled, revision
)
VALUES (1, FALSE, FALSE, FALSE, 1)
ON CONFLICT (id) DO NOTHING;

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

CREATE OR REPLACE FUNCTION prevent_system_prompt_version_delete()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'system prompt versions cannot be deleted';
END;
$$;

DROP TRIGGER IF EXISTS trg_prevent_system_prompt_version_delete
    ON system_prompt_template_versions;
CREATE TRIGGER trg_prevent_system_prompt_version_delete
BEFORE DELETE ON system_prompt_template_versions
FOR EACH ROW
EXECUTE FUNCTION prevent_system_prompt_version_delete();
