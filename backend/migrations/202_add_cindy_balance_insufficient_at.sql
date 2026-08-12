-- Persist Cindy-specific balance exhaustion without changing the administrator's schedulable flag.
ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS cindy_balance_insufficient_at TIMESTAMPTZ NULL;
