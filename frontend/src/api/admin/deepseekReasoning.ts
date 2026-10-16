import { apiClient } from '../client'

export interface DeepSeekReasoningConfig {
  enabled: boolean
  effort: 'low' | 'high' | 'max'
}

const path = '/admin/deepseek-reasoning'
export const deepseekReasoningAPI = {
  get: async () => (await apiClient.get<DeepSeekReasoningConfig>(path)).data,
  save: async (config: DeepSeekReasoningConfig) =>
    (await apiClient.put<DeepSeekReasoningConfig>(path, config)).data
}
