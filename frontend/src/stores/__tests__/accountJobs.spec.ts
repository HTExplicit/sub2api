import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

const { list, get, listItems, cancel, retryFailed } = vi.hoisted(() => ({
  list: vi.fn(),
  get: vi.fn(),
  listItems: vi.fn(),
  cancel: vi.fn(),
  retryFailed: vi.fn(),
}))

vi.mock('@/api/admin/accountJobs', () => ({
  default: { list, get, listItems, cancel, retryFailed },
}))
vi.mock('@/i18n', () => ({
  i18n: { global: { t: (key: string) => key } },
}))

import { useAccountJobsStore } from '@/stores/accountJobs'
import { useAppStore } from '@/stores/app'
import type { AccountJob } from '@/api/admin/accountJobs'

function job(status: AccountJob['status'], overrides: Partial<AccountJob> = {}): AccountJob {
  return {
    id: 19,
    created_by: 1,
    kind: 'account_import',
    status,
    metadata: {},
    target_count: 10,
    processed_count: status === 'running' ? 4 : 10,
    succeeded_count: status === 'succeeded' ? 10 : 0,
    failed_count: status === 'failed' ? 10 : 0,
    canceled_count: 0,
    attempt: 1,
    created_at: '2026-08-20T00:00:00Z',
    updated_at: '2026-08-20T00:01:00Z',
    ...overrides,
  }
}

describe('useAccountJobsStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.useFakeTimers()
    vi.clearAllMocks()
    list.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 })
    get.mockResolvedValue(job('running'))
    listItems.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 })
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('notifies once only when a job tracked in this session becomes terminal', async () => {
    const store = useAccountJobsStore()
    const app = useAppStore()
    store.track(job('running'))
    list
      .mockResolvedValueOnce({ items: [job('succeeded')], total: 1, page: 1, page_size: 20 })
      .mockResolvedValueOnce({ items: [job('succeeded')], total: 1, page: 1, page_size: 20 })

    await store.loadRecent()
    await store.loadRecent()

    expect(app.toasts).toHaveLength(1)
    expect(app.toasts[0].type).toBe('success')
  })

  it('does not notify for terminal jobs discovered from history', async () => {
    list.mockResolvedValue({ items: [job('failed')], total: 1, page: 1, page_size: 20 })
    const store = useAccountJobsStore()
    const app = useAppStore()

    await store.loadRecent()

    expect(app.toasts).toHaveLength(0)
  })

  it('stores recent, active, current and paginated item state', async () => {
    list.mockResolvedValue({
      items: [job('running'), job('succeeded', { id: 20 })],
      total: 7,
      page: 2,
      page_size: 2,
    })
    get.mockResolvedValue(job('running', { processed_count: 5 }))
    listItems.mockResolvedValue({
      items: [{
        id: 90,
        job_id: 19,
        ordinal: 1,
        status: 'succeeded',
        metadata: { account_id: 7 },
        created_at: '2026-08-20T00:00:00Z',
        updated_at: '2026-08-20T00:01:00Z',
      }],
      total: 4,
      page: 2,
      page_size: 1,
    })
    const store = useAccountJobsStore()

    await store.loadRecent({ page: 2, page_size: 2 })
    await store.openJob(19, { page: 2, page_size: 1 })

    expect(store.recentJobs.map((item) => item.id)).toEqual([19, 20])
    expect(store.activeJobs.map((item) => item.id)).toEqual([19])
    expect(store.currentJob?.processed_count).toBe(5)
    expect(store.items.map((item) => item.id)).toEqual([90])
    expect(store.jobPage).toEqual({ total: 7, page: 2, pageSize: 2 })
    expect(store.itemPage).toEqual({ total: 4, page: 2, pageSize: 1 })
    expect(store.drawerOpen).toBe(true)
  })

  it('retries failed items and tracks the replacement job', async () => {
    retryFailed.mockResolvedValue(job('pending', { id: 22, retry_of_job_id: 19, attempt: 2 }))
    const store = useAccountJobsStore()

    const replacement = await store.retryJob(19)

    expect(retryFailed).toHaveBeenCalledWith(19)
    expect(replacement.id).toBe(22)
    expect(store.currentJob?.id).toBe(22)
    expect(store.drawerOpen).toBe(true)
  })

  it('starts item pagination from page one when opening a different job', async () => {
    const store = useAccountJobsStore()
    listItems.mockResolvedValueOnce({ items: [], total: 25, page: 3, page_size: 10 })
    await store.openJob(19, { page: 3, page_size: 10 })
    listItems.mockClear()
    get.mockResolvedValue(job('running', { id: 20 }))

    await store.openJob(20)

    expect(listItems).toHaveBeenCalledWith(20, {
      page: 1,
      page_size: 10,
      status: undefined,
    })
  })

  it('stops polling and clears all session state on session change', async () => {
    const store = useAccountJobsStore()
    store.track(job('running'))
    store.startPolling()
    await vi.advanceTimersByTimeAsync(3_100)
    expect(list).toHaveBeenCalled()

    store.clear()
    const callsAfterClear = list.mock.calls.length
    await vi.advanceTimersByTimeAsync(10_000)

    expect(list).toHaveBeenCalledTimes(callsAfterClear)
    expect(store.recentJobs).toEqual([])
    expect(store.currentJob).toBeNull()
    expect(store.items).toEqual([])
    expect(store.drawerOpen).toBe(false)
  })

  it('polls the currently selected task page and filters without resetting them', async () => {
    const store = useAccountJobsStore()
    list.mockResolvedValue({ items: [], total: 0, page: 3, page_size: 25 })
    await store.loadRecent({ page: 3, page_size: 25, kind: 'account_import', status: 'running' })
    list.mockClear()

    store.startPolling()
    await vi.advanceTimersByTimeAsync(3_100)

    expect(list).toHaveBeenCalledWith({
      page: 3,
      page_size: 25,
      kind: 'account_import',
      status: 'running',
    })
  })
})
