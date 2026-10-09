import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import CodexGatewayBorrowStatusView from '../CodexGatewayBorrowStatusView.vue'
import type { AccountListItem } from '@/types'
import type { BorrowVerification, CodexGatewayBorrowStatus } from '@/api/admin/codexGatewayBorrow'

const mocks = vi.hoisted(() => ({
  getConfig: vi.fn(), saveConfig: vi.fn(), getStatus: vi.fn(), prepare: vi.fn(), verify: vi.fn(),
  listTests: vi.fn(), getTest: vi.fn(), streamTests: vi.fn(), listAccounts: vi.fn(), getAvailableModels: vi.fn(), testAccount: vi.fn()
}))
vi.mock('@/api/admin/codexGatewayBorrow', async importOriginal => ({
  ...(await importOriginal<typeof import('@/api/admin/codexGatewayBorrow')>()), codexGatewayBorrowAPI: mocks
}))
vi.mock('@/api/admin/accounts', () => {
  const accountsAPI = { list: mocks.listAccounts, getAvailableModels: mocks.getAvailableModels, testAccount: mocks.testAccount }
  return { accountsAPI, default: accountsAPI }
})
vi.mock('vue-i18n', async importOriginal => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ locale: { value: 'zh' }, t: (key: string, values?: object) => values ? `${key} ${JSON.stringify(values)}` : key, te: () => true })
}))

const startTime = new Date('2026-10-08T06:00:00.000Z').getTime()
const wrappers: ReturnType<typeof mount>[] = []
const accounts = [
  { id: 1, name: 'Source', platform: 'openai', type: 'oauth', status: 'active', schedulable: true, proxy_id: 7, proxy: { id: 7, name: 'Bound exit' }, extra: {} },
  { id: 2, name: 'Target', platform: 'openai', type: 'setup-token', status: 'error', schedulable: false, proxy_id: null, parent_account_id: 1, extra: {}, error_message: 'Complete original account error' },
  { id: 3, name: 'Second target', platform: 'openai', type: 'oauth', status: 'active', schedulable: true, proxy_id: null, extra: {} },
  { id: 4, name: 'API Key', platform: 'openai', type: 'apikey', status: 'active', schedulable: true }
] as unknown as AccountListItem[]

function makeStatus(overrides: Partial<CodexGatewayBorrowStatus> = {}): CodexGatewayBorrowStatus {
  const expires = new Date(Date.now() + 60_000).toISOString()
  return {
    enabled: true, preparing: false, revision: 1, generated_at: new Date().toISOString(),
    config: { enabled: true, source_account_ids: [1], target_account_ids: [2, 3], models: ['gpt-6-astra', 'gpt-6.1-sol'] },
    candidate: { source_account_id: 1, expires_at: expires, remaining_seconds: 60, cookie_fingerprint: 'full-cookie-fingerprint' },
    sources: [{ account_id: 1, state: 'ready', reason: 'source_qualified', checked_at: new Date().toISOString(), expires_at: expires, remaining_seconds: 60 }],
    targets: [2, 3].flatMap(account_id => ['gpt-6-astra', 'gpt-6.1-sol'].map(model => ({
      account_id, model, state: account_id === 2 ? 'ready' : 'waiting', reason: account_id === 2 ? 'target_probe_passed' : 'not_verified',
      cache_valid: account_id === 2, checked_at: new Date().toISOString(), expires_at: account_id === 2 ? expires : undefined,
      remaining_seconds: account_id === 2 ? 60 : 0, mint_status: account_id === 2 ? 200 : 0, continue_status: account_id === 2 ? 200 : 0,
      minted: account_id === 2, new_ticket: false, reported_model: account_id === 2 ? model : undefined
    }))), ...overrides
  }
}
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => { resolve = resolvePromise; reject = rejectPromise })
  return { promise, resolve, reject }
}
async function mountView() {
  const wrapper = mount(CodexGatewayBorrowStatusView, { global: { stubs: {
    CodexLayout: { template: '<div><slot /></div>' }, CodexGatewayBorrowStatusView: true, CodexBorrowActivity: true, AppLayout: { template: '<div><slot /></div>' }, CodexBorrowNav: true
  } } })
  wrappers.push(wrapper)
  await flushPromises()
  return wrapper
}
function expectNoUnrelatedCalls() {
  for (const name of ['getConfig', 'saveConfig', 'listTests', 'getTest', 'streamTests', 'getAvailableModels', 'testAccount'] as const) expect(mocks[name]).not.toHaveBeenCalled()
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] })
  vi.setSystemTime(startTime)
  for (const mock of Object.values(mocks)) mock.mockReset()
  mocks.listAccounts.mockResolvedValue({ items: accounts, total: accounts.length })
  mocks.getStatus.mockImplementation(async () => makeStatus())
  mocks.prepare.mockImplementation(async () => makeStatus())
  mocks.verify.mockResolvedValue({ account_id: 2, model: 'gpt-6.1-sol', success: true, reason: 'target_probe_passed' })
})
afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount())
  vi.useRealTimers()
})

describe('CodexGatewayBorrowStatusView request and expiry boundaries', () => {
  it('mounts with saved status and local inventory only, and retains collapsed technical details in full', async () => {
    const fullError = `HTTP 403\n${'Complete synthetic upstream detail '.repeat(1800)}`
    const value = makeStatus()
    value.targets[3] = { ...value.targets[3], state: 'rejected', reason: 'unknown_reason_from_server', error: fullError, mint_status: 200, continue_status: 403, minted: true, new_ticket: true, reported_model: 'reported-model' }
    value.sources[0].error = 'Complete synthetic source error'
    mocks.getStatus.mockResolvedValue(value)
    const wrapper = await mountView()
    expect(mocks.getStatus).toHaveBeenCalledOnce()
    expect(mocks.listAccounts).toHaveBeenCalledWith(1, 100, { platform: 'openai', types: 'oauth,setup-token' }, expect.objectContaining({ signal: expect.any(AbortSignal) }))
    expect(wrapper.findAll('[data-test^="borrow-status-"]').filter(row => row.attributes('data-test') !== 'borrow-status-error')).toHaveLength(4)
    expect(wrapper.find('input').exists()).toBe(false)
    expect(wrapper.text()).toContain('Bound exit #7')
    expect(wrapper.get('[data-test="borrow-ready-count"]').text()).toContain('"ready":2,"total":4')
    const failed = wrapper.get('[data-test="borrow-status-3-gpt-6.1-sol"]')
    expect(failed.get('[data-test="borrow-line-reason"]').text()).toBe('unknown_reason_from_server')
    expect(failed.get('[data-test="borrow-target-error"]').text()).toBe(fullError.trim())
    expect(failed.get('[data-test="borrow-target-details"]').text()).toContain('"mint":200,"continuation":403')
    expect(wrapper.get('[data-test="borrow-candidate-details"]').text()).toContain('full-cookie-fingerprint')
    expect(wrapper.get('[data-test="borrow-source-details"]').text()).toContain('Complete synthetic source error')
    expect(wrapper.findAll('details').every(details => !(details.element as HTMLDetailsElement).open)).toBe(true)
    expect(mocks.prepare).not.toHaveBeenCalled()
    expect(mocks.verify).not.toHaveBeenCalled()
    expectNoUnrelatedCalls()
  })

  it('expires ready routes using the server-aligned local clock without polling or preparing', async () => {
    const value = makeStatus()
    value.generated_at = new Date(startTime + 120_000).toISOString()
    value.candidate!.expires_at = new Date(startTime + 123_000).toISOString()
    value.targets.filter(target => target.cache_valid).forEach(target => { target.expires_at = value.candidate!.expires_at })
    mocks.getStatus.mockResolvedValue(value)
    const wrapper = await mountView()
    expect(wrapper.get('[data-test="borrow-ready-count"]').text()).toContain('"ready":2')
    expect(wrapper.get('[data-test="borrow-candidate-remaining"]').text()).toContain('"seconds":3')
    await vi.advanceTimersByTimeAsync(3000)
    expect(wrapper.get('[data-test="borrow-ready-count"]').text()).toContain('"ready":0')
    expect(wrapper.get('[data-test="borrow-status-2-gpt-6-astra"] [data-test="borrow-line-state"]').text()).toContain('lineStates.expired')
    expect(wrapper.get('[data-test="borrow-status-2-gpt-6-astra"] [data-test="borrow-line-reason"]').text()).toContain('reasons.route_expired')
    expect(wrapper.get('[data-test="borrow-candidate"]').text()).toContain('candidateExpired')
    await vi.advanceTimersByTimeAsync(20_000)
    expect(mocks.getStatus).toHaveBeenCalledOnce()
    expect(mocks.prepare).not.toHaveBeenCalled()
    expect(mocks.verify).not.toHaveBeenCalled()
    expectNoUnrelatedCalls()
  })

  it('polls every 5 seconds while preparing, never overlaps a slow read, and stops when preparation ends', async () => {
    const slowRead = deferred<CodexGatewayBorrowStatus>()
    mocks.getStatus.mockResolvedValueOnce(makeStatus({ preparing: true })).mockImplementationOnce(() => slowRead.promise).mockImplementation(async () => makeStatus())
    const wrapper = await mountView()
    await vi.advanceTimersByTimeAsync(4999)
    expect(mocks.getStatus).toHaveBeenCalledOnce()
    await vi.advanceTimersByTimeAsync(1)
    expect(mocks.getStatus).toHaveBeenCalledTimes(2)
    expect(wrapper.get<HTMLButtonElement>('[data-test="borrow-refresh"]').element.disabled).toBe(true)
    await vi.advanceTimersByTimeAsync(15_000)
    expect(mocks.getStatus).toHaveBeenCalledTimes(2)
    slowRead.resolve(makeStatus({ preparing: true }))
    await flushPromises()
    await vi.advanceTimersByTimeAsync(5000)
    expect(mocks.getStatus).toHaveBeenCalledTimes(3)
    expect(wrapper.get('[data-test="borrow-preparation-status"]').text()).toContain('部分线路可用')
    await vi.advanceTimersByTimeAsync(20_000)
    expect(mocks.getStatus).toHaveBeenCalledTimes(3)
    expect(mocks.prepare).not.toHaveBeenCalled()
    expect(mocks.verify).not.toHaveBeenCalled()
    expectNoUnrelatedCalls()
  })

  it('prepares and verifies only explicit controls, polling while each local operation is pending', async () => {
    const preparation = deferred<CodexGatewayBorrowStatus>()
    const verification = deferred<BorrowVerification>()
    mocks.prepare.mockImplementationOnce(() => preparation.promise)
    mocks.verify.mockImplementationOnce(() => verification.promise)
    const wrapper = await mountView()
    await wrapper.get('[data-test="borrow-prepare"]').trigger('click')
    expect(mocks.prepare).toHaveBeenCalledOnce()
    expect(mocks.prepare).toHaveBeenCalledWith(expect.any(AbortSignal))
    await vi.advanceTimersByTimeAsync(5000)
    expect(mocks.getStatus).toHaveBeenCalledTimes(2)
    expect(wrapper.get<HTMLButtonElement>('[data-test="borrow-verify-2-gpt-6.1-sol"]').element.disabled).toBe(true)
    preparation.resolve(makeStatus())
    await flushPromises()
    expect(mocks.getStatus).toHaveBeenCalledTimes(3)
    await wrapper.get('[data-test="borrow-verify-3-gpt-6.1-sol"]').trigger('click')
    expect(mocks.verify).toHaveBeenCalledWith(3, 'gpt-6.1-sol', expect.any(AbortSignal))
    expect(wrapper.get('[data-test="borrow-status-3-gpt-6.1-sol"] [data-test="borrow-line-state"]').text()).toContain('lineStates.validating')
    await vi.advanceTimersByTimeAsync(5000)
    expect(mocks.getStatus).toHaveBeenCalledTimes(4)
    const fullError = `HTTP 429\n${'Untruncated synthetic verification failure '.repeat(1800)}`
    verification.resolve({ account_id: 3, model: 'gpt-6.1-sol', success: false, reason: 'target_probe_failed', error: fullError, mint_status: 429, continue_status: 0, minted: false, new_ticket: false, checked_at: new Date().toISOString() })
    await flushPromises()
    expect(wrapper.get('[data-test="borrow-action-error"]').text()).toBe(fullError.trim())
    expect(wrapper.get('[data-test="borrow-action-failure"] details').attributes('open')).toBeUndefined()
    expect(mocks.getStatus).toHaveBeenCalledTimes(5)
    await vi.advanceTimersByTimeAsync(20_000)
    expect(mocks.getStatus).toHaveBeenCalledTimes(5)
    expect(mocks.verify).toHaveBeenCalledOnce()
    expectNoUnrelatedCalls()
  })

  it('polls server validation started in another tab and stops when its terminal status arrives', async () => {
    const active = makeStatus()
    active.targets[3] = { ...active.targets[3], state: 'validating', reason: 'validating' }
    const terminal = makeStatus()
    terminal.targets[3] = { ...terminal.targets[3], state: 'ready', reason: 'target_probe_passed', cache_valid: true, expires_at: terminal.candidate!.expires_at }
    mocks.getStatus.mockResolvedValueOnce(active).mockResolvedValueOnce(terminal)
    const wrapper = await mountView()
    expect(wrapper.get('[data-test="borrow-preparation-status"]').text()).toBe('admin.codexGatewayBorrow.validating')
    expect(wrapper.get('[data-test="borrow-status-3-gpt-6.1-sol"] [data-test="borrow-line-state"]').text()).toContain('lineStates.validating')
    expect(wrapper.get<HTMLButtonElement>('[data-test="borrow-prepare"]').element.disabled).toBe(true)
    expect(wrapper.get<HTMLButtonElement>('[data-test="borrow-verify-2-gpt-6.1-sol"]').element.disabled).toBe(true)
    await vi.advanceTimersByTimeAsync(4999)
    expect(mocks.getStatus).toHaveBeenCalledOnce()
    await vi.advanceTimersByTimeAsync(1)
    expect(mocks.getStatus).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-test="borrow-preparation-status"]').text()).toContain('部分线路可用')
    expect(wrapper.get('[data-test="borrow-status-3-gpt-6.1-sol"] [data-test="borrow-line-state"]').text()).toContain('lineStates.ready')
    expect(wrapper.get<HTMLButtonElement>('[data-test="borrow-prepare"]').element.disabled).toBe(false)
    await vi.advanceTimersByTimeAsync(10_000)
    expect(mocks.getStatus).toHaveBeenCalledTimes(2)
    expect(mocks.prepare).not.toHaveBeenCalled()
    expect(mocks.verify).not.toHaveBeenCalled()
    expectNoUnrelatedCalls()
  })

  it('aborts a pending status read and all polling when leaving the page', async () => {
    const pending = deferred<CodexGatewayBorrowStatus>()
    mocks.getStatus.mockResolvedValueOnce(makeStatus({ preparing: true })).mockImplementationOnce(() => pending.promise)
    const wrapper = await mountView()
    await vi.advanceTimersByTimeAsync(5000)
    const signal = mocks.getStatus.mock.calls[1][0] as AbortSignal
    const inventorySignal = mocks.listAccounts.mock.calls[0][3].signal as AbortSignal
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
    expect(inventorySignal.aborted).toBe(true)
    pending.resolve(makeStatus({ preparing: true }))
    await flushPromises()
    await vi.advanceTimersByTimeAsync(20_000)
    expect(mocks.getStatus).toHaveBeenCalledTimes(2)
    expect(vi.getTimerCount()).toBe(0)
    expectNoUnrelatedCalls()
  })

  it.each(['verify', 'prepare-older-revision', 'prepare-older-time'] as const)('reads fresh terminal status after an older pending poll: %s', async operation => {
    const oldPoll = deferred<CodexGatewayBorrowStatus>()
    const freshRead = deferred<CodexGatewayBorrowStatus>()
    const preparation = deferred<CodexGatewayBorrowStatus>()
    const verification = deferred<BorrowVerification>()
    const oldStatus = makeStatus()
    mocks.getStatus.mockResolvedValueOnce(makeStatus()).mockImplementationOnce(() => oldPoll.promise).mockImplementationOnce(() => freshRead.promise)
    mocks.prepare.mockImplementationOnce(() => preparation.promise)
    mocks.verify.mockImplementationOnce(() => verification.promise)
    const wrapper = await mountView()
    await wrapper.get(operation === 'verify' ? '[data-test="borrow-verify-2-gpt-6.1-sol"]' : '[data-test="borrow-prepare"]').trigger('click')
    await vi.advanceTimersByTimeAsync(5000)
    expect(mocks.getStatus).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(1000)
    const terminal = makeStatus({ revision: operation === 'prepare-older-revision' ? 2 : 1 })
    terminal.targets[1] = { ...terminal.targets[1], cache_valid: false, state: 'rejected', reason: 'target_route_changed', error: 'Final synthetic verification error' }
    if (operation === 'verify') verification.resolve({ account_id: 2, model: 'gpt-6.1-sol', success: false, reason: 'target_route_changed', error: terminal.targets[1].error, mint_status: 200, continue_status: 200, minted: true, new_ticket: false, checked_at: new Date().toISOString() })
    else preparation.resolve(terminal)
    await flushPromises()
    expect(mocks.getStatus).toHaveBeenCalledTimes(2)
    expect(wrapper.get<HTMLButtonElement>('[data-test="borrow-prepare"]').element.disabled).toBe(true)
    oldPoll.resolve(oldStatus)
    await flushPromises()
    expect(mocks.getStatus).toHaveBeenCalledTimes(3)
    if (operation !== 'verify') {
      // The older poll cannot overwrite the newer prepare POST result.
      expect(wrapper.get('[data-test="borrow-ready-count"]').text()).toContain('"ready":1')
      expect(wrapper.get('[data-test="borrow-status-2-gpt-6.1-sol"] [data-test="borrow-line-state"]').text()).not.toContain('lineStates.ready')
      expect(wrapper.get('[data-test="borrow-status-2-gpt-6.1-sol"] [data-test="borrow-target-error"]').text()).toBe('Final synthetic verification error')
    }
    freshRead.resolve(terminal)
    await flushPromises()
    expect(wrapper.get('[data-test="borrow-status-2-gpt-6.1-sol"] [data-test="borrow-line-state"]').text()).toContain('lineStates.rejected')
    expect(wrapper.get('[data-test="borrow-ready-count"]').text()).toContain('"ready":1')
    await vi.advanceTimersByTimeAsync(10_000)
    expect(mocks.getStatus).toHaveBeenCalledTimes(3)
    expectNoUnrelatedCalls()
  })

  it('uses only the saved combinations and never shows cached pairs as usable while disabled', async () => {
    const value = makeStatus({ enabled: false })
    value.config = { ...value.config, enabled: false, target_account_ids: [2, 3], models: ['gpt-6.1-sol'] }
    value.targets = [value.targets[1]]
    mocks.getStatus.mockResolvedValue(value)
    const wrapper = await mountView()
    expect(wrapper.get('[data-test="borrow-ready-count"]').text()).toContain('"ready":0,"total":2')
    expect(wrapper.find('[data-test="borrow-status-2-gpt-6-astra"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="borrow-status-3-gpt-6.1-sol"] [data-test="borrow-line-state"]').text()).toContain('lineStates.waiting')
    expect(wrapper.get('[data-test="borrow-status-2-gpt-6.1-sol"] [data-test="borrow-line-reason"]').text()).toContain('reasons.disabled')
    expect(wrapper.get<HTMLButtonElement>('[data-test="borrow-prepare"]').element.disabled).toBe(true)
    expect(wrapper.get<HTMLButtonElement>('[data-test="borrow-verify-2-gpt-6.1-sol"]').element.disabled).toBe(true)
    expectNoUnrelatedCalls()
  })
})
