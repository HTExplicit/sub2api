CREATE TABLE IF NOT EXISTS sub2api_plugin_bootstrap (
    plugin_key VARCHAR(160) PRIMARY KEY,
    bundle_sha256 VARCHAR(64) NOT NULL,
    migration_profile VARCHAR(160) NOT NULL,
    desired_enabled BOOLEAN NOT NULL,
    completed BOOLEAN NOT NULL DEFAULT FALSE,
    state_imported BOOLEAN NOT NULL DEFAULT FALSE,
    user_removed BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
