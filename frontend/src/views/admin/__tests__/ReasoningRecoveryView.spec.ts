import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import ReasoningRecoveryView from '../ReasoningRecoveryView.vue'

const { get, put, showSuccess, useStepUp } = vi.hoisted(() => ({
  get: vi.fn(),
  put: vi.fn(),
  showSuccess: vi.fn(),
  useStepUp: vi.fn(),
}))

vi.mock('@/api/client', () => ({ apiClient: { get, put } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess }) }))
vi.mock('@/composables/useStepUp', () => ({ useStepUp }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key }),
}))

const path = '/admin/reasoning-recovery'

async function mountView() {
  const wrapper = mount(ReasoningRecoveryView, {
    global: { stubs: { AppLayout: { template: '<div><slot /></div>' } } },
  })
  await flushPromises()
  return wrapper
}

type View = Awaited<ReturnType<typeof mountView>>
const toggle = (wrapper: View) => wrapper.get('[data-test="reasoning-recovery-enabled"]')
const saveButton = (wrapper: View) => wrapper.get<HTMLButtonElement>('[data-test="reasoning-recovery-save"]')

describe('ReasoningRecoveryView', () => {
  beforeEach(() => {
    get.mockReset().mockResolvedValue({ data: { enabled: true } })
    put.mockReset().mockImplementation(async (_url: string, value: unknown) => ({ data: value }))
    showSuccess.mockReset()
    useStepUp.mockReset()
  })

  it('shows no switch until the stored value is loaded, then enables Save only while the draft differs', async () => {
    let answer!: (value: { data: { enabled: boolean } }) => void
    get.mockReturnValueOnce(new Promise((resolve) => { answer = resolve }))
    const wrapper = await mountView()

    expect(get).toHaveBeenCalledWith(path)
    expect(wrapper.text()).toContain('common.loading')
    expect(wrapper.find('[data-test="reasoning-recovery-enabled"]').exists()).toBe(false)
    expect(saveButton(wrapper).element.disabled).toBe(true)

    answer({ data: { enabled: true } })
    await flushPromises()
    expect(wrapper.text()).not.toContain('common.loading')
    expect(toggle(wrapper).attributes('aria-checked')).toBe('true')
    expect(saveButton(wrapper).element.disabled).toBe(true)

    await toggle(wrapper).trigger('click')
    expect(toggle(wrapper).attributes('aria-checked')).toBe('false')
    expect(saveButton(wrapper).element.disabled).toBe(false)

    await toggle(wrapper).trigger('click')
    expect(saveButton(wrapper).element.disabled).toBe(true)
    expect(put).not.toHaveBeenCalled()
  })

  it('saves the draft with one PUT and no step-up', async () => {
    const wrapper = await mountView()
    await toggle(wrapper).trigger('click')
    await saveButton(wrapper).trigger('click')
    await flushPromises()

    expect(put).toHaveBeenCalledTimes(1)
    expect(put).toHaveBeenCalledWith(path, { enabled: false })
    expect(useStepUp).not.toHaveBeenCalled()
    expect(wrapper.findComponent({ name: 'TotpStepUpDialog' }).exists()).toBe(false)
    expect(showSuccess).toHaveBeenCalledWith('admin.reasoningRecovery.saved')
    expect(toggle(wrapper).attributes('aria-checked')).toBe('false')
    expect(saveButton(wrapper).element.disabled).toBe(true)
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
  })

  it('replaces the draft with the value the server stored', async () => {
    put.mockResolvedValue({ data: { enabled: true } })
    const wrapper = await mountView()
    await toggle(wrapper).trigger('click')
    await saveButton(wrapper).trigger('click')
    await flushPromises()

    expect(put).toHaveBeenCalledWith(path, { enabled: false })
    expect(toggle(wrapper).attributes('aria-checked')).toBe('true')
    expect(saveButton(wrapper).element.disabled).toBe(true)
  })

  it('reports a failed or unusable load without showing a value, and loads again on Reload', async () => {
    get.mockRejectedValueOnce({ message: 'load refused' })
    const wrapper = await mountView()

    expect(wrapper.get('[role="alert"]').text()).toBe('load refused')
    expect(wrapper.find('[data-test="reasoning-recovery-enabled"]').exists()).toBe(false)
    expect(saveButton(wrapper).element.disabled).toBe(true)

    get.mockResolvedValueOnce({ data: {} })
    await wrapper.get('[data-test="reasoning-recovery-reload"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('admin.reasoningRecovery.loadFailed')
    expect(wrapper.find('[data-test="reasoning-recovery-enabled"]').exists()).toBe(false)

    get.mockResolvedValueOnce({ data: { enabled: false } })
    await wrapper.get('[data-test="reasoning-recovery-reload"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(toggle(wrapper).attributes('aria-checked')).toBe('false')
  })

  it('reports a failed save and keeps the draft', async () => {
    put.mockRejectedValue({ message: 'save refused' })
    const wrapper = await mountView()
    await toggle(wrapper).trigger('click')
    await saveButton(wrapper).trigger('click')
    await flushPromises()

    expect(wrapper.get('[role="alert"]').text()).toBe('save refused')
    expect(showSuccess).not.toHaveBeenCalled()
    expect(toggle(wrapper).attributes('aria-checked')).toBe('false')
    expect(saveButton(wrapper).element.disabled).toBe(false)
  })
})
