import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const copyToClipboard = vi.fn().mockResolvedValue(true)

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
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showInfo: vi.fn()
  })
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard
  })
}))

import ModelWhitelistSelector from '../ModelWhitelistSelector.vue'

function mountSelector() {
  return mount(ModelWhitelistSelector, {
    props: {
      modelValue: [],
      platform: 'openai'
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
    .find(candidate => candidate.text().includes(modelId))

  if (!row) {
    throw new Error(`Model row not found: ${modelId}`)
  }

  return row
}

describe('ModelWhitelistSelector', () => {
  beforeEach(() => {
    copyToClipboard.mockClear()
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
      global: {
        stubs: {
          ModelIcon: true
        }
      }
    })

    expect(wrapper.get('[data-testid="managed-model-catalog"]').text()).toContain(
      'admin.accounts.noMatchingModels'
    )
  })

})
