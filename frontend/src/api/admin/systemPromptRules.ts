import { apiClient } from '../client'

import type { SystemPromptCompositionMode } from './systemPrompts'

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
}
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
  expose_server_prompt: boolean
  compact_enabled: boolean
  policy: PromptRulePolicy
  contents: Record<string, { body: string }>
}
export interface PromptBinding { mode: 'inherit' | 'off' | 'custom'; rule_ids: string[] }
export interface PromptAccountBinding {
  account_id: number; name: string; platform: string; account_type: string; updated_at: string
  binding: PromptBinding; effective_rule_ids: string[]; supported: boolean
}
export interface PromptBindingResult { account_id: number; applied: boolean; code?: string }
export interface PromptPreview {
  account_id: number; requested_model: string; upstream_model: string; protocol: string; transport: string
  body: unknown; before_rules: unknown; client_control: unknown; gateway_base_instructions: string
  continuation: boolean; sequence_scope: string; simulated: boolean; wire_verified: boolean
  application: {
    applied: boolean; revision: number; rules_plan?: {
      sha256: string
      placements: Array<{ rule_id: string; version_id: number; protocol: string; position: string; carrier: string; role?: string; index?: number; block_index?: number; body: string; sha256: string }>
      skipped: Array<{ rule_id: string; reason: string }>
    }
  }
}
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
  read: async () => (await apiClient.get<PromptRuleState>(`${basePath}/rules`)).data,
  save: async (policy: PromptRulePolicy, revision: number) =>
    (await apiClient.put<PromptRuleState>(`${basePath}/rules`, { policy, expected_revision: revision })).data,
  accounts: async (ids: number[]) =>
    (await apiClient.post<PromptAccountBinding[]>(`${basePath}/accounts/resolve`, { account_ids: ids })).data,
  bind: async (updates: Array<{ account_id: number; expected_updated_at: string; binding: PromptBinding }>, revision: number) =>
    (await apiClient.post<PromptBindingResult[]>(`${basePath}/accounts/bindings`, { updates, expected_revision: revision })).data,
  preview: async (accountId: number, body: { protocol: string; transport: string; compact: boolean; body: unknown; policy?: PromptRulePolicy; contents?: Record<string, { body: string }>; binding?: PromptBinding; simulate_enabled?: boolean }) =>
    (await apiClient.post<PromptPreview>(`${basePath}/rules/preview/${encodeURIComponent(String(accountId))}`, body)).data,
}
