import { apiClient } from '../client'

export interface ReasoningRecoveryConfig {
  enabled: boolean
}

const basePath = '/admin/reasoning-recovery'

export const reasoningRecoveryAPI = {
  get: async () => (await apiClient.get<ReasoningRecoveryConfig>(basePath)).data,
  save: async (config: ReasoningRecoveryConfig) => (await apiClient.put<ReasoningRecoveryConfig>(basePath, config)).data
}

export default reasoningRecoveryAPI
