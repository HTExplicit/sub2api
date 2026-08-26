import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))

vi.mock('@/api/client', () => ({ apiClient: { get, post } }))

import accountsAPI from '@/api/admin/accounts'

describe('admin accounts job submissions', () => {
  beforeEach(() => {
    get.mockReset().mockResolvedValue({ data: { count: 1, fingerprint: 'f'.repeat(64) } })
    post.mockReset().mockResolvedValue({ data: { id: 71, status: 'pending' } })
  })

  it('submits every approved import and bulk operation as an idempotent job', async () => {
    const account = { name: 'Cindy A', platform: 'cindy', type: 'apikey' } as never
    const data = { type: 'sub2api-data', version: 2, accounts: [], proxies: [] } as never

    await accountsAPI.batchCreate([account])
    await accountsAPI.batchUpdateCredentials({ account_ids: [1], field: 'api_key', value: 'test-value' })
    await accountsAPI.bulkUpdate([1], { status: 'inactive' })
    await accountsAPI.importData({ data, uniform_settings: { concurrency: 2 } })
    await accountsAPI.previewImportData({ data, target_group_id: 12 })
    await accountsAPI.bulkUpdateTaxonomy({ account_ids: [1], tag_add_ids: [9] })
    await accountsAPI.importCodexSession({ content: '{"access_token":"test"}' })
    await accountsAPI.batchDelete([1])
    await accountsAPI.batchClearError([1])
    await accountsAPI.batchRefresh([1])
    await accountsAPI.batchRefreshTier([1])
    await accountsAPI.deleteCindyInsufficient({ count: 1, fingerprint: 'f'.repeat(64) } as never)
    await accountsAPI.previewCindyBannedDeletion()
    await accountsAPI.deleteCindyBanned({ count: 1, fingerprint: 'f'.repeat(64) } as never)

    expect(post.mock.calls.map((call) => call[0])).toEqual([
      '/admin/accounts/batch',
      '/admin/accounts/batch-update-credentials',
      '/admin/accounts/bulk-update',
      '/admin/accounts/data',
      '/admin/accounts/data/preview',
      '/admin/accounts/bulk-taxonomy',
      '/admin/accounts/import/codex-session',
      '/admin/accounts/batch-delete',
      '/admin/accounts/batch-clear-error',
      '/admin/accounts/batch-refresh',
      '/admin/accounts/batch-refresh-tier',
      '/admin/accounts/cindy/delete-insufficient',
      '/admin/accounts/cindy/delete-banned',
    ])
    expect(post.mock.calls[3][1]).toEqual({
      data,
      skip_default_group_bind: undefined,
      uniform_settings: { concurrency: 2 },
    })
    expect(post.mock.calls[3][1]).not.toHaveProperty('target_group_id')
    const keys = post.mock.calls.map((call) => call[2]?.headers?.['Idempotency-Key']).filter((key): key is string => typeof key === 'string')
    expect(keys).toHaveLength(12)
    expect(keys.every((key) => typeof key === 'string' && key.length > 20)).toBe(true)
    expect(new Set(keys).size).toBe(12)
  })
})
