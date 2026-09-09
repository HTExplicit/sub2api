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
  last_success_item_id?: number
  last_success_at?: string
  last_success_reusable?: boolean
  latest_attempt?: {
    item_id: number
    status: string
    classification: string
    checked_at?: string
    stale: boolean
    account_failure: boolean
  }
  recognized?: boolean
  recommended?: boolean
  needs_name_confirmation?: boolean
  already_attempted?: boolean
  has_compatible_success?: boolean
  has_pending_probe?: boolean
  attempted_protocol_count?: number
  probe_eligible?: boolean
  routing_ready?: boolean
  probe_status?: CapabilityItemStatus | 'untested' | 'alive' | 'temporary_failure' | 'unsupported' | 'account_failure'
  publishable?: boolean
  pricing_known?: boolean
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
  group_ids?: string
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
  expected_config_fingerprints?: Record<string, string>
  only_untested?: boolean
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
  operation?: 'merge' | 'replace'
  expected_config_revisions?: { accounts: Record<string, string>; groups: Record<string, string> }
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

export interface CapabilityScope {
  folder_ids: number[]
  account_ids: number[]
}

export interface CapabilityModelOverview {
  public_model: string
  aliases: string[]
  tier: 'standard' | 'vip' | 'ssvip'
  published: boolean
  verified_account_count: number
  routing_ready_account_count: number
  untested_account_count: number
  temporary_failure_account_count: number
  pricing_known: boolean
  needs_name_confirmation: boolean
  last_checked_at: string | null
  last_check_status: string
  action: 'add' | 'check_untested' | 'set_price' | 'review_name' | 'view' | 'wait'
  reasons: string[]
  candidates: CapabilityCandidate[]
}

export interface CapabilityGroupOverview {
  id: number | null
  name: string
  platform: string
  rate_multiplier: number
  published_model_count: number
  verified_account_count: number
  routing_ready_account_count: number
  attention_count: number
  models: CapabilityModelOverview[]
}

export interface CapabilityOverview {
  scope: CapabilityScope
  accounts: CapabilityScopeAccount[]
  groups: CapabilityGroupOverview[]
  totals: {
    group_count: number
    published_model_count: number
    verified_account_count: number
    routing_ready_account_count: number
    attention_count: number
  }
}

export interface CapabilityPlanModel {
  group_id?: number | null
  group_name?: string
  public_model: string
}

export interface CapabilityPlanRequest {
  scope: { folder_ids: number[]; account_ids?: number[] }
  group_ids?: number[]
  models?: CapabilityPlanModel[]
  mainstream_only?: boolean
}

export interface CapabilityPlan {
  scope: CapabilityScope
  preview_request: CapabilityPreviewRequest | null
  probe_request: CreateCapabilityRun | null
  maximum_request_count: number
  impact: {
    added_models: CapabilityPlanModel[]
    added_accounts: Array<CapabilityPlanModel & { account_id: number; account_name: string }>
    added_routes?: Array<CapabilityPlanModel & { account_id: number; account_name: string; upstream_model: string; protocol: CapabilityProtocol }>
    retained_models: CapabilityPlanModel[]
    removed_models: CapabilityPlanModel[]
    removed_accounts: Array<CapabilityPlanModel & { account_id: number; account_name?: string }>
    reused_success_count: number
  }
  exclusions: Array<{ account_id: number; public_model: string; upstream_model: string; reason: string }>
  warnings: string[]
}

const accountCapabilitiesAPI = {
  async overview(params: CapabilityListParams = {}, signal?: AbortSignal) {
    return (await apiClient.get<CapabilityOverview>(`${BASE}/overview`, { params, signal })).data
  },
  // Planning only reads saved evidence. Starting probes and applying changes
  // remain separate, explicit administrator actions.
  async plan(request: CapabilityPlanRequest, signal?: AbortSignal) {
    return (await apiClient.post<CapabilityPlan>(`${BASE}/plan`, request, { signal })).data
  },
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
  // Resolve an uncertain creation receipt without replaying a billable request.
  // Keep the key out of URLs and use the same administrator-scoped header.
  async getRunReceipt(idempotencyKey: string, signal?: AbortSignal) {
    return (await apiClient.get<CapabilityRun>(`${BASE}/runs/receipt`, {
      headers: { 'Idempotency-Key': idempotencyKey }, signal,
    })).data
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
