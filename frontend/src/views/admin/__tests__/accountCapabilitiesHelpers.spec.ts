import { describe, expect, it } from 'vitest'
import { canPublishCapability, deduplicateProbeTargets, isCapabilityRunActive, isPublishableCandidate, parseCapabilityIDs, selectCapabilityPublicationTargets } from '../accountCapabilitiesHelpers'
import type { CapabilityCandidate, CapabilityItem, CapabilityProbeTarget } from '@/api/admin/accountCapabilities'

describe('account capability safety helpers', () => {
  it('deduplicates aliases while preserving account, exact target, protocol and profile boundaries', () => {
    const base: CapabilityProbeTarget = { account_id: 7, upstream_model: 'ns/GPT-6-Astra-ssvip', protocol: 'responses', profile: 'text', aliases: ['gpt-6-astra'] }
    const result = deduplicateProbeTargets([
      base,
      { ...base, aliases: ['gpt-6'] },
      { ...base, account_id: 8 },
      { ...base, upstream_model: 'ns/gpt-6-astra-ssvip' },
      { ...base, protocol: 'responses_websocket' },
      { ...base, profile: 'tool_roundtrip' },
    ])
    expect(result).toHaveLength(5)
    expect(result[0]).toEqual({ ...base, aliases: ['gpt-6-astra', 'gpt-6'] })
  })

  it('does not classify incomplete, stale or out-of-scope evidence as publishable', () => {
    const item = { status: 'succeeded', upstream_model: 'model', result: { status: 'alive' }, stale_config: false, is_current_scope: true } as CapabilityItem
    expect(canPublishCapability(item)).toBe(true)
    for (const status of ['pending', 'running', 'failed', 'indeterminate', 'stale', 'canceled'] as const) {
      expect(canPublishCapability({ ...item, status })).toBe(false)
    }
    expect(canPublishCapability({ ...item, stale_config: true })).toBe(false)
    expect(canPublishCapability({ ...item, is_current_scope: false })).toBe(false)
    expect(canPublishCapability({ ...item, protocol: 'messages_count_tokens', result: { status: 'available' } })).toBe(false)
  })

  it('polls only unsettled runs, not paused or completed history', () => {
    for (const status of ['pending', 'running', 'pausing', 'canceling'] as const) expect(isCapabilityRunActive(status)).toBe(true)
    for (const status of ['paused', 'completed', 'canceled'] as const) expect(isCapabilityRunActive(status)).toBe(false)
  })

  it('parses only explicit positive account and folder IDs', () => {
    expect(parseCapabilityIDs(['17,28', '17', '-1', 'a', '0', '2.5', '1e3'])).toEqual([17, 28])
    expect(parseCapabilityIDs(undefined)).toEqual([])
  })
})

describe('account capability publication target selection', () => {
  function candidate(overrides: Partial<CapabilityCandidate> = {}): CapabilityCandidate {
    const item: CapabilityCandidate = {
      candidate_id: '', account_id: 7, account_name: 'account', folder_id: 1,
      public_model: 'gpt-6-astra', upstream_model: 'gpt-6-astra', aliases: [],
      protocol: 'responses', profile: 'text', tier: 'standard', group_name: 'gpt',
      discovered: true, configured: false, published: false, discovery_status: 'discovered',
      latest_probe_item_id: 101, probe_status: 'alive', stale: false, warnings: [],
      publishable: true, ...overrides,
    }
    item.candidate_id = overrides.candidate_id ?? JSON.stringify([item.account_id, item.group_name, item.public_model, item.upstream_model, item.protocol])
    return item
  }

  it('uses explicit publishability without falling back from false and requires a probe evidence ID', () => {
    expect(isPublishableCandidate(candidate())).toBe(true)
    expect(isPublishableCandidate(candidate({ publishable: false }))).toBe(false)
    expect(isPublishableCandidate(candidate({ latest_probe_item_id: undefined }))).toBe(false)
    expect(isPublishableCandidate(candidate({ publishable: undefined }))).toBe(true)
    expect(isPublishableCandidate(candidate({ publishable: undefined, stale: true }))).toBe(false)
    expect(isPublishableCandidate(candidate({ publishable: undefined, probe_status: 'failed' }))).toBe(false)
    expect(isPublishableCandidate(candidate({ publishable: undefined, latest_probe_item_id: undefined }))).toBe(false)
    const result = selectCapabilityPublicationTargets([
      candidate({ upstream_model: 'gpt-6-astra-ssvip', tier: 'ssvip', publishable: false }),
      candidate(),
    ])
    expect(result.selected.map((item) => item.upstream_model)).toEqual(['gpt-6-astra'])
    expect(result.omittedCount).toBe(0)
  })

  it('prefers more distinct verified protocols over a higher tier', () => {
    const response = candidate()
    const chat = candidate({ protocol: 'chat_completions', latest_probe_item_id: 102 })
    const vip = candidate({ upstream_model: 'gpt-6-astra-ssvip', tier: 'ssvip' })
    const result = selectCapabilityPublicationTargets([vip, response, chat])
    expect(result.selected).toEqual([response, chat])
    expect(result.omittedCount).toBe(1)
  })

  it('prefers SSVIP then VIP over standard on equal protocol coverage, before exact spelling', () => {
    const standard = candidate()
    const vip = candidate({ upstream_model: 'gpt-6-astra-vip', tier: 'vip' })
    const ssvip = candidate({ upstream_model: 'gpt-6-astra-ssvip', tier: 'ssvip' })
    expect(selectCapabilityPublicationTargets([standard, vip, ssvip]).selected).toEqual([ssvip])
    expect(selectCapabilityPublicationTargets([standard, vip]).selected).toEqual([vip])
  })

  it('prefers exact public spelling after protocol and tier ties', () => {
    const alias = candidate({ upstream_model: 'a/gpt-6-astra' })
    const exact = candidate()
    expect(selectCapabilityPublicationTargets([alias, exact]).selected).toEqual([exact])
  })

  it('uses deterministic exact-string lexical order for the final tie', () => {
    const lower = candidate({ upstream_model: 'ns/gpt-6-astra-ssvip', tier: 'ssvip' })
    const upper = candidate({ upstream_model: 'ns/GPT-6-Astra-ssvip', tier: 'ssvip' })
    expect(selectCapabilityPublicationTargets([lower, upper]).selected).toEqual([upper])
    expect(selectCapabilityPublicationTargets([upper, lower]).selected).toEqual([upper])
  })

  it('isolates account, group and public model buckets', () => {
    const base = candidate()
    const otherAccount = candidate({ account_id: 8, upstream_model: 'private/gpt-6-astra' })
    const otherGroup = candidate({ group_name: 'gpt-vip', upstream_model: 'gpt-6-astra-ssvip', tier: 'ssvip' })
    const otherModel = candidate({ public_model: 'gpt-5.6-sol', upstream_model: 'gpt-5.6-sol' })
    const result = selectCapabilityPublicationTargets([base, otherAccount, otherGroup, otherModel])
    expect(result.selected).toEqual([base, otherAccount, otherGroup, otherModel])
    expect(result.omittedCount).toBe(0)
  })

  it('keeps all protocols of the chosen target and deduplicates candidate IDs without mutating inventory', () => {
    const response = candidate()
    const chat = candidate({ protocol: 'chat_completions', latest_probe_item_id: 102 })
    const messages = candidate({ protocol: 'messages', latest_probe_item_id: 103 })
    const alias = candidate({ upstream_model: 'alias/gpt-6-astra' })
    const items = [response, chat, response, messages, alias]
    const before = structuredClone(items)
    const result = selectCapabilityPublicationTargets(items)
    expect(result.selected).toEqual([response, chat, messages])
    expect(result.omittedCount).toBe(1)
    expect(items).toEqual(before)
  })

  it('counts distinct protocols rather than repeated observations for the same protocol', () => {
    const response = candidate()
    const repeated = candidate({ candidate_id: 'another-observation', latest_probe_item_id: 102 })
    const vip = candidate({ upstream_model: 'gpt-6-astra-vip', tier: 'vip' })
    expect(selectCapabilityPublicationTargets([response, repeated, vip]).selected).toEqual([vip])
  })
})
