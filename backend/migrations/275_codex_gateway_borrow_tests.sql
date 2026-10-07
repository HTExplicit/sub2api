-- Manual gateway-borrow pelican observations, isolated from account health and
-- existing diagnostics. Task and children have one 24-hour retention boundary.
CREATE TABLE IF NOT EXISTS codex_gateway_borrow_test_tasks (
    id UUID PRIMARY KEY,
    client_task_id UUID NOT NULL UNIQUE,
    scope TEXT NOT NULL DEFAULT 'manual_pelican' CHECK (scope = 'manual_pelican'),
    created_by BIGINT NOT NULL,
    request_hash TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending','running','complete','failed','incomplete','cancelled','skipped')),
    prompt TEXT NOT NULL,
    total INTEGER NOT NULL CHECK (total > 0),
    completed INTEGER NOT NULL DEFAULT 0 CHECK (completed >= 0 AND completed <= total),
    error BYTEA NOT NULL DEFAULT '\x'::bytea,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (expires_at - created_at = INTERVAL '24 hours')
);

CREATE INDEX IF NOT EXISTS codex_gateway_borrow_test_tasks_expiry
    ON codex_gateway_borrow_test_tasks (expires_at);
CREATE INDEX IF NOT EXISTS codex_gateway_borrow_test_tasks_created
    ON codex_gateway_borrow_test_tasks (created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS codex_gateway_borrow_test_results (
    id UUID PRIMARY KEY,
    task_id UUID NOT NULL REFERENCES codex_gateway_borrow_test_tasks(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL CHECK (ordinal > 0),
    account_id BIGINT NOT NULL CHECK (account_id > 0),
    account_name TEXT NOT NULL DEFAULT '',
    model_id TEXT NOT NULL,
    upstream_model TEXT NOT NULL DEFAULT '',
    effort TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending','running','complete','failed','incomplete','cancelled','skipped')),
    -- BYTEA preserves original output/errors including NUL without sanitizing.
    raw_answer BYTEA NOT NULL DEFAULT '\x'::bytea,
    raw_response BYTEA NOT NULL DEFAULT '\x'::bytea,
    raw_html BYTEA NOT NULL DEFAULT '\x'::bytea,
    html BYTEA NOT NULL DEFAULT '\x'::bytea,
    error BYTEA NOT NULL DEFAULT '\x'::bytea,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    duration_ms BIGINT NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (task_id, account_id, model_id),
    UNIQUE (task_id, ordinal)
);
