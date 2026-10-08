-- Extend the existing observation records in place. Original BYTEA answers and
-- errors, client UUID uniqueness, and the 24-hour retention boundary are kept.
ALTER TABLE codex_gateway_borrow_test_tasks
    ADD COLUMN generation_timeout_seconds INTEGER NOT NULL DEFAULT 90
        CHECK (generation_timeout_seconds BETWEEN 60 AND 1800),
    ADD COLUMN execution_mode TEXT NOT NULL DEFAULT 'legacy_cache'
        CHECK (execution_mode IN ('legacy_cache','account'));

-- Existing legacy observations used a 90-second generation clock. Future tasks
-- default to the new 10-minute budget; the runner always writes its saved budget.
ALTER TABLE codex_gateway_borrow_test_tasks
    ALTER COLUMN generation_timeout_seconds SET DEFAULT 600;

ALTER TABLE codex_gateway_borrow_test_results
    ADD COLUMN platform TEXT NOT NULL DEFAULT '',
    ADD COLUMN actual_endpoint TEXT NOT NULL DEFAULT '',
    ADD COLUMN actual_protocol TEXT NOT NULL DEFAULT '',
    ADD COLUMN actual_transport TEXT NOT NULL DEFAULT '',
    ADD COLUMN borrow_applied BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN queue_duration_ms BIGINT NOT NULL DEFAULT 0 CHECK (queue_duration_ms >= 0),
    ADD COLUMN preparation_duration_ms BIGINT NOT NULL DEFAULT 0 CHECK (preparation_duration_ms >= 0),
    ADD COLUMN generation_duration_ms BIGINT NOT NULL DEFAULT 0 CHECK (generation_duration_ms >= 0),
    ADD COLUMN generation_started_at TIMESTAMPTZ;
