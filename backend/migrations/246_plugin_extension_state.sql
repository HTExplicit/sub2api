-- Extension bindings share lifecycle state, but non-exclusive hooks may have
-- multiple contributing plugins. The official outbound transport stays unique.
DROP INDEX IF EXISTS idx_sub2api_plugin_bindings_one_enabled_scope;
CREATE UNIQUE INDEX idx_sub2api_plugin_bindings_one_enabled_scope
    ON sub2api_plugin_bindings(capability, platform, account_type)
    WHERE enabled = TRUE AND capability IN ('openai.oauth.outbound_transport.v1', 'extensions.provider.v1');

CREATE TABLE IF NOT EXISTS sub2api_plugin_state (
    plugin_key VARCHAR(160) NOT NULL,
    namespace VARCHAR(128) NOT NULL,
    state_key VARCHAR(256) NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    value JSONB NOT NULL,
	 next_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (plugin_key, namespace, state_key)
);

CREATE INDEX IF NOT EXISTS sub2api_plugin_state_due ON sub2api_plugin_state(plugin_key,namespace,next_at) WHERE next_at IS NOT NULL;

CREATE TABLE IF NOT EXISTS sub2api_plugin_leases (
    plugin_key VARCHAR(160) NOT NULL,
    namespace VARCHAR(128) NOT NULL,
    lease_key VARCHAR(256) NOT NULL,
    owner VARCHAR(256) NOT NULL,
    generation BIGINT NOT NULL DEFAULT 1 CHECK (generation > 0),
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (plugin_key, namespace, lease_key)
);
