-- Extend the existing manual observations. Existing works and their bytes remain intact.
ALTER TABLE codex_gateway_borrow_test_tasks
    ADD COLUMN test_config JSONB NOT NULL DEFAULT '{}';
ALTER TABLE codex_gateway_borrow_test_results
    ADD COLUMN evaluation JSONB,
    ADD COLUMN evidence BYTEA NOT NULL DEFAULT ''::bytea,
    ADD COLUMN raw_svg BYTEA NOT NULL DEFAULT ''::bytea,
    ADD COLUMN review_image BYTEA NOT NULL DEFAULT ''::bytea;

CREATE INDEX idx_pelican_results_gallery ON codex_gateway_borrow_test_results (started_at DESC, id DESC);
