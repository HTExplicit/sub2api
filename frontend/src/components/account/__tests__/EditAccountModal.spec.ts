import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, type PropType } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import type { ModelContextCapacityRow, SyncUpstreamModelsResult } from '@/api/admin/accounts'

const { updateAccountMock, checkMixedChannelRiskMock, getAvailableModelsMock, getModelContextCapacitiesMock, previewModelContextCapacitiesMock, syncUpstreamModelsMock, authIsSimpleMode } = vi.hoisted(() => ({
  updateAccountMock: vi.fn(),
  checkMixedChannelRiskMock: vi.fn(),
  getAvailableModelsMock: vi.fn(),
  getModelContextCapacitiesMock: vi.fn(),
  previewModelContextCapacitiesMock: vi.fn(),
  syncUpstreamModelsMock: vi.fn(),
  authIsSimpleMode: { value: true }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showInfo: vi.fn()
  })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    get isSimpleMode() {
      return authIsSimpleMode.value
    }
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      update: updateAccountMock,
      checkMixedChannelRisk: checkMixedChannelRiskMock,
      getAvailableModels: getAvailableModelsMock,
      getModelContextCapacities: getModelContextCapacitiesMock,
      previewModelContextCapacities: previewModelContextCapacitiesMock,
      syncUpstreamModels: syncUpstreamModelsMock
    },
    settings: {
      getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false, providers: [] }),
      getSettings: vi.fn().mockResolvedValue({})
    },
    tlsFingerprintProfiles: {
      list: vi.fn().mockResolvedValue([])
    }
  }
}))

vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn()
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

import EditAccountModal from '../EditAccountModal.vue'
import ModelContextCapacityField from '../ModelContextCapacityField.vue'

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: {
    show: {
      type: Boolean,
      default: false
    }
  },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})

const ModelWhitelistSelectorStub = defineComponent({
  name: 'ModelWhitelistSelector',
  components: { ModelContextCapacityField },
  props: {
    modelValue: {
      type: Array as PropType<string[]>,
      default: () => []
    },
    models: {
      type: Array,
      default: () => []
    },
    readonly: {
      type: Boolean,
      default: false
    },
    accountId: Number,
    syncedModels: Object as PropType<SyncUpstreamModelsResult>,
    capacityRows: {
      type: Array as PropType<ModelContextCapacityRow[]>,
      default: () => []
    },
    capacityDrafts: {
      type: Object as PropType<Record<string, string>>,
      default: () => ({})
    }
  },
  emits: ['update:modelValue', 'update:capacityDrafts', 'capacity-validity', 'upstream-synced'],
  setup(props, { emit }) {
    const editing = new Set<string>()
    const invalid = new Set<string>()
    const capacityRow = (modelId: string) => {
      const direct = props.capacityRows.find(row => row.upstream_model_id === modelId)
      if (direct) return direct
      const aliases = props.capacityRows.filter(row => row.aliases.includes(modelId))
      return aliases.length === 1 ? aliases[0] : undefined
    }
    const capacityKey = (modelId: string) => capacityRow(modelId)?.upstream_model_id ?? modelId
    const commitCapacity = (modelId: string, value: string) => {
      emit('update:capacityDrafts', { ...props.capacityDrafts, [capacityKey(modelId)]: value })
    }
    const updateEditing = (modelId: string, value: boolean) => {
      if (value) editing.add(modelId)
      else editing.delete(modelId)
      emit('capacity-validity', editing.size === 0 && invalid.size === 0)
    }
    const updateValidity = (modelId: string, value: boolean) => {
      if (value) invalid.delete(modelId)
      else invalid.add(modelId)
      emit('capacity-validity', editing.size === 0 && invalid.size === 0)
    }
    return { capacityRow, capacityKey, commitCapacity, updateEditing, updateValidity }
  },
  template: `
    <div>
      <span v-if="readonly" data-testid="managed-model-selector">
        {{ models.map((model) => model.id).join(',') }}
      </span>
      <button
        v-else
        type="button"
        data-testid="rewrite-to-snapshot"
        @click="$emit('update:modelValue', ['gpt-5.2-2025-12-11'])"
      >
        rewrite
      </button>
      <span data-testid="model-whitelist-value">
        {{ Array.isArray(modelValue) ? modelValue.join(',') : '' }}
      </span>
      <ModelContextCapacityField
        v-for="modelId in modelValue"
        :key="modelId"
        :model-id="modelId"
        :row="capacityRow(modelId)"
        :draft="capacityDrafts[capacityKey(modelId)]"
        @commit="commitCapacity(modelId, $event)"
        @editing="updateEditing(modelId, $event)"
        @validity="updateValidity(modelId, $event)"
      />
    </div>
  `
})

const SelectStub = defineComponent({
  name: 'SelectStub',
  props: {
    modelValue: {
      type: [String, Number, Boolean, null],
      default: ''
    },
    options: {
      type: Array,
      default: () => []
    }
  },
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
  `
})

const GroupSelectorStub = defineComponent({
  name: 'GroupSelector',
  props: {
    modelValue: {
      type: Array,
      default: () => []
    }
  },
  emits: ['update:modelValue'],
  template: `
    <div data-testid="group-selector">
      <button
        type="button"
        data-testid="set-shadow-group"
        @click="$emit('update:modelValue', [7])"
      >
        group
      </button>
    </div>
  `
})

function buildAccount() {
  return {
    id: 1,
    name: 'OpenAI Key',
    notes: '',
    platform: 'openai',
    type: 'apikey',
    credentials: {
      api_key: 'sk-test',
      base_url: 'https://api.openai.com',
      model_mapping: {
        'gpt-5.2': 'gpt-5.2'
      }
    },
    extra: {},
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    group_ids: [],
    expires_at: null,
    auto_pause_on_expired: false
  } as any
}

function buildOpenAISparkShadowAccount() {
  const account = buildAccount()
  return {
    ...account,
    id: 4,
    name: 'OpenAI Spark Shadow',
    type: 'oauth',
    parent_account_id: 1,
    credentials: {
      access_token: 'parent-access-token',
      refresh_token: 'parent-refresh-token',
      api_key: 'sk-parent',
      base_url: 'https://api.openai.com',
      model_mapping: {
        'gpt-5.3-codex-spark': 'gpt-5.3-codex-spark'
      },
      compact_model_mapping: {
        'gpt-5.3-codex-spark': 'gpt-5.3-codex-spark-compact'
      }
    },
    group_ids: []
  } as any
}

function buildVertexAccount() {
  return {
    id: 2,
    name: 'Vertex SA',
    notes: '',
    platform: 'gemini',
    type: 'service_account',
    credentials: {
      service_account_json: '{"type":"service_account","client_email":"sa@example.iam.gserviceaccount.com","private_key":"-----BEGIN PRIVATE KEY-----\\nMIIE\\n-----END PRIVATE KEY-----\\n"}',
      project_id: 'demo-project',
      client_email: 'sa@example.iam.gserviceaccount.com',
      location: 'us-central1',
      tier_id: 'vertex'
    },
    extra: {},
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    group_ids: [],
    expires_at: null,
    auto_pause_on_expired: false
  } as any
}

function buildAntigravityAccount(projectId = 'configured-project') {
  return {
    id: 3,
    name: 'Antigravity OAuth',
    notes: '',
    platform: 'antigravity',
    type: 'oauth',
    credentials: {
      antigravity_project_id: projectId,
      model_mapping: {
        'gemini-2.5-flash': 'gemini-2.5-flash'
      }
    },
    extra: {},
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    group_ids: [],
    expires_at: null,
    auto_pause_on_expired: false
  } as any
}

function buildGrokOAuthAccount() {
  return {
    id: 5,
    name: 'Grok OAuth',
    notes: '',
    platform: 'grok',
    type: 'oauth',
    credentials: {
      refresh_token: 'grok-rt',
      base_url: 'https://api.x.ai/v1',
      model_mapping: {
        'grok-latest': 'grok-4.3'
      }
    },
    extra: {},
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    group_ids: [],
    expires_at: null,
    auto_pause_on_expired: false
  } as any
}

function buildGrokAPIKeyAccount() {
  return {
    ...buildAccount(),
    id: 6,
    name: 'Grok API Key',
    platform: 'grok',
    credentials: {},
    credentials_status: { has_api_key: true },
    concurrency: 2
  } as any
}

function buildOpenAISetupTokenAccount() {
  return {
    ...buildAccount(),
    type: 'setup-token',
    extra: {
      openai_oauth_responses_websockets_v2_mode: 'ctx_pool',
      openai_oauth_responses_websockets_v2_enabled: true
    }
  } as any
}

function buildOpenAIOAuthParentAccount() {
  return {
    ...buildAccount(),
    id: 7,
    name: 'OpenAI OAuth Parent',
    type: 'oauth',
    parent_account_id: null,
    credentials: { access_token: 'oauth-token' },
    extra: {}
  } as any
}

function mountModal(account = buildAccount(), renderGroupSelector = false) {
  return mount(EditAccountModal, {
    props: {
      show: true,
      account,
      proxies: [],
      groups: []
    },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        Select: SelectStub,
        Icon: true,
        ProxySelector: true,
        GroupSelector: renderGroupSelector ? false : GroupSelectorStub,
        ModelWhitelistSelector: ModelWhitelistSelectorStub
      }
    }
  })
}

function capacityRow(overrides: Partial<ModelContextCapacityRow> = {}): ModelContextCapacityRow {
  return {
    upstream_model_id: 'gpt-5.2',
    aliases: [],
    editable: true,
    automatic_context_window: 400_000,
    automatic_source: 'official',
    effective_context_window: 400_000,
    effective_source: 'official',
    capacity_basis: 'total_context',
    ...overrides
  }
}

function mockCapacityRows(rows: ModelContextCapacityRow[]) {
  getModelContextCapacitiesMock.mockResolvedValue({ capacity_rows: rows })
  previewModelContextCapacitiesMock.mockResolvedValue({ capacity_rows: rows })
}

function findCapacityField(wrapper: ReturnType<typeof mountModal>, modelId = 'gpt-5.2') {
  const field = wrapper.findAllComponents(ModelContextCapacityField)
    .find(candidate => candidate.props('modelId') === modelId)
  if (!field) throw new Error(`Capacity field not found: ${modelId}`)
  return field
}

async function setCapacityDraft(wrapper: ReturnType<typeof mountModal>, value: string, modelId = 'gpt-5.2') {
  const field = findCapacityField(wrapper, modelId)
  await field.get('[data-testid="context-capacity-edit"]').trigger('click')
  const input = field.get('[data-testid="context-capacity-input"]')
  await input.setValue(value)
  await input.trigger('keydown', { key: 'Enter' })
  return field
}

async function changeRestrictionMode(wrapper: ReturnType<typeof mountModal>, mode: 'whitelist' | 'mapping') {
  const key = mode === 'whitelist' ? 'admin.accounts.modelWhitelist' : 'admin.accounts.modelMapping'
  const button = wrapper.findAll('button').find(candidate => candidate.text() === key)
  if (!button) throw new Error(`Restriction mode button not found: ${mode}`)
  await button.trigger('click')
}

describe('EditAccountModal', () => {
  beforeEach(() => {
    authIsSimpleMode.value = true
    getAvailableModelsMock.mockReset()
    getAvailableModelsMock.mockResolvedValue([])
    getModelContextCapacitiesMock.mockReset().mockResolvedValue({ capacity_rows: [] })
    previewModelContextCapacitiesMock.mockReset().mockResolvedValue({ capacity_rows: [] })
    syncUpstreamModelsMock.mockReset().mockResolvedValue({ models: [], capacity_rows: [] })
  })

  it('clears only the inline target override on empty confirmation and omits managed Extra snapshots', async () => {
    const account = buildAccount()
    account.extra = {
      model_context_overrides: { 'gpt-5.2': 500_000, untouched: 700_000 },
      upstream_model_context_capacities: { stale: true },
      upstream_model_metadata: { stale: true },
      unrelated_setting: 'preserved'
    }
    mockCapacityRows([capacityRow({
      custom_context_window: 500_000,
      effective_context_window: 500_000,
      effective_source: 'custom'
    })])
    updateAccountMock.mockReset().mockResolvedValue(account)
    const wrapper = mountModal(account)
    await flushPromises()
    expect(getModelContextCapacitiesMock).toHaveBeenCalledWith(1, expect.any(AbortSignal))
    expect(previewModelContextCapacitiesMock).toHaveBeenCalledWith(expect.objectContaining({
      account_id: 1,
      model_ids: expect.arrayContaining(['gpt-5.2', 'gpt-5.6-sol'])
    }), expect.any(AbortSignal))
    expect(previewModelContextCapacitiesMock.mock.calls[0]?.[0]).not.toHaveProperty('api_key')
    const selector = wrapper.findComponent(ModelWhitelistSelectorStub)
    const modelSelection = [...selector.props('modelValue')]
    expect(findCapacityField(wrapper).get('[data-testid="context-capacity-source"]').text()).toContain('sources.custom')
    const field = await setCapacityDraft(wrapper, '')
    expect(field.find('[data-testid="context-capacity-input"]').exists()).toBe(false)
    expect(field.get('[data-testid="context-capacity-source"]').text()).toContain('sources.official')
    expect(selector.props('capacityDrafts')).toEqual({ 'gpt-5.2': '' })
    expect(selector.props('modelValue')).toEqual(modelSelection)
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    const payload = updateAccountMock.mock.calls[0]?.[1]
    expect(payload.model_context_overrides).toEqual({ 'gpt-5.2': null })
    expect(payload.extra).not.toHaveProperty('model_context_overrides')
    expect(payload.extra).not.toHaveProperty('upstream_model_context_capacities')
    expect(payload.extra).not.toHaveProperty('upstream_model_metadata')
    expect(payload.extra.unrelated_setting).toBe('preserved')
    expect(account.extra.model_context_overrides).toEqual({ 'gpt-5.2': 500_000, untouched: 700_000 })
    wrapper.unmount()
  })

  it('retains confirmed capacity drafts across restriction modes and resets them on cancel', async () => {
    mockCapacityRows([capacityRow()])
    updateAccountMock.mockReset()
    const wrapper = mountModal()
    await flushPromises()
    await setCapacityDraft(wrapper, '1.05M')
    expect(wrapper.findComponent(ModelWhitelistSelectorStub).props('capacityDrafts')).toEqual({ 'gpt-5.2': '1.05M' })
    await changeRestrictionMode(wrapper, 'mapping')
    expect(wrapper.find('[data-testid="model-context-capacity-panel"]').exists()).toBe(false)
    await changeRestrictionMode(wrapper, 'whitelist')
    const retained = findCapacityField(wrapper)
    expect(retained.props('draft')).toBe('1.05M')
    expect(retained.get('[data-testid="context-capacity-source"]').text()).toContain('sources.custom')
    expect(retained.find('[data-testid="context-capacity-input"]').exists()).toBe(false)
    const cancel = wrapper.findAll('button').find(button => button.text() === 'common.cancel')
    await cancel!.trigger('click')
    expect(updateAccountMock).not.toHaveBeenCalled()
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(wrapper.findComponent(ModelWhitelistSelectorStub).props('capacityDrafts')).toEqual({})
    expect(findCapacityField(wrapper).props('draft')).toBeUndefined()
    expect(findCapacityField(wrapper).get('[data-testid="context-capacity-source"]').text()).toContain('sources.official')
    wrapper.unmount()
  })

  it('keeps protected OAuth capacity inline and read-only without losing the selector account ID', async () => {
    const account = buildOpenAIOAuthParentAccount()
    account.credentials.model_mapping = { 'gpt-5.2': 'gpt-5.2' }
    mockCapacityRows([capacityRow({
      editable: false,
      automatic_context_window: 272_000,
      automatic_source: 'protected',
      effective_context_window: 272_000,
      effective_source: 'protected'
    })])
    updateAccountMock.mockReset().mockResolvedValue(account)
    const wrapper = mountModal(account)
    await flushPromises()
    expect(wrapper.findComponent(ModelWhitelistSelectorStub).props('accountId')).toBe(account.id)
    expect(wrapper.find('[data-testid="context-capacity-input"]').exists()).toBe(false)
    expect(findCapacityField(wrapper).find('[data-testid="context-capacity-edit"]').exists()).toBe(false)
    expect(findCapacityField(wrapper).get('[data-testid="context-capacity-source"]').text()).toContain('sources.protected')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]).not.toHaveProperty('model_context_overrides')
    wrapper.unmount()
  })

  it('owns mapping capacity by its actual target ID even when an earlier row aliases that same ID', async () => {
    const account = buildAccount()
    account.credentials.model_mapping = { 'public-sol': 'gpt-5.6-sol' }
    mockCapacityRows([
      capacityRow({ upstream_model_id: 'different-upstream', aliases: ['gpt-5.6-sol'], automatic_source: 'upstream' }),
      capacityRow({ upstream_model_id: 'gpt-5.6-sol', aliases: ['public-sol'] })
    ])
    updateAccountMock.mockReset().mockResolvedValue(account)
    const wrapper = mountModal(account)
    await flushPromises()
    const field = findCapacityField(wrapper, 'gpt-5.6-sol')
    expect(field.props('row')?.upstream_model_id).toBe('gpt-5.6-sol')
    expect(field.get('[data-testid="context-capacity-source"]').text()).toContain('sources.official')
    expect(wrapper.find('[data-testid="model-context-capacity-panel"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="context-capacity-sync"]').exists()).toBe(false)
    await setCapacityDraft(wrapper, '1.05M', 'gpt-5.6-sol')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.model_context_overrides).toEqual({ 'gpt-5.6-sol': 1_050_000 })
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toEqual({ 'public-sol': 'gpt-5.6-sol' })
    expect(syncUpstreamModelsMock).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('never adopts another upstream row merely because its alias matches the mapping target', async () => {
    const account = buildAccount()
    account.credentials.model_mapping = { 'public-sol': 'target-without-own-row' }
    mockCapacityRows([capacityRow({
      upstream_model_id: 'different-upstream',
      aliases: ['target-without-own-row']
    })])
    const wrapper = mountModal(account)
    await flushPromises()
    expect(findCapacityField(wrapper, 'target-without-own-row').props('row')).toBeUndefined()
    wrapper.unmount()
  })

  it.each([
    { name: 'valid but unconfirmed', value: '1.05M', invalid: false },
    { name: 'invalid after blur', value: 'not-a-capacity', invalid: true }
  ])('blocks Save for $name inline capacity and allows Save after Escape cancels it', async ({ value, invalid }) => {
    const account = buildAccount()
    mockCapacityRows([capacityRow()])
    updateAccountMock.mockReset().mockResolvedValue(account)
    const wrapper = mountModal(account)
    await flushPromises()
    const field = findCapacityField(wrapper)
    await field.get('[data-testid="context-capacity-edit"]').trigger('click')
    const input = field.get('[data-testid="context-capacity-input"]')
    await input.setValue(value)
    if (invalid) await input.trigger('blur')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(updateAccountMock).not.toHaveBeenCalled()
    expect(wrapper.findComponent(ModelWhitelistSelectorStub).props('capacityDrafts')).toEqual({})
    expect(field.find('[data-testid="context-capacity-input"]').exists()).toBe(true)
    await input.trigger('keydown', { key: 'Escape' })
    expect(field.find('[data-testid="context-capacity-input"]').exists()).toBe(false)
    expect(field.get('[data-testid="context-capacity-source"]').text()).toContain('sources.official')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]).not.toHaveProperty('model_context_overrides')
    wrapper.unmount()
  })

  it('reprojects selector synchronization against unsaved mapping aliases without replacing confirmed drafts', async () => {
    const account = buildAccount()
    account.credentials.model_mapping = { 'saved-alias': 'gpt-5.2' }
    mockCapacityRows([capacityRow({ aliases: ['saved-alias'] })])
    const wrapper = mountModal(account)
    await flushPromises()
    await setCapacityDraft(wrapper, '1.05M')
    const aliasInput = wrapper.findAll('input').find(input => (input.element as HTMLInputElement).value === 'saved-alias')!
    await aliasInput.setValue('draft-alias')
    await changeRestrictionMode(wrapper, 'whitelist')
    const selector = wrapper.findComponent(ModelWhitelistSelectorStub)
    const synced: SyncUpstreamModelsResult = {
      models: ['gpt-5.2'],
      capacity_rows: [capacityRow({
        aliases: ['saved-alias'],
        upstream: { context_window: 350_000, observed_at: '2026-09-07T10:00:00Z' },
        automatic_context_window: 350_000,
        automatic_source: 'upstream',
        effective_context_window: 350_000,
        effective_source: 'upstream'
      })]
    }
    const projected = capacityRow({
      aliases: ['draft-alias'],
      upstream: synced.capacity_rows![0].upstream
    })
    previewModelContextCapacitiesMock.mockResolvedValue({ capacity_rows: [projected] })
    selector.vm.$emit('upstream-synced', synced)
    await flushPromises()
    expect(previewModelContextCapacitiesMock).toHaveBeenLastCalledWith(expect.objectContaining({
      account_id: account.id,
      model_mapping: { 'draft-alias': 'gpt-5.2' }
    }), expect.any(AbortSignal))
    expect(selector.props('capacityRows')).toEqual([projected])
    expect(selector.props('syncedModels')).toEqual(synced)
    expect(selector.props('capacityDrafts')).toEqual({ 'gpt-5.2': '1.05M' })
    await changeRestrictionMode(wrapper, 'mapping')
    expect(findCapacityField(wrapper).props('draft')).toBe('1.05M')
    expect(findCapacityField(wrapper).props('row')?.aliases).toEqual(['draft-alias'])
    expect(syncUpstreamModelsMock).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('does not adopt old upstream evidence when selector synchronization follows an endpoint change', async () => {
    vi.useFakeTimers()
    const account = buildAccount()
    mockCapacityRows([capacityRow()])
    const wrapper = mountModal(account)
    try {
      await flushPromises()
      await setCapacityDraft(wrapper, '1.05M')
      const selector = wrapper.findComponent(ModelWhitelistSelectorStub)
      const synced: SyncUpstreamModelsResult = {
        models: ['gpt-5.2'],
        capacity_rows: [capacityRow({
          upstream: { context_window: 350_000, observed_at: '2026-09-07T10:00:00Z' },
          automatic_context_window: 350_000,
          automatic_source: 'upstream',
          effective_context_window: 350_000,
          effective_source: 'upstream'
        })]
      }
      selector.vm.$emit('upstream-synced', synced)
      await flushPromises()
      expect(selector.props('syncedModels')).toEqual(synced)
      const freshRow = capacityRow({
        automatic_context_window: 258_000,
        automatic_source: 'default',
        effective_context_window: 258_000,
        effective_source: 'default'
      })
      previewModelContextCapacitiesMock.mockResolvedValue({ capacity_rows: [freshRow] })
      const endpointInput = wrapper.findAll('input').find(input => (input.element as HTMLInputElement).value === 'https://api.openai.com')!
      await endpointInput.setValue('https://new-provider.example/v1')
      selector.vm.$emit('upstream-synced', synced)
      await flushPromises()
      await vi.advanceTimersByTimeAsync(250)
      await flushPromises()
      expect(previewModelContextCapacitiesMock).toHaveBeenLastCalledWith(expect.objectContaining({
        base_url: 'https://new-provider.example/v1'
      }), expect.any(AbortSignal))
      expect(selector.props('capacityRows')).toEqual([freshRow])
      expect(findCapacityField(wrapper).props('row')?.upstream).toBeUndefined()
      expect(selector.props('capacityDrafts')).toEqual({ 'gpt-5.2': '1.05M' })
      expect(syncUpstreamModelsMock).not.toHaveBeenCalled()
    } finally {
      wrapper.unmount()
      vi.useRealTimers()
    }
  })

  it.each(['apikey', 'oauth', 'setup-token'])('reasoning policy defaults on and omits untouched edits for %s', async (type) => {
    const account = { ...buildAccount(), type }
    updateAccountMock.mockReset().mockResolvedValue(account)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })
    const wrapper = mountModal(account)
    expect(wrapper.get('[data-testid="openai-reasoning-chatReplay-toggle"]').attributes('aria-checked')).toBe('true')
    expect(wrapper.get('[data-testid="openai-reasoning-signatureRecovery-toggle"]').attributes('aria-checked')).toBe('true')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('openai_chat_reasoning_replay_enabled')
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('openai_reasoning_signature_recovery_enabled')
    wrapper.unmount()
  })

  it('reasoning policy preserves explicit false and malformed historical values until changed', async () => {
    const account = buildAccount()
    account.extra = { openai_chat_reasoning_replay_enabled: false, openai_reasoning_signature_recovery_enabled: 'invalid-old-value' }
    updateAccountMock.mockReset().mockResolvedValue(account)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })
    const wrapper = mountModal(account)
    expect(wrapper.get('[data-testid="openai-reasoning-chatReplay-toggle"]').attributes('aria-checked')).toBe('false')
    expect(wrapper.get('[data-testid="openai-reasoning-signatureRecovery-toggle"]').attributes('aria-checked')).toBe('false')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('openai_chat_reasoning_replay_enabled')
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('openai_reasoning_signature_recovery_enabled')
    expect(account.extra.openai_reasoning_signature_recovery_enabled).toBe('invalid-old-value')
    wrapper.unmount()
  })

  it('reasoning policy allows an explicit enable without overwriting the other switch', async () => {
    const account = buildAccount()
    account.extra = { openai_chat_reasoning_replay_enabled: false, openai_reasoning_signature_recovery_enabled: false }
    updateAccountMock.mockReset().mockResolvedValue(account)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })
    const wrapper = mountModal(account)
    await wrapper.get('[data-testid="openai-reasoning-chatReplay-toggle"]').trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_chat_reasoning_replay_enabled).toBe(true)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('openai_reasoning_signature_recovery_enabled')
    wrapper.unmount()
  })

  it('reasoning policy is available for Cindy wire accounts but not native Grok', () => {
    const cindy = mountModal({ ...buildAccount(), platform: 'cindy', wire_platform: 'openai' })
    expect(cindy.find('[data-testid="openai-reasoning-policy"]').exists()).toBe(true)
    cindy.unmount()
    const grok = mountModal(buildGrokAPIKeyAccount())
    expect(grok.find('[data-testid="openai-reasoning-policy"]').exists()).toBe(false)
    grok.unmount()
  })

  it('does not expose account-level compatibility selectors', () => {
    const cindy = buildAccount()
    cindy.platform = 'cindy'
    cindy.is_cindy = true
    cindy.credentials.base_url = 'https://api.laxarouter.ai'
    cindy.extra = {}
    const cindyWrapper = mountModal(cindy)
    expect(cindyWrapper.find('[data-testid="openai-alpha-search-mode-select"]').exists()).toBe(false)
    expect(cindyWrapper.find('[data-testid="openai-prompt-cache-key-mode-select"]').exists()).toBe(false)

    const ordinaryWrapper = mountModal(buildAccount())
    expect(ordinaryWrapper.find('[data-testid="openai-alpha-search-mode-select"]').exists()).toBe(false)
    expect(ordinaryWrapper.find('[data-testid="openai-prompt-cache-key-mode-select"]').exists()).toBe(false)
  })

  it('keeps canonical Cindy identity and fixed endpoint on edit submission', async () => {
    const cindy = buildAccount()
    cindy.platform = 'cindy'
    cindy.is_cindy = true
    cindy.credentials.base_url = 'https://api.laxarouter.ai'
    cindy.extra = {}
    updateAccountMock.mockResolvedValue(cindy)

    const wrapper = mountModal(cindy)
    await flushPromises()
    const baseUrl = wrapper
      .findAll<HTMLInputElement>('input')
      .find((input) => input.element.value === 'https://api.laxarouter.ai')
    expect(baseUrl?.attributes('readonly')).toBeDefined()
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledWith(cindy.id, expect.objectContaining({
      credentials: expect.objectContaining({ base_url: 'https://api.laxarouter.ai' }),
    }))
    const submitted = updateAccountMock.mock.calls[0]?.[1] as { extra?: Record<string, unknown> }
    expect(submitted.extra).not.toHaveProperty('openai_alpha_search_mode')
    expect(submitted.extra).not.toHaveProperty('openai_prompt_cache_key_mode')
  })

  it('preserves legacy compatibility values for rollback without exposing controls', async () => {
    const cindy = buildAccount()
    cindy.platform = 'cindy'
    cindy.is_cindy = true
    cindy.credentials.base_url = 'https://api.laxarouter.ai'
    cindy.extra = {
      openai_responses_mode: 'force_responses',
      openai_alpha_search_mode: 'responses_web_search',
      openai_prompt_cache_key_mode: 'sha256_64'
    }

    updateAccountMock.mockResolvedValue(cindy)
    const wrapper = mountModal(cindy)
    expect(wrapper.find('[data-testid="openai-alpha-search-mode-select"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="openai-prompt-cache-key-mode-select"]').exists()).toBe(false)
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    const submitted = updateAccountMock.mock.calls[0]?.[1] as { extra?: Record<string, unknown> }
    expect(submitted.extra).not.toHaveProperty('openai_alpha_search_mode')
    expect(submitted.extra).not.toHaveProperty('openai_prompt_cache_key_mode')
    expect(cindy.extra?.openai_alpha_search_mode).toBe('responses_web_search')
    expect(cindy.extra?.openai_prompt_cache_key_mode).toBe('sha256_64')
  })

  it('keeps the Cindy catalog read-only while preserving rollback mappings and custom overrides', async () => {
    const cindy = buildAccount()
    cindy.platform = 'cindy'
    cindy.is_cindy = true
    cindy.credentials.base_url = 'https://api.laxarouter.ai'
    cindy.credentials.model_mapping = {
      'gpt-5.6-luna': 'openai/gpt-5.6-luna',
      'gpt-5.6-sol': 'openai/gpt-5.6-sol',
      'gpt-5.6-terra': 'openai/gpt-5.6-terra',
      'gpt-5.4-mini': 'openai/gpt-5.6-luna',
      'gpt-5.4': 'custom/gpt-5.4',
      'customer-latest': 'openai/gpt-5.6-sol'
    }
    cindy.extra = {}
    getAvailableModelsMock.mockResolvedValue([
      {
        id: 'gpt-5.6-luna',
        live_upstream_id: 'openai/gpt-5.6-luna',
        display_name: 'GPT-5.6 Luna',
        context_window: 1_050_000,
        base_context_window: 1_050_000,
        codex_context_window: 1_050_000,
        max_output_tokens: 128_000,
        endpoints: ['responses'],
        source_revision: 'cindy-v0.1.52',
        managed: true,
        verified: true,
        public_model: true
      },
      {
        id: 'gpt-5.6-sol',
        live_upstream_id: 'openai/gpt-5.6-sol',
        display_name: 'GPT-5.6 Sol',
        context_window: 372_000,
        base_context_window: 1_050_000,
        codex_context_window: 372_000,
        max_output_tokens: 128_000,
        endpoints: ['responses'],
        source_revision: 'cindy-v0.1.52',
        managed: true,
        verified: true,
        public_model: true
      },
      {
        id: 'gpt-5.6-terra',
        live_upstream_id: 'openai/gpt-5.6-terra',
        display_name: 'GPT-5.6 Terra',
        context_window: 372_000,
        base_context_window: 1_050_000,
        codex_context_window: 372_000,
        max_output_tokens: 128_000,
        endpoints: ['responses'],
        source_revision: 'cindy-v0.1.52',
        managed: true,
        verified: true,
        public_model: true
      },
      {
        id: 'gpt-5.4',
        live_upstream_id: 'openai/gpt-5.6-sol',
        display_name: 'GPT-5.4 compatibility alias',
        context_window: 372_000,
        base_context_window: 1_050_000,
        codex_context_window: 372_000,
        max_output_tokens: 128_000,
        endpoints: ['responses'],
        source_revision: 'cindy-v0.1.52',
        alias_target: 'gpt-5.6-sol',
        managed: true
      },
      {
        id: 'gpt-5.4-mini',
        live_upstream_id: 'openai/gpt-5.6-luna',
        display_name: 'GPT-5.4 Mini compatibility alias',
        context_window: 1_050_000,
        base_context_window: 1_050_000,
        codex_context_window: 1_050_000,
        max_output_tokens: 128_000,
        endpoints: ['responses'],
        source_revision: 'cindy-v0.1.52',
        alias_target: 'gpt-5.6-luna',
        managed: true
      }
    ])
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(cindy)

    const wrapper = mountModal(cindy)
    await flushPromises()

    expect(getAvailableModelsMock).toHaveBeenCalledWith(cindy.id)
    const whitelistTab = wrapper
      .findAll('button')
      .find(button => button.text().includes('admin.accounts.modelWhitelist'))
    expect(whitelistTab).toBeDefined()
    await whitelistTab!.trigger('click')
    expect(wrapper.get('[data-testid="managed-model-selector"]').text()).toContain('gpt-5.6-sol')
    expect(
      wrapper.findAllComponents(ModelWhitelistSelectorStub).filter(selector => !selector.props('readonly'))
    ).toHaveLength(0)

    const mappingTab = wrapper
      .findAll('button')
      .find(button => button.text().includes('admin.accounts.modelMapping'))
    expect(mappingTab).toBeDefined()
    await mappingTab!.trigger('click')

    const managedAlias = wrapper
      .findAll('[data-testid="cindy-managed-alias"]')
      .find(alias => alias.text().includes('gpt-5.4-mini'))
    expect(managedAlias).toBeDefined()
    expect(managedAlias!.text()).toContain('gpt-5.4-mini')
    expect(managedAlias!.text()).toContain('openai/gpt-5.6-luna')

    const editableMappingInputs = wrapper
      .get('[data-testid="editable-model-mappings"]')
      .findAll<HTMLInputElement>('input')
    expect(editableMappingInputs.map(input => input.element.value)).toEqual([
      'gpt-5.4',
      'custom/gpt-5.4',
      'customer-latest',
      'openai/gpt-5.6-sol'
    ])
    await editableMappingInputs[1].setValue('custom/gpt-5.4-v2')

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toEqual({
      'gpt-5.6-luna': 'openai/gpt-5.6-luna',
      'gpt-5.6-sol': 'openai/gpt-5.6-sol',
      'gpt-5.6-terra': 'openai/gpt-5.6-terra',
      'gpt-5.4-mini': 'openai/gpt-5.6-luna',
      'gpt-5.4': 'custom/gpt-5.4-v2',
      'customer-latest': 'openai/gpt-5.6-sol'
    })
  })

  it('keeps Cindy catalog loading and failure states bounded to the managed projection', async () => {
    const cindy = buildAccount()
    cindy.platform = 'cindy'
    cindy.is_cindy = true
    cindy.credentials.base_url = 'https://api.laxarouter.ai'
    cindy.extra = {}
    let rejectModels: (reason?: unknown) => void = () => undefined
    getAvailableModelsMock.mockReturnValue(new Promise((_, reject) => {
      rejectModels = reject
    }))

    const wrapper = mountModal(cindy)
    expect(wrapper.text()).toContain('admin.accounts.cindyCatalogLoading')
    expect(wrapper.get('button[form="edit-account-form"]').attributes('disabled')).toBeDefined()

    rejectModels(new Error('catalog unavailable'))
    await flushPromises()

    expect(wrapper.text()).toContain('admin.accounts.cindyCatalogLoadFailed')
    expect(wrapper.get('button[form="edit-account-form"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.find('[data-testid="model-whitelist-value"]').exists()).toBe(false)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(updateAccountMock.mock.calls.at(-1)?.[1]?.credentials?.model_mapping).toEqual({
      'gpt-5.2': 'gpt-5.2'
    })
  })

  afterEach(() => vi.useRealTimers())

  it('sets expiry presets from now instead of extending the saved expiry', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2028-02-29T12:34:00'))
    const account = buildAccount()
    account.expires_at = new Date('2030-06-15T09:00:00').getTime() / 1000
    updateAccountMock.mockReset().mockResolvedValue(account)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })
    const wrapper = mountModal(account)
    const input = wrapper.get<HTMLInputElement>('input[type="datetime-local"]')

    for (const [label, expected] of [
      ['payment.oneMonth', '2028-03-29T12:34'],
      ['payment.oneYear', '2029-02-28T12:34'],
    ]) {
      const button = wrapper.findAll('button').find((candidate) => candidate.text() === label)!
      expect(button.attributes('type')).toBe('button')
      await button.trigger('click')
      expect(input.element.value).toBe(expected)
      expect(updateAccountMock).not.toHaveBeenCalled()
    }

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    expect(updateAccountMock.mock.calls[0]?.[1]?.expires_at).toBe(new Date('2029-02-28T12:34:00').getTime() / 1000)
    wrapper.unmount()
  })

  it('can clear a selected expiry preset before saving the account', async () => {
    const account = buildAccount()
    updateAccountMock.mockReset().mockResolvedValue(account)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })
    const wrapper = mountModal(account)
    const button = wrapper.findAll('button').find((candidate) => candidate.text() === 'payment.oneYear')!
    await button.trigger('click')
    const input = wrapper.get<HTMLInputElement>('input[type="datetime-local"]')
    expect(input.element.value).not.toBe('')
    await input.setValue('')

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    expect(updateAccountMock.mock.calls[0]?.[1]?.expires_at).toBe(0)
    wrapper.unmount()
  })

  it('allows removing assigned inactive groups and undoing the selection before saving', async () => {
    authIsSimpleMode.value = false
    const account = buildAccount()
    const activeGroup = {
      id: 1,
      name: 'Active group',
      platform: 'openai',
      status: 'active',
      subscription_type: 'standard',
      rate_multiplier: 1
    }
    const inactiveGroup = { ...activeGroup, id: 2, name: 'Paused group', status: 'inactive' }
    account.group_ids = [1, 2]
    account.groups = [
      { ...activeGroup, name: 'Outdated name' },
      inactiveGroup,
      inactiveGroup,
      { ...inactiveGroup, id: 3, name: 'Unassigned paused group' }
    ]
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account, true)
    await wrapper.setProps({ groups: [activeGroup] as any })
    const selector = wrapper.get('[data-tour="account-form-groups"]')
    expect(selector.findAll('input[type="checkbox"]').map(input => input.attributes('value')))
      .toEqual(['1', '2'])
    expect(selector.text()).toContain('Active group')
    expect(selector.text()).not.toContain('Outdated name')
    const pausedCheckbox = selector.get<HTMLInputElement>('input[value="2"]')
    expect(pausedCheckbox.element.checked).toBe(true)

    await pausedCheckbox.setValue(false)
    expect(selector.get<HTMLInputElement>('input[value="2"]').element.checked).toBe(false)
    await pausedCheckbox.setValue(true)
    expect(pausedCheckbox.element.checked).toBe(true)
    await pausedCheckbox.setValue(false)
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.group_ids).toEqual([1])
    expect(account.group_ids).toEqual([1, 2])
  })

  it('reopening the same account rehydrates the OpenAI whitelist from props', async () => {
    const account = buildAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    expect(wrapper.get('[data-testid="model-whitelist-value"]').text()).toBe('gpt-5.2')

    await wrapper.get('[data-testid="rewrite-to-snapshot"]').trigger('click')
    expect(wrapper.get('[data-testid="model-whitelist-value"]').text()).toBe('gpt-5.2-2025-12-11')

    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })

    expect(wrapper.get('[data-testid="model-whitelist-value"]').text()).toBe('gpt-5.2')

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toEqual({
      'gpt-5.2': 'gpt-5.2'
    })
  })

  it('preserves adaptive Kimi Responses endpoint on submit', async () => {
    const account = buildAccount()
    account.platform = 'kimi'
    account.credentials = {
      api_key: 'sk-kimi',
      account_mode: 'payg',
      api_protocol: 'adaptive',
      base_url: 'https://api.moonshot.cn/v1',
      api_base_urls: {
        chat_completions: 'https://api.moonshot.cn/v1',
        anthropic: 'https://api.moonshot.cn/anthropic',
        responses: 'https://api.moonshot.cn/v1'
      }
    }
    updateAccountMock.mockReset().mockResolvedValue(account)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(account)
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
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

  it('preserves adaptive GLM endpoints on submit', async () => {
    const account = buildAccount()
    account.platform = 'zhipu'
    account.credentials = {
      api_key: 'sk-glm',
      account_mode: 'coding',
      api_protocol: 'adaptive',
      base_url: 'https://open.bigmodel.cn/api/coding/paas/v4',
      api_base_urls: {
        chat_completions: 'https://open.bigmodel.cn/api/coding/paas/v4',
        anthropic: 'https://open.bigmodel.cn/api/anthropic'
      }
    }
    updateAccountMock.mockReset().mockResolvedValue(account)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(account)
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
      account_mode: 'coding',
      api_protocol: 'adaptive',
      base_url: 'https://open.bigmodel.cn/api/coding/paas/v4',
      api_base_urls: {
        chat_completions: 'https://open.bigmodel.cn/api/coding/paas/v4',
        anthropic: 'https://open.bigmodel.cn/api/anthropic'
      }
    })
  })

  it.each([
    ['explicit Chat Completions', 'chat_completions'],
    ['legacy missing protocol', undefined]
  ])('preserves a custom CN relay for %s accounts', async (_name, storedProtocol) => {
    const account = buildAccount()
    account.platform = 'zhipu'
    account.credentials = {
      api_key: 'sk-glm',
      account_mode: 'payg',
      base_url: 'https://relay.example.com/v1'
    }
    if (storedProtocol) {
      account.credentials.api_protocol = storedProtocol
    }
    updateAccountMock.mockReset().mockResolvedValue(account)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(account)
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    const submittedCredentials = updateAccountMock.mock.calls[0]?.[1]?.credentials
    expect(submittedCredentials).toMatchObject({
      account_mode: 'payg',
      api_protocol: 'chat_completions',
      base_url: 'https://relay.example.com/v1'
    })
    expect(submittedCredentials).not.toHaveProperty('api_base_urls')
  })

  it.each([
    { protocol: 'chat_completions', expectedBaseUrl: 'https://api.minimaxi.com/v1' },
    { protocol: 'responses', expectedBaseUrl: 'https://api.minimaxi.com/v1' },
    { protocol: 'anthropic', expectedBaseUrl: 'https://api.minimaxi.com/anthropic' }
  ])('uses the MiniMax $protocol default when a non-adaptive base URL is missing or cleared', async ({ protocol, expectedBaseUrl }) => {
    const account = buildAccount()
    account.platform = 'minimax'
    account.credentials = {
      api_key: 'sk-minimax',
      account_mode: 'payg',
      api_protocol: protocol,
      model_mapping: { 'MiniMax-M2.7': 'MiniMax-M2.7' }
    }
    updateAccountMock.mockReset().mockResolvedValue(account)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(account)
    await flushPromises()
    const baseUrlInput = wrapper.get<HTMLInputElement>('[data-testid="account-base-url"]')
    expect(baseUrlInput.element.value).toBe(expectedBaseUrl)
    await baseUrlInput.setValue('https://relay.example.com/v1')
    await baseUrlInput.setValue('')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    const credentials = updateAccountMock.mock.calls[0]?.[1]?.credentials
    expect(credentials).toMatchObject({
      account_mode: 'payg',
      api_protocol: protocol,
      base_url: expectedBaseUrl,
      model_mapping: { 'MiniMax-M2.7': 'MiniMax-M2.7' }
    })
    expect(credentials).not.toHaveProperty('api_base_urls')
    expect(account.credentials).not.toHaveProperty('base_url')
    wrapper.unmount()
  })

  it('uses the legacy base_url when adaptive endpoints are missing', async () => {
    const account = buildAccount()
    account.platform = 'zhipu'
    account.credentials = {
      api_key: 'sk-glm',
      account_mode: 'payg',
      api_protocol: 'adaptive',
      base_url: 'https://relay.example.com/v1',
      api_base_urls: {
        chat_completions: '   '
      }
    }
    updateAccountMock.mockReset().mockResolvedValue(account)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(account)
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
      api_protocol: 'adaptive',
      base_url: 'https://relay.example.com/v1',
      api_base_urls: {
        chat_completions: 'https://relay.example.com/v1',
        anthropic: 'https://open.bigmodel.cn/api/anthropic'
      }
    })
  })

  it('carries a fixed Chat relay into Adaptive when the user switches protocols', async () => {
    const account = buildAccount()
    account.platform = 'zhipu'
    account.credentials = {
      api_key: 'sk-glm',
      account_mode: 'payg',
      api_protocol: 'chat_completions',
      base_url: 'https://relay.example.com/v1'
    }
    updateAccountMock.mockReset().mockResolvedValue(account)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(account)
    const adaptiveButton = wrapper
      .findAll('button')
      .find(button => button.text().includes('admin.accounts.cnProviders.apiProtocol.adaptive'))
    expect(adaptiveButton).toBeDefined()
    await adaptiveButton!.trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
      api_protocol: 'adaptive',
      base_url: 'https://relay.example.com/v1',
      api_base_urls: {
        chat_completions: 'https://relay.example.com/v1'
      }
    })
  })

  it.each([
    {
      name: 'Anthropic',
      platform: 'zhipu',
      protocol: 'anthropic',
      baseUrl: 'https://relay.example.com/anthropic',
      expectedBaseUrl: 'https://open.bigmodel.cn/api/paas/v4',
      expectedProtocolUrls: {
        chat_completions: 'https://open.bigmodel.cn/api/paas/v4',
        anthropic: 'https://relay.example.com/anthropic'
      }
    },
    {
      name: 'Responses',
      platform: 'deepseek',
      protocol: 'responses',
      baseUrl: 'https://relay.example.com/responses',
      expectedBaseUrl: 'https://api.deepseek.com',
      expectedProtocolUrls: {
        chat_completions: 'https://api.deepseek.com',
        anthropic: 'https://api.deepseek.com/anthropic',
        responses: 'https://relay.example.com/responses'
      }
    }
  ])('keeps a fixed $name relay in its protocol slot when switching to Adaptive', async (testCase) => {
    const account = buildAccount()
    account.platform = testCase.platform
    account.credentials = {
      api_key: 'sk-cn',
      account_mode: 'payg',
      api_protocol: testCase.protocol,
      base_url: testCase.baseUrl
    }
    updateAccountMock.mockReset().mockResolvedValue(account)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(account)
    const adaptiveButton = wrapper
      .findAll('button')
      .find(button => button.text().includes('admin.accounts.cnProviders.apiProtocol.adaptive'))
    expect(adaptiveButton).toBeDefined()
    await adaptiveButton!.trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
      api_protocol: 'adaptive',
      base_url: testCase.expectedBaseUrl,
      api_base_urls: testCase.expectedProtocolUrls
    })
  })

  it('preserves model mappings when editing the whitelist', async () => {
    const account = buildAccount()
    account.credentials.model_mapping = {
      'gpt-5.2': 'gpt-5.2',
      'gpt-latest': 'gpt-5.2'
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    expect(wrapper.get('[data-testid="model-whitelist-value"]').text()).toBe('gpt-5.2')

    await wrapper.get('[data-testid="rewrite-to-snapshot"]').trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toEqual({
      'gpt-5.2-2025-12-11': 'gpt-5.2-2025-12-11',
      'gpt-latest': 'gpt-5.2'
    })
  })

  it('submits OpenAI compact mode and compact-only model mapping', async () => {
    const account = buildAccount()
    account.extra = {
      openai_compact_mode: 'force_on'
    }
    account.credentials = {
      ...account.credentials,
      compact_model_mapping: {
        'gpt-5.4': 'gpt-5.4-openai-compact'
      }
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_compact_mode).toBe('force_on')
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.compact_model_mapping).toEqual({
      'gpt-5.4': 'gpt-5.4-openai-compact'
    })
  })

  it('loads and submits the per-account OpenAI long-context billing toggle', async () => {
    const account = buildAccount()
    account.extra = {
      openai_long_context_billing_enabled: true
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const toggle = wrapper.get('[data-testid="openai-long-context-billing-toggle"]')
    expect(toggle.attributes('aria-checked')).toBe('true')

    await toggle.trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_long_context_billing_enabled).toBe(false)
  })

  it('loads and clears the OAuth-only Codex namespace flatten toggle', async () => {
    const account = buildAccount()
    account.type = 'oauth'
    account.extra = {
      openai_responses_flatten_namespaces: true
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const toggle = wrapper.get('[data-testid="edit-openai-flatten-namespaces-toggle"]')

    // 关闭后应从 extra 中删除该键，而不是写入 false
    await toggle.trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty(
      'openai_responses_flatten_namespaces'
    )
  })

  it('submits the Codex namespace flatten toggle when switched on', async () => {
    const account = buildAccount()
    account.type = 'oauth'
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    await wrapper.get('[data-testid="edit-openai-flatten-namespaces-toggle"]').trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_responses_flatten_namespaces).toBe(
      true
    )
  })

  it('writes the upstream request id header into extra only when it changes', async () => {
    const account = buildAccount()
    account.extra = { openai_compact_mode: 'force_on' }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const untouched = mountModal(account)
    await untouched.get('form#edit-account-form').trigger('submit.prevent')
    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.upstream_request_id_header).toBeUndefined()

    updateAccountMock.mockClear()
    const wrapper = mountModal(account)
    await wrapper.get('[data-testid="upstream-request-id-header"]').setValue(' X-Oneapi-Request-Id ')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).toMatchObject({
      openai_compact_mode: 'force_on',
      upstream_request_id_header: 'X-Oneapi-Request-Id'
    })
  })

  it('removes the upstream request id header from extra when cleared', async () => {
    const account = buildAccount()
    account.extra = { upstream_request_id_header: 'X-Request-ID' }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    expect((wrapper.get('[data-testid="upstream-request-id-header"]').element as HTMLInputElement).value).toBe('X-Request-ID')
    await wrapper.get('[data-testid="upstream-request-id-header"]').setValue('')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).toBeDefined()
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('upstream_request_id_header')
  })

  it('writes images_url_to_b64_json into extra when toggled on', async () => {
    const account = buildAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const toggle = wrapper.get('[data-testid="openai-images-url-to-b64-json-toggle"]')
    expect(toggle.attributes('aria-checked')).toBe('false')
    await toggle.trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.images_url_to_b64_json).toBe(true)
  })

  it('removes images_url_to_b64_json from extra when toggled off', async () => {
    const account = buildAccount()
    account.extra = { images_url_to_b64_json: true }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const toggle = wrapper.get('[data-testid="openai-images-url-to-b64-json-toggle"]')
    expect(toggle.attributes('aria-checked')).toBe('true')
    await toggle.trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).toBeDefined()
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('images_url_to_b64_json')
  })

  it('hides the Codex namespace flatten toggle for non-OAuth OpenAI accounts', async () => {
    const account = buildAccount()
    const wrapper = mountModal(account)

    expect(wrapper.find('[data-testid="edit-openai-flatten-namespaces-toggle"]').exists()).toBe(
      false
    )
  })

  it('defaults legacy OpenAI accounts to long-context billing disabled', async () => {
    const account = buildAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const toggle = wrapper.get('[data-testid="openai-long-context-billing-toggle"]')
    expect(toggle.attributes('aria-checked')).toBe('false')

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_long_context_billing_enabled).toBe(false)
  })

  it('does not render or submit the long-context billing toggle for Spark shadow accounts', async () => {
    const account = buildOpenAISparkShadowAccount()
    account.extra = {
      openai_long_context_billing_enabled: false
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)
    const wrapper = mountModal(account)

    expect(wrapper.find('[data-testid="openai-long-context-billing-toggle"]').exists()).toBe(false)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty(
      'openai_long_context_billing_enabled'
    )
  })

  it('preserves an explicit OpenAI long-context billing opt-out', async () => {
    const account = buildAccount()
    account.extra = {
      openai_long_context_billing_enabled: false
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const toggle = wrapper.get('[data-testid="openai-long-context-billing-toggle"]')
    expect(toggle.attributes('aria-checked')).toBe('false')

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_long_context_billing_enabled).toBe(false)
  })

  it('fails closed for malformed OpenAI long-context billing values', async () => {
    const account = buildAccount()
    account.extra = {
      openai_long_context_billing_enabled: 'false'
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    expect(wrapper.get('[data-testid="openai-long-context-billing-toggle"]').attributes('aria-checked')).toBe('false')

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_long_context_billing_enabled).toBe(false)
  })

  it('loads and submits Grok OAuth model mapping edits', async () => {
    const account = buildGrokOAuthAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    expect(wrapper.text()).toContain('Imagine Image')
    expect(wrapper.text()).toContain('Imagine Video')

    const inputWithValue = (value: string) => {
      const input = wrapper
        .findAll('input')
        .find((input) => (input.element as HTMLInputElement).value === value)
      expect(input).toBeTruthy()
      return input!
    }

    await inputWithValue('grok-latest').setValue('grok')
    await inputWithValue('grok-4.3').setValue('grok-build-0.1')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toEqual({
      grok: 'grok-build-0.1'
    })
  })

  it('uses the official xAI base URL when a Grok API-key account omits base_url', async () => {
    const account = buildGrokAPIKeyAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    expect((wrapper.get('input[placeholder="https://api.x.ai/v1"]').element as HTMLInputElement).value)
      .toBe('https://api.x.ai/v1')

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.base_url).toBe('https://api.x.ai/v1')
  })

  it('only submits model mapping credentials when saving an OpenAI spark shadow account', async () => {
    authIsSimpleMode.value = false
    const account = buildOpenAISparkShadowAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('[data-testid="set-shadow-group"]').trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    const payload = updateAccountMock.mock.calls[0]?.[1]
    expect(payload?.group_ids).toEqual([7])
    expect(payload?.credentials).toEqual({
      model_mapping: {
        'gpt-5.3-codex-spark': 'gpt-5.3-codex-spark'
      },
      compact_model_mapping: {
        'gpt-5.3-codex-spark': 'gpt-5.3-codex-spark-compact'
      }
    })
  })

  it('submits OpenAI APIKey Responses support override mode', async () => {
    const account = buildAccount()
    account.extra = {
      openai_responses_mode: 'force_chat_completions',
      openai_responses_supported: false
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('[data-testid="openai-responses-mode-select"]').setValue('force_responses')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_responses_mode).toBe('force_responses')
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_responses_supported).toBe(false)
  })

  it('submits the account upstream billing auto-probe setting', async () => {
    const account = buildAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const toggle = wrapper.get('[data-testid="upstream-billing-auto-probe"]')
    expect(toggle.attributes('aria-checked')).toBe('false')

    await toggle.trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.upstream_billing_probe_enabled).toBe(true)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty(
      'upstream_billing_probe_enabled'
    )
  })

  it('exposes the upstream billing auto-probe toggle for non-OpenAI API-key accounts', async () => {
    // 探测已放宽到全部 API-key 平台：grok 账号同样能开启并保存。
    const account = buildAccount()
    account.platform = 'grok'
    account.name = 'grok-relay'
    account.credentials = { api_key: 'sk-grok', base_url: 'https://relay.example/v1' }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const toggle = wrapper.get('[data-testid="upstream-billing-auto-probe"]')
    expect(toggle.attributes('aria-checked')).toBe('false')

    await toggle.trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.upstream_billing_probe_enabled).toBe(true)
  })

  it('enabling rate sync also enables probing and stops submitting a manual rate', async () => {
    const account = buildAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const syncToggle = wrapper.get('[data-testid="upstream-billing-rate-sync"]')
    const probeToggle = wrapper.get('[data-testid="upstream-billing-auto-probe"]')
    const rateInput = wrapper.get<HTMLInputElement>('[data-testid="account-rate-multiplier"]')
    expect(syncToggle.attributes('aria-checked')).toBe('false')
    expect(probeToggle.attributes('aria-checked')).toBe('false')
    expect(rateInput.element.disabled).toBe(false)
    expect(wrapper.text()).toContain('admin.accounts.billingRateMultiplierHint')
    expect(wrapper.text()).not.toContain('admin.accounts.upstreamBilling.syncRateManagedHint')

    await syncToggle.trigger('click')
    expect(syncToggle.attributes('aria-checked')).toBe('true')
    expect(probeToggle.attributes('aria-checked')).toBe('true')
    expect(rateInput.element.disabled).toBe(true)
    expect(wrapper.text()).toContain('admin.accounts.upstreamBilling.syncRateManagedHint')
    expect(wrapper.text()).not.toContain('admin.accounts.billingRateMultiplierHint')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    const payload = updateAccountMock.mock.calls[0]?.[1]
    expect(payload?.upstream_billing_probe_enabled).toBe(true)
    expect(payload?.upstream_billing_rate_sync_enabled).toBe(true)
    expect(payload).not.toHaveProperty('rate_multiplier')
  })

  it('disabling probing also disables rate sync and restores manual rate editing', async () => {
    const account = buildAccount()
    account.extra = {
      upstream_billing_probe_enabled: true,
      upstream_billing_rate_sync_enabled: true
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const syncToggle = wrapper.get('[data-testid="upstream-billing-rate-sync"]')
    const probeToggle = wrapper.get('[data-testid="upstream-billing-auto-probe"]')
    const rateInput = wrapper.get<HTMLInputElement>('[data-testid="account-rate-multiplier"]')
    expect(syncToggle.attributes('aria-checked')).toBe('true')
    expect(rateInput.element.disabled).toBe(true)

    await probeToggle.trigger('click')
    expect(probeToggle.attributes('aria-checked')).toBe('false')
    expect(syncToggle.attributes('aria-checked')).toBe('false')
    expect(rateInput.element.disabled).toBe(false)
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    const payload = updateAccountMock.mock.calls[0]?.[1]
    expect(payload?.upstream_billing_probe_enabled).toBe(false)
    expect(payload?.upstream_billing_rate_sync_enabled).toBe(false)
    expect(payload?.rate_multiplier).toBe(1)
  })

  it('disabling only rate sync keeps automatic probing enabled', async () => {
    const account = buildAccount()
    account.extra = {
      upstream_billing_probe_enabled: true,
      upstream_billing_rate_sync_enabled: true
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    await wrapper.get('[data-testid="upstream-billing-rate-sync"]').trigger('click')
    expect(wrapper.get('[data-testid="upstream-billing-auto-probe"]').attributes('aria-checked')).toBe(
      'true'
    )
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    const payload = updateAccountMock.mock.calls[0]?.[1]
    expect(payload?.upstream_billing_probe_enabled).toBe(true)
    expect(payload?.upstream_billing_rate_sync_enabled).toBe(false)
    expect(payload?.rate_multiplier).toBe(1)
  })

  it('clears OpenAI APIKey Responses override when set back to auto', async () => {
    const account = buildAccount()
    account.extra = {
      openai_responses_mode: 'force_chat_completions',
      openai_responses_supported: true
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('[data-testid="openai-responses-mode-select"]').setValue('auto')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('openai_responses_mode')
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_responses_supported).toBe(true)
  })

  it('submits OpenAI APIKey endpoint capabilities from credentials', async () => {
    const account = buildAccount()
    account.credentials.openai_capabilities = ['chat_completions']
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    expect(wrapper.findAll('input[type="checkbox"]').some((input) => (input.element as HTMLInputElement).checked)).toBe(true)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.openai_capabilities).toEqual([
      'chat_completions'
    ])
  })

	it('submits OpenAI quota auto-pause thresholds in extra', async () => {
	  const account = buildAccount()
	  account.extra = {
		auto_pause_5h_threshold: 0.9,
		auto_pause_7d_threshold: 0.8
	  }
	  updateAccountMock.mockReset()
	  checkMixedChannelRiskMock.mockReset()
	  checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
	  updateAccountMock.mockResolvedValue(account)

	  const wrapper = mountModal(account)

	  await wrapper.get('[data-testid="auto-pause-5h-threshold"]').setValue('95')
	  await wrapper.get('[data-testid="auto-pause-7d-threshold"]').setValue('96')
	  await wrapper.get('form#edit-account-form').trigger('submit.prevent')

	  expect(updateAccountMock).toHaveBeenCalledTimes(1)
	  expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.auto_pause_5h_threshold).toBe(0.95)
	  expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.auto_pause_7d_threshold).toBe(0.96)
	})

	it('submits OpenAI quota auto-pause disable flag in extra', async () => {
	  // Toggling the per-account disable flag must persist as auto_pause_5h_disabled
	  // so an admin can exempt one account from auto-pause even when a global default
	  // threshold is configured (otherwise leaving the threshold blank would silently
	  // fall back to the global default).
	  const account = buildAccount()
	  updateAccountMock.mockReset()
	  checkMixedChannelRiskMock.mockReset()
	  checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
	  updateAccountMock.mockResolvedValue(account)

	  const wrapper = mountModal(account)

	  await wrapper.get('[data-testid="auto-pause-5h-disabled"]').trigger('click')
	  await wrapper.get('form#edit-account-form').trigger('submit.prevent')

	  expect(updateAccountMock).toHaveBeenCalledTimes(1)
	  expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.auto_pause_5h_disabled).toBe(true)
	  expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.auto_pause_7d_disabled).toBeUndefined()
	})

  it('keeps at least one OpenAI APIKey endpoint capability selected', async () => {
    const account = buildAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    const chatCheckbox = wrapper.get<HTMLInputElement>(
      '[data-testid="openai-endpoint-capability-chat_completions"]'
    )
    const embeddingsCheckbox = wrapper.get<HTMLInputElement>(
      '[data-testid="openai-endpoint-capability-embeddings"]'
    )

    expect(chatCheckbox.element.checked).toBe(true)
    expect(embeddingsCheckbox.element.checked).toBe(true)

    await embeddingsCheckbox.setValue(false)

    expect(chatCheckbox.element.checked).toBe(true)
    expect(embeddingsCheckbox.element.checked).toBe(false)

    await chatCheckbox.setValue(false)

    expect(chatCheckbox.element.checked).toBe(true)
    expect(embeddingsCheckbox.element.checked).toBe(false)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.openai_capabilities).toEqual([
      'chat_completions'
    ])
  })

  it('disables text generation protocol when only embeddings requests are accepted', async () => {
    const account = buildAccount()
    account.credentials.openai_capabilities = ['embeddings']
    account.extra = {
      openai_responses_mode: 'force_responses',
      openai_responses_supported: true
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    const responsesModeSelect = wrapper.get<HTMLSelectElement>(
      '[data-testid="openai-responses-mode-select"]'
    )

    expect(responsesModeSelect.element.disabled).toBe(true)
    expect(wrapper.find('[data-testid="openai-responses-mode-not-applicable"]').exists()).toBe(true)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.openai_capabilities).toEqual([
      'embeddings'
    ])
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('openai_responses_mode')
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_responses_supported).toBe(true)
  })

  it('submits Codex image tool force-inject mode as bridge override', async () => {
    const account = buildAccount()
    account.extra = {
      codex_image_generation_bridge: false,
      codex_image_generation_bridge_enabled: true
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    expect(wrapper.text()).toContain('admin.accounts.openai.codexImageTool')
    expect(wrapper.text()).toContain('admin.accounts.openai.codexImageToolDesc')
    expect(wrapper.text()).toContain('admin.accounts.openai.codexImageToolEnabledDesc')

    await wrapper.get('button[data-testid="codex-image-tool-enabled"]').trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.codex_image_generation_bridge).toBe(true)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('codex_image_generation_bridge_enabled')
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('codex_image_generation_explicit_tool_policy')
  })

  it('submits Codex image tool no-injection mode without strip policy', async () => {
    const account = buildAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('button[data-testid="codex-image-tool-disabled"]').trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.codex_image_generation_bridge).toBe(false)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('codex_image_generation_explicit_tool_policy')
  })

  it('submits Codex image tool block mode as strip policy and clears bridge override', async () => {
    const account = buildAccount()
    account.extra = {
      codex_image_generation_bridge: true
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    expect(wrapper.text()).toContain('admin.accounts.openai.codexImageToolBlock')
    expect(wrapper.text()).toContain('admin.accounts.openai.codexImageToolBlockDesc')

    await wrapper.get('button[data-testid="codex-image-tool-block"]').trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.codex_image_generation_explicit_tool_policy).toBe('strip')
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('codex_image_generation_bridge')
  })

  it('loads strip policy as block mode and clears both keys when reset to inherit', async () => {
    const account = buildAccount()
    account.extra = {
      codex_image_generation_explicit_tool_policy: 'strip'
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('button[data-testid="codex-image-tool-inherit"]').trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('codex_image_generation_explicit_tool_policy')
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('codex_image_generation_bridge')
  })

  it('setup-token account can select and submit OAuth WS mode', async () => {
    const account = buildOpenAISetupTokenAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('[data-testid="edit-openai-ws-mode-select"]').setValue('http_bridge')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_oauth_responses_websockets_v2_mode).toBe('http_bridge')
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_oauth_responses_websockets_v2_enabled).toBe(true)
  })

  it('allows saving apikey account when backend redacted api_key but credentials_status reports it exists', async () => {
    // 新前端 + 新后端：响应已脱敏，credentials 里没有 api_key，credentials_status.has_api_key=true
    const account = buildAccount()
    account.credentials = {
      base_url: 'https://api.openai.com',
      model_mapping: { 'gpt-5.2': 'gpt-5.2' }
    }
    account.credentials_status = { has_api_key: true }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    // 用户未输入新 key 时，payload 不应带 api_key，由后端合并保留旧值
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials).not.toHaveProperty('api_key')
  })

  it('allows saving apikey account against legacy backend without credentials_status', async () => {
    // 新前端 + 旧后端：credentials_status 缺失，但 credentials.api_key 仍是明文，应允许保存
    const account = buildAccount()
    // 显式确保没有 credentials_status
    expect(account.credentials_status).toBeUndefined()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    // 旧后端响应未脱敏，原 api_key 会随 currentCredentials 一起传回去（旧行为，等价于无操作）
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.api_key).toBe('sk-test')
  })

  it('blocks apikey save when neither credentials_status nor legacy api_key indicates existence', async () => {
    const account = buildAccount()
    account.credentials = {
      base_url: 'https://api.openai.com'
    }
    // 既没有 credentials_status 也没有旧的 api_key
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(account)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).not.toHaveBeenCalled()
  })

  it('allows saving Vertex SA account when backend redacted service_account_json but credentials_status reports it exists', async () => {
    // 新前端 + 新后端：响应已脱敏，credentials 里没有 service_account_json，credentials_status.has_service_account_json=true
    const account = buildVertexAccount()
    account.credentials = {
      project_id: 'demo-project',
      client_email: 'sa@example.iam.gserviceaccount.com',
      location: 'us-central1',
      tier_id: 'vertex'
    }
    account.credentials_status = { has_service_account_json: true }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.project_id).toBe('demo-project')
  })

  it('allows saving Vertex SA account against legacy backend without credentials_status', async () => {
    // 新前端 + 旧后端：credentials_status 缺失，但 credentials.service_account_json 仍是明文，应允许保存
    const account = buildVertexAccount()
    expect(account.credentials_status).toBeUndefined()
    expect(account.credentials.service_account_json).toBeTruthy()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
  })

  it('blocks Vertex SA save when neither credentials_status nor legacy json indicates existence', async () => {
    const account = buildVertexAccount()
    account.credentials = {
      project_id: 'demo-project',
      client_email: 'sa@example.iam.gserviceaccount.com',
      location: 'us-central1',
      tier_id: 'vertex'
    }
    // 既没有 credentials_status 也没有旧的 service_account_json
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(account)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).not.toHaveBeenCalled()
  })

  it('loads and submits Antigravity configured project fallback', async () => {
    const account = buildAntigravityAccount('configured-project')
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const input = wrapper.get<HTMLInputElement>('[data-testid="antigravity-project-id-input"]')
    expect(input.element.value).toBe('configured-project')

    await input.setValue('  updated-project  ')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.antigravity_project_id).toBe(
      'updated-project'
    )
  })

  it('clears Antigravity configured project fallback when input is empty', async () => {
    const account = buildAntigravityAccount('configured-project')
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const input = wrapper.get<HTMLInputElement>('[data-testid="antigravity-project-id-input"]')

    await input.setValue('')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials).not.toHaveProperty(
      'antigravity_project_id'
    )
  })
})

describe('EditAccountModal OpenAI 自动使用重置卡', () => {
  beforeEach(() => {
    authIsSimpleMode.value = true
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })
  })

  it('仅对 OpenAI OAuth 母账号显示，默认关闭且阈值为 100/100', () => {
    const parent = mountModal(buildOpenAIOAuthParentAccount())
    expect(parent.find('[data-testid="auto-reset-credit-settings"]').exists()).toBe(true)
    expect((parent.get('[data-testid="auto-reset-credit-5h-threshold"]').element as HTMLInputElement).value).toBe('100')
    expect((parent.get('[data-testid="auto-reset-credit-7d-threshold"]').element as HTMLInputElement).value).toBe('100')
    expect(parent.get('[data-testid="auto-reset-credit-5h-threshold"]').attributes('disabled')).toBeDefined()
    parent.unmount()

    for (const account of [buildAccount(), buildOpenAISetupTokenAccount(), buildOpenAISparkShadowAccount()]) {
      const wrapper = mountModal(account)
      expect(wrapper.find('[data-testid="auto-reset-credit-settings"]').exists()).toBe(false)
      wrapper.unmount()
    }
  })

  it('独立保存两个阈值，并禁止把运行态回写到管理请求', async () => {
    const account = buildOpenAIOAuthParentAccount()
    account.extra = {
      codex_auto_reset_credit_state: {
        status: 'success',
        trigger_window: '5h',
        available_count: 1
      }
    }
    updateAccountMock.mockResolvedValue(account)
    const wrapper = mountModal(account)

    await wrapper.get('[data-testid="auto-reset-credit-enabled"]').trigger('click')
    await wrapper.get('[data-testid="auto-reset-credit-5h-threshold"]').setValue('75.5')
    await wrapper.get('[data-testid="auto-reset-credit-7d-threshold"]').setValue('92')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    const extra = updateAccountMock.mock.calls[0]?.[1]?.extra
    expect(extra).toMatchObject({
      auto_reset_credit_enabled: true,
      auto_reset_credit_5h_threshold: 0.755,
      auto_reset_credit_7d_threshold: 0.92
    })
    expect(extra).not.toHaveProperty('codex_auto_reset_credit_state')
    wrapper.unmount()
  })

  it('开启后拒绝超出 0.1–100 范围的任一阈值', async () => {
    const wrapper = mountModal(buildOpenAIOAuthParentAccount())
    await wrapper.get('[data-testid="auto-reset-credit-enabled"]').trigger('click')
    await wrapper.get('[data-testid="auto-reset-credit-5h-threshold"]').setValue('0')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    expect(updateAccountMock).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
