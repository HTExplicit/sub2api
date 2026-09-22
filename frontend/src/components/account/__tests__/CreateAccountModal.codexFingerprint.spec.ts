import { defineComponent, nextTick } from 'vue'
import { flushPromises, shallowMount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { accounts, buildExtraInfo, showError, unexpectedNetwork } = vi.hoisted(() => ({
  accounts: {
    create: vi.fn(),
    importCodexSession: vi.fn(),
    createOpenAICodexPAT: vi.fn(),
    generateAuthUrl: vi.fn(),
    exchangeCode: vi.fn(),
    refreshOpenAIToken: vi.fn(),
    probeUpstreamBilling: vi.fn().mockResolvedValue({}),
    syncUpstreamModels: vi.fn().mockResolvedValue({ models: [], metadata: {} }),
    previewModelContextCapacities: vi.fn().mockResolvedValue({ capacity_rows: [] }),
  },
  buildExtraInfo: vi.fn(),
  showError: vi.fn(),
  unexpectedNetwork: vi.fn(() => { throw new Error('Unexpected network in fingerprint regression') }),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts,
    settings: {
      getSettings: vi.fn().mockResolvedValue({}),
      getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false, providers: [] }),
    },
    tlsFingerprintProfiles: { list: vi.fn().mockResolvedValue([]) },
  },
}))
vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn().mockResolvedValue([]),
}))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showSuccess: vi.fn(), showWarning: vi.fn() }),
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isSimpleMode: true }) }))
vi.mock('@/stores/pluginExtensions', () => ({
  usePluginExtensions: () => ({ loaded: true, items: [], refresh: unexpectedNetwork }),
}))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key }),
}))
vi.mock('@/composables/useOpenAIOAuth', async () => {
  const actual = await vi.importActual<typeof import('@/composables/useOpenAIOAuth')>('@/composables/useOpenAIOAuth')
  return { useOpenAIOAuth: () => ({ ...actual.useOpenAIOAuth(), buildExtraInfo }) }
})

import CreateAccountModal from '../CreateAccountModal.vue'

const AccountOperationDialogStub = defineComponent({
  props: ['show', 'job'],
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
})
const SelectStub = defineComponent({
  props: ['modelValue', 'options'],
  emits: ['update:modelValue'],
  template: `<select :value="modelValue" @change="$emit('update:modelValue', $event.target.value)">
    <option v-for="option in options" :key="option.value" :value="option.value">{{ option.label }}</option>
  </select>`,
})
const OAuthFlowStub = defineComponent({
  name: 'OAuthAuthorizationFlow',
  data: () => ({ inputMethod: 'manual', authCode: '', oauthState: '' }),
  methods: { reset() { this.authCode = ''; this.oauthState = ''; this.inputMethod = 'manual' } },
  emits: ['generate-url', 'validate-refresh-token', 'validate-mobile-refresh-token', 'import-codex-session', 'import-codex-pat'],
  template: '<div />',
})

type CreationPath = 'API key' | 'authorization code' | 'refresh-token batch' | 'mobile refresh-token batch' | 'Codex session' | 'Agent Identity' | 'Codex PAT'
const fingerprintSelector = '[data-testid="create-codex-fingerprint-mode-select"]'
const wrappers: VueWrapper[] = []
let restoreNetwork: () => void

function mountModal() {
  const wrapper = shallowMount(CreateAccountModal, {
    props: { show: true, proxies: [], groups: [] },
    global: { stubs: { AccountOperationDialog: AccountOperationDialogStub, Select: SelectStub, OAuthAuthorizationFlow: OAuthFlowStub } },
  })
  wrappers.push(wrapper)
  return wrapper
}

async function clickText(wrapper: VueWrapper, text: string) {
  const button = wrapper.findAll('button').find(candidate =>
    candidate.text() === text || candidate.findAll('span').some(span => span.text() === text))
  expect(button, `button ${text}`).toBeDefined()
  await button!.trigger('click')
}

async function submitPath(wrapper: VueWrapper, path: CreationPath) {
  if (path === 'API key') await clickText(wrapper, 'API Key')
  await wrapper.get('form#create-account-form input[type="text"]').setValue('fingerprint fixture')
  if (path === 'API key') {
    await wrapper.get('form#create-account-form input[type="password"]').setValue('fixture-key')
    await flushPromises()
  }
  await wrapper.get('form#create-account-form').trigger('submit.prevent')
  await flushPromises()

  if (path !== 'API key') {
    const flow = wrapper.getComponent(OAuthFlowStub)
    if (path === 'authorization code') {
      flow.vm.$emit('generate-url')
      await flushPromises()
      flow.vm.authCode = 'fixture-auth-code'
      await nextTick()
      await clickText(wrapper, 'admin.accounts.oauth.completeAuth')
    } else if (path === 'refresh-token batch' || path === 'mobile refresh-token batch') {
      flow.vm.$emit(path === 'refresh-token batch' ? 'validate-refresh-token' : 'validate-mobile-refresh-token', 'fixture-rt-1\nfixture-rt-2')
    } else if (path === 'Codex PAT') {
      flow.vm.$emit('import-codex-pat', 'fixture-pat')
    } else {
      if (path === 'Agent Identity') flow.vm.inputMethod = 'agent_identity'
      flow.vm.$emit('import-codex-session', path === 'Agent Identity'
        ? JSON.stringify({ auth_mode: 'agentIdentity', agent_identity: { agent_runtime_id: 'fixture-runtime' } })
        : 'fixture-session-json')
    }
    await flushPromises()
  }

  const request = path === 'Codex PAT' ? accounts.createOpenAICodexPAT
    : path === 'Codex session' || path === 'Agent Identity' ? accounts.importCodexSession
      : accounts.create
  expect(request).toHaveBeenCalledTimes(path.includes('batch') ? 2 : 1)
  return request.mock.calls.map(([payload]) => payload)
}

beforeEach(() => {
  vi.clearAllMocks()
  accounts.create.mockResolvedValue({ id: 42, platform: 'openai', type: 'apikey' })
  accounts.importCodexSession.mockResolvedValue({ id: 51, kind: 'account_codex_import', status: 'pending' })
  accounts.createOpenAICodexPAT.mockResolvedValue({})
  accounts.generateAuthUrl.mockResolvedValue({ auth_url: 'https://example.invalid/authorize?state=fixture-state', session_id: 'fixture-session' })
  const tokenInfo = { access_token: 'fixture-access-token', refresh_token: 'fixture-refresh-token', email: 'fixture@example.invalid' }
  accounts.exchangeCode.mockResolvedValue(tokenInfo)
  accounts.refreshOpenAIToken.mockResolvedValue(tokenInfo)
  buildExtraInfo.mockReturnValue({ email: 'fixture@example.invalid' })
  vi.stubGlobal('fetch', unexpectedNetwork)
  const send = vi.spyOn(XMLHttpRequest.prototype, 'send').mockImplementation(unexpectedNetwork)
  restoreNetwork = () => send.mockRestore()
})

afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount())
  restoreNetwork()
  vi.unstubAllGlobals()
  expect(showError).not.toHaveBeenCalled()
  expect(unexpectedNetwork).not.toHaveBeenCalled()
})

describe('CreateAccountModal Codex fingerprint ownership', () => {
  it('offers a distinct backend-default choice alongside every explicit mode', async () => {
    const wrapper = mountModal()
    await clickText(wrapper, 'OpenAI')
    const select = wrapper.get<HTMLSelectElement>(fingerprintSelector)
    expect(select.element.value).toBe('default')
    expect(select.findAll('option').map(option => option.attributes('value'))).toEqual(['default', 'off', 'device', 'session', 'full'])
    expect(select.get('option[value="default"]').text()).toBe('admin.accounts.openai.codexFingerprintDefault')
  })

  it.each<CreationPath>(['API key', 'authorization code', 'refresh-token batch', 'mobile refresh-token batch', 'Codex session', 'Agent Identity', 'Codex PAT'])(
    'omits an untouched fingerprint override for %s creation without a plugin', async path => {
      const wrapper = mountModal()
      await clickText(wrapper, 'OpenAI')
      for (const payload of await submitPath(wrapper, path)) {
        expect(payload.extra).not.toHaveProperty('codex_fingerprint_mode')
      }
    },
  )

  it.each(['off', 'session', 'device', 'full'])(
    'preserves explicit %s in every account of a refresh-token batch', async mode => {
      const wrapper = mountModal()
      await clickText(wrapper, 'OpenAI')
      await wrapper.get(fingerprintSelector).setValue(mode)
      for (const payload of await submitPath(wrapper, 'refresh-token batch')) {
        expect(payload.extra.codex_fingerprint_mode).toBe(mode)
      }
    },
  )

  it('does not conflate explicit off with backend default on the import path', async () => {
    const wrapper = mountModal()
    await clickText(wrapper, 'OpenAI')
    await wrapper.get(fingerprintSelector).setValue('off')
    const [payload] = await submitPath(wrapper, 'Codex PAT')
    expect(payload.extra.codex_fingerprint_mode).toBe('off')
  })

  it('removes a stale inherited override when the user returns to default without losing other extra fields', async () => {
    const inherited = { codex_fingerprint_mode: 'full', email: 'fixture@example.invalid', privacy_mode: 'training-disabled' }
    buildExtraInfo.mockReturnValue(inherited)
    const wrapper = mountModal()
    await clickText(wrapper, 'OpenAI')
    await wrapper.get(fingerprintSelector).setValue('session')
    await wrapper.get(fingerprintSelector).setValue('default')
    const [payload] = await submitPath(wrapper, 'authorization code')
    expect(payload.extra).not.toHaveProperty('codex_fingerprint_mode')
    expect(payload.extra).toMatchObject({ email: inherited.email, privacy_mode: inherited.privacy_mode })
    expect(inherited.codex_fingerprint_mode).toBe('full')
  })

  it('resets an explicit choice to omission after closing and reopening the modal', async () => {
    const wrapper = mountModal()
    await clickText(wrapper, 'OpenAI')
    await wrapper.get(fingerprintSelector).setValue('device')
    await clickText(wrapper, 'common.cancel')
    expect(wrapper.emitted('close')).toHaveLength(1)
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await clickText(wrapper, 'OpenAI')
    expect(wrapper.get<HTMLSelectElement>(fingerprintSelector).element.value).toBe('default')
    const [payload] = await submitPath(wrapper, 'Codex session')
    expect(payload.extra).not.toHaveProperty('codex_fingerprint_mode')
  })
})
