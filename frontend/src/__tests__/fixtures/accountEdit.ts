import { createHash } from 'node:crypto'
import { cindyEditDefinition } from '@/features/cindy/accountForm'
import type { Account, AccountEditFieldState, ProviderAccountEditContext } from '@/types'
import type { AccountEditDefinitionV1 } from '@/types/accountEdit'
import { accountEditModes, captureAccountEditState, mappingInputFromStored, type AccountEditInput, type ReadyAccountEditCatalog } from '@/utils/accountEditCodec'
import { resolveOpenAIWSModeFromExtra } from '@/utils/openaiWsMode'

interface NativeEditFixture { account_edit: AccountEditDefinitionV1; available: boolean }
export function editContribution(overrides: Partial<NativeEditFixture> = {}): NativeEditFixture {
  return {
    account_edit: structuredClone(cindyEditDefinition), available: true, ...overrides
  }
}
export function editAccount(overrides: Partial<Account> = {}): Account {
  const account = {
    id: 41, name: 'fixture', notes: '', platform: 'cindy', type: 'apikey', status: 'active', schedulable: true,
    credentials: { base_url: 'https://api.laxarouter.ai' }, credentials_status: { has_api_key: true }, extra: {},
    group_ids: [], proxy_id: null, expires_at: null, concurrency: 3, priority: 50, rate_multiplier: 1, auto_pause_on_expired: true,
    ...overrides
  } as Account
  if (!Object.prototype.hasOwnProperty.call(overrides, 'account_edit_state_sha256')) account.account_edit_state_sha256 = editFixtureStateDigest(account)
  return account
}
export function editFixtureStateDigest(account: Account): string {
  return createHash('sha256').update(JSON.stringify(captureAccountEditState(account))).digest('hex')
}
export function editCatalog(namespace = 'catalog-1'): ReadyAccountEditCatalog {
  return { status: 'ready', namespace, models: [
    { id: 'managed', type: 'chat', display_name: 'Managed', managed: true, public_model: true, verified: true, live_upstream_id: 'wire-managed' },
    { id: 'private-management-entry', type: 'chat', display_name: 'Private', managed: true, public_model: false, verified: false }
  ], aliases: { legacy: 'managed', 'hidden-alias': 'private-management-entry' } }
}
export function editInput(account = editAccount()): AccountEditInput {
  const extra = account.extra || {}
  return {
    responses_mode: extra.openai_responses_mode === 'force_responses' || extra.openai_responses_mode === 'force_chat_completions' ? extra.openai_responses_mode : 'auto',
    compact_mode: extra.openai_compact_mode === 'force_on' || extra.openai_compact_mode === 'force_off' ? extra.openai_compact_mode : 'auto',
    responses_websocket_mode: resolveOpenAIWSModeFromExtra(extra, { modeKey: accountEditModes.responses_websocket_mode.key,
      enabledKey: 'openai_apikey_responses_websockets_v2_enabled', fallbackEnabledKeys: ['responses_websockets_v2_enabled', 'openai_ws_enabled'] }),
    model_mapping: mappingInputFromStored(account.credentials?.model_mapping),
    compact_model_mapping: Object.entries((account.credentials?.compact_model_mapping || {}) as Record<string, string>).map(([from, to]) => ({ from, to }))
  }
}
export function editContext(account = editAccount(), contribution = editContribution()): ProviderAccountEditContext {
  const extra = account.extra || {}, input = editInput(account)
  function field<T extends string>(key: string, effective: T, allowed: readonly string[]): AccountEditFieldState<T> {
    const value = extra[key], recognized = typeof value === 'string' && allowed.includes(value)
    return { present: Object.prototype.hasOwnProperty.call(extra, key), recognized, ...(recognized || value === null ? { value: value as T | null } : {}), effective }
  }
  return {
    schema_version: 1, kind: 'provider', account_id: account.id,
    profile: { native: true, policy_sha256: 'b'.repeat(64), available: contribution.available },
    edit_state_sha256: editFixtureStateDigest(account),
    values: {
      responses_mode: field(accountEditModes.responses_mode.key, input.responses_mode, accountEditModes.responses_mode.values),
      compact_mode: field(accountEditModes.compact_mode.key, input.compact_mode, accountEditModes.compact_mode.values),
      responses_websocket_mode: field(accountEditModes.responses_websocket_mode.key, input.responses_websocket_mode, [...accountEditModes.responses_websocket_mode.values, 'shared', 'dedicated'])
    },
    catalog: editCatalog()
  }
}
