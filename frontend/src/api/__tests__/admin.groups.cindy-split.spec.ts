import { beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiClient: mocks,
}))

import {
  auditCindyGroups,
  getGroupApiKeys,
  previewCindyGroupSplit,
  splitCindyGroup,
} from '@/api/admin/groups'

describe('admin Cindy group split API', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('loads anonymous audit data and paginated source-group API Keys', async () => {
    const audit = {
      summary: { pure_cindy_groups: 1, mixed_groups: 1, no_cindy_groups: 2 },
      groups: [{
        group_id: 7,
        group_name: 'Mixed',
        status: 'active',
        classification: 'mixed',
        cindy_account_count: 2,
        ordinary_account_count: 3,
        api_key_count: 1,
      }],
    }
    const keys = { items: [{ id: 19, name: 'Client', key: 'sk-test' }], total: 1, page: 2, page_size: 50, pages: 2 }
    mocks.get.mockResolvedValueOnce({ data: audit }).mockResolvedValueOnce({ data: keys })

    await expect(auditCindyGroups()).resolves.toEqual(audit)
    await expect(getGroupApiKeys(7, 2, 50)).resolves.toEqual(keys)

    expect(mocks.get).toHaveBeenNthCalledWith(1, '/admin/cindy/groups/audit')
    expect(mocks.get).toHaveBeenNthCalledWith(2, '/admin/groups/7/api-keys', {
      params: { page: 2, page_size: 50 },
    })
  })

  it('previews and commits using the exact server fingerprint', async () => {
    const draft = {
      source_keeps: 'cindy' as const,
      target_name: 'Ordinary target',
      api_key_ids: [19],
    }
    const preview = {
      source_group_id: 7,
      source_group_name: 'Mixed',
      source_keeps: 'cindy' as const,
      target_name: 'Ordinary target',
      target_classification: 'no_cindy' as const,
      member_fingerprint: 'a'.repeat(64),
      cindy_account_count: 2,
      ordinary_account_count: 3,
      accounts_to_move: 3,
      source_api_key_count: 2,
      api_keys_to_rebind: 1,
      api_keys_remaining: 1,
    }
    const result = { ...preview, target_group_id: 12 }
    mocks.post.mockResolvedValueOnce({ data: preview }).mockResolvedValueOnce({ data: result })

    await expect(previewCindyGroupSplit(7, draft)).resolves.toEqual(preview)
    await expect(splitCindyGroup(7, {
      ...draft,
      member_fingerprint: preview.member_fingerprint,
    })).resolves.toEqual(result)

    expect(mocks.post).toHaveBeenNthCalledWith(1, '/admin/cindy/groups/7/split-preview', draft)
    expect(mocks.post).toHaveBeenNthCalledWith(2, '/admin/cindy/groups/7/split', {
      ...draft,
      member_fingerprint: preview.member_fingerprint,
    })
  })
})
