import { resource } from '@/utils/nativeResource'

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
  cindy_health_status?: string
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


export type CindyGroupClassification = 'pure_cindy' | 'mixed' | 'no_cindy'
export type CindyGroupSourceKeeps = 'cindy' | 'ordinary'

export interface CindyGroupAuditEntry {
  group_id: number
  group_name: string
  status: string
  classification: CindyGroupClassification
  cindy_account_count: number
  ordinary_account_count: number
  api_key_count: number
}

export interface CindyGroupAuditSummary {
  pure_cindy_groups: number
  mixed_groups: number
  no_cindy_groups: number
}

export interface CindyGroupAuditResult {
  summary: CindyGroupAuditSummary
  groups: CindyGroupAuditEntry[]
}

export interface CindyGroupSplitPreviewRequest {
  source_keeps: CindyGroupSourceKeeps
  target_name: string
  api_key_ids: number[]
}

export interface CindyGroupSplitCommitRequest extends CindyGroupSplitPreviewRequest {
  member_fingerprint: string
}

export interface CindyGroupSplitPreview {
  source_group_id: number
  source_group_name: string
  source_keeps: CindyGroupSourceKeeps
  target_name: string
  target_classification: CindyGroupClassification
  member_fingerprint: string
  cindy_account_count: number
  ordinary_account_count: number
  accounts_to_move: number
  source_api_key_count: number
  api_keys_to_rebind: number
  api_keys_remaining: number
}

export interface CindyGroupSplitResult extends CindyGroupSplitPreview {
  target_group_id: number
}


export interface ApiKey { id: number; name: string; display_key: string; status: string }
export type CindyCleanupKind = 'insufficient' | 'banned'
export interface CindyCleanupPreview { count: number; fingerprint: string }
export interface CindyCleanupJob { id: number }
export const cindyCleanupAPI = {
  preview: (kind: CindyCleanupKind, operationKey: string, signal?: AbortSignal) =>
    resource<CindyCleanupPreview>(`cindy.cleanup.${kind}.preview`, { operation_key: operationKey }, signal),
  submit: (kind: CindyCleanupKind, preview: CindyCleanupPreview, operationKey: string) =>
    resource<CindyCleanupJob>(`cindy.cleanup.${kind}.submit`, {
      operation_key: operationKey, body: { expected_count: preview.count, fingerprint: preview.fingerprint }
    })
}
export interface CindyDuplicateIdentityGroup { identity_hash: string; proposed_owner_id: number; other_account_ids: number[] }
export const cindyBalanceProbeAPI = {
  preview: (body: CindyBalanceProbePreviewRequest) => resource<CindyBalanceProbePreview>('cindy.probe.preview', { body }),
  create: (body: CindyBalanceProbeCreateRequest) => resource<CindyBalanceProbeJob>('cindy.probe.create', { body }),
  list: (limit = 10) => resource<CindyBalanceProbeJobList>('cindy.probe.list', { query: { limit } }),
  get: (id: number) => resource<CindyBalanceProbeJob>('cindy.probe.get', { params: { id } }),
  listItems: (id: number, query: { state?: string; page?: number; page_size?: number } = {}) => resource<CindyBalanceProbeItemPage>('cindy.probe.items', { params: { id }, query }),
  setRate: (id: number, rate_rps: number) => resource<CindyBalanceProbeJob>('cindy.probe.rate', { params: { id }, body: { rate_rps } }),
  pause: (id: number) => resource<CindyBalanceProbeJob>('cindy.probe.pause', { params: { id } }),
  resume: (id: number) => resource<CindyBalanceProbeJob>('cindy.probe.resume', { params: { id } }),
  cancel: (id: number) => resource<CindyBalanceProbeJob>('cindy.probe.cancel', { params: { id } })
}
export const adminAPI = {
  accounts: { getCindyDuplicateIdentityInventory: () => resource<CindyDuplicateIdentityGroup[]>('cindy.duplicates') },
  groups: {
    auditCindyGroups: () => resource<CindyGroupAuditResult>('cindy.groups.audit'),
    getGroupApiKeys: (id: number, page = 1, page_size = 100) => resource<{ items: ApiKey[]; total: number; pages: number; page_size: number }>('cindy.groups.keys', { params: { id }, query: { page, page_size } }),
    previewCindyGroupSplit: (id: number, body: CindyGroupSplitPreviewRequest) => resource<CindyGroupSplitPreview>('cindy.groups.preview', { params: { id }, body }),
    splitCindyGroup: (id: number, body: CindyGroupSplitCommitRequest) => resource<CindyGroupSplitResult>('cindy.groups.split', { params: { id }, body })
  }
}
