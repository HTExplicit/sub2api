import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import en from '@/i18n/locales/en/admin/systemPrompts'
import zh from '@/i18n/locales/zh/admin/systemPrompts'
import AccountPromptBinding from '@/components/admin/account/AccountPromptBindingPanel.vue'
import { legalPromptModelScope, legalPromptPosition } from '@/api/admin/systemPromptRules'
import { supportsAccountPromptBinding } from '@/utils/accountPromptBinding'
import { promptConfig, capabilities } from '@/views/admin/__tests__/systemPromptFixtures'

const mocks = vi.hoisted(() => ({ read: vi.fn(), accounts: vi.fn(), bind: vi.fn() }))
vi.mock('@/api/admin/systemPromptRules', async () => ({ ...await vi.importActual<typeof import('@/api/admin/systemPromptRules')>('@/api/admin/systemPromptRules'), rulesAPI: mocks }))
const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: { admin: en }, zh: { admin: zh } } })
const account = (id: number) => ({ account_id: id, name: `Account ${id}`, platform: 'openai', account_type: 'oauth', updated_at: '2026-09-23T00:00:00Z', binding: { mode: 'inherit', rule_ids: [] }, effective_rule_ids: ['first'], supported: true })
beforeEach(() => { Object.values(mocks).forEach(mock => mock.mockReset()); mocks.read.mockResolvedValue(promptConfig()) })
afterEach(() => vi.restoreAllMocks())

describe('protocol capability options', () => {
  it('requires support on every platform while preserving dynamically selected protocol choices', () => {
    for (const position of ['control_prepend', 'control_append', 'conversation_head', 'conversation_tail', 'before_last_user', 'after_last_user'] as const) {
      for (const role of ['auto', 'system', 'developer'] as const) expect(legalPromptPosition(role, position, ['openai'], capabilities)).toBe(true)
      expect(legalPromptPosition('auto', position, ['openai', 'gemini'], capabilities)).toBe(position.startsWith('control_'))
    }
    expect(legalPromptPosition('developer', 'control_append', ['gemini'], capabilities)).toBe(false)
    expect(legalPromptPosition('auto', 'control_append', [], capabilities)).toBe(false)
    expect(legalPromptPosition('auto', 'control_append', ['unknown'], capabilities)).toBe(false)
    expect(legalPromptPosition('developer', 'after_last_user', ['kimi'], capabilities)).toBe(true)
    expect(legalPromptPosition('developer', 'after_last_user', ['kimi', 'anthropic'], capabilities)).toBe(false)
  })
  it('distinguishes Antigravity upstream Messages from OAuth or mixed scopes and requires supported models', () => {
    const rule = { ...promptConfig().policy.rules[0]!, platforms: ['antigravity'], position: 'after_last_user' as const }
    expect(legalPromptPosition('auto', rule.position, rule.platforms, capabilities)).toBe(true)
    expect(legalPromptPosition('auto', rule.position, rule.platforms, capabilities, ['upstream'])).toBe(true)
    expect(legalPromptPosition('auto', rule.position, rule.platforms, capabilities, ['oauth'])).toBe(false)
    expect(legalPromptPosition('auto', rule.position, rule.platforms, capabilities, ['upstream', 'oauth'])).toBe(false)
    expect(legalPromptPosition('auto', 'before_last_user', rule.platforms, capabilities, ['upstream'])).toBe(false)
    expect(legalPromptPosition('auto', 'control_append', rule.platforms, capabilities, ['upstream', 'oauth'])).toBe(true)
    expect(legalPromptModelScope(rule, capabilities)).toBe(false)
    expect(legalPromptModelScope({ ...rule, account_types: ['upstream'], models: ['claude-opus-5'] }, capabilities)).toBe(true)
    expect(legalPromptModelScope({ ...rule, models: ['claude-opus-5'], model_match: 'requested' }, capabilities)).toBe(false)
    expect(legalPromptModelScope({ ...rule, platforms: ['kimi'], role: 'developer' }, capabilities)).toBe(true)
  })
})
describe('account prompt bindings', () => {
  it('exposes binding controls for every conversation platform including Vertex accounts', () => {
    for (const platform of ['anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'cindy', 'minimax', 'opencode_go']) {
      for (const type of ['oauth', 'setup-token', 'apikey', 'bedrock', 'service_account', 'upstream']) expect(supportsAccountPromptBinding({ platform, type })).toBe(true)
    }
    expect(supportsAccountPromptBinding({ platform: 'unknown', type: 'apikey' })).toBe(false)
  })
  it('replaces defaults with account references and reports partial CAS failures', async () => {
    mocks.accounts.mockResolvedValue([account(1), account(2)])
    mocks.bind.mockResolvedValue([{ account_id: 1, applied: true }, { account_id: 2, applied: false, code: 'account_revision_conflict' }])
    const wrapper = mount(AccountPromptBinding, { props: { accountIds: [1, 2] }, global: { plugins: [i18n()] } })
    await flushPromises(); await wrapper.get('[data-test="binding-mode"]').setValue('custom')
    await wrapper.get('fieldset input').setValue(true)
    await wrapper.get('[data-test="save-binding"]').trigger('click'); await flushPromises()
    expect(mocks.bind).toHaveBeenCalledWith([1, 2].map(id => ({ account_id: id, expected_updated_at: account(id).updated_at, binding: { mode: 'custom', rule_ids: ['first'] } })), 7)
    expect(wrapper.text()).toContain('Account changed; refresh before retrying')
    expect(wrapper.emitted('changed')).toHaveLength(1)
    expect(JSON.stringify(mocks.bind.mock.calls)).not.toContain('First body')
    wrapper.unmount()
  })
})
