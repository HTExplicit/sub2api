import { apiClient } from '../client'
import { accountJobIdempotencyHeaders, type AccountJob } from './accountJobs'
import type { PluginResourceDescriptor } from '@/components/plugins/resourceClient'
import type { AccountResourceActionV1, AccountViewDefinitionV1, AccountViewIdentityV1 } from '@sub2api/plugin-ui/account-view'
import type { AccountCreateDefinitionV1 } from '@sub2api/plugin-ui/account-create'
import type { AccountEditDefinitionV1 } from '@sub2api/plugin-ui/account-edit'
import type { CapturedAccountView } from '@/composables/useAccountViewContext'
import { accountViewClient } from './accountViewClient'
import { pluginDispatchClient, pluginDispatchHeaders, type PluginDispatchContext } from './pluginDispatch'
export { pluginDispatchClient, pluginDispatchHeaders, type PluginDispatchContext } from './pluginDispatch'

declare module 'axios' {
  interface AxiosRequestConfig { rawPluginConfig?: boolean }
}

export interface PluginVersionSnapshot { revision: number; package_sha256: string }
export interface PluginConfigSnapshot extends PluginVersionSnapshot { config: Record<string, unknown> }
const revisionHeader = 'X-Sub2API-Plugin-Revision'
const packageHeader = 'X-Sub2API-Plugin-Package'

function packageHeaders(digest: string) {
  if (!/^[a-f0-9]{64}$/.test(digest)) throw new Error('Reload the plugin view before submitting this operation')
  return { [packageHeader]: digest }
}

function versionHeaders(snapshot: PluginVersionSnapshot) {
  if (!Number.isSafeInteger(snapshot.revision) || snapshot.revision <= 0) throw new Error('Reload the plugin configuration before submitting this operation')
  return { ...packageHeaders(snapshot.package_sha256), [revisionHeader]: String(snapshot.revision) }
}

function configSnapshot(data: Record<string, unknown>, headers: Record<string, unknown>): PluginConfigSnapshot {
  const revision = Number(headers[revisionHeader.toLowerCase()])
  const digest = String(headers[packageHeader.toLowerCase()] || '')
  versionHeaders({ revision, package_sha256: digest })
  if (!data || typeof data !== 'object' || Array.isArray(data)) throw new Error('Invalid plugin configuration response')
  return { config: data, revision, package_sha256: digest }
}

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
  plugin_key?: string
  account_edit?: AccountEditDefinitionV1
  edit_definition_digest?: string
  account_create?: AccountCreateDefinitionV1
  create_definition_digest?: string
  runtime_generation?: number
  view_definition_digest?: string
  account_view?: AccountViewDefinitionV1
  value_bindings?: Record<string, string>
  resource_action?: AccountResourceActionV1
  all_accounts?: boolean
  retained_controls?: boolean
  events?: string[]
  package_sha256?: string
  capability?: string
  stylesheet_url?: string
  config_flag?: string
  fields?: PluginFormField[]
  display_fields?: PluginDisplayField[]
  account_filter?: { platforms?: string[]; types?: string[]; statuses?: string[]; exclude_shadows?: boolean }
  account_scope?: { version: 1; bindings: Array<{ platform: string; account_type: string; rollout_percent: number }> }
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
  kind: 'select' | 'textarea' | 'text'
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

export async function invokeAdmin(id: number, operation: string, accountID: number | undefined, payload: Record<string, unknown>, expectedPackage: string, dispatch?: PluginDispatchContext): Promise<{ payload?: unknown; code?: string; message?: string }> {
  const { data } = await pluginDispatchClient(dispatch).post<{ payload?: unknown; code?: string; message?: string }>(`/admin/plugins/${id}/actions`, { operation, account_id: accountID, payload }, { timeout: 30000, headers: { ...packageHeaders(expectedPackage), ...pluginDispatchHeaders(dispatch) } })
  return data
}

export async function submitJob(id: number, operation: string, items: Array<{ account_id: number; payload: Record<string, unknown>; label?: string }>, expectedPackage: string, key?: string, dispatch?: PluginDispatchContext): Promise<AccountJob> {
  const config = key ? { headers: { 'Idempotency-Key': key } } : accountJobIdempotencyHeaders('plugin_operation')
  const { data } = await pluginDispatchClient(dispatch).post<AccountJob>(`/admin/plugins/${id}/jobs`, { operation, items }, { ...config, headers: { ...config.headers, ...packageHeaders(expectedPackage), ...pluginDispatchHeaders(dispatch) } })
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
  view_context?: AccountViewIdentityV1
  request_binding?: string
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
  plugin: PluginVersionSnapshot & { id: number },
  rolloutPercent: number,
  acceptUntested: boolean
): Promise<PluginInstallation> {
  const { data } = await apiClient.post<PluginInstallation>(`/admin/plugins/${plugin.id}/enable`, {
    rollout_percent: rolloutPercent,
    accept_untested: acceptUntested
  }, { headers: versionHeaders(plugin) })
  changed()
  return data
}

export async function disable(plugin: PluginVersionSnapshot & { id: number }): Promise<PluginInstallation> {
  const { data } = await apiClient.post<PluginInstallation>(`/admin/plugins/${plugin.id}/disable`, undefined, { headers: versionHeaders(plugin) })
  changed()
  return data
}

export async function remove(plugin: PluginVersionSnapshot & { id: number }): Promise<void> {
  await apiClient.delete(`/admin/plugins/${plugin.id}`, { headers: versionHeaders(plugin) })
  changed()
}

export async function getConfig(id: number, expectedPackage: string): Promise<PluginConfigSnapshot> {
  const { data, headers } = await apiClient.get<Record<string, unknown>>(`/admin/plugins/${id}/config`, { rawPluginConfig: true, headers: packageHeaders(expectedPackage) })
  const snapshot = configSnapshot(data, headers)
  if (snapshot.package_sha256 !== expectedPackage) throw new Error('Plugin package changed; reload the view')
  return snapshot
}

export async function saveConfig(
  id: number,
  config: Record<string, unknown>,
  expected: PluginVersionSnapshot
): Promise<PluginConfigSnapshot> {
  const { data, headers } = await apiClient.put<Record<string, unknown>>(`/admin/plugins/${id}/config`, config, { rawPluginConfig: true, headers: versionHeaders(expected) })
  const snapshot = configSnapshot(data, headers)
  if (snapshot.package_sha256 !== expected.package_sha256 || snapshot.revision < expected.revision || snapshot.revision > expected.revision + 1) throw new Error('Plugin save receipt does not match the submitted version')
  changed()
  return snapshot
}

export async function test(plugin: PluginVersionSnapshot & { id: number }): Promise<PluginTestResult> {
  const { data } = await apiClient.post<PluginTestResult>(`/admin/plugins/${plugin.id}/test`, undefined, { headers: versionHeaders(plugin) })
  return data
}

export async function status(id: number): Promise<PluginStatusResult> {
  const { data } = await apiClient.get<PluginStatusResult>(`/admin/plugins/${id}/status`)
  return data
}

export async function createUISession(id: number, contributionID?: string, view?: CapturedAccountView): Promise<PluginUISession> {
  const { data } = await accountViewClient(view).post<PluginUISession>(`/admin/plugins/${id}/ui-session`, { contribution_id: contributionID })
  return data
}

export async function createUserUISession(id: number, contributionID?: string): Promise<PluginUISession> {
  const { data } = await apiClient.post<PluginUISession>(`/plugins/${id}/ui-session`, { contribution_id: contributionID })
  return data
}

export async function resources(id: number, permission: 'admin' | 'user' = 'admin', dispatch?: PluginDispatchContext): Promise<PluginResourceDescriptor[]> {
  const prefix = permission === 'admin' ? '/admin' : ''
  const { data } = await pluginDispatchClient(dispatch).get<PluginResourceDescriptor[]>(`${prefix}/plugins/${id}/resources`, { headers: pluginDispatchHeaders(dispatch) })
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
