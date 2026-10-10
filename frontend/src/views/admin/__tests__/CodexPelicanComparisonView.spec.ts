import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import CodexPelicanComparisonView from '../CodexPelicanComparisonView.vue'
import PelicanTaskComposer from '@/components/admin/codex/PelicanTaskComposer.vue'
import type { PelicanResultSummary, PelicanTaskSnapshot } from '@/api/admin/pelicanTests'

const mocks = vi.hoisted(() => ({ getOptions: vi.fn(), getAccountOptions: vi.fn(), listTasks: vi.fn(), getTask: vi.fn(), getResults: vi.fn(), getResult: vi.fn(), startTask: vi.fn(), cancelTask: vi.fn(), observeTask: vi.fn(), listAccounts: vi.fn(), locale: { value: 'zh' } }))
vi.mock('@/api/admin/pelicanTests', async original => ({ ...(await original<typeof import('@/api/admin/pelicanTests')>()), pelicanTestsAPI: mocks }))
vi.mock('@/api/admin/accounts', () => ({ accountsAPI: { list: mocks.listAccounts }, default: { list: mocks.listAccounts } }))
vi.mock('vue-i18n', async original => ({ ...(await original<typeof import('vue-i18n')>()), useI18n: () => ({ locale: mocks.locale, t: (key: string) => key, te: () => true }) }))
const wrappers: ReturnType<typeof mount>[] = []
const task = (id = 'task-1', status = 'complete'): PelicanTaskSnapshot => ({ id, client_task_id: 'client', status, total: 3, completed: status === 'complete' ? 3 : 1, created_at: new Date().toISOString(), expires_at: new Date(Date.now() + 86_400_000).toISOString(), prompt: 'Fixed prompt', error: '', generation_timeout_seconds: 600, counts: { complete: 1, failed: 1, cancelled: 1 } })
const result = (id: string, taskID = 'task-1', status = 'failed'): PelicanResultSummary => ({ id, task_id: taskID, ordinal: 1, account_id: id === 'r1' ? 1 : 2, account_name: id, platform: 'openai', model_id: 'gpt-6.1-sol', effort: '', status, has_preview: false, interrupted: false, duration_ms: 10, queue_duration_ms: 1, preparation_duration_ms: 2, generation_duration_ms: 7, expires_at: new Date(Date.now() + 86_400_000).toISOString() })
beforeEach(() => {
  for (const value of Object.values(mocks)) if (typeof value === 'function') value.mockReset()
  mocks.locale.value = 'zh'; localStorage.clear(); sessionStorage.clear()
  vi.stubGlobal('IntersectionObserver', undefined)
  mocks.getOptions.mockResolvedValue({ generated_at: new Date().toISOString() })
  mocks.listTasks.mockResolvedValue({ items: [task(), task('task-2')], total: 2, page: 1, size: 12 })
  mocks.getTask.mockImplementation(async (id: string) => task(id))
  mocks.getResults.mockImplementation(async (id: string) => ({ items: [result(id === 'task-1' ? 'r1' : 'r2', id), result('success', id, 'complete')], total: 2, page: 1, page_size: 24 }))
  mocks.getResult.mockImplementation(async (id: string, resultID: string) => ({ ...result(resultID, id), raw_response: 'Full raw response', raw_answer: 'Full raw answer', raw_html: '', html: '', error: 'Full administrator error', preview_unavailable: 'No HTML' }))
  mocks.observeTask.mockImplementation((_id: string, _event: unknown, signal: AbortSignal) => new Promise<void>(resolve => signal.addEventListener('abort', () => resolve(), { once: true })))
  mocks.cancelTask.mockResolvedValue(task('task-1', 'cancelled'))
})
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.useRealTimers(); vi.unstubAllGlobals() })
async function make() {
  const wrapper = mount(CodexPelicanComparisonView, { global: { stubs: {
    AppLayout: { template: '<div><slot /></div>' },
    BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' },
    PelicanTaskComposer: { props: ['initial', 'submitting'], emits: ['submit'], template: '<div data-test="composer-stub" />' },
    BorrowPelicanPreview: true
  } } }); wrappers.push(wrapper); await flushPromises(); return wrapper
}
describe('Pelican background workbench', () => {
  it('opens task summaries without enumerating accounts or starting models', async () => {
    const wrapper = await make()
    expect(mocks.listTasks).toHaveBeenCalledOnce(); expect(mocks.listAccounts).not.toHaveBeenCalled()
    await wrapper.get('[data-test="pelican-task-task-1"]').trigger('click'); await flushPromises()
    expect(mocks.getResults).toHaveBeenCalledWith('task-1', expect.objectContaining({ page: 1, page_size: 24 }), expect.any(AbortSignal))
    expect(mocks.getResult).not.toHaveBeenCalled(); expect(mocks.startTask).not.toHaveBeenCalled()
    expect(wrapper.get('[data-test="pelican-active-task"]').text()).toContain('已处理 3 / 3')
    await wrapper.get('[data-test="pelican-result-r1"] button').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('Full administrator error'); expect(wrapper.text()).toContain('Full raw response')
    expect(mocks.startTask).not.toHaveBeenCalled()
  })
  it('reconnects observation without submitting and unmount only disconnects', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] })
    mocks.getTask.mockResolvedValue(task('task-1', 'running'))
    mocks.observeTask.mockRejectedValueOnce(new Error('disconnected'))
    const wrapper = await make()
    await wrapper.get('[data-test="pelican-task-task-1"]').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('正在重连')
    await vi.advanceTimersByTimeAsync(1000); await flushPromises()
    expect(mocks.observeTask).toHaveBeenCalledTimes(2); expect(mocks.startTask).not.toHaveBeenCalled()
    const signal = mocks.observeTask.mock.calls[1][2] as AbortSignal
    wrapper.unmount(); expect(signal.aborted).toBe(true); expect(mocks.cancelTask).not.toHaveBeenCalled()
  })
  it('cancels only through the explicit stop button', async () => {
    mocks.getTask.mockResolvedValue(task('task-1', 'running'))
    const wrapper = await make(); await wrapper.get('[data-test="pelican-task-task-1"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="pelican-test-stop"]').trigger('click'); await flushPromises()
    expect(mocks.cancelTask).toHaveBeenCalledOnce(); expect(mocks.cancelTask).toHaveBeenCalledWith('task-1'); expect(mocks.startTask).not.toHaveBeenCalled()
  })
  it('copies only failed parameters for a new reviewed task and preserves old successes', async () => {
    const wrapper = await make(); await wrapper.get('[data-test="pelican-task-task-1"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="pelican-retry-failed"]').trigger('click'); await flushPromises()
    expect(wrapper.getComponent(PelicanTaskComposer).props('initial').targets).toEqual([{ account_id: 1, model_id: 'gpt-6.1-sol', effort: '' }])
    expect(mocks.startTask).not.toHaveBeenCalled()
    mocks.startTask.mockResolvedValue(task('new-task'))
    wrapper.getComponent(PelicanTaskComposer).vm.$emit('submit', { generation_timeout_seconds: 600, targets: [{ account_id: 1, model_id: 'gpt-6.1-sol', effort: '' }] })
    await flushPromises(); expect(mocks.startTask).toHaveBeenCalledOnce()
    expect(mocks.startTask.mock.calls[0][0].client_task_id).not.toBe('client')
  })
  it('compares saved results across batches without calling models', async () => {
    const wrapper = await make(); await wrapper.get('[data-test="pelican-task-task-1"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="pelican-result-r1"] input').setValue(true)
    await wrapper.get('[data-test="pelican-task-task-2"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="pelican-result-r2"] input').setValue(true)
    await wrapper.get('[data-test="pelican-compare"]').trigger('click'); await flushPromises()
    expect(mocks.getResult).toHaveBeenCalledWith('task-1', 'r1', expect.any(AbortSignal))
    expect(mocks.getResult).toHaveBeenCalledWith('task-2', 'r2', expect.any(AbortSignal))
    expect(mocks.startTask).not.toHaveBeenCalled()
  })
})
