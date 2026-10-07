import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
}))

vi.mock('@/api/client', () => ({ apiClient: { get, post } }))

import accountJobsAPI from '@/api/admin/accountJobs'

describe('accountJobsAPI', () => {
  beforeEach(() => {
    get.mockReset()
    post.mockReset()
    get.mockResolvedValue({ data: { items: [], total: 0, page: 1, page_size: 20 } })
    post.mockResolvedValue({ data: { id: 41, status: 'pending' } })
  })

  it('uses the account job list, detail, item and cancellation routes', async () => {
    const controller = new AbortController()

    await accountJobsAPI.list(
      { status: 'running', page: 2, page_size: 25 },
      { signal: controller.signal },
    )
    await accountJobsAPI.get(41, { signal: controller.signal })
    await accountJobsAPI.listItems(
      41,
      { status: 'failed', page: 3, page_size: 50 },
      { signal: controller.signal },
    )
    await accountJobsAPI.cancel(41)

    expect(get).toHaveBeenNthCalledWith(1, '/admin/account-jobs', {
      params: { status: 'running', page: 2, page_size: 25 },
      signal: controller.signal,
    })
    expect(get).toHaveBeenNthCalledWith(2, '/admin/account-jobs/41', {
      signal: controller.signal,
    })
    expect(get).toHaveBeenNthCalledWith(3, '/admin/account-jobs/41/items', {
      params: { status: 'failed', page: 3, page_size: 50 },
      signal: controller.signal,
    })
    expect(post).toHaveBeenNthCalledWith(1, '/admin/account-jobs/41/cancel')
  })

  it('adds a fresh Idempotency-Key to each retry submission', async () => {
    await accountJobsAPI.retryFailed(41)
    await accountJobsAPI.retryFailed(41)

    for (const call of [1, 2]) {
      expect(post).toHaveBeenNthCalledWith(
        call,
        '/admin/account-jobs/41/retry-failed',
        undefined,
        { headers: { 'Idempotency-Key': expect.stringMatching(/^account_job_retry-/) } },
      )
    }

    const keys = post.mock.calls.map((call) => call[2]?.headers?.['Idempotency-Key'])
    expect(new Set(keys).size).toBe(2)
  })
})
