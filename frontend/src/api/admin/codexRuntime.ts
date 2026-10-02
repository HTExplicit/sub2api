import { apiClient } from '../client'

// Native Codex runtime settings. The server defines only request_zstd; other keys it may
// return are tolerated when read and never sent back.
export interface CodexRuntimeConfig extends Record<string, unknown> {
  request_zstd?: boolean
}

export interface CodexRuntimeSettingsUpdate {
  request_zstd: boolean
}

export const codexRuntimeAPI = {
  async getConfig(): Promise<CodexRuntimeConfig> {
    return (await apiClient.get<CodexRuntimeConfig>('/admin/settings/codex-runtime', { rawPluginConfig: true })).data
  },
  async saveConfig(config: CodexRuntimeSettingsUpdate): Promise<CodexRuntimeConfig> {
    return (await apiClient.put<CodexRuntimeConfig>('/admin/settings/codex-runtime', config, { rawPluginConfig: true })).data
  },
  async diagnostics(errorId: number, signal?: AbortSignal): Promise<unknown> {
    return (await apiClient.get(`/admin/ops/errors/${errorId}/diagnostics`, { signal })).data
  }
}
