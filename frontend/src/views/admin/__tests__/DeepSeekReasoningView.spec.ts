import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import DeepSeekReasoningView from '../DeepSeekReasoningView.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), save: vi.fn(), showSuccess: vi.fn() }))
vi.mock('@/api/admin/deepseekReasoning', () => ({ deepseekReasoningAPI: mocks }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: mocks.showSuccess }) }))
vi.mock('vue-i18n', async importOriginal => ({ ...(await importOriginal<typeof import('vue-i18n')>()), useI18n: () => ({ t: (key: string) => key }) }))

function render() {
  return mount(DeepSeekReasoningView, { global: { stubs: { AppLayout: { template: '<main><slot /></main>' } } } })
}

describe('DeepSeek default reasoning settings', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    mocks.get.mockResolvedValue({ enabled: false, effort: 'max' })
    mocks.save.mockImplementation(async config => config)
  })

  it('loads settings, tracks edits, and saves the selected configuration', async () => {
    const wrapper = render()
    await flushPromises()
    expect((wrapper.get('[data-test="effort"]').element as HTMLSelectElement).value).toBe('max')
    expect(wrapper.get('[data-test="save"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-test="enabled"]').trigger('click')
    await wrapper.get('[data-test="effort"]').setValue('low')
    expect(wrapper.text()).toContain('admin.deepseekReasoning.unsaved')
    await wrapper.get('[data-test="save"]').trigger('click')
    await flushPromises()
    expect(mocks.save).toHaveBeenCalledWith({ enabled: true, effort: 'low' })
    expect(mocks.showSuccess).toHaveBeenCalledWith('admin.deepseekReasoning.saved')
    expect(wrapper.get('[data-test="save"]').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it('shows loading while the settings request is pending', async () => {
    let finish!: (value: { enabled: boolean; effort: string }) => void
    mocks.get.mockReturnValue(new Promise(resolve => { finish = resolve }))
    const wrapper = render()
    await flushPromises()
    expect(wrapper.text()).toContain('common.loading')
    expect(wrapper.get('[data-test="save"]').attributes('disabled')).toBeDefined()
    finish({ enabled: false, effort: 'max' })
    await flushPromises()
    expect(wrapper.find('[data-test="effort"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('keeps the draft editable when saving fails', async () => {
    mocks.save.mockRejectedValue(new Error('store unavailable'))
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-test="enabled"]').trigger('click')
    await wrapper.get('[data-test="save"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBeTruthy()
    expect(wrapper.text()).toContain('admin.deepseekReasoning.unsaved')
    expect(wrapper.get('[data-test="save"]').attributes('disabled')).toBeUndefined()
    expect(mocks.showSuccess).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('shows a load error without exposing a blank configuration to save', async () => {
    mocks.get.mockRejectedValue(new Error('unavailable'))
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBeTruthy()
    expect(wrapper.find('[data-test="effort"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="save"]').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })
})
