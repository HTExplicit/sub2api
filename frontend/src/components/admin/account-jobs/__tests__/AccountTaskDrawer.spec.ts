import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
const api = vi.hoisted(() => ({ list: vi.fn(), get: vi.fn(), listItems: vi.fn(), cancel: vi.fn(), retryFailed: vi.fn(), mergeDuplicates: vi.fn() }))
vi.mock('@/api/admin/accountJobs', () => ({ default: api }))
vi.mock('@/api/admin/accounts', () => ({ list: vi.fn().mockResolvedValue({ items: [] }) }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))
import AccountTaskDrawer from '../AccountTaskDrawer.vue'
import AccountOperationDialog from '../AccountOperationDialog.vue'
import { useAccountJobsStore } from '@/stores/accountJobs'
import type { AccountJob } from '@/api/admin/accountJobs'
const base: AccountJob = { id: 51, created_by: 1, kind: 'account_batch_refresh', status: 'running', metadata: { secret: 'MustNotRender' }, target_count: 2, processed_count: 1, succeeded_count: 1, failed_count: 0, canceled_count: 0, attempt: 1, created_at: '2026-08-21T00:00:00Z', updated_at: '2026-08-21T00:01:00Z' }
let wrappers: VueWrapper[]
const options = { global: { stubs: { Teleport: true, Icon: true } } }
function button(wrapper: VueWrapper, label: string) { return wrapper.findAll('button').find(b => b.text().endsWith(label))! }
describe('account operation presentation', () => {
  beforeEach(() => {
    setActivePinia(createPinia()); vi.resetAllMocks(); vi.useFakeTimers(); wrappers = []
    api.get.mockImplementation(async () => ({ ...useAccountJobsStore().currentJob }))
    api.list.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 })
    api.listItems.mockImplementation(async () => ({ items: [...useAccountJobsStore().items], total: useAccountJobsStore().items.length, page: 1, page_size: 20 }))
  })
  afterEach(() => { wrappers.forEach(w => w.unmount()); useAccountJobsStore().clear(); vi.useRealTimers() })
  it('keeps execution in the original window and minimizes without canceling', async () => {
    const wrapper = mount(AccountOperationDialog, { ...options, props: { show: true, title: 'Refresh', job: base } }); wrappers.push(wrapper)
    await flushPromises()
    const store = useAccountJobsStore()
    expect(store.embeddedOpen).toBe(true)
    expect(store.drawerOpen).toBe(false)
    expect(wrapper.get('[role="progressbar"]').attributes('aria-valuenow')).toBe('50')
    await button(wrapper, '.minimize').trigger('click')
    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(api.cancel).not.toHaveBeenCalled()
    await wrapper.setProps({ show: false })
    const host = mount(AccountTaskDrawer, options); wrappers.push(host)
    expect(host.find('[data-test="operation-dock"]').exists()).toBe(true)
    api.get.mockResolvedValue({ ...base, status: 'succeeded', processed_count: 2, succeeded_count: 2 })
    await vi.advanceTimersByTimeAsync(3000)
    expect(store.activeCount).toBe(0)
    expect(store.drawerOpen).toBe(false)
    expect(host.text()).toContain('statuses.succeeded')
  })
  it('shows stopping until the server confirms a terminal result and retries only failures', async () => {
    const store = useAccountJobsStore(); store.track(base)
    const wrapper = mount(AccountTaskDrawer, options); wrappers.push(wrapper); await flushPromises()
    api.cancel.mockResolvedValue({ ...base, cancel_requested_at: '2026-08-21T00:02:00Z' })
    await button(wrapper, '.cancel').trigger('click'); await flushPromises()
    expect(button(wrapper, '.stopping').attributes('disabled')).toBeDefined()
    expect(button(wrapper, '.retryFailed')).toBeUndefined()
    store.currentJob = { ...base, status: 'partially_succeeded', processed_count: 2, failed_count: 1 }
    await store.loadCurrent(base.id)
    await flushPromises()
    expect(api.listItems.mock.calls.at(-1)?.[1].status).toBe('failed')
    api.retryFailed.mockResolvedValue({ ...base, id: 52, status: 'pending', target_count: 1, processed_count: 0, retry_of_job_id: 51 })
    await button(wrapper, '.retryFailed').trigger('click'); await flushPromises()
    expect(api.retryFailed).toHaveBeenCalledWith(51)
    expect(store.currentJob?.id).toBe(52)
  })
  it('validates safe duplicate metadata and requires explicit merge confirmation', async () => {
    const store = useAccountJobsStore()
    store.track({ ...base, kind: 'account_duplicate_review', status: 'succeeded', processed_count: 2 })
    store.items = [{ id: 101, job_id: 51, ordinal: 1, status: 'succeeded', metadata: { confirmation_hash: 'opaque-confirmation-token', api_key: 'MustNotRender', accounts: [{ account_id: 7, name: 'Keep me', group_count: 2, tag_count: 1, configuration_score: 9 }, { account_id: 8, name: 'Merge me', group_count: 1, tag_count: 0, configuration_score: 4 }] }, created_at: base.created_at, updated_at: base.updated_at }]
    const wrapper = mount(AccountTaskDrawer, options); wrappers.push(wrapper); await flushPromises()
    expect(wrapper.text()).toContain('Keep me')
    expect(wrapper.text()).not.toContain('MustNotRender')
    await wrapper.get('[data-test="duplicate-survivor-7"]').setValue(true)
    await wrapper.get('[data-test="duplicate-merge-submit"]').trigger('click')
    expect(api.mergeDuplicates).not.toHaveBeenCalled()
    api.mergeDuplicates.mockResolvedValue({ ...base, id: 52, kind: 'account_duplicate_merge', status: 'pending' })
    await wrapper.get('[data-test="duplicate-merge-submit"]').trigger('click'); await flushPromises()
    expect(api.mergeDuplicates).toHaveBeenCalledWith({ survivor_account_id: 7, loser_account_ids: [8], confirmation_hash: 'opaque-confirmation-token' })
    expect(store.currentJob?.id).toBe(52)
  })
  it('keeps expired retry failures actionable without claiming success', async () => {
    const store = useAccountJobsStore(); store.track({ ...base, status: 'failed', failed_count: 2 })
    const wrapper = mount(AccountTaskDrawer, options); wrappers.push(wrapper); await flushPromises()
    api.retryFailed.mockRejectedValue({ code: 'ACCOUNT_JOB_PAYLOAD_EXPIRED' })
    await button(wrapper, '.retryFailed').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('accountTasks.retryExpired')
    expect(button(wrapper, '.retryFailed')).toBeUndefined()
    expect(store.currentJob?.status).toBe('failed')
  })
})
