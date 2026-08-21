import { apiClient } from '../client'

const BASE_PATH = '/admin/account-jobs'

export type AccountJobStatus =
  | 'pending'
  | 'running'
  | 'succeeded'
  | 'partially_succeeded'
  | 'failed'
  | 'canceled'

export type AccountJobItemStatus = 'pending' | 'running' | 'succeeded' | 'failed' | 'canceled'

export interface AccountJob {
  id: number
  created_by: number
  kind: string
  status: AccountJobStatus
  metadata: Record<string, unknown>
  target_count: number
  processed_count: number
  succeeded_count: number
  failed_count: number
  canceled_count: number
  cancel_requested_at?: string
  error_code?: string
  error_message?: string
  retry_of_job_id?: number
  attempt: number
  started_at?: string
  finished_at?: string
  created_at: string
  updated_at: string
}

export interface AccountJobItem {
  id: number
  job_id: number
  ordinal: number
  action?: string
  target_account_id?: number
  status: AccountJobItemStatus
  metadata: Record<string, unknown>
  error_code?: string
  error_message?: string
  started_at?: string
  finished_at?: string
  created_at: string
  updated_at: string
}

export interface AccountJobPage {
  items: AccountJob[]
  total: number
  page: number
  page_size: number
}

export interface AccountJobItemPage {
  items: AccountJobItem[]
  total: number
  page: number
  page_size: number
}

export interface AccountJobListParams {
  kind?: string
  status?: string
  page?: number
  page_size?: number
}

export interface AccountJobItemListParams {
  status?: string
  page?: number
  page_size?: number
}

export interface DuplicateMergeRequest {
  survivor_account_id: number
  loser_account_ids: number[]
  confirmation_hash: string
}

export interface DuplicateReviewAccount {
  account_id: number
  name: string
  group_count: number
  tag_count: number
  configuration_score: number
}

export interface DuplicateReviewMetadata {
  confirmation_hash: string
  accounts: DuplicateReviewAccount[]
}

export function createAccountJobIdempotencyKey(scope: string): string {
  const suffix = globalThis.crypto?.randomUUID?.()
    ?? `${Date.now()}-${Math.random().toString(16).slice(2)}`
  return `${scope.replace(/-/g, '_')}-${suffix}`
}

export function accountJobIdempotencyHeaders(scope: string) {
  return {
    headers: {
      'Idempotency-Key': createAccountJobIdempotencyKey(scope),
    },
  }
}

async function list(
  params: AccountJobListParams = {},
  options: { signal?: AbortSignal } = {},
): Promise<AccountJobPage> {
  const { data } = await apiClient.get<AccountJobPage>(BASE_PATH, {
    params,
    signal: options.signal,
  })
  return data
}

async function get(jobID: number, options: { signal?: AbortSignal } = {}): Promise<AccountJob> {
  const { data } = await apiClient.get<AccountJob>(`${BASE_PATH}/${jobID}`, {
    signal: options.signal,
  })
  return data
}

async function listItems(
  jobID: number,
  params: AccountJobItemListParams = {},
  options: { signal?: AbortSignal } = {},
): Promise<AccountJobItemPage> {
  const { data } = await apiClient.get<AccountJobItemPage>(`${BASE_PATH}/${jobID}/items`, {
    params,
    signal: options.signal,
  })
  return data
}

async function cancel(jobID: number): Promise<AccountJob> {
  const { data } = await apiClient.post<AccountJob>(`${BASE_PATH}/${jobID}/cancel`)
  return data
}

async function retryFailed(jobID: number): Promise<AccountJob> {
  const { data } = await apiClient.post<AccountJob>(
    `${BASE_PATH}/${jobID}/retry-failed`,
    undefined,
    accountJobIdempotencyHeaders('account_job_retry'),
  )
  return data
}

async function reviewDuplicates(accountIDs: number[]): Promise<AccountJob> {
  const { data } = await apiClient.post<AccountJob>(
    '/admin/accounts/duplicates/review',
    { account_ids: accountIDs },
    accountJobIdempotencyHeaders('account_duplicate_review'),
  )
  return data
}

async function mergeDuplicates(request: DuplicateMergeRequest): Promise<AccountJob> {
  const { data } = await apiClient.post<AccountJob>(
    '/admin/accounts/duplicates/merge',
    request,
    accountJobIdempotencyHeaders('account_duplicate_merge'),
  )
  return data
}

const accountJobsAPI = {
  list,
  get,
  listItems,
  cancel,
  retryFailed,
  reviewDuplicates,
  mergeDuplicates,
}

export default accountJobsAPI
