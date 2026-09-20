import { apiClient } from '../client'
import { accountJobIdempotencyHeaders, type AccountJob } from './accountJobs'
import type { PluginResourceDescriptor } from '@/components/plugins/resourceClient'

export interface PluginCapability {
  id: string
  platform: string
  account_type: string
}

export interface PluginRequirements {
	extension_api?: number
  sub2api: string
  recommended_sub2api_version?: string
  tested_sub2api_versions?: string[]
  plugin_protocol: number
  transport_api: number
  ui_bridge: number
}

export interface PluginManifest {
  source_revision?: string
	dependencies?: Array<{ capability: string; platform?: string; account_type?: string }>
	contributions?: Array<Omit<PluginContribution, 'plugin_id' | 'available' | 'reason'>>
  schema_version: number
  id: string
  name: string
  version: string
  description?: string
  author?: string
  requires: PluginRequirements
  capabilities: PluginCapability[]
  ui: { entrypoint: string }
}

export interface PluginCompatibility {
  compatible: boolean
  tested: boolean
  status: 'compatible' | 'untested' | 'incompatible'
  message: string
  current_sub2api_version: string
  required_sub2api_version: string
  recommended_sub2api_version: string
  plugin_protocol: number
  transport_api: number
  ui_bridge: number
}

export interface PluginBinding {
  id: number
  plugin_id: number
  capability: string
  platform: string
  account_type: string
  enabled: boolean
  rollout_percent: number
}

export interface PluginInstallation {
	 revision: number
	 package_sha256: string
	 update_policy: 'bundled' | 'pinned'
	desired_enabled?: boolean
  id: number
  plugin_key: string
  name: string
  version: string
  description: string
  author: string
  manifest: PluginManifest
  binary_sha256: string
  signature_status: 'trusted' | 'unsigned'
  state: 'disabled' | 'starting' | 'enabled' | 'error' | 'incompatible' | 'updating'
  last_error: string
  installed_at: string
  enabled_at?: string
  updated_at: string
  bindings: PluginBinding[]
  compatibility: PluginCompatibility
  runtime_healthy: boolean
  runtime_message: string
}

export interface PluginContribution {
  retained_controls?: boolean
  events?: string[]
  package_sha256?: string
  capability?: string
  stylesheet_url?: string
  config_flag?: string
  fields?: PluginFormField[]
  display_fields?: PluginDisplayField[]
  account_filter?: { platforms?: string[]; types?: string[]; statuses?: string[]; exclude_shadows?: boolean }
  id: string
  slot: string
  label: Record<string, string>
  permission: string
  action?: string
  entrypoint?: string
  order?: number
  plugin_id: number
  available: boolean
  reason?: string
}

export interface PluginDisplayField {
  key: string
  kind: 'text' | 'badge' | 'datetime'
  prefix?: string
  new_line?: boolean
  values?: Record<string, { label: Record<string, string>; tone?: 'neutral' | 'success' | 'warning' | 'danger' | 'info' }>
}

export interface PluginFormField {
  key: string
  kind: 'select' | 'textarea'
  label: Record<string, string>
  options_source?: string
  default_label?: Record<string, string>
  default_source?: string
  placeholder?: string
  rows?: number
  max_length?: number
  hint?: Record<string, string>
  reset_label?: Record<string, string>
  limit_message?: Record<string, string>
}

export async function contributions(): Promise<PluginContribution[]> {
  const { data } = await apiClient.get<PluginContribution[]>('/admin/plugins/contributions')
  return data || []
}

export async function publicContributions(): Promise<PluginContribution[]> {
  const { data } = await apiClient.get<PluginContribution[]>('/settings/plugins', { timeout: 5000 })
  return data || []
}

export async function invokeAdmin(id: number, operation: string, accountID: number | undefined, payload: Record<string, unknown>): Promise<{ payload?: unknown; code?: string; message?: string }> {
  const { data } = await apiClient.post(`/admin/plugins/${id}/actions`, { operation, account_id: accountID, payload }, { timeout: 30000 })
  return data
}

export async function submitJob(id: number, operation: string, items: Array<{ account_id: number; payload: Record<string, unknown>; label?: string }>, key?: string): Promise<AccountJob> {
  const config = key ? { headers: { 'Idempotency-Key': key } } : accountJobIdempotencyHeaders('plugin_operation')
  const { data } = await apiClient.post<AccountJob>(`/admin/plugins/${id}/jobs`, { operation, items }, config)
  return data
}

export interface PluginTestResult {
  success: boolean
  message: string
  latency_ms: number
  status_json?: string
}

export interface PluginStatusResult {
  healthy: boolean
  message: string
  status_json?: string
}

export interface PluginUISession {
  plugin_key?: string
  package_sha256?: string
  permission?: 'admin' | 'user'
  url: string
  bridge_token: string
  ui_bridge_version: number
  expires_at: string
}

export async function list(): Promise<PluginInstallation[]> {
  const { data } = await apiClient.get<PluginInstallation[]>('/admin/plugins')
  return data
}

export async function upload(file: File): Promise<PluginInstallation> {
  const form = new FormData()
  form.append('plugin', file)
  const { data } = await apiClient.post<PluginInstallation>('/admin/plugins/upload', form, {
    headers: { 'Content-Type': 'multipart/form-data' },
    timeout: 120000
  })
  return data
}

export async function update(plugin: PluginInstallation, file: File): Promise<PluginInstallation> {
  const form = new FormData()
  form.append('plugin', file)
  form.append('expected_revision', String(plugin.revision))
  form.append('expected_package_sha256', plugin.package_sha256)
  const { data } = await apiClient.post<PluginInstallation>(`/admin/plugins/${plugin.id}/update`, form, {
    headers: { 'Content-Type': 'multipart/form-data' }, timeout: 120000
  })
  changed()
  return data
}

export async function followBundled(plugin: PluginInstallation): Promise<PluginInstallation> {
  const { data } = await apiClient.post<PluginInstallation>(`/admin/plugins/${plugin.id}/follow-bundled`, {
    expected_revision: plugin.revision, expected_package_sha256: plugin.package_sha256
  }, { timeout: 120000 })
  changed()
  return data
}

export async function enable(
  id: number,
  rolloutPercent: number,
  acceptUntested: boolean
): Promise<PluginInstallation> {
  const { data } = await apiClient.post<PluginInstallation>(`/admin/plugins/${id}/enable`, {
    rollout_percent: rolloutPercent,
    accept_untested: acceptUntested
  })
  changed()
  return data
}

export async function disable(id: number): Promise<PluginInstallation> {
  const { data } = await apiClient.post<PluginInstallation>(`/admin/plugins/${id}/disable`)
  changed()
  return data
}

export async function remove(id: number): Promise<void> {
  await apiClient.delete(`/admin/plugins/${id}`)
  changed()
}

export async function getConfig(id: number): Promise<Record<string, unknown>> {
  const { data } = await apiClient.get<Record<string, unknown>>(`/admin/plugins/${id}/config`)
  return data
}

export async function saveConfig(
  id: number,
  config: Record<string, unknown>
): Promise<Record<string, unknown>> {
  const { data } = await apiClient.put<Record<string, unknown>>(`/admin/plugins/${id}/config`, config)
  changed()
  return data
}

export async function test(id: number): Promise<PluginTestResult> {
  const { data } = await apiClient.post<PluginTestResult>(`/admin/plugins/${id}/test`)
  return data
}

export async function status(id: number): Promise<PluginStatusResult> {
  const { data } = await apiClient.get<PluginStatusResult>(`/admin/plugins/${id}/status`)
  return data
}

export async function createUISession(id: number, contributionID?: string): Promise<PluginUISession> {
  const { data } = await apiClient.post<PluginUISession>(`/admin/plugins/${id}/ui-session`, { contribution_id: contributionID })
  return data
}

export async function createUserUISession(id: number, contributionID?: string): Promise<PluginUISession> {
  const { data } = await apiClient.post<PluginUISession>(`/plugins/${id}/ui-session`, { contribution_id: contributionID })
  return data
}

export async function resources(id: number, permission: 'admin' | 'user' = 'admin'): Promise<PluginResourceDescriptor[]> {
  const prefix = permission === 'admin' ? '/admin' : ''
  const { data } = await apiClient.get<PluginResourceDescriptor[]>(`${prefix}/plugins/${id}/resources`)
  return data
}

export default {
  contributions,
  invokeAdmin,
  submitJob,
  list,
  upload,
  update,
  followBundled,
  enable,
  disable,
  remove,
  getConfig,
  saveConfig,
  test,
  status,
  createUISession,
  createUserUISession,
  resources
}

function changed() { if (typeof window !== 'undefined') window.dispatchEvent(new Event('sub2api:plugins-changed')) }
