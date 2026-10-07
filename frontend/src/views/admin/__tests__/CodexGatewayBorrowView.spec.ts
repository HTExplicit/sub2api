import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import CodexGatewayBorrowView from '../CodexGatewayBorrowView.vue'
import type { AccountListItem } from '@/types'
import type { BorrowTestEvent, BorrowTestResult, BorrowTestTask, CodexGatewayBorrowConfig, CodexGatewayBorrowStatus } from '@/api/admin/codexGatewayBorrow'

const mocks = vi.hoisted(() => ({
  getConfig: vi.fn(), saveConfig: vi.fn(), getStatus: vi.fn(), prepare: vi.fn(), verify: vi.fn(), listTests: vi.fn(), getTest: vi.fn(), streamTests: vi.fn(),
  listAccounts: vi.fn(), getAvailableModels: vi.fn(), testAccount: vi.fn(), showSuccess: vi.fn()
}))
vi.mock('@/api/admin/codexGatewayBorrow', async importOriginal => ({
  ...(await importOriginal<typeof import('@/api/admin/codexGatewayBorrow')>()),
  codexGatewayBorrowAPI: mocks
}))
vi.mock('@/api/admin/accounts', () => {
  const accountsAPI = { list: mocks.listAccounts, getAvailableModels: mocks.getAvailableModels, testAccount: mocks.testAccount }
  return { accountsAPI, default: accountsAPI }
})
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: mocks.showSuccess }) }))
vi.mock('vue-i18n', async importOriginal => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key, te: () => true })
}))

const wrappers: ReturnType<typeof mount>[] = []
const now = new Date().toISOString()
const expires = new Date(Date.now() + 60 * 60 * 1000).toISOString()
const disabledConfig: CodexGatewayBorrowConfig = { enabled: false, source_account_ids: [], target_account_ids: [], models: ['gpt-6-astra', 'gpt-6.1-sol'] }
const enabledConfig: CodexGatewayBorrowConfig = { ...disabledConfig, enabled: true, source_account_ids: [1], target_account_ids: [2, 3] }
const localAccounts = [
  { id: 1, name: 'Source', platform: 'openai', type: 'oauth', status: 'active', schedulable: true, proxy_id: 7, proxy: { id: 7, name: 'Bound proxy' }, extra: { openai_oauth_responses_websockets_v2_mode: 'ctx_pool' } },
  { id: 2, name: 'Paused target', platform: 'openai', type: 'setup-token', status: 'error', schedulable: false, proxy_id: null, parent_account_id: 1, extra: {}, error_message: 'Original account error' },
  { id: 3, name: 'Target without cache', platform: 'openai', type: 'oauth', status: 'active', schedulable: true, proxy_id: null, extra: {} },
  { id: 4, name: 'API key', platform: 'openai', type: 'apikey', status: 'active' }
] as unknown as AccountListItem[]

function makeStatus(config = enabledConfig): CodexGatewayBorrowStatus {
  return {
    enabled: config.enabled, revision: 1, generated_at: now, preparing: false, config: structuredClone(config),
    candidate: { source_account_id: 1, expires_at: expires, remaining_seconds: 3600, cookie_fingerprint: 'full-fingerprint' },
    sources: [{ account_id: 1, state: 'ready', reason: 'candidate_ready', remaining_seconds: 3600 }],
    targets: config.target_account_ids.flatMap(account_id => config.models.map(model => ({ account_id, model, state: account_id === 2 ? 'valid' : 'missing', reason: account_id === 2 ? 'validated' : 'no_matched_cache', cache_valid: account_id === 2, expires_at: expires, remaining_seconds: 3600, mint_status: 200, continue_status: 200, minted: true, new_ticket: true }))),
    model_efforts: { 'gpt-6-astra': ['medium', 'high', 'max'], 'gpt-6.1-sol': ['low', 'medium', 'high', 'xhigh', 'max', 'ultra'] }
  }
}
function makeResult(overrides: Partial<BorrowTestResult> = {}): BorrowTestResult {
  return { id: 'result-1', account_id: 2, account_name: 'Paused target', model_id: 'gpt-6.1-sol', effort: 'high', status: 'complete', raw_answer: '<html><script>window.generated=1</script><svg></svg></html>', raw_response: '{"response":"full"}', raw_html: '<html><script>window.generated=1</script><svg></svg></html>', html: '<html><script>window.generated=1</script><svg></svg></html>', error: '', duration_ms: 1500, started_at: now, preview_url: `/api/v1/codex-gateway-borrow/preview/${'a'.repeat(43)}/index.html`, ...overrides }
}
function makeTask(results: BorrowTestResult[] = []): BorrowTestTask {
  return { id: 'server-task', client_task_id: 'client-task', status: 'complete', created_at: now, expires_at: expires, total: results.length, completed: results.length, results }
}
async function mountView(config = enabledConfig) {
  mocks.getConfig.mockResolvedValue(structuredClone(config))
  mocks.getStatus.mockResolvedValue(makeStatus(config))
  const wrapper = mount(CodexGatewayBorrowView, { global: { stubs: {
    AppLayout: { template: '<div><slot /></div>' },
    BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' },
    BorrowPelicanPreview: { props: ['previewUrl'], template: '<div data-test="preview-cap">{{ previewUrl }}</div>' }
  } } })
  wrappers.push(wrapper)
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  for (const mock of Object.values(mocks)) mock.mockReset()
  mocks.listAccounts.mockResolvedValue({ items: localAccounts, total: 4, page: 1, page_size: 100, pages: 1 })
  mocks.listTests.mockResolvedValue({ items: [], total: 0, page: 1, size: 12 })
  mocks.saveConfig.mockImplementation(async config => structuredClone(config))
  mocks.prepare.mockResolvedValue(makeStatus())
  mocks.verify.mockResolvedValue({ account_id: 2, model: 'gpt-6.1-sol', success: true })
})
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()) })

describe('CodexGatewayBorrowView request boundaries', () => {
  it('reads local configuration, inventory, status and server history without auto-selecting or generating', async () => {
    const wrapper = await mountView(disabledConfig)
    expect(mocks.listAccounts).toHaveBeenCalledWith(1, 100, { platform: 'openai', types: 'oauth,setup-token' }, expect.objectContaining({ signal: expect.any(AbortSignal) }))
    expect(wrapper.find('[data-test="borrow-source_account_ids-4"]').exists()).toBe(false)
    expect(wrapper.findAll<HTMLInputElement>('input[type="checkbox"]').filter(input => input.attributes('data-test')?.match(/borrow-(source|target)_account_ids/)).every(input => !input.element.checked)).toBe(true)
    expect(wrapper.get<HTMLTextAreaElement>('[data-test="borrow-fixed-prompt"]').element.value).toBe('创建一个HTML，内容是SVG绘制一个鹈鹕骑自行车的2D动画，你不需要任何测试')
    expect(wrapper.get('[data-test="borrow-fixed-prompt"]').attributes('readonly')).toBeDefined()
    expect(mocks.prepare).not.toHaveBeenCalled()
    expect(mocks.verify).not.toHaveBeenCalled()
    expect(mocks.streamTests).not.toHaveBeenCalled()
    expect(mocks.getAvailableModels).not.toHaveBeenCalled()
    expect(mocks.testAccount).not.toHaveBeenCalled()
  })

  it('keeps source and target sets disjoint and saves through one PUT without preparing twice', async () => {
    const wrapper = await mountView(disabledConfig)
    await wrapper.get('[data-test="borrow-enabled"]').trigger('click')
    expect(wrapper.get<HTMLButtonElement>('[data-test="borrow-save"]').element.disabled).toBe(true)
    await wrapper.get('[data-test="borrow-source_account_ids-1"]').setValue(true)
    expect(wrapper.get<HTMLInputElement>('[data-test="borrow-target_account_ids-1"]').element.disabled).toBe(true)
    await wrapper.get('[data-test="borrow-target_account_ids-2"]').setValue(true)
    await wrapper.get('[data-test="borrow-save"]').trigger('click')
    await flushPromises()
    expect(mocks.saveConfig).toHaveBeenCalledOnce()
    expect(mocks.saveConfig.mock.calls[0][0]).toEqual({ enabled: true, source_account_ids: [1], target_account_ids: [2], models: ['gpt-6-astra', 'gpt-6.1-sol'] })
    expect(mocks.prepare).not.toHaveBeenCalled()
    expect(mocks.streamTests).not.toHaveBeenCalled()
  })

  it('runs prepare and force verify only on their own explicit controls', async () => {
    const wrapper = await mountView()
    await wrapper.get('[data-test="borrow-prepare"]').trigger('click')
    await flushPromises()
    expect(mocks.prepare).toHaveBeenCalledOnce()
    await wrapper.get('[data-test="borrow-verify-2-gpt-6.1-sol"]').trigger('click')
    await flushPromises()
    expect(mocks.verify).toHaveBeenCalledWith(2, 'gpt-6.1-sol', expect.any(AbortSignal))
    expect(mocks.streamTests).not.toHaveBeenCalled()
    expect(mocks.testAccount).not.toHaveBeenCalled()
  })

  it('explicitly selects paused targets with valid caches and shows skip for missing caches', async () => {
    const wrapper = await mountView()
    expect(wrapper.get('[data-test="borrow-test-account-2"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.text()).toContain('admin.codexGatewayBorrow.paused')
    expect(wrapper.text()).toContain('admin.codexGatewayBorrow.willSkip')
    expect(wrapper.get<HTMLButtonElement>('[data-test="borrow-test-start"]').element.disabled).toBe(true)
    await wrapper.get('[data-test="borrow-test-account-2"]').setValue(true)
    expect(wrapper.get<HTMLButtonElement>('[data-test="borrow-test-start"]').element.disabled).toBe(false)
    expect(mocks.streamTests).not.toHaveBeenCalled()
    const efforts = wrapper.findAll('select').map(select => select.findAll('option').map(option => option.element.value))
    expect(efforts).toEqual([['medium', 'high', 'max'], ['low', 'medium', 'high', 'xhigh', 'max', 'ultra']])
  })

  it('updates one card per result, preserves full errors and raw source, and cancels on stop', async () => {
    let emit!: (event: BorrowTestEvent) => void
    let signal!: AbortSignal
    let finish!: () => void
    mocks.streamTests.mockImplementation((_request, onEvent, abortSignal) => {
      emit = onEvent; signal = abortSignal
      return new Promise<void>(resolve => { finish = resolve; signal.addEventListener('abort', () => resolve(), { once: true }) })
    })
    const wrapper = await mountView()
    await wrapper.get('[data-test="borrow-test-account-2"]').setValue(true)
    await wrapper.get('[data-test="borrow-test-start"]').trigger('click')
    const request = mocks.streamTests.mock.calls[0][0]
    expect(request.client_task_id).toMatch(/^[0-9a-f-]{14}7/)
    expect(request.targets).toEqual([{ account_id: 2, model_id: 'gpt-6-astra', effort: 'high' }, { account_id: 2, model_id: 'gpt-6.1-sol', effort: 'high' }])
    const result = makeResult({ status: 'running' })
    emit({ type: 'task_start', task: { ...makeTask([result]), status: 'running', completed: 0 } })
    const fullError = 'HTTP 429\n' + 'full error '.repeat(1800)
    emit({ type: 'result_complete', task_id: 'server-task', result: { ...result, status: 'failed', error: fullError } })
    await flushPromises()
    expect(wrapper.findAll('[data-test="borrow-result-result-1"]')).toHaveLength(1)
    expect(wrapper.get('[data-test="borrow-result-error"]').text()).toBe(fullError.trim())
    expect(wrapper.findAll('details').filter(item => item.text().includes(result.raw_html)).every(item => !(item.element as HTMLDetailsElement).open)).toBe(true)
    expect(wrapper.find('script').exists()).toBe(false)
    await wrapper.get('[data-test="borrow-test-stop"]').trigger('click')
    expect(signal.aborted).toBe(true)
    finish()
    await flushPromises()
    expect(mocks.streamTests).toHaveBeenCalledOnce()
    expect(mocks.testAccount).not.toHaveBeenCalled()
  })

  it('reads a past task and preview without generating, and aborts all connections on unmount', async () => {
    const task = makeTask([makeResult()])
    mocks.listTests.mockResolvedValue({ items: [task], total: 1, page: 1, size: 12 })
    mocks.getTest.mockResolvedValue(task)
    const wrapper = await mountView()
    await wrapper.get('[data-test="borrow-tab-history"]').trigger('click')
    await wrapper.get('[data-test="borrow-history-server-task"]').trigger('click')
    await flushPromises()
    expect(mocks.getTest).toHaveBeenCalledWith('server-task', expect.any(AbortSignal))
    expect(wrapper.get('[data-test="preview-cap"]').text()).toBe(task.results![0].preview_url)
    expect(mocks.streamTests).not.toHaveBeenCalled()
    let testSignal!: AbortSignal
    mocks.streamTests.mockImplementation((_request, _event, signal) => { testSignal = signal; return new Promise<void>(resolve => signal.addEventListener('abort', () => resolve(), { once: true })) })
    await wrapper.get('[data-test="borrow-test-account-2"]').setValue(true)
    await wrapper.get('[data-test="borrow-test-start"]').trigger('click')
    const readSignal = mocks.getConfig.mock.calls[0][0] as AbortSignal
    wrapper.unmount()
    expect(testSignal.aborted).toBe(true)
    expect(readSignal.aborted).toBe(true)
    await flushPromises()
    expect(mocks.streamTests).toHaveBeenCalledOnce()
  })
})
