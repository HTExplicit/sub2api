-- Retain the original submission identity until expiry so deletion cannot
-- turn an uncertain/repeated POST into another model invocation.
ALTER TABLE codex_gateway_borrow_test_tasks
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
