-- Account jobs keep the original error text of a failed job or item. Upstream
-- responses and connection errors are often longer than 512 characters, and a
-- rejected write left the job or item without its result. VARCHAR(512) to TEXT
-- only changes the catalog: no table rewrite and no index on these columns.
-- Running it again is a no-op.
SET LOCAL lock_timeout = '10s';
SET LOCAL statement_timeout = '120s';

ALTER TABLE admin_account_jobs
    ALTER COLUMN error_message TYPE TEXT;

ALTER TABLE admin_account_job_items
    ALTER COLUMN error_message TYPE TEXT;
