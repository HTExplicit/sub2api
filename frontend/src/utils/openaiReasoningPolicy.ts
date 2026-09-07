export const OPENAI_CHAT_REASONING_REPLAY_KEY = 'openai_chat_reasoning_replay_enabled'
export const OPENAI_REASONING_SIGNATURE_RECOVERY_KEY = 'openai_reasoning_signature_recovery_enabled'

export interface OpenAIReasoningPolicy {
  chatReplay: boolean
  signatureRecovery: boolean
}

export type OpenAIReasoningPolicyField = keyof OpenAIReasoningPolicy

interface ReasoningPolicyAccount {
  platform?: unknown
  wire_platform?: unknown
  type?: unknown
}

const policyKeys = {
  chatReplay: OPENAI_CHAT_REASONING_REPLAY_KEY,
  signatureRecovery: OPENAI_REASONING_SIGNATURE_RECOVERY_KEY
} as const

export function isOpenAIReasoningPolicyApplicable(account: ReasoningPolicyAccount | null | undefined): boolean {
  if (!account || !['apikey', 'oauth', 'setup-token'].includes(String(account.type))) return false
  const wirePlatform = typeof account.wire_platform === 'string'
    ? account.wire_platform.trim().toLowerCase()
    : ''
  const platform = typeof account.platform === 'string' ? account.platform.trim().toLowerCase() : ''
  return (wirePlatform || (platform === 'cindy' ? 'openai' : platform)) === 'openai'
}

export function defaultOpenAIReasoningPolicy(): OpenAIReasoningPolicy {
  return { chatReplay: true, signatureRecovery: true }
}

export function emptyOpenAIReasoningPolicySelection(): OpenAIReasoningPolicy {
  return { chatReplay: false, signatureRecovery: false }
}

export function readOpenAIReasoningPolicy(extra: Record<string, unknown> | null | undefined): OpenAIReasoningPolicy {
  const read = (key: string): boolean => {
    if (!extra || !Object.prototype.hasOwnProperty.call(extra, key)) return true
    return extra[key] === true
  }
  return {
    chatReplay: read(OPENAI_CHAT_REASONING_REPLAY_KEY),
    signatureRecovery: read(OPENAI_REASONING_SIGNATURE_RECOVERY_KEY)
  }
}

// The admin update API preserves omitted policy keys. Send only deliberate edits,
// never replay a modal's stale snapshot (including malformed historical values).
export function applyOpenAIReasoningPolicyEdits(
  extra: Record<string, unknown> | null | undefined,
  values: OpenAIReasoningPolicy,
  selected: OpenAIReasoningPolicy
): Record<string, unknown> {
  const result = { ...extra }
  for (const field of Object.keys(policyKeys) as OpenAIReasoningPolicyField[]) {
    const key = policyKeys[field]
    delete result[key]
    if (selected[field]) result[key] = values[field]
  }
  return result
}
