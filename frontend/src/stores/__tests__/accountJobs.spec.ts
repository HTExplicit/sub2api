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
    }, { signal: expect.any(AbortSignal) })
  })

  it('does not poll either the list or details, including after tracking a task', async () => {
    const store = useAccountJobsStore()
    store.track(job('running'))
    await vi.advanceTimersByTimeAsync(10_000)
    expect(list).not.toHaveBeenCalled()
    expect(get).not.toHaveBeenCalled()
    expect(listItems).not.toHaveBeenCalled()

    store.clear()
    await vi.advanceTimersByTimeAsync(10_000)

    expect(list).not.toHaveBeenCalled()
    expect(store.recentJobs).toEqual([])
    expect(store.currentJob).toBeNull()
    expect(store.items).toEqual([])
    expect(store.drawerOpen).toBe(false)
  })

  it('refreshes the selected page and filters only on an explicit request', async () => {
    const store = useAccountJobsStore()
    list.mockResolvedValue({ items: [], total: 0, page: 3, page_size: 25 })
    await store.loadRecent({ page: 3, page_size: 25, kind: 'account_import', status: 'running' })
    list.mockClear()

    await vi.advanceTimersByTimeAsync(10_000)
    expect(list).not.toHaveBeenCalled()
    await store.loadRecent()

    expect(list).toHaveBeenCalledWith({
      page: 3,
      page_size: 25,
      kind: 'account_import',
      status: 'running',
    }, { signal: expect.any(AbortSignal) })
  })

  it('ignores a superseded slow list response and keeps the latest page and filter', async () => {
    const store = useAccountJobsStore()
    let resolveOld!: (value: unknown) => void
    list.mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
    const oldRequest = store.loadRecent({ page: 2, status: 'running' })
    const oldSignal = list.mock.calls[0][1].signal as AbortSignal
    list.mockResolvedValueOnce({ items: [job('failed', { id: 21 })], total: 1, page: 1, page_size: 20 })

    await store.loadRecent({ page: 1, status: 'failed' })
    resolveOld({ items: [job('running')], total: 10, page: 2, page_size: 20 })
    await oldRequest

    expect(oldSignal.aborted).toBe(true)
    expect(store.recentJobs.map(item => item.id)).toEqual([21])
    expect(store.jobPage.page).toBe(1)
    expect(store.loadingJobs).toBe(false)
  })

  it('silently handles cancellation and does not clear the newer loading state', async () => {
    const store = useAccountJobsStore()
    list.mockImplementationOnce((_params, { signal }: { signal: AbortSignal }) => new Promise((_resolve, reject) => {
      signal.addEventListener('abort', () => reject({ code: 'ERR_CANCELED' }))
    }))
    const canceled = store.loadRecent({ page: 2 })
    let resolveNew!: (value: unknown) => void
    list.mockImplementationOnce(() => new Promise(resolve => { resolveNew = resolve }))
    const current = store.loadRecent({ page: 3 })
    await canceled
    expect(store.loadingJobs).toBe(true)
    resolveNew({ items: [], total: 0, page: 3, page_size: 20 })
    await current
    expect(store.loadingJobs).toBe(false)
    expect(useAppStore().toasts).toHaveLength(0)
  })

  it('ignores detail responses after selecting a different job or closing the drawer', async () => {
    const store = useAccountJobsStore()
    let resolveOld!: (value: AccountJob) => void
    get.mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
    const oldRequest = store.openJob(19)
    get.mockResolvedValueOnce(job('running', { id: 20 }))
    await store.openJob(20)
    resolveOld(job('failed'))
    await oldRequest
    expect(store.currentJob?.id).toBe(20)

    let resolveClosed!: (value: AccountJob) => void
    get.mockImplementationOnce(() => new Promise(resolve => { resolveClosed = resolve }))
    const closedRequest = store.loadCurrent(20)
    store.closeDrawer()
    resolveClosed(job('failed', { id: 20 }))
    await closedRequest
    expect(store.currentJob?.status).toBe('running')
    expect(store.loadingCurrent).toBe(false)
    expect(store.drawerOpen).toBe(false)
  })

  it('clears an expired selection and its items on a detail 404', async () => {
    const store = useAccountJobsStore()
    store.track(job('running'))
    store.itemPage.total = 5
    store.itemPage.page = 2
    get.mockRejectedValueOnce({ status: 404 })

    await store.openJob(19)

    expect(store.currentJob).toBeNull()
    expect(store.items).toEqual([])
    expect(store.itemPage.total).toBe(0)
    expect(store.itemPage.page).toBe(1)
    expect(store.loadingCurrent).toBe(false)
    get.mockClear()
    await store.refreshDrawer()
    expect(get).not.toHaveBeenCalled()
  })

  it('does not insert off-page detail or submitted tasks into a filtered list', async () => {
    const store = useAccountJobsStore()
    list.mockResolvedValue({ items: [job('failed', { id: 22 })], total: 4, page: 2, page_size: 1 })
    await store.loadRecent({ page: 2, page_size: 1, status: 'failed' })
    await store.openJob(19)
    store.track(job('pending', { id: 23 }))

    expect(store.recentJobs.map(item => item.id)).toEqual([22])
    expect(store.jobPage).toEqual({ total: 4, page: 2, pageSize: 1 })
  })

  it('cancels pending reads and prevents their state from returning after session clear', async () => {
    const store = useAccountJobsStore()
    let resolveOld!: (value: unknown) => void
    list.mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
    const pending = store.loadRecent()
    const signal = list.mock.calls[0][1].signal as AbortSignal
    store.clear()
    resolveOld({ items: [job('running')], total: 1, page: 1, page_size: 20 })
    await pending

    expect(signal.aborted).toBe(true)
    expect(store.recentJobs).toEqual([])
    expect(store.loadingJobs).toBe(false)
  })
})
