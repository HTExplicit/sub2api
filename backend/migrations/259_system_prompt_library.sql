-- System prompts become one settings row (system_prompts: a small library,
-- one global switch, one site default) plus an optional per-account binding in
-- accounts.extra.system_prompt. The template/version/rule/Skill/frozen-file
-- storage is removed.
--
-- Behaviour is unchanged: the switch starts off and no default is selected,
-- matching the previous state in which the legacy default rule was disabled.
-- Inline rule content is kept as library prompts so an administrator can
-- enable it later. The migrated Claude OAuth rule returns to the upstream
-- claude_oauth_system_prompt(_blocks) settings.
SET LOCAL lock_timeout = '10s';
SET LOCAL statement_timeout = '120s';

DO $system_prompts$
DECLARE
    library JSONB := '[]'::jsonb;
    policy_rule JSONB;
    version_body TEXT;
    version_mode TEXT;
    prompt_id TEXT;
    prompt_name TEXT;
    prompt_position TEXT;
    prompt_role TEXT;
    used_ids TEXT[] := ARRAY[]::TEXT[];
    claude_body JSONB;
    claude_blocks JSONB;
    claude_expansion TEXT;
    current_blocks JSONB;
    current_expansion TEXT;
BEGIN
    IF to_regclass('system_prompt_rule_policies') IS NOT NULL
       AND to_regclass('system_prompt_template_versions') IS NOT NULL THEN
        FOR policy_rule IN
            SELECT item.value
            FROM system_prompt_rule_policies p,
                 jsonb_array_elements(CASE WHEN jsonb_typeof(p.policy->'rules') = 'array'
                                           THEN p.policy->'rules' ELSE '[]'::jsonb END) WITH ORDINALITY AS item(value, ord)
            WHERE p.id = 1
            ORDER BY item.ord
        LOOP
            version_body := NULL;
            version_mode := NULL;
            IF (policy_rule->>'version_id') ~ '^[0-9]+$' THEN
                SELECT v.body, v.composition_mode INTO version_body, version_mode
                FROM system_prompt_template_versions v
                WHERE v.id = (policy_rule->>'version_id')::BIGINT;
            END IF;

            IF version_mode = 'anthropic_system_blocks' THEN
                IF claude_body IS NULL AND COALESCE((policy_rule->>'enabled')::BOOLEAN, FALSE) THEN
                    BEGIN
                        claude_body := version_body::JSONB;
                    EXCEPTION WHEN others THEN
                        claude_body := NULL;
                    END;
                END IF;
                CONTINUE;
            END IF;

            -- Only plain text survives; Skill/hybrid content depended on the
            -- removed registry and was never plain library text.
            IF version_mode IS DISTINCT FROM 'inline' OR version_body IS NULL
               OR version_body !~ '[^[:space:]]' OR octet_length(version_body) > 65536
               OR jsonb_array_length(library) >= 50 THEN
                CONTINUE;
            END IF;

            prompt_id := COALESCE(policy_rule->>'id', '');
            IF prompt_id !~ '^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$' OR prompt_id = ANY(used_ids) THEN
                prompt_id := 'imported-' || (jsonb_array_length(library) + 1);
            END IF;
            used_ids := used_ids || prompt_id;
            prompt_name := left(btrim(COALESCE(policy_rule->>'name', '')), 100);
            IF prompt_name !~ '[^[:space:]]' THEN
                prompt_name := prompt_id;
            END IF;
            prompt_position := CASE WHEN policy_rule->>'position' IN ('control_prepend', 'conversation_head')
                                    THEN 'prepend' ELSE 'append' END;
            prompt_role := CASE WHEN policy_rule->>'role' IN ('system', 'developer')
                                THEN policy_rule->>'role' ELSE 'auto' END;
            library := library || jsonb_build_array(jsonb_build_object(
                'id', prompt_id, 'name', prompt_name, 'body', version_body,
                'position', prompt_position, 'role', prompt_role));
        END LOOP;
    END IF;

    INSERT INTO settings (key, value)
    VALUES ('system_prompts', jsonb_build_object(
        'enabled', FALSE, 'default_prompt_id', '', 'prompts', library)::TEXT)
    ON CONFLICT (key) DO NOTHING;

    -- Claude OAuth mimicry reads only the upstream settings again. Production
    -- already stores the same blocks; copy the enabled rule only on a mismatch.
    IF jsonb_typeof(claude_body->'blocks') = 'array' THEN
        claude_blocks := claude_body->'blocks';
        claude_expansion := COALESCE(claude_body->>'expansion_prompt', '');
        BEGIN
            SELECT NULLIF(btrim(value), '')::JSONB INTO current_blocks
            FROM settings WHERE key = 'claude_oauth_system_prompt_blocks';
        EXCEPTION WHEN others THEN
            current_blocks := NULL;
        END;
        IF jsonb_typeof(current_blocks) = 'object' THEN
            current_blocks := current_blocks->'blocks';
        END IF;
        SELECT value INTO current_expansion FROM settings WHERE key = 'claude_oauth_system_prompt';
        IF current_blocks IS DISTINCT FROM claude_blocks
           OR COALESCE(current_expansion, '') IS DISTINCT FROM claude_expansion THEN
            INSERT INTO settings (key, value) VALUES ('claude_oauth_system_prompt_blocks', jsonb_pretty(claude_blocks))
            ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();
            INSERT INTO settings (key, value) VALUES ('claude_oauth_system_prompt', claude_expansion)
            ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();
        END IF;
    END IF;
END;
$system_prompts$;

-- Account bindings move from prompt_skills to system_prompt. Only an explicit
-- "off" has a meaning in the new model; rule selections referenced the
-- removed rule list.
UPDATE accounts
SET extra = (extra - 'prompt_skills')
    || CASE WHEN extra->'prompt_skills'->>'mode' = 'off'
            THEN jsonb_build_object('system_prompt', jsonb_build_object('mode', 'off'))
            ELSE '{}'::jsonb END
WHERE extra ? 'prompt_skills';

DO $drop_protection$
DECLARE
    protected RECORD;
BEGIN
    FOR protected IN
        SELECT trigger_name, table_name FROM (VALUES
            ('trg_protect_prompt_independent_state', 'system_prompt_independent_state'),
            ('trg_protect_prompt_independent_contents', 'system_prompt_independent_contents'),
            ('trg_protect_prompt_version_compat', 'system_prompt_version_compat'),
            ('trg_protect_prompt_frozen_public_files', 'system_prompt_frozen_public_files'),
            ('trg_protect_system_prompt_version_content', 'system_prompt_template_versions'),
            ('trg_prevent_system_prompt_version_delete', 'system_prompt_template_versions'),
            ('trg_protect_system_prompt_template_managed_source', 'system_prompt_templates'),
            ('trg_protect_system_prompt_skill_bundle_version', 'system_prompt_skill_bundle_versions'),
            ('trg_prevent_system_prompt_skill_bundle_version_delete', 'system_prompt_skill_bundle_versions'),
            ('trg_protect_system_prompt_skill_prompt_version', 'system_prompt_skill_prompt_versions'),
            ('trg_prevent_system_prompt_skill_prompt_version_delete', 'system_prompt_skill_prompt_versions')
        ) AS t(trigger_name, table_name)
    LOOP
        IF to_regclass(protected.table_name) IS NOT NULL THEN
            EXECUTE format('DROP TRIGGER IF EXISTS %I ON %I', protected.trigger_name, protected.table_name);
        END IF;
    END LOOP;
END;
$drop_protection$;

DROP TABLE IF EXISTS
    system_prompt_independent_contents,
    system_prompt_version_compat,
    system_prompt_independent_state,
    system_prompt_frozen_public_files,
    system_prompt_rule_policies,
    system_prompt_runtime,
    system_prompt_template_versions,
    system_prompt_templates,
    system_prompt_skill_sync_jobs,
    system_prompt_skill_runtime,
    system_prompt_skill_bundle_versions,
    system_prompt_skill_prompt_versions;

DROP FUNCTION IF EXISTS protect_system_prompt_frozen_content();
DROP FUNCTION IF EXISTS protect_system_prompt_version_content();
DROP FUNCTION IF EXISTS prevent_system_prompt_version_delete();
DROP FUNCTION IF EXISTS protect_system_prompt_template_managed_source();
DROP FUNCTION IF EXISTS protect_system_prompt_skill_bundle_version();
DROP FUNCTION IF EXISTS prevent_system_prompt_skill_bundle_version_delete();
DROP FUNCTION IF EXISTS protect_system_prompt_skill_prompt_version();
DROP FUNCTION IF EXISTS prevent_system_prompt_skill_prompt_version_delete();
