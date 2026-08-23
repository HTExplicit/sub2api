-- Bind canonical Cindy groups to one managed channel. The channel stores only
-- a stable catalog marker: application code projects model mappings and prices
-- from the release-owned Cindy catalog, avoiding a second persisted model list.

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
          AND NOT EXISTS (
              SELECT 1
              FROM account_groups ag
              JOIN accounts a ON a.id = ag.account_id
              WHERE ag.group_id = g.id
                AND a.deleted_at IS NULL
                AND NOT (
                    a.platform IS NOT DISTINCT FROM 'cindy'
                    AND a.wire_platform IS NOT DISTINCT FROM 'openai'
                    AND a.provider_profile IS NOT DISTINCT FROM 'cindy_laxa_v1'
                    AND a.type IS NOT DISTINCT FROM 'apikey'
                    AND jsonb_typeof(a.credentials->'base_url') = 'string'
                    AND LOWER(BTRIM(a.credentials->>'base_url')) IN (
                        'https://api.laxarouter.ai', 'https://api.laxarouter.ai/'
                    )
                )
          )
    );
$$;

CREATE OR REPLACE FUNCTION enqueue_channel_group_cache_invalidations(target_group_id BIGINT)
RETURNS VOID
LANGUAGE plpgsql
AS $$
BEGIN
    IF target_group_id IS NULL OR target_group_id <= 0 THEN
        RETURN;
    END IF;

    INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload, dedup_key)
    VALUES (
        'group_changed', NULL, target_group_id, NULL,
        'scheduler_outbox:channel_group:' || target_group_id::TEXT
    )
    ON CONFLICT (dedup_key) WHERE dedup_key IS NOT NULL DO NOTHING;

    PERFORM enqueue_group_api_key_auth_cache_invalidations(target_group_id);
END;
$$;

CREATE OR REPLACE FUNCTION enqueue_channel_group_binding_invalidations()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        PERFORM enqueue_channel_group_cache_invalidations(OLD.group_id);
    END IF;
    IF TG_OP <> 'DELETE'
       AND (TG_OP = 'INSERT' OR NEW.group_id IS DISTINCT FROM OLD.group_id) THEN
        PERFORM enqueue_channel_group_cache_invalidations(NEW.group_id);
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_channel_groups_cache_invalidation ON channel_groups;
CREATE TRIGGER trg_channel_groups_cache_invalidation
AFTER INSERT OR UPDATE OR DELETE ON channel_groups
FOR EACH ROW EXECUTE FUNCTION enqueue_channel_group_binding_invalidations();

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
        SELECT cg.group_id
        FROM channel_groups cg
        WHERE cg.channel_id = CASE WHEN TG_OP = 'DELETE' THEN OLD.id ELSE NEW.id END
    LOOP
        PERFORM enqueue_channel_group_cache_invalidations(affected_group_id);
    END LOOP;

    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
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
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_channel_model_pricing_cache_invalidation ON channel_model_pricing;
CREATE TRIGGER trg_channel_model_pricing_cache_invalidation
AFTER INSERT OR UPDATE OR DELETE ON channel_model_pricing
FOR EACH ROW EXECUTE FUNCTION enqueue_channel_pricing_invalidations();

DO $$
DECLARE
    managed_channel_id BIGINT;
    managed_channel_count BIGINT;
    conflicting_group_ids TEXT;
BEGIN
    IF EXISTS (
        SELECT 1 FROM groups g
        WHERE g.deleted_at IS NULL
          AND g.platform = 'cindy'
          AND NOT project_is_strict_cindy_group(g.id)
    ) THEN
        RAISE EXCEPTION 'canonical Cindy group has fallback or mixed provider membership';
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM groups g WHERE project_is_strict_cindy_group(g.id)
    ) THEN
        RETURN;
    END IF;

    SELECT COUNT(*), MIN(c.id)
    INTO managed_channel_count, managed_channel_id
    FROM channels c
    WHERE c.features_config->>'cindy_catalog_managed' = 'cindy_laxa_v1';

    IF managed_channel_count > 1 THEN
        RAISE EXCEPTION 'ambiguous managed Cindy channel topology: % candidates', managed_channel_count;
    END IF;

    IF managed_channel_count = 0 THEN
        IF EXISTS (SELECT 1 FROM channels c WHERE LOWER(c.name) = LOWER('Cindy Catalog')) THEN
            RAISE EXCEPTION 'ambiguous managed Cindy channel topology: reserved name already exists';
        END IF;
        INSERT INTO channels (
            name, description, status, model_mapping, billing_model_source,
            restrict_models, features_config
        ) VALUES (
            'Cindy Catalog', 'Release-owned Cindy catalog channel', 'active',
            '{"cindy":{}}'::jsonb, 'channel_mapped', TRUE,
            '{"cindy_catalog_managed":"cindy_laxa_v1"}'::jsonb
        ) RETURNING id INTO managed_channel_id;
    ELSE
        IF EXISTS (
            SELECT 1
            FROM channels c
            WHERE c.id = managed_channel_id
              AND (
                  COALESCE(c.model_mapping, '{}'::jsonb) NOT IN ('{}'::jsonb, '{"cindy":{}}'::jsonb)
              )
        ) OR EXISTS (
            SELECT 1 FROM channel_model_pricing cmp
            WHERE cmp.channel_id = managed_channel_id
        ) OR EXISTS (
            SELECT 1
            FROM channel_groups cg
            WHERE cg.channel_id = managed_channel_id
              AND NOT project_is_strict_cindy_group(cg.group_id)
        ) THEN
            RAISE EXCEPTION 'managed Cindy channel has ambiguous mapping, pricing, or group topology';
        END IF;

        UPDATE channels
        SET status = 'active',
            model_mapping = '{"cindy":{}}'::jsonb,
            billing_model_source = 'channel_mapped',
            restrict_models = TRUE,
            updated_at = NOW()
        WHERE id = managed_channel_id;
    END IF;

    SELECT STRING_AGG(g.id::TEXT, ',' ORDER BY g.id)
    INTO conflicting_group_ids
    FROM groups g
    JOIN channel_groups cg ON cg.group_id = g.id
    WHERE project_is_strict_cindy_group(g.id)
      AND cg.channel_id <> managed_channel_id;

    IF conflicting_group_ids IS NOT NULL THEN
        RAISE EXCEPTION 'strict Cindy groups already belong to another channel: %', conflicting_group_ids;
    END IF;

    INSERT INTO channel_groups (channel_id, group_id)
    SELECT managed_channel_id, g.id
    FROM groups g
    WHERE project_is_strict_cindy_group(g.id)
    ON CONFLICT (group_id) DO NOTHING;
END;
$$;
