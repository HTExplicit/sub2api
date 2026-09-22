import { resource } from '@sub2api/plugin-ui'

export interface AdminGroup { id: number; name: string; platform: string; wire_platform?: string; provider_profile?: string }
export interface Proxy { id: number; name: string }
export interface AccountImportPreview {
  create_count: number; update_count: number; reject_count: number
  items: Array<{ index: number; name: string; action: string; account_id?: number; warnings?: string[]; code?: string; message?: string; error?: string }>
}
export interface AdminDataPayload {
  type?: string
  version?: number
  exported_at: string
  proxies: AdminDataProxy[]
  accounts: AdminDataAccount[]
  // 导出时被排除的 spark 影子账号数量(影子不持凭据、其调度配置不在备份范围)。
  skipped_shadows?: number
}

export interface AdminDataProxy {
  proxy_key: string
  name: string
  protocol: string
  host: string
  port: number
  username?: string | null
  password?: string | null
  status: 'active' | 'inactive'
}

export interface AdminDataAccount {
  name: string
  notes?: string | null
  platform: string
  type: string
  credentials: Record<string, unknown>
  extra?: Record<string, unknown>
  proxy_key?: string | null
  concurrency: number
  priority: number
  rate_multiplier?: number | null
  expires_at?: number | null
  auto_pause_on_expired?: boolean
  management_folder?: string | null
  tags?: string[]
  groups?: string[]
  status?: 'active' | 'inactive' | 'disabled' | 'error'
  schedulable?: boolean
}

export type AdminDataImportAction = 'skip' | 'update' | 'create'

export interface AdminDataImportNotesSetting {
  mode: 'append' | 'replace'
  value: string
}

export interface AdminDataImportUniformSettings {
  name_prefix?: string
  name_suffix?: string
  notes?: AdminDataImportNotesSetting
  management_folder?: string
  tags?: string[]
  group_ids?: number[]
  proxy_id?: number
  concurrency?: number
  priority?: number
  rate_multiplier?: number
  status?: 'active' | 'disabled' | 'error'
  schedulable?: boolean
}

export interface AccountImportRequest { data: AdminDataPayload; skip_default_group_bind?: boolean; uniform_settings?: AdminDataImportUniformSettings; target_group_id?: number | null }

export interface AccountManagementFolder { id: number; name: string; sort_order: number; account_count: number; created_at: string; updated_at: string }
export type AccountManagementTag = AccountManagementFolder
export interface AccountJob { id: number; metadata: Record<string, unknown> }
export interface AccountListFilters { [key: string]: unknown }
export interface AccountBulkTaxonomyRequest {
  account_ids?: number[]; filters?: AccountListFilters; expected_match_count?: number
  folder_action?: 'set' | 'clear'; folder_id?: number; tag_add_ids?: number[]; tag_remove_ids?: number[]
}
export interface AccountAvailableModel {
  id: string; type: string; display_name: string; reasoning_efforts?: string[]; default_reasoning_effort?: string
  endpoints?: string[]; managed?: boolean; public_model?: boolean; verified?: boolean
  [key: string]: unknown
}
export interface AccountTestPlanView {
  schema_version: 1
  account_id: number
  wire_platform: string
  default_mode: string
  models: AccountAvailableModel[]
  mode_views: Record<string, { model_ids: string[]; default_model_id: string }>
  policy_stamp?: string
}
export interface BatchTestModelRow {
  account_id: number; name: string; platform: string; type: string; is_cindy: boolean
  models: AccountAvailableModel[]; error_code?: string; test_plan?: AccountTestPlanView
}
export interface TestSelection { account_id: number; model_id: string; reasoning_effort?: string }

const operationKey = (kind: string) => `${kind}-${crypto.randomUUID()}`
export const accounts = {
  previewImportData: (body: AccountImportRequest) => resource<AccountImportPreview>('import.preview', { body }),
  importData: (body: AccountImportRequest) => resource<AccountJob>('import.submit', { body, operation_key: operationKey('account_import') }),
  listFolders: () => resource<AccountManagementFolder[]>('taxonomy.folders.list'),
  listTags: () => resource<AccountManagementTag[]>('taxonomy.tags.list'),
  createFolder: (name: string, sort_order = 0) => resource<AccountManagementFolder>('taxonomy.folders.create', { body: { name, sort_order } }),
  createTag: (name: string, sort_order = 0) => resource<AccountManagementTag>('taxonomy.tags.create', { body: { name, sort_order } }),
  updateFolder: (id: number, name: string, sort_order = 0) => resource<AccountManagementFolder>('taxonomy.folders.update', { params: { id }, body: { name, sort_order } }),
  updateTag: (id: number, name: string, sort_order = 0) => resource<AccountManagementTag>('taxonomy.tags.update', { params: { id }, body: { name, sort_order } }),
  deleteFolder: (id: number, move_accounts = false) => resource<void>('taxonomy.folders.delete', { params: { id }, query: { move_accounts } }),
  deleteTag: (id: number) => resource<void>('taxonomy.tags.delete', { params: { id } }),
  reorderFolders: (ordered_ids: number[]) => resource<AccountManagementFolder[]>('taxonomy.folders.order', { body: { ordered_ids } }),
  reorderTags: (ordered_ids: number[]) => resource<AccountManagementTag[]>('taxonomy.tags.order', { body: { ordered_ids } }),
  bulkUpdateTaxonomy: (body: AccountBulkTaxonomyRequest) => resource<AccountJob>('taxonomy.bulk.update', { body, operation_key: operationKey('account_bulk_taxonomy') }),
  setTaxonomy: (id: number, folder_id: number | null, tag_ids: number[]) => resource<{ account_id: number }>('taxonomy.account.update', { params: { id }, body: { folder_id, tag_ids } })
}

export const accountJobsAPI = {
  batchTest: (items: TestSelection[], prompt = '') => resource<AccountJob>('tests.submit', { body: { items, prompt }, operation_key: operationKey('account_batch_test') }),
  async batchTestModels(account_ids: number[], signal?: AbortSignal) {
    return (await resource<{ items: BatchTestModelRow[] }>('tests.models', { body: { account_ids }, query: { view: 'account-test-plan-v1' } }, signal)).items
  }
}
export const adminAPI = { accounts }
