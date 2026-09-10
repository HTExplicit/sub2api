import { beforeEach, describe, expect, it, vi } from 'vitest'
const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get, post } }))
import api from '../accountCapabilities'

describe('accountCapabilitiesAPI', () => {
  beforeEach(() => { vi.clearAllMocks(); get.mockResolvedValue({ data: { items: [], total: 0 } }); post.mockResolvedValue({ data: { id: 1 } }) })

  it('uses admin-only read endpoints and retains scope filters and abort signals', async () => {
    const signal = new AbortController().signal
    const params = { folder_ids: '17,28', page: 2, page_size: 20 }
    await api.candidates(params, signal)
    await api.inventory({ ...params, kind: 'discover' }, signal)
    await api.listRuns(params, signal)
    await api.getRun(3, signal)
    await api.listItems(3, { status: 'indeterminate' }, signal)
    expect(get).toHaveBeenNthCalledWith(1, '/admin/account-capabilities/candidates', { params, signal })
    expect(get).toHaveBeenNthCalledWith(2, '/admin/account-capabilities', { params: { ...params, kind: 'discover' }, signal })
    expect(get).toHaveBeenNthCalledWith(3, '/admin/account-capabilities/runs', { params, signal })
    expect(get).toHaveBeenNthCalledWith(4, '/admin/account-capabilities/runs/3', { signal })
    expect(get).toHaveBeenNthCalledWith(5, '/admin/account-capabilities/runs/3/items', { params: { status: 'indeterminate' }, signal })
  })

  it('reads the complete overview and requests a server recommendation without starting a check', async () => {
    const signal = new AbortController().signal
    const params = { folder_ids: '17,28', account_ids: '71', group_ids: '4' }
    const request = { scope: { folder_ids: [17, 28], account_ids: [71] }, group_ids: [4], mainstream_only: true }
    await api.overview(params, signal)
    await api.plan(request, signal)
    expect(get).toHaveBeenCalledWith('/admin/account-capabilities/overview', { params, signal })
    expect(post).toHaveBeenCalledTimes(1)
    expect(post).toHaveBeenCalledWith('/admin/account-capabilities/plan', request, { signal })
  })

  it('leaves run idempotency owned by the caller and routes all controls to the server', async () => {
    const request = { kind: 'discover' as const, folder_ids: [17, 28], account_ids: [] }
    await api.createRun(request, 'same-operation-key')
    await api.createRun(request, 'same-operation-key')
    for (const action of ['pause', 'resume', 'cancel'] as const) await api.controlRun(3, action)
    expect(post).toHaveBeenNthCalledWith(1, '/admin/account-capabilities/runs', request, { headers: { 'Idempotency-Key': 'same-operation-key' } })
    expect(post.mock.calls[1]).toEqual(post.mock.calls[0])
    expect(post).toHaveBeenNthCalledWith(3, '/admin/account-capabilities/runs/3/pause')
    expect(post).toHaveBeenNthCalledWith(4, '/admin/account-capabilities/runs/3/resume')
    expect(post).toHaveBeenNthCalledWith(5, '/admin/account-capabilities/runs/3/cancel')
  })

  it('resolves an uncertain creation receipt with a read and keeps the original key out of URLs', async () => {
    const signal = new AbortController().signal
    await api.getRunReceipt('original-submission-key', signal)
    expect(get).toHaveBeenCalledWith('/admin/account-capabilities/runs/receipt', {
      headers: { 'Idempotency-Key': 'original-submission-key' }, signal,
    })
    expect(post).not.toHaveBeenCalled()
  })

  it('previews and applies a saved changeset without mutating groups through another API', async () => {
    const request = { idempotency_key: 'preview-one', scope: { folder_ids: [17], account_ids: [] }, groups: [], scheduling_evidence_ids: [42] }
    await api.preview(request)
    await api.getChangeset(5)
    await api.apply(5)
    await api.apply(5)
    expect(post).toHaveBeenNthCalledWith(1, '/admin/account-capabilities/changesets/preview', request)
    expect(get).toHaveBeenCalledWith('/admin/account-capabilities/changesets/5')
    expect(post).toHaveBeenNthCalledWith(2, '/admin/account-capabilities/changesets/5/apply', {})
    expect(post.mock.calls[2]).toEqual(post.mock.calls[1])
  })
})
