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
// Every control of the page, in document order.
const controls = (wrapper: View) => wrapper.findAll('button').map(button => button.attributes('data-test'))

describe('ReasoningRecoveryView', () => {
  beforeEach(() => {
    get.mockReset().mockResolvedValue({ data: { enabled: true } })
    put.mockReset().mockImplementation(async (_url: string, value: unknown) => ({ data: value }))
    showSuccess.mockReset()
    useStepUp.mockReset()
  })

  it('shows only Save until the stored value is loaded, then the switch, and enables Save only while the draft differs', async () => {
    let answer!: (value: { data: { enabled: boolean } }) => void
    get.mockReturnValueOnce(new Promise((resolve) => { answer = resolve }))
    const wrapper = await mountView()

    expect(get).toHaveBeenCalledWith(path)
    expect(wrapper.text()).toContain('common.loading')
    expect(controls(wrapper)).toEqual(['reasoning-recovery-save'])
    expect(saveButton(wrapper).element.disabled).toBe(true)

    answer({ data: { enabled: true } })
    await flushPromises()
    expect(wrapper.text()).not.toContain('common.loading')
    expect(controls(wrapper)).toEqual(['reasoning-recovery-save', 'reasoning-recovery-enabled'])
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

  it('reports a failed or unusable load without a switch, and reads again only when the page is opened again', async () => {
    get.mockRejectedValueOnce({ message: 'load refused' })
    const refused = await mountView()

    expect(refused.get('[role="alert"]').text()).toBe('load refused')
    expect(controls(refused)).toEqual(['reasoning-recovery-save'])
    expect(saveButton(refused).element.disabled).toBe(true)
    expect(get).toHaveBeenCalledTimes(1)
    refused.unmount()

    get.mockResolvedValueOnce({ data: {} })
    const unusable = await mountView()
    expect(unusable.get('[role="alert"]').text()).toBe('admin.reasoningRecovery.loadFailed')
    expect(controls(unusable)).toEqual(['reasoning-recovery-save'])
    unusable.unmount()

    get.mockResolvedValueOnce({ data: { enabled: false } })
    const reopened = await mountView()
    expect(get).toHaveBeenCalledTimes(3)
    expect(reopened.find('[role="alert"]').exists()).toBe(false)
    expect(toggle(reopened).attributes('aria-checked')).toBe('false')
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
