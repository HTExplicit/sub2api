-- Cindy health keeps only generation-bound transient and confirmed budget
-- state. Administrator status/schedulable fields remain independent.
CREATE TABLE IF NOT EXISTS cindy_health_states (
    account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    credential_identity_id BIGINT NOT NULL REFERENCES account_credential_identities(id) ON DELETE CASCADE,
    credential_generation BIGINT NOT NULL CHECK (credential_generation > 0),
    episode_id VARCHAR(64) NOT NULL CHECK (episode_id <> ''),
    status VARCHAR(32) NOT NULL CHECK (status IN ('quarantined', 'confirmed_exhausted')),
    evidence VARCHAR(32) NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    quarantine_until TIMESTAMPTZ,
    confirmed_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT cindy_health_states_status_shape CHECK (
        (status = 'quarantined' AND quarantine_until IS NOT NULL AND confirmed_at IS NULL)
        OR
        (status = 'confirmed_exhausted' AND quarantine_until IS NULL AND confirmed_at IS NOT NULL)
    )
);

CREATE INDEX IF NOT EXISTS cindy_health_states_transient_expiry_idx
    ON cindy_health_states (quarantine_until, account_id)
    WHERE status = 'quarantined';

-- Existing dual-confirmed balance markers remain confirmed after the schema
-- transition and are bound to the active credential generation from 230.
INSERT INTO cindy_health_states (
    account_id, credential_identity_id, credential_generation, episode_id,
    status, evidence, observed_at, confirmed_at
)
SELECT
    a.id, i.id, i.generation, 'legacy-confirmed',
    'confirmed_exhausted', 'legacy_confirmed_budget',
    a.cindy_balance_insufficient_at, a.cindy_balance_insufficient_at
FROM accounts a
JOIN account_credential_identities i ON i.account_id = a.id AND i.active
WHERE a.deleted_at IS NULL
  AND a.platform = 'cindy'
  AND a.wire_platform = 'openai'
  AND a.provider_profile = 'cindy_laxa_v1'
  AND a.cindy_balance_insufficient_at IS NOT NULL
ON CONFLICT (account_id) DO NOTHING;
