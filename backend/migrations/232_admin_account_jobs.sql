CREATE TABLE IF NOT EXISTS admin_account_jobs (
    id BIGSERIAL PRIMARY KEY,
    created_by BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    kind VARCHAR(64) NOT NULL,
    idempotency_key VARCHAR(255) NOT NULL,
    request_hash CHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    raw_payload_ciphertext TEXT,
    raw_payload_expires_at TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '24 hours'),
    metadata JSONB NOT NULL DEFAULT '{}'::JSONB,
    target_count INTEGER NOT NULL DEFAULT 0 CHECK (target_count >= 0),
    processed_count INTEGER NOT NULL DEFAULT 0 CHECK (processed_count >= 0),
    succeeded_count INTEGER NOT NULL DEFAULT 0 CHECK (succeeded_count >= 0),
    failed_count INTEGER NOT NULL DEFAULT 0 CHECK (failed_count >= 0),
    canceled_count INTEGER NOT NULL DEFAULT 0 CHECK (canceled_count >= 0),
    cancel_requested_at TIMESTAMPTZ,
    error_code VARCHAR(96),
    error_message VARCHAR(512),
    retry_of_job_id BIGINT REFERENCES admin_account_jobs(id) ON DELETE SET NULL,
    attempt INTEGER NOT NULL DEFAULT 1 CHECK (attempt > 0),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT admin_account_jobs_status_check CHECK (
        status IN ('pending', 'running', 'succeeded', 'partially_succeeded', 'failed', 'canceled')
    ),
    CONSTRAINT admin_account_jobs_request_hash_format CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT admin_account_jobs_count_bounds CHECK (
        processed_count <= target_count
        AND succeeded_count + failed_count + canceled_count <= processed_count
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS admin_account_jobs_idempotency_uq
    ON admin_account_jobs (created_by, kind, idempotency_key);

CREATE UNIQUE INDEX IF NOT EXISTS admin_account_jobs_admin_kind_running_uq
    ON admin_account_jobs (created_by, kind)
    WHERE status = 'running';

CREATE INDEX IF NOT EXISTS admin_account_jobs_claim_idx
    ON admin_account_jobs (status, created_at, id)
    WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS admin_account_jobs_payload_expiry_idx
    ON admin_account_jobs (raw_payload_expires_at)
    WHERE raw_payload_ciphertext IS NOT NULL;

CREATE INDEX IF NOT EXISTS admin_account_jobs_retention_idx
    ON admin_account_jobs (finished_at)
    WHERE finished_at IS NOT NULL;

CREATE TABLE IF NOT EXISTS admin_account_job_items (
    id BIGSERIAL PRIMARY KEY,
    job_id BIGINT NOT NULL REFERENCES admin_account_jobs(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL CHECK (ordinal > 0),
    action VARCHAR(32) NOT NULL DEFAULT '',
    target_account_id BIGINT,
    status VARCHAR(24) NOT NULL DEFAULT 'pending',
    metadata JSONB NOT NULL DEFAULT '{}'::JSONB,
    error_code VARCHAR(96),
    error_message VARCHAR(512),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT admin_account_job_items_status_check CHECK (
        status IN ('pending', 'running', 'succeeded', 'failed', 'canceled')
    ),
    CONSTRAINT admin_account_job_items_job_ordinal_uq UNIQUE (job_id, ordinal)
);

CREATE INDEX IF NOT EXISTS admin_account_job_items_job_status_idx
    ON admin_account_job_items (job_id, status, ordinal);

CREATE INDEX IF NOT EXISTS admin_account_job_items_target_idx
    ON admin_account_job_items (target_account_id)
    WHERE target_account_id IS NOT NULL;
