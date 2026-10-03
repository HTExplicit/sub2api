import { webcrypto } from 'node:crypto'
import { AxiosHeaders, type InternalAxiosRequestConfig } from 'axios'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { stabilizeAccountOperationKey, settleAccountOperation, clearAccountOperationKeys } from '../accountOperationIdempotency'
const request = (key: string, ids = [1]) => ({ method: 'post', url: '/admin/accounts/batch-delete', data: { account_ids: ids }, headers: new AxiosHeaders({ 'Idempotency-Key': key }) }) as InternalAxiosRequestConfig
const fullPathRequest = (key: string) => ({
  method: 'post', url: '/api/v1/admin/accounts/batch-test', baseURL: '',
  data: { items: [{ account_id: 1, model_id: 'fixture-model' }], prompt: '' },
  headers: new AxiosHeaders({ 'Idempotency-Key': key }),
}) as InternalAxiosRequestConfig
beforeEach(() => { localStorage.setItem('auth_user', JSON.stringify({ id: 7 })) })
afterEach(() => { clearAccountOperationKeys(); localStorage.removeItem('auth_user'); vi.unstubAllGlobals() })
describe('uncertain account operation submission', () => {
  it('reuses a lost-response key for full-path account resources', async () => {
    vi.stubGlobal('crypto', webcrypto)
    const first = fullPathRequest('first-key')
    await stabilizeAccountOperationKey(first)
    const retry = fullPathRequest('new-key')
    await stabilizeAccountOperationKey(retry)

    expect(retry.headers.get('Idempotency-Key')).toBe('first-key')
  })
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

  it('partitions requests by path and destination', async () => {
    vi.stubGlobal('crypto', webcrypto)
    await stabilizeAccountOperationKey(fullPathRequest('original'))
    const mutations = [
      (value: InternalAxiosRequestConfig) => { value.url = '/admin/accounts/batch-test' },
      (value: InternalAxiosRequestConfig) => { value.baseURL = 'https://fixture.invalid' },
    ]
    for (const [index, mutate] of mutations.entries()) {
      const changed = fullPathRequest(`partition-${index}`)
      mutate(changed)
      await stabilizeAccountOperationKey(changed)
      expect(changed.headers.get('Idempotency-Key')).toBe(`partition-${index}`)
      const retry = fullPathRequest(`retry-${index}`)
      mutate(retry)
      await stabilizeAccountOperationKey(retry)
      expect(retry.headers.get('Idempotency-Key')).toBe(`partition-${index}`)
    }
    const originalRetry = fullPathRequest('original-retry')
    await stabilizeAccountOperationKey(originalRetry)
    expect(originalRetry.headers.get('Idempotency-Key')).toBe('original')
  })

  it('isolates administrators even before the store clears its session', async () => {
    vi.stubGlobal('crypto', webcrypto)
    await stabilizeAccountOperationKey(fullPathRequest('user-seven'))
    localStorage.setItem('auth_user', JSON.stringify({ id: 8 }))
    const first = fullPathRequest('user-eight')
    await stabilizeAccountOperationKey(first)
    expect(first.headers.get('Idempotency-Key')).toBe('user-eight')
    const retry = fullPathRequest('user-eight-retry')
    await stabilizeAccountOperationKey(retry)
    expect(retry.headers.get('Idempotency-Key')).toBe('user-eight')
  })

  it('does not cache retry identities without a valid persisted actor', async () => {
    vi.stubGlobal('crypto', webcrypto)
    await stabilizeAccountOperationKey(fullPathRequest('authenticated'))
    for (const raw of [null, '{invalid', JSON.stringify({ id: 0 }), JSON.stringify({ id: '7' }), JSON.stringify({ id: Number.MAX_SAFE_INTEGER + 1 })]) {
      if (raw === null) localStorage.removeItem('auth_user')
      else localStorage.setItem('auth_user', raw)
      const first = fullPathRequest('unidentified-first')
      const retry = fullPathRequest('unidentified-retry')
      await stabilizeAccountOperationKey(first)
      await stabilizeAccountOperationKey(retry)
      expect(first.headers.get('Idempotency-Key')).toBe('unidentified-first')
      expect(retry.headers.get('Idempotency-Key')).toBe('unidentified-retry')
      expect(retry).not.toHaveProperty('accountOperationSignature')
    }
  })

  it.each(['actor', 'epoch'])('rejects an %s change while the digest is pending', async (change) => {
    let completeDigest!: (value: ArrayBuffer) => void
    vi.stubGlobal('crypto', { subtle: { digest: () => new Promise<ArrayBuffer>(resolve => { completeDigest = resolve }) } })
    const first = fullPathRequest('pending')
    const pending = stabilizeAccountOperationKey(first)
    if (change === 'actor') localStorage.setItem('auth_user', JSON.stringify({ id: 8 }))
    else clearAccountOperationKeys()
    completeDigest(new ArrayBuffer(32))

    await expect(pending).rejects.toThrow('Account operation session changed')
    expect(first).not.toHaveProperty('accountOperationSignature')
    expect(first.headers.get('Idempotency-Key')).toBe('pending')
    vi.stubGlobal('crypto', webcrypto)
    const next = fullPathRequest('new-context')
    await stabilizeAccountOperationKey(next)
    expect(next.headers.get('Idempotency-Key')).toBe('new-context')
  })

  it('includes full-path account jobs but leaves other route families alone', async () => {
    vi.stubGlobal('crypto', webcrypto)
    const first = fullPathRequest('job-first'); first.url = '/api/v1/admin/account-jobs/19/retry'
    await stabilizeAccountOperationKey(first)
    const retry = fullPathRequest('job-retry'); retry.url = first.url
    await stabilizeAccountOperationKey(retry)
    expect(retry.headers.get('Idempotency-Key')).toBe('job-first')
    for (const url of ['/api/v1/admin/settings', '/api/v1/admin/accounts-other', '/api/v2/admin/accounts/batch-test']) {
      const unrelated = fullPathRequest('unrelated-first'); unrelated.url = url
      const again = fullPathRequest('unrelated-second'); again.url = url
      await stabilizeAccountOperationKey(unrelated)
      await stabilizeAccountOperationKey(again)
      expect(again.headers.get('Idempotency-Key')).toBe('unrelated-second')
      expect(again).not.toHaveProperty('accountOperationSignature')
    }
  })
})
