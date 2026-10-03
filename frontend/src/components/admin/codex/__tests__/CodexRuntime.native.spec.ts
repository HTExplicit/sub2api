import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import CodexRuntimeSettings from '../CodexRuntimeSettings.vue'
import CodexContinuationDiagnostics from '../CodexContinuationDiagnostics.vue'

const { get, put, stepUpRun } = vi.hoisted(() => ({
  get: vi.fn(), put: vi.fn(),
  stepUpRun: vi.fn(async (action: () => Promise<unknown>) => action())
}))
vi.mock('@/api/client', () => ({ apiClient: { get, put } }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { id: 1 } }) }))
vi.mock('@/composables/useStepUp', () => ({ useStepUp: () => ({ run: stepUpRun }), isStepUpCancelled: () => false }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ locale: { value: 'en' }, t: (key: string) => key })
}))

const wrappers: VueWrapper[] = []
const global = { stubs: { TotpStepUpDialog: true } }
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(done => { resolve = done })
  return { promise, resolve }
}

beforeEach(() => {
  vi.clearAllMocks()
  get.mockReset().mockResolvedValue({ data: { request_zstd: true } })
  put.mockReset().mockImplementation(async (_url: string, value: unknown) => ({ data: value }))
})
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()) })

describe('native Codex controls', () => {
  it('reads request compression and saves request_zstd through step-up', async () => {
    get.mockResolvedValue({ data: { request_zstd: false } })
    const wrapper = mount(CodexRuntimeSettings, { global }); wrappers.push(wrapper)
    await flushPromises()
    expect(get).toHaveBeenCalledWith('/admin/settings/codex-runtime')
    expect(wrapper.get('h2').text()).toBe('Codex runtime settings')
    expect((wrapper.get('[data-test="codex-compression"]').element as HTMLInputElement).checked).toBe(false)
    await wrapper.get('[data-test="codex-compression"]').setValue(true)
    await wrapper.get('[data-test="codex-save"]').trigger('click')
    await flushPromises()
    expect(put).toHaveBeenCalledTimes(1)
    expect(put).toHaveBeenCalledWith('/admin/settings/codex-runtime', { request_zstd: true })
    expect(stepUpRun).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[role="status"]').text()).toContain('Settings saved')
  })

  it('does not report a save that finishes after the checkbox changed again', async () => {
    const pendingSave = deferred<{ data: unknown }>()
    put.mockReturnValueOnce(pendingSave.promise)
    const wrapper = mount(CodexRuntimeSettings, { global }); wrappers.push(wrapper)
    await flushPromises()
    expect((wrapper.get('[data-test="codex-compression"]').element as HTMLInputElement).checked).toBe(true)
    await wrapper.get('[data-test="codex-save"]').trigger('click')
    await flushPromises()
    expect(put).toHaveBeenCalledWith('/admin/settings/codex-runtime', { request_zstd: true })
    await wrapper.get('[data-test="codex-compression"]').setValue(false)
    pendingSave.resolve({ data: { request_zstd: true } })
    await flushPromises()
    expect((wrapper.get('[data-test="codex-compression"]').element as HTMLInputElement).checked).toBe(false)
    expect(wrapper.text()).not.toContain('Settings saved')
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
