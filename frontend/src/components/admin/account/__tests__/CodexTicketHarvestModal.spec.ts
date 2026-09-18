import { mount, flushPromises } from '@vue/test-utils'
import { describe, it, expect, vi } from 'vitest'
import CodexTicketHarvestModal from '../CodexTicketHarvestModal.vue'
const mocks = vi.hoisted(() => ({ policy: vi.fn(), harvest: vi.fn(), stop: vi.fn(), getById: vi.fn() }))
vi.mock('@/api/admin/codexTickets', () => ({ codexTicketsAPI: mocks }))
vi.mock('@/api/admin/accounts', () => ({ getById: mocks.getById }))
vi.mock('@/api/admin/accountJobs', () => ({ accountJobIdempotencyHeaders: () => ({ headers: { 'Idempotency-Key': 'test-key' } }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
describe('manual account/model harvest', () => {
  it('selects configured models, explains ineligible accounts and submits a persistent job', async () => {
    mocks.policy.mockResolvedValue({ enabled: true, models: ['gpt-6-astra', 'gpt-5.6-sol'] })
    mocks.getById.mockImplementation(async (id: number) => ({ id, name: 'Account ' + id, platform: id === 1 ? 'openai' : 'cindy', type: id === 1 ? 'oauth' : 'apikey', status: 'active' }))
    mocks.harvest.mockResolvedValue({ id: 77, status: 'pending' })
    const wrapper = mount(CodexTicketHarvestModal, { props: { show: true, accountIds: [1, 2] }, global: { stubs: { BaseDialog: { template: '<div><slot/><slot name="footer"/></div>' } } } })
    await flushPromises()
    expect(wrapper.text()).toContain('tickets.ineligible')
    expect(wrapper.findAll('input[type="checkbox"]').filter(i => (i.element as HTMLInputElement).checked)).toHaveLength(2)
    await wrapper.findAll('button').find(b => b.text().endsWith('.start'))!.trigger('click'); await flushPromises()
    expect(mocks.harvest).toHaveBeenCalledWith([1], ['gpt-6-astra', 'gpt-5.6-sol'], false, { headers: { 'Idempotency-Key': 'test-key' } })
    expect(wrapper.emitted('submitted')![0][0]).toEqual({ id: 77, status: 'pending' })
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })
})
