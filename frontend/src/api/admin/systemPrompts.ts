import { apiClient } from '../client'

export type SystemPromptCompositionMode = 'inline' | 'offline_bundle' | 'remote_skill' | 'codex_skill_hybrid'
export type SystemPromptClientMode = 'codex' | 'openai_compatible'

export interface SystemPromptRuntime {
  enabled: boolean
  expose_server_prompt: boolean
  compact_enabled: boolean
  template_id: number
  version_id: number
  template_version: number
  revision: number
  sha256: string
  byte_length: number
  degraded: boolean
  composition_mode: SystemPromptCompositionMode
  bundle_id: string
  bundle_manifest_sha256: string
  registry_revision?: number
  registry_manifest_sha256?: string
  registry_archive_sha256?: string
  registry_source_commit?: string
  bundle_available: boolean
  bundle_degraded: boolean
  degraded_reason?: string
  updated_at: string
}

export interface SystemPromptTemplate {
  id: number
  slug: string
  name: string
  description: string
  is_seed: boolean
  managed_source?: string
  created_by?: number
  updated_by?: number
  created_at: string
  updated_at: string
}

export interface SystemPromptVersion {
  id: number
  template_id: number
  version: number
  body: string
  sha256: string
  byte_length: number
  note: string
  composition_mode: SystemPromptCompositionMode
  bundle_id: string
  bundle_manifest_sha256: string
  created_by?: number
  published_at?: string
  published_by?: number
  created_at: string
  is_active: boolean
  source_repository?: string
  source_commit?: string
  source_version?: string
  source_artifact?: string
  source_artifact_sha256?: string
  source_license_sha256?: string
}

export interface SystemPromptBundleDocument {
  path: string
  sha256: string
  byte_length: number
  kind: 'text' | 'binary' | 'script'
  required?: boolean
}

export interface SystemPromptBundleRoute {
  id: string
  keywords: string[]
  entry: string
  references: string[]
  priority?: number
}

export interface SystemPromptBundleSummary {
  bundle_id: string
  name?: string
  description?: string
  version?: string
  manifest_sha256: string
  available: boolean
  degraded: boolean
  degraded_reason?: string
  document_count: number
  route_count: number
  total_bytes: number
  loaded_at?: string
}

export interface SystemPromptBundleDetail extends SystemPromptBundleSummary {
  documents: SystemPromptBundleDocument[]
  routes: SystemPromptBundleRoute[]
}

export interface RemoteSkillBundleVersion {
  id: number
  bundle_id: string
  source_commit: string
  overlay_sha256: string
  manifest_sha256: string
  archive_sha256: string
  file_count: number
  total_bytes: number
  added_files: number
  modified_files: number
  deleted_files: number
  script_changes: number
  binary_changes: number
  created_by?: number
  published_at?: string
  published_by?: number
  created_at: string
}

export interface RemoteSkillBundleVersionDetail extends RemoteSkillBundleVersion {
  verified: boolean
  routing_warnings?: string[]
}

export interface RemoteSkillRegistrySnapshot {
  revision: number
  active?: RemoteSkillBundleVersion
  degraded: boolean
  degraded_reason?: string
  updated_at: string
}

export interface RemoteSkillRegistryResponse {
  runtime: RemoteSkillRegistrySnapshot
  versions: RemoteSkillBundleVersion[]
  client_install: RemoteSkillClientInstall
}

export interface RemoteSkillClientInstaller {
  strategy: string
  bootstrap_url: string
  bootstrap_sha256: string
  acquire_command: string
  execute_command: string
}

export interface RemoteSkillClientInstall {
  skill_name: string
  source_commit?: string
  manifest_sha256?: string
  descriptor_url: string
  powershell: RemoteSkillClientInstaller
  python: RemoteSkillClientInstaller
}

export interface RemoteSkillSyncJob {
  id: number
  status: 'queued' | 'running' | 'succeeded' | 'failed'
  progress_stage: string
  source_commit?: string
  candidate_bundle_version_id?: number
  error_code?: string
  created_at: string
  started_at?: string
  completed_at?: string
}

export type ManagedSourceSyncStatus = 'up_to_date' | 'no_prompt_change' | 'candidate_created'

export interface ManagedSourceSyncVersion {
  id: number
  template_id: number
  version: number
  sha256: string
  byte_length: number
  source_repository?: string
  source_commit?: string
  source_version?: string
  source_artifact?: string
  source_artifact_sha256?: string
  source_license_sha256?: string
}

export interface ManagedSourceSyncResponse {
  status: ManagedSourceSyncStatus
  version?: ManagedSourceSyncVersion
}

export interface SystemPromptListResponse {
  templates: SystemPromptTemplate[]
  runtime: SystemPromptRuntime
}

export interface SystemPromptDetailResponse {
  template: SystemPromptTemplate
  versions: SystemPromptVersion[]
  runtime: SystemPromptRuntime
}

export interface CreateSystemPromptRequest {
  slug: string
  name: string
  description: string
  body: string
  note: string
  composition_mode?: SystemPromptCompositionMode
  bundle_id?: string
  bundle_manifest_sha256?: string
  expected_revision: number
}

export interface PreviewUpstreamResponse {
  body: unknown
  client_mode: SystemPromptClientMode
  base_server_instructions: string
  final_server_instructions: string
  application: {
    applied: boolean
    carrier: string
    client_instructions: string
    server_instructions: string
    revision: number
    sha256: string
    base_sha256?: string
    effective_sha256?: string
    effective_byte_length?: number
    bundle_id?: string
    bundle_manifest_sha256?: string
    bundle_revision?: number
    bundle_archive_sha256?: string
    bundle_source_commit?: string
    route_ids?: string[]
    document_ids?: string[]
    omitted_document_ids?: string[]
    degraded?: boolean
    degraded_reason?: string
  }
}

export async function list(): Promise<SystemPromptListResponse> {
  const { data } = await apiClient.get<SystemPromptListResponse>('/admin/system-prompts')
  return data
}

export async function get(id: number): Promise<SystemPromptDetailResponse> {
  const { data } = await apiClient.get<SystemPromptDetailResponse>(`/admin/system-prompts/${id}`)
  return data
}

export async function listVersions(id: number): Promise<SystemPromptVersion[]> {
  const { data } = await apiClient.get<SystemPromptVersion[]>(`/admin/system-prompts/${id}/versions`)
  return data
}

export async function listBundles(): Promise<SystemPromptBundleSummary[]> {
  const { data } = await apiClient.get<SystemPromptBundleSummary[]>('/admin/system-prompts/bundles')
  return data
}

export async function getBundle(bundleId: string): Promise<SystemPromptBundleDetail> {
  const { data } = await apiClient.get<SystemPromptBundleDetail>(
    `/admin/system-prompts/bundles/${encodeURIComponent(bundleId)}`
  )
  return data
}

export async function getSkillRegistry(): Promise<RemoteSkillRegistryResponse> {
  const { data } = await apiClient.get<RemoteSkillRegistryResponse>('/admin/system-prompts/skill-registry')
  return data
}

export async function getSkillVersion(id: number): Promise<RemoteSkillBundleVersionDetail> {
  const { data } = await apiClient.get<RemoteSkillBundleVersionDetail>(`/admin/system-prompts/skill-registry/versions/${id}`)
  return data
}

export async function startSkillSync(expectedRevision: number): Promise<RemoteSkillSyncJob> {
  const { data } = await apiClient.post<RemoteSkillSyncJob>('/admin/system-prompts/skill-registry/syncs', {
    expected_revision: expectedRevision
  })
  return data
}

export async function getSkillSync(id: number): Promise<RemoteSkillSyncJob> {
  const { data } = await apiClient.get<RemoteSkillSyncJob>(`/admin/system-prompts/skill-registry/syncs/${id}`)
  return data
}

export async function publishSkillVersion(
  id: number,
  expectedRevision: number,
  rollback = false
): Promise<RemoteSkillRegistrySnapshot> {
  const action = rollback ? 'rollback' : 'publish'
  const { data } = await apiClient.post<RemoteSkillRegistrySnapshot>(
    `/admin/system-prompts/skill-registry/versions/${id}/${action}`,
    { expected_revision: expectedRevision }
  )
  return data
}

export async function create(payload: CreateSystemPromptRequest): Promise<SystemPromptDetailResponse> {
  const { data } = await apiClient.post<SystemPromptDetailResponse>('/admin/system-prompts', payload)
  return data
}

export async function updateMetadata(
  id: number,
  payload: { name?: string; description?: string; expected_revision: number }
): Promise<SystemPromptTemplate> {
  const { data } = await apiClient.patch<SystemPromptTemplate>(`/admin/system-prompts/${id}`, payload)
  return data
}

export async function saveDraft(
  id: number,
  payload: {
    body: string
    note: string
    composition_mode: SystemPromptCompositionMode
    bundle_id: string
    bundle_manifest_sha256: string
    expected_latest_version: number
    expected_revision: number
  }
): Promise<SystemPromptVersion> {
  const { data } = await apiClient.post<SystemPromptVersion>(`/admin/system-prompts/${id}/versions`, payload)
  return data
}

export async function syncManagedSource(
  id: number,
  payload: { expected_latest_version: number; expected_revision: number }
): Promise<ManagedSourceSyncResponse> {
  const { data } = await apiClient.post<ManagedSourceSyncResponse>(
    `/admin/system-prompts/${id}/upstream-sync`,
    payload
  )
  return data
}

export async function publish(
  id: number,
  versionId: number,
  expectedRevision: number,
  rollback = false
): Promise<SystemPromptRuntime> {
  const action = rollback ? 'rollback' : 'publish'
  const { data } = await apiClient.post<SystemPromptRuntime>(
    `/admin/system-prompts/${id}/versions/${versionId}/${action}`,
    { expected_revision: expectedRevision }
  )
  return data
}

export async function updateRuntime(payload: {
  expected_revision: number
  enabled: boolean
  expose_server_prompt: boolean
  compact_enabled: boolean
}): Promise<SystemPromptRuntime> {
  const { data } = await apiClient.put<SystemPromptRuntime>('/admin/system-prompts/runtime', payload)
  return data
}

export async function duplicate(
  id: number,
  payload: { slug: string; name: string; expected_revision: number }
): Promise<SystemPromptDetailResponse> {
  const { data } = await apiClient.post<SystemPromptDetailResponse>(`/admin/system-prompts/${id}/duplicate`, payload)
  return data
}

export async function remove(id: number, expectedRevision: number): Promise<{ deleted: boolean }> {
  const { data } = await apiClient.delete<{ deleted: boolean }>(`/admin/system-prompts/${id}`, {
    params: { expected_revision: expectedRevision }
  })
  return data
}

export async function previewMerge(payload: {
  template_id?: number
  version_id?: number
  client_instructions: string
  server_instructions?: string
  composition_mode?: SystemPromptCompositionMode
  bundle_id?: string
  bundle_manifest_sha256?: string
  body?: unknown
  client_mode: SystemPromptClientMode
}): Promise<{
  instructions: string
  client_mode: SystemPromptClientMode
  base_server_instructions: string
  final_server_instructions: string
  application: PreviewUpstreamResponse['application']
}> {
  const { data } = await apiClient.post('/admin/system-prompts/preview/merge', payload)
  return data
}

export async function previewUpstream(payload: {
  template_id: number
  version_id: number
  server_instructions?: string
  composition_mode?: SystemPromptCompositionMode
  bundle_id?: string
  bundle_manifest_sha256?: string
  protocol: 'responses' | 'chat'
  compact: boolean
  body: unknown
  client_mode: SystemPromptClientMode
}): Promise<PreviewUpstreamResponse> {
  const { data } = await apiClient.post<PreviewUpstreamResponse>('/admin/system-prompts/preview/upstream', payload)
  return data
}

export const systemPromptsAPI = {
  list,
  get,
  listVersions,
  listBundles,
  getBundle,
  getSkillRegistry,
  getSkillVersion,
  startSkillSync,
  getSkillSync,
  publishSkillVersion,
  create,
  updateMetadata,
  saveDraft,
  syncManagedSource,
  publish,
  updateRuntime,
  duplicate,
  remove,
  previewMerge,
  previewUpstream
}

export default systemPromptsAPI
