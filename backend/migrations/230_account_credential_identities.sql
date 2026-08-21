-- Credential identities persist a domain-separated SHA-256 fingerprint only.
-- Raw credentials remain exclusively in accounts.credentials and are consumed
-- by explicit admin work in a later release.
CREATE TABLE IF NOT EXISTS account_credential_identities (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    provider_profile VARCHAR(64) NOT NULL,
    auth_type VARCHAR(32) NOT NULL,
    normalized_base_url TEXT NOT NULL,
    fingerprint CHAR(64) NOT NULL,
    generation BIGINT NOT NULL DEFAULT 1 CHECK (generation > 0),
    active BOOLEAN NOT NULL DEFAULT TRUE,
    retired_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT account_credential_identities_fingerprint_format
        CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
    CONSTRAINT account_credential_identities_retired_state
        CHECK ((active AND retired_at IS NULL) OR (NOT active AND retired_at IS NOT NULL))
);

-- Historical duplicates remain visible so the ordinary review/confirmation
-- merge job can resolve them without blocking service availability. New binds
-- serialize on the fingerprint and reject another account in the repository.
CREATE INDEX IF NOT EXISTS account_credential_identities_fingerprint_idx
    ON account_credential_identities (fingerprint);

CREATE UNIQUE INDEX IF NOT EXISTS account_credential_identities_active_account_uq
    ON account_credential_identities (account_id)
    WHERE active;

CREATE INDEX IF NOT EXISTS account_credential_identities_account_generation_idx
    ON account_credential_identities (account_id, generation DESC);

WITH cindy_identity_material AS (
    SELECT
        a.id AS account_id,
        encode(
            sha256(
                convert_to('sub2api/account-credential-identity/v1', 'UTF8') ||
                int4send(octet_length(convert_to('cindy_laxa_v1', 'UTF8'))) ||
                convert_to('cindy_laxa_v1', 'UTF8') ||
                int4send(octet_length(convert_to('apikey', 'UTF8'))) ||
                convert_to('apikey', 'UTF8') ||
                int4send(octet_length(convert_to('https://api.laxarouter.ai', 'UTF8'))) ||
                convert_to('https://api.laxarouter.ai', 'UTF8') ||
                int4send(octet_length(convert_to(a.credentials->>'api_key', 'UTF8'))) ||
                convert_to(a.credentials->>'api_key', 'UTF8')
            ),
            'hex'
        ) AS fingerprint
    FROM accounts a
    WHERE a.deleted_at IS NULL
      AND a.platform = 'cindy'
      AND a.wire_platform = 'openai'
      AND a.provider_profile = 'cindy_laxa_v1'
      AND a.type = 'apikey'
      AND jsonb_typeof(a.credentials->'base_url') = 'string'
      AND LOWER(BTRIM(a.credentials->>'base_url')) IN (
          'https://api.laxarouter.ai', 'https://api.laxarouter.ai/'
      )
      AND jsonb_typeof(a.credentials->'api_key') = 'string'
      AND a.credentials->>'api_key' <> ''
)
INSERT INTO account_credential_identities (
    account_id, provider_profile, auth_type, normalized_base_url,
    fingerprint, generation, active
)
SELECT account_id, 'cindy_laxa_v1', 'apikey', 'https://api.laxarouter.ai',
       fingerprint, 1, TRUE
FROM cindy_identity_material
ON CONFLICT (account_id) WHERE active DO NOTHING;
