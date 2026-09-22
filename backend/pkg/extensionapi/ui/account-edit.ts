/** Bounded, declarative account-edit data. Native account state and credentials stay in the host. */
export type AccountEditLabels = Record<string, string>
export type AccountEditResponsesMode = 'auto' | 'force_responses' | 'force_chat_completions'
export type AccountEditCompactMode = 'auto' | 'force_on' | 'force_off'
export type AccountEditWebSocketMode = 'off' | 'ctx_pool' | 'passthrough' | 'http_bridge'
export type AccountEditModeTarget = 'responses_mode' | 'compact_mode' | 'responses_websocket_mode'
export type AccountEditMappingTarget = 'model_mapping' | 'compact_model_mapping'
export type AccountEditTarget = AccountEditModeTarget | AccountEditMappingTarget

export type AccountEditWireControlV1 =
  | { target: 'responses_mode'; values: AccountEditResponsesMode[]; allow_clear: boolean }
  | { target: 'compact_mode'; values: AccountEditCompactMode[]; allow_clear: boolean }
  | { target: 'responses_websocket_mode'; values: AccountEditWebSocketMode[]; allow_clear: boolean }

export interface AccountEditDefinitionV1 {
  version: 1
  platform: 'cindy'
  account_type: 'apikey'
  credential_profile: 'cindy_laxa_v1'
  credential_ui: {
    base_url: string
    base_url_readonly: true
    hint: AccountEditLabels
  }
  catalog_source: 'provider_catalog_snapshot_v1'
  mapping_policy: 'managed_catalog_v1'
  compact_mapping_editable: boolean
  wire_controls: AccountEditWireControlV1[]
  unlisted_fields: 'preserve'
  labels: {
    managed_catalog: AccountEditLabels
    managed_aliases: AccountEditLabels
    custom_mappings: AccountEditLabels
  }
}

export type AccountEditModeChangeV1<T extends string> = { op: 'set'; value: T } | { op: 'clear' }
export type AccountEditMappingChangeV1 = { op: 'set' } | { op: 'clear' }
export interface AccountEditChangesV1 {
  responses_mode?: AccountEditModeChangeV1<AccountEditResponsesMode>
  compact_mode?: AccountEditModeChangeV1<AccountEditCompactMode>
  responses_websocket_mode?: AccountEditModeChangeV1<AccountEditWebSocketMode>
  model_mapping?: AccountEditMappingChangeV1
  compact_model_mapping?: AccountEditMappingChangeV1
}

/** Preconditions and finite intent only. Mapping values use the native host credential slots. */
export interface ProviderEditRequestV1 {
  contribution_id: string
  expected_package_sha256: string
  expected_definition_sha256: string
  expected_runtime_generation: number
  expected_state_sha256: string
  expected_catalog_namespace?: string
  changes: AccountEditChangesV1
}
