-- Cindy becomes a first-class provider identity. This migration is the only
-- legacy OpenAI+Laxa reader; runtime code must use platform=cindy afterwards.
BEGIN;

ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS wire_platform VARCHAR(50) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS provider_profile VARCHAR(100) NOT NULL DEFAULT '';

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS wire_platform VARCHAR(50) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS provider_profile VARCHAR(100) NOT NULL DEFAULT '';

-- Ordinary legacy rows retain their own protocol family. The exact Cindy
-- projection below overwrites only the strict OpenAI API-key identity.
UPDATE accounts
SET wire_platform = LOWER(BTRIM(platform)), provider_profile = ''
WHERE wire_platform = '';

UPDATE groups
SET wire_platform = LOWER(BTRIM(platform)), provider_profile = ''
WHERE wire_platform = '';

CREATE TABLE IF NOT EXISTS cindy_platform_v1_projection (
    entity_type VARCHAR(16) NOT NULL CHECK (entity_type IN ('account', 'group')),
    entity_id BIGINT NOT NULL,
    original_platform VARCHAR(50) NOT NULL,
    original_wire_platform VARCHAR(50) NOT NULL,
    original_provider_profile VARCHAR(100) NOT NULL,
    projected_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (entity_type, entity_id)
);

CREATE INDEX IF NOT EXISTS accounts_provider_identity_idx
    ON accounts (platform, wire_platform, provider_profile)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS groups_provider_identity_idx
    ON groups (platform, wire_platform, provider_profile)
    WHERE deleted_at IS NULL;

CREATE OR REPLACE FUNCTION project_cindy_platform_v1_from_legacy()
RETURNS TABLE(promoted_accounts BIGINT, promoted_groups BIGINT)
LANGUAGE plpgsql
AS $$
DECLARE
    changed_accounts BIGINT := 0;
    changed_groups BIGINT := 0;
    removed_accounts BIGINT := 0;
    removed_groups BIGINT := 0;
BEGIN
    -- A fallback changes the semantic group boundary. Do not silently project
    -- a legacy group whose fallback could cross the new provider boundary.
    IF EXISTS (
        SELECT 1
        FROM groups g
        WHERE g.deleted_at IS NULL
          AND g.platform = 'openai'
          AND g.fallback_group_id IS NOT NULL
          AND EXISTS (SELECT 1 FROM account_groups ag WHERE ag.group_id = g.id)
          AND NOT EXISTS (
              SELECT 1
              FROM account_groups ag
              JOIN accounts a ON a.id = ag.account_id
              WHERE ag.group_id = g.id
                AND (a.deleted_at IS NOT NULL
                  OR a.platform <> 'openai'
                  OR a.type <> 'apikey'
                  OR jsonb_typeof(a.credentials->'base_url') <> 'string'
                  OR LOWER(BTRIM(a.credentials->>'base_url')) NOT IN (
                      'https://api.laxarouter.ai', 'https://api.laxarouter.ai/'
                  ))
          )
    ) THEN
        RAISE EXCEPTION 'Cindy projection candidate has fallback_group_id';
    END IF;

    CREATE TEMP TABLE IF NOT EXISTS cindy_platform_v1_candidate_accounts (
        id BIGINT PRIMARY KEY
    ) ON COMMIT DROP;
    CREATE TEMP TABLE IF NOT EXISTS cindy_platform_v1_candidate_groups (
        id BIGINT PRIMARY KEY
    ) ON COMMIT DROP;
    TRUNCATE cindy_platform_v1_candidate_accounts, cindy_platform_v1_candidate_groups;

    -- Start with exact legacy candidates that do not participate in an
    -- obviously incompatible group. URL matching is strict root-only.
    INSERT INTO cindy_platform_v1_candidate_accounts (id)
    SELECT a.id
    FROM accounts a
    WHERE a.deleted_at IS NULL
      AND a.platform = 'openai'
      AND a.type = 'apikey'
      AND jsonb_typeof(a.credentials->'base_url') = 'string'
      AND LOWER(BTRIM(a.credentials->>'base_url')) IN (
          'https://api.laxarouter.ai', 'https://api.laxarouter.ai/'
      )
      AND NOT EXISTS (
          SELECT 1
          FROM account_groups ag
          JOIN groups g ON g.id = ag.group_id
          WHERE ag.account_id = a.id
            AND g.deleted_at IS NULL
            AND (g.platform <> 'openai'
              OR g.fallback_group_id IS NOT NULL
              OR EXISTS (
                  SELECT 1
                  FROM account_groups other_ag
                  JOIN accounts other_a ON other_a.id = other_ag.account_id
                  WHERE other_ag.group_id = g.id
                    AND (other_a.deleted_at IS NOT NULL
                      OR other_a.platform <> 'openai'
                      OR other_a.type <> 'apikey'
                      OR jsonb_typeof(other_a.credentials->'base_url') <> 'string'
                      OR LOWER(BTRIM(other_a.credentials->>'base_url')) NOT IN (
                          'https://api.laxarouter.ai', 'https://api.laxarouter.ai/'
                      ))
              ))
      );

    -- Reduce the candidates to a closed membership set. This prevents a
    -- partial projection from creating a mixed provider pool without relying
    -- without database write interception.
    LOOP
        INSERT INTO cindy_platform_v1_candidate_groups (id)
        SELECT g.id
        FROM groups g
        WHERE g.deleted_at IS NULL
          AND g.platform = 'openai'
          AND g.fallback_group_id IS NULL
          AND EXISTS (SELECT 1 FROM account_groups ag WHERE ag.group_id = g.id)
          AND NOT EXISTS (
              SELECT 1
              FROM account_groups ag
              LEFT JOIN cindy_platform_v1_candidate_accounts ca ON ca.id = ag.account_id
              WHERE ag.group_id = g.id AND ca.id IS NULL
          )
        ON CONFLICT DO NOTHING;

        DELETE FROM cindy_platform_v1_candidate_accounts ca
        WHERE EXISTS (
            SELECT 1
            FROM account_groups ag
            JOIN groups g ON g.id = ag.group_id
            LEFT JOIN cindy_platform_v1_candidate_groups cg ON cg.id = ag.group_id
            WHERE ag.account_id = ca.id
              AND g.deleted_at IS NULL
              AND cg.id IS NULL
        );
        GET DIAGNOSTICS removed_accounts = ROW_COUNT;

        DELETE FROM cindy_platform_v1_candidate_groups cg
        WHERE EXISTS (
            SELECT 1
            FROM account_groups ag
            LEFT JOIN cindy_platform_v1_candidate_accounts ca ON ca.id = ag.account_id
            WHERE ag.group_id = cg.id AND ca.id IS NULL
        );
        GET DIAGNOSTICS removed_groups = ROW_COUNT;

        EXIT WHEN removed_accounts = 0 AND removed_groups = 0;
    END LOOP;

    INSERT INTO cindy_platform_v1_projection (
        entity_type, entity_id, original_platform, original_wire_platform, original_provider_profile
    )
    SELECT 'group', g.id, g.platform, g.wire_platform, g.provider_profile
    FROM groups g
    JOIN cindy_platform_v1_candidate_groups cg ON cg.id = g.id
    ON CONFLICT (entity_type, entity_id) DO NOTHING;

    INSERT INTO cindy_platform_v1_projection (
        entity_type, entity_id, original_platform, original_wire_platform, original_provider_profile
    )
    SELECT 'account', a.id, a.platform, a.wire_platform, a.provider_profile
    FROM accounts a
    JOIN cindy_platform_v1_candidate_accounts ca ON ca.id = a.id
    ON CONFLICT (entity_type, entity_id) DO NOTHING;

    UPDATE groups g
    SET platform = 'cindy', wire_platform = 'openai', provider_profile = 'cindy_laxa_v1'
    FROM cindy_platform_v1_candidate_groups cg
    WHERE g.id = cg.id
      AND (g.platform, g.wire_platform, g.provider_profile) IS DISTINCT FROM ('cindy', 'openai', 'cindy_laxa_v1');
    GET DIAGNOSTICS changed_groups = ROW_COUNT;

    UPDATE accounts a
    SET platform = 'cindy', wire_platform = 'openai', provider_profile = 'cindy_laxa_v1'
    FROM cindy_platform_v1_candidate_accounts ca
    WHERE a.id = ca.id
      AND (a.platform, a.wire_platform, a.provider_profile) IS DISTINCT FROM ('cindy', 'openai', 'cindy_laxa_v1');
    GET DIAGNOSTICS changed_accounts = ROW_COUNT;

    RETURN QUERY SELECT changed_accounts, changed_groups;
END;
$$;

-- Rollback is explicit and data-preserving. New first-class Cindy rows created
-- after the initial projection receive the canonical legacy OpenAI projection.
CREATE OR REPLACE FUNCTION project_cindy_platform_v1_to_legacy()
RETURNS TABLE(restored_accounts BIGINT, restored_groups BIGINT)
LANGUAGE plpgsql
AS $$
DECLARE
    changed_accounts BIGINT := 0;
    changed_groups BIGINT := 0;
BEGIN
    INSERT INTO cindy_platform_v1_projection (
        entity_type, entity_id, original_platform, original_wire_platform, original_provider_profile
    )
    SELECT 'account', a.id, 'openai', 'openai', ''
    FROM accounts a
    WHERE a.platform = 'cindy'
    ON CONFLICT (entity_type, entity_id) DO NOTHING;

    INSERT INTO cindy_platform_v1_projection (
        entity_type, entity_id, original_platform, original_wire_platform, original_provider_profile
    )
    SELECT 'group', g.id, 'openai', 'openai', ''
    FROM groups g
    WHERE g.platform = 'cindy'
    ON CONFLICT (entity_type, entity_id) DO NOTHING;

    UPDATE accounts a
    SET platform = p.original_platform,
        wire_platform = p.original_wire_platform,
        provider_profile = p.original_provider_profile
    FROM cindy_platform_v1_projection p
    WHERE p.entity_type = 'account'
      AND p.entity_id = a.id
      AND a.platform = 'cindy';
    GET DIAGNOSTICS changed_accounts = ROW_COUNT;

    UPDATE groups g
    SET platform = p.original_platform,
        wire_platform = p.original_wire_platform,
        provider_profile = p.original_provider_profile
    FROM cindy_platform_v1_projection p
    WHERE p.entity_type = 'group'
      AND p.entity_id = g.id
      AND g.platform = 'cindy';
    GET DIAGNOSTICS changed_groups = ROW_COUNT;

    RETURN QUERY SELECT changed_accounts, changed_groups;
END;
$$;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'accounts_cindy_platform_identity_check') THEN
        ALTER TABLE accounts ADD CONSTRAINT accounts_cindy_platform_identity_check CHECK (
            platform <> 'cindy' OR (
                wire_platform = 'openai'
                AND provider_profile = 'cindy_laxa_v1'
                AND type = 'apikey'
                AND jsonb_typeof(credentials->'base_url') = 'string'
                AND LOWER(BTRIM(credentials->>'base_url')) IN (
                    'https://api.laxarouter.ai', 'https://api.laxarouter.ai/'
                )
            )
        ) NOT VALID;
        ALTER TABLE accounts VALIDATE CONSTRAINT accounts_cindy_platform_identity_check;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'groups_cindy_platform_identity_check') THEN
        ALTER TABLE groups ADD CONSTRAINT groups_cindy_platform_identity_check CHECK (
            platform <> 'cindy' OR (
                wire_platform = 'openai' AND provider_profile = 'cindy_laxa_v1'
            )
        ) NOT VALID;
        ALTER TABLE groups VALIDATE CONSTRAINT groups_cindy_platform_identity_check;
    END IF;
END;
$$;

SELECT * FROM project_cindy_platform_v1_from_legacy();

COMMIT;
