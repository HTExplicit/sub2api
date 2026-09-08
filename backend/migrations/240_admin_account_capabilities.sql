-- Persistent, explicitly initiated capability observations and publication audit.
-- Existing groups remain unmanaged until an approved changeset is applied.
ALTER TABLE groups ADD COLUMN IF NOT EXISTS managed_model_routes JSONB NOT NULL DEFAULT '{}';

CREATE TABLE admin_capability_runs (
    id BIGSERIAL PRIMARY KEY,
    created_by BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    kind VARCHAR(16) NOT NULL,
    idempotency_key VARCHAR(255) NOT NULL,
    request_hash CHAR(64) NOT NULL,
    folder_ids JSONB NOT NULL,
    account_ids JSONB NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    target_count INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    UNIQUE (created_by, idempotency_key)
);

CREATE TABLE admin_capability_items (
    id BIGSERIAL PRIMARY KEY,
    run_id BIGINT NOT NULL REFERENCES admin_capability_runs(id) ON DELETE RESTRICT,
    ordinal INTEGER NOT NULL,
    account_id BIGINT NOT NULL,
    account_name VARCHAR(255) NOT NULL,
    folder_id BIGINT NOT NULL,
    config_fingerprint CHAR(64) NOT NULL,
    upstream_model TEXT NOT NULL DEFAULT '',
    protocol VARCHAR(32) NOT NULL DEFAULT '',
    profile VARCHAR(32) NOT NULL DEFAULT 'text',
    aliases JSONB NOT NULL DEFAULT '[]',
    status VARCHAR(24) NOT NULL DEFAULT 'pending',
    result JSONB NOT NULL DEFAULT '{}',
    request_count INTEGER NOT NULL DEFAULT 0,
    claimed_at TIMESTAMPTZ,
    dispatched_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (run_id, ordinal),
    UNIQUE (run_id, account_id, upstream_model, protocol, profile)
);
CREATE INDEX admin_capability_items_claim_idx ON admin_capability_items(status, run_id, ordinal);
CREATE UNIQUE INDEX admin_capability_items_one_running_account_idx
    ON admin_capability_items(account_id) WHERE status = 'running';
CREATE INDEX admin_capability_items_latest_idx
    ON admin_capability_items(account_id, upstream_model, protocol, profile, finished_at DESC);
CREATE INDEX admin_capability_runs_created_idx ON admin_capability_runs(created_at DESC, id DESC);

CREATE TABLE admin_capability_changesets (
    id BIGSERIAL PRIMARY KEY,
    idempotency_key VARCHAR(128) NOT NULL UNIQUE,
    request JSONB NOT NULL,
    plan JSONB NOT NULL,
    before_fingerprint CHAR(64) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'preview' CHECK (status IN ('preview', 'applied')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    applied_at TIMESTAMPTZ
);

-- A managed route is part of authorization and scheduling. Persist invalidation
-- in the same transaction even when a writer does not use the management UI.
CREATE OR REPLACE FUNCTION enqueue_managed_model_routes_invalidations()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM enqueue_group_api_key_auth_cache_invalidations(NEW.id);
    PERFORM enqueue_channel_group_scheduler_invalidation(NEW.id);
    RETURN NEW;
END;
$$;
CREATE TRIGGER trg_groups_managed_model_routes_invalidation
AFTER UPDATE OF managed_model_routes, model_allowlist ON groups
FOR EACH ROW
WHEN (
    OLD.managed_model_routes IS DISTINCT FROM NEW.managed_model_routes
    OR (
        (OLD.managed_model_routes ->> 'enabled' = 'true' OR NEW.managed_model_routes ->> 'enabled' = 'true')
        AND OLD.model_allowlist IS DISTINCT FROM NEW.model_allowlist
    )
)
EXECUTE FUNCTION enqueue_managed_model_routes_invalidations();
