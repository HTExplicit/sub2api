import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import CodexPelicanComparisonView from '../CodexPelicanComparisonView.vue'
import PelicanTaskComposer from '@/components/admin/codex/PelicanTaskComposer.vue'
import Pagination from '@/components/common/Pagination.vue'
import type { PelicanResultSummary, PelicanTaskSnapshot } from '@/api/admin/pelicanTests'

const mocks = vi.hoisted(() => ({ getOptions: vi.fn(), getAccountOptions: vi.fn(), listTasks: vi.fn(), getTask: vi.fn(), getResults: vi.fn(), getResult: vi.fn(), startTask: vi.fn(), cancelTask: vi.fn(), deleteTask: vi.fn(), deleteResult: vi.fn(), observeTask: vi.fn(), listAccounts: vi.fn(), locale: { value: 'zh' } }))
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
  mocks.startTask.mockResolvedValue(task('new-task'))
  mocks.deleteTask.mockResolvedValue({ deleted: true })
  mocks.deleteResult.mockResolvedValue({ deleted: true })
})
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.useRealTimers(); vi.unstubAllGlobals() })
async function make() {
  const wrapper = mount(CodexPelicanComparisonView, { global: { stubs: {
    AppLayout: { template: '<div><slot /></div>' },
    BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' },
    PelicanTaskComposer: { props: ['initial', 'submitting'], emits: ['submit'], template: '<div data-test="composer-stub" />' },
    PelicanPreview: true
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
  it('retests failed parameters directly while preserving the original task', async () => {
    const wrapper = await make(); await wrapper.get('[data-test="pelican-task-task-1"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="pelican-retry-failed"]').trigger('click'); await flushPromises()
    expect(wrapper.findComponent(PelicanTaskComposer).exists()).toBe(false)
    expect(mocks.startTask).toHaveBeenCalledOnce()
    expect(mocks.startTask.mock.calls[0][0].targets).toEqual([{ account_id: 1, model_id: 'gpt-6.1-sol', effort: '' }])
    expect(mocks.startTask.mock.calls[0][0].client_task_id).not.toBe('client')
  })
  it('retests all pages independently of the visible filter', async () => {
    const wrapper = await make(); await wrapper.get('[data-test="pelican-task-task-1"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="pelican-result-filter"]').setValue('failed'); await new Promise(resolve => setTimeout(resolve, 260)); await flushPromises()
    const all = Array.from({ length: 51 }, (_, index) => ({ ...result(`row-${index}`), account_id: index + 1, effort: 'high' }))
    mocks.getResults.mockImplementation(async (id: string, filters: { page: number; page_size: number }) => filters.page_size === 50
      ? { items: filters.page === 1 ? all.slice(0, 50) : all.slice(50), total: 51, page: filters.page, page_size: 50 }
      : { items: [], total: 0, page: 1, page_size: 24 })
    await wrapper.get('[data-test="pelican-retest-task"]').trigger('click'); await flushPromises()
    expect(mocks.startTask).toHaveBeenCalledOnce()
    expect(mocks.startTask.mock.calls[0][0].targets).toHaveLength(51)
    expect(mocks.startTask.mock.calls[0][0].targets[50]).toEqual({ account_id: 51, model_id: 'gpt-6.1-sol', effort: 'high' })
    expect(mocks.deleteTask).not.toHaveBeenCalled()
  })
  it('retests a single work with its source budget and blocks duplicate submissions', async () => {
    mocks.getTask.mockImplementation(async (id: string) => ({ ...task(id), generation_timeout_seconds: 1200 }))
    let complete!: (value: PelicanTaskSnapshot) => void
    mocks.startTask.mockImplementation(() => new Promise(resolve => { complete = resolve }))
    const wrapper = await make(); await wrapper.get('[data-test="pelican-task-task-1"]').trigger('click'); await flushPromises()
    const button = wrapper.get('[data-test="pelican-result-r1"] [data-test="pelican-retest-result"]')
    await button.trigger('click'); await flushPromises(); await button.trigger('click'); await flushPromises()
    expect(mocks.startTask).toHaveBeenCalledOnce()
    expect(mocks.startTask.mock.calls[0][0]).toMatchObject({ generation_timeout_seconds: 1200, targets: [{ account_id: 1, model_id: 'gpt-6.1-sol', effort: '' }] })
    complete(task('new-task')); await flushPromises()
  })
  it('retries selected failures from different tasks with each original budget', async () => {
    mocks.getTask.mockImplementation(async (id: string) => ({ ...task(id), generation_timeout_seconds: id === 'task-1' ? 300 : 1200 }))
    const wrapper = await make(); await wrapper.get('[data-test="pelican-task-task-1"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="pelican-result-r1"] input').setValue(true)
    await wrapper.get('[data-test="pelican-task-task-2"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="pelican-result-r2"] input').setValue(true)
    await wrapper.get('[data-test="pelican-retry-selected"]').trigger('click'); await flushPromises()
    expect(mocks.startTask.mock.calls.map(([request]) => request.generation_timeout_seconds)).toEqual([300, 1200])
    expect(new Set(mocks.startTask.mock.calls.map(([request]) => request.client_task_id)).size).toBe(2)
  })
  it('confirms single deletion and clears its comparison and open detail', async () => {
    const wrapper = await make(); await wrapper.get('[data-test="pelican-task-task-1"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="pelican-result-r1"] input').setValue(true)
    await wrapper.get('[data-test="pelican-result-r1"] button').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="pelican-result-r1"] [data-test="pelican-delete-result"]').trigger('click')
    expect(mocks.deleteResult).not.toHaveBeenCalled()
    mocks.getResults.mockResolvedValue({ items: [result('success', 'task-1', 'complete')], total: 1, page: 1, page_size: 24 })
    mocks.getTask.mockResolvedValue({ ...task(), total: 1, completed: 1, counts: { complete: 1 } })
    await wrapper.get('[data-test="pelican-confirm-delete"]').trigger('click'); await flushPromises()
    expect(mocks.deleteResult).toHaveBeenCalledWith('task-1', 'r1')
    expect(wrapper.find('[data-test="pelican-comparison-tray"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="pelican-detail-content"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="pelican-result-r1"]').exists()).toBe(false)
    expect(mocks.startTask).not.toHaveBeenCalled()
  })
  it('selects the next task after removing the last work', async () => {
    let deleted = false
    mocks.deleteResult.mockImplementation(async () => { deleted = true; return { deleted: true } })
    mocks.getTask.mockImplementation(async (id: string) => {
      if (deleted && id === 'task-1') throw Object.assign(new Error('deleted'), { isAxiosError: true, response: { status: 410 } })
      return task(id)
    })
    mocks.listTasks.mockImplementation(async () => ({ items: deleted ? [task('task-2')] : [task(), task('task-2')], total: deleted ? 1 : 2, page: 1, size: 12 }))
    const wrapper = await make(); await wrapper.get('[data-test="pelican-task-task-1"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="pelican-result-r1"] [data-test="pelican-delete-result"]').trigger('click')
    await wrapper.get('[data-test="pelican-confirm-delete"]').trigger('click'); await flushPromises()
    expect(wrapper.get('[data-test="pelican-active-task"]').text()).toContain('task-2')
    expect(wrapper.find('[data-test="pelican-task-task-1"]').exists()).toBe(false)
  })
  it('disables every delete action while the parent task is running', async () => {
    mocks.getTask.mockResolvedValue(task('task-1', 'running'))
    const wrapper = await make(); await wrapper.get('[data-test="pelican-task-task-1"]').trigger('click'); await flushPromises()
    expect(wrapper.get<HTMLButtonElement>('[data-test="pelican-delete-task"]').element.disabled).toBe(true)
    expect(wrapper.findAll<HTMLButtonElement>('[data-test="pelican-delete-result"]').every(button => button.element.disabled)).toBe(true)
    expect(wrapper.get<HTMLButtonElement>('[data-test="pelican-retest-task"]').element.disabled).toBe(true)
  })
  it('reuses the same retest identity after an uncertain submission', async () => {
    mocks.startTask.mockRejectedValueOnce(new Error('connection lost')).mockResolvedValueOnce(task('new-task'))
    const wrapper = await make(); await wrapper.get('[data-test="pelican-task-task-1"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="pelican-retest-task"]').trigger('click'); await flushPromises()
    const original = mocks.startTask.mock.calls[0][0]
    expect(JSON.parse(sessionStorage.getItem('pelican.pending-task.v1')!)).toEqual(original)
    const retry = wrapper.findAll('button').find(button => button.text() === '重试同一提交')!
    await retry.trigger('click'); await flushPromises()
    expect(mocks.startTask.mock.calls[1][0]).toEqual(original)
    expect(sessionStorage.getItem('pelican.pending-task.v1')).toBeNull()
  })
  it('moves back to the remaining result page after deleting the last item on page two', async () => {
    let deleted = false
    const remaining = Array.from({ length: 24 }, (_, index) => result(`remaining-${index}`))
    mocks.getResults.mockImplementation(async (_id: string, filters: { page: number }) => ({ items: filters.page === 2 ? deleted ? [] : [result('last')] : remaining, total: deleted ? 24 : 25, page: filters.page, page_size: 24 }))
    mocks.deleteResult.mockImplementation(async () => { deleted = true; return { deleted: true } })
    const wrapper = await make(); await wrapper.get('[data-test="pelican-task-task-1"]').trigger('click'); await flushPromises()
    wrapper.getComponent(Pagination).vm.$emit('update:page', 2); await flushPromises()
    await wrapper.get('[data-test="pelican-result-last"] [data-test="pelican-delete-result"]').trigger('click')
    await wrapper.get('[data-test="pelican-confirm-delete"]').trigger('click'); await flushPromises()
    expect(wrapper.getComponent(Pagination).props('page')).toBe(1)
    expect(wrapper.find('[data-test="pelican-result-remaining-0"]').exists()).toBe(true)
  })
  it('deletes the whole task and shows the empty state when no task remains', async () => {
    mocks.listTasks.mockResolvedValueOnce({ items: [task()], total: 1, page: 1, size: 12 }).mockResolvedValue({ items: [], total: 0, page: 1, size: 12 })
    const wrapper = await make(); await wrapper.get('[data-test="pelican-task-task-1"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="pelican-result-r1"] input').setValue(true)
    await wrapper.get('[data-test="pelican-delete-task"]').trigger('click')
    await wrapper.get('[data-test="pelican-confirm-delete"]').trigger('click'); await flushPromises()
    expect(mocks.deleteTask).toHaveBeenCalledWith('task-1')
    expect(wrapper.find('[data-test="pelican-active-task"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="pelican-comparison-tray"]').exists()).toBe(false)
    expect(sessionStorage.getItem('pelican.active-task.v1')).toBeNull()
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
