import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { reactive } from 'vue'
import PluginFrame from '../PluginFrame.vue'

const calls = vi.hoisted(() => ({ resources: vi.fn(), session: vi.fn(), execute: vi.fn() }))
const registry = reactive({ loaded: true, items: [] as Array<{ plugin_id: number; id: string; available: boolean }>, refresh: vi.fn() })
vi.mock('@/api/admin', () => ({ adminAPI: { plugins: { resources: calls.resources, createUISession: calls.session, createUserUISession: calls.session } } }))
vi.mock('@/api/admin/accountJobs', () => ({ default: {} }))
vi.mock('../resourceClient', () => ({ callPluginResource: vi.fn() }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showError: vi.fn(), showInfo: vi.fn(), showSuccess: vi.fn(), showWarning: vi.fn() }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { id: 1 } }) }))
vi.mock('@/stores/pluginExtensions', () => ({ usePluginExtensions: () => registry }))
vi.mock('@/composables/useStepUp', () => ({ isStepUpCancelled: () => false, useStepUp: () => ({ run: calls.execute }) }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })

describe('resource availability bridge', () => {
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
    calls.session.mockResolvedValue({ url: 'about:blank', bridge_token: 'fixture-token', permission: 'user', package_sha256: 'fixture-package' })
    calls.resources.mockReset().mockResolvedValue([{ name: 'user.history', available: true, method: 'LOCAL', path: '/host-private', permission: 'user' }])
    const wrapper = mount(PluginFrame, { attachTo: document.body, props: { pluginId: 7, title: 'fixture', permission: 'user' }, global: { stubs: { TotpStepUpDialog: true } } })
    await flushPromises()
    const target = (wrapper.get('iframe').element as HTMLIFrameElement).contentWindow!
    const posted = vi.spyOn(target, 'postMessage').mockImplementation(() => {})
    const send = (id: string) => window.dispatchEvent(new MessageEvent('message', { origin: 'null', source: target, data: { source: 'sub2api-plugin-ui', bridge_token: 'fixture-token', type: 'extension.resources', request_id: id } }))
    send('one')
    await flushPromises()
    expect(calls.resources).toHaveBeenLastCalledWith(7, 'user')
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
