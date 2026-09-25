-- Storage only. The application validates the active on-disk publication and
-- migrates content plus its complete public tree in one database transaction.
-- No embedded seed or archived hybrid body may substitute for that publication.
CREATE TABLE IF NOT EXISTS system_prompt_independent_state (
    id SMALLINT PRIMARY KEY CHECK (id = 1),
    completed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS system_prompt_independent_contents (
    template_id BIGINT PRIMARY KEY REFERENCES system_prompt_templates(id),
    rule_id TEXT NOT NULL UNIQUE,
    source_template_id BIGINT NOT NULL REFERENCES system_prompt_templates(id),
    baseline_version_id BIGINT NOT NULL REFERENCES system_prompt_template_versions(id)
);

CREATE TABLE IF NOT EXISTS system_prompt_version_compat (
    version_id BIGINT PRIMARY KEY REFERENCES system_prompt_template_versions(id),
    preserve_echo BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE TABLE IF NOT EXISTS system_prompt_frozen_public_files (
    path TEXT PRIMARY KEY,
    body BYTEA NOT NULL CHECK (octet_length(body) > 0),
    sha256 CHAR(64) NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$')
);

CREATE OR REPLACE FUNCTION protect_system_prompt_frozen_content()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'independent prompt migration records are immutable';
END;
$$;

CREATE TRIGGER trg_protect_prompt_independent_state
BEFORE UPDATE OR DELETE ON system_prompt_independent_state
FOR EACH ROW EXECUTE FUNCTION protect_system_prompt_frozen_content();
CREATE TRIGGER trg_protect_prompt_independent_contents
BEFORE UPDATE OR DELETE ON system_prompt_independent_contents
FOR EACH ROW EXECUTE FUNCTION protect_system_prompt_frozen_content();
CREATE TRIGGER trg_protect_prompt_version_compat
BEFORE UPDATE OR DELETE ON system_prompt_version_compat
FOR EACH ROW EXECUTE FUNCTION protect_system_prompt_frozen_content();
CREATE TRIGGER trg_protect_prompt_frozen_public_files
BEFORE UPDATE OR DELETE ON system_prompt_frozen_public_files
FOR EACH ROW EXECUTE FUNCTION protect_system_prompt_frozen_content();
