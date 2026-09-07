import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import type { ModelContextCapacityRow, SyncUpstreamModelsResult } from '@/api/admin/accounts'
import { getModelsByPlatform } from '@/composables/useModelWhitelist'

const {
  copyToClipboard,
  showError,
  showSuccess,
  showInfo,
  showWarning,
  syncUpstreamModels,
  syncUpstreamModelsPreview
} = vi.hoisted(() => ({
  copyToClipboard: vi.fn().mockResolvedValue(true),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  showInfo: vi.fn(),
  showWarning: vi.fn(),
  syncUpstreamModels: vi.fn(),
  syncUpstreamModelsPreview: vi.fn()
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => {
        if (key === 'common.copy') return '复制'
        return params?.count ? `${key} ${params.count}` : key
      }
    })
  }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess,
    showInfo,
    showWarning
  })
}))

vi.mock('@/api/admin/accounts', () => ({
  accountsAPI: {
    syncUpstreamModels,
    syncUpstreamModelsPreview
  }
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard
  })
}))

import ModelWhitelistSelector from '../ModelWhitelistSelector.vue'
import ModelContextCapacityField from '../ModelContextCapacityField.vue'

function capacityRow(overrides: Partial<ModelContextCapacityRow> = {}): ModelContextCapacityRow {
  return {
    upstream_model_id: 'gpt-5.6-sol',
    aliases: ['gpt-5.6-sol'],
    editable: true,
    automatic_context_window: 258_000,
    automatic_source: 'default',
    effective_context_window: 258_000,
    effective_source: 'default',
    capacity_basis: 'context_window',
    ...overrides
  }
}

function mountSelector(props: Record<string, unknown> = {}) {
  return mount(ModelWhitelistSelector, {
    props: {
      modelValue: [],
      platform: 'openai',
      ...props,
    },
    global: {
      stubs: {
        ModelIcon: true
      }
    }
  })
}

function findModelRow(wrapper: ReturnType<typeof mountSelector>, modelId: string) {
  const row = wrapper
    .findAll('[data-testid="model-option"]')
    .find(candidate => candidate.attributes('data-model-id') === modelId)

  if (!row) {
    throw new Error(`Model row not found: ${modelId}`)
  }

  return row
}

describe('ModelWhitelistSelector', () => {
  beforeEach(() => {
    copyToClipboard.mockClear()
    showError.mockReset()
    showSuccess.mockReset()
    showInfo.mockReset()
    showWarning.mockReset()
    syncUpstreamModels.mockReset()
    syncUpstreamModelsPreview.mockReset()
  })

  it('copies a model ID without selecting the model', async () => {
    const wrapper = mountSelector()
    await wrapper.get('div.cursor-pointer').trigger('click')

    const row = findModelRow(wrapper, 'gpt-5.6-sol')

    const copyButton = row.get('[data-testid="copy-model-id"]')
    expect(copyButton.attributes('aria-label')).toBe('复制 gpt-5.6-sol')

    await copyButton.trigger('click')
    await flushPromises()

    expect(copyToClipboard).toHaveBeenCalledWith('gpt-5.6-sol')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

  it('keeps the existing model selection behavior', async () => {
    const wrapper = mountSelector()
    await wrapper.get('div.cursor-pointer').trigger('click')

    const row = findModelRow(wrapper, 'gpt-5.6-sol')
    await row.get('[data-testid="select-model"]').trigger('click')

    expect(wrapper.emitted('update:modelValue')).toEqual([[['gpt-5.6-sol']]])
    expect(copyToClipboard).not.toHaveBeenCalled()
  })

  it('keeps the original action order and fills platform-related models without a network request', async () => {
    const manual = 'Manual.Exact-ID'
    const wrapper = mountSelector({ modelValue: [manual, 'gpt-5.6-sol'], platforms: ['openai', 'anthropic'], accountId: 46 })
    const actions = wrapper.findAll('button').filter(button => [
      'admin.accounts.fillRelatedModels',
      'admin.accounts.syncUpstreamModels',
      'admin.accounts.clearAllModels'
    ].includes(button.text()))
    expect(actions.map(button => button.text())).toEqual([
      'admin.accounts.fillRelatedModels',
      'admin.accounts.syncUpstreamModels',
      'admin.accounts.clearAllModels'
    ])

    await wrapper.get('[data-testid="fill-related-models"]').trigger('click')

    expect(wrapper.emitted('update:modelValue')).toEqual([[[
      ...new Set([manual, 'gpt-5.6-sol', ...getModelsByPlatform('openai'), ...getModelsByPlatform('anthropic')])
    ]]])
    expect(syncUpstreamModels).not.toHaveBeenCalled()
    expect(syncUpstreamModelsPreview).not.toHaveBeenCalled()
    expect(wrapper.emitted('update:capacityDrafts')).toBeUndefined()
    wrapper.unmount()
  })

  it('calls the saved-account sync once and adds each new exact ID while preserving manual IDs', async () => {
    let resolve!: (result: SyncUpstreamModelsResult) => void
    syncUpstreamModels.mockReturnValue(new Promise(result => { resolve = result }))
    const result: SyncUpstreamModelsResult = {
      models: ['New.Exact-ID', 'New.Exact-ID', 'manual-model'],
      metadata: { 'New.Exact-ID': { id: 'New.Exact-ID', context_window: 1_050_000 } },
      capacity_rows: [capacityRow({ upstream_model_id: 'New.Exact-ID', aliases: ['New.Exact-ID'] })]
    }
    const wrapper = mountSelector({ modelValue: ['manual-model', 'user-only-model'], accountId: 46 })
    const button = wrapper.get('[data-testid="sync-upstream-models"]')
    await button.trigger('click')
    await button.trigger('click')
    expect(syncUpstreamModels).toHaveBeenCalledOnce()
    expect(syncUpstreamModels).toHaveBeenCalledWith(46)
    expect(syncUpstreamModelsPreview).not.toHaveBeenCalled()

    resolve(result)
    await flushPromises()

    expect(wrapper.emitted('upstream-synced')).toEqual([[result]])
    expect(wrapper.emitted('update:modelValue')).toEqual([[['manual-model', 'user-only-model', 'New.Exact-ID']]])
    expect(wrapper.emitted('update:capacityDrafts')).toBeUndefined()
    wrapper.unmount()
  })

  it('retains the saved OAuth account sync entry even when capacity editing is protected', async () => {
    syncUpstreamModels.mockResolvedValue({ models: ['oauth-model'] })
    const wrapper = mountSelector({
      modelValue: ['gpt-5.6-sol'],
      accountId: 91,
      capacityRows: [capacityRow({ editable: false, effective_source: 'protected' })]
    })

    expect(wrapper.find('[data-testid="context-capacity-input"]').exists()).toBe(false)
    await wrapper.get('[data-testid="sync-upstream-models"]').trigger('click')
    await flushPromises()

    expect(syncUpstreamModels).toHaveBeenCalledOnce()
    expect(syncUpstreamModels).toHaveBeenCalledWith(91)
    expect(wrapper.emitted('update:modelValue')).toEqual([[['gpt-5.6-sol', 'oauth-model']]])
    wrapper.unmount()
  })

  it('shows separate always-visible capacity and source beside unchanged selected and candidate names', async () => {
    const wrapper = mountSelector({
      modelValue: ['gpt-5.6-sol', 'Unknown.Exact-ID'],
      capacityRows: [capacityRow({ effective_context_window: 1_050_000, effective_source: 'official' })]
    })
    const chips = wrapper.findAll('[data-testid="selected-model"]')
    expect(chips[0].element.parentElement?.classList.contains('grid-cols-2')).toBe(true)
    expect(chips.map(chip => chip.get('[data-testid="selected-model-name"]').text())).toEqual(['gpt-5.6-sol', 'Unknown.Exact-ID'])
    expect(chips[0].get('[data-testid="context-capacity-value"]').text()).toContain('1.05M')
    expect(chips[0].get('[data-testid="context-capacity-source"]').text()).toContain('official')
    expect(chips[1].get('[data-testid="context-capacity-source"]').text()).not.toBe('')
    expect(chips[1].find('[data-testid="context-capacity-edit"]').exists()).toBe(false)

    await wrapper.get('[data-testid="model-selector-toggle"]').trigger('click')
    const candidate = findModelRow(wrapper, 'gpt-5.6-sol')
    expect(candidate.get('[data-testid="model-option-name"]').text()).toBe('gpt-5.6-sol')
    expect(candidate.get('[data-testid="context-capacity-value"]').text()).toContain('1.05M')
    expect(candidate.get('[data-testid="context-capacity-source"]').text()).toContain('official')
    expect(candidate.get('[data-testid="copy-model-id"]').attributes('aria-label')).toBe('复制 gpt-5.6-sol')
    await candidate.get('[data-testid="copy-model-id"]').trigger('click')
    expect(copyToClipboard).toHaveBeenCalledOnce()
    expect(copyToClipboard).toHaveBeenCalledWith('gpt-5.6-sol')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(wrapper.emitted('update:capacityDrafts')).toBeUndefined()
    wrapper.unmount()
  })

  it('commits only the real alias target capacity without toggling the dropdown or changing the whitelist', async () => {
    const wrapper = mountSelector({
      modelValue: ['Public.Alias'],
      capacityRows: [
        capacityRow({ upstream_model_id: 'Public.Alias', aliases: ['other-alias'], effective_context_window: 999_000 }),
        capacityRow({ upstream_model_id: 'Real.Upstream-ID', aliases: ['Public.Alias'] })
      ],
      capacityDrafts: { 'untouched-model': '300K' }
    })
    const chip = wrapper.get('[data-testid="selected-model"]')
    expect(chip.get('[data-testid="context-capacity-value"]').text()).toContain('258K')
    await chip.get('[data-testid="context-capacity-edit"]').trigger('click')
    expect(wrapper.find('[data-testid="model-option"]').exists()).toBe(false)
    expect(wrapper.emitted('capacity-validity')?.at(-1)).toEqual([false])
    const input = chip.get('[data-testid="context-capacity-input"]')
    await input.setValue('1.05M')
    expect(wrapper.emitted('update:capacityDrafts')).toBeUndefined()
    const keydown = vi.fn()
    wrapper.element.addEventListener('keydown', keydown)
    const enter = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })
    input.element.dispatchEvent(enter)
    await flushPromises()

    expect(enter.defaultPrevented).toBe(true)
    expect(keydown).not.toHaveBeenCalled()
    expect(wrapper.emitted('update:capacityDrafts')).toEqual([[{
      'untouched-model': '300K', 'Real.Upstream-ID': '1.05M'
    }]])
    expect(wrapper.emitted('capacity-validity')?.at(-1)).toEqual([true])
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(chip.get('[data-testid="selected-model-name"]').text()).toBe('Public.Alias')
    expect(wrapper.find('[data-testid="model-option"]').exists()).toBe(false)
    expect(syncUpstreamModels).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('edits a candidate capacity independently of selection and cancels without a patch', async () => {
    const wrapper = mountSelector({ modelValue: ['manual-model'], capacityRows: [capacityRow()] })
    await wrapper.get('[data-testid="model-selector-toggle"]').trigger('click')
    const candidate = findModelRow(wrapper, 'gpt-5.6-sol')
    await candidate.get('[data-testid="context-capacity-edit"]').trigger('click')
    await candidate.get('[data-testid="context-capacity-input"]').setValue('2M')
    await candidate.get('[data-testid="context-capacity-input"]').trigger('keydown', { key: 'Escape' })

    expect(candidate.find('[data-testid="context-capacity-input"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="model-option"]').exists()).toBe(true)
    expect(wrapper.emitted('update:capacityDrafts')).toBeUndefined()
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(wrapper.emitted('capacity-validity')?.at(-1)).toEqual([true])
    expect(copyToClipboard).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('keeps Save blocked for either duplicate model field until every pending edit ends', async () => {
    const wrapper = mountSelector({ modelValue: ['gpt-5.6-sol'], capacityRows: [capacityRow()] })
    await wrapper.get('[data-testid="model-selector-toggle"]').trigger('click')
    const selected = wrapper.get('[data-testid="selected-model"]').getComponent(ModelContextCapacityField)
    const candidate = findModelRow(wrapper, 'gpt-5.6-sol').getComponent(ModelContextCapacityField)
    selected.vm.$emit('editing', true)
    selected.vm.$emit('validity', false)
    candidate.vm.$emit('validity', true)
    await flushPromises()
    expect(wrapper.emitted('capacity-validity')?.at(-1)).toEqual([false])

    candidate.vm.$emit('editing', true)
    selected.vm.$emit('editing', false)
    selected.vm.$emit('validity', true)
    await flushPromises()
    expect(wrapper.emitted('capacity-validity')?.at(-1)).toEqual([false])

    candidate.vm.$emit('editing', false)
    await flushPromises()
    expect(wrapper.emitted('capacity-validity')?.at(-1)).toEqual([true])
    wrapper.unmount()
    expect(wrapper.emitted('capacity-validity')?.at(-1)).toEqual([true])
  })

  it('renders dynamic managed models with their real context without changing the whitelist', async () => {
    const wrapper = mount(ModelWhitelistSelector, {
      props: {
        modelValue: [],
        platform: 'openai',
        readonly: true,
        models: [
          {
            id: 'gpt-5.6-sol',
            type: 'model',
            display_name: 'GPT-5.6 Sol',
            created_at: '',
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
            id: 'candidate-unverified',
            type: 'model',
            display_name: 'Candidate Unverified',
            created_at: '',
            endpoints: [],
            source_revision: 'cindy-v0.1.52',
            managed: true,
            verified: false,
            public_model: false
          }
        ]
      },
      global: {
        stubs: {
          ModelIcon: true
        }
      }
    })

    const rows = wrapper.findAll('[data-testid="managed-model-option"]')
    const row = rows[0]
    expect(row.text()).toContain('gpt-5.6-sol')
    expect(row.text()).toContain('1,050,000')
    expect(row.text()).toContain('372,000')
    expect(row.text()).toContain('128,000')
    expect(row.get('[data-testid="model-verification-status"]').text()).toContain(
      'admin.accounts.cindyModelVerified'
    )
    expect(row.text()).toContain('responses')
    expect(rows[1].get('[data-testid="model-verification-status"]').text()).toContain(
      'admin.accounts.cindyModelPendingVerification'
    )
    expect(wrapper.find('[data-testid="select-model"]').exists()).toBe(false)
    expect(wrapper.find('input[placeholder="admin.accounts.enterCustomModelName"]').exists()).toBe(false)

    await row.trigger('click')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

  it('renders an explicit empty state for an empty managed catalog', () => {
    const wrapper = mount(ModelWhitelistSelector, {
      props: {
        modelValue: [],
        readonly: true,
        models: []
      },
      global: { stubs: { ModelIcon: true } }
    })

    expect(wrapper.get('[data-testid="managed-model-catalog"]').text()).toContain(
      'admin.accounts.noMatchingModels'
    )
  })

  it('warns when model IDs sync but capability metadata is incomplete', async () => {
    syncUpstreamModels.mockResolvedValue({
      models: ['x-preview-f-free'],
      warnings: [{
        code: 'upstream_model_metadata_incomplete',
        message: 'Model IDs were synced, but capability metadata could not be updated.'
      }]
    })
    const wrapper = mountSelector({ accountId: 46 })
    const syncButton = wrapper.findAll('button')
      .find(button => button.text() === 'admin.accounts.syncUpstreamModels')

    expect(syncButton).toBeDefined()
    await syncButton!.trigger('click')
    await flushPromises()

    expect(wrapper.emitted('update:modelValue')).toEqual([[['x-preview-f-free']]])
    expect(showWarning).toHaveBeenCalledWith('admin.accounts.syncUpstreamModelsMetadataIncomplete')
    expect(showSuccess).not.toHaveBeenCalled()
  })

  it('shows success and a partial warning when some capabilities were saved', async () => {
    syncUpstreamModels.mockResolvedValue({
      models: ['gpt-6-astra', 'gpt-image-2'],
      warnings: [
        {
          code: 'upstream_model_metadata_partial',
          message: 'Some model capabilities were saved; remaining models are still incomplete.'
        }
      ]
    })
    const wrapper = mount(ModelWhitelistSelector, {
      props: {
        modelValue: [],
        platform: 'openai',
        accountId: 46
      },
      global: {
        stubs: {
          ModelIcon: true
        }
      }
    })

    const syncButton = wrapper
      .findAll('button')
      .find(button => button.text() === 'admin.accounts.syncUpstreamModels')
    expect(syncButton).toBeDefined()
    await syncButton!.trigger('click')
    await flushPromises()

    expect(wrapper.emitted('update:modelValue')).toEqual([[['gpt-6-astra', 'gpt-image-2']]])
    expect(showSuccess).toHaveBeenCalledWith('admin.accounts.syncUpstreamModelsSuccess 2')
    expect(showWarning).toHaveBeenCalledWith('admin.accounts.syncUpstreamModelsMetadataPartial')
  })

  it('reports a successful preview so account creation can persist metadata', async () => {
    syncUpstreamModelsPreview.mockResolvedValue({
      models: ['x-preview-f-free'],
      metadata: {
        'x-preview-f-free': {
          id: 'x-preview-f-free',
          reasoning: true,
          supported_reasoning_levels: ['low', 'high', 'max'],
        },
      },
    })
    const wrapper = mountSelector({
      syncCredentials: {
        platform: 'openai',
        type: 'apikey',
        base_url: 'https://opencode.ai/zen/v1',
        api_key: 'test-key',
      },
    })
    const syncButton = wrapper.findAll('button')
      .find(button => button.text() === 'admin.accounts.syncUpstreamModels')

    expect(syncButton).toBeDefined()
    await syncButton?.trigger('click')
    await flushPromises()

    expect(syncUpstreamModelsPreview).toHaveBeenCalledOnce()
    expect(wrapper.emitted('upstream-synced')).toEqual([[{
      models: ['x-preview-f-free'],
      metadata: { 'x-preview-f-free': { id: 'x-preview-f-free', reasoning: true, supported_reasoning_levels: ['low', 'high', 'max'] } }
    }]])
    expect(wrapper.emitted('update:modelValue')).toEqual([[['x-preview-f-free']]])
    await wrapper.get('div.cursor-pointer').trigger('click')
    expect(findModelRow(wrapper, 'x-preview-f-free')).toBeDefined()
  })

  it('forwards capacity rows for saved accounts and retains dynamic models after remount', async () => {
    const result = {
      models: ['dynamic-only'],
      metadata: { 'dynamic-only': { id: 'dynamic-only', context_window: 1_050_000 } },
      capacity_rows: [capacityRow({ upstream_model_id: 'dynamic-only', aliases: ['dynamic-only'], effective_context_window: 1_050_000 })]
    }
    syncUpstreamModels.mockResolvedValue(result)
    const wrapper = mountSelector({ accountId: 46 })
    await wrapper.findAll('button').find(button => button.text() === 'admin.accounts.syncUpstreamModels')!.trigger('click')
    await flushPromises()
    expect(wrapper.emitted('upstream-synced')).toEqual([[result]])
    wrapper.unmount()

    const remounted = mountSelector({ syncedModels: result, capacityRows: result.capacity_rows })
    await remounted.get('div.cursor-pointer').trigger('click')
    expect(findModelRow(remounted, 'dynamic-only').get('[data-testid="context-capacity-value"]').text()).toContain('1.05M')
    expect(remounted.emitted('update:modelValue')).toBeUndefined()
    remounted.unmount()
  })

  it('clears a locally cached dynamic model when sync source changes', async () => {
    syncUpstreamModelsPreview.mockResolvedValue({ models: ['provider-a-only'] })
    const wrapper = mountSelector({ syncCredentials: { platform: 'openai', type: 'apikey', base_url: 'https://a.example/v1', api_key: 'key-a' } })
    await wrapper.findAll('button').find(button => button.text() === 'admin.accounts.syncUpstreamModels')!.trigger('click')
    await flushPromises()
    await wrapper.get('div.cursor-pointer').trigger('click')
    expect(wrapper.text()).toContain('provider-a-only')
    await wrapper.setProps({ syncedModels: undefined, syncCredentials: { platform: 'openai', type: 'apikey', base_url: 'https://b.example/v1', api_key: 'key-b' } })
    expect(wrapper.text()).not.toContain('provider-a-only')
    wrapper.unmount()
  })

  it('ignores an in-flight old-source response instead of forwarding stale rows or changing selection', async () => {
    let resolve!: (result: { models: string[] }) => void
    syncUpstreamModelsPreview.mockReturnValue(new Promise(result => { resolve = result }))
    const wrapper = mountSelector({ syncCredentials: { platform: 'openai', type: 'apikey', base_url: 'https://a.example/v1', api_key: 'key-a' } })
    await wrapper.findAll('button').find(button => button.text() === 'admin.accounts.syncUpstreamModels')!.trigger('click')
    await wrapper.setProps({ syncCredentials: { platform: 'openai', type: 'apikey', base_url: 'https://b.example/v1', api_key: 'key-b' } })
    resolve({ models: ['stale-provider-a-model'] })
    await flushPromises()
    expect(wrapper.emitted('upstream-synced')).toBeUndefined()
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(showSuccess).not.toHaveBeenCalled()
    expect(showError).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('does not synchronize when a parent marks saved connection changes as unsaved', async () => {
    const wrapper = mountSelector({ accountId: 42, syncDisabled: true, syncDisabledReason: 'Save first' })
    const button = wrapper.findAll('button').find(button => button.text() === 'admin.accounts.syncUpstreamModels')!
    expect(button.attributes('disabled')).toBeDefined()
    expect(button.attributes('title')).toBe('Save first')
    await button.trigger('click')
    expect(syncUpstreamModels).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('invalidates an in-flight saved-account sync when the parent source generation changes', async () => {
    let resolve!: (result: SyncUpstreamModelsResult) => void
    syncUpstreamModels.mockReturnValue(new Promise(result => { resolve = result }))
    const wrapper = mountSelector({ modelValue: ['manual-model'], accountId: 42, syncSourceKey: 'source-a' })
    await wrapper.get('[data-testid="sync-upstream-models"]').trigger('click')
    await wrapper.setProps({ syncSourceKey: 'source-b' })
    resolve({ models: ['stale-model'], capacity_rows: [capacityRow()] })
    await flushPromises()
    expect(syncUpstreamModels).toHaveBeenCalledOnce()
    expect(syncUpstreamModels).toHaveBeenCalledWith(42)
    expect(wrapper.emitted('upstream-synced')).toBeUndefined()
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(showSuccess).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('keeps selectors without capacity data unchanged', async () => {
    const wrapper = mountSelector({ modelValue: ['gpt-5.6-sol'] })
    expect(wrapper.findComponent(ModelContextCapacityField).exists()).toBe(false)
    expect(wrapper.get('[data-testid="selected-model-name"]').text()).toBe('gpt-5.6-sol')
    await wrapper.get('[data-testid="model-selector-toggle"]').trigger('click')
    expect(wrapper.findAll('[data-testid="model-option"]').length).toBeGreaterThan(0)
    expect(wrapper.findComponent(ModelContextCapacityField).exists()).toBe(false)
    expect(wrapper.find('[data-testid="fill-related-models"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('retains inline placeholders for an explicitly connected loading catalog', async () => {
    const wrapper = mountSelector({ modelValue: ['gpt-5.6-sol'], capacityRows: [] })
    expect(wrapper.findComponent(ModelContextCapacityField).exists()).toBe(true)
    await wrapper.setProps({ capacityRows: [capacityRow({ effective_source: 'official', effective_context_window: 1_050_000 })] })
    expect(wrapper.get('[data-testid="context-capacity-value"]').text()).toBe('[1.05M]')
    expect(wrapper.get('[data-testid="context-capacity-source"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('does not invalidate a sync when an independent capacity draft changes', async () => {
    let resolve!: (result: SyncUpstreamModelsResult) => void
    syncUpstreamModels.mockReturnValue(new Promise(result => { resolve = result }))
    const wrapper = mountSelector({ accountId: 42, syncSourceKey: 'same-source' })
    await wrapper.get('[data-testid="sync-upstream-models"]').trigger('click')
    await wrapper.setProps({ capacityDrafts: { 'gpt-5.6-sol': '1M' } })
    const result = { models: ['new-model'], capacity_rows: [capacityRow()] }
    resolve(result)
    await flushPromises()
    expect(wrapper.emitted('upstream-synced')).toEqual([[result]])
    expect(wrapper.emitted('update:modelValue')).toEqual([[['new-model']]])
    expect(wrapper.props('capacityDrafts')).toEqual({ 'gpt-5.6-sol': '1M' })
    wrapper.unmount()
  })

})
