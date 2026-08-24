-- Strict Cindy terminal health is bound to the active credential generation.
ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS cindy_banned_at TIMESTAMPTZ;

ALTER TABLE cindy_health_states
    DROP CONSTRAINT IF EXISTS cindy_health_states_status_check;
ALTER TABLE cindy_health_states
    DROP CONSTRAINT IF EXISTS cindy_health_states_status_shape;
ALTER TABLE cindy_health_states
    ADD CONSTRAINT cindy_health_states_status_check
        CHECK (status IN ('quarantined', 'confirmed_exhausted', 'banned'));
ALTER TABLE cindy_health_states
    ADD CONSTRAINT cindy_health_states_status_shape CHECK (
        (status = 'quarantined' AND quarantine_until IS NOT NULL AND confirmed_at IS NULL)
        OR
        (status IN ('confirmed_exhausted', 'banned') AND quarantine_until IS NULL AND confirmed_at IS NOT NULL)
    );

CREATE INDEX IF NOT EXISTS accounts_cindy_banned_at_idx
    ON accounts (cindy_banned_at, id)
    WHERE cindy_banned_at IS NOT NULL AND deleted_at IS NULL;

COMMENT ON COLUMN cindy_health_states.credential_generation IS
    'CAS generation binding for quarantined, confirmed_exhausted, and banned health states';
