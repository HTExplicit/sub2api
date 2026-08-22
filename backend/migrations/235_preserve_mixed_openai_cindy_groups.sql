-- Migration 234 replayed ledger-backed Cindy identities before inspecting the
-- current group topology. Restore every current Cindy row first, then promote
-- only the legacy rows that still form closed, pure Laxa groups.
BEGIN;

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

COMMIT;
