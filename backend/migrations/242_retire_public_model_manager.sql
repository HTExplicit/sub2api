-- The operator stages a credential-free, reviewed migration plan. Apply its
-- ordinary mappings and remove the retired schema in this same transaction,
-- before the new application accepts traffic. Historical billing is untouched.
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

DO $$
DECLARE
    plan JSONB;
    op JSONB;
    item JSONB;
    actual JSONB;
    original_credentials JSONB;
    current_digest TEXT;
    wanted_ids BIGINT[];
    actual_ids BIGINT[];
    target_id BIGINT;
    removal_keys TEXT[];
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_schema='public' AND table_name='groups' AND column_name='managed_model_routes') THEN
        RETURN;
    END IF;
    SELECT value::jsonb INTO plan FROM settings WHERE key = 'public_model_retirement_plan';
    SELECT COALESCE(array_agg(id ORDER BY id), ARRAY[]::BIGINT[]) INTO actual_ids
    FROM groups WHERE deleted_at IS NULL AND managed_model_routes->>'enabled' = 'true';

    IF plan IS NULL THEN
        IF cardinality(actual_ids) > 0 OR EXISTS (
            SELECT 1 FROM accounts a,
            LATERAL jsonb_object_keys(CASE WHEN jsonb_typeof(a.credentials->'model_mapping') = 'object'
                THEN a.credentials->'model_mapping' ELSE '{}'::jsonb END) AS key
            WHERE a.deleted_at IS NULL AND key LIKE 's2pub-%'
        ) THEN
            RAISE EXCEPTION 'public_model_retirement_plan_required';
        END IF;
    ELSE
        IF COALESCE(plan->>'schema','') <> '1' OR COALESCE(plan->>'plan_digest','') !~ '^[0-9a-f]{64}$'
           OR jsonb_array_length(plan->'groups') > 100
           OR jsonb_array_length(plan->'accounts') > 1000 THEN
            RAISE EXCEPTION 'invalid_public_model_retirement_plan';
        END IF;
        SELECT array_agg((g->>'id')::bigint ORDER BY (g->>'id')::bigint) INTO wanted_ids
        FROM jsonb_array_elements(plan->'groups') g;
        IF wanted_ids IS DISTINCT FROM actual_ids THEN
            RAISE EXCEPTION 'public_retirement_group_scope_changed';
        END IF;
        LOCK TABLE admin_capability_runs IN SHARE ROW EXCLUSIVE MODE;
        IF EXISTS (SELECT 1 FROM admin_capability_runs WHERE status IN ('pending','running','paused')) THEN
            RAISE EXCEPTION 'public_retirement_jobs_not_finished';
        END IF;

        -- Lock every object in stable order before checking or changing any of
        -- them. Digests exclude timestamps updated by unrelated bookkeeping.
        PERFORM 1 FROM groups WHERE id=ANY(wanted_ids) ORDER BY id FOR UPDATE;
        PERFORM 1 FROM channels WHERE id IN (
            SELECT (c->>'id')::bigint FROM jsonb_array_elements(plan->'channels') c
        ) ORDER BY id FOR UPDATE;
        PERFORM 1 FROM accounts WHERE id IN (
            SELECT (a->>'id')::bigint FROM jsonb_array_elements(plan->'accounts') a
        ) ORDER BY id FOR UPDATE;

        FOR op IN SELECT value FROM jsonb_array_elements(plan->'groups') LOOP
            target_id := (op->>'id')::bigint;
            SELECT encode(sha256(convert_to((to_jsonb(g)-'updated_at'-'created_at')::text,'UTF8')),'hex')
            INTO current_digest FROM groups g WHERE id=target_id AND deleted_at IS NULL;
            IF current_digest IS DISTINCT FROM op->>'digest' THEN
                RAISE EXCEPTION 'public_retirement_group_changed:%', target_id;
            END IF;
            SELECT COALESCE(jsonb_agg(ag.account_id ORDER BY ag.account_id),'[]') INTO actual
            FROM account_groups ag JOIN accounts a ON a.id=ag.account_id
            WHERE ag.group_id=target_id AND a.deleted_at IS NULL;
            IF actual IS DISTINCT FROM op->'account_ids' THEN
                RAISE EXCEPTION 'public_retirement_group_bindings_changed:%', target_id;
            END IF;
            IF (SELECT count(*) FROM api_keys WHERE group_id=target_id AND deleted_at IS NULL)
               <> (op->>'key_count')::bigint THEN
                RAISE EXCEPTION 'public_retirement_group_keys_changed:%', target_id;
            END IF;
        END LOOP;

        FOR op IN SELECT value FROM jsonb_array_elements(plan->'channels') LOOP
            target_id := (op->>'id')::bigint;
            SELECT encode(sha256(convert_to((to_jsonb(c)-'updated_at'-'created_at')::text,'UTF8')),'hex')
            INTO current_digest FROM channels c WHERE id=target_id;
            IF current_digest IS DISTINCT FROM op->>'digest' THEN
                RAISE EXCEPTION 'public_retirement_channel_changed:%', target_id;
            END IF;
            SELECT COALESCE(jsonb_agg(group_id ORDER BY group_id),'[]') INTO actual
            FROM channel_groups WHERE channel_id=target_id;
            IF actual IS DISTINCT FROM op->'group_ids' THEN
                RAISE EXCEPTION 'public_retirement_channel_bindings_changed:%', target_id;
            END IF;
        END LOOP;

        FOR op IN SELECT value FROM jsonb_array_elements(plan->'accounts') LOOP
            target_id := (op->>'id')::bigint;
            SELECT a.credentials, encode(sha256(convert_to(a.credentials::text,'UTF8')),'hex')
            INTO original_credentials,current_digest FROM accounts a
            WHERE a.id=target_id AND a.deleted_at IS NULL AND a.platform <> 'cindy'
              AND a.status=op->>'status' AND a.schedulable=(op->>'schedulable')::boolean;
            IF current_digest IS DISTINCT FROM op->>'credentials_digest' THEN
                RAISE EXCEPTION 'public_retirement_account_changed:%', target_id;
            END IF;
            SELECT COALESCE(jsonb_agg(group_id ORDER BY group_id),'[]') INTO actual
            FROM account_groups WHERE account_id=target_id;
            IF actual IS DISTINCT FROM op->'group_ids' THEN
                RAISE EXCEPTION 'public_retirement_account_bindings_changed:%', target_id;
            END IF;
            SELECT COALESCE(array_agg(value),ARRAY[]::text[]) INTO removal_keys
            FROM jsonb_array_elements_text(op->'remove_mapping');
            IF EXISTS (SELECT 1 FROM unnest(removal_keys) key
                       WHERE key !~ '^s2pub-g[1-9][0-9]*-(m[0-9a-f]{16}|b[0-9a-f]{24})$') THEN
                RAISE EXCEPTION 'public_retirement_invalid_selector';
            END IF;
            -- Never replace an existing manual key with a new value.
            IF EXISTS (SELECT 1 FROM jsonb_each(op->'set_mapping') AS entry
                       WHERE original_credentials->'model_mapping' ? entry.key) THEN
                RAISE EXCEPTION 'public_retirement_manual_mapping_conflict:%', target_id;
            END IF;
            UPDATE accounts SET credentials=jsonb_set(credentials,'{model_mapping}',
                (COALESCE(credentials->'model_mapping','{}'::jsonb)-removal_keys) || (op->'set_mapping'),true),
                updated_at=NOW() WHERE id=target_id;
            INSERT INTO scheduler_outbox(event_type,account_id) VALUES('account_changed',target_id);
        END LOOP;

        FOR op IN SELECT value FROM jsonb_array_elements(plan->'composite') LOOP
            target_id := (op->>'group_id')::bigint;
            SELECT COALESCE(jsonb_agg(to_jsonb(r)-'created_at'-'updated_at' ORDER BY r.id),'[]') INTO actual
            FROM composite_model_routes r WHERE r.group_id=target_id AND r.deleted_at IS NULL;
            IF actual IS DISTINCT FROM op->'before' THEN
                RAISE EXCEPTION 'public_retirement_composite_routes_changed:%', target_id;
            END IF;
            DELETE FROM composite_model_routes WHERE group_id=target_id
              AND id IN (SELECT value::bigint FROM jsonb_array_elements_text(op->'delete'));
            FOR item IN SELECT value FROM jsonb_array_elements(op->'add') LOOP
                INSERT INTO composite_model_routes(group_id,public_model,match_type,target_platform,upstream_model,endpoint,priority,enabled,notes)
                VALUES(target_id,item->>'public_model',item->>'match_type',item->>'target_platform',item->>'upstream_model',
                       item->>'endpoint',(item->>'priority')::int,(item->>'enabled')::boolean,'');
            END LOOP;
        END LOOP;

        FOR op IN SELECT value FROM jsonb_array_elements(plan->'channels') LOOP
            UPDATE channels SET model_mapping=op->'model_mapping',updated_at=NOW()
            WHERE id=(op->>'id')::bigint;
        END LOOP;
        FOR op IN SELECT value FROM jsonb_array_elements(plan->'groups') LOOP
            target_id := (op->>'id')::bigint;
            UPDATE groups SET managed_model_routes='{}'::jsonb,
                messages_dispatch_model_config=op->'messages_dispatch_model_config',
                default_mapped_model=op->>'default_mapped_model',updated_at=NOW()
            WHERE id=target_id;
            PERFORM enqueue_channel_group_cache_invalidations(target_id);
        END LOOP;
    END IF;
END;
$$;

DROP TRIGGER IF EXISTS trg_groups_managed_model_routes_invalidation ON groups;
DROP FUNCTION IF EXISTS enqueue_managed_model_routes_invalidations();
ALTER TABLE groups DROP COLUMN IF EXISTS managed_model_routes;
DROP TABLE IF EXISTS admin_capability_items;
DROP TABLE IF EXISTS admin_capability_runs;
DROP TABLE IF EXISTS admin_capability_changesets;
DELETE FROM settings WHERE key IN ('internal_rate_conversion_enabled','public_model_retirement_plan');
