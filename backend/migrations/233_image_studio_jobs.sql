CREATE TABLE IF NOT EXISTS image_studio_jobs (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id) ON DELETE RESTRICT,
    mode VARCHAR(16) NOT NULL,
    model VARCHAR(64) NOT NULL,
    prompt TEXT NOT NULL,
    size VARCHAR(32) NOT NULL DEFAULT '',
    quality VARCHAR(32) NOT NULL DEFAULT '',
    count SMALLINT NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    processed_count SMALLINT NOT NULL DEFAULT 0,
    succeeded_count SMALLINT NOT NULL DEFAULT 0,
    failed_count SMALLINT NOT NULL DEFAULT 0,
    canceled_count SMALLINT NOT NULL DEFAULT 0,
    cancel_requested_at TIMESTAMPTZ,
    error_code VARCHAR(96),
    error_message VARCHAR(512),
    request_expires_at TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '24 hours'),
    retain_until TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '30 days'),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT image_studio_jobs_mode_check CHECK (mode IN ('generate', 'edit')),
    CONSTRAINT image_studio_jobs_model_check CHECK (model IN ('gpt-image-2', 'gemini-3-pro-image')),
    CONSTRAINT image_studio_jobs_count_check CHECK (count BETWEEN 1 AND 4),
    CONSTRAINT image_studio_jobs_status_check CHECK (
        status IN (
            'pending', 'preparing', 'running', 'succeeded',
            'partially_succeeded', 'failed', 'canceled', 'canceled_with_results'
        )
    ),
    CONSTRAINT image_studio_jobs_count_bounds CHECK (
        processed_count BETWEEN 0 AND count
        AND succeeded_count BETWEEN 0 AND processed_count
        AND failed_count BETWEEN 0 AND processed_count
        AND canceled_count BETWEEN 0 AND processed_count
        AND succeeded_count + failed_count + canceled_count = processed_count
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS image_studio_jobs_one_active_user_uq
    ON image_studio_jobs (user_id)
    WHERE status IN ('pending', 'preparing', 'running');

CREATE INDEX IF NOT EXISTS image_studio_jobs_claim_idx
    ON image_studio_jobs (status, created_at, id)
    WHERE status IN ('pending', 'running');

CREATE INDEX IF NOT EXISTS image_studio_jobs_retention_idx
    ON image_studio_jobs (retain_until);

CREATE TABLE IF NOT EXISTS image_studio_items (
    id BIGSERIAL PRIMARY KEY,
    job_id BIGINT NOT NULL REFERENCES image_studio_jobs(id) ON DELETE CASCADE,
    ordinal SMALLINT NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'pending',
    error_code VARCHAR(96),
    error_message VARCHAR(512),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT image_studio_items_ordinal_check CHECK (ordinal BETWEEN 1 AND 4),
    CONSTRAINT image_studio_items_status_check CHECK (
        status IN ('pending', 'running', 'succeeded', 'failed', 'canceled')
    ),
    CONSTRAINT image_studio_items_job_ordinal_uq UNIQUE (job_id, ordinal)
);

CREATE INDEX IF NOT EXISTS image_studio_items_job_status_idx
    ON image_studio_items (job_id, status, ordinal);

CREATE TABLE IF NOT EXISTS image_studio_artifacts (
    id BIGSERIAL PRIMARY KEY,
    job_id BIGINT NOT NULL REFERENCES image_studio_jobs(id) ON DELETE CASCADE,
    item_id BIGINT REFERENCES image_studio_items(id) ON DELETE CASCADE,
    kind VARCHAR(24) NOT NULL,
    storage_key VARCHAR(96) NOT NULL UNIQUE,
    content_type VARCHAR(64) NOT NULL,
    byte_size BIGINT NOT NULL CHECK (byte_size > 0),
    revised_prompt VARCHAR(2048),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT image_studio_artifacts_kind_check CHECK (kind IN ('reference', 'mask', 'output')),
    CONSTRAINT image_studio_artifacts_content_type_check CHECK (
        content_type IN ('image/png', 'image/jpeg', 'image/webp')
    )
);

CREATE INDEX IF NOT EXISTS image_studio_artifacts_job_idx
    ON image_studio_artifacts (job_id, item_id, id);

CREATE INDEX IF NOT EXISTS image_studio_artifacts_expiry_idx
    ON image_studio_artifacts (expires_at);
