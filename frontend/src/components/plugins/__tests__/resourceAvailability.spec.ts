import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { reactive } from 'vue'
import PluginFrame from '../PluginFrame.vue'
import type { PluginContribution } from '@/api/admin/plugins'
import { captureAccountView } from '@/composables/useAccountViewContext'
import { viewIdentity } from '../accountView'
import { viewContributions } from './accountView.fixtures'

const calls = vi.hoisted(() => ({ resources: vi.fn(), resource: vi.fn(), session: vi.fn(), execute: vi.fn(), invoke: vi.fn(), submit: vi.fn(), job: vi.fn() }))
const registry = reactive({ loaded: true, items: [] as PluginContribution[], refresh: vi.fn() })
vi.mock('@/api/admin', () => ({ adminAPI: { plugins: { resources: calls.resources, createUISession: calls.session, createUserUISession: calls.session, invokeAdmin: calls.invoke, submitJob: calls.submit } } }))
vi.mock('@/api/admin/accountJobs', () => ({ default: { get: calls.job } }))
vi.mock('../resourceClient', () => ({ callPluginResource: calls.resource }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showError: vi.fn(), showInfo: vi.fn(), showSuccess: vi.fn(), showWarning: vi.fn() }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { id: 1 } }) }))
vi.mock('@/stores/pluginExtensions', () => ({ usePluginExtensions: () => registry }))
vi.mock('@/composables/useStepUp', () => ({ isStepUpCancelled: () => false, useStepUp: () => ({ run: calls.execute }) }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })

describe('resource availability bridge', () => {
  it('keeps a mounted view frame and its retained controls after the disabled owner disappears', async () => {
    registry.items = viewContributions()
    const view = registry.items.find(item => item.slot === 'account.view.v1')!
    const origin = captureAccountView({ contribution: view, presetID: 'cindy', query: {}, actorID: 1,
      currentActor: () => 1, currentItems: () => registry.items })
    calls.session.mockReset().mockResolvedValue({ url: 'about:blank', bridge_token: 'retained-token', permission: 'admin',
      package_sha256: view.package_sha256, expires_at: '2099-01-01T00:00:00Z', request_binding: 'retained-pin', view_context: viewIdentity(view, 'cindy') })
    calls.resources.mockReset().mockResolvedValue([
      { name: 'cindy.probe.job.get', available: true, retained: true, permission: 'admin', method: 'GET', path: '/api/v1/admin/cindy-balance-probe/jobs/:id' },
      { name: 'cindy.probe.submit', available: false, permission: 'admin', method: 'POST', path: '/api/v1/admin/cindy-balance-probe/jobs' }
    ])
    calls.resource.mockReset().mockResolvedValue({ id: 72, status: 'running' })
    calls.execute.mockImplementation((execute: () => unknown) => execute())
    const wrapper = mount(PluginFrame, { attachTo: document.body, props: { pluginId: 7, title: 'probe', originView: origin,
      expectedPackage: view.package_sha256, context: { contribution_id: 'cindy-balance-probe' } }, global: { stubs: { TotpStepUpDialog: true } } })
    await flushPromises()
    const iframe = wrapper.get('iframe').element as HTMLIFrameElement
    const target = iframe.contentWindow!
    const posted = vi.spyOn(target, 'postMessage').mockImplementation(() => {})
    const send = async (type: string, request_id: string, fields: Record<string, unknown> = {}) => {
      window.dispatchEvent(new MessageEvent('message', { origin: 'null', source: target,
        data: { source: 'sub2api-plugin-ui', bridge_token: 'retained-token', type, request_id, ...fields } }))
      await flushPromises()
    }
    registry.items = []; await flushPromises()
    await send('extension.context', 'context')
    expect(posted).toHaveBeenCalledWith(expect.objectContaining({ request_id: 'context', context: expect.objectContaining({ available: false, retained_controls: true }) }), '*')
    await send('extension.resource', 'existing-result', { operation: 'cindy.probe.job.get', input: { params: { id: 72 } } })
    expect(calls.resource).toHaveBeenCalledWith(7, view.package_sha256, expect.objectContaining({ retained: true }), expect.anything(), expect.anything(), 1,
      expect.objectContaining({ requestBinding: 'retained-pin', retained: true, view: origin }))
    await send('extension.resource', 'new-work', { operation: 'cindy.probe.submit', input: { body: {} } })
    expect(calls.resource).toHaveBeenCalledTimes(1)
    expect(calls.session).toHaveBeenCalledTimes(1)
    expect(wrapper.get('iframe').element).toBe(iframe)
    wrapper.unmount()
  })
  it('blocks out-of-scope business submission while retaining owned job history', async () => {
    calls.invoke.mockReset()
    calls.submit.mockReset()
    calls.job.mockReset().mockResolvedValue({ id: 9, metadata: { plugin_id: 7 } })
    calls.session.mockResolvedValue({ url: 'about:blank', bridge_token: 'scope-token', permission: 'admin', package_sha256: 'fixture-package', expires_at: '2099-01-01T00:00:00Z' })
    const wrapper = mount(PluginFrame, { attachTo: document.body, props: { pluginId: 7, title: 'scope', permission: 'admin', context: { account_id: 3 }, admission: { allowed: false, reason: 'outside_scope' } }, global: { stubs: { TotpStepUpDialog: true } } })
    await flushPromises()
    const target = (wrapper.get('iframe').element as HTMLIFrameElement).contentWindow!
    const posted = vi.spyOn(target, 'postMessage').mockImplementation(() => {})
    const send = (type: string, request_id: string, fields: Record<string, unknown>) => window.dispatchEvent(new MessageEvent('message', { origin: 'null', source: target, data: { source: 'sub2api-plugin-ui', bridge_token: 'scope-token', type, request_id, ...fields } }))
    send('extension.invoke', 'invoke', { operation: 'harvest', payload: {} })
    send('extension.job.submit', 'submit', { operation: 'harvest', items: [{ account_id: 3, payload: {} }] })
    send('extension.job.get', 'history', { job_id: 9 })
    await flushPromises()
    expect(calls.invoke).not.toHaveBeenCalled()
    expect(calls.submit).not.toHaveBeenCalled()
    expect(posted).toHaveBeenCalledWith(expect.objectContaining({ request_id: 'invoke', ok: false }), '*')
    expect(posted).toHaveBeenCalledWith(expect.objectContaining({ request_id: 'submit', ok: false }), '*')
    expect(calls.job).toHaveBeenCalledWith(9)
    expect(posted).toHaveBeenCalledWith(expect.objectContaining({ request_id: 'history', ok: true }), '*')
    wrapper.unmount()
  })

  it('exposes the SDK read-only query without host paths and rejects malformed availability', async () => {
    const { resourceAvailability } = await import('@sub2api/plugin-ui/client')
    const request = vi.fn().mockResolvedValue({ resources: [{ name: 'taxonomy.tags.delete', available: false, path: '/host-private' }] })
    vi.stubGlobal('Sub2APIPluginBridge', class { request = request })
    expect(await resourceAvailability()).toEqual([{ name: 'taxonomy.tags.delete', available: false }])
    expect(request).toHaveBeenCalledWith('extension.resources')
    request.mockResolvedValueOnce({ resources: [{ name: 'taxonomy.tags.delete', available: 'yes' }] })
    await expect(resourceAvailability()).rejects.toThrow('Invalid resource availability')
  })

  it('returns only named availability from the session role catalog and refreshes after context changes', async () => {
    calls.session.mockResolvedValue({ url: 'about:blank', bridge_token: 'fixture-token', permission: 'user', package_sha256: 'fixture-package', expires_at: '2099-01-01T00:00:00Z' })
    calls.resources.mockReset().mockResolvedValue([{ name: 'user.history', available: true, method: 'LOCAL', path: '/host-private', permission: 'user' }])
    const wrapper = mount(PluginFrame, { attachTo: document.body, props: { pluginId: 7, title: 'fixture', permission: 'user' }, global: { stubs: { TotpStepUpDialog: true } } })
    await flushPromises()
    const target = (wrapper.get('iframe').element as HTMLIFrameElement).contentWindow!
    const posted = vi.spyOn(target, 'postMessage').mockImplementation(() => {})
    const send = (id: string) => window.dispatchEvent(new MessageEvent('message', { origin: 'null', source: target, data: { source: 'sub2api-plugin-ui', bridge_token: 'fixture-token', type: 'extension.resources', request_id: id } }))
    send('one')
    await flushPromises()
    expect(calls.resources).toHaveBeenLastCalledWith(7, 'user', expect.objectContaining({ retained: true }))
    expect(posted).toHaveBeenCalledWith(expect.objectContaining({ type: 'extension.resources.result', ok: true, resources: [{ name: 'user.history', available: true }] }), '*')
    const first = posted.mock.calls.find(([message]) => message.type === 'extension.resources.result')![0]
    expect(first.resources[0]).not.toHaveProperty('path')
    expect(first.resources[0]).not.toHaveProperty('method')
    expect(calls.execute).not.toHaveBeenCalled()
    calls.resources.mockResolvedValueOnce([{ name: 'user.history', available: false, path: '/host-private' }])
    registry.items = [...registry.items]
    await flushPromises()
    expect(posted).toHaveBeenCalledWith(expect.objectContaining({ type: 'extension.context.updated' }), '*')
    send('two')
    await flushPromises()
    expect(calls.resources).toHaveBeenCalledTimes(2)
    expect(posted).toHaveBeenCalledWith(expect.objectContaining({ request_id: 'two', resources: [{ name: 'user.history', available: false }] }), '*')
    wrapper.unmount()
  })
})
