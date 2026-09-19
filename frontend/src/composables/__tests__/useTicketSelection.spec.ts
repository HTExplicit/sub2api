import { ref, defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { describe, it, expect, vi } from 'vitest'
import { useTicketSelection } from '../useTicketSelection'
import type { TicketAccountIdentity } from '@/utils/codexTicketEligibility'
const api = vi.hoisted(() => ({ list: vi.fn() }))
vi.mock('@/api/admin/accounts', () => api)
const account = (id: number, type: 'oauth' | 'setup-token' | 'apikey' = 'oauth'): TicketAccountIdentity => ({ id, type, platform: 'openai', parent_account_id: null })
describe('ticket selection across pages', () => {
  it('hides unknown and mixed selections, and admits a fully verified OAuth-like selection', async () => {
    const ids = ref([1, 2]), rows = ref([account(1)])
    let resolve!: (value: unknown) => void
    api.list.mockReturnValueOnce(new Promise(r => { resolve = r }))
    let selection!: ReturnType<typeof useTicketSelection>
    const wrapper = mount(defineComponent({ setup() { selection = useTicketSelection(ids, rows); return () => null } }))
    expect(selection.canHarvestTickets.value).toBe(false)
    resolve({ items: [account(2, 'setup-token')] })
    await flushPromises()
    expect(selection.canHarvestTickets.value).toBe(true)
    rows.value = [account(2, 'apikey')]
    await flushPromises()
    expect(selection.canHarvestTickets.value).toBe(false)
    ids.value = []
    await flushPromises()
    expect(selection.canHarvestTickets.value).toBe(false)
    wrapper.unmount()
  })
  it('ignores an obsolete fetch after selection changes and never infers shadow eligibility', async () => {
    const ids = ref([1, 2]), rows = ref([account(1)])
    let resolve!: (value: unknown) => void
    api.list.mockReturnValueOnce(new Promise(r => { resolve = r }))
    let selection!: ReturnType<typeof useTicketSelection>
    const wrapper = mount(defineComponent({ setup() { selection = useTicketSelection(ids, rows); return () => null } }))
    ids.value = [3]; rows.value = [{ ...account(3), parent_account_id: 1 }]
    await flushPromises()
    resolve({ items: [account(3)] }); await flushPromises()
    expect(selection.canHarvestTickets.value).toBe(false)
    wrapper.unmount()
  })
})
