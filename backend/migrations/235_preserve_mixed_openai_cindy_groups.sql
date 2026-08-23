-- Migration 234 replayed every ledger-backed Cindy identity before inspecting
-- current topology. Restore current Cindy rows first, then keep ledger replay
-- only for lifecycle-excluded rows that discovery cannot classify.
BEGIN;

SELECT * FROM project_cindy_platform_v1_to_legacy();

CREATE OR REPLACE FUNCTION project_cindy_platform_v1_from_legacy()
RETURNS TABLE(promoted_accounts BIGINT, promoted_groups BIGINT)
LANGUAGE plpgsql
AS $$
DECLARE
    discovered_accounts BIGINT := 0;
    discovered_groups BIGINT := 0;
    replayed_accounts BIGINT := 0;
    replayed_groups BIGINT := 0;
BEGIN
    SELECT d.promoted_accounts, d.promoted_groups
    INTO discovered_accounts, discovered_groups
    FROM project_cindy_platform_v1_discover_legacy() d;

    UPDATE accounts a
    SET platform = 'cindy', wire_platform = 'openai', provider_profile = 'cindy_laxa_v1'
    FROM cindy_platform_v1_projection p
    WHERE p.entity_type = 'account'
      AND p.entity_id = a.id
      AND a.deleted_at IS NOT NULL
      AND a.type = 'apikey'
      AND jsonb_typeof(a.credentials->'base_url') = 'string'
      AND LOWER(BTRIM(a.credentials->>'base_url')) IN (
          'https://api.laxarouter.ai', 'https://api.laxarouter.ai/'
      )
      AND (a.platform, a.wire_platform, a.provider_profile) IS NOT DISTINCT FROM
          (p.original_platform, p.original_wire_platform, p.original_provider_profile);
    GET DIAGNOSTICS replayed_accounts = ROW_COUNT;

    UPDATE groups g
    SET platform = 'cindy', wire_platform = 'openai', provider_profile = 'cindy_laxa_v1'
    FROM cindy_platform_v1_projection p
    WHERE p.entity_type = 'group'
      AND p.entity_id = g.id
      AND (g.deleted_at IS NOT NULL OR NOT EXISTS (
          SELECT 1
          FROM account_groups ag
          JOIN accounts a ON a.id = ag.account_id
          WHERE ag.group_id = g.id
            AND a.deleted_at IS NULL
      ))
      AND (g.platform, g.wire_platform, g.provider_profile) IS NOT DISTINCT FROM
          (p.original_platform, p.original_wire_platform, p.original_provider_profile);
    GET DIAGNOSTICS replayed_groups = ROW_COUNT;

    RETURN QUERY SELECT
        discovered_accounts + replayed_accounts,
        discovered_groups + replayed_groups;
END;
$$;

SELECT * FROM project_cindy_platform_v1_from_legacy();

COMMIT;
