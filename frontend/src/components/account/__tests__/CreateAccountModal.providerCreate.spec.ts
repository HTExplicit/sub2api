import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, reactive } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import type { PluginContribution } from '@/api/admin/plugins'
import { createContributionAdmission, contributionAdmission } from '@/components/plugins/contributionAdmission'
import { isAccountCreateProfile } from '@/composables/useAccountCreateProfile'
import manifest from '../../../../../plugins/cindy-provider/manifest.source.json'
import CreateAccountModal from '../CreateAccountModal.vue'

const calls = vi.hoisted(() => ({ create: vi.fn(), catalog: vi.fn(), error: vi.fn(), preview: vi.fn(), probe: vi.fn() }))
const registry = reactive({ loaded: true, items: [] as PluginContribution[], refresh: vi.fn() })
const actor = reactive({ user: { id: 41 }, isAdmin: true, isAuthenticated: true, isSimpleMode: true })
vi.mock('@/stores/pluginExtensions', () => ({ usePluginExtensions: () => registry }))
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
const sha = 'a'.repeat(64), definitionSHA = 'b'.repeat(64)
function profile(): PluginContribution {
  const declaration = structuredClone(manifest.contributions.find(item => item.id === 'cindy-create')!)
  return { ...declaration, account_create: { ...declaration.account_create!, defaults: { concurrency: 7, priority: 23, rate_multiplier: 1.75, load_factor: 6, responses_mode: 'force_chat_completions' } },
    plugin_id: 7, plugin_key: manifest.id, package_sha256: sha, create_definition_digest: definitionSHA, runtime_generation: 3,
    available: true, account_scope: { version: 1, bindings: [{ platform: 'cindy', account_type: 'apikey', rollout_percent: 100 }] } } as PluginContribution
}
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
  registry.loaded = true; registry.items = [profile()]; actor.user.id = 41
  calls.create.mockReset().mockResolvedValue({ id: 99, platform: 'cindy', type: 'apikey' })
  calls.catalog.mockReset().mockResolvedValue(['provider-model-a'])
  calls.preview.mockReset().mockResolvedValue({ capacity_rows: [] })
  calls.probe.mockReset(); calls.error.mockReset()
})
afterEach(() => wrappers.splice(0).forEach(wrapper => wrapper.unmount()))

describe('declared provider account creation', () => {
  it.each([0, 50, 99])('denies ID-less creation for an effective %i percent binding', rollout => {
    const item = profile(); item.account_scope!.bindings[0]!.rollout_percent = rollout
    expect(createContributionAdmission(item, { platform: 'cindy', accountType: 'apikey' }).allowed).toBe(false)
    expect(contributionAdmission(item).allowed).toBe(false)
  })
  it('requires the complete host projection and never reuses account-zero admission', () => {
    const item = profile()
    expect(createContributionAdmission(item, { platform: 'cindy', accountType: 'apikey' }).allowed).toBe(true)
    expect(createContributionAdmission({ ...item, available: false }, { platform: 'cindy', accountType: 'apikey' }).allowed).toBe(false)
    expect(createContributionAdmission(item, { platform: 'openai', accountType: 'apikey' }).allowed).toBe(false)
    expect(isAccountCreateProfile({ ...item, runtime_generation: 0 })).toBe(false)
    expect(isAccountCreateProfile({ ...item, create_definition_digest: undefined })).toBe(false)
    for (const defaults of [{ ...item.account_create!.defaults, priority: -1 }, { ...item.account_create!.defaults, load_factor: 1.5 }, { ...item.account_create!.defaults, load_factor: 10001 }]) {
      expect(isAccountCreateProfile({ ...item, account_create: { ...item.account_create!, defaults } })).toBe(false)
    }
  })
  it('uses the declaration defaults, omits inherited numbers and allows server default-group fallback', async () => {
    const wrapper = open(); await chooseProvider(wrapper)
    expect(vm(wrapper).form).toMatchObject({ concurrency: 7, priority: 23, rate_multiplier: 1.75, load_factor: 6 })
    expect(vm(wrapper).openAIResponsesMode).toBe('force_chat_completions')
    expect(wrapper.get('[data-tour="account-form-priority"]').attributes('min')).toBe('0')
    await wrapper.get('[data-extension-field="device_id"]').setValue('018f9321-6b7c-7a12-9234-123456789abc')
    vm(wrapper).providerFieldValues = { ...vm(wrapper).providerFieldValues, credentials: 'NOT-AN-INPUT', alias: 'NOT-AN-INPUT' }
    await submit(wrapper)
    expect(calls.create).toHaveBeenCalledTimes(1)
    const body = calls.create.mock.calls[0][0]
    expect(body).toMatchObject({ platform: 'cindy', type: 'apikey', group_ids: [], upstream_billing_probe_enabled: false,
      credentials: { api_key: 'HOST-ONLY-KEY', base_url: 'https://api.laxarouter.ai' },
      provider_create: { contribution_id: 'cindy-create', expected_package_sha256: sha, expected_definition_sha256: definitionSHA,
        expected_runtime_generation: 3, values: { device_id: '018f9321-6b7c-7a12-9234-123456789abc' },
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
    vm(wrapper).form.rate_multiplier = 0; vm(wrapper).form.load_factor = null
    await wrapper.get('[data-testid="openai-responses-mode-select"]').setValue('auto')
    await wrapper.get('[data-extension-field="device_id"]').setValue('keep-device-input')
    await submit(wrapper)
    const body = calls.create.mock.calls[0][0]
    expect(body).toMatchObject({ rate_multiplier: 0, load_factor: null, extra: { openai_responses_mode: 'auto' } })
    expect(body.provider_create.inherit_defaults).toEqual(['concurrency', 'priority'])
    expect(vm(wrapper).form.rate_multiplier).toBe(0)
    expect((wrapper.get('[data-extension-field="device_id"]').element as HTMLInputElement).value).toBe('keep-device-input')
    expect(wrapper.emitted('close')).toBeUndefined()
  })
  it('keeps an overlong declared text input editable while blocking its submission', async () => {
    const wrapper = open(); await chooseProvider(wrapper)
    const field = wrapper.get('[data-extension-field="device_id"]')
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
    await wrapper.get('[data-extension-field="device_id"]').setValue('preserved-provider-input')
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
  it('retains an unavailable draft and requires explicit reconciliation before adopting a new package', async () => {
    const wrapper = open(); await chooseProvider(wrapper)
    vm(wrapper).form.concurrency = 9; vm(wrapper).openAIResponsesMode = 'auto'
    await wrapper.get('[data-extension-field="device_id"]').setValue('retained')
    registry.items = []; await flushPromises()
    expect(wrapper.get('[data-extension-field="device_id"]').attributes('disabled')).toBeDefined()
    await submit(wrapper)
    expect(calls.create).not.toHaveBeenCalled()
    const next = profile(); next.package_sha256 = 'c'.repeat(64); next.runtime_generation = 4
    next.account_create!.defaults.priority = 77; next.account_create!.defaults.concurrency = 16
    registry.items = [next]; await flushPromises()
    expect(vm(wrapper).form.priority).toBe(23)
    expect(vm(wrapper).providerCreationBlocked).toBe(true)
    await wrapper.get('[data-testid="provider-create-reconcile"]').trigger('click'); await flushPromises()
    expect(vm(wrapper).form).toMatchObject({ concurrency: 9, priority: 77 })
    expect(vm(wrapper).openAIResponsesMode).toBe('auto')
    expect(vm(wrapper).providerFieldValues.device_id).toBe('retained')
    expect(vm(wrapper).providerCreationBlocked).toBe(false)
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
  it.each([true, false])('keeps ordinary OpenAI independent with registry loaded=%s and does not classify by URL', async loaded => {
    registry.items = []
    registry.loaded = loaded
    const wrapper = open()
    expect(wrapper.find('[data-testid="select-cindy-platform"]').exists()).toBe(false)
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
