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
      { kind: 'account_import', status: 'running', page: 2, page_size: 25 },
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
      params: { kind: 'account_import', status: 'running', page: 2, page_size: 25 },
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

  it('uses batched discovery and persists individual model IDs', async () => {
    const signal = new AbortController().signal
    post.mockResolvedValueOnce({ data: { items: [{ account_id: 7, models: [] }] } })
    expect(await accountJobsAPI.batchTestModels([7], signal)).toEqual([{ account_id: 7, models: [] }])
    await accountJobsAPI.batchTest([{ account_id: 7, model_id: 'raw-alias' }])
    expect(post).toHaveBeenNthCalledWith(1, '/admin/accounts/batch-test-models', { account_ids: [7] }, { signal })
    expect(post).toHaveBeenNthCalledWith(2, '/admin/accounts/batch-test', { items: [{ account_id: 7, model_id: 'raw-alias' }] }, { headers: { 'Idempotency-Key': expect.stringMatching(/^account_batch_test-/) } })
  })

  it('adds a fresh Idempotency-Key to retry and duplicate job submissions', async () => {
    await accountJobsAPI.retryFailed(41)
    await accountJobsAPI.reviewDuplicates([7, 8])
    await accountJobsAPI.mergeDuplicates({
      survivor_account_id: 7,
      loser_account_ids: [8],
      confirmation_hash: 'a'.repeat(64),
    })

    expect(post).toHaveBeenNthCalledWith(
      1,
      '/admin/account-jobs/41/retry-failed',
      undefined,
      { headers: { 'Idempotency-Key': expect.stringMatching(/^account_job_retry-/) } },
    )
    expect(post).toHaveBeenNthCalledWith(
      2,
      '/admin/accounts/duplicates/review',
      { account_ids: [7, 8] },
      { headers: { 'Idempotency-Key': expect.stringMatching(/^account_duplicate_review-/) } },
    )
    expect(post).toHaveBeenNthCalledWith(
      3,
      '/admin/accounts/duplicates/merge',
      {
        survivor_account_id: 7,
        loser_account_ids: [8],
        confirmation_hash: 'a'.repeat(64),
      },
      { headers: { 'Idempotency-Key': expect.stringMatching(/^account_duplicate_merge-/) } },
    )

    const keys = post.mock.calls.map((call) => call[2]?.headers?.['Idempotency-Key'])
    expect(new Set(keys).size).toBe(3)
  })
})
