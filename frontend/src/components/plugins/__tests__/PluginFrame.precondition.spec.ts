import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { reactive } from 'vue'
import PluginFrame from '../PluginFrame.vue'

const calls = vi.hoisted(() => ({ load: vi.fn(), save: vi.fn(), test: vi.fn(), invoke: vi.fn(), submit: vi.fn(), session: vi.fn(), job: vi.fn() }))
const registry = reactive({ loaded: true, items: [] as Array<{ plugin_id: number; id: string; package_sha256: string; available: boolean }>, refresh: vi.fn() })
vi.mock('@/api/admin', () => ({ adminAPI: { plugins: { getConfig: calls.load, saveConfig: calls.save, test: calls.test, invokeAdmin: calls.invoke, submitJob: calls.submit, createUISession: calls.session } } }))
vi.mock('@/api/admin/accountJobs', () => ({ default: { get: calls.job } }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showError: vi.fn(), showInfo: vi.fn(), showSuccess: vi.fn(), showWarning: vi.fn() }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { id: 1 } }) }))
vi.mock('@/stores/pluginExtensions', () => ({ usePluginExtensions: () => registry }))
vi.mock('@/composables/useStepUp', () => ({ isStepUpCancelled: () => false, useStepUp: () => ({ run: (execute: () => unknown) => execute() }) }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))

const digest = 'a'.repeat(64)
let wrapper: VueWrapper | undefined
async function setup(context: Record<string, unknown> = {}) {
  wrapper = mount(PluginFrame, { attachTo: document.body, props: { pluginId: 7, title: 'configuration', permission: 'admin', context, admission: { allowed: true } }, global: { stubs: { TotpStepUpDialog: true } } })
  await flushPromises()
  const target = (wrapper.get('iframe').element as HTMLIFrameElement).contentWindow!
  const posted = vi.spyOn(target, 'postMessage').mockImplementation(() => {})
  const send = async (type: string, request_id: string, fields: Record<string, unknown> = {}) => {
    window.dispatchEvent(new MessageEvent('message', { origin: 'null', source: target, data: { source: 'sub2api-plugin-ui', bridge_token: 'fixture', type, request_id, ...fields } }))
    await flushPromises()
  }
  return { send, posted }
}
beforeEach(() => {
  vi.clearAllMocks()
  registry.items = []
  calls.session.mockResolvedValue({ url: 'about:blank', bridge_token: 'fixture', permission: 'admin', package_sha256: digest })
  calls.load.mockResolvedValue({ config: { value: 'initial' }, revision: 4, package_sha256: digest })
  calls.job.mockResolvedValue({ id: 9, metadata: { plugin_id: 7 } })
})
afterEach(() => { wrapper?.unmount(); wrapper = undefined; vi.restoreAllMocks() })

describe('plugin host-owned read-time preconditions', () => {
  it('keeps the draft and read version on conflict; only an explicit load refreshes it', async () => {
    const { send, posted } = await setup({ mode: 'configuration' })
    const draft = { value: 'my unsaved edit', nested: { preserved: true } }
    await send('config.load', 'load')
    calls.save.mockRejectedValue({ status: 409, reason: 'PLUGIN_STATE_CHANGED', message: 'Plugin changed; reload explicitly' })
    calls.load.mockResolvedValue({ config: { value: 'other administrator' }, revision: 5, package_sha256: digest })
    await send('config.save', 'save', { config: draft, revision: 900, package_sha256: 'b'.repeat(64) })
    expect(calls.save).toHaveBeenLastCalledWith(7, draft, { revision: 4, package_sha256: digest })
    expect(calls.load).toHaveBeenCalledTimes(1)
    expect(draft).toEqual({ value: 'my unsaved edit', nested: { preserved: true } })
    expect(posted).toHaveBeenCalledWith(expect.objectContaining({ request_id: 'save', ok: false, status: 409 }), '*')
    await send('config.save', 'retry', { config: draft })
    expect(calls.save).toHaveBeenLastCalledWith(7, draft, { revision: 4, package_sha256: digest })
    expect(calls.load).toHaveBeenCalledTimes(1)
    await send('config.load', 'explicit-reload')
    calls.save.mockResolvedValue({ config: { value: 'canonical' }, revision: 6, package_sha256: digest })
    await send('config.save', 'after-reload', { config: draft })
    expect(calls.save).toHaveBeenLastCalledWith(7, draft, { revision: 5, package_sha256: digest })
    calls.save.mockResolvedValue({ config: draft, revision: 7, package_sha256: digest })
    await send('config.save', 'next-save', { config: draft })
    expect(calls.save).toHaveBeenLastCalledWith(7, draft, { revision: 6, package_sha256: digest })
  })

  it('never fetches a fresh version to bless a save or test before load', async () => {
    const { send, posted } = await setup()
    await send('config.save', 'save-before-load', { config: { changed: true } })
    await send('config.test', 'test-before-load')
    expect(calls.load).not.toHaveBeenCalled()
    expect(calls.save).not.toHaveBeenCalled()
    expect(calls.test).not.toHaveBeenCalled()
    expect(posted).toHaveBeenCalledWith(expect.objectContaining({ request_id: 'save-before-load', ok: false }), '*')
  })

  it('blocks old-package business actions and jobs but leaves owned history open', async () => {
    registry.items = [{ plugin_id: 7, id: 'actions', package_sha256: 'b'.repeat(64), available: true }]
    const { send, posted } = await setup({ contribution_id: 'actions', account_id: 3 })
    await send('extension.invoke', 'action', { operation: 'harvest', payload: {} })
    await send('extension.job.submit', 'job', { operation: 'harvest', items: [{ account_id: 3, payload: {} }] })
    await send('extension.job.get', 'history', { job_id: 9 })
    expect(calls.invoke).not.toHaveBeenCalled()
    expect(calls.submit).not.toHaveBeenCalled()
    expect(calls.job).toHaveBeenCalledWith(9)
    expect(posted).toHaveBeenCalledWith(expect.objectContaining({ request_id: 'history', ok: true }), '*')
  })

  it('sends the session package even before the contribution registry notices an update', async () => {
    calls.invoke.mockResolvedValue({ payload: { success: true } })
    const { send } = await setup({ account_id: 3 })
    await send('extension.invoke', 'action', { operation: 'harvest', payload: { input: true }, package_sha256: 'b'.repeat(64) })
    expect(calls.invoke).toHaveBeenCalledWith(7, 'harvest', 3, { input: true }, digest)
  })
})
