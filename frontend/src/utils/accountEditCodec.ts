import type { AccountEditChangesV1, AccountEditModeTarget } from '@/types/accountEdit'

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
const own = (value: object, key: string) => Object.prototype.hasOwnProperty.call(value, key)

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
