import { apiClient } from '../client'

export interface CodexRuntimeSettings {
  request_zstd: boolean
}

export const codexRuntimeAPI = {
  async getSettings(): Promise<CodexRuntimeSettings> {
    const { data } = await apiClient.get<CodexRuntimeSettings>('/admin/settings/codex-runtime')
    return data
  },
  async updateSettings(settings: CodexRuntimeSettings): Promise<CodexRuntimeSettings> {
    const { data } = await apiClient.put<CodexRuntimeSettings>('/admin/settings/codex-runtime', settings)
    return data
  },
  async diagnostics(errorId: number, signal?: AbortSignal): Promise<unknown> {
    return (await apiClient.get(`/admin/ops/errors/${errorId}/diagnostics`, { signal })).data
  }
}
