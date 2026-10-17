-- Preserve existing observations as sample one and permit independent repeats.
ALTER TABLE codex_gateway_borrow_test_results
    ADD COLUMN repeat_index INTEGER NOT NULL DEFAULT 1 CHECK (repeat_index > 0);

-- PostgreSQL truncates generated identifiers; locate this exact legacy key by
-- its columns rather than guessing its truncated name.
DO $$
DECLARE legacy_key TEXT;
BEGIN
    SELECT conname INTO STRICT legacy_key FROM pg_constraint
    WHERE conrelid = 'codex_gateway_borrow_test_results'::regclass
      AND contype = 'u'
      AND pg_get_constraintdef(oid) = 'UNIQUE (task_id, account_id, model_id)';
    EXECUTE format('ALTER TABLE codex_gateway_borrow_test_results DROP CONSTRAINT %I', legacy_key);
END $$;

ALTER TABLE codex_gateway_borrow_test_results
    ADD CONSTRAINT pelican_result_sample_unique UNIQUE (task_id, account_id, model_id, repeat_index);
