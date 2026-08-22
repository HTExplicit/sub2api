-- Make the first-class Cindy projection a true round trip. The original
-- forward function discovered only active legacy rows in non-empty groups,
-- while rollback intentionally covered every recorded Cindy row. Put the
-- ledger restore in front and align discovery with non-deleted membership.
BEGIN;

DO $$
BEGIN
    IF TO_REGPROCEDURE('project_cindy_platform_v1_discover_legacy()') IS NULL THEN
        ALTER FUNCTION project_cindy_platform_v1_from_legacy()
            RENAME TO project_cindy_platform_v1_discover_legacy;
    END IF;
END;
$$;

-- The runtime classifier and split workflow define group identity from complete
-- non-deleted membership. Replace the 229 discovery body so historical
-- soft-deleted memberships cannot keep an otherwise strict active Cindy group
-- on the legacy identity.
CREATE OR REPLACE FUNCTION project_cindy_platform_v1_discover_legacy()
RETURNS TABLE(promoted_accounts BIGINT, promoted_groups BIGINT)
LANGUAGE plpgsql
AS $$
DECLARE
    changed_accounts BIGINT := 0;
    changed_groups BIGINT := 0;
    removed_accounts BIGINT := 0;
    removed_groups BIGINT := 0;
BEGIN
    IF EXISTS (
        SELECT 1
        FROM groups g
        WHERE g.deleted_at IS NULL
          AND g.platform = 'openai'
          AND g.fallback_group_id IS NOT NULL
          AND EXISTS (
              SELECT 1
              FROM account_groups ag
              JOIN accounts a ON a.id = ag.account_id
              WHERE ag.group_id = g.id
                AND a.deleted_at IS NULL
          )
          AND NOT EXISTS (
              SELECT 1
              FROM account_groups ag
              JOIN accounts a ON a.id = ag.account_id
              WHERE ag.group_id = g.id
                AND a.deleted_at IS NULL
                AND (a.platform <> 'openai'
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
                    AND other_a.deleted_at IS NULL
                    AND (other_a.platform <> 'openai'
                      OR other_a.type <> 'apikey'
                      OR jsonb_typeof(other_a.credentials->'base_url') <> 'string'
                      OR LOWER(BTRIM(other_a.credentials->>'base_url')) NOT IN (
                          'https://api.laxarouter.ai', 'https://api.laxarouter.ai/'
                      ))
              ))
      );

    LOOP
        INSERT INTO cindy_platform_v1_candidate_groups (id)
        SELECT g.id
        FROM groups g
        WHERE g.deleted_at IS NULL
          AND g.platform = 'openai'
          AND g.fallback_group_id IS NULL
          AND EXISTS (
              SELECT 1
              FROM account_groups ag
              JOIN accounts a ON a.id = ag.account_id
              WHERE ag.group_id = g.id
                AND a.deleted_at IS NULL
          )
          AND NOT EXISTS (
              SELECT 1
              FROM account_groups ag
              JOIN accounts a ON a.id = ag.account_id
              LEFT JOIN cindy_platform_v1_candidate_accounts ca ON ca.id = ag.account_id
              WHERE ag.group_id = g.id
                AND a.deleted_at IS NULL
                AND ca.id IS NULL
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
            JOIN accounts a ON a.id = ag.account_id
            LEFT JOIN cindy_platform_v1_candidate_accounts ca ON ca.id = ag.account_id
            WHERE ag.group_id = cg.id
              AND a.deleted_at IS NULL
              AND ca.id IS NULL
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

CREATE OR REPLACE FUNCTION project_cindy_platform_v1_from_legacy()
RETURNS TABLE(promoted_accounts BIGINT, promoted_groups BIGINT)
LANGUAGE plpgsql
AS $$
DECLARE
    restored_accounts BIGINT := 0;
    restored_groups BIGINT := 0;
    discovered_accounts BIGINT := 0;
    discovered_groups BIGINT := 0;
BEGIN
    UPDATE groups g
    SET platform = 'cindy', wire_platform = 'openai', provider_profile = 'cindy_laxa_v1'
    FROM cindy_platform_v1_projection p
    WHERE p.entity_type = 'group'
      AND p.entity_id = g.id
      AND (g.platform, g.wire_platform, g.provider_profile) IS NOT DISTINCT FROM
          (p.original_platform, p.original_wire_platform, p.original_provider_profile);
    GET DIAGNOSTICS restored_groups = ROW_COUNT;

    UPDATE accounts a
    SET platform = 'cindy', wire_platform = 'openai', provider_profile = 'cindy_laxa_v1'
    FROM cindy_platform_v1_projection p
    WHERE p.entity_type = 'account'
      AND p.entity_id = a.id
      AND (a.platform, a.wire_platform, a.provider_profile) IS NOT DISTINCT FROM
          (p.original_platform, p.original_wire_platform, p.original_provider_profile);
    GET DIAGNOSTICS restored_accounts = ROW_COUNT;

    SELECT d.promoted_accounts, d.promoted_groups
    INTO discovered_accounts, discovered_groups
    FROM project_cindy_platform_v1_discover_legacy() d;

    RETURN QUERY SELECT
        restored_accounts + discovered_accounts,
        restored_groups + discovered_groups;
END;
$$;

SELECT * FROM project_cindy_platform_v1_from_legacy();

COMMIT;
