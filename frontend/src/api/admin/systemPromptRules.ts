import { apiClient } from '../client'

export type SystemPromptCompositionMode = 'inline' | 'anthropic_system_blocks' | 'codex_skill_hybrid'

export type PromptRole = 'auto' | 'system' | 'developer'
export type PromptPosition = 'control_prepend' | 'control_append' | 'conversation_head' | 'conversation_tail' | 'before_last_user' | 'after_last_user'
export interface PromptRule {
  id: string
  name: string
  enabled: boolean
  template_id: number
  version_id: number
  order: number
  role: PromptRole
  position: PromptPosition
  platforms: string[]
  account_types?: string[]
  exclude_model_contains?: string[]
  request_profiles?: string[]
  model_match: 'requested' | 'upstream'
  models: string[]
}
export interface PromptRulePolicy { version: number; rules: PromptRule[]; default_rule_ids: string[] }
export interface PromptRuleState { policy: PromptRulePolicy; revision: number; enabled: boolean; compact_enabled: boolean }
export interface PromptContent {
  body: string
  available?: boolean
  composition_mode: SystemPromptCompositionMode
  managed: boolean
  template_id: number
  version_id: number
  restore_version_id?: number
}
export interface PromptHistoryVersion { id: number; body: string; composition_mode: SystemPromptCompositionMode; created_at: string; restorable: boolean }
export interface PromptCapability {
  protocol: string
  platforms: string[]
  roles: PromptRole[]
  positions_by_role: Partial<Record<PromptRole, PromptPosition[]>>
  limitations: string[]
  conversation_system_models?: string[]
}
export interface PromptConfig extends PromptRuleState {
  expose_server_prompt: boolean
  contents: Record<string, PromptContent>
  capabilities: PromptCapability[]
}
export interface PromptConfigWrite {
  expected_revision: number
  enabled: boolean
  expose_server_prompt?: boolean
  compact_enabled?: boolean
  policy: PromptRulePolicy
  contents: Record<string, { body: string; restore_version_id?: number }>
}
export interface PromptBinding { mode: 'inherit' | 'off' | 'custom'; rule_ids: string[] }
export interface PromptAccountBinding {
  account_id: number; name: string; platform: string; account_type: string; updated_at: string
  binding: PromptBinding; effective_rule_ids: string[]; supported: boolean
}
export interface PromptBindingResult { account_id: number; applied: boolean; code?: string }
function scopedPromptCapabilities(platform: string, position: PromptPosition, capabilities: PromptCapability[], accountTypes: string[]): PromptCapability[] {
  const protocols = capabilities.filter(capability => capability.platforms.includes(platform))
  if (platform !== 'antigravity') return protocols
  // Antigravity upstream accounts forward Messages; OAuth uses Gemini. Match
  // the server's save-time scope selection and leave broad scopes to preview.
  const usesMessages = (accountTypes.length === 1 && accountTypes[0] === 'upstream') || (!accountTypes.length && !position.startsWith('control_'))
  return protocols.filter(capability => capability.protocol === (usesMessages ? 'messages' : 'gemini'))
}
export function legalPromptPosition(role: PromptRole, position: PromptPosition, platforms: string[], capabilities: PromptCapability[], accountTypes: string[] = []): boolean {
  if (!platforms.length) return false
  return platforms.every(platform => {
    const protocols = scopedPromptCapabilities(platform, position, capabilities, accountTypes)
    return protocols.some(capability => capability.positions_by_role[role]?.includes(position))
  })
}
export function legalPromptModelScope(rule: PromptRule, capabilities: PromptCapability[]): boolean {
  if (rule.position.startsWith('control_')) return true
  return rule.platforms.every(platform => scopedPromptCapabilities(platform, rule.position, capabilities, rule.account_types || []).some(capability => {
    if (!capability.positions_by_role[rule.role]?.includes(rule.position)) return false
    if (!capability.conversation_system_models?.length) return true
    return rule.model_match === 'upstream' && rule.models.length > 0 && rule.models.every(model => capability.conversation_system_models!.includes(model))
  }))
}
const basePath = '/admin/system-prompts'

export const rulesAPI = {
  config: async () => (await apiClient.get<PromptConfig>(`${basePath}/config`)).data,
  saveConfig: async (payload: PromptConfigWrite) => (await apiClient.put<PromptConfig>(`${basePath}/config`, payload)).data,
  history: async (ruleID: string) => (await apiClient.get<PromptHistoryVersion[]>(`${basePath}/rules/${encodeURIComponent(ruleID)}/history`)).data,
  read: async () => (await apiClient.get<PromptRuleState>(`${basePath}/rules`)).data,
  accounts: async (ids: number[]) =>
    (await apiClient.post<PromptAccountBinding[]>(`${basePath}/accounts/resolve`, { account_ids: ids })).data,
  bind: async (updates: Array<{ account_id: number; expected_updated_at: string; binding: PromptBinding }>, revision: number) =>
    (await apiClient.post<PromptBindingResult[]>(`${basePath}/accounts/bindings`, { updates, expected_revision: revision })).data,
}
