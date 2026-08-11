import { apiClient } from '../client'

export type SystemPromptCompositionMode = 'inline' | 'codex_skill_hybrid'
export type SystemPromptClientMode = 'codex' | 'openai_compatible'
export type RemoteSkillSourceID = 'moxinggang'

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
  registry_raw_tree_sha256?: string
  registry_effective_tree_sha256?: string
  registry_prompt_raw_sha256?: string
  registry_prompt_effective_sha256?: string
  registry_upstream_source_id?: RemoteSkillSourceID
  registry_upstream_root?: string
  registry_public_root?: string
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

export interface RemoteSkillBundleVersion {
  id: number
  upstream_source_id: RemoteSkillSourceID
  upstream_root: string
  public_root: string
  raw_tree_sha256: string
  effective_tree_sha256: string
  prompt_version_id: number
  file_count: number
  raw_total_bytes: number
  effective_total_bytes: number
  added_files: number
  modified_files: number
  deleted_files: number
  script_changes: number
  binary_changes: number
  fetched_at: string
  created_by?: number
  published_at?: string
  published_by?: number
  created_at: string
}

export interface RemoteSkillPromptVersion {
  id: number
  raw_sha256: string
  effective_sha256: string
  diff: string
  created_by?: number
  created_at: string
}

export interface RemoteSkillFileChange {
  path: string
  change: 'added' | 'modified' | 'deleted'
  kind: string
  raw_sha256?: string
  effective_sha256?: string
  previous_effective_sha256?: string
}

export interface RemoteSkillBundleVersionDetail extends RemoteSkillBundleVersion {
  prompt: RemoteSkillPromptVersion
  file_changes: RemoteSkillFileChange[]
  verified: boolean
}

export interface RemoteSkillRegistrySnapshot {
  revision: number
  active?: RemoteSkillBundleVersion
  active_prompt?: RemoteSkillPromptVersion
  degraded: boolean
  degraded_reason?: string
  updated_at: string
}

export interface RemoteSkillRegistryResponse {
  runtime: RemoteSkillRegistrySnapshot
  versions: RemoteSkillBundleVersion[]
  source: {
    upstream_source_id: RemoteSkillSourceID
    upstream_root: string
    public_root: string
  }
}

export interface RemoteSkillSyncJob {
  id: number
  status: 'queued' | 'running' | 'succeeded' | 'failed'
  progress_stage: string
  candidate_bundle_version_id?: number
  prompt_capture_provided: boolean
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
    bundle_raw_tree_sha256?: string
    bundle_effective_tree_sha256?: string
    bundle_prompt_raw_sha256?: string
    bundle_prompt_effective_sha256?: string
    bundle_upstream_source_id?: string
    bundle_upstream_root?: string
    bundle_public_root?: string
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

export async function getSkillRegistry(): Promise<RemoteSkillRegistryResponse> {
  const { data } = await apiClient.get<RemoteSkillRegistryResponse>('/admin/system-prompts/skill-registry')
  return data
}

export async function getSkillVersion(id: number): Promise<RemoteSkillBundleVersionDetail> {
  const { data } = await apiClient.get<RemoteSkillBundleVersionDetail>(`/admin/system-prompts/skill-registry/versions/${id}`)
  return data
}

export async function startSkillSync(
  expectedRevision: number,
  promptCapture?: File
): Promise<RemoteSkillSyncJob> {
  const form = new FormData()
  form.append('expected_revision', String(expectedRevision))
  if (promptCapture) form.append('prompt_capture', promptCapture)
  const { data } = await apiClient.post<RemoteSkillSyncJob>('/admin/system-prompts/skill-registry/syncs', form)
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
