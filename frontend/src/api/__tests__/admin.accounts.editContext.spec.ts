import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { CapturedAccountView } from '@/composables/useAccountViewContext'

const { get, request } = vi.hoisted(() => ({ get: vi.fn(), request: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get, request } }))
import { accountAPIForView, getEditContext } from '@/api/admin/accounts'
import { ACCOUNT_VIEW_HEADER, accountViewIdentityHeader } from '@/components/plugins/accountView'

function view(assertCurrent = vi.fn()): CapturedAccountView {
  return { actorID: 9, assertCurrent, context: { version: 1, plugin_id: 7, plugin_key: 'codexrip.cindy-provider', package_sha256: 'a'.repeat(64),
    view_id: 'cindy-accounts', preset_id: 'cindy', view_definition_digest: 'b'.repeat(64), query: { cindy_only: true } } }
}
describe('fixed account edit-context API', () => {
  beforeEach(() => { get.mockReset(); request.mockReset() })

  it('uses the fixed real-ID read and passes cancellation without a provider selector', async () => {
    const data = { schema_version: 1, kind: 'core', account_id: 41 }, signal = new AbortController().signal
    get.mockResolvedValue({ data })
    expect(await getEditContext(41, signal)).toEqual(data)
    expect(get).toHaveBeenCalledWith('/admin/accounts/41/edit-context', { signal })
    expect(request).not.toHaveBeenCalled()
  })

  it('captures the origin view on the fixed read through the existing native facade', async () => {
    const scope = view(), signal = new AbortController().signal
    request.mockResolvedValue({ data: { schema_version: 1, kind: 'core', account_id: 41 } })
    await accountAPIForView(scope).getEditContext(41, signal)
    const { query: _query, ...identity } = scope.context
    expect(request).toHaveBeenCalledWith(expect.objectContaining({ url: '/admin/accounts/41/edit-context', method: 'GET', signal,
      headers: { [ACCOUNT_VIEW_HEADER]: accountViewIdentityHeader(identity) } }))
    expect(get).not.toHaveBeenCalled()
  })

  it('never retries a failed or expired view read through an unbound core client', async () => {
    const denied = view(vi.fn(() => { throw new Error('view unavailable') }))
    await expect(accountAPIForView(denied).getEditContext(41)).rejects.toThrow('view unavailable')
    expect(request).not.toHaveBeenCalled()
    let current = true
    const scope = view(vi.fn(() => { if (!current) throw new Error('view expired') }))
    request.mockImplementation(async () => { current = false; return { data: { schema_version: 1, kind: 'core', account_id: 41 } } })
    await expect(accountAPIForView(scope).getEditContext(41)).rejects.toThrow('view expired')
    expect(request).toHaveBeenCalledTimes(1)
    expect(get).not.toHaveBeenCalled()
  })

  it('keeps the existing view context on a basic-only native update', async () => {
    const scope = view()
    request.mockResolvedValue({ data: { id: 41, notes: 'basic edit' } })
    await accountAPIForView(scope).update(41, { notes: 'basic edit' })
    expect(request).toHaveBeenCalledWith(expect.objectContaining({ url: '/admin/accounts/41', method: 'PUT',
      data: { notes: 'basic edit', view_context: scope.context } }))
  })
})
