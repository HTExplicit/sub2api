import { apiClient } from '../client'

export type PromptDelivery = 'native_control' | 'system' | 'developer'
export type PromptPosition = 'control_prepend' | 'control_append' | 'conversation_head' | 'conversation_tail'
export interface PromptRule {
  id: string
  name: string
  enabled: boolean
  template_id: number
  version_id: number
  follow_active?: boolean
  order: number
  delivery: PromptDelivery
  position: PromptPosition
  model_match: 'requested' | 'upstream'
  models: string[]
}
export interface PromptRulePolicy { version: number; rules: PromptRule[]; default_rule_ids: string[] }
export interface PromptRuleState { policy: PromptRulePolicy; revision: number; enabled: boolean; compact_enabled: boolean }
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
      placements: Array<{ rule_id: string; version_id: number; delivery: string; position: string; carrier: string; role?: string; body: string; sha256: string }>
      skipped: Array<{ rule_id: string; reason: string }>
    }
  }
}
export const legalPromptPosition = (delivery: PromptDelivery, position: PromptPosition) => delivery !== 'native_control' || position.startsWith('control_')
const basePath = '/admin/system-prompts'

export const rulesAPI = {
  read: async () => (await apiClient.get<PromptRuleState>(`${basePath}/rules`)).data,
  save: async (policy: PromptRulePolicy, revision: number) =>
    (await apiClient.put<PromptRuleState>(`${basePath}/rules`, { policy, expected_revision: revision })).data,
  accounts: async (ids: number[]) =>
    (await apiClient.post<PromptAccountBinding[]>(`${basePath}/accounts/resolve`, { account_ids: ids })).data,
  bind: async (updates: Array<{ account_id: number; expected_updated_at: string; binding: PromptBinding }>, revision: number) =>
    (await apiClient.post<PromptBindingResult[]>(`${basePath}/accounts/bindings`, { updates, expected_revision: revision })).data,
  preview: async (accountId: number, body: { protocol: string; transport: string; compact: boolean; body: unknown; policy?: PromptRulePolicy; binding?: PromptBinding; simulate_enabled?: boolean }) =>
    (await apiClient.post<PromptPreview>(`${basePath}/rules/preview/${encodeURIComponent(String(accountId))}`, body)).data,
}
