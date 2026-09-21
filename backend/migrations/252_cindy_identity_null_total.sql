-- Forward-only NULL-total Cindy identity hardening. Historical migrations and
-- their checksums remain immutable. This file defines projection behavior but
-- never invokes a projection or rewrites business rows.
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- Serialize the recheck with ordinary writers until the runner commits this
-- migration. No explicit BEGIN/COMMIT: the runner owns this file's transaction.
LOCK TABLE accounts IN ACCESS EXCLUSIVE MODE;
LOCK TABLE groups, account_groups IN SHARE ROW EXCLUSIVE MODE;

DO $$
DECLARE
    invalid_count BIGINT;
BEGIN
    SELECT COUNT(*) INTO invalid_count
    FROM accounts
    WHERE (
        platform <> 'cindy' OR (
            wire_platform = 'openai'
            AND provider_profile = 'cindy_laxa_v1'
            AND type = 'apikey'
            AND jsonb_typeof(credentials->'base_url') = 'string'
            AND LOWER(BTRIM(credentials->>'base_url')) IN (
                'https://api.laxarouter.ai', 'https://api.laxarouter.ai/'
            )
        )
    ) IS NOT TRUE;
    IF invalid_count > 0 THEN
        RAISE EXCEPTION 'CINDY_IDENTITY_NULL_TOTAL_VIOLATIONS: accounts=%', invalid_count;
    END IF;

    SELECT COUNT(*) INTO invalid_count
    FROM groups g
    WHERE g.deleted_at IS NULL AND g.platform = 'cindy'
      AND (
          g.wire_platform IS DISTINCT FROM 'openai'
          OR g.provider_profile IS DISTINCT FROM 'cindy_laxa_v1'
          OR g.fallback_group_id IS NOT NULL
          OR g.fallback_group_id_on_invalid_request IS NOT NULL
          OR EXISTS (
              SELECT 1
              FROM account_groups ag JOIN accounts a ON a.id = ag.account_id
              WHERE ag.group_id = g.id AND a.deleted_at IS NULL
                AND (
                    a.platform = 'cindy'
                    AND a.wire_platform = 'openai'
                    AND a.provider_profile = 'cindy_laxa_v1'
                    AND a.type = 'apikey'
                    AND jsonb_typeof(a.credentials->'base_url') = 'string'
                    AND LOWER(BTRIM(a.credentials->>'base_url')) IN (
                        'https://api.laxarouter.ai', 'https://api.laxarouter.ai/'
                    )
                ) IS NOT TRUE
          )
      );
    IF invalid_count > 0 THEN
        RAISE EXCEPTION 'CINDY_GROUP_NULL_TOTAL_VIOLATIONS: groups=%', invalid_count;
    END IF;
END;
$$;

ALTER TABLE accounts
    DROP CONSTRAINT IF EXISTS accounts_cindy_platform_identity_check;
ALTER TABLE accounts
    ADD CONSTRAINT accounts_cindy_platform_identity_check CHECK ((
        platform <> 'cindy' OR (
            wire_platform = 'openai'
            AND provider_profile = 'cindy_laxa_v1'
            AND type = 'apikey'
            AND jsonb_typeof(credentials->'base_url') = 'string'
            AND LOWER(BTRIM(credentials->>'base_url')) IN (
                'https://api.laxarouter.ai', 'https://api.laxarouter.ai/'
            )
        )
    ) IS TRUE) NOT VALID;
ALTER TABLE accounts
    VALIDATE CONSTRAINT accounts_cindy_platform_identity_check;

CREATE OR REPLACE FUNCTION project_is_strict_cindy_group(target_group_id BIGINT)
RETURNS BOOLEAN
LANGUAGE sql
STABLE
AS $$
    SELECT EXISTS (
        SELECT 1
        FROM groups g
        WHERE g.id = target_group_id
          AND g.deleted_at IS NULL
          AND g.platform = 'cindy'
          AND g.wire_platform = 'openai'
          AND g.provider_profile = 'cindy_laxa_v1'
          AND g.fallback_group_id IS NULL
          AND g.fallback_group_id_on_invalid_request IS NULL
          AND EXISTS (
              SELECT 1
              FROM account_groups strict_membership
              JOIN accounts strict_member ON strict_member.id = strict_membership.account_id
              WHERE strict_membership.group_id = g.id
                AND strict_member.deleted_at IS NULL
                AND strict_member.platform = 'cindy'
                AND strict_member.wire_platform = 'openai'
                AND strict_member.provider_profile = 'cindy_laxa_v1'
                AND strict_member.type = 'apikey'
                AND jsonb_typeof(strict_member.credentials->'base_url') = 'string'
                AND LOWER(BTRIM(strict_member.credentials->>'base_url')) IN (
                    'https://api.laxarouter.ai', 'https://api.laxarouter.ai/'
                )
          )
          AND NOT EXISTS (
              SELECT 1
              FROM account_groups membership
              JOIN accounts member ON member.id = membership.account_id
              WHERE membership.group_id = g.id
                AND member.deleted_at IS NULL
                AND (
                    member.platform IS NOT DISTINCT FROM 'cindy'
                    AND member.wire_platform IS NOT DISTINCT FROM 'openai'
                    AND member.provider_profile IS NOT DISTINCT FROM 'cindy_laxa_v1'
                    AND member.type IS NOT DISTINCT FROM 'apikey'
                    AND jsonb_typeof(member.credentials->'base_url') = 'string'
                    AND LOWER(BTRIM(member.credentials->>'base_url')) IN (
                        'https://api.laxarouter.ai', 'https://api.laxarouter.ai/'
                    )
                ) IS NOT TRUE
          )
    );
$$;

-- Retain the 234 closed-set algorithm and its genuine Cindy fallback guard,
-- while treating every missing identity component as a non-Cindy member.
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
                AND (a.platform IS DISTINCT FROM 'openai'
                  OR a.type IS DISTINCT FROM 'apikey'
                  OR jsonb_typeof(a.credentials->'base_url') IS DISTINCT FROM 'string'
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
            AND (g.platform IS DISTINCT FROM 'openai'
              OR g.fallback_group_id IS NOT NULL
              OR EXISTS (
                  SELECT 1
                  FROM account_groups other_ag
                  JOIN accounts other_a ON other_a.id = other_ag.account_id
                  WHERE other_ag.group_id = g.id
                    AND other_a.deleted_at IS NULL
                    AND (other_a.platform IS DISTINCT FROM 'openai'
                      OR other_a.type IS DISTINCT FROM 'apikey'
                      OR jsonb_typeof(other_a.credentials->'base_url') IS DISTINCT FROM 'string'
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
