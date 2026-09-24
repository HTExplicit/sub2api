import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

enableAutoUnmount(afterEach)

const { loadInventory, showError } = vi.hoisted(() => ({
  loadInventory: vi.fn(),
  showError: vi.fn()
}))

vi.mock('@/features/cindy/api', () => ({
  adminAPI: { accounts: { getCindyDuplicateIdentityInventory: loadInventory } }
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError }) }))
vi.mock('@/features/cindy/nativeState', async () => { const { computed } = await import('vue'); return { useCindyAdminScope: () => ({ available: computed(() => true) }) } })
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string, args?: any) => `${key}${args ? JSON.stringify(args) : ''}` }) }))

import CindyDuplicateInventoryPanel from '../CindyDuplicateInventoryPanel.vue'

describe('CindyDuplicateInventoryPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    loadInventory.mockResolvedValue([])
  })

  it('does not label a failed inventory read as an empty successful inventory', async () => {
    loadInventory.mockRejectedValueOnce(new Error('fixture unavailable'))
    const wrapper = mount(CindyDuplicateInventoryPanel)
    await flushPromises()
    expect(wrapper.find('[data-testid="cindy-duplicate-empty"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="cindy-duplicate-load-failed"]').text()).toContain('duplicateLoadFailed')
  })

  it('does not notify from a late inventory reply after unmount', async () => {
    let reject!: (error: Error) => void
    loadInventory.mockReturnValueOnce(new Promise((_, failure) => { reject = failure }))
    const wrapper = mount(CindyDuplicateInventoryPanel)
    wrapper.unmount()
    reject(new Error('late fixture error'))
    await flushPromises()
    expect(showError).not.toHaveBeenCalled()
  })

  it('loads the redacted inventory without exposing credential material', async () => {
    loadInventory.mockResolvedValueOnce([{
      identity_hash: 'a'.repeat(64),
      proposed_owner_id: 41,
      other_account_ids: [42, 43]
    }])
    const wrapper = mount(CindyDuplicateInventoryPanel)
    await flushPromises()

    expect(loadInventory).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('41')
    expect(wrapper.text()).toContain('42, 43')
    expect(wrapper.text()).not.toContain('a'.repeat(64))
  })
})
