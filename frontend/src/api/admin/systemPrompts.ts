import { apiClient } from '../client'

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
  updated_at: string
}

export interface SystemPromptTemplate {
  id: number
  slug: string
  name: string
  description: string
  is_seed: boolean
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
  created_by?: number
  published_at?: string
  published_by?: number
  created_at: string
  is_active: boolean
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
  expected_revision: number
}

export interface PreviewUpstreamResponse {
  body: unknown
  application: {
    applied: boolean
    carrier: string
    client_instructions: string
    server_instructions: string
    revision: number
    sha256: string
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
  payload: { body: string; note: string; expected_latest_version: number; expected_revision: number }
): Promise<SystemPromptVersion> {
  const { data } = await apiClient.post<SystemPromptVersion>(`/admin/system-prompts/${id}/versions`, payload)
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
}): Promise<{ instructions: string }> {
  const { data } = await apiClient.post<{ instructions: string }>('/admin/system-prompts/preview/merge', payload)
  return data
}

export async function previewUpstream(payload: {
  template_id: number
  version_id: number
  server_instructions?: string
  protocol: 'responses' | 'chat'
  compact: boolean
  body: unknown
}): Promise<PreviewUpstreamResponse> {
  const { data } = await apiClient.post<PreviewUpstreamResponse>('/admin/system-prompts/preview/upstream', payload)
  return data
}

export const systemPromptsAPI = {
  list,
  get,
  listVersions,
  create,
  updateMetadata,
  saveDraft,
  publish,
  updateRuntime,
  duplicate,
  remove,
  previewMerge,
  previewUpstream
}

export default systemPromptsAPI
