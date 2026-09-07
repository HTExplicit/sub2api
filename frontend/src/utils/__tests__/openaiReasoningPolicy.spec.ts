import { describe, expect, it } from 'vitest'
import {
  OPENAI_CHAT_REASONING_REPLAY_KEY,
  OPENAI_REASONING_SIGNATURE_RECOVERY_KEY,
  applyOpenAIReasoningPolicyEdits,
  defaultOpenAIReasoningPolicy,
  emptyOpenAIReasoningPolicySelection,
  isOpenAIReasoningPolicyApplicable,
  readOpenAIReasoningPolicy
} from '../openaiReasoningPolicy'

describe('OpenAI reasoning policy', () => {
  it('matches the backend effective wire family and accepted account types', () => {
    for (const type of ['apikey', 'oauth', 'setup-token']) {
      expect(isOpenAIReasoningPolicyApplicable({ platform: 'openai', type })).toBe(true)
    }
    expect(isOpenAIReasoningPolicyApplicable({ platform: 'cindy', type: 'apikey' })).toBe(true)
    expect(isOpenAIReasoningPolicyApplicable({ platform: 'cindy', wire_platform: 'openai', type: 'apikey' })).toBe(true)
    expect(isOpenAIReasoningPolicyApplicable({ platform: 'openai', wire_platform: 'anthropic', type: 'apikey' })).toBe(false)
    for (const platform of ['anthropic', 'grok', 'gemini']) {
      expect(isOpenAIReasoningPolicyApplicable({ platform, type: 'apikey' })).toBe(false)
    }
    expect(isOpenAIReasoningPolicyApplicable({ platform: 'openai', type: 'service_account' })).toBe(false)
    expect(isOpenAIReasoningPolicyApplicable(null)).toBe(false)
  })

  it('defaults absent keys on but treats malformed historical values as off', () => {
    expect(readOpenAIReasoningPolicy(null)).toEqual(defaultOpenAIReasoningPolicy())
    for (const value of [false, null, undefined, 'true', 1, {}, []]) {
      expect(readOpenAIReasoningPolicy({ [OPENAI_CHAT_REASONING_REPLAY_KEY]: value }))
        .toEqual({ chatReplay: false, signatureRecovery: true })
    }
    expect(readOpenAIReasoningPolicy({ [OPENAI_REASONING_SIGNATURE_RECOVERY_KEY]: true }))
      .toEqual(defaultOpenAIReasoningPolicy())
  })

  it('sends only selected edits and never replays stale policy snapshots', () => {
    const base = {
      unrelated_setting: 'preserved',
      [OPENAI_CHAT_REASONING_REPLAY_KEY]: 'bad-old-value',
      [OPENAI_REASONING_SIGNATURE_RECOVERY_KEY]: false
    }
    expect(applyOpenAIReasoningPolicyEdits(base, defaultOpenAIReasoningPolicy(), emptyOpenAIReasoningPolicySelection()))
      .toEqual({ unrelated_setting: 'preserved' })
    expect(applyOpenAIReasoningPolicyEdits(base, { chatReplay: false, signatureRecovery: true }, { chatReplay: true, signatureRecovery: false }))
      .toEqual({ unrelated_setting: 'preserved', [OPENAI_CHAT_REASONING_REPLAY_KEY]: false })
    expect(base[OPENAI_CHAT_REASONING_REPLAY_KEY]).toBe('bad-old-value')
    expect(base[OPENAI_REASONING_SIGNATURE_RECOVERY_KEY]).toBe(false)
  })
})
