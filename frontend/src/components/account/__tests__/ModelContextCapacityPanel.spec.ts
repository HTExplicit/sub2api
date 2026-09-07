import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import type { ModelContextCapacityRow } from '@/api/admin/accounts'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) => params?.value ? `${key} ${params.value}` : key
  })
}))

import ModelContextCapacityPanel from '../ModelContextCapacityPanel.vue'

function capacityRow(overrides: Partial<ModelContextCapacityRow> = {}): ModelContextCapacityRow {
  return {
    upstream_model_id: 'real-model',
    aliases: ['public-a', 'public-b'],
    editable: true,
    upstream: {
      context_window: 262_144,
      max_context_window: 1_048_576,
      max_output_tokens: 8_192,
      capacity_basis: 'total_context',
      observed_at: '2026-09-07T10:00:00Z'
    },
    official: {
      model_id: 'real-model',
      provider: 'Example',
      product: 'API',
      context_window: 1_050_000,
      capacity_basis: 'total_context',
      source_url: 'https://example.com/models/real-model',
      verified_at: '2026-09-07',
      original_text: 'Context window: 1,050,000 tokens',
      normalization_basis: 'Exact integer in official specification',
      conditions: 'Hosted API only'
    },
    automatic_context_window: 1_050_000,
    automatic_source: 'official',
    effective_context_window: 1_050_000,
    effective_source: 'official',
    capacity_basis: 'total_context',
    ...overrides
  }
}

function mountPanel(rows: ModelContextCapacityRow[] = [capacityRow()], modelValue: Record<string, string> = {}) {
  return mount(ModelContextCapacityPanel, { props: { rows, modelValue } })
}

describe('ModelContextCapacityPanel', () => {
  it('renders the true target, aliases and precise source evidence', () => {
    const wrapper = mountPanel()
    expect(wrapper.text()).toContain('real-model')
    expect(wrapper.text()).toContain('public-a, public-b')
    expect(wrapper.text()).toContain('262.144K')
    expect(wrapper.text()).toContain('1.048576M')
    expect(wrapper.text()).toContain('8.192K')
    expect(wrapper.text()).toContain('2026-09-07T10:00:00Z')
    expect(wrapper.text()).toContain('2026-09-07')
    expect(wrapper.text()).toContain('Context window: 1,050,000 tokens')
    expect(wrapper.text()).toContain('Exact integer in official specification')
    expect(wrapper.text()).toContain('Hosted API only')
    expect(wrapper.get('a').attributes()).toMatchObject({
      href: 'https://example.com/models/real-model', target: '_blank', rel: 'noopener noreferrer'
    })
    expect(wrapper.get('[data-testid="context-capacity-effective"]').text()).toContain('1.05M')
    expect(wrapper.find('[data-testid="context-capacity-difference"]').exists()).toBe(true)
  })

  it('updates only a model draft and previews the highest custom priority', async () => {
    const wrapper = mountPanel([capacityRow()], { other: '258K' })
    await wrapper.get('input').setValue('2M')
    expect(wrapper.emitted('update:modelValue')).toEqual([[{ other: '258K', 'real-model': '2M' }]])
    // The parent owns drafts; the panel does not mutate props or call an API.
    expect(wrapper.props('modelValue')).toEqual({ other: '258K' })
    await wrapper.setProps({ modelValue: { other: '258K', 'real-model': '2M' } })
    expect(wrapper.get('[data-testid="context-capacity-effective"]').text()).toContain('2M')
    expect(wrapper.get('[data-testid="context-capacity-effective"]').text()).toContain('sources.custom')
  })

  it('keeps an untouched saved override and restores automatic priority only on explicit clear', async () => {
    const row = capacityRow({ custom_context_window: 900_000, effective_context_window: 900_000, effective_source: 'custom' })
    const wrapper = mountPanel([row])
    expect((wrapper.get('input').element as HTMLInputElement).value).toBe('900K')
    expect(wrapper.get('[data-testid="context-capacity-effective"]').text()).toContain('900K')
    await wrapper.get('[data-testid="context-capacity-clear"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')).toEqual([[{ 'real-model': '' }]])
    await wrapper.setProps({ modelValue: { 'real-model': '' } })
    expect(wrapper.get('[data-testid="context-capacity-effective"]').text()).toContain('1.05M')
    expect(wrapper.get('[data-testid="context-capacity-effective"]').text()).toContain('sources.official')
  })

  it('does not overwrite dirty input when newer upstream rows arrive', async () => {
    const wrapper = mountPanel([capacityRow()], { 'real-model': '1.234567M' })
    await wrapper.setProps({ rows: [capacityRow({ automatic_context_window: 1_000_000 })] })
    expect((wrapper.get('input').element as HTMLInputElement).value).toBe('1.234567M')
    expect(wrapper.get('[data-testid="context-capacity-effective"]').text()).toContain('1.234567M')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

  it('reports invalid input without rounding or presenting it as an adopted value', async () => {
    const wrapper = mountPanel()
    expect(wrapper.emitted('validity')?.at(-1)).toEqual([true])
    await wrapper.setProps({ modelValue: { 'real-model': '1.0000001M' } })
    expect(wrapper.emitted('validity')?.at(-1)).toEqual([false])
    expect(wrapper.get('input').attributes('aria-invalid')).toBe('true')
    expect(wrapper.get('[role="alert"]').text()).toContain('contextCapacity.invalid')
    expect(wrapper.get('[data-testid="context-capacity-effective"]').text()).toContain('—')
    expect(wrapper.find('[data-testid="context-capacity-difference"]').exists()).toBe(false)
    await wrapper.setProps({ modelValue: { 'real-model': '258K' } })
    expect(wrapper.emitted('validity')?.at(-1)).toEqual([true])
  })

  it('keeps protected catalogs read-only even if an unrelated draft is provided', () => {
    const wrapper = mountPanel([capacityRow({ editable: false, effective_context_window: 372_000, effective_source: 'protected' })], { 'real-model': '2M' })
    expect(wrapper.find('input').exists()).toBe(false)
    expect(wrapper.find('[data-testid="context-capacity-clear"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="context-capacity-readonly"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="context-capacity-effective"]').text()).toContain('372K')
    expect(wrapper.get('[data-testid="context-capacity-effective"]').text()).toContain('sources.protected')
  })

  it('preserves input-only semantics when clearing a custom value', () => {
    const row = capacityRow()
    row.official = { ...row.official!, context_window: undefined, max_input_tokens: 1_048_576, capacity_basis: 'input_limit' }
    row.automatic_context_window = 1_048_576
    const wrapper = mountPanel([row], { 'real-model': '' })
    expect(wrapper.get('[data-testid="context-capacity-effective"]').text()).toContain('1.048576M')
    expect(wrapper.text()).toContain('basis.input_limit')
  })

  it('renders dynamic rows and unknown protected values without suggesting a default', async () => {
    const wrapper = mountPanel([])
    expect(wrapper.text()).toContain('contextCapacity.empty')
    await wrapper.setProps({ rows: [capacityRow({ upstream_model_id: 'dynamic-model', editable: false, effective_context_window: 0, effective_source: 'protected' })] })
    expect(wrapper.text()).toContain('dynamic-model')
    expect(wrapper.get('[data-testid="context-capacity-effective"]').text()).toContain('—')
  })

  it('ignores unsafe source URL schemes and renders quiet loading failures', async () => {
    const row = capacityRow()
    row.official = { ...row.official!, source_url: 'javascript:alert(1)', source_urls: ['https://example.com/safe', 'data:text/plain,unsafe'] }
    const wrapper = mountPanel([row])
    expect(wrapper.findAll('a').map(link => link.attributes('href'))).toEqual(['https://example.com/safe'])
    await wrapper.setProps({ error: 'Saved capacities are currently unavailable.' })
    expect(wrapper.get('[role="status"]').text()).toBe('Saved capacities are currently unavailable.')
  })

  it('emits a shared sync action without changing any capacity drafts', async () => {
    const wrapper = mountPanel([capacityRow()], { 'real-model': '1.05M' })
    expect(wrapper.find('[data-testid="context-capacity-sync"]').exists()).toBe(false)
    await wrapper.setProps({ canSync: true })
    const button = wrapper.get('[data-testid="context-capacity-sync"]')
    expect(button.text()).toBe('admin.accounts.syncUpstreamModels')
    await button.trigger('click')
    expect(wrapper.emitted('sync')).toEqual([[]])
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(wrapper.props('modelValue')).toEqual({ 'real-model': '1.05M' })
  })

  it('disables sync for unsaved profile changes and displays the supplied safe reason', async () => {
    const wrapper = mountPanel()
    await wrapper.setProps({
      canSync: true,
      syncDisabled: true,
      syncDisabledReason: 'Save endpoint changes before syncing upstream models.'
    })
    const button = wrapper.get('[data-testid="context-capacity-sync"]')
    expect(button.attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('Save endpoint changes before syncing upstream models.')
    await button.trigger('click')
    expect(wrapper.emitted('sync')).toBeUndefined()
  })

  it('disables repeated sync while its parent is syncing', async () => {
    const wrapper = mountPanel()
    await wrapper.setProps({ canSync: true, syncing: true })
    const button = wrapper.get('[data-testid="context-capacity-sync"]')
    expect(button.attributes('disabled')).toBeDefined()
    expect(button.text()).toBe('admin.accounts.syncUpstreamModelsLoading')
    expect(wrapper.get('[data-testid="model-context-capacity-panel"]').attributes('aria-busy')).toBe('true')
    await button.trigger('click')
    expect(wrapper.emitted('sync')).toBeUndefined()
  })
})
