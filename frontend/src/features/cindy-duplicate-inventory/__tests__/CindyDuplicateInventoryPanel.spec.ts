import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const { loadInventory, showError } = vi.hoisted(() => ({
  loadInventory: vi.fn(),
  showError: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: { accounts: { getCindyDuplicateIdentityInventory: loadInventory } }
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string, args?: any) => `${key}${args ? JSON.stringify(args) : ''}` }) }))

import CindyDuplicateInventoryPanel from '../CindyDuplicateInventoryPanel.vue'

describe('CindyDuplicateInventoryPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    loadInventory.mockResolvedValue([])
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
