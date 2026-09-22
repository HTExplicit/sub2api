import type {
  AccountEditChangesV1, AccountEditCompactMode, AccountEditModeTarget,
  AccountEditResponsesMode, AccountEditWebSocketMode
} from '@sub2api/plugin-ui/account-edit'
import type { Account, AccountAvailableModel, AccountEditCatalog, UpdateAccountRequest } from '@/types'
import { buildModelMappingObject, splitModelMappingObject } from '@/composables/useModelWhitelist'

export interface AccountEditMappingRow { from: string; to: string }
export interface AccountEditInput {
  responses_mode: AccountEditResponsesMode
  compact_mode: AccountEditCompactMode
  responses_websocket_mode: AccountEditWebSocketMode
  model_mapping: { mode: 'whitelist' | 'mapping'; allowed: string[]; rows: AccountEditMappingRow[] }
  compact_model_mapping: AccountEditMappingRow[]
}
export interface AccountEditStoredState { extra: Record<string, unknown>; credentials: Record<string, unknown> }
export type ReadyAccountEditCatalog = Extract<AccountEditCatalog, { status: 'ready' }>

export const accountEditModes = {
  responses_mode: { key: 'openai_responses_mode', keys: ['openai_responses_mode'], values: ['auto', 'force_responses', 'force_chat_completions'] },
  compact_mode: { key: 'openai_compact_mode', keys: ['openai_compact_mode'], values: ['auto', 'force_on', 'force_off'] },
  responses_websocket_mode: {
    key: 'openai_apikey_responses_websockets_v2_mode',
    keys: ['openai_apikey_responses_websockets_v2_mode', 'openai_apikey_responses_websockets_v2_enabled', 'responses_websockets_v2_enabled', 'openai_ws_enabled'],
    values: ['off', 'ctx_pool', 'passthrough', 'http_bridge']
  }
} as const
export const accountEditExtraKeys = Object.values(accountEditModes).flatMap(mode => [...mode.keys])
export const accountEditMappingKeys = ['model_mapping', 'compact_model_mapping'] as const
const own = (value: object, key: string) => Object.prototype.hasOwnProperty.call(value, key)
export const copyAccountEditValue = <T>(value: T): T => JSON.parse(JSON.stringify(value)) as T
const sorted = (value: unknown): unknown => Array.isArray(value) ? value.map(sorted)
  : value && typeof value === 'object' ? Object.fromEntries(Object.entries(value).sort(([a], [b]) => a.localeCompare(b)).map(([key, item]) => [key, sorted(item)])) : value
export const equalAccountEditValue = (a: unknown, b: unknown) => JSON.stringify(sorted(a)) === JSON.stringify(sorted(b))
const record = (value: unknown): Record<string, unknown> => value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}

/** Only the five owned targets; no token, device identity or private account object in this snapshot. */
export function captureAccountEditState(account: Pick<Account, 'extra' | 'credentials'>): AccountEditStoredState {
  const extra = record(account.extra), credentials = record(account.credentials)
  return copyAccountEditValue({
    extra: Object.fromEntries(accountEditExtraKeys.filter(key => own(extra, key)).map(key => [key, extra[key]])),
    credentials: Object.fromEntries(accountEditMappingKeys.filter(key => own(credentials, key)).map(key => [key, credentials[key]]))
  })
}

/** Stored platform/type, never the broad is_cindy hint or an ordinary OpenAI endpoint. */
export function requiresAccountEditProfile(account: Pick<Account, 'platform' | 'type'> | null | undefined): boolean {
  return account?.platform === 'cindy' && account.type === 'apikey'
}

/** Preserve ordinary core encodings; an explicit mode set changes only its primary mode key. */
export function applyCoreAccountModeChanges(
  next: Record<string, unknown>, current: Record<string, unknown> | undefined,
  type: string, changes: AccountEditChangesV1
): void {
  const raw = current || {}
  const keys = [...accountEditExtraKeys, 'openai_oauth_responses_websockets_v2_mode', 'openai_oauth_responses_websockets_v2_enabled']
  for (const key of keys) {
    if (own(raw, key)) next[key] = raw[key]
    else delete next[key]
  }
  for (const target of Object.keys(accountEditModes) as AccountEditModeTarget[]) {
    const change = changes[target]
    if (!change) continue
    if (change.op !== 'set' || !(accountEditModes[target].values as readonly string[]).includes(change.value)) throw new Error('Invalid core mode edit')
    if (target === 'responses_mode' && type !== 'apikey') throw new Error('Responses mode is not applicable')
    const key = target === 'responses_websocket_mode' && type !== 'apikey'
      ? 'openai_oauth_responses_websockets_v2_mode' : accountEditModes[target].key
    next[key] = change.value
  }
}

export function projectAccountEditCatalog(catalog: ReadyAccountEditCatalog): { models: AccountAvailableModel[]; aliases: AccountAvailableModel[] } {
  const models = catalog.models.filter(model => model.managed === true && !model.alias_target)
  const byID = new Map(catalog.models.filter(model => model.public_model === true && !model.alias_target).map(model => [model.id, model]))
  const aliases: AccountAvailableModel[] = []
  for (const [id, target] of Object.entries(catalog.aliases)) {
    const model = byID.get(target)
    if (model) aliases.push({ ...model, id, alias_target: target, managed: true })
  }
  return { models, aliases }
}

export function partitionAccountEditMappings(state: AccountEditStoredState, catalog: ReadyAccountEditCatalog) {
  const { models, aliases } = projectAccountEditCatalog(catalog)
  const managedByID = new Map([...models, ...aliases].map(model => [model.id, model]))
  const managed: Array<[string, string]> = [], editable: Array<[string, unknown]> = []
  for (const [from, to] of Object.entries(record(state.credentials.model_mapping))) {
    const model = managedByID.get(from.trim())
    if (model && typeof to === 'string' && [model.id, model.alias_target, model.live_upstream_id].includes(to.trim())) managed.push([from, to])
    else editable.push([from, to])
  }
  return { managed: Object.fromEntries(managed), editable: Object.fromEntries(editable) }
}

export function mappingInputFromStored(raw: unknown): AccountEditInput['model_mapping'] {
  const parsed = splitModelMappingObject(record(raw))
  return { mode: parsed.modelMappings.length > 0 && parsed.allowedModels.length === 0 ? 'mapping' : 'whitelist', allowed: parsed.allowedModels, rows: parsed.modelMappings }
}

export function accountEditMappingValue(input: AccountEditInput, preserved: Record<string, string>): Record<string, string> {
  return { ...preserved, ...(buildModelMappingObject('combined', input.model_mapping.allowed, input.model_mapping.rows) || {}) }
}

/** Unknown catalog-dependent intent stays pending; it is never guessed to be an empty no-op. */
export function effectiveAccountEditChanges(
  state: AccountEditStoredState, input: AccountEditInput, intent: AccountEditChangesV1,
  catalog?: ReadyAccountEditCatalog, preserved: Record<string, string> = {}
): AccountEditChangesV1 {
  const result: AccountEditChangesV1 = copyAccountEditValue(intent)
  for (const target of Object.keys(accountEditModes) as AccountEditModeTarget[]) {
    const change = result[target], mode = accountEditModes[target]
    if (!change) continue
    if (change.op === 'set') {
      if (!(mode.values as readonly string[]).includes(change.value)) throw new Error('Invalid account edit mode')
      if (own(state.extra, mode.key) && state.extra[mode.key] === change.value) delete result[target]
    } else if (!mode.keys.some(key => own(state.extra, key))) delete result[target]
  }
  if (result.model_mapping && catalog) {
    const next = result.model_mapping.op === 'clear' ? partitionAccountEditMappings(state, catalog).managed : accountEditMappingValue(input, preserved)
    const remove = result.model_mapping.op === 'clear' && Object.keys(next).length === 0
    if (remove ? !own(state.credentials, 'model_mapping') : own(state.credentials, 'model_mapping') && equalAccountEditValue(state.credentials.model_mapping, next)) delete result.model_mapping
  }
  if (result.compact_model_mapping?.op === 'clear' && !own(state.credentials, 'compact_model_mapping')) delete result.compact_model_mapping
  if (result.compact_model_mapping?.op === 'set' && own(state.credentials, 'compact_model_mapping') && equalAccountEditValue(state.credentials.compact_model_mapping, buildModelMappingObject('mapping', [], input.compact_model_mapping) || {})) delete result.compact_model_mapping
  return result
}

/** Typed modes own their registered keys; omission is retain, never a full-snapshot deletion signal. */
export function prepareAccountProviderEdit(
  payload: UpdateAccountRequest, input: AccountEditInput, changes: AccountEditChangesV1,
  preserved: Record<string, string>
): void {
  if (payload.extra) for (const key of accountEditExtraKeys) delete payload.extra[key]
  if (payload.credentials) for (const key of accountEditMappingKeys) delete payload.credentials[key]
  if (changes.model_mapping?.op === 'set') {
    payload.credentials ||= {}
    payload.credentials.model_mapping = accountEditMappingValue(input, preserved)
  }
  if (changes.compact_model_mapping?.op === 'set') {
    payload.credentials ||= {}
    payload.credentials.compact_model_mapping = buildModelMappingObject('mapping', [], input.compact_model_mapping) || {}
  }
}
