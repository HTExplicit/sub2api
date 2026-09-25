import type { PromptCapability, PromptConfig, PromptPosition, PromptRole } from '@/api/admin/systemPromptRules'

const positions: PromptPosition[] = ['control_prepend', 'control_append', 'conversation_head', 'conversation_tail', 'before_last_user', 'after_last_user']
const roles: PromptRole[] = ['auto', 'system', 'developer']
export const capabilities: PromptCapability[] = [
  { protocol: 'responses', platforms: ['openai', 'cindy'], roles, positions_by_role: Object.fromEntries(roles.map(role => [role, positions])), limitations: ['oauth_system_unsupported'] },
  { protocol: 'chat', platforms: ['openai', 'cindy', 'grok', 'kimi', 'zhipu', 'deepseek', 'minimax', 'opencode_go'], roles, positions_by_role: Object.fromEntries(roles.map(role => [role, positions])), limitations: [] },
  { protocol: 'messages', platforms: ['anthropic', 'antigravity', 'kimi', 'zhipu', 'deepseek', 'minimax', 'opencode_go'], roles: ['auto', 'system'], positions_by_role: { auto: ['control_prepend', 'control_append', 'conversation_tail', 'after_last_user'], system: ['control_prepend', 'control_append', 'conversation_tail', 'after_last_user'] }, limitations: [], conversation_system_models: ['claude-opus-5'] },
  { protocol: 'gemini', platforms: ['gemini', 'antigravity'], roles: ['auto', 'system'], positions_by_role: { auto: ['control_prepend', 'control_append'], system: ['control_prepend', 'control_append'] }, limitations: [] },
]
export function promptConfig(): PromptConfig {
  return {
    revision: 7, enabled: true, expose_server_prompt: false, compact_enabled: false,
    policy: { version: 2, default_rule_ids: ['first'], rules: [
      { id: 'first', name: 'First prompt', enabled: true, template_id: 1, version_id: 10, order: 100, role: 'auto', position: 'control_append', platforms: ['openai'], model_match: 'upstream', models: [] },
      { id: 'second', name: 'Second prompt', enabled: true, template_id: 2, version_id: 20, order: 200, role: 'auto', position: 'control_append', platforms: ['gemini'], model_match: 'upstream', models: [] },
    ] },
    contents: {
      first: { body: 'First body', template_id: 1, version_id: 10, composition_mode: 'inline', managed: false },
      second: { body: 'Second body', template_id: 2, version_id: 20, composition_mode: 'inline', managed: false },
    },
    capabilities,
  }
}
export const template = { id: 1, slug: 'first', name: 'First source', description: '', managed_source: '', is_seed: false, created_at: '', updated_at: '' }
export const version = { id: 9, template_id: 1, version: 1, body: 'Historical body', sha256: 'abc', byte_length: 15, note: 'Original note', composition_mode: 'inline', bundle_id: '', bundle_manifest_sha256: '', created_at: '' }
