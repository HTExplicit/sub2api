-- Freeze legacy global references and preserve each domain's effective switch.
-- The archived policy/runtime and original Claude settings remain available for
-- inspection; application traffic reads only the v2 policy after this migration.
ALTER TABLE system_prompt_rule_policies
    ADD COLUMN IF NOT EXISTS legacy_policy JSONB,
    ADD COLUMN IF NOT EXISTS legacy_runtime JSONB;

ALTER TABLE system_prompt_template_versions
    DROP CONSTRAINT IF EXISTS system_prompt_template_versions_composition;
ALTER TABLE system_prompt_template_versions
    ADD CONSTRAINT system_prompt_template_versions_composition CHECK (
        (composition_mode IN ('inline', 'anthropic_system_blocks')
         AND bundle_id IS NULL AND bundle_manifest_sha256 IS NULL)
        OR (composition_mode = 'codex_skill_hybrid'
            AND bundle_id = 'codexrip-reverse-skill' AND bundle_manifest_sha256 IS NULL)
    );

DO $prompt_v2$
DECLARE
    old_policy JSONB;
    old_runtime JSONB;
    old_rule JSONB;
    new_rule JSONB;
    new_rules JSONB := '[]'::jsonb;
    new_defaults JSONB := '[]'::jsonb;
    omitted_ids JSONB := '[]'::jsonb;
    reference_id JSONB;
    selected_template BIGINT;
    selected_version BIGINT;
    resolved_role TEXT;
    prompt_enabled BOOLEAN;
    configured_blocks TEXT;
    expansion_prompt TEXT;
    raw_blocks JSONB;
    normalized_blocks JSONB := '[]'::jsonb;
    old_block JSONB;
    new_block JSONB;
    cache_control JSONB;
    block_type TEXT;
    migrated_body TEXT;
    migrated_template BIGINT;
    migrated_version BIGINT;
    claude_rule_id TEXT := 'migrated-claude-oauth';
    suffix INTEGER := 0;
    -- Match strings.TrimSpace used by the legacy Claude parser, including
    -- Unicode whitespace, so an empty setting never becomes custom content.
    trim_chars TEXT := CHR(9) || CHR(10) || CHR(11) || CHR(12) || CHR(13) || CHR(32)
        || CHR(133) || CHR(160) || CHR(5760) || CHR(8192) || CHR(8193) || CHR(8194)
        || CHR(8195) || CHR(8196) || CHR(8197) || CHR(8198) || CHR(8199) || CHR(8200)
        || CHR(8201) || CHR(8202) || CHR(8232) || CHR(8233) || CHR(8239) || CHR(8287) || CHR(12288);
BEGIN
    SELECT p.policy, to_jsonb(r) INTO old_policy, old_runtime
    FROM system_prompt_rule_policies p JOIN system_prompt_runtime r ON r.id = p.id
    WHERE p.id = 1 FOR UPDATE OF p, r;
    IF old_policy IS NULL OR old_policy->>'version' = '2' THEN
        RETURN;
    END IF;
    IF old_policy->>'version' IS DISTINCT FROM '1' THEN
        RAISE EXCEPTION 'unsupported legacy prompt policy version';
    END IF;

    FOR old_rule IN SELECT value FROM jsonb_array_elements(old_policy->'rules') LOOP
        selected_template := (old_rule->>'template_id')::bigint;
        selected_version := (old_rule->>'version_id')::bigint;
        IF COALESCE((old_rule->>'follow_active')::boolean, false) THEN
            selected_template := (old_runtime->>'active_template_id')::bigint;
            selected_version := (old_runtime->>'active_version_id')::bigint;
        END IF;
        IF COALESCE(selected_template, 0) < 1 OR COALESCE(selected_version, 0) < 1 THEN
            -- Migration 253 installs an empty seed placeholder on a fresh
            -- database. It has no content or effective behavior to preserve.
            IF old_rule->>'id' = 'legacy-default'
               AND NOT (old_runtime->>'enabled')::boolean
               AND NOT EXISTS (
                   SELECT 1 FROM accounts a
                   WHERE a.deleted_at IS NULL
                     AND a.extra->'prompt_skills'->>'mode' = 'custom'
                     AND a.extra->'prompt_skills'->'rule_ids' ? (old_rule->>'id')
               ) THEN
                omitted_ids := omitted_ids || jsonb_build_array(old_rule->>'id');
                CONTINUE;
            END IF;
            RAISE EXCEPTION 'legacy prompt rule has no immutable content';
        END IF;
        IF NOT EXISTS (
            SELECT 1 FROM system_prompt_template_versions v
            JOIN system_prompt_templates t ON t.id = v.template_id
            WHERE v.id = selected_version AND t.id = selected_template AND t.deleted_at IS NULL
        ) THEN
            RAISE EXCEPTION 'legacy prompt rule content reference is invalid';
        END IF;
        resolved_role := CASE old_rule->>'delivery'
            WHEN 'native_control' THEN 'auto'
            WHEN 'system' THEN 'system'
            WHEN 'developer' THEN 'developer'
            ELSE NULL END;
        IF resolved_role IS NULL THEN
            RAISE EXCEPTION 'legacy prompt delivery is invalid';
        END IF;
        new_rule := (old_rule - 'follow_active' - 'delivery') || jsonb_build_object(
            'template_id', selected_template, 'version_id', selected_version,
            'role', resolved_role, 'platforms', jsonb_build_array('openai', 'cindy'),
            'enabled', COALESCE((old_rule->>'enabled')::boolean, false)
                       AND (old_runtime->>'enabled')::boolean
        );
        new_rules := new_rules || jsonb_build_array(new_rule);
    END LOOP;
    FOR reference_id IN SELECT value FROM jsonb_array_elements(old_policy->'default_rule_ids') LOOP
        IF NOT omitted_ids ? (reference_id #>> '{}') THEN
            new_defaults := new_defaults || jsonb_build_array(reference_id);
        END IF;
    END LOOP;

    -- Only explicit custom content creates a Claude rule. Empty settings keep
    -- the protocol adapter's built-in billing and Claude Code identity blocks.
    SELECT COALESCE(NULLIF(value, ''), 'true') = 'true' INTO prompt_enabled
    FROM settings WHERE key = 'enable_claude_oauth_system_prompt_injection';
    prompt_enabled := COALESCE(prompt_enabled, true);
    SELECT value INTO configured_blocks FROM settings WHERE key = 'claude_oauth_system_prompt_blocks';
    SELECT value INTO expansion_prompt FROM settings WHERE key = 'claude_oauth_system_prompt';
    configured_blocks := BTRIM(COALESCE(configured_blocks, ''), trim_chars);
    expansion_prompt := BTRIM(COALESCE(expansion_prompt, ''), trim_chars);
    raw_blocks := '[]'::jsonb;
    IF configured_blocks <> '' THEN
        raw_blocks := configured_blocks::jsonb;
        IF jsonb_typeof(raw_blocks) = 'object' THEN
            raw_blocks := COALESCE(NULLIF(raw_blocks->'blocks', 'null'::jsonb), '[]'::jsonb);
        END IF;
        IF raw_blocks = 'null'::jsonb THEN
            raw_blocks := '[]'::jsonb;
        END IF;
        IF jsonb_typeof(raw_blocks) IS DISTINCT FROM 'array' THEN
            RAISE EXCEPTION 'legacy Claude prompt blocks are invalid';
        END IF;
    END IF;
    IF jsonb_array_length(raw_blocks) = 0 AND expansion_prompt <> '' THEN
        raw_blocks := '[
            {"type":"text","text":"{billing_header}"},
            {"type":"text","text":"{claude_code_system_prompt}"},
            {"type":"text","text":"{claude_code_expansion_prompt}","cache_control":{"type":"ephemeral","ttl":"5m"}}
        ]'::jsonb;
    END IF;
    IF jsonb_array_length(raw_blocks) > 0 THEN
        FOR old_block IN SELECT value FROM jsonb_array_elements(raw_blocks) LOOP
            IF old_block = 'null'::jsonb THEN
                old_block := '{}'::jsonb;
            END IF;
            IF jsonb_typeof(old_block) IS DISTINCT FROM 'object'
               OR COALESCE(jsonb_typeof(old_block->'enabled'), 'null') NOT IN ('boolean', 'null')
               OR COALESCE(jsonb_typeof(old_block->'type'), 'null') NOT IN ('string', 'null')
               OR COALESCE(jsonb_typeof(old_block->'text'), 'null') NOT IN ('string', 'null') THEN
                RAISE EXCEPTION 'legacy Claude prompt block shape is invalid';
            END IF;
            block_type := COALESCE(NULLIF(BTRIM(old_block->>'type', trim_chars), ''), 'text');
            IF block_type <> 'text' THEN
                RAISE EXCEPTION 'legacy Claude prompt block type is invalid';
            END IF;
            new_block := jsonb_build_object(
                'type', 'text', 'text', COALESCE(old_block->>'text', ''),
                'enabled', COALESCE((old_block->>'enabled')::boolean, true)
            );
            cache_control := old_block->'cache_control';
            IF cache_control = 'true'::jsonb THEN
                cache_control := '{"type":"ephemeral","ttl":"5m"}'::jsonb;
            ELSIF cache_control IS NULL OR cache_control IN ('false'::jsonb, 'null'::jsonb) THEN
                cache_control := NULL;
            ELSIF jsonb_typeof(cache_control) <> 'object' THEN
                RAISE EXCEPTION 'legacy Claude prompt cache control is invalid';
            END IF;
            IF cache_control IS NOT NULL THEN
                new_block := new_block || jsonb_build_object('cache_control', cache_control);
            END IF;
            normalized_blocks := normalized_blocks || jsonb_build_array(new_block);
        END LOOP;
        -- Preserve single-pass placeholder expansion: a configured expansion
        -- containing a placeholder is literal replacement text, not a template.
        migrated_body := jsonb_build_object('blocks', normalized_blocks,
                                           'expansion_prompt', expansion_prompt)::text;
        IF OCTET_LENGTH(migrated_body) > 65536 THEN
            RAISE EXCEPTION 'migrated Claude prompt exceeds the immutable content limit';
        END IF;
        WHILE EXISTS (SELECT 1 FROM jsonb_array_elements(new_rules) item WHERE item->>'id' = claude_rule_id)
           OR EXISTS (SELECT 1 FROM system_prompt_templates WHERE slug = claude_rule_id) LOOP
            suffix := suffix + 1;
            claude_rule_id := 'migrated-claude-oauth-' || suffix;
        END LOOP;
        INSERT INTO system_prompt_templates (slug, name, description)
        VALUES (claude_rule_id, 'Claude OAuth custom instructions', 'Migrated explicit Claude OAuth system blocks')
        RETURNING id INTO migrated_template;
        INSERT INTO system_prompt_template_versions
            (template_id, version, body, sha256, byte_length, composition_mode, note, published_at)
        VALUES (migrated_template, 1, migrated_body,
                encode(sha256(convert_to(migrated_body, 'UTF8')), 'hex'), OCTET_LENGTH(migrated_body),
                'anthropic_system_blocks', 'Migrated from Claude OAuth custom prompt settings', NOW())
        RETURNING id INTO migrated_version;
        new_rules := new_rules || jsonb_build_array(jsonb_build_object(
            'id', claude_rule_id, 'name', 'Claude OAuth custom instructions', 'enabled', prompt_enabled,
            'template_id', migrated_template, 'version_id', migrated_version,
            'role', 'auto', 'position', 'control_append', 'order', 100,
            'platforms', jsonb_build_array('anthropic'), 'account_types', jsonb_build_array('oauth', 'setup-token'),
            'request_profiles', jsonb_build_array('generic-mimic'),
            'exclude_model_contains', jsonb_build_array('fable'),
            'model_match', 'upstream', 'models', '[]'::jsonb
        ));
        new_defaults := new_defaults || jsonb_build_array(claude_rule_id);
    END IF;
    IF jsonb_array_length(new_rules) > 64 THEN
        RAISE EXCEPTION 'migrated prompt policy exceeds the rule limit';
    END IF;
    UPDATE system_prompt_rule_policies
    SET legacy_policy = COALESCE(legacy_policy, old_policy),
        legacy_runtime = COALESCE(legacy_runtime, old_runtime),
        policy = jsonb_build_object('version', 2, 'rules', new_rules, 'default_rule_ids', new_defaults)
    WHERE id = 1;
    UPDATE system_prompt_runtime
    SET enabled = true, revision = revision + 1, updated_at = NOW()
    WHERE id = 1;
END;
$prompt_v2$;

DO $prompt_v2_constraint$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                   WHERE conname = 'system_prompt_rule_policy_v2'
                     AND conrelid = 'system_prompt_rule_policies'::regclass) THEN
        ALTER TABLE system_prompt_rule_policies
            ADD CONSTRAINT system_prompt_rule_policy_v2 CHECK (policy->>'version' = '2');
    END IF;
END;
$prompt_v2_constraint$;
