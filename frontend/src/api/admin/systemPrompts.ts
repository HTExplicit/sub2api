import { apiClient } from '../client'

export type SystemPromptPosition = 'prepend' | 'append'
export type SystemPromptRole = 'auto' | 'system' | 'developer'
export type SystemPromptBindingMode = 'inherit' | 'off' | 'custom'

export interface SystemPrompt {
  id: string
  name: string
  body: string
  position: SystemPromptPosition
  role: SystemPromptRole
}

export interface SystemPromptConfig {
  enabled: boolean
  default_prompt_id: string
  prompts: SystemPrompt[]
}

export interface SystemPromptUsage {
  inherit: number
  off: number
  custom: Record<string, number>
}

export interface SystemPromptState extends SystemPromptConfig {
  usage: SystemPromptUsage
}

export interface SystemPromptBinding {
  mode: SystemPromptBindingMode
  prompt_id?: string
}

export const systemPromptMaxBodyBytes = 64 * 1024

const basePath = '/admin/system-prompts'

export const systemPromptsAPI = {
  get: async () => (await apiClient.get<SystemPromptState>(basePath)).data,
  save: async (config: SystemPromptConfig) => (await apiClient.put<SystemPromptState>(basePath, config)).data,
  setBindings: async (accountIds: number[], binding: SystemPromptBinding) =>
    (await apiClient.put<{ updated: number }>(`${basePath}/bindings`, { account_ids: accountIds, ...binding })).data
}

export default systemPromptsAPI
