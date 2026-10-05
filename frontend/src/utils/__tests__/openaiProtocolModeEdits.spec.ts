import { describe, expect, it } from 'vitest'
import { openAIProtocolModes, applyOpenAIProtocolModeEdits } from '@/utils/openaiProtocolModeEdits'

describe('OpenAI protocol mode edits', () => {
  it.each([undefined, null, 'auto', 'force_responses', 'legacy-unknown'])('retains raw Responses state %s on unrelated edits', raw => {
    const extra = raw === undefined ? {} : { openai_responses_mode: raw }
    const next = { ...extra, openai_responses_mode: 'auto', openai_compact_mode: 'auto' }
    applyOpenAIProtocolModeEdits(next, extra, 'apikey', {})
    expect(next).toEqual(extra)
  })

  it.each(['shared', 'dedicated', null])('retains WS encoding %s and all legacy booleans without cleanup', raw => {
    const extra = { openai_apikey_responses_websockets_v2_mode: raw, openai_apikey_responses_websockets_v2_enabled: false, responses_websockets_v2_enabled: true, openai_ws_enabled: false, openai_ws_force_http: true }
    const next = { ...extra }
    applyOpenAIProtocolModeEdits(next, extra, 'apikey', {})
    expect(next).toEqual(extra)
    applyOpenAIProtocolModeEdits(next, extra, 'apikey', { responses_websocket_mode: 'ctx_pool' })
    expect(next).toEqual({ ...extra, openai_apikey_responses_websockets_v2_mode: 'ctx_pool' })
  })

  it('stores an explicit auto literally and leaves OAuth WS fallback keys untouched', () => {
    const extra = { openai_responses_mode: 'force_responses', openai_compact_mode: null, openai_oauth_responses_websockets_v2_mode: 'shared', openai_ws_enabled: true }
    const next = { ...extra }
    applyOpenAIProtocolModeEdits(next, extra, 'apikey', { responses_mode: 'auto', compact_mode: 'auto' })
    expect(next.openai_responses_mode).toBe('auto')
    expect(next.openai_compact_mode).toBe('auto')
    applyOpenAIProtocolModeEdits(next, next, 'oauth', { responses_websocket_mode: 'off' })
    expect(next.openai_oauth_responses_websockets_v2_mode).toBe('off')
    expect(next.openai_ws_enabled).toBe(true)
  })

  it('defines exactly four WS mode keys', () => {
    expect(openAIProtocolModes.responses_websocket_mode.keys).toEqual(['openai_apikey_responses_websockets_v2_mode', 'openai_apikey_responses_websockets_v2_enabled', 'responses_websockets_v2_enabled', 'openai_ws_enabled'])
  })
})
