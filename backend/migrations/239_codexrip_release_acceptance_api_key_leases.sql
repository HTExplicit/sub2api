-- Internal, short-lived client keys used only by the root-only release
-- acceptance runner. Existing and newly created user keys retain purpose=user.
ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS purpose VARCHAR(32) NOT NULL DEFAULT 'user';

ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS lease_id VARCHAR(64);

DO $$
BEGIN
    ALTER TABLE api_keys
        ADD CONSTRAINT api_keys_purpose_valid
        CHECK (purpose IN ('user', 'release_acceptance'));
EXCEPTION
    WHEN duplicate_object THEN NULL;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS api_keys_lease_id_unique
    ON api_keys (lease_id)
    WHERE lease_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS api_keys_acceptance_expiry
    ON api_keys (expires_at)
    WHERE purpose = 'release_acceptance' AND deleted_at IS NULL;
