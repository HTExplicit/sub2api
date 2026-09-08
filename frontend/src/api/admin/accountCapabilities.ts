import { apiClient } from '../client'

const BASE = '/admin/account-capabilities'

export type CapabilityProtocol = 'responses' | 'responses_websocket' | 'chat_completions' | 'messages' | 'responses_input_tokens' | 'messages_count_tokens'
export type CapabilityProfile = 'text' | 'tool_roundtrip'
export type CapabilityRunStatus = 'pending' | 'running' | 'pausing' | 'paused' | 'completed' | 'canceling' | 'canceled'
export type CapabilityItemStatus = 'pending' | 'running' | 'succeeded' | 'failed' | 'indeterminate' | 'stale' | 'canceled'

export interface CapabilityModel {
  id: string
  display_name?: string
}

export interface CapabilityResult {
  status?: string
  classification?: string
  http_status?: number
  error_code?: string
  reason?: string
  account_failure?: boolean
  request_count?: number
  latency_ms?: number
  observed_at?: string
  usage?: { input_tokens?: number; output_tokens?: number; total_tokens?: number }
  models?: CapabilityModel[]
}

export interface CapabilityItem {
  id: number
  run_id: number
  account_id: number
  account_name: string
  folder_id: number
  upstream_model: string
  protocol: CapabilityProtocol
  profile: CapabilityProfile
  aliases: string[]
  status: CapabilityItemStatus
  result: CapabilityResult
  request_count: number
  request_count_unknown?: boolean
  stale_config?: boolean
  is_current_scope?: boolean
  dispatched_at?: string
  finished_at?: string
}

export interface CapabilityCandidate {
  candidate_id: string
  account_id: number
  account_name: string
  folder_id: number
  public_model: string
  upstream_model: string
  aliases: string[]
  protocol: CapabilityProtocol
  profile: CapabilityProfile
  tier: 'standard' | 'vip' | 'ssvip'
  group_id?: number
  group_name: string
  discovered: boolean
  configured: boolean
  published: boolean
  discovery_status: string
  latest_probe_item_id?: number
  probe_status?: CapabilityItemStatus | 'untested' | 'alive' | 'temporary_failure' | 'unsupported' | 'account_failure'
  publishable?: boolean
  not_publishable_reasons?: string[]
  stale: boolean
  warnings: string[]
}

export interface CapabilityScopeAccount {
  id: number
  name: string
  folder_id: number
  platform: string
  status: string
  schedulable: boolean
}

export interface CapabilityCandidatePage extends CapabilityPage<CapabilityCandidate> {
  accounts: CapabilityScopeAccount[]
}

export interface CapabilityRun {
  id: number
  created_by: number
  kind: 'discover' | 'probe'
  folder_ids: number[]
  account_ids: number[]
  status: CapabilityRunStatus
  target_count: number
  processed_count: number
  succeeded_count: number
  failed_count: number
  request_count: number
  possibly_sent_count?: number
  started_at?: string
  finished_at?: string
  created_at: string
  updated_at: string
}

export interface CapabilityPage<T> {
  items: T[]
  total: number
  page: number
  page_size: number
}

export interface CapabilityListParams {
  folder_ids?: string
  account_ids?: string
  account_id?: number
  model?: string
  search?: string
  status?: string
  kind?: 'discover' | 'probe'
  page?: number
  page_size?: number
}

export interface CapabilityProbeTarget {
  account_id: number
  upstream_model: string
  protocol: CapabilityProtocol
  profile: CapabilityProfile
  aliases: string[]
}

export interface CreateCapabilityRun {
  kind: 'discover' | 'probe'
  folder_ids: number[]
  account_ids: number[]
  items?: CapabilityProbeTarget[]
}

export interface CapabilityPublicationModel {
  public_model: string
  aliases?: string[]
  tier?: 'standard' | 'vip'
  evidence_ids: number[]
}

export interface CapabilityPublicationGroup {
  id?: number
  name: string
  platform: 'openai' | 'composite'
  rate_multiplier: number
  models: CapabilityPublicationModel[]
}

export interface CapabilityPreviewRequest {
  idempotency_key?: string
  scope: { folder_ids: number[]; account_ids: number[] }
  groups: CapabilityPublicationGroup[]
  detach_account_ids?: number[]
  scheduling_evidence_ids?: number[]
}

export interface CapabilityChange {
  kind: 'bindings' | 'mappings' | 'allowlist' | 'scheduling' | 'routes' | 'channel' | 'group'
  account_id?: number
  group_id?: number
  label: string
  before: unknown
  after: unknown
  evidence_ids: number[]
}

export interface CapabilityChangeset {
  id: number
  scope: { folder_ids: number[]; account_ids: number[] }
  status: 'preview' | 'applied'
  created_at: string
  applied_at?: string
  changes: CapabilityChange[]
  warnings: string[]
}

const accountCapabilitiesAPI = {
  async candidates(params: CapabilityListParams = {}, signal?: AbortSignal) {
    return (await apiClient.get<CapabilityCandidatePage>(`${BASE}/candidates`, { params, signal })).data
  },
  async inventory(params: CapabilityListParams = {}, signal?: AbortSignal) {
    return (await apiClient.get<CapabilityPage<CapabilityItem>>(BASE, { params, signal })).data
  },
  async listRuns(params: CapabilityListParams = {}, signal?: AbortSignal) {
    return (await apiClient.get<CapabilityPage<CapabilityRun>>(`${BASE}/runs`, { params, signal })).data
  },
  async getRun(id: number, signal?: AbortSignal) {
    return (await apiClient.get<CapabilityRun>(`${BASE}/runs/${id}`, { signal })).data
  },
  async listItems(id: number, params: CapabilityListParams = {}, signal?: AbortSignal) {
    return (await apiClient.get<CapabilityPage<CapabilityItem>>(`${BASE}/runs/${id}/items`, { params, signal })).data
  },
  // The caller retains this key after an uncertain response. Retrying the same
  // action must not accidentally create another set of billable requests.
  async createRun(request: CreateCapabilityRun, idempotencyKey: string) {
    return (await apiClient.post<CapabilityRun>(`${BASE}/runs`, request, {
      headers: { 'Idempotency-Key': idempotencyKey },
    })).data
  },
  async controlRun(id: number, action: 'pause' | 'resume' | 'cancel') {
    return (await apiClient.post<CapabilityRun>(`${BASE}/runs/${id}/${action}`)).data
  },
  async preview(request: CapabilityPreviewRequest) {
    return (await apiClient.post<CapabilityChangeset>(`${BASE}/changesets/preview`, request)).data
  },
  async getChangeset(id: number) {
    return (await apiClient.get<CapabilityChangeset>(`${BASE}/changesets/${id}`)).data
  },
  async apply(id: number) {
    return (await apiClient.post<CapabilityChangeset>(`${BASE}/changesets/${id}/apply`, {})).data
  },
}

export default accountCapabilitiesAPI
