import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import en from '../locales/en'
import zh from '../locales/zh'
import PromptRulesManager from '../PromptRulesManager.vue'
import AccountPromptBinding from '../AccountPromptBinding.vue'
import { legalPromptPosition } from '../rules-api'

const mocks = vi.hoisted(() => ({ read: vi.fn(), save: vi.fn(), accounts: vi.fn(), bind: vi.fn(), preview: vi.fn(), list: vi.fn(), listVersions: vi.fn(), notify: vi.fn(), event: vi.fn() }))
vi.mock('../rules-api', async () => ({ ...await vi.importActual<typeof import('../rules-api')>('../rules-api'), rulesAPI: { read: mocks.read, save: mocks.save, accounts: mocks.accounts, bind: mocks.bind, preview: mocks.preview } }))
vi.mock('../api', () => ({ default: { list: mocks.list, listVersions: mocks.listVersions } }))
vi.mock('@sub2api/plugin-ui', async () => ({ ...await vi.importActual<typeof import('@sub2api/plugin-ui')>('@sub2api/plugin-ui'), useNotifications: () => ({ showSuccess: mocks.notify }), emitHostEvent: mocks.event }))
const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: { admin: en }, zh: { admin: zh } } })
const rule = () => ({ id: 'default', name: 'Default rule', enabled: true, follow_active: true, template_id: 1, version_id: 2, order: 100, delivery: 'native_control', position: 'control_append', model_match: 'upstream', models: [] })
const state = () => ({ revision: 7, enabled: true, compact_enabled: false, policy: { version: 1, default_rule_ids: ['default'], rules: [rule()] } })
const account = (id: number) => ({ account_id: id, name: `Account ${id}`, platform: 'openai', account_type: 'oauth', updated_at: '2026-09-23T00:00:00Z', binding: { mode: 'inherit', rule_ids: [] }, effective_rule_ids: ['default'], supported: true })
beforeEach(() => {
  Object.values(mocks).forEach(mock => mock.mockReset())
  mocks.read.mockResolvedValue(state()); mocks.list.mockResolvedValue({ templates: [{ id: 1, name: 'Template' }] }); mocks.listVersions.mockResolvedValue([])
})
afterEach(() => vi.restoreAllMocks())

describe('prompt rule management', () => {
  it('keeps role independent from all four placements and rejects native sequence placements', () => {
    for (const position of ['control_prepend', 'control_append', 'conversation_head', 'conversation_tail'] as const) {
      expect(legalPromptPosition('system', position)).toBe(true)
      expect(legalPromptPosition('developer', position)).toBe(true)
      expect(legalPromptPosition('native_control', position)).toBe(position.startsWith('control_'))
    }
  })
  it('saves exact versioned rules with CAS and keeps drafts when the revision changed', async () => {
    const wrapper = mount(PromptRulesManager, { global: { plugins: [i18n()] } })
    await flushPromises()
    await wrapper.get('[data-test="prompt-rule-default"] input[maxlength="200"]').setValue('Changed name')
    mocks.save.mockRejectedValue({ response: { status: 409, data: { code: 'system_prompt_revision_conflict' } } })
    await wrapper.get('[data-test="save-rules"]').trigger('click'); await flushPromises()
    expect(mocks.save).toHaveBeenCalledWith(expect.objectContaining({ rules: [expect.objectContaining({ name: 'Changed name', delivery: 'native_control', position: 'control_append' })] }), 7)
    expect((wrapper.get('input[maxlength="200"]').element as HTMLInputElement).value).toBe('Changed name')
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    wrapper.unmount()
  })
  it('renders preview roles and continuation semantics without a model call', async () => {
    mocks.preview.mockResolvedValue({ requested_model: 'alias', upstream_model: 'mapped', protocol: 'responses', transport: 'http', body: { input: [{ role: 'developer', content: 'Site text' }] }, before_rules: {}, client_control: {}, gateway_base_instructions: '', continuation: true, simulated: true, wire_verified: true, application: { applied: true, rules_plan: { placements: [{ rule_id: 'r', position: 'conversation_tail', carrier: 'input', role: 'developer', body: 'Site text' }], skipped: [] } } })
    const wrapper = mount(PromptRulesManager, { global: { plugins: [i18n()] } })
    await flushPromises(); await wrapper.get('input[type="number"]').setValue(42)
    await wrapper.get('[data-test="preview-model"]').setValue('gpt-6-luna')
    const button = wrapper.findAll('button').find(item => item.text() === 'Generate preview')!
    await button.trigger('click'); await flushPromises()
    expect(mocks.preview).toHaveBeenCalledWith(42, expect.objectContaining({ policy: state().policy, protocol: 'responses', transport: 'http', body: expect.objectContaining({ model: 'gpt-6-luna', instructions: 'Client instructions' }) }))
    expect(wrapper.text()).toContain('role=developer')
    expect(wrapper.text()).toContain('current outbound array')
    wrapper.unmount()
  })
})

describe('account prompt bindings', () => {
  it('applies only references to every selected account and reports partial CAS failure', async () => {
    mocks.accounts.mockResolvedValue([account(1), account(2)])
    mocks.bind.mockResolvedValue([{ account_id: 1, applied: true }, { account_id: 2, applied: false, code: 'account_revision_conflict' }])
    const wrapper = mount(AccountPromptBinding, { props: { accountIds: [1, 2] }, global: { plugins: [i18n()] } })
    await flushPromises(); await wrapper.get('[data-test="binding-mode"]').setValue('custom')
    await wrapper.get('fieldset input').setValue(true)
    await wrapper.get('[data-test="save-binding"]').trigger('click'); await flushPromises()
    expect(mocks.bind).toHaveBeenCalledWith([1, 2].map(id => ({ account_id: id, expected_updated_at: account(id).updated_at, binding: { mode: 'custom', rule_ids: ['default'] } })), 7)
    expect(wrapper.text()).toContain('Account changed; refresh before retrying')
    expect(mocks.event).toHaveBeenCalledWith('changed', { account_ids: [1] })
    expect(JSON.stringify(mocks.bind.mock.calls)).not.toContain('Site text')
    wrapper.unmount()
  })
})
