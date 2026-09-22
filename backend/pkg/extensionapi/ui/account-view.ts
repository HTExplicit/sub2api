/** Versioned data-only account view declarations. Private host UI is not exported. */
export type AccountViewLabels = Record<string, string>

export interface AccountViewPredicate {
  platforms?: string[]
  types?: string[]
  statuses?: string[]
  plans?: string[]
  privacy_mode?: string
  cindy_only?: boolean
  cindy_balance_status?: 'insufficient'
  cindy_health_status?: 'banned'
}

export interface AccountViewPreset {
  id: string
  label: AccountViewLabels
  query: AccountViewPredicate
  counter: string
}

export interface AccountViewDefinitionV1 {
  version: 1
  source: 'accounts.console.v1'
  base_query: AccountViewPredicate
  default_preset: string
  extends_core_filters?: boolean
  core_filter_surface_refs?: string[]
  presets: AccountViewPreset[]
  layout: Array<{ kind: 'account_table' } | { kind: 'surface'; surface_ref: string }>
  table: { layouts: Array<'table' | 'compact' | 'cards'>; column_refs: string[]; action_refs: string[] }
  navigation?: { section: 'admin.extensions'; target_view: string; icon: 'accounts'; order?: number }
  legacy_query_aliases?: Array<{ match: Record<string, string>; preset: string; priority: number }>
}

export interface AccountResourceActionV1 {
  version: 1
  resource: string
  account_parameter: 'id'
  account_source: 'row.id'
  row_predicate?: AccountViewPredicate
  effect: 'refresh_current_account_view'
}

/** Non-secret identity; it never replaces the operation plugin's own identity. */
export interface AccountViewIdentityV1 {
  version: 1
  plugin_id: number
  plugin_key: string
  package_sha256: string
  view_id: string
  preset_id: string
  view_definition_digest: string
}

/** Only the user filter layer. Manifest predicates cannot be overridden here. */
export interface AccountViewQueryV1 {
  platforms?: string[]
  types?: string[]
  statuses?: string[]
  plans?: string[]
  proxies?: string[]
  folders?: string[]
  tags?: number[]
  account_ids?: number[]
  group_id?: number
  privacy_mode?: string
  search?: string
  sort_by?: string
  sort_order?: 'asc' | 'desc'
}

export interface AccountViewContextV1 extends AccountViewIdentityV1 {
  query: AccountViewQueryV1
}

/** Explicit host projection for owned surfaces; never a native Account DTO. */
export interface AccountViewStateV1 {
  identity: AccountViewIdentityV1
  base_query: AccountViewPredicate
  preset: AccountViewPreset
  query: AccountViewQueryV1
  selected_ids: number[]
  preset_counts: Record<string, number>
  available: boolean
}

export interface AccountViewRecoveryAck { account_id: number; recovered: boolean }
export const ACCOUNT_VIEW_REFRESH_EVENT = 'refresh_current_account_view' as const
