import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { reactive } from 'vue'
import CodexRuntimeSettings from '../CodexRuntimeSettings.vue'
import type { ProxyParseResult, ProxyTestResult } from '@/api/admin/codexTickets'

const { getConfig, saveConfig, parseProxy, testProxy, stepUpRun } = vi.hoisted(() => ({
  getConfig: vi.fn(), saveConfig: vi.fn(), parseProxy: vi.fn(), testProxy: vi.fn(),
  stepUpRun: vi.fn(async (action: () => Promise<unknown>) => action())
}))
const auth = reactive<{ user: { id: number } | null }>({ user: { id: 1 } })
vi.mock('@/api/admin/codexRuntime', () => ({ codexRuntimeAPI: { getConfig, saveConfig } }))
vi.mock('@/api/admin/codexTickets', () => ({ codexTicketsAPI: { parseProxy, testProxy } }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => auth }))
vi.mock('@/composables/useStepUp', () => ({ useStepUp: () => ({ run: stepUpRun }), isStepUpCancelled: () => false }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ locale: { value: 'en' }, t: (key: string) => key })
}))

const wrappers: VueWrapper[] = []
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(done => { resolve = done })
  return { promise, resolve }
}
function result(ids = ['one']): ProxyParseResult {
  return { candidates: ids.map((id, index) => ({ selection_id: id, protocol: 'http', host: 'proxy.example', port: 8080, username_masked: '***', source_line: index + 1, source_column: 1, format: 'uri' })), issues: [], selection_id: ids.length === 1 ? ids[0] : undefined, selection_required: ids.length > 1 }
}
const reached: ProxyTestResult = { success: true, network_reachable: true, protocol: 'http', code: 'proxy_reachable', http_status: 200, stages: [], message: 'ok' }
async function settings() {
  const wrapper = mount(CodexRuntimeSettings, { global: { stubs: { TotpStepUpDialog: true } } })
  wrappers.push(wrapper)
  await flushPromises()
  return wrapper
}
beforeEach(() => {
  vi.clearAllMocks()
  auth.user = { id: 1 }
  getConfig.mockReset().mockResolvedValue({ enabled: true, proxy_url: 'http://user:password@proxy.example:8080', request_zstd: true, models: ['retained'] })
  saveConfig.mockReset().mockImplementation(async config => ({ ...config, proxy_selection_id: undefined }))
  parseProxy.mockReset().mockResolvedValue(result())
  testProxy.mockReset().mockResolvedValue(reached)
})
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()) })

describe('292 proxy draft and candidate isolation', () => {
  it('requires one candidate before save or test and selecting alone has no side effects', async () => {
    parseProxy.mockResolvedValue(result(['one', 'two']))
    const wrapper = await settings()
    await wrapper.get('[data-test="codex-save"]').trigger('click')
    await flushPromises()
    expect(saveConfig).not.toHaveBeenCalled()
    expect(testProxy).not.toHaveBeenCalled()
    expect(stepUpRun).not.toHaveBeenCalled()
    expect(wrapper.get('[data-test="codex-proxy-candidates"]').text()).not.toContain('password')
    await wrapper.get('input[value="two"]').setValue(true)
    await flushPromises()
    expect(saveConfig).not.toHaveBeenCalled()
    expect(testProxy).not.toHaveBeenCalled()
    await wrapper.get('[data-test="codex-test-proxy"]').trigger('click')
    await flushPromises()
    expect(testProxy).toHaveBeenCalledWith('http://user:password@proxy.example:8080', 'http', 'two')
    await wrapper.get('[data-test="codex-save"]').trigger('click')
    await flushPromises()
    expect(saveConfig).toHaveBeenCalledWith(expect.objectContaining({ models: ['retained'], proxy_selection_id: 'two' }))
  })

  it('invalidates candidates, selection and test results immediately when raw input or protocol changes', async () => {
    const wrapper = await settings()
    await wrapper.get('[data-test="codex-test-proxy"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Proxy connected')
    await wrapper.get('[data-test="codex-protocol"]').setValue('socks5')
    expect(wrapper.find('[data-test="codex-proxy-candidates"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('Proxy connected')
    await wrapper.get('[data-test="codex-parse-proxy"]').trigger('click')
    await flushPromises()
    expect(parseProxy).toHaveBeenLastCalledWith('http://user:password@proxy.example:8080', 'socks5')
    await wrapper.get('[data-test="codex-proxy"]').setValue('edited.example:8080')
    expect(wrapper.find('[data-test="codex-proxy-candidates"]').exists()).toBe(false)
  })

  it('keeps invalid input and field issues available for correction', async () => {
    parseProxy.mockResolvedValue({ candidates: [], issues: [{ line: 2, column: 7, field: 'port', code: 'invalid_port', message: 'Proxy port must be between 1 and 65535' }], selection_required: false })
    const wrapper = await settings()
    await wrapper.get('[data-test="codex-proxy"]').setValue('bad input')
    await wrapper.get('[data-test="codex-test-proxy"]').trigger('click')
    await flushPromises()
    expect((wrapper.get('[data-test="codex-proxy"]').element as HTMLTextAreaElement).value).toBe('bad input')
    expect(wrapper.get('[data-test="codex-proxy-issues"]').text()).toContain('Line 2, column 7')
    expect(testProxy).not.toHaveBeenCalled()
    await wrapper.get('[data-test="codex-save"]').trigger('click')
    await flushPromises()
    expect(saveConfig).not.toHaveBeenCalled()
  })

  it('ignores parse and test replies from older drafts', async () => {
    const pendingParse = deferred<ProxyParseResult>()
    parseProxy.mockReturnValueOnce(pendingParse.promise)
    const wrapper = await settings()
    await wrapper.get('[data-test="codex-parse-proxy"]').trigger('click')
    await wrapper.get('[data-test="codex-proxy"]').setValue('new.example:8080')
    pendingParse.resolve(result(['old']))
    await flushPromises()
    expect(wrapper.find('[data-test="codex-proxy-candidates"]').exists()).toBe(false)
    const pendingTest = deferred<ProxyTestResult>()
    testProxy.mockReturnValueOnce(pendingTest.promise)
    await wrapper.get('[data-test="codex-test-proxy"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="codex-proxy"]').setValue('latest.example:8080')
    pendingTest.resolve(reached)
    await flushPromises()
    expect(wrapper.text()).not.toContain('Proxy connected')
    expect((wrapper.get('[data-test="codex-proxy"]').element as HTMLTextAreaElement).value).toBe('latest.example:8080')
  })

  it('does not overwrite a newer draft when a save finishes', async () => {
    const pendingSave = deferred<{ proxy_url: string }>()
    saveConfig.mockReturnValueOnce(pendingSave.promise)
    const wrapper = await settings()
    await wrapper.get('[data-test="codex-save"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="codex-proxy"]').setValue('newer.example:8080')
    pendingSave.resolve({ proxy_url: 'http://saved.example:8080' })
    await flushPromises()
    expect((wrapper.get('[data-test="codex-proxy"]').element as HTMLTextAreaElement).value).toBe('newer.example:8080')
    expect(wrapper.text()).not.toContain('Settings saved')
  })

  it('rejects replies and pending step-up actions after an administrator session changes', async () => {
    const pendingTest = deferred<ProxyTestResult>()
    testProxy.mockReturnValueOnce(pendingTest.promise)
    const wrapper = await settings()
    await wrapper.get('[data-test="codex-test-proxy"]').trigger('click')
    await flushPromises()
    getConfig.mockResolvedValue({ proxy_url: 'http://next.example:8080' })
    auth.user = { id: 2 }
    await flushPromises()
    pendingTest.resolve(reached)
    await flushPromises()
    expect(wrapper.text()).not.toContain('Proxy connected')
    expect((wrapper.get('[data-test="codex-proxy"]').element as HTMLTextAreaElement).value).toBe('http://next.example:8080')
    let pendingAction: (() => Promise<unknown>) | undefined
    stepUpRun.mockImplementationOnce(async action => { pendingAction = action; return undefined })
    await wrapper.get('[data-test="codex-test-proxy"]').trigger('click')
    await flushPromises()
    auth.user = null
    auth.user = { id: 2 }
    await flushPromises()
    expect(() => pendingAction!()).toThrow('common.operationFailed')
    expect(testProxy).toHaveBeenCalledTimes(1)
  })

  it('shows the original failure, every stage, certificate facts, the username and runtime settings', async () => {
    parseProxy.mockResolvedValue({ candidates: [{ selection_id: 'one', protocol: 'http', host: 'proxy.example', port: 8080, username: 'sticky-session-7', username_masked: '***', source_line: 1, source_column: 1, format: 'uri' }], issues: [], selection_id: 'one', selection_required: false })
    testProxy.mockResolvedValue({ success: false, network_reachable: false, protocol: 'http', code: 'ticket_proxy_auth', message: 'proxy auth failed', failure_detail: 'Head "https://chatgpt.com/backend-api/codex/responses": Proxy Authentication Required',
      stages: [{ name: 'tcp', success: true, duration_ms: 12, message: 'tcp 192.0.2.1:8080' }, { name: 'connect', success: false, duration_ms: 20, message: 'HTTP 407 Proxy Authentication Required' }],
      certificate_error: 'x509: certificate signed by unknown authority',
      certificates: [{ server_name: 'chatgpt.com', subject: 'CN=chatgpt.com', issuer: 'CN=Inspecting Proxy CA', not_before: '2026-01-01T00:00:00Z', not_after: '2027-01-01T00:00:00Z', dns_names: ['chatgpt.com'], sha256: 'a'.repeat(64), spki_sha256: 'b'.repeat(64) }] })
    const wrapper = await settings()
    expect(wrapper.get('[data-test="codex-runtime-facts"]').text()).toContain('retained')
    await wrapper.get('[data-test="codex-parse-proxy"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="codex-proxy-candidates"]').text()).toContain('sticky-session-7')
    await wrapper.get('[data-test="codex-test-proxy"]').trigger('click')
    await flushPromises()
    const status = wrapper.get('[role="status"]').text()
    for (const value of ['ticket_proxy_auth', 'proxy auth failed', 'Proxy Authentication Required', 'HTTP 407', 'tcp 192.0.2.1:8080', 'CN=Inspecting Proxy CA', 'x509: certificate signed by unknown authority', 'b'.repeat(64)]) expect(status).toContain(value)
  })
})
