import { describe, expect, it } from 'vitest'
import type { AccountEditChangesV1 } from '@sub2api/plugin-ui/account-edit'
import type { UpdateAccountRequest } from '@/types'
import { buildModelMappingObject } from '@/composables/useModelWhitelist'
import { editAccount, editCatalog, editContribution, editInput } from '@/__tests__/fixtures/accountEdit'
import {
  accountEditExtraKeys, accountEditModes, applyCoreAccountModeChanges, captureAccountEditState,
  effectiveAccountEditChanges, partitionAccountEditMappings, prepareAccountProviderEdit,
  projectAccountEditCatalog, requiresAccountEditProfile
} from '@/utils/accountEditCodec'

describe('account edit finite lossless codec', () => {
  it('matches only stored canonical identity, not ordinary OpenAI URL or is_cindy hints', () => {
    expect(requiresAccountEditProfile(editAccount())).toBe(true)
    expect(requiresAccountEditProfile(editAccount({ platform: 'openai', is_cindy: true }))).toBe(false)
    expect(requiresAccountEditProfile(editAccount({ type: 'oauth' }))).toBe(false)
  })

  it.each([undefined, null, 'auto', 'force_responses', 'legacy-unknown'])('retains raw Responses state %s on unrelated edits', raw => {
    const extra = raw === undefined ? {} : { openai_responses_mode: raw }
    const next = { ...extra, openai_responses_mode: 'auto', openai_compact_mode: 'auto' }
    applyCoreAccountModeChanges(next, extra, 'apikey', {})
    expect(next).toEqual(extra)
  })

  it.each(['shared', 'dedicated', null])('retains WS encoding %s and all legacy booleans without cleanup', raw => {
    const extra = { openai_apikey_responses_websockets_v2_mode: raw, openai_apikey_responses_websockets_v2_enabled: false, responses_websockets_v2_enabled: true, openai_ws_enabled: false, openai_ws_force_http: true }
    const next = { ...extra }
    applyCoreAccountModeChanges(next, extra, 'apikey', {})
    expect(next).toEqual(extra)
    applyCoreAccountModeChanges(next, extra, 'apikey', { responses_websocket_mode: { op: 'set', value: 'ctx_pool' } })
    expect(next).toEqual({ ...extra, openai_apikey_responses_websockets_v2_mode: 'ctx_pool' })
  })

  it('stores explicit core auto literally and leaves OAuth WS fallback keys untouched', () => {
    const extra = { openai_responses_mode: 'force_responses', openai_compact_mode: null, openai_oauth_responses_websockets_v2_mode: 'shared', openai_ws_enabled: true }
    const next = { ...extra }
    applyCoreAccountModeChanges(next, extra, 'apikey', { responses_mode: { op: 'set', value: 'auto' }, compact_mode: { op: 'set', value: 'auto' } })
    expect(next.openai_responses_mode).toBe('auto')
    expect(next.openai_compact_mode).toBe('auto')
    applyCoreAccountModeChanges(next, next, 'oauth', { responses_websocket_mode: { op: 'set', value: 'off' } })
    expect(next.openai_oauth_responses_websockets_v2_mode).toBe('off')
    expect(next.openai_ws_enabled).toBe(true)
  })

  it('distinguishes missing/null/auto/clear and strips only proven no-ops', () => {
    const account = editAccount({ extra: { openai_compact_mode: null, openai_ws_enabled: false } })
    const state = captureAccountEditState(account), input = editInput(account)
    expect(effectiveAccountEditChanges(state, input, { responses_mode: { op: 'clear' } })).toEqual({})
    expect(effectiveAccountEditChanges(state, input, { responses_mode: { op: 'set', value: 'auto' }, compact_mode: { op: 'clear' }, responses_websocket_mode: { op: 'clear' } })).toEqual({
      responses_mode: { op: 'set', value: 'auto' }, compact_mode: { op: 'clear' }, responses_websocket_mode: { op: 'clear' }
    })
    expect(() => effectiveAccountEditChanges(state, input, { responses_websocket_mode: { op: 'set', value: 'auto' } } as unknown as AccountEditChangesV1)).toThrow('Invalid account edit mode')
  })

  it('defines exactly four WS clear keys and never exposes a client delete-key list', () => {
    expect(accountEditModes.responses_websocket_mode.keys).toEqual(['openai_apikey_responses_websockets_v2_mode', 'openai_apikey_responses_websockets_v2_enabled', 'responses_websockets_v2_enabled', 'openai_ws_enabled'])
    const definition = editContribution().account_edit
    expect(definition.wire_controls.find(control => control.target === 'responses_websocket_mode')?.values).toEqual(['off', 'ctx_pool', 'passthrough', 'http_bridge'])
    expect(definition).not.toHaveProperty('defaults')
    expect(definition).not.toHaveProperty('minimum_effective_groups')
  })

  it('uses full management inventory and joins only aliases with public targets', () => {
    const projection = projectAccountEditCatalog(editCatalog())
    expect(projection.models.map(model => model.id)).toEqual(['managed', 'private-management-entry'])
    expect(projection.aliases.map(model => model.id)).toEqual(['legacy'])
    expect(projection.aliases[0]).toMatchObject({ alias_target: 'managed', live_upstream_id: 'wire-managed' })
  })

  it('preserves only stored managed pairs and supports custom and shadowed mappings', () => {
    const account = editAccount({ credentials: { model_mapping: { managed: 'wire-managed', legacy: 'managed', custom: 'target', 'private-management-entry': 'custom-override' } } })
    const state = captureAccountEditState(account), catalog = editCatalog(), partition = partitionAccountEditMappings(state, catalog)
    expect(partition.managed).toEqual({ managed: 'wire-managed', legacy: 'managed' })
    expect(partition.editable).toEqual({ custom: 'target', 'private-management-entry': 'custom-override' })
    const input = editInput(account)
    input.model_mapping = { mode: 'mapping', allowed: [], rows: [{ from: 'custom', to: 'new-target' }, { from: 'managed', to: 'shadowed-custom' }] }
    const payload: UpdateAccountRequest = { credentials: { ...account.credentials }, extra: { openai_passthrough: true, openai_ws_force_http: true } }
    prepareAccountProviderEdit(payload, input, { model_mapping: { op: 'set' } }, partition.managed)
    expect(payload.credentials?.model_mapping).toEqual({ managed: 'shadowed-custom', legacy: 'managed', custom: 'new-target' })
    expect(payload.extra).toEqual({ openai_passthrough: true, openai_ws_force_http: true })
    expect(payload.credentials?.model_mapping).not.toHaveProperty('private-management-entry')
  })

  it('sends typed modes/clear without contradictory raw slots and preserves unrelated facts', () => {
    const extra = Object.fromEntries(accountEditExtraKeys.map(key => [key, null]))
    Object.assign(extra, { openai_passthrough: true, openai_oauth_passthrough: true, openai_oauth_responses_websockets_v2_mode: 'shared', openai_ws_force_http: true, openai_compact_supported: false })
    const payload: UpdateAccountRequest = { extra, credentials: { api_key: 'fixture-only', model_mapping: { managed: 'managed' }, compact_model_mapping: { old: 'old' } } }
    prepareAccountProviderEdit(payload, editInput(), { responses_mode: { op: 'set', value: 'auto' }, responses_websocket_mode: { op: 'clear' }, model_mapping: { op: 'clear' }, compact_model_mapping: { op: 'clear' } }, {})
    for (const key of accountEditExtraKeys) expect(payload.extra).not.toHaveProperty(key)
    expect(payload.extra).toEqual({ openai_passthrough: true, openai_oauth_passthrough: true, openai_oauth_responses_websockets_v2_mode: 'shared', openai_ws_force_http: true, openai_compact_supported: false })
    expect(payload.credentials).toEqual({ api_key: 'fixture-only' })
  })

  it('preserves own __proto__ custom aliases through partition, no-op comparison and both mapping writes', () => {
    const mapping = JSON.parse('{"__proto__":"valid-target","custom":"other"}')
    const account = editAccount({ credentials: { model_mapping: mapping, compact_model_mapping: mapping } })
    const state = captureAccountEditState(account), catalog = editCatalog(), input = editInput(account)
    const partition = partitionAccountEditMappings(state, catalog)
    expect(Object.prototype.hasOwnProperty.call(partition.editable, '__proto__')).toBe(true)
    expect(partition.editable.__proto__).toBe('valid-target')
    expect(effectiveAccountEditChanges(state, input, { model_mapping: { op: 'set' }, compact_model_mapping: { op: 'set' } }, catalog, {})).toEqual({})
    input.model_mapping.rows[0].to = 'new-target'
    input.compact_model_mapping[0].to = 'compact-target'
    const payload: UpdateAccountRequest = {}
    prepareAccountProviderEdit(payload, input, { model_mapping: { op: 'set' }, compact_model_mapping: { op: 'set' } }, {})
    expect(Object.keys(payload.credentials?.model_mapping as object)).toContain('__proto__')
    expect((payload.credentials?.model_mapping as Record<string, string>).__proto__).toBe('new-target')
    expect((payload.credentials?.compact_model_mapping as Record<string, string>).__proto__).toBe('compact-target')
  })

  it('keeps shared builder plain-object, trimming, wildcard and last-entry rules with safe data keys', () => {
    const value = buildModelMappingObject('combined', [' __proto__ ', ' exact ', 'bad*'], [
      { from: ' request* ', to: ' upstream ' }, { from: 'exact', to: 'first' }, { from: 'exact', to: 'last' }, { from: '__proto__', to: 'custom-target' }
    ])!
    expect(Object.getPrototypeOf(value)).toBe(Object.prototype)
    expect(Object.keys(value)).toEqual(['__proto__', 'exact', 'request*'])
    expect(value.__proto__).toBe('custom-target')
    expect(value.exact).toBe('last')
    expect(value['request*']).toBe('upstream')
    expect(buildModelMappingObject('mapping', [], [])).toBeNull()
  })
})
