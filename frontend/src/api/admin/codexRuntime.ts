import { apiClient } from '../client'

export interface CodexRuntimeConfig extends Record<string, unknown> {
  enabled?: boolean
  request_zstd?: boolean
  proxy_url?: string
  proxy_protocol?: string
  proxy_selection_id?: string
}

export const codexRuntimeAPI = {
  async getConfig(): Promise<CodexRuntimeConfig> {
    return (await apiClient.get<CodexRuntimeConfig>('/admin/settings/codex-runtime', { rawPluginConfig: true })).data
  },
  async saveConfig(config: CodexRuntimeConfig): Promise<CodexRuntimeConfig> {
    return (await apiClient.put<CodexRuntimeConfig>('/admin/settings/codex-runtime', config, { rawPluginConfig: true })).data
  },
  async diagnostics(errorId: number, signal?: AbortSignal): Promise<unknown> {
    return (await apiClient.get(`/admin/ops/errors/${errorId}/diagnostics`, { signal })).data
  }
}
