import { webcrypto } from 'node:crypto'
import { AxiosHeaders, type InternalAxiosRequestConfig } from 'axios'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { stabilizeAccountOperationKey, settleAccountOperation, clearAccountOperationKeys } from '../accountOperationIdempotency'
const request = (key: string, ids = [1]) => ({ method: 'post', url: '/admin/accounts/batch-delete', data: { account_ids: ids }, headers: new AxiosHeaders({ 'Idempotency-Key': key }) }) as InternalAxiosRequestConfig
afterEach(() => { clearAccountOperationKeys(); vi.unstubAllGlobals() })
describe('uncertain account operation submission', () => {
  it('reuses the key after a lost response, isolates changed targets and releases confirmed submissions', async () => {
    vi.stubGlobal('crypto', webcrypto)
    const first = request('first'); await stabilizeAccountOperationKey(first)
    const retry = request('new-key'); await stabilizeAccountOperationKey(retry)
    expect(retry.headers.get('Idempotency-Key')).toBe('first')
    const changed = request('changed', [2]); await stabilizeAccountOperationKey(changed)
    expect(changed.headers.get('Idempotency-Key')).toBe('changed')
    settleAccountOperation(retry)
    const next = request('next'); await stabilizeAccountOperationKey(next)
    expect(next.headers.get('Idempotency-Key')).toBe('next')
  })
  it('does not retain another administrator session or affect non-operation requests', async () => {
    vi.stubGlobal('crypto', webcrypto)
    await stabilizeAccountOperationKey(request('old'))
    clearAccountOperationKeys()
    const next = request('next'); await stabilizeAccountOperationKey(next)
    expect(next.headers.get('Idempotency-Key')).toBe('next')
    const unrelated = request('unrelated'); unrelated.url = '/admin/settings'
    await stabilizeAccountOperationKey(unrelated)
    expect(unrelated.headers.get('Idempotency-Key')).toBe('unrelated')
  })
})
