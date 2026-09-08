import { describe, expect, it } from 'vitest'
import { capabilityPublicationTier } from '../accountCapabilitiesHelpers'

describe('account capability publication tiers', () => {
  it('retains standard versus VIP isolation for canonical GPT models', () => {
    expect(capabilityPublicationTier('gpt-6-astra', 'standard')).toBe('standard')
    expect(capabilityPublicationTier('gpt-6-astra', 'vip')).toBe('vip')
    expect(capabilityPublicationTier('gpt-5.6-sol', 'ssvip')).toBe('vip')
  })

  it('uses the single public tier for other brands without rewriting their raw targets or tiers', () => {
    for (const publicModel of ['gemini-3.1-pro-preview', 'grok-4.6', 'claude-opus-5', 'qwen3.8-max', 'MiniMax-M3']) {
      const observed = Object.freeze({ publicModel, upstreamModel: `${publicModel}-ssvip`, tier: 'ssvip' as const })
      expect(capabilityPublicationTier(observed.publicModel, observed.tier)).toBe('standard')
      expect(capabilityPublicationTier(observed.publicModel, 'vip')).toBe('standard')
      expect(capabilityPublicationTier(observed.publicModel, 'standard')).toBe('standard')
      expect(observed.upstreamModel).toBe(`${publicModel}-ssvip`)
      expect(observed.tier).toBe('ssvip')
    }
  })
})
