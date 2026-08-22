-- Make the first-class Cindy projection a true round trip. The original
-- forward function discovered only active legacy rows in non-empty groups,
-- while rollback intentionally covered every recorded Cindy row. Preserve
-- that discovery implementation and put the ledger restore in front of it.
BEGIN;

DO $$
BEGIN
    IF TO_REGPROCEDURE('project_cindy_platform_v1_discover_legacy()') IS NULL THEN
        ALTER FUNCTION project_cindy_platform_v1_from_legacy()
            RENAME TO project_cindy_platform_v1_discover_legacy;
    END IF;
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
