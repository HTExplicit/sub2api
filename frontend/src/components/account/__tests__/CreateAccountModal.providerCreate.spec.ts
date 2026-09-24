import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, reactive } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'

import CreateAccountModal from '../CreateAccountModal.vue'
import { cindyCreateChoice } from '@/features/cindy/accountForm'

const calls = vi.hoisted(() => ({ create: vi.fn(), catalog: vi.fn(), error: vi.fn(), preview: vi.fn(), probe: vi.fn() }))

const actor = reactive({ user: { id: 41 }, isAdmin: true, isAuthenticated: true, isSimpleMode: true })

vi.mock('@/stores/auth', () => ({ useAuthStore: () => actor }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: calls.error, showSuccess: vi.fn(), showWarning: vi.fn(), showInfo: vi.fn() }) }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key, locale: { value: 'en' } }) }))
vi.mock('@/api/admin', () => ({ adminAPI: {
  accounts: { create: calls.create, previewModelContextCapacities: calls.preview, probeUpstreamBilling: calls.probe, checkMixedChannelRisk: vi.fn().mockResolvedValue({ has_risk: false }) },
  groups: { getModelAllowlistCandidates: calls.catalog },
  settings: { getSettings: vi.fn().mockResolvedValue({}), getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false, providers: [] }) },
  tlsFingerprintProfiles: { list: vi.fn().mockResolvedValue([]) }
} }))
vi.mock('@/api/admin/accounts', () => ({ getAntigravityDefaultModelMapping: vi.fn().mockResolvedValue([]) }))

const selector = defineComponent({ name: 'ModelWhitelistSelector', props: ['models'], template: '<div data-testid="catalog-ids"><span v-for="model in models">{{ model.id }}</span></div>' })
const selects = defineComponent({ props: ['modelValue', 'options', 'disabled'], emits: ['update:modelValue'],
  template: '<select :value="modelValue" :disabled="disabled" @change="$emit(\'update:modelValue\', $event.target.value)"><option v-for="option in options" :value="option.value">{{ option.label }}</option></select>' })
const wrappers: VueWrapper[] = []

function open() {
  const wrapper = mount(CreateAccountModal, { props: { show: true, groups: [], proxies: [] }, global: { stubs: {
    AccountOperationDialog: { template: '<div><slot/><slot name="footer"/></div>' },
    BaseDialog: { template: '<div><slot/><slot name="footer"/></div>' },
    Select: selects, ModelWhitelistSelector: selector, ModelContextCapacityField: true,
    Icon: true, PlatformIcon: true, ProxySelector: true, ProxyAdBanner: true, GroupSelector: true,
    QuotaLimitCard: true, ConfirmDialog: true, OAuthAuthorizationFlow: true, OpenAIReasoningPolicyFields: true
  } } })
  wrappers.push(wrapper); return wrapper
}
async function chooseProvider(wrapper: VueWrapper) {
  await wrapper.get('[data-testid="select-cindy-platform"]').trigger('click')
  await flushPromises()
}
const vm = (wrapper: VueWrapper) => wrapper.vm as any
async function submit(wrapper: VueWrapper, key = 'HOST-ONLY-KEY') {
  vm(wrapper).form.name = 'Declared account'
  vm(wrapper).apiKeyValue = key
  await wrapper.get('form#create-account-form').trigger('submit.prevent')
  await flushPromises()
}
beforeEach(() => {
    actor.user.id = 41
  calls.create.mockReset().mockResolvedValue({ id: 99, platform: 'cindy', type: 'apikey' })
  calls.catalog.mockReset().mockResolvedValue(['provider-model-a'])
  calls.preview.mockReset().mockResolvedValue({ capacity_rows: [] })
  calls.probe.mockReset(); calls.error.mockReset()
})
afterEach(() => wrappers.splice(0).forEach(wrapper => wrapper.unmount()))

describe('declared provider account creation', () => {

  it('uses the declaration defaults, omits inherited numbers and allows server default-group fallback', async () => {
    const wrapper = open(); await chooseProvider(wrapper)
    const { responses_mode, ...defaults } = cindyCreateChoice.account_create.defaults
    expect(vm(wrapper).form).toMatchObject(defaults)
    expect(vm(wrapper).openAIResponsesMode).toBe(responses_mode)
    expect(wrapper.get('[data-tour="account-form-priority"]').attributes('min')).toBe('0')
    await wrapper.get('#cindy-create-device-id').setValue('018f9321-6b7c-7a12-9234-123456789abc')
    vm(wrapper).providerFieldValues = { ...vm(wrapper).providerFieldValues, credentials: 'NOT-AN-INPUT', alias: 'NOT-AN-INPUT' }
    await submit(wrapper)
    expect(calls.create).toHaveBeenCalledTimes(1)
    const body = calls.create.mock.calls[0][0]
    expect(body).toMatchObject({ platform: 'cindy', type: 'apikey', group_ids: [], upstream_billing_probe_enabled: false,
      credentials: { api_key: 'HOST-ONLY-KEY', base_url: 'https://api.laxarouter.ai' },
      provider_create: { values: { device_id: '018f9321-6b7c-7a12-9234-123456789abc' },
        inherit_defaults: ['concurrency', 'priority', 'rate_multiplier', 'load_factor', 'responses_mode'] } })
    for (const key of ['concurrency', 'priority', 'rate_multiplier', 'load_factor']) expect(body).not.toHaveProperty(key)
    expect(body.credentials).not.toHaveProperty('model_mapping')
    expect(JSON.stringify(body.provider_create)).not.toContain('HOST-ONLY-KEY')
    expect(JSON.stringify(body.provider_create)).not.toContain('NOT-AN-INPUT')
    expect(body.extra || {}).not.toHaveProperty('cindy_device_id')
    expect(body.extra || {}).not.toHaveProperty('cindy_device_id_source')
    expect(calls.probe).not.toHaveBeenCalled()
    const models = wrapper.findComponent(selector).props('models')
    expect(models[0]).toMatchObject({ id: 'provider-model-a' })
    expect(models[0]).not.toHaveProperty('verified')
    expect(models[0]).not.toHaveProperty('managed')
  })
  it('retains explicit zero/null/auto and preserves its draft on a server rejection', async () => {
    calls.create.mockRejectedValue({ response: { status: 400, data: { message: 'No effective group' } } })
    const wrapper = open(); await chooseProvider(wrapper)
    vm(wrapper).form.rate_multiplier = 0
    vm(wrapper).form.load_factor = 6; await flushPromises()
    vm(wrapper).form.load_factor = null; await flushPromises()
    await wrapper.get('[data-testid="openai-responses-mode-select"]').setValue('auto')
    await wrapper.get('#cindy-create-device-id').setValue('keep-device-input')
    await submit(wrapper)
    const body = calls.create.mock.calls[0][0]
    expect(body).toMatchObject({ rate_multiplier: 0, load_factor: null, extra: { openai_responses_mode: 'auto' } })
    expect(body.provider_create.inherit_defaults).toEqual(['concurrency', 'priority'])
    expect(vm(wrapper).form.rate_multiplier).toBe(0)
    expect((wrapper.get('#cindy-create-device-id').element as HTMLInputElement).value).toBe('keep-device-input')
    expect(wrapper.emitted('close')).toBeUndefined()
  })
  it('keeps an overlong declared text input editable while blocking its submission', async () => {
    const wrapper = open(); await chooseProvider(wrapper)
    const field = wrapper.get('#cindy-create-device-id')
    await field.setValue('x'.repeat(65))
    expect(field.attributes('disabled')).toBeUndefined()
    await submit(wrapper)
    expect(calls.create).not.toHaveBeenCalled()
    await field.setValue('fixed-input')
    expect(vm(wrapper).providerCreationBlocked).toBe(false)
  })
  it('keeps independent provider and core drafts across platform, type and endpoint changes', async () => {
    const wrapper = open()
    vm(wrapper).form.platform = 'openai'; await flushPromises(); vm(wrapper).accountCategory = 'apikey'; await flushPromises()
    vm(wrapper).apiKeyBaseUrl = 'https://ordinary.example/v1'; vm(wrapper).apiKeyValue = 'CORE-DRAFT'
    vm(wrapper).form.concurrency = 19; vm(wrapper).openAIResponsesMode = 'force_chat_completions'
    vm(wrapper).upstreamModelsPreviewed = true
    await chooseProvider(wrapper)
    expect(vm(wrapper).upstreamModelsPreviewed).toBe(false)
    vm(wrapper).form.concurrency = 9; vm(wrapper).openAIResponsesMode = 'auto'; vm(wrapper).apiKeyValue = 'PROVIDER-DRAFT'
    await wrapper.get('#cindy-create-device-id').setValue('preserved-provider-input')
    vm(wrapper).accountCategory = 'oauth-based'; await flushPromises()
    expect(vm(wrapper).providerCreationBlocked).toBe(true)
    vm(wrapper).accountCategory = 'apikey'; await flushPromises()
    vm(wrapper).apiKeyBaseUrl = 'https://wrong.example'; await flushPromises()
    expect(vm(wrapper).providerCreationBlocked).toBe(true)
    vm(wrapper).apiKeyBaseUrl = 'https://api.laxarouter.ai'; await flushPromises()
    vm(wrapper).form.platform = 'openai'; await flushPromises()
    expect(vm(wrapper).apiKeyBaseUrl).toBe('https://ordinary.example/v1')
    expect(vm(wrapper).apiKeyValue).toBe('CORE-DRAFT')
    expect(vm(wrapper).form.concurrency).toBe(19)
    expect(vm(wrapper).openAIResponsesMode).toBe('force_chat_completions')
    expect(vm(wrapper).upstreamModelsPreviewed).toBe(true)
    await chooseProvider(wrapper)
    expect(vm(wrapper).apiKeyValue).toBe('PROVIDER-DRAFT')
    expect(vm(wrapper).form.concurrency).toBe(9)
    expect(vm(wrapper).openAIResponsesMode).toBe('auto')
    expect(vm(wrapper).providerFieldValues.device_id).toBe('preserved-provider-input')
    await wrapper.setProps({ show: false }); await flushPromises()
    expect(vm(wrapper).apiKeyValue).toBe('')
    expect(vm(wrapper).providerFieldValues).toEqual({})
  })

  it('drops late catalog replies after switching and preserves earlier IDs on a later catalog failure', async () => {
    let finish!: (ids: string[]) => void
    calls.catalog.mockReturnValueOnce(new Promise(resolve => { finish = resolve })).mockResolvedValue(['fresh-model'])
    const wrapper = open(); await chooseProvider(wrapper)
    vm(wrapper).form.platform = 'openai'; await flushPromises()
    finish(['late-model']); await flushPromises()
    expect(vm(wrapper).providerCatalogModels).toEqual([])
    await chooseProvider(wrapper)
    expect(vm(wrapper).providerCatalogModels.map((model: { id: string }) => model.id)).toEqual(['fresh-model'])
    calls.catalog.mockRejectedValueOnce(new Error('catalog unavailable'))
    await vm(wrapper).providerCreate.refreshCatalog(); await flushPromises()
    expect(wrapper.get('[data-testid="catalog-ids"]').text()).toContain('fresh-model')
    expect(wrapper.text()).not.toContain('late-model')
  })
  it('keeps ordinary OpenAI independent and does not classify by URL', async () => {

    const wrapper = open()
    expect(wrapper.find('[data-testid="select-cindy-platform"]').exists()).toBe(true)
    vm(wrapper).form.platform = 'openai'; await flushPromises(); vm(wrapper).accountCategory = 'apikey'; await flushPromises()
    vm(wrapper).apiKeyBaseUrl = 'https://api.laxarouter.ai'
    await flushPromises()
    expect(vm(wrapper).openAIResponsesMode).toBe('auto')
    expect(vm(wrapper).providerCreationBlocked).toBe(false)
    expect(vm(wrapper).buildOpenAIExtra() || {}).not.toHaveProperty('codex_fingerprint_mode')
    vm(wrapper).codexFingerprintMode = 'off'
    await submit(wrapper, 'ordinary-key')
    const body = calls.create.mock.calls[0][0]
    expect(body.platform).toBe('openai')
    expect(body).not.toHaveProperty('provider_create')
    expect(body.extra.codex_fingerprint_mode).toBe('off')
    expect(body.extra).not.toHaveProperty('openai_responses_mode')
  })
})
