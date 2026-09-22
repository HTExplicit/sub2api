-- A package is an immutable program AND UI resource set. Runtime generations
-- fence old processes; revisions also change when configuration or intent does.
ALTER TABLE sub2api_plugin_installations
    DROP CONSTRAINT sub2api_plugin_installations_state_check,
    ADD CONSTRAINT sub2api_plugin_installations_state_check
        CHECK (state IN ('disabled','starting','enabled','error','incompatible','updating'));

ALTER TABLE sub2api_plugin_installations
    ADD COLUMN revision BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN runtime_generation BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN package_sha256 TEXT NOT NULL DEFAULT '',
    ADD COLUMN update_policy TEXT NOT NULL DEFAULT 'pinned'
        CHECK (update_policy IN ('bundled', 'pinned'));

UPDATE sub2api_plugin_installations
SET package_sha256=encode(sha256(artifact_data),'hex')
WHERE artifact_data IS NOT NULL;

UPDATE sub2api_plugin_installations p SET update_policy='bundled'
WHERE EXISTS (SELECT 1 FROM sub2api_plugin_bootstrap b WHERE b.plugin_key=p.plugin_key);

CREATE TABLE sub2api_plugin_updates (
    plugin_id BIGINT PRIMARY KEY REFERENCES sub2api_plugin_installations(id) ON DELETE CASCADE,
    artifact_data BYTEA NOT NULL,
    package_sha256 TEXT NOT NULL CHECK (package_sha256 ~ '^[a-f0-9]{64}$'),
    source_generation BIGINT NOT NULL,
    update_policy TEXT NOT NULL CHECK (update_policy IN ('bundled', 'pinned')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Count every changed desired state, including binding-only transactions. A
-- periodic healthy observation must not create a spurious configuration edit.
CREATE FUNCTION sub2api_plugin_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF ROW(NEW.manifest,NEW.config_encrypted,NEW.state,NEW.update_policy,NEW.runtime_generation)
       IS DISTINCT FROM ROW(OLD.manifest,OLD.config_encrypted,OLD.state,OLD.update_policy,OLD.runtime_generation)
       OR NEW.revision IS DISTINCT FROM OLD.revision THEN
        NEW.revision := OLD.revision + 1;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER sub2api_plugin_revision BEFORE UPDATE ON sub2api_plugin_installations
    FOR EACH ROW EXECUTE FUNCTION sub2api_plugin_revision();
