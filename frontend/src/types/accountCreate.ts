/** Static, bounded provider-create data. Credentials and private forms stay in the host. */
export type AccountCreateLabels = Record<string, string>
export type AccountCreateDefaultTarget = 'concurrency' | 'priority' | 'rate_multiplier' | 'load_factor' | 'responses_mode'
export type AccountCreateResponsesMode = 'auto' | 'force_responses' | 'force_chat_completions'

export interface AccountCreateDefaultsV1 {
  concurrency: number
  priority: number
  rate_multiplier: number
  load_factor: number | null
  responses_mode: AccountCreateResponsesMode
}

export interface AccountCreateDefinitionV1 {
  version: 1
  platform: 'cindy'
  account_type: 'apikey'
  credential_profile: 'cindy_laxa_v1'
  credential_ui: {
    base_url: string
    base_url_readonly: true
    api_key_placeholder: string
    hint: AccountCreateLabels
    base_url_hint?: AccountCreateLabels
    account_type_hint?: AccountCreateLabels
    catalog_label?: AccountCreateLabels
    catalog_hint?: AccountCreateLabels
  }
  defaults: AccountCreateDefaultsV1
  /** Evaluated by the server after its existing default-group fallback. */
  minimum_effective_groups: 1
  upstream_billing_probe: 'unsupported'
  model_editing: 'provider_managed'
  catalog_source: 'group_model_candidates'
  field_bindings: Record<string, 'provider_device_identity_input'>
}

/** Preconditions and non-secret field inputs only; never an owner selector or credential container. */
export interface ProviderCreateRequestV1 {
  // Legacy job payloads retain these fields; native creation does not send them.
  contribution_id?: string
  expected_package_sha256?: string
  expected_definition_sha256?: string
  expected_runtime_generation?: number
  values: Record<string, string>
  inherit_defaults: AccountCreateDefaultTarget[]
}
