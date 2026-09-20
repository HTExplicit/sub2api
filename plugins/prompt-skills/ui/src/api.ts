import { resource } from '@sub2api/plugin-ui'

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

export interface RemoteSkillPromptVersionDetail extends RemoteSkillPromptVersion {
  raw_body: string
  effective_body: string
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
  prompt: RemoteSkillPromptVersionDetail
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


export const list = () => resource<SystemPromptListResponse>('prompts.list')
export const get = (id: number) => resource<SystemPromptDetailResponse>('prompts.read', { params: { id } })
export const listVersions = (id: number) => resource<SystemPromptVersion[]>('prompts.versions', { params: { id } })
export const getSkillRegistry = () => resource<RemoteSkillRegistryResponse>('skills.registry')
export const getSkillVersion = (id: number) => resource<RemoteSkillBundleVersionDetail>('skills.version', { params: { bundle_version_id: id } })
export const getSkillSync = (id: number) => resource<RemoteSkillSyncJob>('skills.sync.read', { params: { sync_id: id } })

export function startSkillSync(expectedRevision: number, promptCapture?: File) {
  const form: Array<[string, string | Blob]> = [['expected_revision', String(expectedRevision)]]
  if (promptCapture) form.push(['prompt_capture', promptCapture])
  return resource<RemoteSkillSyncJob>('skills.sync.start', { form })
}

export function publishSkillVersion(id: number, expectedRevision: number, rollback = false) {
  return resource<RemoteSkillRegistrySnapshot>(rollback ? 'skills.rollback' : 'skills.publish', {
    params: { bundle_version_id: id }, body: { expected_revision: expectedRevision }
  })
}

export const create = (body: CreateSystemPromptRequest) => resource<SystemPromptDetailResponse>('prompts.create', { body })
export function updateMetadata(id: number, body: { name?: string; description?: string; expected_revision: number }) {
  return resource<SystemPromptTemplate>('prompts.update', { params: { id }, body })
}

export function saveDraft(id: number, body: {
  body: string; note: string; composition_mode: SystemPromptCompositionMode; bundle_id: string
  bundle_manifest_sha256: string; expected_latest_version: number; expected_revision: number
}) {
  return resource<SystemPromptVersion>('prompts.draft', { params: { id }, body })
}

export function syncManagedSource(id: number, body: { expected_latest_version: number; expected_revision: number }) {
  return resource<ManagedSourceSyncResponse>('prompts.source.sync', { params: { id }, body })
}

export function publish(id: number, versionId: number, expectedRevision: number, rollback = false) {
  return resource<SystemPromptRuntime>(rollback ? 'prompts.rollback' : 'prompts.publish', {
    params: { id, version_id: versionId }, body: { expected_revision: expectedRevision }
  })
}

export function updateRuntime(body: { expected_revision: number; enabled: boolean; expose_server_prompt: boolean; compact_enabled: boolean }) {
  return resource<SystemPromptRuntime>('prompts.runtime.update', { body })
}

export function duplicate(id: number, body: { slug: string; name: string; expected_revision: number }) {
  return resource<SystemPromptDetailResponse>('prompts.duplicate', { params: { id }, body })
}

export function remove(id: number, expectedRevision: number) {
  return resource<{ deleted: boolean }>('prompts.delete', { params: { id }, query: { expected_revision: expectedRevision } })
}

export function previewMerge(body: {
  template_id?: number; version_id?: number; client_instructions: string; server_instructions?: string
  composition_mode?: SystemPromptCompositionMode; bundle_id?: string; bundle_manifest_sha256?: string
  body?: unknown; client_mode: SystemPromptClientMode
}) {
  return resource<{ instructions: string; client_mode: SystemPromptClientMode; base_server_instructions: string; final_server_instructions: string; application: PreviewUpstreamResponse['application'] }>('prompts.preview.merge', { body })
}

export function previewUpstream(body: {
  template_id: number; version_id: number; server_instructions?: string; composition_mode?: SystemPromptCompositionMode
  bundle_id?: string; bundle_manifest_sha256?: string; protocol: 'responses' | 'chat'; compact: boolean
  body: unknown; client_mode: SystemPromptClientMode
}) {
  return resource<PreviewUpstreamResponse>('prompts.preview.upstream', { body })
}

export default { list, get, listVersions, getSkillRegistry, getSkillVersion, startSkillSync, getSkillSync,
  publishSkillVersion, create, updateMetadata, saveDraft, syncManagedSource, publish, updateRuntime,
  duplicate, remove, previewMerge, previewUpstream }
