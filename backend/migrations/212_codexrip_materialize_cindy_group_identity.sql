-- Strict Cindy identity is materialized in the API-key auth snapshot. Any
-- committed account identity or membership change must therefore enqueue the
-- affected group keys in the existing durable cross-instance invalidation
-- outbox. Runtime-only scheduling fields deliberately remain outside this
-- trigger so ordinary health/usage updates do not churn auth snapshots.

CREATE OR REPLACE FUNCTION enqueue_group_api_key_auth_cache_invalidations(target_group_id BIGINT)
RETURNS VOID
LANGUAGE plpgsql
AS $$
BEGIN
    IF target_group_id IS NULL OR target_group_id <= 0 THEN
        RETURN;
    END IF;

    INSERT INTO auth_cache_invalidation_outbox (cache_key)
    SELECT encode(sha256(convert_to(k.key, 'UTF8')), 'hex')
    FROM api_keys AS k
    WHERE k.group_id = target_group_id
      AND k.deleted_at IS NULL
      AND k.key <> '';
END;
$$;

CREATE OR REPLACE FUNCTION enqueue_account_identity_auth_cache_invalidations()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    affected_group_id BIGINT;
    target_account_id BIGINT;
BEGIN
    IF TG_OP = 'UPDATE'
       AND OLD.platform IS NOT DISTINCT FROM NEW.platform
       AND OLD.type IS NOT DISTINCT FROM NEW.type
       AND OLD.credentials IS NOT DISTINCT FROM NEW.credentials
       AND OLD.status IS NOT DISTINCT FROM NEW.status
       AND OLD.deleted_at IS NOT DISTINCT FROM NEW.deleted_at THEN
        RETURN NEW;
    END IF;

    target_account_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.id ELSE NEW.id END;
    FOR affected_group_id IN
        SELECT DISTINCT ag.group_id
        FROM account_groups AS ag
        WHERE ag.account_id = target_account_id
    LOOP
        PERFORM enqueue_group_api_key_auth_cache_invalidations(affected_group_id);
    END LOOP;

    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_accounts_cindy_identity_auth_cache_invalidation ON accounts;
CREATE TRIGGER trg_accounts_cindy_identity_auth_cache_invalidation
AFTER UPDATE OF platform, type, credentials, status, deleted_at ON accounts
FOR EACH ROW EXECUTE FUNCTION enqueue_account_identity_auth_cache_invalidations();

DROP TRIGGER IF EXISTS trg_accounts_cindy_identity_delete_auth_cache_invalidation ON accounts;
CREATE TRIGGER trg_accounts_cindy_identity_delete_auth_cache_invalidation
BEFORE DELETE ON accounts
FOR EACH ROW EXECUTE FUNCTION enqueue_account_identity_auth_cache_invalidations();

CREATE OR REPLACE FUNCTION enqueue_account_group_auth_cache_invalidations()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE'
       AND OLD.account_id IS NOT DISTINCT FROM NEW.account_id
       AND OLD.group_id IS NOT DISTINCT FROM NEW.group_id THEN
        RETURN NEW;
    END IF;

    IF TG_OP <> 'INSERT' THEN
        PERFORM enqueue_group_api_key_auth_cache_invalidations(OLD.group_id);
    END IF;
    IF TG_OP <> 'DELETE'
       AND (TG_OP = 'INSERT' OR NEW.group_id IS DISTINCT FROM OLD.group_id) THEN
        PERFORM enqueue_group_api_key_auth_cache_invalidations(NEW.group_id);
    END IF;

    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_account_groups_cindy_identity_auth_cache_invalidation ON account_groups;
CREATE TRIGGER trg_account_groups_cindy_identity_auth_cache_invalidation
AFTER INSERT OR UPDATE OR DELETE ON account_groups
FOR EACH ROW EXECUTE FUNCTION enqueue_account_group_auth_cache_invalidations();
