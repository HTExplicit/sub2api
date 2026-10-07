-- Retire the release-acceptance key leases introduced by 239.
--
-- Only soft-deleted release_acceptance keys with a nonempty lease are removed.
-- Their usage_logs and billing_usage_entries cascade with the key; the hot
-- and archived billing dedup claims have no foreign key and are removed by
-- the same selected key IDs. Ordinary keys and their data stay. Diagnostic
-- records in ops_error_logs, ops_system_logs and
-- ops_ingress_reject_aggregates keep their original api_key_id and content.
--
-- Refuse an active acceptance key, an acceptance key without a lease, an
-- unknown purpose or any ordinary key carrying a lease. Nothing is removed
-- when this validation fails. Dropping both columns also removes the old
-- application's ability to authenticate keys: deploy only after the host
-- deployment tool no longer reads them. There is no old-image rollback;
-- repair a failure forward with this schema and the matching application.
--
-- A repeated run finds neither column and changes nothing. The migration
-- runner owns the transaction; no explicit BEGIN/COMMIT belongs here.
SET LOCAL lock_timeout = '10s';
SET LOCAL statement_timeout = '120s';

LOCK TABLE api_keys IN ACCESS EXCLUSIVE MODE;

DO $$
DECLARE
    lease_columns integer;
    acceptance_key_ids bigint[];
BEGIN
    SELECT count(*) INTO lease_columns
    FROM pg_attribute
    WHERE attrelid = 'api_keys'::regclass
      AND attname IN ('purpose', 'lease_id')
      AND NOT attisdropped;

    IF lease_columns = 0 THEN
        RETURN;
    ELSIF lease_columns <> 2 THEN
        RAISE EXCEPTION 'migration 274: release-acceptance lease schema is incomplete; repair it before upgrading';
    END IF;

    IF EXISTS (
        SELECT 1 FROM api_keys
        WHERE purpose IS DISTINCT FROM 'user'
          AND purpose IS DISTINCT FROM 'release_acceptance'
    ) OR EXISTS (
        SELECT 1 FROM api_keys
        WHERE purpose = 'user' AND lease_id IS NOT NULL
    ) OR EXISTS (
        SELECT 1 FROM api_keys
        WHERE purpose = 'release_acceptance'
          AND (deleted_at IS NULL OR lease_id IS NULL OR btrim(lease_id) = '')
    ) THEN
        RAISE EXCEPTION 'migration 274: release-acceptance keys are not retired leased keys, or ordinary keys carry leases; resolve the key state before upgrading';
    END IF;

    SELECT array_agg(id) INTO acceptance_key_ids
    FROM api_keys
    WHERE purpose = 'release_acceptance';

    DELETE FROM usage_billing_dedup
    WHERE api_key_id = ANY (acceptance_key_ids);
    DELETE FROM usage_billing_dedup_archive
    WHERE api_key_id = ANY (acceptance_key_ids);
    DELETE FROM api_keys
    WHERE id = ANY (acceptance_key_ids);

    DROP INDEX IF EXISTS api_keys_acceptance_expiry;
    DROP INDEX IF EXISTS api_keys_lease_id_unique;
    ALTER TABLE api_keys
        DROP CONSTRAINT IF EXISTS api_keys_purpose_valid,
        DROP COLUMN purpose,
        DROP COLUMN lease_id;
END $$;
