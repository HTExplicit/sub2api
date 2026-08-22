-- Migration 234 replayed ledger-backed Cindy identities before inspecting the
-- current group topology. Restore every current Cindy row first, then promote
-- only the legacy rows that still form closed, pure Laxa groups.
BEGIN;

CREATE TEMP TABLE project_cindy_platform_v1_preserved_accounts
ON COMMIT DROP
AS
SELECT id, platform, wire_platform, provider_profile
FROM accounts
WHERE platform = 'cindy'
  AND deleted_at IS NOT NULL;

CREATE TEMP TABLE project_cindy_platform_v1_preserved_groups
ON COMMIT DROP
AS
SELECT g.id, g.platform, g.wire_platform, g.provider_profile
FROM groups g
WHERE g.platform = 'cindy'
  AND (g.deleted_at IS NOT NULL OR NOT EXISTS (
      SELECT 1
      FROM account_groups ag
      JOIN accounts a ON a.id = ag.account_id
      WHERE ag.group_id = g.id
        AND a.deleted_at IS NULL
  ));

SELECT * FROM project_cindy_platform_v1_to_legacy();

CREATE OR REPLACE FUNCTION project_cindy_platform_v1_from_legacy()
RETURNS TABLE(promoted_accounts BIGINT, promoted_groups BIGINT)
LANGUAGE plpgsql
AS $$
BEGIN
    RETURN QUERY
    SELECT d.promoted_accounts, d.promoted_groups
    FROM project_cindy_platform_v1_discover_legacy() d;
END;
$$;

SELECT * FROM project_cindy_platform_v1_from_legacy();

UPDATE accounts a
SET platform = p.platform,
    wire_platform = p.wire_platform,
    provider_profile = p.provider_profile
FROM project_cindy_platform_v1_preserved_accounts p
WHERE a.id = p.id
  AND (a.platform, a.wire_platform, a.provider_profile) IS DISTINCT FROM
      (p.platform, p.wire_platform, p.provider_profile);

UPDATE groups g
SET platform = p.platform,
    wire_platform = p.wire_platform,
    provider_profile = p.provider_profile
FROM project_cindy_platform_v1_preserved_groups p
WHERE g.id = p.id
  AND (g.platform, g.wire_platform, g.provider_profile) IS DISTINCT FROM
      (p.platform, p.wire_platform, p.provider_profile);

COMMIT;
