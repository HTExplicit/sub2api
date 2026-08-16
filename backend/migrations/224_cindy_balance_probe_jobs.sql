CREATE TABLE IF NOT EXISTS cindy_balance_probe_jobs (
    id BIGSERIAL PRIMARY KEY,
    status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN (
        'queued', 'running', 'paused', 'paused_upstream', 'cancel_requested',
        'completed', 'completed_with_issues', 'canceled'
    )),
    requested_by BIGINT NULL REFERENCES users(id) ON DELETE SET NULL,
    scope JSONB NOT NULL DEFAULT '{}'::jsonb,
    rate_rps NUMERIC(2,1) NOT NULL DEFAULT 0.5 CHECK (rate_rps >= 0.1 AND rate_rps <= 1.0),
    candidate_count INTEGER NOT NULL CHECK (candidate_count >= 0),
    candidate_fingerprint TEXT NOT NULL CHECK (length(candidate_fingerprint) = 64),
    request_count INTEGER NOT NULL DEFAULT 0 CHECK (request_count >= 0),
    consecutive_upstream_failures INTEGER NOT NULL DEFAULT 0 CHECK (consecutive_upstream_failures >= 0),
    last_request_started_at TIMESTAMPTZ NULL,
    lease_token TEXT NULL,
    lease_until TIMESTAMPTZ NULL,
    heartbeat_at TIMESTAMPTZ NULL,
    cancel_requested_at TIMESTAMPTZ NULL,
    started_at TIMESTAMPTZ NULL,
    finished_at TIMESTAMPTZ NULL,
    failure_reason TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_cindy_balance_probe_jobs_one_active
    ON cindy_balance_probe_jobs ((1))
    WHERE status IN ('queued', 'running', 'paused', 'paused_upstream', 'cancel_requested');

CREATE INDEX IF NOT EXISTS idx_cindy_balance_probe_jobs_created_at
    ON cindy_balance_probe_jobs (created_at DESC);

CREATE TABLE IF NOT EXISTS cindy_balance_probe_items (
    id BIGSERIAL PRIMARY KEY,
    job_id BIGINT NOT NULL REFERENCES cindy_balance_probe_jobs(id) ON DELETE CASCADE,
    account_id BIGINT NOT NULL,
    ordinal INTEGER NOT NULL CHECK (ordinal > 0),
    identity_fingerprint TEXT NOT NULL CHECK (length(identity_fingerprint) = 64),
    account_updated_at TIMESTAMPTZ NOT NULL,
    was_marked BOOLEAN NOT NULL DEFAULT FALSE,
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN (
        'pending', 'luna_running', 'luna_exact', 'terra_running',
        'healthy', 'recovered', 'still_exhausted', 'exhausted',
        'already_marked', 'inconclusive', 'confirmation_expired',
        'skipped_stale', 'unknown_after_crash', 'canceled'
    )),
    luna_outcome TEXT NULL,
    luna_at TIMESTAMPTZ NULL,
    terra_outcome TEXT NULL,
    terra_at TIMESTAMPTZ NULL,
    request_count SMALLINT NOT NULL DEFAULT 0 CHECK (request_count >= 0 AND request_count <= 2),
    final_outcome TEXT NULL,
    started_at TIMESTAMPTZ NULL,
    finished_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (job_id, account_id),
    UNIQUE (job_id, ordinal)
);

CREATE INDEX IF NOT EXISTS idx_cindy_balance_probe_items_job_state_ordinal
    ON cindy_balance_probe_items (job_id, state, ordinal);

CREATE INDEX IF NOT EXISTS idx_cindy_balance_probe_items_account_created
    ON cindy_balance_probe_items (account_id, created_at DESC);
