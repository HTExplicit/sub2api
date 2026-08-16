import { apiClient } from '../client'

const BASE_PATH = '/admin/cindy/balance-probe-jobs'

export type CindyBalanceProbeScopeMode = 'all' | 'filter' | 'selected'

export interface CindyBalanceProbeFilters {
  platforms?: string[]
  types?: string[]
  statuses?: string[]
  plans?: string[]
  proxy_ids?: number[]
  include_direct?: boolean
  folder_ids?: number[]
  include_uncategorized?: boolean
  tag_ids?: number[]
  account_ids?: number[]
  search?: string
  group_id?: number
  privacy_mode?: string
  cindy_balance_status?: string
  sort_by?: string
  sort_order?: string
}

export interface CindyBalanceProbeScope {
  mode: CindyBalanceProbeScopeMode
  account_ids?: number[]
  filters?: CindyBalanceProbeFilters
}

export interface CindyBalanceProbePreviewRequest {
  scope: CindyBalanceProbeScope
  rate_rps: number
}

export interface CindyBalanceProbeCreateRequest extends CindyBalanceProbePreviewRequest {
  expected_count: number
  candidate_fingerprint: string
}

export interface CindyBalanceProbePreview {
  scope: CindyBalanceProbeScope
  candidate_count: number
  marked_count: number
  unmarked_count: number
  candidate_fingerprint: string
  minimum_calls: number
  maximum_calls: number
  rate_rps: number
  minimum_eta_seconds: number
  maximum_eta_seconds: number
}

export interface CindyBalanceProbeCounts {
  pending: number
  running: number
  healthy: number
  recovered: number
  exhausted: number
  inconclusive: number
  skipped: number
}

export type CindyBalanceProbeJobStatus =
  | 'queued'
  | 'running'
  | 'paused'
  | 'paused_upstream'
  | 'cancel_requested'
  | 'completed'
  | 'completed_with_issues'
  | 'canceled'

export interface CindyBalanceProbeJob {
  id: number
  status: CindyBalanceProbeJobStatus | string
  requested_by?: number
  scope: CindyBalanceProbeScope
  rate_rps: number
  candidate_count: number
  candidate_fingerprint: string
  request_count: number
  consecutive_upstream_failures: number
  last_request_started_at?: string
  heartbeat_at?: string
  cancel_requested_at?: string
  started_at?: string
  finished_at?: string
  failure_reason?: string
  created_at: string
  updated_at: string
  counts: CindyBalanceProbeCounts
}

export interface CindyBalanceProbeItem {
  id: number
  job_id: number
  account_id: number
  ordinal: number
  was_marked: boolean
  state: string
  luna_outcome?: string
  luna_at?: string
  terra_outcome?: string
  terra_at?: string
  request_count: number
  final_outcome?: string
  started_at?: string
  finished_at?: string
  created_at: string
  updated_at: string
}

export interface CindyBalanceProbeItemPage {
  items: CindyBalanceProbeItem[]
  total: number
  page: number
  page_size: number
}

export interface CindyBalanceProbeJobList {
  items: CindyBalanceProbeJob[]
  total: number
}

export function canonicalizeCindyBalanceProbeScope(scope: CindyBalanceProbeScope): CindyBalanceProbeScope {
  if (scope.mode !== 'selected') return scope

  const sourceIDs = scope.account_ids?.length ? scope.account_ids : scope.filters?.account_ids || []
  const accountIDs = [...new Set(sourceIDs.filter((accountID) => Number.isSafeInteger(accountID) && accountID > 0))]
    .sort((left, right) => left - right)
  return { mode: 'selected', account_ids: accountIDs }
}

export async function previewCindyBalanceProbe(
  request: CindyBalanceProbePreviewRequest,
): Promise<CindyBalanceProbePreview> {
  const { data } = await apiClient.post<CindyBalanceProbePreview>(`${BASE_PATH}/preview`, request)
  return data
}

export async function createCindyBalanceProbe(
  request: CindyBalanceProbeCreateRequest,
): Promise<CindyBalanceProbeJob> {
  const { data } = await apiClient.post<CindyBalanceProbeJob>(BASE_PATH, request)
  return data
}

export async function listCindyBalanceProbeJobs(limit = 10): Promise<CindyBalanceProbeJobList> {
  const { data } = await apiClient.get<CindyBalanceProbeJobList>(BASE_PATH, { params: { limit } })
  return data
}

export async function getCindyBalanceProbeJob(jobID: number): Promise<CindyBalanceProbeJob> {
  const { data } = await apiClient.get<CindyBalanceProbeJob>(`${BASE_PATH}/${jobID}`)
  return data
}

export async function listCindyBalanceProbeItems(
  jobID: number,
  params: { state?: string; page?: number; page_size?: number } = {},
): Promise<CindyBalanceProbeItemPage> {
  const { data } = await apiClient.get<CindyBalanceProbeItemPage>(`${BASE_PATH}/${jobID}/items`, { params })
  return data
}

export async function setCindyBalanceProbeRate(jobID: number, rateRPS: number): Promise<CindyBalanceProbeJob> {
  const { data } = await apiClient.patch<CindyBalanceProbeJob>(`${BASE_PATH}/${jobID}/rate`, { rate_rps: rateRPS })
  return data
}

export async function pauseCindyBalanceProbe(jobID: number): Promise<CindyBalanceProbeJob> {
  const { data } = await apiClient.post<CindyBalanceProbeJob>(`${BASE_PATH}/${jobID}/pause`)
  return data
}

export async function resumeCindyBalanceProbe(jobID: number): Promise<CindyBalanceProbeJob> {
  const { data } = await apiClient.post<CindyBalanceProbeJob>(`${BASE_PATH}/${jobID}/resume`)
  return data
}

export async function cancelCindyBalanceProbe(jobID: number): Promise<CindyBalanceProbeJob> {
  const { data } = await apiClient.post<CindyBalanceProbeJob>(`${BASE_PATH}/${jobID}/cancel`)
  return data
}

export const cindyBalanceProbeAPI = {
  preview: previewCindyBalanceProbe,
  create: createCindyBalanceProbe,
  list: listCindyBalanceProbeJobs,
  get: getCindyBalanceProbeJob,
  listItems: listCindyBalanceProbeItems,
  setRate: setCindyBalanceProbeRate,
  pause: pauseCindyBalanceProbe,
  resume: resumeCindyBalanceProbe,
  cancel: cancelCindyBalanceProbe,
}

export default cindyBalanceProbeAPI
