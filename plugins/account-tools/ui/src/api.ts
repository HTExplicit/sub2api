import { resource } from '@sub2api/plugin-ui'

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
export interface BatchTestModelRow {
  account_id: number; name: string; platform: string; type: string; is_cindy: boolean
  models: AccountAvailableModel[]; error_code?: string
}
export interface TestSelection { account_id: number; model_id: string; reasoning_effort?: string }

const operationKey = (kind: string) => `${kind}-${crypto.randomUUID()}`
export const accounts = {
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
    return (await resource<{ items: BatchTestModelRow[] }>('tests.models', { body: { account_ids } }, signal)).items
  }
}
export const adminAPI = { accounts }
