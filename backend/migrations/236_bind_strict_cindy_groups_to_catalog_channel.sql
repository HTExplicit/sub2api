-- Bind strict Cindy groups to one migration-owned catalog channel. The row
-- stores topology only; model admission/mapping and all pricing resolve from
-- the release-owned Go catalog.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

LOCK TABLE groups, accounts, account_groups IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE channels, channel_groups, channel_model_pricing,
    channel_account_stats_pricing_rules IN SHARE ROW EXCLUSIVE MODE;

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
                AND NOT (
                    member.platform IS NOT DISTINCT FROM 'cindy'
                    AND member.wire_platform IS NOT DISTINCT FROM 'openai'
                    AND member.provider_profile IS NOT DISTINCT FROM 'cindy_laxa_v1'
                    AND member.type IS NOT DISTINCT FROM 'apikey'
                    AND jsonb_typeof(member.credentials->'base_url') = 'string'
                    AND LOWER(BTRIM(member.credentials->>'base_url')) IN (
                        'https://api.laxarouter.ai', 'https://api.laxarouter.ai/'
                    )
                )
          )
    );
$$;

CREATE OR REPLACE FUNCTION project_managed_cindy_channel_id()
RETURNS BIGINT
LANGUAGE plpgsql
STABLE
AS $$
DECLARE
    managed_id BIGINT;
    managed_count BIGINT;
BEGIN
    SELECT COUNT(*), MIN(c.id)
    INTO managed_count, managed_id
    FROM channels c
    WHERE c.features_config->>'cindy_catalog_managed' = 'cindy_laxa_v1';
    IF managed_count <> 1 THEN
        RAISE EXCEPTION 'managed Cindy channel topology requires exactly one marker, found %', managed_count;
    END IF;
    RETURN managed_id;
END;
$$;

CREATE OR REPLACE FUNCTION enqueue_channel_group_scheduler_invalidation(target_group_id BIGINT)
RETURNS VOID
LANGUAGE plpgsql
AS $$
BEGIN
    IF target_group_id IS NULL OR target_group_id <= 0 THEN
        RETURN;
    END IF;
    INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload, dedup_key)
    VALUES ('group_changed', NULL, target_group_id, NULL,
        'scheduler_outbox:channel_group:' || target_group_id::TEXT)
    ON CONFLICT (dedup_key) WHERE dedup_key IS NOT NULL DO NOTHING;
END;
$$;

CREATE OR REPLACE FUNCTION enqueue_channel_group_cache_invalidations(target_group_id BIGINT)
RETURNS VOID
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM enqueue_channel_group_scheduler_invalidation(target_group_id);
    PERFORM enqueue_group_api_key_auth_cache_invalidations(target_group_id);
END;
$$;

CREATE OR REPLACE FUNCTION project_assert_cindy_group_topology(target_group_id BIGINT)
RETURNS VOID
LANGUAGE plpgsql
AS $$
DECLARE
    canonical BOOLEAN;
    fallback_configured BOOLEAN;
    has_active_members BOOLEAN;
BEGIN
    SELECT
        g.deleted_at IS NULL AND g.platform = 'cindy'
            AND g.wire_platform = 'openai' AND g.provider_profile = 'cindy_laxa_v1',
        g.fallback_group_id IS NOT NULL OR g.fallback_group_id_on_invalid_request IS NOT NULL,
        EXISTS (
            SELECT 1 FROM account_groups ag JOIN accounts a ON a.id = ag.account_id
            WHERE ag.group_id = g.id AND a.deleted_at IS NULL
        )
    INTO canonical, fallback_configured, has_active_members
    FROM groups g WHERE g.id = target_group_id;

    IF NOT FOUND OR NOT canonical THEN
        RETURN;
    END IF;
    IF fallback_configured THEN
        RAISE EXCEPTION 'canonical Cindy group % cannot configure fallback groups', target_group_id;
    END IF;
    IF has_active_members AND NOT project_is_strict_cindy_group(target_group_id) THEN
        RAISE EXCEPTION 'canonical Cindy group % has mixed or cross-profile membership', target_group_id;
    END IF;
END;
$$;

CREATE OR REPLACE FUNCTION project_reconcile_cindy_group_channel(target_group_id BIGINT)
RETURNS VOID
LANGUAGE plpgsql
AS $$
DECLARE
    managed_id BIGINT;
    attached_id BIGINT;
BEGIN
    managed_id := project_managed_cindy_channel_id();
    SELECT cg.channel_id INTO attached_id FROM channel_groups cg WHERE cg.group_id = target_group_id;
    IF project_is_strict_cindy_group(target_group_id) THEN
        IF attached_id IS NOT NULL AND attached_id <> managed_id THEN
            RAISE EXCEPTION 'strict Cindy group % belongs to another channel %', target_group_id, attached_id;
        END IF;
        INSERT INTO channel_groups (channel_id, group_id)
        VALUES (managed_id, target_group_id)
        ON CONFLICT (group_id) DO NOTHING;
    ELSE
        DELETE FROM channel_groups WHERE channel_id = managed_id AND group_id = target_group_id;
    END IF;
END;
$$;

CREATE OR REPLACE FUNCTION enqueue_channel_group_binding_invalidations()
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
        PERFORM enqueue_channel_group_cache_invalidations(OLD.group_id);
    END IF;
    IF TG_OP <> 'DELETE' AND (TG_OP = 'INSERT' OR NEW.group_id IS DISTINCT FROM OLD.group_id) THEN
        PERFORM enqueue_channel_group_cache_invalidations(NEW.group_id);
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_channel_groups_cache_invalidation ON channel_groups;
CREATE TRIGGER trg_channel_groups_cache_invalidation
AFTER INSERT OR UPDATE OR DELETE ON channel_groups
FOR EACH ROW EXECUTE FUNCTION enqueue_channel_group_binding_invalidations();

DO $$
DECLARE
    managed_channel_id BIGINT;
    managed_channel_count BIGINT;
    conflicting_group_ids TEXT;
BEGIN
    SELECT COUNT(*), MIN(c.id) INTO managed_channel_count, managed_channel_id
    FROM channels c WHERE c.features_config->>'cindy_catalog_managed' = 'cindy_laxa_v1';
    IF managed_channel_count > 1 THEN
        RAISE EXCEPTION 'ambiguous managed Cindy channel topology: % candidates', managed_channel_count;
    END IF;
    IF managed_channel_count = 0 THEN
        IF EXISTS (SELECT 1 FROM channels c WHERE LOWER(BTRIM(c.name)) = LOWER('Cindy Catalog')) THEN
            RAISE EXCEPTION 'ambiguous managed Cindy channel topology: reserved name already exists';
        END IF;
        INSERT INTO channels (
            name, description, status, model_mapping, billing_model_source,
            restrict_models, features, features_config, apply_pricing_to_account_stats
        ) VALUES (
            'Cindy Catalog', 'Release-owned Cindy catalog channel', 'active',
            '{"cindy":{}}'::jsonb, 'channel_mapped', TRUE, '',
            '{"cindy_catalog_managed":"cindy_laxa_v1"}'::jsonb, FALSE
        ) RETURNING id INTO managed_channel_id;
    ELSE
        IF EXISTS (
            SELECT 1 FROM channels c WHERE c.id = managed_channel_id AND (
                c.name IS DISTINCT FROM 'Cindy Catalog'
                OR c.description IS DISTINCT FROM 'Release-owned Cindy catalog channel'
                OR c.status IS DISTINCT FROM 'active'
                OR c.model_mapping IS DISTINCT FROM '{"cindy":{}}'::jsonb
                OR c.billing_model_source IS DISTINCT FROM 'channel_mapped'
                OR c.restrict_models IS DISTINCT FROM TRUE
                OR c.features IS DISTINCT FROM ''
                OR c.features_config IS DISTINCT FROM '{"cindy_catalog_managed":"cindy_laxa_v1"}'::jsonb
                OR c.apply_pricing_to_account_stats IS DISTINCT FROM FALSE
            )
        ) OR EXISTS (
            SELECT 1 FROM channels c
            WHERE c.id <> managed_channel_id AND LOWER(BTRIM(c.name)) = LOWER('Cindy Catalog')
        ) OR EXISTS (
            SELECT 1 FROM channel_model_pricing cmp WHERE cmp.channel_id = managed_channel_id
        ) OR EXISTS (
            SELECT 1 FROM channel_account_stats_pricing_rules caspr
            WHERE caspr.channel_id = managed_channel_id
        ) OR EXISTS (
            SELECT 1 FROM channel_groups cg
            WHERE cg.channel_id = managed_channel_id
              AND NOT project_is_strict_cindy_group(cg.group_id)
        ) THEN
            RAISE EXCEPTION 'managed Cindy channel has ambiguous name, mapping, pricing, features, or group topology';
        END IF;
    END IF;

    IF EXISTS (
        SELECT 1 FROM groups g WHERE g.deleted_at IS NULL AND g.platform = 'cindy' AND (
            g.fallback_group_id IS NOT NULL
            OR g.fallback_group_id_on_invalid_request IS NOT NULL
            OR (EXISTS (
                SELECT 1 FROM account_groups ag JOIN accounts a ON a.id = ag.account_id
                WHERE ag.group_id = g.id AND a.deleted_at IS NULL
            ) AND NOT project_is_strict_cindy_group(g.id))
        )
    ) THEN
        RAISE EXCEPTION 'canonical Cindy group has fallback or mixed provider membership';
    END IF;

    SELECT STRING_AGG(g.id::TEXT, ',' ORDER BY g.id) INTO conflicting_group_ids
    FROM groups g JOIN channel_groups cg ON cg.group_id = g.id
    WHERE project_is_strict_cindy_group(g.id) AND cg.channel_id <> managed_channel_id;
    IF conflicting_group_ids IS NOT NULL THEN
        RAISE EXCEPTION 'strict Cindy groups already belong to another channel: %', conflicting_group_ids;
    END IF;
    INSERT INTO channel_groups (channel_id, group_id)
    SELECT managed_channel_id, g.id FROM groups g WHERE project_is_strict_cindy_group(g.id)
    ON CONFLICT (group_id) DO NOTHING;
END;
$$;

CREATE OR REPLACE FUNCTION guard_managed_cindy_channel()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    old_managed BOOLEAN := FALSE;
    new_managed BOOLEAN := FALSE;
    old_reserved BOOLEAN := FALSE;
    new_reserved BOOLEAN := FALSE;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        old_managed := OLD.features_config->>'cindy_catalog_managed' = 'cindy_laxa_v1';
        old_reserved := LOWER(BTRIM(OLD.name)) = LOWER('Cindy Catalog');
    END IF;
    IF TG_OP <> 'DELETE' THEN
        new_managed := NEW.features_config ? 'cindy_catalog_managed';
        new_reserved := LOWER(BTRIM(NEW.name)) = LOWER('Cindy Catalog');
    END IF;
    IF old_managed OR new_managed OR old_reserved OR new_reserved THEN
        RAISE EXCEPTION 'managed Cindy catalog channel is immutable';
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_guard_managed_cindy_channel ON channels;
CREATE TRIGGER trg_guard_managed_cindy_channel
BEFORE INSERT OR UPDATE OR DELETE ON channels
FOR EACH ROW EXECUTE FUNCTION guard_managed_cindy_channel();

CREATE OR REPLACE FUNCTION guard_managed_cindy_channel_group()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    managed_id BIGINT := project_managed_cindy_channel_id();
BEGIN
    IF TG_OP <> 'INSERT' AND OLD.channel_id = managed_id
       AND project_is_strict_cindy_group(OLD.group_id) THEN
        RAISE EXCEPTION 'strict Cindy group binding is managed by the backend';
    END IF;
    IF TG_OP <> 'DELETE' THEN
        IF NEW.channel_id = managed_id AND NOT project_is_strict_cindy_group(NEW.group_id) THEN
            RAISE EXCEPTION 'managed Cindy channel accepts only strict Cindy groups';
        END IF;
        IF NEW.channel_id <> managed_id AND project_is_strict_cindy_group(NEW.group_id) THEN
            RAISE EXCEPTION 'strict Cindy groups must use the managed Cindy channel';
        END IF;
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_guard_managed_cindy_channel_group ON channel_groups;
CREATE TRIGGER trg_guard_managed_cindy_channel_group
BEFORE INSERT OR UPDATE OR DELETE ON channel_groups
FOR EACH ROW EXECUTE FUNCTION guard_managed_cindy_channel_group();

CREATE OR REPLACE FUNCTION guard_managed_cindy_channel_pricing()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    target_channel_id BIGINT := CASE WHEN TG_OP = 'DELETE' THEN OLD.channel_id ELSE NEW.channel_id END;
BEGIN
    IF target_channel_id = project_managed_cindy_channel_id() THEN
        RAISE EXCEPTION 'managed Cindy channel cannot persist partial pricing';
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_guard_managed_cindy_channel_pricing ON channel_model_pricing;
CREATE TRIGGER trg_guard_managed_cindy_channel_pricing
BEFORE INSERT OR UPDATE OR DELETE ON channel_model_pricing
FOR EACH ROW EXECUTE FUNCTION guard_managed_cindy_channel_pricing();

DROP TRIGGER IF EXISTS trg_guard_managed_cindy_account_stats_pricing ON channel_account_stats_pricing_rules;
CREATE TRIGGER trg_guard_managed_cindy_account_stats_pricing
BEFORE INSERT OR UPDATE OR DELETE ON channel_account_stats_pricing_rules
FOR EACH ROW EXECUTE FUNCTION guard_managed_cindy_channel_pricing();

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
       AND OLD.wire_platform IS NOT DISTINCT FROM NEW.wire_platform
       AND OLD.provider_profile IS NOT DISTINCT FROM NEW.provider_profile
       AND OLD.type IS NOT DISTINCT FROM NEW.type
       AND OLD.credentials IS NOT DISTINCT FROM NEW.credentials
       AND OLD.status IS NOT DISTINCT FROM NEW.status
       AND OLD.deleted_at IS NOT DISTINCT FROM NEW.deleted_at THEN
        RETURN NEW;
    END IF;
    target_account_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.id ELSE NEW.id END;
    FOR affected_group_id IN
        SELECT DISTINCT ag.group_id FROM account_groups ag WHERE ag.account_id = target_account_id
    LOOP
        PERFORM enqueue_channel_group_cache_invalidations(affected_group_id);
        IF TG_OP = 'UPDATE' THEN
            PERFORM project_assert_cindy_group_topology(affected_group_id);
            PERFORM project_reconcile_cindy_group_channel(affected_group_id);
        END IF;
    END LOOP;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_accounts_cindy_identity_auth_cache_invalidation ON accounts;
CREATE TRIGGER trg_accounts_cindy_identity_auth_cache_invalidation
AFTER UPDATE OF platform, wire_platform, provider_profile, type, credentials, status, deleted_at ON accounts
FOR EACH ROW EXECUTE FUNCTION enqueue_account_identity_auth_cache_invalidations();

CREATE OR REPLACE FUNCTION enqueue_account_group_auth_cache_invalidations()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        PERFORM enqueue_channel_group_cache_invalidations(OLD.group_id);
        PERFORM project_assert_cindy_group_topology(OLD.group_id);
        PERFORM project_reconcile_cindy_group_channel(OLD.group_id);
    END IF;
    IF TG_OP <> 'DELETE' AND (TG_OP = 'INSERT'
        OR NEW.account_id IS DISTINCT FROM OLD.account_id
        OR NEW.group_id IS DISTINCT FROM OLD.group_id) THEN
        PERFORM enqueue_channel_group_cache_invalidations(NEW.group_id);
        PERFORM project_assert_cindy_group_topology(NEW.group_id);
        PERFORM project_reconcile_cindy_group_channel(NEW.group_id);
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_account_groups_cindy_identity_auth_cache_invalidation ON account_groups;
CREATE TRIGGER trg_account_groups_cindy_identity_auth_cache_invalidation
AFTER INSERT OR UPDATE OR DELETE ON account_groups
FOR EACH ROW EXECUTE FUNCTION enqueue_account_group_auth_cache_invalidations();

CREATE OR REPLACE FUNCTION enqueue_group_auth_cache_invalidation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE'
       AND OLD.status IS NOT DISTINCT FROM NEW.status
       AND OLD.is_exclusive IS NOT DISTINCT FROM NEW.is_exclusive
       AND OLD.allow_image_generation IS NOT DISTINCT FROM NEW.allow_image_generation
       AND OLD.platform IS NOT DISTINCT FROM NEW.platform
       AND OLD.subscription_type IS NOT DISTINCT FROM NEW.subscription_type
       AND OLD.rate_multiplier IS NOT DISTINCT FROM NEW.rate_multiplier
       AND OLD.peak_rate_enabled IS NOT DISTINCT FROM NEW.peak_rate_enabled
       AND OLD.peak_start IS NOT DISTINCT FROM NEW.peak_start
       AND OLD.peak_end IS NOT DISTINCT FROM NEW.peak_end
       AND OLD.peak_rate_multiplier IS NOT DISTINCT FROM NEW.peak_rate_multiplier
       AND OLD.profit_control_enabled IS NOT DISTINCT FROM NEW.profit_control_enabled
       AND OLD.profit_min_margin IS NOT DISTINCT FROM NEW.profit_min_margin
       AND OLD.profit_safety_buffer IS NOT DISTINCT FROM NEW.profit_safety_buffer
       AND OLD.deleted_at IS NOT DISTINCT FROM NEW.deleted_at
       AND OLD.wire_platform IS NOT DISTINCT FROM NEW.wire_platform
       AND OLD.provider_profile IS NOT DISTINCT FROM NEW.provider_profile
       AND OLD.fallback_group_id IS NOT DISTINCT FROM NEW.fallback_group_id
       AND OLD.fallback_group_id_on_invalid_request IS NOT DISTINCT FROM NEW.fallback_group_id_on_invalid_request THEN
        RETURN NEW;
    END IF;
    PERFORM enqueue_group_api_key_auth_cache_invalidations(OLD.id);
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_groups_auth_cache_invalidation ON groups;
CREATE TRIGGER trg_groups_auth_cache_invalidation
AFTER UPDATE OR DELETE ON groups
FOR EACH ROW EXECUTE FUNCTION enqueue_group_auth_cache_invalidation();

CREATE OR REPLACE FUNCTION reconcile_group_cindy_channel_topology()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM enqueue_channel_group_scheduler_invalidation(NEW.id);
    PERFORM project_assert_cindy_group_topology(NEW.id);
    PERFORM project_reconcile_cindy_group_channel(NEW.id);
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_groups_cindy_channel_topology ON groups;
CREATE TRIGGER trg_groups_cindy_channel_topology
AFTER UPDATE OF platform, wire_platform, provider_profile,
    fallback_group_id, fallback_group_id_on_invalid_request, deleted_at ON groups
FOR EACH ROW EXECUTE FUNCTION reconcile_group_cindy_channel_topology();

CREATE OR REPLACE FUNCTION enqueue_channel_definition_invalidations()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    affected_group_id BIGINT;
BEGIN
    IF TG_OP = 'UPDATE'
       AND OLD.status IS NOT DISTINCT FROM NEW.status
       AND OLD.model_mapping IS NOT DISTINCT FROM NEW.model_mapping
       AND OLD.billing_model_source IS NOT DISTINCT FROM NEW.billing_model_source
       AND OLD.restrict_models IS NOT DISTINCT FROM NEW.restrict_models
       AND OLD.features_config IS NOT DISTINCT FROM NEW.features_config THEN
        RETURN NEW;
    END IF;
    FOR affected_group_id IN
        SELECT cg.group_id FROM channel_groups cg
        WHERE cg.channel_id = CASE WHEN TG_OP = 'DELETE' THEN OLD.id ELSE NEW.id END
    LOOP
        PERFORM enqueue_channel_group_cache_invalidations(affected_group_id);
    END LOOP;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_channels_cache_invalidation ON channels;
CREATE TRIGGER trg_channels_cache_invalidation
AFTER UPDATE OR DELETE ON channels
FOR EACH ROW EXECUTE FUNCTION enqueue_channel_definition_invalidations();

CREATE OR REPLACE FUNCTION enqueue_channel_pricing_invalidations()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    affected_group_id BIGINT;
    target_channel_id BIGINT;
BEGIN
    target_channel_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.channel_id ELSE NEW.channel_id END;
    FOR affected_group_id IN
        SELECT cg.group_id FROM channel_groups cg WHERE cg.channel_id = target_channel_id
    LOOP
        PERFORM enqueue_channel_group_cache_invalidations(affected_group_id);
    END LOOP;
    IF TG_OP = 'UPDATE' AND NEW.channel_id IS DISTINCT FROM OLD.channel_id THEN
        FOR affected_group_id IN
            SELECT cg.group_id FROM channel_groups cg WHERE cg.channel_id = OLD.channel_id
        LOOP
            PERFORM enqueue_channel_group_cache_invalidations(affected_group_id);
        END LOOP;
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_channel_model_pricing_cache_invalidation ON channel_model_pricing;
CREATE TRIGGER trg_channel_model_pricing_cache_invalidation
AFTER INSERT OR UPDATE OR DELETE ON channel_model_pricing
FOR EACH ROW EXECUTE FUNCTION enqueue_channel_pricing_invalidations();
