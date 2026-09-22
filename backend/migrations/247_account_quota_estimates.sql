CREATE TABLE IF NOT EXISTS account_quota_estimates (
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    identity_hash VARCHAR(64) NOT NULL,
    period_key VARCHAR(96) NOT NULL,
    resets_at TIMESTAMPTZ NOT NULL,
    baseline_at TIMESTAMPTZ NOT NULL,
    baseline_utilization DOUBLE PRECISION NOT NULL,
    baseline_cost NUMERIC(30,10) NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    utilization DOUBLE PRECISION NOT NULL,
    account_cost NUMERIC(30,10) NOT NULL,
    PRIMARY KEY(account_id,identity_hash,period_key)
);
CREATE INDEX IF NOT EXISTS account_quota_estimates_expiry ON account_quota_estimates(resets_at);
