vi.mock('@/components/admin/account-jobs/AccountOperationDialog.vue', () => ({ default: { props: ['job', 'show'], template: '<div><slot/><slot name="footer"/></div>' } }))
import { mount, flushPromises } from '@vue/test-utils'
import { describe, it, expect, vi } from 'vitest'
import CodexTicketHarvestModal from '../CodexTicketHarvestModal.vue'
const mocks = vi.hoisted(() => ({ policy: vi.fn(), harvest: vi.fn(), stop: vi.fn(), getById: vi.fn() }))
vi.mock('@/api/admin/codexTickets', () => ({ codexTicketsAPI: mocks }))
vi.mock('@/api/admin/accounts', () => ({ getById: mocks.getById }))
vi.mock('@/api/admin/accountJobs', () => ({ accountJobIdempotencyHeaders: () => ({ headers: { 'Idempotency-Key': 'test-key' } }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
describe('manual account/model harvest', () => {
  function mountModal(ids = [1]) {
    mocks.policy.mockResolvedValue({ enabled: true, models: ['gpt-6-astra', 'gpt-5.6-sol'] })
    mocks.getById.mockImplementation(async (id: number) => ({ id, name: 'Account ' + id, platform: 'openai', type: 'setup-token', status: 'disabled' }))
    mocks.harvest.mockResolvedValue({ id: 77, status: 'pending' })
    return mount(CodexTicketHarvestModal, { props: { show: true, accountIds: ids } })
  }
  it('allows disabled Setup Token accounts and keeps results in the original window', async () => {
    const wrapper = mountModal()
    await flushPromises()
    expect(wrapper.findAll('input[type="checkbox"]').filter(i => (i.element as HTMLInputElement).checked)).toHaveLength(2)
    await wrapper.findAll('button').find(b => b.text().endsWith('.start'))!.trigger('click')
    await flushPromises()
    expect(mocks.getById).toHaveBeenCalledTimes(2) // Load + submission revalidation.
    expect(mocks.harvest).toHaveBeenCalledWith([1], ['gpt-6-astra', 'gpt-5.6-sol'], false, { headers: { 'Idempotency-Key': 'test-key' } })
    expect(wrapper.emitted('submitted')![0][0]).toEqual({ id: 77, status: 'pending' })
    expect(wrapper.emitted('close')).toBeUndefined()
    wrapper.unmount()
  })
  it('rejects a mixed set without silently submitting its eligible subset', async () => {
    mocks.harvest.mockClear()
    const wrapper = mountModal([1, 2])
    mocks.getById.mockImplementation(async (id: number) => ({ id, platform: id === 1 ? 'openai' : 'cindy', type: id === 1 ? 'oauth' : 'apikey' }))
    await flushPromises()
    expect(wrapper.findAll('button').find(b => b.text().endsWith('.start'))!.attributes('disabled')).toBeDefined()
    expect(mocks.harvest).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('rejects an account whose type changes immediately before submission', async () => {
    mocks.harvest.mockClear()
    const wrapper = mountModal()
    await flushPromises()
    mocks.getById.mockResolvedValue({ id: 1, platform: 'openai', type: 'apikey' })
    await wrapper.findAll('button').find(b => b.text().endsWith('.start'))!.trigger('click')
    await flushPromises()
    expect(mocks.harvest).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('accountTasks.selectionChanged')
    wrapper.unmount()
  })
})
