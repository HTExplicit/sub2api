import { defineComponent, type PropType } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const {
  createAccountMock,
  probeUpstreamBillingMock,
  syncUpstreamModelsMock,
  syncUpstreamModelsPreviewMock,
  showWarningMock,
  importCodexSessionMock,
  createOpenAICodexPATMock,
  authIsSimpleMode,
  showSuccess,
  getCindyModelsMock,
  previewModelContextCapacitiesMock,
} = vi.hoisted(() => ({
  createAccountMock: vi.fn(),
  probeUpstreamBillingMock: vi.fn(),
  syncUpstreamModelsMock: vi.fn(),
  syncUpstreamModelsPreviewMock: vi.fn(),
  showWarningMock: vi.fn(),
  importCodexSessionMock: vi.fn(),
  createOpenAICodexPATMock: vi.fn(),
  authIsSimpleMode: { value: true },
  showSuccess: vi.fn(),
  getCindyModelsMock: vi.fn(),
  previewModelContextCapacitiesMock: vi.fn(),
}))

vi.mock('@/stores/app', () => ({
	useAppStore: () => ({
		showError: vi.fn(),
		showSuccess,
		showWarning: showWarningMock,
  }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    get isSimpleMode() {
      return authIsSimpleMode.value
    },
  }),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      create: createAccountMock,
      probeUpstreamBilling: probeUpstreamBillingMock,
      syncUpstreamModels: syncUpstreamModelsMock,
      syncUpstreamModelsPreview: syncUpstreamModelsPreviewMock,
      previewModelContextCapacities: previewModelContextCapacitiesMock,
      checkMixedChannelRisk: vi.fn().mockResolvedValue({ has_risk: false }),
      importCodexSession: importCodexSessionMock,
      createOpenAICodexPAT: createOpenAICodexPATMock,
    },
    settings: {
      getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false, providers: [] }),
      getSettings: vi.fn().mockResolvedValue({}),
    },
    tlsFingerprintProfiles: {
      list: vi.fn().mockResolvedValue([]),
    },
    groups: {
      getModelAllowlistCandidates: getCindyModelsMock,
    },
  },
}))

vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn().mockResolvedValue([]),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

import CreateAccountModal from '../CreateAccountModal.vue'
import ModelContextCapacityField from '../ModelContextCapacityField.vue'
import type { ModelContextCapacityRow, SyncUpstreamModelsResult } from '@/api/admin/accounts'

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: { show: { type: Boolean, default: false } },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
})

const OAuthAuthorizationFlowStub = defineComponent({
  name: 'OAuthAuthorizationFlow',
  props: {
    showManualOption: Boolean,
    showCodexSessionImportOption: Boolean,
    showAgentIdentityOption: Boolean,
    showCodexPatOption: Boolean,
    initialInputMethod: String,
  },
  data: () => ({ inputMethod: 'manual' }),
  emits: ['import-codex-session', 'import-codex-pat'],
  template: `
    <div>
      <button data-testid="import-codex-session" @click="$emit('import-codex-session', 'session-json')">session</button>
      <button data-testid="import-codex-pat" @click="$emit('import-codex-pat', 'pat-token')">pat</button>
    </div>
  `,
})

const SelectStub = defineComponent({
  props: ['modelValue', 'options'],
  emits: ['update:modelValue'],
  template: `
    <select
      v-bind="$attrs"
      :value="modelValue"
      @change="$emit('update:modelValue', $event.target.value)"
    >
      <option v-for="option in options" :key="option.value" :value="option.value">
        {{ option.label }}
      </option>
    </select>
  `,
})

const GroupSelectorStub = defineComponent({
  name: 'GroupSelector',
  props: {
    modelValue: {
      type: Array,
      default: () => [],
    },
  },
  emits: ['update:modelValue'],
  template: `
    <button
      type="button"
      data-testid="select-pricing-groups"
      @click="$emit('update:modelValue', [1, 2])"
    >
      groups
    </button>
  `,
})

const ModelWhitelistSelectorStub = defineComponent({
  name: 'ModelWhitelistSelector',
  components: { ModelContextCapacityField },
  props: {
    modelValue: {
      type: Array as PropType<string[]>,
      default: () => [],
    },
    platform: String,
    syncCredentials: Object,
    models: Array,
    readonly: Boolean,
    hideSync: Boolean,
    capacityRows: { type: Array as PropType<ModelContextCapacityRow[]>, default: () => [] },
    capacityDrafts: { type: Object as PropType<Record<string, string>>, default: () => ({}) },
    syncedModels: Object as PropType<SyncUpstreamModelsResult>,
  },
  emits: ['update:modelValue', 'upstream-synced', 'update:capacityDrafts', 'capacity-validity'],
  data: () => ({
    capacityEditing: {} as Record<string, boolean>,
    capacityValidity: {} as Record<string, boolean>,
  }),
  methods: {
    capacityRow(modelID: string): ModelContextCapacityRow | undefined {
      const direct = this.capacityRows.find(row => row.upstream_model_id === modelID)
      if (direct) return direct
      const aliases = this.capacityRows.filter(row => row.aliases.includes(modelID))
      return aliases.length === 1 ? aliases[0] : undefined
    },
    commitCapacity(modelID: string, draft: string) {
      const upstreamID = this.capacityRow(modelID)?.upstream_model_id ?? modelID
      this.$emit('update:capacityDrafts', { ...this.capacityDrafts, [upstreamID]: draft })
    },
    updateCapacityState(modelID: string, kind: 'editing' | 'validity', value: boolean) {
      if (kind === 'editing') this.capacityEditing[modelID] = value
      else this.capacityValidity[modelID] = value
      this.$emit('capacity-validity',
        !Object.values(this.capacityEditing).some(Boolean) &&
        Object.values(this.capacityValidity).every(Boolean))
    },
  },
  template: `
    <div>
      <button
        type="button"
        data-testid="model-whitelist-selector"
        @click="$emit('update:modelValue', ['public-glm']); $emit('upstream-synced')"
      >{{ models?.map((model) => model.id).join(',') || 'models' }}</button>
      <template v-for="modelID in modelValue" :key="modelID">
        <ModelContextCapacityField
          v-if="capacityRow(modelID)"
          :model-id="capacityRow(modelID).upstream_model_id"
          :row="capacityRow(modelID)"
          :draft="capacityDrafts[capacityRow(modelID).upstream_model_id]"
          @commit="commitCapacity(modelID, $event)"
          @editing="updateCapacityState(modelID, 'editing', $event)"
          @validity="updateCapacityState(modelID, 'validity', $event)"
        />
      </template>
    </div>
  `,
})

function mountModal(groups: any[] = []) {
  return mount(CreateAccountModal, {
    props: { show: true, proxies: [], groups },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        OAuthAuthorizationFlow: OAuthAuthorizationFlowStub,
        ConfirmDialog: true,
        Select: SelectStub,
        Icon: true,
        PlatformIcon: true,
        ProxySelector: true,
        ProxyAdBanner: true,
        GroupSelector: GroupSelectorStub,
        ModelWhitelistSelector: ModelWhitelistSelectorStub,
        QuotaLimitCard: true,
      },
    },
  })
}

async function selectButtonByText(wrapper: ReturnType<typeof mountModal>, text: string) {
  const button = wrapper.findAll('button').find((candidate) => candidate.text().includes(text))
  expect(button).toBeDefined()
  await button?.trigger('click')
}

function buildCapacityRow(
  modelID = 'gpt-5.6-sol',
  values: Partial<ModelContextCapacityRow> = {}
): ModelContextCapacityRow {
  return {
    upstream_model_id: modelID,
    aliases: [],
    editable: true,
    automatic_context_window: 1_050_000,
    automatic_source: 'official',
    effective_context_window: 1_050_000,
    effective_source: 'official',
    capacity_basis: 'total_context',
    ...values,
  }
}

async function mountCapacityModal(rows: ModelContextCapacityRow[]) {
  previewModelContextCapacitiesMock.mockResolvedValue({ capacity_rows: rows })
  const wrapper = mountModal()
  await selectButtonByText(wrapper, 'OpenAI')
  await selectButtonByText(wrapper, 'API Key')
  await wrapper.get('form#create-account-form input[type="text"]').setValue('capacity account')
  await wrapper.get('form#create-account-form input[type="password"]').setValue('test-api-key')
  await vi.waitFor(() => {
    expect(wrapper.getComponent(ModelWhitelistSelectorStub).props('capacityRows')).toEqual(rows)
  })
  return wrapper
}

function capacityField(wrapper: ReturnType<typeof mountModal>, modelID = 'gpt-5.6-sol') {
  const field = wrapper.findAllComponents(ModelContextCapacityField)
    .find(candidate => candidate.props('modelId') === modelID)
  expect(field).toBeDefined()
  return field!
}

async function commitCapacity(wrapper: ReturnType<typeof mountModal>, value: string, modelID = 'gpt-5.6-sol') {
  const field = capacityField(wrapper, modelID)
  await field.get('[data-testid="context-capacity-edit"]').trigger('click')
  await field.get('[data-testid="context-capacity-input"]').setValue(value)
  await field.get('[data-testid="context-capacity-input"]').trigger('keydown', { key: 'Enter' })
}

async function submitApiKeyAccount(
  platform: 'openai' | 'anthropic',
  enableLongContextBilling = false,
  disableUpstreamBillingProbe = false
) {
  const wrapper = mountModal()
  await selectButtonByText(wrapper, platform === 'openai' ? 'OpenAI' : 'admin.accounts.claudeConsole')
  if (platform === 'openai') {
    await selectButtonByText(wrapper, 'API Key')
  }
  await wrapper.get('form#create-account-form input[type="text"]').setValue(`${platform} account`)
  await wrapper.get('form#create-account-form input[type="password"]').setValue('test-api-key')
  if (enableLongContextBilling) {
    await wrapper.get('[data-testid="openai-long-context-billing-toggle"]').trigger('click')
  }
  if (disableUpstreamBillingProbe) {
    await wrapper.get('[data-testid="upstream-billing-auto-probe"]').trigger('click')
  }
  await wrapper.get('form#create-account-form').trigger('submit.prevent')
  await flushPromises()
  return wrapper
}

async function openCodexImportStep(toggleClicks = 0) {
  const wrapper = mountModal()
  await selectButtonByText(wrapper, 'OpenAI')
  for (let click = 0; click < toggleClicks; click += 1) {
    await wrapper.get('[data-testid="openai-long-context-billing-toggle"]').trigger('click')
  }
  await wrapper.get('form#create-account-form input[type="text"]').setValue('Codex import')
  await wrapper.get('form#create-account-form').trigger('submit.prevent')
  return wrapper
}

describe('CreateAccountModal OpenAI long-context billing', () => {
  beforeEach(() => {
    authIsSimpleMode.value = true
    createAccountMock.mockReset().mockResolvedValue({ id: 42, platform: 'openai', type: 'apikey' })
    probeUpstreamBillingMock.mockReset().mockResolvedValue({})
    syncUpstreamModelsMock.mockReset().mockResolvedValue({ models: [], metadata: {} })
    syncUpstreamModelsPreviewMock.mockReset().mockResolvedValue({ models: [], capacity_rows: [] })
    showWarningMock.mockReset()
    importCodexSessionMock.mockReset().mockResolvedValue({
      id: 51,
      kind: 'account_codex_import',
      status: 'pending'
    })
    createOpenAICodexPATMock.mockReset().mockResolvedValue({})
    showSuccess.mockReset()
    getCindyModelsMock.mockReset().mockResolvedValue(['gpt-5.6-luna', 'gpt-image-2'])
    previewModelContextCapacitiesMock.mockReset().mockResolvedValue({ capacity_rows: [] })
  })

  it('commits an exact inline capacity without changing the whitelist or its credential mapping', async () => {
    const wrapper = await mountCapacityModal([buildCapacityRow()])
    const selector = wrapper.getComponent(ModelWhitelistSelectorStub)
    const selection = [...selector.props('modelValue')]
    const field = capacityField(wrapper)
    expect(selector.props('hideSync')).toBe(false)
    expect(wrapper.find('[data-testid="model-context-capacity-panel"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="context-capacity-sync"]').exists()).toBe(false)
    expect(field.get('[data-testid="context-capacity-source"]').text()).toContain('official')

    await field.get('[data-testid="context-capacity-edit"]').trigger('click')
    await field.get('[data-testid="context-capacity-input"]').setValue('1.025M')
    expect(selector.props('capacityDrafts')).toEqual({})
    expect(selector.props('modelValue')).toEqual(selection)
    await field.get('[data-testid="context-capacity-input"]').trigger('keydown', { key: 'Enter' })
    expect(selector.props('capacityDrafts')).toEqual({ 'gpt-5.6-sol': '1.025M' })
    expect(field.get('[data-testid="context-capacity-source"]').text()).toContain('custom')
    expect(selector.props('modelValue')).toEqual(selection)

    await vi.waitFor(() => expect(wrapper.get('[data-tour="account-form-submit"]').attributes('disabled')).toBeUndefined())
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()
    const payload = createAccountMock.mock.calls[0]?.[0]
    expect(payload.model_context_overrides).toEqual({ 'gpt-5.6-sol': 1_025_000 })
    expect(payload.credentials.model_mapping).toEqual(Object.fromEntries(selection.map(modelID => [modelID, modelID])))
    expect(payload.extra).not.toHaveProperty('model_context_overrides')
    expect(previewModelContextCapacitiesMock.mock.calls.every(([params]) => !('api_key' in params))).toBe(true)
    wrapper.unmount()
  })

  it('blocks invalid and unconfirmed inline capacity edits, cancels on Escape and confirms on valid blur', async () => {
    const wrapper = await mountCapacityModal([buildCapacityRow()])
    const selector = wrapper.getComponent(ModelWhitelistSelectorStub)
    const selection = [...selector.props('modelValue')]
    const field = capacityField(wrapper)
    await field.get('[data-testid="context-capacity-edit"]').trigger('click')
    const input = field.get('[data-testid="context-capacity-input"]')
    await input.setValue('1.01')
    await input.trigger('keydown', { key: 'Enter' })
    expect(input.attributes('aria-invalid')).toBe('true')
    expect(wrapper.get('[data-tour="account-form-submit"]').attributes('disabled')).toBeDefined()
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    expect(createAccountMock).not.toHaveBeenCalled()

    await input.setValue('700K')
    expect(wrapper.get('[data-tour="account-form-submit"]').attributes('disabled')).toBeDefined()
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    expect(createAccountMock).not.toHaveBeenCalled()
    expect(selector.props('capacityDrafts')).toEqual({})
    await input.trigger('keydown', { key: 'Escape' })
    expect(field.find('[data-testid="context-capacity-input"]').exists()).toBe(false)
    expect(selector.props('capacityDrafts')).toEqual({})
    expect(selector.props('modelValue')).toEqual(selection)

    await field.get('[data-testid="context-capacity-edit"]').trigger('click')
    await field.get('[data-testid="context-capacity-input"]').setValue('700K')
    await field.get('[data-testid="context-capacity-input"]').trigger('blur')
    await vi.waitFor(() => expect(wrapper.get('[data-tour="account-form-submit"]').attributes('disabled')).toBeUndefined())
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(createAccountMock.mock.calls[0]?.[0]?.model_context_overrides).toEqual({ 'gpt-5.6-sol': 700_000 })
    wrapper.unmount()
  })

  it('retains a confirmed capacity across whitelist and mapping modes, then resets it on modal cancel', async () => {
    const row = buildCapacityRow()
    const wrapper = await mountCapacityModal([row])
    await commitCapacity(wrapper, '1.025M')
    await selectButtonByText(wrapper, 'admin.accounts.modelMapping')
    await selectButtonByText(wrapper, 'admin.accounts.addMapping')
    await wrapper.get('input[placeholder="admin.accounts.requestModel"]').setValue('public-sol')
    await wrapper.get('input[placeholder="admin.accounts.actualModel"]').setValue('gpt-5.6-sol')
    expect(capacityField(wrapper).props('draft')).toBe('1.025M')
    await selectButtonByText(wrapper, 'admin.accounts.modelWhitelist')
    expect(wrapper.getComponent(ModelWhitelistSelectorStub).props('capacityDrafts')).toEqual({ 'gpt-5.6-sol': '1.025M' })
    expect(capacityField(wrapper).props('draft')).toBe('1.025M')

    await selectButtonByText(wrapper, 'common.cancel')
    expect(createAccountMock).not.toHaveBeenCalled()
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')
    await vi.waitFor(() => expect(wrapper.getComponent(ModelWhitelistSelectorStub).props('capacityRows')).toEqual([row]))
    expect(wrapper.getComponent(ModelWhitelistSelectorStub).props('capacityDrafts')).toEqual({})
    expect(capacityField(wrapper).props('draft')).toBeUndefined()
    expect(capacityField(wrapper).get('[data-testid="context-capacity-source"]').text()).toContain('official')
    wrapper.unmount()
  })

  it('owns mapping capacity by its exact target ID even when an earlier row advertises that target as an alias', async () => {
    const decoy = buildCapacityRow('other-upstream-model', {
      aliases: ['gpt-5.6-sol', 'alias-only-target'],
      automatic_context_window: 900_000,
      effective_context_window: 900_000,
    })
    const targetRow = buildCapacityRow('gpt-5.6-sol', {
      aliases: ['public-sol'],
      automatic_context_window: 400_000,
      effective_context_window: 400_000,
    })
    const wrapper = await mountCapacityModal([decoy, targetRow])
    await selectButtonByText(wrapper, 'admin.accounts.modelMapping')
    await selectButtonByText(wrapper, 'admin.accounts.addMapping')
    const requestInput = wrapper.get<HTMLInputElement>('input[placeholder="admin.accounts.requestModel"]')
    const targetInput = wrapper.get<HTMLInputElement>('input[placeholder="admin.accounts.actualModel"]')
    await requestInput.setValue('public-sol')
    await targetInput.setValue('gpt-5.6-sol')
    expect(capacityField(wrapper).props('row')).toEqual(targetRow)
    await targetInput.setValue('alias-only-target')
    expect(capacityField(wrapper, 'alias-only-target').props('row')).toBeUndefined()
    await targetInput.setValue('gpt-5.6-sol')
    await commitCapacity(wrapper, '750K')
    expect(requestInput.element.value).toBe('public-sol')
    expect(targetInput.element.value).toBe('gpt-5.6-sol')
    await vi.waitFor(() => expect(wrapper.get('[data-tour="account-form-submit"]').attributes('disabled')).toBeUndefined())
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()
    const payload = createAccountMock.mock.calls[0]?.[0]
    expect(payload.credentials.model_mapping).toEqual({ 'public-sol': 'gpt-5.6-sol' })
    expect(payload.model_context_overrides).toEqual({ 'gpt-5.6-sol': 750_000 })
    wrapper.unmount()
  })

  it('reprojects selector synchronization without changing selection and persists its preview after create', async () => {
    const row = buildCapacityRow('gpt-5.6-sol', {
      aliases: ['current-projection'],
      automatic_context_window: 258_000,
      automatic_source: 'default',
      effective_context_window: 258_000,
      effective_source: 'default',
    })
    const observation = buildCapacityRow('gpt-5.6-sol', {
      aliases: ['stale-synced-alias'],
      automatic_context_window: 600_000,
      automatic_source: 'upstream',
      effective_context_window: 600_000,
      effective_source: 'upstream',
      upstream: { context_window: 600_000, observed_at: '2026-09-07T00:00:00Z' },
    })
    const result = { models: ['gpt-5.6-sol', 'dynamic-model'], capacity_rows: [observation] }
    const wrapper = await mountCapacityModal([row])
    const selector = wrapper.getComponent(ModelWhitelistSelectorStub)
    const selection = [...selector.props('modelValue')]
    const previewCount = previewModelContextCapacitiesMock.mock.calls.length
    selector.vm.$emit('upstream-synced', result)
    await vi.waitFor(() => expect(previewModelContextCapacitiesMock.mock.calls.length).toBeGreaterThan(previewCount))
    await flushPromises()
    expect(selector.props('modelValue')).toEqual(selection)
    expect(selector.props('syncedModels')).toEqual(result)
    expect(capacityField(wrapper).props('row')).toMatchObject({
      aliases: ['current-projection'],
      automatic_source: 'upstream',
      effective_context_window: 600_000,
      upstream: observation.upstream,
    })
    expect(previewModelContextCapacitiesMock.mock.lastCall?.[0]).toMatchObject({
      model_ids: expect.arrayContaining(['dynamic-model']),
      model_mapping: Object.fromEntries(selection.map(modelID => [modelID, modelID])),
    })
    await commitCapacity(wrapper, '700K')
    await vi.waitFor(() => expect(wrapper.get('[data-tour="account-form-submit"]').attributes('disabled')).toBeUndefined())
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(createAccountMock.mock.calls[0]?.[0]?.model_context_overrides).toEqual({ 'gpt-5.6-sol': 700_000 })
    expect(syncUpstreamModelsMock).toHaveBeenCalledWith(42)
    wrapper.unmount()
  })

  it('keeps a confirmed capacity draft but drops upstream evidence after the endpoint changes', async () => {
    const row = buildCapacityRow('gpt-5.6-sol', {
      automatic_context_window: 258_000,
      automatic_source: 'default',
      effective_context_window: 258_000,
      effective_source: 'default',
    })
    const observation = buildCapacityRow('gpt-5.6-sol', {
      automatic_context_window: 600_000,
      automatic_source: 'upstream',
      effective_context_window: 600_000,
      effective_source: 'upstream',
      upstream: { context_window: 600_000, observed_at: '2026-09-07T00:00:00Z' },
    })
    const wrapper = await mountCapacityModal([row])
    const selector = wrapper.getComponent(ModelWhitelistSelectorStub)
    selector.vm.$emit('upstream-synced', { models: ['gpt-5.6-sol'], capacity_rows: [observation] })
    await flushPromises()
    await commitCapacity(wrapper, '700K')
    await wrapper.get('input[placeholder="https://api.openai.com"]').setValue('https://new-provider.example/v1')
    await vi.waitFor(() => expect(selector.props('capacityRows')).toEqual([row]))
    expect(selector.props('syncedModels')).toBeUndefined()
    expect(selector.props('capacityDrafts')).toEqual({ 'gpt-5.6-sol': '700K' })
    expect(capacityField(wrapper).props('row')).not.toHaveProperty('upstream')
    expect(previewModelContextCapacitiesMock.mock.lastCall?.[0]?.base_url).toBe('https://new-provider.example/v1')
    expect(createAccountMock).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('reasoning policy defaults on without writing untouched switches during creation', async () => {
    const wrapper = await submitApiKeyAccount('openai')
    expect(wrapper.get('[data-testid="openai-reasoning-chatReplay-toggle"]').attributes('aria-checked')).toBe('true')
    expect(wrapper.get('[data-testid="openai-reasoning-signatureRecovery-toggle"]').attributes('aria-checked')).toBe('true')
    expect(createAccountMock.mock.calls[0]?.[0]?.extra).not.toHaveProperty('openai_chat_reasoning_replay_enabled')
    expect(createAccountMock.mock.calls[0]?.[0]?.extra).not.toHaveProperty('openai_reasoning_signature_recovery_enabled')
    wrapper.unmount()
  })

  it('reasoning policy creates an explicit independent opt-out', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('openai account')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('test-api-key')
    await wrapper.get('[data-testid="openai-reasoning-signatureRecovery-toggle"]').trigger('click')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(createAccountMock.mock.calls[0]?.[0]?.extra?.openai_reasoning_signature_recovery_enabled).toBe(false)
    expect(createAccountMock.mock.calls[0]?.[0]?.extra).not.toHaveProperty('openai_chat_reasoning_replay_enabled')
    wrapper.unmount()
  })

  it('reasoning policy preserves untouched import settings and forwards explicit edits', async () => {
    const untouched = await openCodexImportStep()
    await untouched.get('[data-testid="import-codex-session"]').trigger('click')
    await flushPromises()
    expect(importCodexSessionMock.mock.calls[0]?.[0]?.extra).not.toHaveProperty('openai_chat_reasoning_replay_enabled')
    expect(importCodexSessionMock.mock.calls[0]?.[0]?.extra).not.toHaveProperty('openai_reasoning_signature_recovery_enabled')
    untouched.unmount()

    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await wrapper.get('[data-testid="openai-reasoning-chatReplay-toggle"]').trigger('click')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('Codex import')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await wrapper.get('[data-testid="import-codex-pat"]').trigger('click')
    await flushPromises()
    expect(createOpenAICodexPATMock.mock.calls[0]?.[0]?.extra?.openai_chat_reasoning_replay_enabled).toBe(false)
    expect(createOpenAICodexPATMock.mock.calls[0]?.[0]?.extra).not.toHaveProperty('openai_reasoning_signature_recovery_enabled')
    wrapper.unmount()
  })

  it('creates a canonical Cindy API-key account with fixed identity defaults', async () => {
    const wrapper = mountModal([
      { id: 1, name: 'Cindy', platform: 'cindy', wire_platform: 'openai', provider_profile: 'cindy_laxa_v1' }
    ])
    await wrapper.get('[data-testid="select-cindy-platform"]').trigger('click')
    await wrapper.get('[data-testid="select-pricing-groups"]').trigger('click')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('Cindy account')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('cindy-api-key')
    await wrapper.get('[data-testid="cindy-device-id"]').setValue('aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Cindy account',
      platform: 'cindy',
      type: 'apikey',
      credentials: expect.objectContaining({
        api_key: 'cindy-api-key',
        base_url: 'https://api.laxarouter.ai'
      }),
      extra: expect.objectContaining({
        cindy_device_id: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
        cindy_device_id_source: 'input-preserved'
      }),
      concurrency: 3,
      priority: 50,
      rate_multiplier: 1,
      group_ids: [1, 2],
      upstream_billing_probe_enabled: false
    }))
    expect(wrapper.get('[placeholder="https://api.laxarouter.ai"]').attributes('readonly')).toBeDefined()
    expect(wrapper.find('[data-testid="cindy-managed-create-catalog"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="cindy-managed-create-catalog"]').text()).toContain('gpt-5.6-luna')
  })

  it('keeps only the transport selector and removes account-level compatibility modes', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')

    const baseUrl = wrapper
      .findAll<HTMLInputElement>('input')
      .find((candidate) => candidate.attributes('placeholder') === 'https://api.openai.com')
    expect(baseUrl).toBeDefined()
    await baseUrl?.setValue('https://api.laxarouter.ai')
    await flushPromises()

    const responses = wrapper.get<HTMLSelectElement>('[data-testid="openai-responses-mode-select"]')

    expect(responses.element.value).toBe('force_responses')
    expect(wrapper.find('[data-testid="openai-alpha-search-mode-select"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="openai-prompt-cache-key-mode-select"]').exists()).toBe(false)
  })

  afterEach(() => vi.useRealTimers())

  it('sets month and year expiry presets without submitting the account form', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-01-31T12:34:00'))
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('expiry account')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('test-api-key')
    const input = wrapper.get<HTMLInputElement>('input[type="datetime-local"]')

    for (const [label, expected] of [
      ['payment.oneMonth', '2026-02-28T12:34'],
      ['payment.oneYear', '2027-01-31T12:34'],
    ]) {
      const button = wrapper.findAll('button').find((candidate) => candidate.text() === label)!
      expect(button.attributes('type')).toBe('button')
      await button.trigger('click')
      expect(input.element.value).toBe(expected)
      expect(createAccountMock).not.toHaveBeenCalled()
    }

    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(createAccountMock.mock.calls[0]?.[0]?.expires_at).toBe(new Date('2027-01-31T12:34:00').getTime() / 1000)
    wrapper.unmount()
  })

  it('allows a manually entered expiry to override a preset before account creation', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('custom expiry account')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('test-api-key')
    await selectButtonByText(wrapper, 'payment.oneMonth')
    await wrapper.get('input[type="datetime-local"]').setValue('2030-04-15T09:20')

    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(createAccountMock.mock.calls[0]?.[0]?.expires_at).toBe(new Date('2030-04-15T09:20:00').getTime() / 1000)
    wrapper.unmount()
  })

  it('hides only the redundant account toggle when every selected group enables tier pricing', async () => {
    authIsSimpleMode.value = false
    const wrapper = mountModal([
      { id: 1, long_context_pricing_enabled: true },
      { id: 2, long_context_pricing_enabled: true },
    ])

    await selectButtonByText(wrapper, 'OpenAI')
    await wrapper.get('[data-testid="select-pricing-groups"]').trigger('click')

    expect(wrapper.find('[data-testid="openai-long-context-billing-toggle"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="create-openai-ws-mode"]').exists()).toBe(true)
  })

  it('keeps the account toggle when any selected group disables tier pricing', async () => {
    authIsSimpleMode.value = false
    const wrapper = mountModal([
      { id: 1, long_context_pricing_enabled: true },
      { id: 2, long_context_pricing_enabled: false },
    ])

    await selectButtonByText(wrapper, 'OpenAI')
    await wrapper.get('[data-testid="select-pricing-groups"]').trigger('click')

    expect(wrapper.find('[data-testid="openai-long-context-billing-toggle"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="create-openai-ws-mode"]').exists()).toBe(true)
  })

  it('sends false explicitly for normal OpenAI account creation by default', async () => {
    await submitApiKeyAccount('openai')

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBe(false)
  })

  it('omits the upstream request id header from extra when left empty', async () => {
    await submitApiKeyAccount('openai')

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.extra).not.toHaveProperty('upstream_request_id_header')
  })

  it('sends the trimmed upstream request id header in extra when filled', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('openai account')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('test-api-key')
    await wrapper.get('[data-testid="upstream-request-id-header"]').setValue('  X-Oneapi-Request-Id  ')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.extra?.upstream_request_id_header).toBe('X-Oneapi-Request-Id')
  })

  it('omits images_url_to_b64_json from extra by default', async () => {
    await submitApiKeyAccount('openai')

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.extra).not.toHaveProperty('images_url_to_b64_json')
  })

  it('sends images_url_to_b64_json in extra when the toggle is enabled', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('openai account')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('test-api-key')
    await wrapper.get('[data-testid="openai-images-url-to-b64-json-toggle"]').trigger('click')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.extra?.images_url_to_b64_json).toBe(true)
  })

  it('persists upstream model metadata after creating an account from preview', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('OpenCode account')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('test-api-key')
    await wrapper.get('[data-testid="model-whitelist-selector"]').trigger('click')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock).toHaveBeenCalledOnce()
    expect(syncUpstreamModelsMock).toHaveBeenCalledWith(42)
  })

  it('includes the current concrete model mapping in preview credentials', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('test-api-key')
    await wrapper.get('[data-testid="model-whitelist-selector"]').trigger('click')
    await flushPromises()

    expect(wrapper.getComponent(ModelWhitelistSelectorStub).props('syncCredentials')).toMatchObject({
      model_mapping: { 'public-glm': 'public-glm' }
    })
  })

  it('runs formal capability sync after creating an account with explicit mappings', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('Mapped account')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('test-api-key')
    await selectButtonByText(wrapper, 'admin.accounts.modelMapping')
    await selectButtonByText(wrapper, 'admin.accounts.addMapping')
    await wrapper.get('input[placeholder="admin.accounts.requestModel"]').setValue('public-glm')
    await wrapper.get('input[placeholder="admin.accounts.actualModel"]').setValue('glm-5.3')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock.mock.calls[0]?.[0]?.credentials?.model_mapping).toEqual({
      'public-glm': 'glm-5.3'
    })
    expect(syncUpstreamModelsMock).toHaveBeenCalledWith(42)
  })

  it('warns when post-create capability metadata remains incomplete', async () => {
    syncUpstreamModelsMock.mockResolvedValue({
      models: ['x-preview-f-free'],
      warnings: [{ code: 'upstream_model_metadata_incomplete', message: 'metadata incomplete' }],
    })
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('OpenCode account')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('test-api-key')
    await wrapper.get('[data-testid="model-whitelist-selector"]').trigger('click')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(showWarningMock).toHaveBeenCalledWith(
      'admin.accounts.syncUpstreamModelsMetadataIncomplete'
    )
  })

  // namespace 摊平是仅 OAuth 的兼容开关：API Key 走 chat completions 回退桥时由桥自行摊平
  it('shows the Codex namespace flatten toggle only for OpenAI OAuth accounts', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')

    expect(wrapper.find('[data-testid="create-openai-flatten-namespaces-toggle"]').exists()).toBe(
      true
    )

    await selectButtonByText(wrapper, 'API Key')
    expect(wrapper.find('[data-testid="create-openai-flatten-namespaces-toggle"]').exists()).toBe(
      false
    )
  })

  it('enables upstream billing probes by default for new OpenAI API key accounts', async () => {
    await submitApiKeyAccount('openai')

    expect(createAccountMock.mock.calls[0]?.[0]?.upstream_billing_probe_enabled).toBe(true)
  })

  it('waits for the initial upstream billing probe before refreshing the account list', async () => {
    let resolveProbe: (() => void) | undefined
    probeUpstreamBillingMock.mockImplementationOnce(
      () => new Promise<void>((resolve) => {
        resolveProbe = resolve
      })
    )

    const wrapper = await submitApiKeyAccount('openai')

    expect(probeUpstreamBillingMock).toHaveBeenCalledWith(42)
    expect(wrapper.emitted('created')).toBeUndefined()

    resolveProbe?.()
    await flushPromises()

    expect(wrapper.emitted('created')).toHaveLength(1)
  })

  it('sends an explicit disabled state when the create toggle is turned off', async () => {
    await submitApiKeyAccount('openai', false, true)

    expect(createAccountMock.mock.calls[0]?.[0]?.upstream_billing_probe_enabled).toBe(false)
    expect(probeUpstreamBillingMock).not.toHaveBeenCalled()
  })

  it('submits adaptive Kimi protocol endpoints', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'Kimi')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('Kimi adaptive')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-kimi')

    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.credentials).toMatchObject({
      account_mode: 'payg',
      api_protocol: 'adaptive',
      base_url: 'https://api.moonshot.cn/v1',
      api_base_urls: {
        chat_completions: 'https://api.moonshot.cn/v1',
        anthropic: 'https://api.moonshot.cn/anthropic',
        responses: 'https://api.moonshot.cn/v1'
      }
    })
  })

  it('submits adaptive Kimi Coding Plan Responses endpoint', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'Kimi')
    await selectButtonByText(wrapper, 'admin.accounts.cnProviders.accountMode.coding')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('Kimi coding')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-kimi-coding')

    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.credentials).toMatchObject({
      account_mode: 'coding',
      api_protocol: 'adaptive',
      base_url: 'https://api.kimi.com/coding/v1',
      api_base_urls: {
        chat_completions: 'https://api.kimi.com/coding/v1',
        anthropic: 'https://api.kimi.com/coding',
        responses: 'https://api.kimi.com/coding/v1'
      }
    })
  })

  it('uses the edited adaptive Chat endpoint when previewing upstream models', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'Kimi')
    await wrapper
      .get('[data-testid="cn-adaptive-base-url-chat_completions"]')
      .setValue('https://relay.example.com/v1')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-relay')

    expect(wrapper.getComponent(ModelWhitelistSelectorStub).props('syncCredentials')).toMatchObject({
      platform: 'kimi',
      type: 'apikey',
      base_url: 'https://relay.example.com/v1',
      api_key: 'sk-relay'
    })
  })

  it('exposes Agent Identity in the OpenAI authorization methods', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('OpenAI account')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')

    const flow = wrapper.getComponent(OAuthAuthorizationFlowStub)
    expect(flow.props('showManualOption')).toBe(true)
    expect(flow.props('showCodexSessionImportOption')).toBe(true)
    expect(flow.props('showAgentIdentityOption')).toBe(true)
    expect(flow.props('showCodexPatOption')).toBe(true)
    expect(flow.props('initialInputMethod')).toBe('manual')
  })

  it.each([
    ['camelCase', { authMode: 'agentIdentity', agentIdentity: { agentRuntimeId: 'runtime' } }],
    ['nested identity without auth_mode', { agent_identity: { agent_runtime_id: 'runtime' } }],
  ])('accepts backend-compatible %s Agent Identity imports', async (_name, content) => {
    const wrapper = await openCodexImportStep()
    const flow = wrapper.getComponent(OAuthAuthorizationFlowStub)
    flow.vm.inputMethod = 'agent_identity'

    flow.vm.$emit('import-codex-session', JSON.stringify(content))
    await flushPromises()

    expect(importCodexSessionMock).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('created')?.[0]?.[0]).toMatchObject({ id: 51, status: 'pending' })
    expect(showSuccess).not.toHaveBeenCalled()
  })

  it('sends true explicitly when OpenAI long-context billing is enabled', async () => {
    await submitApiKeyAccount('openai', true)

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBe(true)
  })

  it('omits the OpenAI setting for non-OpenAI account creation', async () => {
    await submitApiKeyAccount('anthropic')

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBeUndefined()
    // 上游倍率探测已放宽到全部 API-key 平台：非 OpenAI 平台与 OpenAI 一致，默认开启。
    expect(createAccountMock.mock.calls[0]?.[0]?.upstream_billing_probe_enabled).toBe(true)
  })

  it('sends an explicit disabled state when the non-OpenAI create toggle is turned off', async () => {
    await submitApiKeyAccount('anthropic', false, true)

    expect(createAccountMock.mock.calls[0]?.[0]?.upstream_billing_probe_enabled).toBe(false)
  })

  it('antigravity upstream 创建默认携带上游倍率探测开关', async () => {
    // antigravity upstream 走独立创建 helper，
    // 也必须与其余 API-key 平台一样默认开启探测并传递开关。
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'Antigravity')
    await selectButtonByText(wrapper, 'admin.accounts.types.antigravityApikey')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('antigravity relay')
    const baseInput = wrapper
      .findAll('input')
      .find((candidate) => candidate.attributes('placeholder') === 'https://cloudcode-pa.googleapis.com')
    expect(baseInput).toBeDefined()
    await baseInput?.setValue('https://relay.example')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-upstream')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    const payload = createAccountMock.mock.calls[0]?.[0]
    expect(payload?.platform).toBe('antigravity')
    expect(payload?.type).toBe('apikey')
    expect(payload?.upstream_billing_probe_enabled).toBe(true)
    // 创建成功后前端立即发起一次首探（与其他 apikey 平台一致）。
    expect(probeUpstreamBillingMock).toHaveBeenCalledWith(42)
  })

  it('leaves Codex session import billing ownership to the backend', async () => {
    const wrapper = await openCodexImportStep()
    await wrapper.get('[data-testid="import-codex-session"]').trigger('click')
    await flushPromises()

    expect(importCodexSessionMock).toHaveBeenCalledTimes(1)
    expect(importCodexSessionMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBeUndefined()
  })

  it('leaves Codex PAT import billing ownership to the backend', async () => {
    const wrapper = await openCodexImportStep()
    await wrapper.get('[data-testid="import-codex-pat"]').trigger('click')
    await flushPromises()

    expect(createOpenAICodexPATMock).toHaveBeenCalledTimes(1)
    expect(createOpenAICodexPATMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBeUndefined()
  })

  it('sends explicit true for Codex session import after the toggle is enabled', async () => {
    const wrapper = await openCodexImportStep(1)
    await wrapper.get('[data-testid="import-codex-session"]').trigger('click')
    await flushPromises()

    expect(importCodexSessionMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBe(true)
  })

  it('sends explicit false for Codex session import after the toggle is changed back', async () => {
    const wrapper = await openCodexImportStep(2)
    await wrapper.get('[data-testid="import-codex-session"]').trigger('click')
    await flushPromises()

    expect(importCodexSessionMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBe(false)
  })

  it('sends explicit true for Codex PAT import after the toggle is enabled', async () => {
    const wrapper = await openCodexImportStep(1)
    await wrapper.get('[data-testid="import-codex-pat"]').trigger('click')
    await flushPromises()

    expect(createOpenAICodexPATMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBe(true)
  })

  it('sends explicit false for Codex PAT import after the toggle is changed back', async () => {
    const wrapper = await openCodexImportStep(2)
    await wrapper.get('[data-testid="import-codex-pat"]').trigger('click')
    await flushPromises()

    expect(createOpenAICodexPATMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBe(false)
  })
})
