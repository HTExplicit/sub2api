import { ref } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import CindyProviderSettingsPanel from '@/components/admin/CindyProviderSettingsPanel.vue'

const api = vi.hoisted(() => ({ read: vi.fn(), save: vi.fn(), catalog: vi.fn() }))
vi.mock('@/api/admin/cindyProvider', () => ({ getCindyProviderSettings: api.read, updateCindyProviderSettings: api.save, getCindyProviderCatalog: api.catalog }))
vi.mock('@/features/cindy/nativeState', () => ({ useCindyAdminScope: () => ({ available: ref(true), actorID: ref(7) }) }))
vi.mock('vue-i18n', async importOriginal => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))
const initial = { balance_detection: false, catalog_enabled: true, search_enabled: true }
const render = () => mount(CindyProviderSettingsPanel, { global: { stubs: { TotpStepUpDialog: true } } })

describe('native Cindy provider settings', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    api.read.mockResolvedValue({ ...initial })
    api.catalog.mockResolvedValue([])
  })
  it('preserves edits made during save and suppresses follow-up catalog work after unmount', async () => {
    let finish!: (value: typeof initial) => void
    api.save.mockReturnValueOnce(new Promise(resolve => { finish = resolve }))
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="cindy-provider-balance_detection"]').setValue(true)
    await wrapper.get('[data-testid="cindy-provider-save"]').trigger('click')
    await wrapper.get('[data-testid="cindy-provider-balance_detection"]').setValue(false)
    finish({ ...initial, balance_detection: true })
    await flushPromises()
    expect((wrapper.get('[data-testid="cindy-provider-balance_detection"]').element as HTMLInputElement).checked).toBe(false)
    expect(wrapper.get('[role="status"]').text()).toBe('unsaved')
    api.save.mockReturnValueOnce(new Promise(resolve => { finish = resolve }))
    await wrapper.get('[data-testid="cindy-provider-save"]').trigger('click')
    const reads = api.catalog.mock.calls.length
    wrapper.unmount()
    finish({ ...initial })
    await flushPromises()
    expect(api.catalog).toHaveBeenCalledTimes(reads)
  })
  it('uses the existing step-up grant and retries only the captured settings', async () => {
    api.save.mockRejectedValueOnce({ code: 'STEP_UP_REQUIRED' }).mockResolvedValueOnce({ ...initial })
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="cindy-provider-save"]').trigger('click')
    await flushPromises()
    const dialog = wrapper.findComponent({ name: 'TotpStepUpDialog' })
    const controller = dialog.props('controller') as { visible: { value: boolean }; onVerified(): void }
    expect(controller.visible.value).toBe(true)
    controller.onVerified()
    await flushPromises()
    expect(api.save).toHaveBeenCalledTimes(2)
    expect(api.save.mock.calls[1]).toEqual(api.save.mock.calls[0])
    wrapper.unmount()
  })
})
