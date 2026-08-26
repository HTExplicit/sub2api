-- 238_cindy_account_stats_reset_at.sql
-- Cindy-only statistics watermark. It changes the lower bound for account
-- today-stat reads; historical usage rows and balances remain untouched.
ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS cindy_account_stats_reset_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_accounts_cindy_stats_reset_at
    ON accounts (cindy_account_stats_reset_at)
    WHERE cindy_account_stats_reset_at IS NOT NULL;

ALTER TABLE usage_billing_dedup
    ADD COLUMN IF NOT EXISTS account_id BIGINT;
