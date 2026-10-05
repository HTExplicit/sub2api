export const openAIProtocolModes = {
  responses_mode: { key: 'openai_responses_mode', keys: ['openai_responses_mode'], values: ['auto', 'force_responses', 'force_chat_completions'] },
  compact_mode: { key: 'openai_compact_mode', keys: ['openai_compact_mode'], values: ['auto', 'force_on', 'force_off'] },
  responses_websocket_mode: {
    key: 'openai_apikey_responses_websockets_v2_mode',
    keys: ['openai_apikey_responses_websockets_v2_mode', 'openai_apikey_responses_websockets_v2_enabled', 'responses_websockets_v2_enabled', 'openai_ws_enabled'],
    values: ['off', 'ctx_pool', 'passthrough', 'http_bridge']
  }
} as const
export type OpenAIProtocolMode = keyof typeof openAIProtocolModes
/** The OpenAI protocol modes the admin changed in the edit form; modes the admin did not touch keep their stored encoding. */
export type OpenAIProtocolModeEdits = { [Mode in OpenAIProtocolMode]?: (typeof openAIProtocolModes)[Mode]['values'][number] }

const protocolModeExtraKeys = Object.values(openAIProtocolModes).flatMap(mode => [...mode.keys])
const own = (value: object, key: string) => Object.prototype.hasOwnProperty.call(value, key)

/** Preserve the stored mode encodings; an edited mode changes only its primary mode key. */
export function applyOpenAIProtocolModeEdits(
  next: Record<string, unknown>, current: Record<string, unknown> | undefined,
  type: string, edits: OpenAIProtocolModeEdits
): void {
  const raw = current || {}
  const keys = [...protocolModeExtraKeys, 'openai_oauth_responses_websockets_v2_mode', 'openai_oauth_responses_websockets_v2_enabled']
  for (const key of keys) {
    if (own(raw, key)) next[key] = raw[key]
    else delete next[key]
  }
  for (const mode of Object.keys(openAIProtocolModes) as OpenAIProtocolMode[]) {
    const value = edits[mode]
    if (!value) continue
    if (!(openAIProtocolModes[mode].values as readonly string[]).includes(value)) throw new Error('Invalid protocol mode edit')
    if (mode === 'responses_mode' && type !== 'apikey') throw new Error('Responses mode is not applicable')
    const key = mode === 'responses_websocket_mode' && type !== 'apikey'
      ? 'openai_oauth_responses_websockets_v2_mode' : openAIProtocolModes[mode].key
    next[key] = value
  }
}
