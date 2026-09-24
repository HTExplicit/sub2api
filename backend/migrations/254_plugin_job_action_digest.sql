-- Plugin jobs use a complete SHA-256 hex digest as the per-target payload key.
-- Preserve existing actions while allowing all 64 hexadecimal characters.
ALTER TABLE admin_account_job_items
    ALTER COLUMN action TYPE VARCHAR(64);
