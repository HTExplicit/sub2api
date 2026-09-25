/** Explicit OpenAI protocol-mode edits; modes the admin did not touch keep their stored encoding. */
export type AccountEditResponsesMode = 'auto' | 'force_responses' | 'force_chat_completions'
export type AccountEditCompactMode = 'auto' | 'force_on' | 'force_off'
export type AccountEditWebSocketMode = 'off' | 'ctx_pool' | 'passthrough' | 'http_bridge'
export type AccountEditModeTarget = 'responses_mode' | 'compact_mode' | 'responses_websocket_mode'

export type AccountEditModeChangeV1<T extends string> = { op: 'set'; value: T } | { op: 'clear' }
export interface AccountEditChangesV1 {
  responses_mode?: AccountEditModeChangeV1<AccountEditResponsesMode>
  compact_mode?: AccountEditModeChangeV1<AccountEditCompactMode>
  responses_websocket_mode?: AccountEditModeChangeV1<AccountEditWebSocketMode>
}
