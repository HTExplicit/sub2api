import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import CodexPelicanComparisonView from '../CodexPelicanComparisonView.vue'
import { PELICAN_PROMPT, type PelicanOptions, type PelicanAccountOption, type PelicanTestEvent, type PelicanTestResult, type PelicanTestTask } from '@/api/admin/pelicanTests'
import type { AccountListItem } from '@/types'

const mocks = vi.hoisted(() => ({
  getOptions: vi.fn(), getAccountOptions: vi.fn(), listTests: vi.fn(), getTest: vi.fn(), streamTests: vi.fn(),
  listAccounts: vi.fn(), getAvailableModels: vi.fn(), testAccount: vi.fn(),
  getConfig: vi.fn(), getStatus: vi.fn(), prepare: vi.fn(), verify: vi.fn(), saveConfig: vi.fn()
}))
vi.mock('@/api/admin/pelicanTests', async importOriginal => ({
  ...(await importOriginal<typeof import('@/api/admin/pelicanTests')>()), pelicanTestsAPI: mocks
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
  useI18n: () => ({ t: (key: string, values?: Record<string, unknown>) => values?.count === undefined ? key : `${key} (${values.count})`, te: () => true })
}))

const wrappers: ReturnType<typeof mount>[] = []
const localAccounts = [
  { id: 1, name: 'Source', platform: 'openai', type: 'oauth', status: 'active', schedulable: true },
  { id: 2, name: 'Paused target', platform: 'openai', type: 'setup-token', status: 'error', schedulable: false, error_message: 'Original account error' },
  { id: 3, name: 'Unconfigured API key', platform: 'openai', type: 'apikey', status: 'active', schedulable: true },
  { id: 4, name: 'Claude API key', platform: 'anthropic', type: 'apikey', status: 'active', schedulable: true },
  { id: 5, name: 'Gemini OAuth', platform: 'gemini', type: 'oauth', status: 'active', schedulable: true }
] as unknown as AccountListItem[]

function makeOptions(accounts: PelicanAccountOption[] = []): PelicanOptions {
  return { generated_at: new Date().toISOString(), max_concurrency: 10, default_generation_timeout_seconds: 600, min_generation_timeout_seconds: 60, max_generation_timeout_seconds: 1800, accounts }
}
function accountOptions(id: number): PelicanAccountOption {
  const account = localAccounts.find(item => item.id === id)!
  const ids = account.platform === 'openai' ? ['gpt-6-astra', 'gpt-6.1-sol'] : account.platform === 'anthropic' ? ['claude-sonnet-5-5'] : ['gemini-3-pro']
  return { id, name: account.name, platform: account.platform, type: account.type, status: account.status, schedulable: account.schedulable,
    parent_account_id: null, proxy_id: null, proxy_name: '', rate_limited_until: null, overload_until: null, temp_unschedulable_until: null,
    capability_reason: '', manual_model_allowed: true, default_model_id: ids[0],
    models: ids.map(model => ({ id: model, display_name: model, upstream_model: model, reasoning_efforts: account.platform === 'openai' ? ['low', 'high', 'max'] : [], default_effort: account.platform === 'openai' ? 'high' : '', text_supported: true, capability_reason: '' })) }
}
function makeResult(overrides: Partial<PelicanTestResult> = {}): PelicanTestResult {
  return { id: 'result-1', account_id: 2, account_name: 'Paused target with a complete unabridged name', platform: 'openai', model_id: 'gpt-6.1-sol', upstream_model: 'mapped-model', effort: 'high', status: 'complete',
    raw_answer: '<html><script>window.generated=1</script><svg></svg></html>', raw_response: 'data: {"response":"full"}\n\ndata: [DONE]\n\n',
    raw_html: '<html><script>window.generated=1</script><svg></svg></html>', html: '<html><script>window.generated=1</script><svg></svg></html>', error: '', duration_ms: 1500,
    actual_endpoint: 'https://local-upstream.invalid/v1/responses', actual_protocol: 'responses', actual_transport: 'http', borrow_applied: true,
    queue_duration_ms: 20, preparation_duration_ms: 30, generation_duration_ms: 1450, started_at: new Date().toISOString(),
    preview_url: `/api/v1/pelican-tests/preview/${'a'.repeat(43)}/index.html`, ...overrides }
}
function makeTask(results: PelicanTestResult[] = []): PelicanTestTask {
  return { id: 'server-task', client_task_id: 'client-task', status: 'complete', created_at: new Date().toISOString(), expires_at: new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString(), generation_timeout_seconds: 600, execution_mode: 'account', total: results.length, completed: results.length, results }
}
async function mountView() {
  const wrapper = mount(CodexPelicanComparisonView, { global: { stubs: {
    AppLayout: { template: '<div><slot /></div>' },
    BaseDialog: { props: ['show'], template: '<div v-if="show" data-test="result-dialog"><slot /></div>' },
    BorrowPelicanPreview: { props: { previewUrl: String, interactive: Boolean }, template: '<div data-test="preview-cap" :data-interactive="interactive">{{ previewUrl }}</div>' }
  } } })
  wrappers.push(wrapper)
  await flushPromises()
  return wrapper
}
function assertNoUpstreamOrBorrowRequests() {
  for (const key of ['getConfig', 'saveConfig', 'getStatus', 'prepare', 'verify', 'getAvailableModels', 'testAccount'] as const) expect(mocks[key]).not.toHaveBeenCalled()
}
async function selectAccount(wrapper: ReturnType<typeof mount>, id: number) {
  await wrapper.get(`[data-test="pelican-account-${id}"]`).setValue(true)
  await flushPromises()
}

beforeEach(() => {
  for (const mock of Object.values(mocks)) mock.mockReset()
  localStorage.clear()
  vi.stubGlobal('IntersectionObserver', undefined)
  mocks.getOptions.mockResolvedValue(makeOptions())
  mocks.getAccountOptions.mockImplementation((ids: number[]) => Promise.resolve(makeOptions(ids.map(accountOptions))))
  mocks.listAccounts.mockResolvedValue({ items: localAccounts, total: 5, page: 1, page_size: 100, pages: 1 })
  mocks.listTests.mockResolvedValue({ items: [], total: 0, page: 1, size: 12 })
  mocks.streamTests.mockResolvedValue(undefined)
})
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.useRealTimers(); vi.unstubAllGlobals() })

describe('Standalone Pelican account tests', () => {
  it('reads all local accounts with no platform/type filter, starts with no selection, and preserves the fixed read-only prompt', async () => {
    const wrapper = await mountView()
    expect(mocks.getOptions).toHaveBeenCalledOnce()
    expect(mocks.listAccounts).toHaveBeenCalledWith(1, 100, undefined, expect.objectContaining({ signal: expect.any(AbortSignal) }))
    expect(wrapper.findAll('input[type="checkbox"]')).toHaveLength(5)
    expect(wrapper.findAll<HTMLInputElement>('input[type="checkbox"]').every(input => !input.element.checked)).toBe(true)
    expect(wrapper.find('nav').exists()).toBe(false)
    expect(wrapper.find('a[href*="codex-gateway-borrow"]').exists()).toBe(false)
    expect(wrapper.get<HTMLDetailsElement>('[data-test="pelican-prompt-details"]').element.open).toBe(false)
    expect(wrapper.get<HTMLTextAreaElement>('[data-test="pelican-fixed-prompt"]').element.value).toBe('创建一个HTML，内容是SVG绘制一个鹈鹕骑自行车的2D动画，你不需要任何测试')
    expect(wrapper.get('[data-test="pelican-fixed-prompt"]').attributes('readonly')).toBeDefined()
    expect(PELICAN_PROMPT).toBe(wrapper.get<HTMLTextAreaElement>('[data-test="pelican-fixed-prompt"]').element.value)
    expect(wrapper.get<HTMLInputElement>('[data-test="pelican-budget"]').element.value).toBe('10')
    expect(wrapper.get<HTMLButtonElement>('[data-test="pelican-test-start"]').element.disabled).toBe(true)
    expect(mocks.getAccountOptions).not.toHaveBeenCalled()
    assertNoUpstreamOrBorrowRequests()
  })

  it('filters and batch-selects all account types, keeps paused accounts selectable, and configures each account separately', async () => {
    const wrapper = await mountView()
    await wrapper.get('[data-test="pelican-platform-filter"]').setValue('anthropic')
    await wrapper.get('[data-test="pelican-select-filtered"]').trigger('click')
    await flushPromises()
    expect(mocks.getAccountOptions).toHaveBeenLastCalledWith([4], expect.any(AbortSignal))
    expect(wrapper.get<HTMLSelectElement>('[data-test="pelican-effort-4-0"]').element.value).toBe('')
    await wrapper.get('[data-test="pelican-platform-filter"]').setValue('')
    await wrapper.get('[data-test="pelican-account-search"]').setValue('Paused')
    expect(wrapper.get('[data-test="pelican-account-list"]').text()).toContain('Original account error')
    expect(wrapper.get('[data-test="pelican-account-list"]').text()).toContain('admin.codexGatewayBorrow.paused')
    expect(wrapper.get('[data-test="pelican-account-2"]').attributes('disabled')).toBeUndefined()
    await selectAccount(wrapper, 2)
    expect(wrapper.get<HTMLSelectElement>('[data-test="pelican-effort-2-0"]').element.value).toBe('high')
    await wrapper.get('[data-test="pelican-model-2-0"]').setValue('gpt-6.1-sol')
    await wrapper.get('[data-test="pelican-effort-2-0"]').setValue('max')
    await wrapper.get('[data-test="pelican-test-start"]').trigger('click')
    await flushPromises()
    expect(mocks.streamTests.mock.calls[0][0].targets).toEqual([
      { account_id: 4, model_id: 'claude-sonnet-5-5', effort: '' }, { account_id: 2, model_id: 'gpt-6.1-sol', effort: 'max' }
    ])
    assertNoUpstreamOrBorrowRequests()
  })

  it('submits current account/model pairs and a server-clock UUID once, accepts manual models, and enforces 1–30 minute budgets', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-10-08T03:30:00Z'))
    const serverTime = Date.now() - 20 * 60 * 1000
    mocks.getOptions.mockResolvedValue({ ...makeOptions(), generated_at: new Date(serverTime).toISOString() })
    mocks.getAccountOptions.mockImplementation((ids: number[]) => Promise.resolve({ ...makeOptions(ids.map(accountOptions)), generated_at: new Date(serverTime).toISOString() }))
    const wrapper = await mountView()
    await selectAccount(wrapper, 3)
    await wrapper.get('[data-test="pelican-add-model-3"]').trigger('click')
    expect(wrapper.get<HTMLInputElement>('[data-test="pelican-model-3-1"]').element.value).toBe('gpt-6.1-sol')
    await wrapper.get('[data-test="pelican-model-3-1"]').setValue('custom/text-model')
    expect(wrapper.get<HTMLSelectElement>('[data-test="pelican-effort-3-1"]').element.value).toBe('')
    for (const invalid of [0, 31, 1.5]) {
      await wrapper.get('[data-test="pelican-budget"]').setValue(invalid)
      expect(wrapper.get<HTMLButtonElement>('[data-test="pelican-test-start"]').element.disabled).toBe(true)
    }
    await wrapper.get('[data-test="pelican-budget"]').setValue(10)
    await wrapper.get('[data-test="pelican-test-start"]').trigger('click')
    await flushPromises()
    const request = mocks.streamTests.mock.calls[0][0]
    expect(Object.keys(request).sort()).toEqual(['client_task_id', 'generation_timeout_seconds', 'targets'])
    expect(request.generation_timeout_seconds).toBe(600)
    expect(Number.parseInt(request.client_task_id.replaceAll('-', '').slice(0, 12), 16)).toBe(serverTime - 1000)
    expect(request.targets).toEqual([{ account_id: 3, model_id: 'gpt-6-astra', effort: 'high' }, { account_id: 3, model_id: 'custom/text-model', effort: '' }])
    expect(mocks.streamTests).toHaveBeenCalledOnce()
    assertNoUpstreamOrBorrowRequests()
  })

  it('keeps missing text capability visible and records explicit choices without availability gating', async () => {
    const candidate = accountOptions(5)
    candidate.capability_reason = 'Account currently has no text capability'
    candidate.models[0] = { ...candidate.models[0], text_supported: false, capability_reason: 'This model only generates images' }
    mocks.getAccountOptions.mockResolvedValue(makeOptions([candidate]))
    const wrapper = await mountView()
    await selectAccount(wrapper, 5)
    expect(wrapper.text()).toContain(candidate.capability_reason)
    expect(wrapper.text()).toContain(candidate.models[0].capability_reason)
    expect(wrapper.get<HTMLButtonElement>('[data-test="pelican-test-start"]').element.disabled).toBe(false)
    expect(mocks.streamTests).not.toHaveBeenCalled()
    assertNoUpstreamOrBorrowRequests()
  })

  it('shows phase progress, keeps full answers/errors in details, and cancels queued and active tasks on stop', async () => {
    let emit!: (event: PelicanTestEvent) => void
    let signal!: AbortSignal
    mocks.streamTests.mockImplementation((_request, onEvent, abortSignal) => {
      emit = onEvent; signal = abortSignal
      return new Promise<void>(resolve => signal.addEventListener('abort', () => resolve(), { once: true }))
    })
    const wrapper = await mountView()
    await selectAccount(wrapper, 2)
    await wrapper.get('[data-test="pelican-test-start"]').trigger('click')
    const result = makeResult({ status: 'running' })
    const pending = makeResult({ id: 'result-2', account_id: 3, status: 'pending', preview_url: undefined })
    emit({ type: 'task_start', task: { ...makeTask([result, pending]), status: 'running', completed: 0 } })
    emit({ type: 'result_phase', task_id: 'server-task', result, phase: 'preparing' })
    await flushPromises()
    expect(wrapper.get('[data-test="pelican-result-result-1"]').text()).toContain('admin.pelicanTests.phases.preparing')
    const fullError = 'HTTP 429\n' + 'full error '.repeat(1800)
    emit({ type: 'result_complete', task_id: 'server-task', result: { ...result, status: 'failed', error: fullError } })
    await flushPromises()
    expect(wrapper.find('[data-test="pelican-result-error"]').exists()).toBe(false)
    await wrapper.get('[data-test="pelican-details-result-1"]').trigger('click')
    expect(wrapper.get('[data-test="pelican-result-error"]').text()).toBe(fullError.trim())
    expect(wrapper.get('[data-test="pelican-raw-answer"]').text()).toBe(result.raw_answer)
    expect(wrapper.get('[data-test="pelican-raw-html"]').text()).toBe(result.raw_html)
    expect(wrapper.get('[data-test="pelican-raw-response"]').text()).toBe(result.raw_response.trim())
    expect(wrapper.get('[data-test="pelican-detail-content"]').text()).toContain(result.actual_endpoint)
    expect(wrapper.get('[data-test="pelican-detail-content"]').text()).toContain('mapped-model')
    expect(wrapper.find('script').exists()).toBe(false)
    await wrapper.get('[data-test="pelican-test-stop"]').trigger('click')
    await flushPromises()
    expect(signal.aborted).toBe(true)
    expect(wrapper.get('[data-test="pelican-result-result-2"]').text()).toContain('admin.codexGatewayBorrow.states.cancelled')
    expect(mocks.streamTests).toHaveBeenCalledOnce()
  })

  it('reports an interrupted stream in full without generating again and cancels on unmount', async () => {
    const failure = 'stream ended without task_complete\n' + 'upstream original '.repeat(800)
    mocks.streamTests.mockRejectedValueOnce(new Error(failure))
    const wrapper = await mountView()
    await selectAccount(wrapper, 1)
    await wrapper.get('[data-test="pelican-test-start"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe(failure.trim())
    expect(wrapper.get('[data-test="pelican-result-grid"]').text()).toContain('admin.codexGatewayBorrow.states.incomplete')
    let signal!: AbortSignal
    mocks.streamTests.mockImplementation((_request, _onEvent, abortSignal) => {
      signal = abortSignal
      return new Promise<void>(resolve => signal.addEventListener('abort', () => resolve(), { once: true }))
    })
    await wrapper.get('[data-test="pelican-test-start"]').trigger('click')
    wrappers.splice(wrappers.indexOf(wrapper), 1)
    wrapper.unmount()
    await flushPromises()
    expect(signal.aborted).toBe(true)
    expect(mocks.streamTests).toHaveBeenCalledTimes(2)
    assertNoUpstreamOrBorrowRequests()
  })

  it('reads history and changes compact gallery size/details/enlargement without model calls or removing records', async () => {
    const task = makeTask([makeResult()])
    mocks.listTests.mockResolvedValue({ items: [task], total: 1, page: 1, size: 12 })
    mocks.getTest.mockResolvedValue(task)
    const wrapper = await mountView()
    await wrapper.get('[data-test="pelican-tab-results"]').trigger('keydown', { key: 'ArrowRight' })
    await wrapper.get('[data-test="pelican-history-server-task"]').trigger('click')
    await flushPromises()
    expect(mocks.getTest).toHaveBeenCalledWith('server-task', expect.any(AbortSignal))
    expect(wrapper.get('[data-test="pelican-result-grid"]').attributes('data-size')).toBe('standard')
    const card = wrapper.get('[data-test="pelican-result-result-1"]')
    expect(card.text()).not.toContain('mapped-model')
    expect(card.text()).not.toContain(task.results![0].actual_endpoint)
    expect(card.find('details').exists()).toBe(false)
    expect(wrapper.get('[data-test="preview-cap"]').text()).toBe(task.results![0].preview_url)
    await wrapper.get('[data-test="pelican-card-size"]').setValue('compact')
    expect(wrapper.get('[data-test="pelican-result-grid"]').attributes('data-size')).toBe('compact')
    expect(localStorage.getItem('pelican_card_size')).toBe('compact')
    expect(wrapper.findAll('[data-test="pelican-result-result-1"]')).toHaveLength(1)
    await wrapper.get('[data-test="pelican-enlarge-result-1"]').trigger('click')
    expect(wrapper.get('[data-test="pelican-enlarged-content"] [data-test="preview-cap"]').attributes('data-interactive')).toBe('true')
    await wrapper.get('[data-test="pelican-details-result-1"]').trigger('click')
    expect(wrapper.get('[data-test="pelican-detail-content"]').text()).toContain(task.results![0].account_name)
    expect(mocks.streamTests).not.toHaveBeenCalled()
    assertNoUpstreamOrBorrowRequests()
  })

  it('mounts previews only while cards are visible and unloads animations outside the viewport', async () => {
    let onIntersection!: IntersectionObserverCallback
    const disconnect = vi.fn()
    vi.stubGlobal('IntersectionObserver', class {
      constructor(callback: IntersectionObserverCallback) { onIntersection = callback }
      observe = vi.fn()
      unobserve = vi.fn()
      disconnect = disconnect
    })
    const task = makeTask([makeResult(), makeResult({ id: 'result-2' })])
    mocks.listTests.mockResolvedValue({ items: [task], total: 1, page: 1, size: 12 })
    mocks.getTest.mockResolvedValue(task)
    const wrapper = await mountView()
    await wrapper.get('[data-test="pelican-tab-history"]').trigger('click')
    await wrapper.get('[data-test="pelican-history-server-task"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="preview-cap"]').exists()).toBe(false)
    const target = wrapper.get('[data-preview-result="result-1"]').element
    onIntersection([{ target, isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver)
    await flushPromises()
    expect(wrapper.findAll('[data-test="preview-cap"]')).toHaveLength(1)
    onIntersection([{ target, isIntersecting: false } as IntersectionObserverEntry], {} as IntersectionObserver)
    await flushPromises()
    expect(wrapper.find('[data-test="preview-cap"]').exists()).toBe(false)
    wrappers.splice(wrappers.indexOf(wrapper), 1)
    wrapper.unmount()
    expect(disconnect).toHaveBeenCalledOnce()
    expect(mocks.streamTests).not.toHaveBeenCalled()
  })
})
