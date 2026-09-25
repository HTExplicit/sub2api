import { describe, expect, it } from 'vitest'
import { buildModelMappingObject } from '@/composables/useModelWhitelist'
import { accountEditModes, applyCoreAccountModeChanges } from '@/utils/accountEditCodec'

describe('account edit finite lossless codec', () => {
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

  it('defines exactly four WS mode keys', () => {
    expect(accountEditModes.responses_websocket_mode.keys).toEqual(['openai_apikey_responses_websockets_v2_mode', 'openai_apikey_responses_websockets_v2_enabled', 'responses_websockets_v2_enabled', 'openai_ws_enabled'])
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
