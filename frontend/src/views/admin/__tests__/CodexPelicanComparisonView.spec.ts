import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import CodexPelicanComparisonView from '../CodexPelicanComparisonView.vue'
import type { PelicanResultPage, PelicanResultSummary, PelicanTaskEvent, PelicanTaskSnapshot } from '@/api/admin/pelicanTests'

const mocks = vi.hoisted(() => ({ gallery: vi.fn(), getOptions: vi.fn(), listTasks: vi.fn(), getTask: vi.fn(), getResults: vi.fn(), getResult: vi.fn(), startTask: vi.fn(), retestTask: vi.fn(), cancelTask: vi.fn(), deleteTask: vi.fn(), deleteResult: vi.fn(), observeTask: vi.fn(), locale: { value: 'zh' } }))
vi.mock('@/api/admin/pelicanTests', async original => ({ ...(await original<typeof import('@/api/admin/pelicanTests')>()), pelicanTestsAPI: mocks }))
vi.mock('vue-i18n', async original => ({ ...(await original<typeof import('vue-i18n')>()), useI18n: () => ({ locale: mocks.locale, t: (key: string) => key }) }))
const wrappers: ReturnType<typeof mount>[] = []
const task = (id = 'task-1', status = 'complete'): PelicanTaskSnapshot => ({ id, client_task_id: 'client', status, total: 3, completed: status === 'complete' ? 3 : 1, created_at: new Date().toISOString(), expires_at: new Date(Date.now() + 86_400_000).toISOString(), prompt: 'Scene prompt', error: '', generation_timeout_seconds: 600, test_config: { test_items: ['pelican', 'candy'] }, counts: { complete: 2, failed: 1 }, failed_count: 2 })
const result = (id = 'r1', taskID = 'task-1'): PelicanResultSummary => ({ id, task_id: taskID, ordinal: 1, account_id: 1, account_name: id, platform: 'openai', model_id: 'gpt-6.1-sol', effort: 'medium', status: 'complete', has_preview: true, preview_url: `/api/v1/pelican-tests/preview/${'a'.repeat(43)}/scene.svg`, interrupted: false, started_at: new Date().toISOString(), duration_ms: 191000, queue_duration_ms: 0, preparation_duration_ms: 0, generation_duration_ms: 191000, expires_at: new Date(Date.now() + 86_400_000).toISOString(), evaluation: { test_items: ['pelican', 'candy'], scene: '雪山谷地', nonce: 'ABC123', generation: 'passed', candy: 'passed', review: 'invalid', verdict: 'invalid', checks: { svg: true, animation: true, nonce: true } } })
beforeEach(() => {
  for (const value of Object.values(mocks)) if (typeof value === 'function') value.mockReset()
  mocks.locale.value = 'zh'; sessionStorage.clear(); localStorage.clear()
  mocks.getOptions.mockResolvedValue({ generated_at: new Date().toISOString() })
  mocks.listTasks.mockResolvedValue({ items: [task(), task('task-2')], total: 2, page: 1, size: 12 })
  mocks.getTask.mockImplementation(async (id: string) => task(id))
  mocks.gallery.mockResolvedValue({ items: [result(), result('r2', 'task-2')], total: 2, page: 1, page_size: 24 })
  mocks.getResult.mockImplementation(async (id: string, resultID: string) => ({ ...result(resultID, id), raw_response: 'Full raw response', raw_answer: 'Full raw answer', raw_html: '', html: '', error: 'Full administrator error', preview_unavailable: '', evidence: { candy: { prompt: 'Candy prompt', raw_answer: btoa('21'), raw_response: btoa('RAW-CANDY'), error: '', status: 'complete', duration_ms: 20, invocation: {} } } }))
  mocks.observeTask.mockImplementation((_id: string, _event: unknown, signal: AbortSignal) => new Promise<void>(resolve => signal.addEventListener('abort', () => resolve(), { once: true })))
  mocks.cancelTask.mockResolvedValue(task('task-1', 'cancelled')); mocks.startTask.mockResolvedValue(task('new-task')); mocks.retestTask.mockResolvedValue(task('new-task'))
  mocks.deleteTask.mockResolvedValue({ deleted: true }); mocks.deleteResult.mockResolvedValue({ deleted: true })
})
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.useRealTimers(); vi.unstubAllGlobals() })
async function make(realPreview = false) { const wrapper = mount(CodexPelicanComparisonView, { global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' }, PelicanTaskComposer: { props: ['initial', 'submitting'], emits: ['submit'], template: '<div data-test="composer-stub" />' }, PelicanPreview: !realPreview } } }); wrappers.push(wrapper); await flushPromises(); return wrapper }
async function selectTask(wrapper: ReturnType<typeof mount>) { await wrapper.get('[data-test="pelican-task-select"]').setValue('task-1'); await flushPromises() }

describe('Pelican gallery workbench', () => {
  it('opens all-work summaries and previews without reading answers or invoking models', async () => {
    const wrapper = await make()
    expect(mocks.gallery).toHaveBeenCalledWith(expect.objectContaining({ page: 1, page_size: 24, task_id: undefined }), expect.any(AbortSignal))
    expect(wrapper.findAll('article[data-test^="pelican-result-"]')).toHaveLength(2)
    expect(wrapper.text()).toContain('雪山谷地'); expect(wrapper.text()).toContain('视觉审核 · 未通过')
    expect(mocks.getResult).not.toHaveBeenCalled(); expect(mocks.startTask).not.toHaveBeenCalled(); expect(mocks.retestTask).not.toHaveBeenCalled()
    await wrapper.get('[data-test="pelican-result-r1"] button').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('Full administrator error'); expect(wrapper.text()).toContain('Full raw response'); expect(wrapper.text()).toContain('RAW-CANDY')
  })
  it('filters by task and verdict through the gallery endpoint', async () => {
    const wrapper = await make(); await selectTask(wrapper)
    expect(mocks.gallery).toHaveBeenLastCalledWith(expect.objectContaining({ task_id: 'task-1' }), expect.any(AbortSignal))
    await wrapper.get('[data-test="pelican-verdict-filter"]').setValue('invalid'); await new Promise(resolve => setTimeout(resolve, 270)); await flushPromises()
    expect(mocks.gallery).toHaveBeenLastCalledWith(expect.objectContaining({ task_id: 'task-1', verdict: 'invalid' }), expect.any(AbortSignal)); expect(mocks.getResult).not.toHaveBeenCalled()
  })
  it('reconnects read-only observation and unmount only disconnects', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'setInterval', 'clearTimeout', 'clearInterval'] })
    mocks.getTask.mockResolvedValue(task('task-1', 'running')); mocks.observeTask.mockRejectedValueOnce(new Error('disconnected'))
    const wrapper = await make(); await selectTask(wrapper); expect(wrapper.text()).toContain('正在重连')
    await vi.advanceTimersByTimeAsync(1000); await flushPromises(); expect(mocks.observeTask).toHaveBeenCalledTimes(2)
    const signal = mocks.observeTask.mock.calls[1][2] as AbortSignal; wrapper.unmount(); expect(signal.aborted).toBe(true); expect(mocks.cancelTask).not.toHaveBeenCalled(); expect(mocks.startTask).not.toHaveBeenCalled()
  })
  it('only the explicit stop button cancels a running task', async () => {
    mocks.getTask.mockResolvedValue(task('task-1', 'running')); const wrapper = await make(); await selectTask(wrapper)
    await wrapper.get('[data-test="pelican-test-stop"]').trigger('click'); await flushPromises(); expect(mocks.cancelTask).toHaveBeenCalledWith('task-1')
  })
  it('retests failures server-side without enumerating original pages', async () => {
    const wrapper = await make(); await selectTask(wrapper)
    await wrapper.get('[data-test="pelican-retry-failed"]').trigger('click'); await flushPromises()
    expect(mocks.retestTask).toHaveBeenCalledWith('task-1', expect.objectContaining({ failed_only: true, client_task_id: expect.any(String) }), expect.any(AbortSignal))
    expect(mocks.getResults).not.toHaveBeenCalled(); expect(mocks.startTask).not.toHaveBeenCalled()
  })
  it('retests one work with a selected-result request and blocks a second submission', async () => {
    let resolve!: (value: PelicanTaskSnapshot) => void; mocks.retestTask.mockImplementation(() => new Promise(done => { resolve = done }))
    const wrapper = await make(); const button = wrapper.get('[data-test="pelican-result-r1"] [data-test="pelican-retest-result"]')
    await button.trigger('click'); await flushPromises(); expect((button.element as HTMLButtonElement).disabled).toBe(true); await button.trigger('click'); expect(mocks.retestTask).toHaveBeenCalledOnce()
    expect(mocks.retestTask.mock.calls[0][1].result_ids).toEqual(['r1']); resolve(task('new-task')); await flushPromises()
  })
  it('compares across tasks and groups selected failure retests by source task', async () => {
    const wrapper = await make(); for (const box of wrapper.findAll('[data-test^="pelican-result-"] input[type="checkbox"]')) await box.setValue(true)
    await wrapper.get('[data-test="pelican-compare"]').trigger('click'); await flushPromises(); expect(mocks.getResult.mock.calls.map(call => call[0])).toEqual(['task-1', 'task-2'])
    await wrapper.get('[data-test="pelican-retry-selected"]').trigger('click'); await flushPromises(); expect(mocks.retestTask.mock.calls.map(call => call[0])).toEqual(['task-1', 'task-2'])
  })
  it('deletes a work through its source task and refreshes summaries', async () => {
    const wrapper = await make(); await wrapper.get('[data-test="pelican-result-r1"] [data-test="pelican-delete-result"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="pelican-confirm-delete"]').trigger('click'); await flushPromises(); expect(mocks.deleteResult).toHaveBeenCalledWith('task-1', 'r1'); expect(mocks.gallery.mock.calls.length).toBeGreaterThan(1)
  })
  it('handles normalized API rejection and allows a corrected new submission', async () => {
    mocks.retestTask.mockRejectedValue({ status: 400, message: 'Invalid request' })
    const wrapper = await make(); const button = wrapper.get('[data-test="pelican-result-r1"] [data-test="pelican-retest-result"]')
    await button.trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('Invalid request'); expect(wrapper.text()).not.toContain('上次提交尚未确认')
    expect((button.element as HTMLButtonElement).disabled).toBe(false)
  })
  it('does not display a fake animation for candy-only results', async () => {
    const value = result(); value.has_preview = false; value.preview_url = undefined; value.evaluation = { ...value.evaluation!, test_items: ['candy'], generation: 'not_selected', review: 'not_selected', verdict: 'passed' }
    mocks.gallery.mockResolvedValue({ items: [value], total: 1, page: 1, page_size: 24 }); const wrapper = await make(); expect(wrapper.text()).toContain('糖果推理检测'); expect(wrapper.find('img').exists()).toBe(false)
  })
  it('shows a restarted partial work as interrupted while retaining its preview', async () => {
    const value = result(); value.status = 'incomplete'; value.interrupted = true; value.evaluation = { ...value.evaluation!, review: 'running', verdict: 'running' }
    mocks.gallery.mockResolvedValue({ items: [value], total: 1, page: 1, page_size: 24 })
    const wrapper = await make(); expect(wrapper.text()).toContain('服务中断'); expect(wrapper.text()).toContain('视觉审核 · 未完成'); expect(wrapper.text()).not.toContain('视觉审核 · 进行中')
    expect(wrapper.findComponent({ name: 'PelicanPreview' }).exists()).toBe(true)
    expect(mocks.retestTask).not.toHaveBeenCalled()
  })
})

it('copies each model once with its configured repeat count and displays sample numbers', async () => {
  const source = task(); source.test_config = { test_items: ['pelican', 'candy'], repeat_count: 3 }
  mocks.getTask.mockResolvedValue(source)
  const second = { ...result('r2'), repeat_index: 2, repeat_count: 3 }
  mocks.gallery.mockResolvedValue({ items: [second], total: 1, page: 1, page_size: 24 })
  mocks.getResults.mockResolvedValue({ items: [result('r1'), second, { ...result('r3'), model_id: 'gpt-6-astra' }], total: 3, page: 1, page_size: 50 })
  mocks.getResult.mockResolvedValue({ ...second, raw_answer: '', raw_response: '', raw_html: '', html: '', error: '', preview_unavailable: '' })
  const wrapper = await make(); await selectTask(wrapper)
  expect(wrapper.get('[data-test="pelican-repeat-label"]').text()).toBe('第 2 / 3 次')
  await wrapper.get('[data-test="pelican-result-r2"] button').trigger('click'); await flushPromises()
  expect(wrapper.get('[data-test="pelican-repeat-detail"]').text()).toContain('第 2 / 3 次')
  const copy = wrapper.findAll('button').find(button => button.text() === '复制参数')!
  await copy.trigger('click'); await flushPromises()
  expect(wrapper.getComponent('[data-test="composer-stub"]').props('initial')).toEqual(expect.objectContaining({ repeat_count: 3, targets: [{ account_id: 1, model_id: 'gpt-6.1-sol', effort: 'medium' }, { account_id: 1, model_id: 'gpt-6-astra', effort: 'medium' }] }))
  expect(mocks.startTask).not.toHaveBeenCalled()
})

it('keeps original-mode HTML works previewable without empty scene or nonce labels', async () => {
  const value = result()
  value.status = 'failed'; value.preview_url = value.preview_url!.replace('/scene.svg', '/index.html')
  value.evaluation = { ...value.evaluation!, scene: '', nonce: '', checks: { document: true }, review: 'error', verdict: 'error' }
  mocks.gallery.mockResolvedValue({ items: [value], total: 1, page: 1, page_size: 24 })
  mocks.getResult.mockResolvedValue({ ...value, raw_html: '<html><svg/><script>animate()</script></html>', html: '<html><svg/><script>animate()</script></html>', raw_response: 'RAW', raw_answer: 'ANSWER', error: 'Review error', preview_unavailable: '' })
  const wrapper = await make()
  expect(wrapper.get('[data-test="pelican-result-r1"]').text()).not.toContain('场景')
  expect(wrapper.get('[data-test="pelican-result-r1"]').text()).not.toContain('校验码')
  expect(wrapper.getComponent({ name: 'PelicanPreview' }).props('previewUrl')).toContain('/index.html')
  await wrapper.get('[data-test="pelican-result-r1"] button').trigger('click'); await flushPromises()
  const content = wrapper.get('[data-test="pelican-detail-content"]')
  expect(content.text()).not.toContain('场景：'); expect(content.text()).not.toContain('校验码：')
  expect(content.text()).toContain('作品格式'); expect(content.text()).toContain('Review error')
})


it('updates task progress quietly without restarting the displayed HTML animation', async () => {
  let onEvent!: (event: PelicanTaskEvent) => void
  mocks.observeTask.mockImplementation((_id: string, callback: typeof onEvent, signal: AbortSignal) => {
    onEvent = callback
    return new Promise<void>(resolve => signal.addEventListener('abort', () => resolve(), { once: true }))
  })
  mocks.getTask.mockResolvedValue(task('task-1', 'running'))
  const value = result()
  value.preview_url = value.preview_url!.replace('scene.svg', 'index.html')
  mocks.gallery.mockResolvedValue({ items: [value], total: 1, page: 1, page_size: 24 })
  const wrapper = await make(true); await selectTask(wrapper)
  const animation = wrapper.get('iframe').element
  const source = wrapper.get('iframe').attributes('src')
  let completeRead!: (page: PelicanResultPage) => void
  mocks.gallery.mockImplementationOnce(() => new Promise(resolve => { completeRead = resolve }))
  const progress = { ...task('task-1', 'running'), completed: 2 }
  onEvent({ type: 'snapshot', task: progress }); await wrapper.vm.$nextTick()
  expect(wrapper.get('[data-test="pelican-active-task"]').text()).toContain('已处理 2 / 3')
  expect(wrapper.text()).not.toContain('读取中…')
  expect((wrapper.get('[data-test="pelican-refresh"]').element as HTMLButtonElement).disabled).toBe(false)
  completeRead({ items: [{ ...value, preview_url: value.preview_url!.replace('a'.repeat(43), 'b'.repeat(43)), evaluation: { ...value.evaluation!, review: 'passed', verdict: 'passed' } }], total: 1, page: 1, page_size: 24 })
  await flushPromises()
  expect(wrapper.get('iframe').element).toBe(animation)
  expect(wrapper.get('iframe').attributes('src')).toBe(source)
  expect(wrapper.get('[data-test="pelican-result-r1"]').text()).toContain('视觉审核 · 通过')
  expect(mocks.startTask).not.toHaveBeenCalled(); expect(mocks.retestTask).not.toHaveBeenCalled(); expect(mocks.cancelTask).not.toHaveBeenCalled()
})
