import { apiClient } from '../client'

export type CodexFingerprintMode = 'off' | 'device' | 'session' | 'full'

export interface CodexFingerprintSettings {
  enabled: boolean
  user_agent: string
  client_version: string
  version_auto_sync_enabled: boolean
}

export interface CodexFingerprintSettingsView extends CodexFingerprintSettings {
  synced_version: string
  effective_version: string
  effective_user_agent: string
}

export interface CodexFingerprintAccountView {
  mode: CodexFingerprintMode
  simulation_enabled: boolean
  identity: {
    user_agent: string
    originator: string
    version: string
    identity_source: string
    identity_account_id: number
    identity_persisted: boolean
    fingerprint_mode_configured: string
    fingerprint_mode_effective: CodexFingerprintMode
    fingerprint_reason: string
  }
  device_identity: {
    v: number
    os_type: string
    os_version: string
    arch: string
    terminal: string
    sandbox: string
    generated_at?: string
  } | null
  effective_device: {
    os_type: string | null
    os_version: string | null
    arch: string | null
    terminal: string | null
    platform_sandbox: string | null
  } | null
  identifier_policy: Record<'installation_id' | 'session_id' | 'thread_id' | 'parent_thread_id' | 'window_id', {
    behavior: 'fixed' | 'request_derived' | 'passthrough'
    rule: string
    value: string | null
  }>
}

export const codexFingerprintAPI = {
  async getSettings(): Promise<CodexFingerprintSettingsView> {
    const { data } = await apiClient.get<CodexFingerprintSettingsView>('/admin/settings/codex-fingerprint')
    if (typeof data.enabled !== 'boolean' || typeof data.user_agent !== 'string' || typeof data.client_version !== 'string' ||
      typeof data.version_auto_sync_enabled !== 'boolean' || typeof data.effective_version !== 'string') throw new Error('Invalid Codex fingerprint settings')
    return data
  },
  async saveSettings(config: CodexFingerprintSettings): Promise<CodexFingerprintSettingsView> {
    const { data } = await apiClient.put<CodexFingerprintSettingsView>('/admin/settings/codex-fingerprint', config)
    return data
  },
  async getAccount(id: number, options?: { signal?: AbortSignal }): Promise<CodexFingerprintAccountView> {
    const { data } = await apiClient.get<CodexFingerprintAccountView>(`/admin/accounts/${id}/codex-fingerprint`, options)
    return data
  },
  async saveAccount(id: number, mode: CodexFingerprintMode): Promise<CodexFingerprintAccountView> {
    const { data } = await apiClient.put<CodexFingerprintAccountView>(`/admin/accounts/${id}/codex-fingerprint`, { mode })
    return data
  }
}
