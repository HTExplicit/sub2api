-- Rule definitions share the existing runtime revision transaction. Content
-- remains in immutable template versions; account.extra stores references only.
CREATE TABLE IF NOT EXISTS system_prompt_rule_policies (
    id SMALLINT PRIMARY KEY REFERENCES system_prompt_runtime(id) ON DELETE RESTRICT CHECK (id = 1),
    policy JSONB NOT NULL CHECK (jsonb_typeof(policy) = 'object')
);
INSERT INTO system_prompt_rule_policies (id, policy)
SELECT 1, jsonb_build_object(
    'version', 1,
    'default_rule_ids', jsonb_build_array('legacy-default'),
    'rules', jsonb_build_array(jsonb_build_object(
        'id', 'legacy-default', 'name', 'Default / 默认规则', 'enabled', true,
        'template_id', COALESCE(active_template_id, 0), 'version_id', COALESCE(active_version_id, 0),
        'follow_active', true, 'order', 100, 'delivery', 'native_control',
        'position', 'control_append', 'model_match', 'upstream', 'models', '[]'::jsonb
    ))
) FROM system_prompt_runtime WHERE id = 1
ON CONFLICT (id) DO NOTHING;
