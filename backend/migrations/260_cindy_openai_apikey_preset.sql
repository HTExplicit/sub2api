-- Cindy (Laxa) becomes an ordinary OpenAI API-key upstream.
--
-- Accounts keep their own base_url (https://api.laxarouter.ai), API key, proxy,
-- groups and protocol extras; only the retired `cindy` platform identity, its
-- projection/guard/cache triggers, the Cindy tables/columns and the
-- cindy_provider_config setting are removed. Model routing becomes plain data:
-- each account's model_mapping is rewritten to the public free baseline and
-- the formerly managed "Cindy Catalog" channel becomes an ordinary channel that
-- carries the Cindy list prices (model-access catalog, 2026-09-25).
-- No explicit BEGIN/COMMIT: the runner owns this file's transaction.
SET LOCAL lock_timeout = '10s';
SET LOCAL statement_timeout = '120s';

-- 1. Guard, projection and Cindy-motivated cache triggers. The guards reject
-- every edit of the managed channel; the identity triggers read columns that
-- are dropped below. Channel caches keep the application-level invalidation.
DROP TRIGGER IF EXISTS trg_guard_managed_cindy_channel ON channels;
DROP TRIGGER IF EXISTS trg_guard_managed_cindy_channel_group ON channel_groups;
DROP TRIGGER IF EXISTS trg_guard_managed_cindy_channel_pricing ON channel_model_pricing;
DROP TRIGGER IF EXISTS trg_guard_managed_cindy_account_stats_pricing ON channel_account_stats_pricing_rules;
DROP TRIGGER IF EXISTS trg_groups_cindy_channel_topology ON groups;
DROP TRIGGER IF EXISTS trg_channel_groups_cache_invalidation ON channel_groups;
DROP TRIGGER IF EXISTS trg_channels_cache_invalidation ON channels;
DROP TRIGGER IF EXISTS trg_channel_model_pricing_cache_invalidation ON channel_model_pricing;
DROP TRIGGER IF EXISTS trg_accounts_cindy_identity_auth_cache_invalidation ON accounts;
DROP TRIGGER IF EXISTS trg_accounts_cindy_identity_delete_auth_cache_invalidation ON accounts;
DROP TRIGGER IF EXISTS trg_account_groups_cindy_identity_auth_cache_invalidation ON account_groups;

-- Restore the upstream (193) group auth-cache trigger body; the 236 copy read
-- the identity columns dropped below.
CREATE OR REPLACE FUNCTION enqueue_group_auth_cache_invalidation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    target_group_id BIGINT;
BEGIN
    target_group_id := OLD.id;
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
       AND OLD.deleted_at IS NOT DISTINCT FROM NEW.deleted_at THEN
        RETURN NEW;
    END IF;

    INSERT INTO auth_cache_invalidation_outbox (cache_key)
    SELECT encode(sha256(convert_to(k.key, 'UTF8')), 'hex')
    FROM api_keys AS k
    WHERE k.group_id = target_group_id
      AND k.deleted_at IS NULL
      AND k.key <> '';
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

-- 2. Durable Cindy exhaustion / 401 markers become the ordinary error state
-- (manual recovery or a successful account test). The original upstream text
-- was never stored, so the message names the migrated marker.
UPDATE accounts AS a
SET status = 'error',
    error_message = 'budget_exceeded: Cindy balance exhausted (migrated marker)',
    updated_at = NOW()
WHERE a.platform = 'cindy'
  AND a.deleted_at IS NULL
  AND a.status = 'active'
  AND (a.cindy_balance_insufficient_at IS NOT NULL
       OR EXISTS (SELECT 1 FROM cindy_health_states AS h
                  WHERE h.account_id = a.id AND h.status = 'confirmed_exhausted'));

UPDATE accounts AS a
SET status = 'error',
    error_message = 'Unauthorized (401): Cindy account banned (migrated marker)',
    updated_at = NOW()
WHERE a.platform = 'cindy'
  AND a.deleted_at IS NULL
  AND a.status = 'active'
  AND (a.cindy_banned_at IS NOT NULL
       OR EXISTS (SELECT 1 FROM cindy_health_states AS h
                  WHERE h.account_id = a.id AND h.status = 'banned'));

-- 3. The formerly managed catalog channel carries the Cindy list prices for
-- the baseline models (USD per token, published cost discount applied:
-- qwen3.8-27b 30%, glm-5.3-flash 20%). Priority prices are expressed as the
-- fast multiplier; gpt-5.6-luna's >272K tier is a context interval.
CREATE TEMP TABLE cindy_260_baseline_pricing (
    model TEXT PRIMARY KEY,
    input_price NUMERIC(20,12) NOT NULL,
    output_price NUMERIC(20,12) NOT NULL,
    cache_read_price NUMERIC(20,12) NOT NULL,
    cache_write_price NUMERIC(20,12),
    fast_multiplier NUMERIC(12,6)
) ON COMMIT DROP;

INSERT INTO cindy_260_baseline_pricing
    (model, input_price, output_price, cache_read_price, cache_write_price, fast_multiplier)
VALUES
    ('deepseek-v4-flash',            0.0000003,     0.0000012,   0.000000006,  0,              NULL),
    ('deepseek-v4-flash-vision-exp', 0.0000003,     0.0000012,   0.000000006,  0,              NULL),
    ('deepseek-v4-pro',              0.00000132,    0.00000396,  0.000000044,  0,              NULL),
    ('gemini-3.6-flash',             0.0000015,     0.0000075,   0.00000015,   NULL,           1.8),
    ('gpt-5.6-luna',                 0.0000002,     0.0000012,   0.00000002,   NULL,           10),
    ('qwen3.8-27b',                  0.0000002975,  0.000001785, 0.0000000595, 0.000000371875, NULL),
    ('qwen3.8-flash',                0.00000016,    0.00000047,  0.000000016,  0.0000002,      NULL),
    ('hy3',                          0.000000132,   0.000000528, 0.000000033,  0,              NULL),
    ('glm-5.3-flash',                0.00000012,    0.0000004,   0.000000024,  0,              NULL);

WITH managed AS (
    SELECT c.id FROM channels AS c
    WHERE c.features_config->>'cindy_catalog_managed' = 'cindy_laxa_v1'
), inserted AS (
    INSERT INTO channel_model_pricing (
        channel_id, platform, models, billing_mode, input_price, output_price,
        cache_write_price, cache_read_price, fast_multiplier
    )
    SELECT m.id, 'openai', jsonb_build_array(p.model), 'token', p.input_price, p.output_price,
           p.cache_write_price, p.cache_read_price, p.fast_multiplier
    FROM managed AS m
    CROSS JOIN cindy_260_baseline_pricing AS p
    WHERE NOT EXISTS (
        SELECT 1 FROM channel_model_pricing AS existing
        WHERE existing.channel_id = m.id AND existing.models ? p.model
    )
    RETURNING id, models
)
INSERT INTO channel_pricing_intervals (
    pricing_id, min_tokens, max_tokens, input_price, output_price, cache_read_price, sort_order
)
SELECT i.id, 272000, NULL, 0.000002, 0.000009, 0.0000002, 0
FROM inserted AS i
WHERE i.models ? 'gpt-5.6-luna';

UPDATE channels
SET features_config = features_config - 'cindy_catalog_managed',
    description = CASE
        WHEN description = 'Release-owned Cindy catalog channel'
        THEN 'Cindy (Laxa) list prices for the OpenAI-compatible free baseline'
        ELSE description
    END,
    updated_at = NOW()
WHERE features_config ? 'cindy_catalog_managed';

-- Channel data keyed by the retired platform moves to openai (existing
-- openai entries win on conflict).
UPDATE channels
SET model_mapping = CASE
        WHEN jsonb_typeof(model_mapping->'cindy') = 'object' AND model_mapping->'cindy' <> '{}'::jsonb
        THEN (model_mapping - 'cindy') || jsonb_build_object(
            'openai', (model_mapping->'cindy') || COALESCE(model_mapping->'openai', '{}'::jsonb))
        ELSE model_mapping - 'cindy'
    END,
    updated_at = NOW()
WHERE jsonb_typeof(model_mapping) = 'object' AND model_mapping ? 'cindy';

UPDATE channel_model_pricing SET platform = 'openai', updated_at = NOW() WHERE platform = 'cindy';
UPDATE channel_account_stats_model_pricing SET platform = 'openai', updated_at = NOW() WHERE platform = 'cindy';

-- 4. Accounts: platform openai, model_mapping = public free baseline (short
-- public id -> provider/model), protocol defaults kept as ordinary extras
-- (existing values win): Responses forced, compact and WS v2 off, long
-- prompt_cache_key hashed, and no automatic Codex image_generation tool (the
-- Laxa endpoint rejects it; the Cindy identity used to suppress it). The dead
-- alpha-search mode is removed. cindy_device_id and cindy_device_id_source stay
-- as plain extras without logic.
UPDATE accounts
SET platform = 'openai',
    credentials = jsonb_set(
        COALESCE(credentials, '{}'::jsonb),
        '{model_mapping}',
        '{
            "deepseek-v4-flash": "deepseek/deepseek-v4-flash",
            "deepseek-v4-flash-vision-exp": "deepseek/deepseek-v4-flash-vision-exp",
            "deepseek-v4-pro": "deepseek/deepseek-v4-pro",
            "gemini-3.6-flash": "google/gemini-3.6-flash",
            "gpt-5.6-luna": "openai/gpt-5.6-luna",
            "qwen3.8-27b": "qwen/qwen3.8-27b",
            "qwen3.8-flash": "qwen/qwen3.8-flash",
            "hy3": "tencent/hy3",
            "glm-5.3-flash": "z-ai/glm-5.3-flash"
        }'::jsonb,
        TRUE
    ),
    extra = jsonb_build_object(
        'openai_responses_mode', 'force_responses',
        'openai_compact_mode', 'force_off',
        'openai_apikey_responses_websockets_v2_enabled', FALSE,
        'openai_prompt_cache_key_mode', 'sha256_64',
        'codex_image_generation_bridge', FALSE
    ) || (CASE WHEN jsonb_typeof(extra) = 'object' THEN extra ELSE '{}'::jsonb END - 'openai_alpha_search_mode'),
    updated_at = NOW()
WHERE platform = 'cindy';

-- openai_alpha_search_mode was a rollback-window key without runtime readers.
UPDATE accounts
SET extra = extra - 'openai_alpha_search_mode'
WHERE jsonb_typeof(extra) = 'object' AND extra ? 'openai_alpha_search_mode';

-- 5. Groups (group 12 in production) become ordinary OpenAI groups; the
-- restored upstream trigger invalidates the auth snapshots of their keys.
UPDATE groups SET platform = 'openai', updated_at = NOW() WHERE platform = 'cindy';

-- 6. Per-user quotas: a 'cindy' limit has no platform any more. Cindy usage is
-- now OpenAI usage and follows the user's openai limit (if any); copying the
-- cindy limit into openai would newly restrict real OpenAI traffic.
DELETE FROM user_platform_quotas WHERE platform = 'cindy';
ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;
ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok',
                        'kimi', 'zhipu', 'deepseek', 'minimax', 'opencode_go'));

UPDATE settings
SET value = (value::jsonb - 'cindy')::text, updated_at = NOW()
WHERE key = 'default_platform_quotas'
  AND CASE WHEN value IS JSON OBJECT THEN value::jsonb ? 'cindy' ELSE FALSE END;

DELETE FROM settings WHERE key = 'cindy_provider_config';

-- The Responses image bridge served only Cindy image models; its switch leaves
-- the stored image tools settings (studio_enabled is kept, missing means off).
UPDATE settings
SET value = (value::jsonb - 'responses_image_enabled')::text, updated_at = NOW()
WHERE key = 'image_tools_config'
  AND CASE WHEN value IS JSON OBJECT THEN value::jsonb ? 'responses_image_enabled' ELSE FALSE END;

-- 7. Cindy tables, functions, constraints and columns.
DROP TABLE IF EXISTS cindy_balance_probe_items;
DROP TABLE IF EXISTS cindy_balance_probe_jobs;
DROP TABLE IF EXISTS cindy_health_states;
DROP TABLE IF EXISTS cindy_platform_v1_projection;
DROP TABLE IF EXISTS account_credential_identities;

DROP FUNCTION IF EXISTS project_sync_cindy_credential_generation();
DROP FUNCTION IF EXISTS reconcile_group_cindy_channel_topology();
DROP FUNCTION IF EXISTS guard_managed_cindy_channel();
DROP FUNCTION IF EXISTS guard_managed_cindy_channel_group();
DROP FUNCTION IF EXISTS guard_managed_cindy_channel_pricing();
DROP FUNCTION IF EXISTS enqueue_channel_group_binding_invalidations();
DROP FUNCTION IF EXISTS enqueue_channel_definition_invalidations();
DROP FUNCTION IF EXISTS enqueue_channel_pricing_invalidations();
DROP FUNCTION IF EXISTS enqueue_account_identity_auth_cache_invalidations();
DROP FUNCTION IF EXISTS enqueue_account_group_auth_cache_invalidations();
DROP FUNCTION IF EXISTS project_reconcile_cindy_group_channel(BIGINT);
DROP FUNCTION IF EXISTS project_assert_cindy_group_topology(BIGINT);
-- enqueue_channel_group_cache_invalidations(BIGINT),
-- enqueue_group_api_key_auth_cache_invalidations(BIGINT) and
-- enqueue_channel_group_scheduler_invalidation(BIGINT) are generic helpers that
-- stay: the managed model route trigger from 240
-- (trg_groups_managed_model_routes_invalidation) and migration 242 call them.
DROP FUNCTION IF EXISTS project_managed_cindy_channel_id();
DROP FUNCTION IF EXISTS project_is_strict_cindy_group(BIGINT);
DROP FUNCTION IF EXISTS project_cindy_platform_v1_from_legacy();
DROP FUNCTION IF EXISTS project_cindy_platform_v1_discover_legacy();
DROP FUNCTION IF EXISTS project_cindy_platform_v1_to_legacy();

ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_cindy_platform_identity_check;
ALTER TABLE groups DROP CONSTRAINT IF EXISTS groups_cindy_platform_identity_check;
DROP INDEX IF EXISTS accounts_provider_identity_idx;
DROP INDEX IF EXISTS groups_provider_identity_idx;
DROP INDEX IF EXISTS accounts_cindy_banned_at_idx;
DROP INDEX IF EXISTS idx_accounts_cindy_stats_reset_at;

ALTER TABLE accounts
    DROP COLUMN IF EXISTS wire_platform,
    DROP COLUMN IF EXISTS provider_profile,
    DROP COLUMN IF EXISTS cindy_balance_insufficient_at,
    DROP COLUMN IF EXISTS cindy_banned_at,
    DROP COLUMN IF EXISTS cindy_credential_generation,
    DROP COLUMN IF EXISTS cindy_account_stats_reset_at;

ALTER TABLE groups
    DROP COLUMN IF EXISTS wire_platform,
    DROP COLUMN IF EXISTS provider_profile;

-- 8. Rebuild scheduler buckets for the re-platformed accounts.
INSERT INTO scheduler_outbox (event_type) VALUES ('full_rebuild');
