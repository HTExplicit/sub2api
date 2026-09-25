import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import CodexRuntimeSettings from '../CodexRuntimeSettings.vue'
import CodexTicketOperationModal from '../CodexTicketOperationModal.vue'
import CodexContinuationDiagnostics from '../CodexContinuationDiagnostics.vue'
import AccountOperationDialog from '@/components/admin/account-jobs/AccountOperationDialog.vue'
import type { CodexTicketOperation } from '@/utils/codexTickets'

const { get, post, put, listAccounts, track, closeDrawer, stepUpRun } = vi.hoisted(() => ({
  get: vi.fn(), post: vi.fn(), put: vi.fn(), listAccounts: vi.fn(), track: vi.fn(), closeDrawer: vi.fn(),
  stepUpRun: vi.fn(async (action: () => Promise<unknown>) => action())
}))
vi.mock('@/api/client', () => ({ apiClient: { get, post, put } }))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { list: listAccounts } } }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { id: 1 } }) }))
vi.mock('@/stores/accountJobs', () => ({ useAccountJobsStore: () => ({ track, closeDrawer, currentJob: null, embeddedOpen: false, loadCurrent: vi.fn().mockResolvedValue(undefined) }) }))
vi.mock('@/composables/useStepUp', () => ({ useStepUp: () => ({ run: stepUpRun }), isStepUpCancelled: () => false }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ locale: { value: 'en' }, t: (key: string) => key })
}))

const wrappers: VueWrapper[] = []
const global = { stubs: {
  BaseDialog: { props: ['show'], template: '<div v-if="show"><slot/><slot name="footer"/></div>' },
  AccountOperationProgress: { template: '<div data-test="durable-progress" />' },
  TotpStepUpDialog: true
} }
const account = (id: number, parent: number | null = null) => ({ id, platform: 'openai', type: 'oauth', parent_account_id: parent })
function accepted(kind = 'codex_ticket_harvest') { return { id: 31, kind, status: 'pending' } }
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(done => { resolve = done })
  return { promise, resolve }
}
function operation(mode: CodexTicketOperation, ids = [7, 11]) {
  const wrapper = mount(CodexTicketOperationModal, { props: { show: true, operation: mode, accountIds: ids }, global })
  wrappers.push(wrapper)
  return wrapper
}

beforeEach(() => {
  vi.clearAllMocks()
  get.mockReset().mockImplementation(async (url: string) => ({ data: url.endsWith('/policy') ? { enabled: true, models: ['model-a', 'model-b'] } : {} }))
  post.mockReset().mockImplementation(async (url: string) => ({ data: accepted(url.includes('stop') ? 'codex_ticket_stop' : 'codex_ticket_harvest') }))
  put.mockReset().mockImplementation(async (_url: string, value: unknown) => ({ data: value }))
  listAccounts.mockReset().mockImplementation(async (_page: number, _size: number, filters: { account_ids: string }) => {
    const ids = filters.account_ids.split(',').map(Number)
    return { items: ids.map(id => account(id)), total: ids.length }
  })
})
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()) })

describe('native Codex controls', () => {
  it('retains raw configuration fields and forwards the original proxy input and protocol through step-up', async () => {
    const config = { enabled: true, request_zstd: true, proxy_url: 'host:1080:user:fixture', proxy_protocol: 'socks5h', models: ['model-a'], fail_closed: true, code: 42, data: { retained: true } }
    get.mockResolvedValue({ data: config })
    post.mockImplementation(async (url: string) => ({ data: url.endsWith('/proxy-parse')
      ? { candidates: [{ selection_id: 'selected', protocol: 'socks5h', host: 'host', port: 1080, source_line: 1, source_column: 1, format: 'four fields' }], issues: [], selection_id: 'selected', selection_required: false }
      : { network_reachable: true, code: 'target_http_status', http_status: 407, stages: [] } }))
    const wrapper = mount(CodexRuntimeSettings, { global }); wrappers.push(wrapper)
    await flushPromises()
    expect(get).toHaveBeenCalledWith('/admin/settings/codex-runtime', { rawPluginConfig: true })
    await wrapper.get('[data-test="codex-test-proxy"]').trigger('click')
    await flushPromises()
    expect(post).toHaveBeenCalledWith('/admin/settings/openai-codex-ticket/proxy-parse', { proxy_url: config.proxy_url, protocol: 'socks5h' })
    expect(post).toHaveBeenCalledWith('/admin/settings/openai-codex-ticket/proxy-test', { proxy_url: config.proxy_url, protocol: 'socks5h', proxy_selection_id: 'selected' }, { timeout: 30000 })
    await wrapper.get('[data-test="codex-compression"]').setValue(false)
    await wrapper.get('[data-test="codex-save"]').trigger('click')
    await flushPromises()
    expect(put).toHaveBeenLastCalledWith('/admin/settings/codex-runtime', { ...config, request_zstd: false, proxy_selection_id: 'selected' }, { rawPluginConfig: true })
    await wrapper.get('[data-test="codex-clear-proxy"]').trigger('click')
    await flushPromises()
    expect(put).toHaveBeenLastCalledWith('/admin/settings/codex-runtime', { ...config, enabled: false, request_zstd: false, proxy_url: '', proxy_selection_id: undefined }, { rawPluginConfig: true })
    expect(stepUpRun).toHaveBeenCalledTimes(3)
  })

  it.each([
    ['harvest', [7], '/admin/accounts/7/codex-tickets/harvest'],
    ['harvest', [7, 11], '/admin/accounts/codex-tickets/batch-harvest'],
    ['stop', [7], '/admin/accounts/7/codex-tickets/stop-job'],
    ['stop', [7, 11], '/admin/accounts/codex-tickets/batch-stop']
  ] as const)('submits %s for frozen IDs %j to the durable job endpoint', async (mode, ids, path) => {
    const wrapper = operation(mode, [...ids])
    await flushPromises()
    await wrapper.setProps({ accountIds: [99] })
    await wrapper.get('[data-model="model-b"]').setValue(false)
    await wrapper.get('[data-test="codex-submit"]').trigger('click')
    await flushPromises()
    expect(listAccounts).toHaveBeenCalledWith(1, 100, { account_ids: ids.join(','), lite: '1', include_scheduler_score: '0' })
    const body = { account_ids: [...ids], models: ['model-a'], ...(mode === 'harvest' ? { force: false } : {}) }
    expect(post).toHaveBeenCalledTimes(1)
    expect(post).toHaveBeenCalledWith(path, body, { headers: { 'Idempotency-Key': expect.any(String) } })
    expect(wrapper.getComponent(AccountOperationDialog).props('job')).toMatchObject({ id: 31, status: 'pending', kind: mode === 'harvest' ? 'codex_ticket_harvest' : 'codex_ticket_stop' })
    expect(track).toHaveBeenCalledWith(expect.objectContaining({ id: 31 }), expect.objectContaining({ embedded: true }))
  })

  it('rejects the entire selection when a fresh identity becomes ineligible', async () => {
    listAccounts.mockResolvedValue({ items: [account(7), account(11, 7)], total: 2 })
    const wrapper = operation('harvest')
    await flushPromises()
    await wrapper.get('[data-test="codex-submit"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('every account must be eligible')
    expect(post).not.toHaveBeenCalled()
  })

  it('tracks an accepted job after the window closes without canceling it', async () => {
    const pending = deferred<{ data: ReturnType<typeof accepted> }>()
    post.mockReturnValue(pending.promise)
    const wrapper = operation('stop', [7])
    await flushPromises()
    await wrapper.get('[data-test="codex-submit"]').trigger('click')
    await flushPromises()
    expect(post).toHaveBeenCalledTimes(1)
    await wrapper.setProps({ show: false })
    pending.resolve({ data: accepted('codex_ticket_stop') })
    await flushPromises()
    expect(track).toHaveBeenCalledWith(expect.objectContaining({ id: 31 }), { open: false })
    expect(post).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('close')).toBeUndefined()
  })

  it('uses native diagnostics and ignores the result for an older error ID', async () => {
    const earlier = deferred<{ data: unknown }>()
    get.mockReturnValueOnce(earlier.promise).mockResolvedValueOnce({ data: [{ account_id: 11, attempt: 1, diagnostic: { recovery: { not_attempted_reason: 'source_changed' } } }] })
    const wrapper = mount(CodexContinuationDiagnostics, { props: { errorId: 41 } }); wrappers.push(wrapper)
    await wrapper.setProps({ errorId: 42 })
    await flushPromises()
    earlier.resolve({ data: [{ account_id: 7, attempt: 1, diagnostic: { classification: 'previous_response_not_found' } }] })
    await flushPromises()
    expect(get).toHaveBeenLastCalledWith('/admin/ops/errors/42/diagnostics', { signal: expect.any(AbortSignal) })
    expect(wrapper.text()).toContain('The source changed')
    expect(wrapper.text()).not.toContain('Account 7')
    expect(wrapper.text()).not.toContain('The referenced response was not found')
  })
})
