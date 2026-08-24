-- Strict Cindy terminal health is bound to the active credential generation.
ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS cindy_banned_at TIMESTAMPTZ;
ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS cindy_credential_generation BIGINT NOT NULL DEFAULT 0
        CHECK (cindy_credential_generation >= 0);

UPDATE accounts a
SET cindy_credential_generation = i.generation
FROM account_credential_identities i
WHERE i.account_id = a.id AND i.active
  AND a.cindy_credential_generation IS DISTINCT FROM i.generation;

CREATE OR REPLACE FUNCTION project_sync_cindy_credential_generation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.active THEN
        UPDATE accounts
        SET cindy_credential_generation = NEW.generation
        WHERE id = NEW.account_id
          AND cindy_credential_generation IS DISTINCT FROM NEW.generation;
    ELSIF TG_OP = 'UPDATE' AND OLD.active AND NOT NEW.active THEN
        UPDATE accounts
        SET cindy_credential_generation = 0
        WHERE id = OLD.account_id
          AND cindy_credential_generation = OLD.generation;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_project_sync_cindy_credential_generation ON account_credential_identities;
CREATE TRIGGER trg_project_sync_cindy_credential_generation
AFTER INSERT OR UPDATE OF active, generation ON account_credential_identities
FOR EACH ROW EXECUTE FUNCTION project_sync_cindy_credential_generation();

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
