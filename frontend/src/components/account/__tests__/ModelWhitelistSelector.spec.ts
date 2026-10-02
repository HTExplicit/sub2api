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
        if (key === 'admin.accounts.modelMappingConflict') {
          return `Model mapping conflict: ${params?.from} → ${params?.to}`
        }
        if (params && key.startsWith('admin.accounts.syncUpstreamPicker.')) {
          return `${key} ${JSON.stringify(params)}`
        }
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
        ModelIcon: true,
        // The upstream picker is a BaseDialog: render it in place and without a transition so it can be queried.
        Teleport: true,
        Transition: true
      }
    }
  })
}

type SelectorWrapper = ReturnType<typeof mountSelector>

function findModelRow(wrapper: SelectorWrapper, modelId: string) {
  const row = wrapper
    .findAll('[data-testid="model-option"]')
    .find(candidate => candidate.attributes('data-model-id') === modelId)

  if (!row) {
    throw new Error(`Model row not found: ${modelId}`)
  }

  return row
}

function findPicker(wrapper: SelectorWrapper) {
  return wrapper.find('[data-testid="upstream-model-picker"]')
}

function pickerRows(wrapper: SelectorWrapper) {
  return wrapper.findAll('[data-testid="upstream-picker-row"]')
}

function pickerRowIds(wrapper: SelectorWrapper) {
  return pickerRows(wrapper).map(row => row.attributes('data-model-id'))
}

// A row's state is its checkbox's checked property; the row's data-checked has to agree with it.
function isPickerRowChecked(row: ReturnType<typeof pickerRows>[number]) {
  const checked = (row.get('[data-testid="upstream-picker-checkbox"]').element as HTMLInputElement).checked
  expect(row.attributes('data-checked')).toBe(String(checked))
  return checked
}

function checkedPickerRowIds(wrapper: SelectorWrapper) {
  return pickerRows(wrapper)
    .filter(isPickerRowChecked)
    .map(row => row.attributes('data-model-id'))
}

function findPickerRow(wrapper: SelectorWrapper, modelId: string) {
  const row = pickerRows(wrapper).find(candidate => candidate.attributes('data-model-id') === modelId)
  if (!row) {
    throw new Error(`Picker row not found: ${modelId}`)
  }
  return row
}

function pickerText(wrapper: SelectorWrapper, testId: string) {
  return wrapper.get(`[data-testid="${testId}"]`).text()
}

async function openPicker(wrapper: SelectorWrapper) {
  await wrapper.get('[data-testid="sync-upstream-models"]').trigger('click')
  await flushPromises()
  expect(findPicker(wrapper).exists()).toBe(true)
}

async function confirmPicker(wrapper: SelectorWrapper) {
  await wrapper.get('[data-testid="upstream-picker-confirm"]').trigger('click')
}

async function setPickerFilter(wrapper: SelectorWrapper, filter: string) {
  const option = wrapper
    .findAll('[data-testid="upstream-picker-filter-option"]')
    .find(candidate => candidate.attributes('data-filter') === filter)
  if (!option) {
    throw new Error(`Picker filter not found: ${filter}`)
  }
  await option.trigger('click')
}

// Upstream returns one model twice, one padded and a blank; the whitelist holds one of them and one it lacks.
const pickerWhitelist = ['zz-legacy', 'kept-model']
function pickerFixture(): SyncUpstreamModelsResult {
  return {
    models: [' model-10 ', 'model-2', 'kept-model', '', 'model-2'],
    metadata: {
      'model-2': { id: 'model-2', display_name: 'Model Two', context_window: 200_000, max_output_tokens: 64_000 },
      'kept-model': { id: 'kept-model', display_name: 'kept-model', context_window: 1_050_000 }
    }
  }
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

  it('rejects a custom whitelist model that is already mapped to a different target', async () => {
    const wrapper = mountSelector({ modelMappings: [{ from: 'gpt-latest', to: 'deepseek-chat' }] })
    await wrapper.get('input[placeholder="admin.accounts.enterCustomModelName"]').setValue(' gpt-latest ')
    await wrapper.findAll('button').find(button => button.text() === 'admin.accounts.addModel')!.trigger('click')

    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(showInfo).toHaveBeenCalledWith(expect.stringContaining('gpt-latest → deepseek-chat'))
  })

  it('keeps the existing duplicate identity warning before checking mappings', async () => {
    const wrapper = mountSelector({ modelValue: ['gpt-latest'], modelMappings: [{ from: 'gpt-latest', to: 'deepseek-chat' }] })
    await wrapper.get('input[placeholder="admin.accounts.enterCustomModelName"]').setValue('gpt-latest')
    await wrapper.findAll('button').find(button => button.text() === 'admin.accounts.addModel')!.trigger('click')
    expect(showInfo).toHaveBeenCalledWith('admin.accounts.modelExists')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

  it('allows matching identity mapping as a whitelist model', async () => {
    const wrapper = mountSelector({ modelMappings: [{ from: 'gpt-latest', to: 'gpt-latest' }] })
    await wrapper.get('input[placeholder="admin.accounts.enterCustomModelName"]').setValue('gpt-latest')
    await wrapper.findAll('button').find(button => button.text() === 'admin.accounts.addModel')!.trigger('click')
    expect(wrapper.emitted('update:modelValue')).toEqual([[['gpt-latest']]])
  })

  it('still allows custom models without a mapping prop', async () => {
    const wrapper = mountSelector()
    await wrapper.get('input[placeholder="admin.accounts.enterCustomModelName"]').setValue('custom-model')
    await wrapper.findAll('button').find(button => button.text() === 'admin.accounts.addModel')!.trigger('click')
    expect(wrapper.emitted('update:modelValue')).toEqual([[['custom-model']]])
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
    const selected = wrapper.emitted('update:modelValue')![0][0] as string[]
    expect(selected).toContain('gpt-6-sol')
    expect(selected).toContain('gpt-6-luna')
    await wrapper.setProps({ modelValue: selected })
    await wrapper.get('[data-testid="fill-related-models"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')![1][0]).toEqual(selected)
    wrapper.unmount()
  })

  it('calls the saved-account sync once and applies the picked exact IDs, manual ones kept when checked, on confirm', async () => {
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
    expect(findPicker(wrapper).exists()).toBe(false)

    resolve(result)
    await flushPromises()

    // The sync opens the picker; neither the whitelist nor the synced result reaches the form before confirm.
    expect(pickerRowIds(wrapper)).toEqual(['manual-model', 'New.Exact-ID', 'user-only-model'])
    expect(wrapper.emitted('upstream-synced')).toBeUndefined()
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    // The search ignores case on the ID itself: this mixed-case ID has no display name.
    await wrapper.get('[data-testid="upstream-picker-search"]').setValue('new.exact-id')
    expect(pickerRowIds(wrapper)).toEqual(['New.Exact-ID'])
    await wrapper.get('[data-testid="upstream-picker-search"]').setValue('')

    await findPickerRow(wrapper, 'user-only-model').trigger('click')
    await confirmPicker(wrapper)

    expect(wrapper.emitted('upstream-synced')).toEqual([[result]])
    expect(wrapper.emitted('update:modelValue')).toEqual([[['manual-model', 'user-only-model', 'New.Exact-ID']]])
    expect(wrapper.emitted('update:capacityDrafts')).toBeUndefined()
    expect(findPicker(wrapper).exists()).toBe(false)
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
    await openPicker(wrapper)

    expect(syncUpstreamModels).toHaveBeenCalledOnce()
    expect(syncUpstreamModels).toHaveBeenCalledWith(91)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    // The whitelist entry the upstream did not return starts unchecked, so confirming as offered drops it.
    await confirmPicker(wrapper)
    expect(wrapper.emitted('update:modelValue')).toEqual([[['oauth-model']]])
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

  it('shows incomplete capability metadata inside the picker instead of a toast', async () => {
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

    expect(pickerText(wrapper, 'upstream-picker-metadata-notice')).toBe('admin.accounts.syncUpstreamModelsMetadataIncomplete')
    expect(showWarning).not.toHaveBeenCalled()
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()

    await confirmPicker(wrapper)
    expect(wrapper.emitted('update:modelValue')).toEqual([[['x-preview-f-free']]])
    expect(showSuccess).toHaveBeenCalledWith('admin.accounts.syncUpstreamPicker.updated {"added":1,"removed":0}')
    expect(showWarning).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('shows partial capability metadata inside the picker and the whitelist change on confirm', async () => {
    syncUpstreamModels.mockResolvedValue({
      models: ['gpt-6-astra', 'gpt-image-2'],
      warnings: [
        {
          code: 'upstream_model_metadata_partial',
          message: 'Some model capabilities were saved; remaining models are still incomplete.'
        }
      ]
    })
    const wrapper = mountSelector({ accountId: 46 })

    const syncButton = wrapper
      .findAll('button')
      .find(button => button.text() === 'admin.accounts.syncUpstreamModels')
    expect(syncButton).toBeDefined()
    await syncButton!.trigger('click')
    await flushPromises()

    expect(pickerText(wrapper, 'upstream-picker-metadata-notice')).toBe('admin.accounts.syncUpstreamModelsMetadataPartial')
    await confirmPicker(wrapper)
    expect(wrapper.emitted('update:modelValue')).toEqual([[['gpt-6-astra', 'gpt-image-2']]])
    expect(showSuccess).toHaveBeenCalledWith('admin.accounts.syncUpstreamPicker.updated {"added":2,"removed":0}')
    expect(showWarning).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('reports an upstream list of blank IDs without opening the picker or touching the whitelist', async () => {
    const result: SyncUpstreamModelsResult = { models: ['', '  '] }
    syncUpstreamModels.mockResolvedValue(result)
    const wrapper = mountSelector({ modelValue: ['kept-model'], accountId: 7 })
    await wrapper.get('[data-testid="sync-upstream-models"]').trigger('click')
    await flushPromises()

    expect(findPicker(wrapper).exists()).toBe(false)
    expect(showInfo).toHaveBeenCalledWith('admin.accounts.syncUpstreamModelsEmpty')
    expect(wrapper.emitted('upstream-synced')).toEqual([[result]])
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    wrapper.unmount()
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
    expect(wrapper.emitted('upstream-synced')).toBeUndefined()
    await confirmPicker(wrapper)
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
    await confirmPicker(wrapper)
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
    await confirmPicker(wrapper)
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
    expect(findPicker(wrapper).exists()).toBe(false)
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
    expect(findPicker(wrapper).exists()).toBe(false)
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
    expect(pickerRowIds(wrapper)).toEqual(['new-model'])
    await confirmPicker(wrapper)
    expect(wrapper.emitted('upstream-synced')).toEqual([[result]])
    expect(wrapper.emitted('update:modelValue')).toEqual([[['new-model']]])
    expect(wrapper.props('capacityDrafts')).toEqual({ 'gpt-5.6-sol': '1M' })
    wrapper.unmount()
  })

  it('opens the picker sorted by ID with upstream models checked and whitelist-only entries unchecked', async () => {
    syncUpstreamModels.mockResolvedValue(pickerFixture())
    const wrapper = mountSelector({ modelValue: pickerWhitelist, accountId: 7 })
    await openPicker(wrapper)

    expect(pickerRowIds(wrapper)).toEqual(['kept-model', 'model-2', 'model-10', 'zz-legacy'])
    expect(checkedPickerRowIds(wrapper)).toEqual(['kept-model', 'model-2', 'model-10'])
    const tags = (modelId: string) => findPickerRow(wrapper, modelId).findAll('.badge').map(tag => tag.attributes('data-testid'))
    expect(tags('kept-model')).toEqual([])
    expect(tags('model-2')).toEqual(['upstream-picker-tag-new'])
    expect(tags('model-10')).toEqual(['upstream-picker-tag-new'])
    expect(tags('zz-legacy')).toEqual(['upstream-picker-tag-missing'])
    expect(findPickerRow(wrapper, 'model-2').get('[data-testid="upstream-picker-details"]').text()).toBe(
      'Model Two · admin.accounts.syncUpstreamPicker.contextWindow {"value":"200K"} · admin.accounts.syncUpstreamPicker.maxOutput {"value":"64K"}'
    )
    // A display name equal to the ID is not repeated.
    expect(findPickerRow(wrapper, 'kept-model').get('[data-testid="upstream-picker-details"]').text()).toBe(
      'admin.accounts.syncUpstreamPicker.contextWindow {"value":"1.05M"}'
    )
    expect(findPickerRow(wrapper, 'zz-legacy').find('[data-testid="upstream-picker-details"]').exists()).toBe(false)
    // Each checkbox is named by the ID and described by its row's tag and capability line.
    const description = (modelId: string) => (findPickerRow(wrapper, modelId)
      .get('[data-testid="upstream-picker-checkbox"]').attributes('aria-describedby') ?? '')
      .split(' ').filter(Boolean).map(id => wrapper.get(`[id="${id}"]`).text())
    expect(description('model-2')).toEqual([
      'admin.accounts.syncUpstreamPicker.tags.new',
      'Model Two · admin.accounts.syncUpstreamPicker.contextWindow {"value":"200K"} · admin.accounts.syncUpstreamPicker.maxOutput {"value":"64K"}'
    ])
    expect(description('kept-model')).toEqual(['admin.accounts.syncUpstreamPicker.contextWindow {"value":"1.05M"}'])
    expect(description('zz-legacy')).toEqual(['admin.accounts.syncUpstreamPicker.tags.missing'])
    expect(pickerText(wrapper, 'upstream-picker-counter')).toBe('admin.accounts.syncUpstreamPicker.selectedCount {"selected":3,"total":4}')
    expect(pickerText(wrapper, 'upstream-picker-summary')).toBe('admin.accounts.syncUpstreamPicker.summary {"added":2,"removed":1}')
    expect(wrapper.find('[data-testid="upstream-picker-configured-notice"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="upstream-picker-metadata-notice"]').exists()).toBe(false)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(wrapper.emitted('upstream-synced')).toBeUndefined()
    expect(showSuccess).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('checks every row with 全选 whatever the search and the status filter show', async () => {
    syncUpstreamModels.mockResolvedValue(pickerFixture())
    const wrapper = mountSelector({ modelValue: pickerWhitelist, accountId: 7 })
    await openPicker(wrapper)
    await setPickerFilter(wrapper, 'new')
    await wrapper.get('[data-testid="upstream-picker-search"]').setValue('10')
    expect(pickerRowIds(wrapper)).toEqual(['model-10'])

    await wrapper.get('[data-testid="upstream-picker-select-all"]').trigger('click')

    expect(pickerText(wrapper, 'upstream-picker-counter')).toBe('admin.accounts.syncUpstreamPicker.selectedCount {"selected":4,"total":4}')
    await wrapper.get('[data-testid="upstream-picker-search"]').setValue('')
    await setPickerFilter(wrapper, 'all')
    expect(checkedPickerRowIds(wrapper)).toEqual(['kept-model', 'model-2', 'model-10', 'zz-legacy'])
    wrapper.unmount()
  })

  it('inverts every row with 反选, including the rows the search hides', async () => {
    syncUpstreamModels.mockResolvedValue(pickerFixture())
    const wrapper = mountSelector({ modelValue: pickerWhitelist, accountId: 7 })
    await openPicker(wrapper)
    await wrapper.get('[data-testid="upstream-picker-search"]').setValue('model-2')
    expect(pickerRowIds(wrapper)).toEqual(['model-2'])

    await wrapper.get('[data-testid="upstream-picker-invert"]').trigger('click')

    expect(pickerText(wrapper, 'upstream-picker-counter')).toBe('admin.accounts.syncUpstreamPicker.selectedCount {"selected":1,"total":4}')
    await wrapper.get('[data-testid="upstream-picker-search"]').setValue('')
    expect(checkedPickerRowIds(wrapper)).toEqual(['zz-legacy'])
    await wrapper.get('[data-testid="upstream-picker-invert"]').trigger('click')
    expect(checkedPickerRowIds(wrapper)).toEqual(['kept-model', 'model-2', 'model-10'])
    wrapper.unmount()
  })

  it('renders a long list in a window while the counter, 全选, 反选 and confirm cover every row', async () => {
    const models = Array.from({ length: 300 }, (_, index) => `bulk-model-${String(index).padStart(3, '0')}`)
    syncUpstreamModels.mockResolvedValue({ models })
    const wrapper = mountSelector({ accountId: 7 })
    await openPicker(wrapper)

    // Only the first rows of the sorted list are in the DOM.
    const rendered = pickerRowIds(wrapper)
    expect(rendered.length).toBeGreaterThan(0)
    expect(rendered.length).toBeLessThan(models.length)
    expect(rendered).toEqual(models.slice(0, rendered.length))
    expect(pickerText(wrapper, 'upstream-picker-counter')).toBe('admin.accounts.syncUpstreamPicker.selectedCount {"selected":300,"total":300}')

    await wrapper.get('[data-testid="upstream-picker-invert"]').trigger('click')
    expect(pickerText(wrapper, 'upstream-picker-counter')).toBe('admin.accounts.syncUpstreamPicker.selectedCount {"selected":0,"total":300}')
    expect(checkedPickerRowIds(wrapper)).toEqual([])
    expect(wrapper.get('[data-testid="upstream-picker-confirm"]').attributes('disabled')).toBeDefined()

    await wrapper.get('[data-testid="upstream-picker-select-all"]').trigger('click')
    expect(pickerText(wrapper, 'upstream-picker-counter')).toBe('admin.accounts.syncUpstreamPicker.selectedCount {"selected":300,"total":300}')
    await confirmPicker(wrapper)
    expect(wrapper.emitted('update:modelValue')).toEqual([[models]])
    wrapper.unmount()
  })

  it('combines the status filter with a case-insensitive search on the ID and the display name', async () => {
    syncUpstreamModels.mockResolvedValue(pickerFixture())
    const wrapper = mountSelector({ modelValue: pickerWhitelist, accountId: 7 })
    await openPicker(wrapper)
    expect(wrapper.findAll('[data-testid="upstream-picker-filter-option"]').map(option => option.text())).toEqual([
      'admin.accounts.syncUpstreamPicker.filters.all 4',
      'admin.accounts.syncUpstreamPicker.filters.checked 3',
      'admin.accounts.syncUpstreamPicker.filters.unchecked 1',
      'admin.accounts.syncUpstreamPicker.filters.new 2',
      'admin.accounts.syncUpstreamPicker.filters.missing 1'
    ])

    await setPickerFilter(wrapper, 'new')
    expect(pickerRowIds(wrapper)).toEqual(['model-2', 'model-10'])
    await wrapper.get('[data-testid="upstream-picker-search"]').setValue('TWO')
    expect(pickerRowIds(wrapper)).toEqual(['model-2'])
    await setPickerFilter(wrapper, 'missing')
    expect(pickerRowIds(wrapper)).toEqual([])
    expect(pickerText(wrapper, 'upstream-picker-empty')).toBe('admin.accounts.noMatchingModels')
    await wrapper.get('[data-testid="upstream-picker-search"]').setValue('')
    expect(pickerRowIds(wrapper)).toEqual(['zz-legacy'])

    await setPickerFilter(wrapper, 'checked')
    expect(pickerRowIds(wrapper)).toEqual(['kept-model', 'model-2', 'model-10'])
    await findPickerRow(wrapper, 'model-2').trigger('click')
    expect(pickerRowIds(wrapper)).toEqual(['kept-model', 'model-10'])
    await setPickerFilter(wrapper, 'unchecked')
    expect(pickerRowIds(wrapper)).toEqual(['model-2', 'zz-legacy'])
    expect(wrapper.findAll('[data-testid="upstream-picker-filter-option"]').map(option => option.attributes('aria-checked')))
      .toEqual(['false', 'false', 'true', 'false', 'false'])
    await wrapper.get('[data-testid="upstream-picker-filter"]').trigger('keydown', { key: 'ArrowLeft' })
    expect(pickerRowIds(wrapper)).toEqual(['kept-model', 'model-10'])

    // Copying an ID does not toggle its row.
    await findPickerRow(wrapper, 'kept-model').get('[data-testid="upstream-picker-copy"]').trigger('click')
    expect(copyToClipboard).toHaveBeenCalledWith('kept-model')
    expect(isPickerRowChecked(findPickerRow(wrapper, 'kept-model'))).toBe(true)
    wrapper.unmount()
  })

  it('moves the focus to the row taking the place of a toggled row that leaves the filtered list', async () => {
    syncUpstreamModels.mockResolvedValue(pickerFixture())
    const wrapper = mount(ModelWhitelistSelector, {
      props: { modelValue: pickerWhitelist, platform: 'openai', accountId: 7 },
      attachTo: document.body,
      global: { stubs: { ModelIcon: true, Teleport: true, Transition: true } }
    })
    await openPicker(wrapper)
    await setPickerFilter(wrapper, 'checked')
    const checkbox = (modelId: string) => findPickerRow(wrapper, modelId).get('[data-testid="upstream-picker-checkbox"]')
    const toggleFocused = async (modelId: string) => {
      const box = checkbox(modelId)
      ;(box.element as HTMLInputElement).focus()
      await box.trigger('click')
      await flushPromises()
    }

    await toggleFocused('model-2')
    expect(pickerRowIds(wrapper)).toEqual(['kept-model', 'model-10'])
    expect(document.activeElement).toBe(checkbox('model-10').element)
    // The last row hands the focus back to the row before it.
    await toggleFocused('model-10')
    expect(document.activeElement).toBe(checkbox('kept-model').element)
    // With no row left, the search takes it.
    await toggleFocused('kept-model')
    expect(pickerRowIds(wrapper)).toEqual([])
    expect(document.activeElement).toBe(wrapper.get('[data-testid="upstream-picker-search"]').element)
    wrapper.unmount()
  })

  it('confirms whitelist entries in their own order, then newly checked models in list order', async () => {
    const result: SyncUpstreamModelsResult = { models: ['zeta-kept', 'beta-new', 'alpha-kept', 'alpha-new'] }
    syncUpstreamModels.mockResolvedValue(result)
    const wrapper = mountSelector({ modelValue: ['zeta-kept', 'alpha-kept', 'gone-model'], accountId: 7 })
    await openPicker(wrapper)
    expect(pickerRowIds(wrapper)).toEqual(['alpha-kept', 'alpha-new', 'beta-new', 'gone-model', 'zeta-kept'])

    await findPickerRow(wrapper, 'alpha-kept').trigger('click')
    const betaCheckbox = findPickerRow(wrapper, 'beta-new').get('[data-testid="upstream-picker-checkbox"]')
    await betaCheckbox.trigger('click')
    expect(isPickerRowChecked(findPickerRow(wrapper, 'beta-new'))).toBe(false)
    await betaCheckbox.trigger('click')
    expect(pickerText(wrapper, 'upstream-picker-summary')).toBe('admin.accounts.syncUpstreamPicker.summary {"added":2,"removed":2}')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()

    await confirmPicker(wrapper)

    expect(wrapper.emitted('update:modelValue')).toEqual([[['zeta-kept', 'alpha-new', 'beta-new']]])
    expect(showSuccess).toHaveBeenCalledWith('admin.accounts.syncUpstreamPicker.updated {"added":2,"removed":2}')
    expect(showInfo).not.toHaveBeenCalled()
    expect(wrapper.emitted('upstream-synced')).toEqual([[result]])
    expect(findPicker(wrapper).exists()).toBe(false)
    // A confirmed result keeps its models available as candidates.
    await wrapper.get('[data-testid="model-selector-toggle"]').trigger('click')
    expect(findModelRow(wrapper, 'beta-new').exists()).toBe(true)
    wrapper.unmount()
  })

  it('keeps Confirm disabled with a hint while no model is checked', async () => {
    syncUpstreamModels.mockResolvedValue(pickerFixture())
    const wrapper = mountSelector({ modelValue: pickerWhitelist, accountId: 7 })
    await openPicker(wrapper)
    const confirmButton = wrapper.get('[data-testid="upstream-picker-confirm"]')
    expect(confirmButton.attributes('disabled')).toBeUndefined()
    expect(wrapper.find('[data-testid="upstream-picker-empty-hint"]').exists()).toBe(false)

    await wrapper.get('[data-testid="upstream-picker-select-all"]').trigger('click')
    await wrapper.get('[data-testid="upstream-picker-invert"]').trigger('click')

    expect(checkedPickerRowIds(wrapper)).toEqual([])
    expect(confirmButton.attributes('disabled')).toBeDefined()
    expect(pickerText(wrapper, 'upstream-picker-empty-hint')).toBe('admin.accounts.syncUpstreamPicker.emptySelectionHint')
    await confirmButton.trigger('click')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(findPicker(wrapper).exists()).toBe(true)

    await findPickerRow(wrapper, 'zz-legacy').trigger('click')
    expect(confirmButton.attributes('disabled')).toBeUndefined()
    expect(wrapper.find('[data-testid="upstream-picker-empty-hint"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('cancels from the button, the header close or Escape without touching the whitelist or the candidates', async () => {
    syncUpstreamModels.mockImplementation(async () => ({ models: ['upstream-only-model', 'gpt-5.6-sol'] }))
    const wrapper = mountSelector({ modelValue: ['gpt-5.6-sol'], accountId: 7 })

    await openPicker(wrapper)
    await findPickerRow(wrapper, 'gpt-5.6-sol').trigger('click')
    await wrapper.get('[data-testid="upstream-picker-search"]').setValue('upstream')
    await wrapper.get('[data-testid="upstream-picker-cancel"]').trigger('click')
    expect(findPicker(wrapper).exists()).toBe(false)

    // Every open starts again from the result and the current whitelist.
    await openPicker(wrapper)
    expect((wrapper.get('[data-testid="upstream-picker-search"]').element as HTMLInputElement).value).toBe('')
    expect(checkedPickerRowIds(wrapper)).toEqual(['gpt-5.6-sol', 'upstream-only-model'])
    await wrapper.get('button[aria-label="Close modal"]').trigger('click')
    expect(findPicker(wrapper).exists()).toBe(false)

    await openPicker(wrapper)
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }))
    await flushPromises()
    expect(findPicker(wrapper).exists()).toBe(false)

    expect(syncUpstreamModels).toHaveBeenCalledTimes(3)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(wrapper.emitted('upstream-synced')).toBeUndefined()
    expect(showSuccess).not.toHaveBeenCalled()
    expect(showInfo).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="model-selector-toggle"]').trigger('click')
    expect(wrapper.find('[data-testid="model-option"][data-model-id="upstream-only-model"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('explains a configured model list and reports an unchanged whitelist on confirm', async () => {
    syncUpstreamModelsPreview.mockResolvedValue({ models: ['b-model', 'a-model'], model_list_source: 'configured' })
    const wrapper = mountSelector({
      modelValue: ['b-model', 'a-model'],
      syncCredentials: { platform: 'openai', type: 'apikey', base_url: 'https://a.example/v1', api_key: 'key-a' }
    })
    await openPicker(wrapper)

    expect(pickerText(wrapper, 'upstream-picker-configured-notice')).toBe('admin.accounts.syncUpstreamPicker.configuredSource')
    expect(wrapper.findAll('.badge')).toHaveLength(0)
    await confirmPicker(wrapper)
    expect(wrapper.emitted('update:modelValue')).toEqual([[['b-model', 'a-model']]])
    expect(showInfo).toHaveBeenCalledWith('admin.accounts.syncUpstreamPicker.unchanged')
    expect(showSuccess).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('closes an open picker unapplied when the sync source changes, and never submits a form', async () => {
    syncUpstreamModelsPreview.mockResolvedValue({ models: ['provider-a-only'] })
    const wrapper = mountSelector({ syncCredentials: { platform: 'openai', type: 'apikey', base_url: 'https://a.example/v1', api_key: 'key-a' } })
    await openPicker(wrapper)

    const enter = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })
    wrapper.get('[data-testid="upstream-picker-search"]').element.dispatchEvent(enter)
    await flushPromises()
    expect(enter.defaultPrevented).toBe(true)
    expect(findPicker(wrapper).exists()).toBe(true)
    const buttons = [
      ...findPicker(wrapper).findAll('button'),
      ...wrapper.get('[data-testid="upstream-picker-footer"]').findAll('button')
    ]
    expect(buttons.length).toBeGreaterThan(0)
    expect(buttons.filter(button => button.attributes('type') !== 'button')).toEqual([])

    await wrapper.setProps({ syncCredentials: { platform: 'openai', type: 'apikey', base_url: 'https://b.example/v1', api_key: 'key-b' } })
    expect(findPicker(wrapper).exists()).toBe(false)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(wrapper.emitted('upstream-synced')).toBeUndefined()
    wrapper.unmount()
  })

it('shows the upstream sync button for OpenCode Go create-account credentials', () => {
    const wrapper = mountSelector({
      platform: 'opencode_go',
      syncCredentials: {
        platform: 'opencode_go',
        type: 'apikey',
        base_url: 'https://opencode.ai/zen/go/v1',
        api_key: 'sk-test',
      },
    })
    const syncButton = wrapper
      .findAll('button')
      .find(button => button.text() === 'admin.accounts.syncUpstreamModels')

    expect(syncButton).toBeDefined()
    expect(syncButton?.exists()).toBe(true)
  })
})
