-- Drop usage_billing_dedup.account_id.
--
-- 238 added the column for the Cindy account statistics: every billing claim
-- stored the account that first settled the request. Those statistics went
-- with Cindy (260 dropped accounts.cindy_account_stats_reset_at, the other half
-- of 238). Nothing reads the column and the billing claim no longer writes it.
--
-- Only this column goes, with its values. No row is added or removed; the
-- unique claim on (request_id, api_key_id), the fingerprint and created_at are
-- untouched, and usage_billing_dedup_archive never had the column. Dropping a
-- column rewrites no row but holds the table's exclusive lock until the
-- migration commits; lock_timeout bounds the wait for that lock.
--
-- There is no down path. Earlier images name the column in the insert of every
-- billing claim, so on a database without it their usage billing fails; going
-- back to one of them needs the column added again first
-- (ALTER TABLE usage_billing_dedup ADD COLUMN account_id BIGINT).
--
-- The statement is a no-op once the column is gone, so the migration can run
-- again. No explicit BEGIN/COMMIT: the runner owns this file's transaction.
SET LOCAL lock_timeout = '10s';
SET LOCAL statement_timeout = '120s';

ALTER TABLE usage_billing_dedup
    DROP COLUMN IF EXISTS account_id;
