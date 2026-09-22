import { describe, expect, it, vi } from 'vitest'
import { createPluginViewDispatch } from '../pluginViewDispatch'
import { captureAccountView } from '@/composables/useAccountViewContext'
import { viewIdentity } from '../accountView'
import { cindyView, viewContributions } from './accountView.fixtures'
import type { PluginUISession } from '@/api/admin/plugins'

function fixture() {
  const items = viewContributions()
  const view = items.find(item => item.slot === 'account.view.v1')!
  let actor = 41, revision = 1, live = true
  const makeScope = (presetID: string) => captureAccountView({ contribution: view, presetID, query: { search: `query-${presetID}` },
    actorID: actor, currentActor: () => actor, currentItems: () => items })
  let origin = makeScope('insufficient')
  const session = (scope = origin): PluginUISession => ({ url: 'about:blank', bridge_token: 'bridge-only',
    package_sha256: 'c'.repeat(64), plugin_key: 'codexrip.account-tools', permission: 'admin', ui_bridge_version: 1,
    expires_at: '2099-01-01T00:00:00Z', request_binding: `pin-${scope.context.preset_id}`, view_context: viewIdentity(view, scope.context.preset_id) })
  const createSession = vi.fn(async (scope = origin) => session(scope))
  const broker = createPluginViewDispatch({ origin: () => origin, revision: () => String(revision),
    assertFrame: retained => { if (!live && !retained) throw new Error('disabled') }, createSession })
  broker.prime(session(), origin)
  return { broker, createSession, session, items, switchPreset() { origin = makeScope('banned'); revision++ },
    disable() { live = false; view.available = false }, actor(value: number) { actor = value } }
}

describe('trusted view request dispatch', () => {
  it('keeps a captured operation on its original preset without replacing the static iframe session', async () => {
    const f = fixture(), staticSession = f.session()
    const first = await f.broker.acquire('confirmation')
    first.release(); f.switchPreset()
    const second = await f.broker.acquire('confirmation')
    expect(second.dispatch.view?.context.preset_id).toBe('insufficient')
    expect(second.dispatch.requestBinding).toBe('pin-insufficient')
    expect(f.createSession).not.toHaveBeenCalled()
    second.release()
    const next = await f.broker.acquire('new-confirmation')
    expect(next.dispatch.view?.context.preset_id).toBe('banned')
    expect(next.dispatch.requestBinding).toBe('pin-banned')
    expect(staticSession.bridge_token).toBe('bridge-only')
    expect(staticSession.view_context?.preset_id).toBe('insufficient')
    next.release()
  })
  it('drops late ambient replies and allows only an existing retained pin after disable', async () => {
    const f = fixture()
    const read = await f.broker.acquire()
    f.switchPreset()
    expect(read.canPublish()).toBe(false)
    read.release()
    const ready = await f.broker.acquire('known')
    ready.release(); f.disable()
    await expect(f.broker.acquire('new')).rejects.toThrow()
    const retained = await f.broker.acquire('known', true)
    expect(retained.dispatch.retained).toBe(true)
    expect(retained.dispatch.requestBinding).toBe('pin-banned')
    f.actor(42)
    expect(retained.canPublish()).toBe(false)
    retained.release()
  })
  it('rejects forged session identity and does not silently rebind evicted keys', async () => {
    const f = fixture()
    expect(() => f.broker.prime({ ...f.session(), view_context: viewIdentity(cindyView(), 'banned') }, (awaitScope(f)))).toThrow()
    for (let index = 0; index < 33; index++) { const lease = await f.broker.acquire(`key-${index}`); lease.release() }
    await expect(f.broker.acquire('key-0')).rejects.toThrow('expired')
    f.broker.clear()
    await expect(f.broker.acquire('after-close')).rejects.toThrow('closed')
  })
})

function awaitScope(f: ReturnType<typeof fixture>) {
  const item = f.items.find(item => item.slot === 'account.view.v1')!
  return captureAccountView({ contribution: item, presetID: 'insufficient', query: {}, actorID: 41, currentActor: () => 41, currentItems: () => f.items })
}
