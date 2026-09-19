import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, ref } from 'vue'

import { useAccountSelectionMetadata } from '../useAccountSelectionMetadata'

const mocks = vi.hoisted(() => ({ list: vi.fn() }))
vi.mock('@/api/admin/accounts', () => ({ list: mocks.list }))

describe('useAccountSelectionMetadata', () => {
  it('keeps unknown cross-page selections ineligible until their identity read completes', async () => {
    let resolve!: (value: unknown) => void
    mocks.list.mockReturnValueOnce(new Promise(value => { resolve = value }))
    const ids = ref([1, 2])
    const rows = ref([{ id: 1, platform: 'openai', type: 'oauth', status: 'active', parent_account_id: null }])
    let selection!: ReturnType<typeof useAccountSelectionMetadata>
    const wrapper = mount(defineComponent({ setup() { selection = useAccountSelectionMetadata(ids, rows); return () => null } }))

    expect(selection.selectedAccounts.value.map(item => item.id)).toEqual([1])
    resolve({ items: [{ id: 2, platform: 'openai', type: 'setup-token', status: 'disabled', parent_account_id: null }] })
    await flushPromises()
    expect(selection.selectedAccounts.value).toEqual([
      { id: 1, platform: 'openai', type: 'oauth', status: 'active', parent_account_id: null },
      { id: 2, platform: 'openai', type: 'setup-token', status: 'disabled', parent_account_id: null }
    ])
    wrapper.unmount()
  })

  it('stores only identity fields from list rows', async () => {
    mocks.list.mockResolvedValueOnce({ items: [] })
    const ids = ref([9])
    const rows = ref([{ id: 9, platform: 'openai', type: 'oauth', status: 'active', parent_account_id: null, credentials: { access_token: 'not-retained' } }])
    let selection!: ReturnType<typeof useAccountSelectionMetadata>
    const wrapper = mount(defineComponent({ setup() { selection = useAccountSelectionMetadata(ids, rows); return () => null } }))
    await flushPromises()
    expect(selection.selectedAccounts.value[0]).toEqual({ id: 9, platform: 'openai', type: 'oauth', status: 'active', parent_account_id: null })
    wrapper.unmount()
  })
})
