import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import CodexPelicanComparisonView from '../CodexPelicanComparisonView.vue'
import { PELICAN_BORROW_PROMPT, type BorrowTestEvent, type BorrowTestResult, type BorrowTestTask, type CodexGatewayBorrowConfig, type CodexGatewayBorrowStatus } from '@/api/admin/codexGatewayBorrow'
import type { AccountListItem } from '@/types'

const mocks = vi.hoisted(() => ({
  getConfig: vi.fn(), saveConfig: vi.fn(), getStatus: vi.fn(), prepare: vi.fn(), verify: vi.fn(), listTests: vi.fn(), getTest: vi.fn(), streamTests: vi.fn(),
  listAccounts: vi.fn(), getAvailableModels: vi.fn(), testAccount: vi.fn()
}))
vi.mock('@/api/admin/codexGatewayBorrow', async importOriginal => ({
  ...(await importOriginal<typeof import('@/api/admin/codexGatewayBorrow')>()),
  codexGatewayBorrowAPI: mocks
}))
vi.mock('@/api/admin/accounts', () => {
  const accountsAPI = { list: mocks.listAccounts, getAvailableModels: mocks.getAvailableModels, testAccount: mocks.testAccount }
  return { accountsAPI, default: accountsAPI }
})
vi.mock('vue-i18n', async importOriginal => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string, values?: Record<string, unknown>) => values?.count === undefined ? key : `${key} (${values.count})`, te: () => true })
}))

const wrappers: ReturnType<typeof mount>[] = []
const disabledConfig: CodexGatewayBorrowConfig = { enabled: false, source_account_ids: [], target_account_ids: [], models: ['gpt-6-astra', 'gpt-6.1-sol'] }
const enabledConfig: CodexGatewayBorrowConfig = { ...disabledConfig, enabled: true, source_account_ids: [1], target_account_ids: [2, 3] }
const localAccounts = [
  { id: 1, name: 'Source', platform: 'openai', type: 'oauth', status: 'active', schedulable: true, proxy_id: 7, proxy: { id: 7, name: 'Bound proxy' }, extra: {} },
  { id: 2, name: 'Paused target', platform: 'openai', type: 'setup-token', status: 'error', schedulable: false, proxy_id: null, parent_account_id: 1, extra: {}, error_message: 'Original account error' },
  { id: 3, name: 'Target without cache', platform: 'openai', type: 'oauth', status: 'active', schedulable: true, proxy_id: null, extra: {} },
  { id: 4, name: 'API key', platform: 'openai', type: 'apikey', status: 'active' }
] as unknown as AccountListItem[]

function makeStatus(config = enabledConfig): CodexGatewayBorrowStatus {
  const now = new Date().toISOString()
  const expires = new Date(Date.now() + 60 * 60 * 1000).toISOString()
  return {
    enabled: config.enabled, revision: 1, generated_at: now, preparing: false, config: structuredClone(config),
    candidate: { source_account_id: 1, expires_at: expires, remaining_seconds: 3600, cookie_fingerprint: 'full-fingerprint' },
    sources: [{ account_id: 1, state: 'ready', reason: 'candidate_ready', remaining_seconds: 3600 }],
    targets: config.target_account_ids.flatMap(account_id => config.models.map(model => ({ account_id, model, state: account_id === 2 ? 'valid' : 'missing', reason: account_id === 2 ? 'validated' : 'no_matched_cache', cache_valid: account_id === 2, expires_at: expires, remaining_seconds: 3600, mint_status: 200, continue_status: 200, minted: true, new_ticket: true }))),
    model_efforts: { 'gpt-6-astra': ['medium', 'high', 'max'], 'gpt-6.1-sol': ['low', 'medium', 'high', 'xhigh', 'max', 'ultra'] }
  }
}
function makeResult(overrides: Partial<BorrowTestResult> = {}): BorrowTestResult {
  return { id: 'result-1', account_id: 2, account_name: 'Paused target', model_id: 'gpt-6.1-sol', effort: 'high', status: 'complete', raw_answer: '<html><script>window.generated=1</script><svg></svg></html>', raw_response: 'data: {"response":"full"}\n\ndata: [DONE]\n\n', raw_html: '<html><script>window.generated=1</script><svg></svg></html>', html: '<html><script>window.generated=1</script><svg></svg></html>', error: '', duration_ms: 1500, started_at: new Date().toISOString(), preview_url: `/api/v1/codex-gateway-borrow/preview/${'a'.repeat(43)}/index.html`, ...overrides }
}
function makeTask(results: BorrowTestResult[] = []): BorrowTestTask {
  return { id: 'server-task', client_task_id: 'client-task', status: 'complete', created_at: new Date().toISOString(), expires_at: new Date(Date.now() + 60 * 60 * 1000).toISOString(), total: results.length, completed: results.length, results }
}
async function mountView(config = enabledConfig, statusOverrides: Partial<CodexGatewayBorrowStatus> = {}) {
  mocks.getStatus.mockResolvedValue({ ...makeStatus(config), ...statusOverrides })
  const wrapper = mount(CodexPelicanComparisonView, { global: { stubs: {
    AppLayout: { template: '<div><slot /></div>' },
    CodexBorrowNav: true,
    RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' },
    BaseDialog: { props: ['show'], template: '<div v-if="show" data-test="enlarged-dialog"><slot /></div>' },
    BorrowPelicanPreview: { props: { previewUrl: String, interactive: Boolean }, template: '<div data-test="preview-cap" :data-interactive="interactive">{{ previewUrl }}</div>' }
  } } })
  wrappers.push(wrapper)
  await flushPromises()
  return wrapper
}
function assertNoUpstreamOrConfigurationRequests() {
  expect(mocks.getConfig).not.toHaveBeenCalled()
  expect(mocks.saveConfig).not.toHaveBeenCalled()
  expect(mocks.prepare).not.toHaveBeenCalled()
  expect(mocks.verify).not.toHaveBeenCalled()
  expect(mocks.getAvailableModels).not.toHaveBeenCalled()
  expect(mocks.testAccount).not.toHaveBeenCalled()
}
function unmountView(wrapper: ReturnType<typeof mount>) {
  wrappers.splice(wrappers.indexOf(wrapper), 1)
  wrapper.unmount()
}

beforeEach(() => {
  for (const mock of Object.values(mocks)) mock.mockReset()
  mocks.listAccounts.mockResolvedValue({ items: localAccounts, total: 4, page: 1, page_size: 100, pages: 1 })
  mocks.listTests.mockResolvedValue({ items: [], total: 0, page: 1, size: 12 })
})
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.useRealTimers() })

describe('CodexPelicanComparisonView request boundaries', () => {
  it('only reads saved status, local inventory and history, and keeps the fixed prompt collapsed and read-only', async () => {
    const wrapper = await mountView(disabledConfig)
    expect(mocks.getStatus).toHaveBeenCalledOnce()
    expect(mocks.listAccounts).toHaveBeenCalledWith(1, 100, { platform: 'openai', types: 'oauth,setup-token' }, expect.objectContaining({ signal: expect.any(AbortSignal) }))
    expect(mocks.listTests).toHaveBeenCalledOnce()
    expect(wrapper.find('[data-test="borrow-test-account-4"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="borrow-save"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="borrow-prepare"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="borrow-enabled"]').exists()).toBe(false)
    expect(wrapper.get<HTMLDetailsElement>('[data-test="pelican-prompt-details"]').element.open).toBe(false)
    expect(wrapper.get<HTMLTextAreaElement>('[data-test="borrow-fixed-prompt"]').element.value).toBe(PELICAN_BORROW_PROMPT)
    expect(PELICAN_BORROW_PROMPT).toBe('创建一个HTML，内容是SVG绘制一个鹈鹕骑自行车的2D动画，你不需要任何测试')
    expect(wrapper.get('[data-test="borrow-fixed-prompt"]').attributes('readonly')).toBeDefined()
    expect(wrapper.get('[data-test="pelican-status-link"]').attributes('href')).toBe('/admin/codex-gateway-borrow/status')
    await wrapper.get('[data-test="borrow-refresh"]').trigger('click')
    await wrapper.get('[data-test="borrow-tab-history"]').trigger('click')
    await wrapper.get('[data-test="borrow-history-refresh"]').trigger('click')
    await flushPromises()
    expect(mocks.getStatus).toHaveBeenCalledTimes(2)
    expect(mocks.streamTests).not.toHaveBeenCalled()
    assertNoUpstreamOrConfigurationRequests()
  })

  it('requires explicit account selection, includes paused targets, and shows every combination and supported effort', async () => {
    const wrapper = await mountView()
    expect(wrapper.get('[data-test="borrow-test-account-2"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.get<HTMLInputElement>('[data-test="borrow-test-account-2"]').element.checked).toBe(false)
    expect(wrapper.text()).toContain('admin.codexGatewayBorrow.paused')
    expect(wrapper.get('[data-test="pelican-qualification-2-gpt-6.1-sol"]').text()).toContain('admin.codexGatewayBorrow.lineStates.ready')
    expect(wrapper.get('[data-test="pelican-qualification-2-gpt-6.1-sol"]').text()).toContain('admin.codexGatewayBorrow.reasons.validated')
    expect(wrapper.get('[data-test="pelican-qualification-3-gpt-6.1-sol"]').text()).toContain('admin.codexGatewayBorrow.willSkip')
    expect(wrapper.get('[data-test="pelican-qualification-3-gpt-6.1-sol"]').text()).toContain('admin.codexGatewayBorrow.reasons.no_matched_cache')
    expect(wrapper.get<HTMLButtonElement>('[data-test="borrow-test-start"]').element.disabled).toBe(true)
    const efforts = wrapper.findAll('select').map(select => select.findAll('option').map(option => option.element.value))
    expect(efforts).toEqual([['medium', 'high', 'max'], ['low', 'medium', 'high', 'xhigh', 'max', 'ultra']])
    expect(wrapper.findAll<HTMLSelectElement>('select').every(select => select.element.value === 'high')).toBe(true)
    await wrapper.get('[data-test="borrow-test-account-2"]').setValue(true)
    await wrapper.get('[data-test="borrow-test-account-3"]').setValue(true)
    expect(wrapper.get('[data-test="borrow-test-start"]').text()).toContain('(4)')
    await wrapper.get('[data-test="borrow-test-model-gpt-6.1-sol"]').setValue(false)
    expect(wrapper.get('[data-test="borrow-test-start"]').text()).toContain('(2)')
    expect(mocks.streamTests).not.toHaveBeenCalled()
    assertNoUpstreamOrConfigurationRequests()
  })

  it('submits selected missing-cache combinations once with the existing parameters and server-clock task ID', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] })
    vi.setSystemTime(new Date('2026-10-08T03:30:00Z'))
    const serverTime = Date.now() - 20 * 60 * 1000
    const wrapper = await mountView(enabledConfig, { generated_at: new Date(serverTime).toISOString() })
    await wrapper.get('[data-test="borrow-test-account-3"]').setValue(true)
    await wrapper.get('[data-test="borrow-effort-gpt-6-astra"]').setValue('max')
    mocks.streamTests.mockImplementation((request, emit: (event: BorrowTestEvent) => void) => {
      const skipped = request.targets.map((target: BorrowTestRequestTarget, index: number) => makeResult({ ...target, id: `skipped-${index}`, status: 'skipped', error: 'no_matched_cache', preview_url: undefined }))
      emit({ type: 'task_complete', task: makeTask(skipped) })
      return Promise.resolve()
    })
    await wrapper.get('[data-test="borrow-test-start"]').trigger('click')
    await flushPromises()
    const request = mocks.streamTests.mock.calls[0][0]
    expect(Object.keys(request).sort()).toEqual(['client_task_id', 'targets'])
    expect(request.client_task_id).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/)
    expect(Number.parseInt(request.client_task_id.replaceAll('-', '').slice(0, 12), 16)).toBe(serverTime - 1000)
    expect(request.targets).toEqual([{ account_id: 3, model_id: 'gpt-6-astra', effort: 'max' }, { account_id: 3, model_id: 'gpt-6.1-sol', effort: 'high' }])
    expect(wrapper.findAll('[data-test="borrow-result-error"]').map(error => error.text())).toEqual(['no_matched_cache', 'no_matched_cache'])
    expect(mocks.streamTests).toHaveBeenCalledOnce()
    assertNoUpstreamOrConfigurationRequests()
  })

  it('shows immediate progress, updates one card per result, preserves full errors and originals, and aborts on stop', async () => {
    let emit!: (event: BorrowTestEvent) => void
    let signal!: AbortSignal
    mocks.streamTests.mockImplementation((_request, onEvent, abortSignal) => {
      emit = onEvent; signal = abortSignal
      return new Promise<void>(resolve => signal.addEventListener('abort', () => resolve(), { once: true }))
    })
    const wrapper = await mountView()
    await wrapper.get('[data-test="borrow-test-account-2"]').setValue(true)
    await wrapper.get('[data-test="borrow-test-start"]').trigger('click')
    expect(wrapper.findAll('article[data-test^="borrow-result-"]')).toHaveLength(2)
    expect(wrapper.get('[data-test="pelican-progress"]').text()).toContain('0 / 2')
    const result = makeResult({ status: 'running' })
    const pending = makeResult({ id: 'result-2', model_id: 'gpt-6-astra', status: 'pending', preview_url: undefined })
    emit({ type: 'task_start', task: { ...makeTask([result, pending]), status: 'running', completed: 0 } })
    emit({ type: 'result_started', task_id: 'server-task', result })
    const fullError = 'HTTP 429\n' + 'full error '.repeat(1800)
    emit({ type: 'result_complete', task_id: 'server-task', result: { ...result, status: 'failed', error: fullError } })
    await flushPromises()
    expect(wrapper.findAll('[data-test="borrow-result-result-1"]')).toHaveLength(1)
    expect(wrapper.get('[data-test="borrow-result-error"]').text()).toBe(fullError.trim())
    const originalDetails = wrapper.get('[data-test="borrow-result-result-1"]').findAll<HTMLDetailsElement>('details')
    expect(originalDetails).toHaveLength(3)
    expect(originalDetails.every(details => !details.element.open)).toBe(true)
    expect(originalDetails[0].get('pre').text()).toBe(result.raw_answer)
    expect(originalDetails[1].get('pre').text()).toBe(result.raw_html)
    expect(originalDetails[2].get('pre').text()).toBe(result.raw_response.trim())
    expect(wrapper.find('script').exists()).toBe(false)
    await wrapper.get('[data-test="borrow-test-stop"]').trigger('click')
    expect(signal.aborted).toBe(true)
    await flushPromises()
    expect(wrapper.get('[data-test="borrow-result-result-2"]').text()).toContain('admin.codexGatewayBorrow.states.cancelled')
    expect(mocks.streamTests).toHaveBeenCalledOnce()
    assertNoUpstreamOrConfigurationRequests()
  })

  it('reports an interrupted stream in full without retrying or preparing another route', async () => {
    const failure = 'stream ended without task_complete\n' + 'upstream original '.repeat(800)
    mocks.streamTests.mockRejectedValue(new Error(failure))
    const wrapper = await mountView()
    await wrapper.get('[data-test="borrow-test-account-2"]').setValue(true)
    await wrapper.get('[data-test="borrow-test-start"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe(failure.trim())
    expect(wrapper.findAll('article[data-test^="borrow-result-"]').every(card => card.text().includes('admin.codexGatewayBorrow.states.incomplete'))).toBe(true)
    expect(mocks.streamTests).toHaveBeenCalledOnce()
    assertNoUpstreamOrConfigurationRequests()
  })

  it('reads historical results and enlarged server previews without generating or changing configuration', async () => {
    const task = makeTask([makeResult()])
    mocks.listTests.mockResolvedValue({ items: [task], total: 1, page: 1, size: 12 })
    mocks.getTest.mockResolvedValue(task)
    const wrapper = await mountView()
    await wrapper.get('[data-test="borrow-tab-results"]').trigger('keydown', { key: 'ArrowRight' })
    expect(wrapper.get('[data-test="borrow-tab-history"]').attributes('aria-selected')).toBe('true')
    await wrapper.get('[data-test="borrow-history-server-task"]').trigger('click')
    await flushPromises()
    expect(mocks.getTest).toHaveBeenCalledWith('server-task', expect.any(AbortSignal))
    expect(wrapper.get('[data-test="preview-cap"]').text()).toBe(task.results![0].preview_url)
    await wrapper.get('[data-test="borrow-enlarge-result-1"]').trigger('click')
    expect(wrapper.get('[data-test="enlarged-dialog"] [data-test="preview-cap"]').attributes('data-interactive')).toBe('true')
    expect(wrapper.findAll('[data-test="preview-cap"]').every(preview => preview.text() === task.results![0].preview_url)).toBe(true)
    expect(wrapper.find('script').exists()).toBe(false)
    expect(mocks.streamTests).not.toHaveBeenCalled()
    assertNoUpstreamOrConfigurationRequests()
  })

  it('aborts in-flight generation and read requests on leaving and does not refresh history afterward', async () => {
    let testSignal!: AbortSignal
    mocks.streamTests.mockImplementation((_request, _event, signal) => {
      testSignal = signal
      return new Promise<void>(resolve => signal.addEventListener('abort', () => resolve(), { once: true }))
    })
    const wrapper = await mountView()
    await wrapper.get('[data-test="borrow-test-account-2"]').setValue(true)
    await wrapper.get('[data-test="borrow-test-start"]').trigger('click')
    const statusSignal = mocks.getStatus.mock.calls[0][0] as AbortSignal
    const inventorySignal = mocks.listAccounts.mock.calls[0][3].signal as AbortSignal
    const historySignal = mocks.listTests.mock.calls[0][1] as AbortSignal
    unmountView(wrapper)
    expect(testSignal.aborted).toBe(true)
    expect(statusSignal.aborted).toBe(true)
    expect(inventorySignal.aborted).toBe(true)
    expect(historySignal.aborted).toBe(true)
    await flushPromises()
    expect(mocks.listTests).toHaveBeenCalledOnce()
    expect(mocks.streamTests).toHaveBeenCalledOnce()
    assertNoUpstreamOrConfigurationRequests()
  })

  it('blocks generation while a historical task is loading and ignores its reply after leaving', async () => {
    const task = makeTask([makeResult()])
    mocks.listTests.mockResolvedValue({ items: [task], total: 1, page: 1, size: 12 })
    let resolveHistory!: (task: BorrowTestTask) => void
    mocks.getTest.mockImplementation(() => new Promise<BorrowTestTask>(resolve => { resolveHistory = resolve }))
    const wrapper = await mountView()
    await wrapper.get('[data-test="borrow-test-account-2"]').setValue(true)
    expect(wrapper.get<HTMLButtonElement>('[data-test="borrow-test-start"]').element.disabled).toBe(false)
    await wrapper.get('[data-test="borrow-tab-history"]').trigger('click')
    await wrapper.get('[data-test="borrow-history-server-task"]').trigger('click')
    expect(wrapper.get<HTMLButtonElement>('[data-test="borrow-test-start"]').element.disabled).toBe(true)
    await wrapper.get('[data-test="borrow-test-start"]').trigger('click')
    expect(mocks.streamTests).not.toHaveBeenCalled()
    const readSignal = mocks.getTest.mock.calls[0][1] as AbortSignal
    const viewState = wrapper.vm as unknown as { activeTask: BorrowTestTask | null }
    expect(viewState.activeTask).toBeNull()
    unmountView(wrapper)
    expect(readSignal.aborted).toBe(true)
    resolveHistory(task)
    await flushPromises()
    expect(viewState.activeTask).toBeNull()
    expect(mocks.streamTests).not.toHaveBeenCalled()
    assertNoUpstreamOrConfigurationRequests()
  })

  it('expires qualification locally, shows original unknown reasons and stops its local clock on leaving', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] })
    vi.setSystemTime(new Date('2026-10-08T03:30:00Z'))
    const status = makeStatus()
    status.targets[0].expires_at = new Date(Date.now() + 1000).toISOString()
    status.targets[2].reason = 'unknown upstream reason with details'
    const wrapper = await mountView(enabledConfig, status)
    expect(wrapper.get('[data-test="pelican-qualification-2-gpt-6-astra"]').text()).toContain('admin.codexGatewayBorrow.lineStates.ready')
    expect(wrapper.get('[data-test="pelican-qualification-3-gpt-6-astra"]').text()).toContain('unknown upstream reason with details')
    await vi.advanceTimersByTimeAsync(1100)
    const expired = wrapper.get('[data-test="pelican-qualification-2-gpt-6-astra"]').text()
    expect(expired).toContain('admin.codexGatewayBorrow.lineStates.expired')
    expect(expired).toContain('admin.codexGatewayBorrow.reasons.route_expired')
    expect(expired).toContain('admin.codexGatewayBorrow.willSkip')
    expect(expired).not.toContain('admin.codexGatewayBorrow.lineStates.ready')
    expect(mocks.getStatus).toHaveBeenCalledOnce()
    expect(mocks.streamTests).not.toHaveBeenCalled()
    unmountView(wrapper)
    expect(vi.getTimerCount()).toBe(0)
    assertNoUpstreamOrConfigurationRequests()
  })

  it('keeps history readable after status loading fails and prevents generation without a server clock', async () => {
    const task = makeTask([makeResult()])
    mocks.listTests.mockResolvedValue({ items: [task], total: 1, page: 1, size: 12 })
    mocks.getTest.mockResolvedValue(task)
    mocks.getStatus.mockRejectedValueOnce(new Error('Status read failed'))
    const wrapper = await mountView()
    expect(wrapper.get('[role="alert"]').text()).toBe('Status read failed')
    await wrapper.get('[data-test="borrow-tab-history"]').trigger('click')
    await wrapper.get('[data-test="borrow-history-server-task"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="borrow-result-result-1"]').exists()).toBe(true)
    const badClockStatus = makeStatus()
    badClockStatus.generated_at = 'invalid timestamp'
    mocks.getStatus.mockResolvedValue(badClockStatus)
    await wrapper.get('[data-test="borrow-refresh"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="borrow-test-account-2"]').setValue(true)
    expect(wrapper.get<HTMLButtonElement>('[data-test="borrow-test-start"]').element.disabled).toBe(true)
    expect(wrapper.text()).toContain('admin.codexGatewayBorrow.clockUnavailable')
    expect(mocks.streamTests).not.toHaveBeenCalled()
    assertNoUpstreamOrConfigurationRequests()
  })
})

type BorrowTestRequestTarget = { account_id: number; model_id: string; effort: string }
