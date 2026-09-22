import type { PluginDispatchContext, PluginUISession } from '@/api/admin/plugins'
import type { CapturedAccountView } from '@/composables/useAccountViewContext'
import { accountViewIdentityKey } from './accountView'

interface Binding { requestBinding?: string; expires: number }
interface Capture { view?: CapturedAccountView; binding: Binding; pending: number; expires: number }

/** Business request pins are independent of the static iframe/bridge session. */
export function createPluginViewDispatch(options: {
  origin(): CapturedAccountView | undefined
  revision(): string
  assertFrame(retained: boolean): void
  createSession(view?: CapturedAccountView): Promise<PluginUISession>
}) {
  const bindings = new Map<string, Binding>()
  const captures = new Map<string, Capture>()
  const retired = new Set<string>()
  let disposed = false
  const keyOf = (view?: CapturedAccountView) => view ? accountViewIdentityKey(view.context) : 'native-core'
  function pin(session: PluginUISession, view?: CapturedAccountView) {
    if (view) {
      if (!session.request_binding || !session.view_context || accountViewIdentityKey(session.view_context) !== keyOf(view)) throw new Error('Account view session identity mismatch')
    } else if (session.view_context) throw new Error('Account view session cannot become unbound')
    const expires = Date.parse(session.expires_at)
    if (!Number.isFinite(expires) || expires <= Date.now()) throw new Error('Plugin session expired')
    const result = { requestBinding: session.request_binding, expires }
    if (bindings.size >= 32 && !bindings.has(keyOf(view))) bindings.delete(bindings.keys().next().value!)
    bindings.set(keyOf(view), result)
    return result
  }
  function retire(key: string) { captures.delete(key); retired.add(key) }
  function makeRoom() {
    for (const [key, capture] of captures) if (!capture.pending && capture.expires <= Date.now()) retire(key)
    if (captures.size >= 32) {
      const oldest = [...captures].find(([, capture]) => capture.pending === 0)
      if (!oldest) throw new Error('Too many pending account view operations')
      retire(oldest[0])
    }
    // Do not silently rebind an evicted operation key to a different view.
    if (retired.size >= 256) throw new Error('Reopen the view to start a new operation; existing results are retained')
  }
  async function acquire(operationKey?: string, retained = false) {
    if (disposed) throw new Error('Plugin view closed')
    options.assertFrame(retained)
    if (operationKey !== undefined && !/^[a-zA-Z0-9:_-]{1,160}$/.test(operationKey)) throw new Error('Invalid operation identity')
    if (operationKey && retired.has(operationKey)) throw new Error('Operation context expired; request a fresh preview')
    const revision = options.revision()
    let capture = operationKey ? captures.get(operationKey) : undefined
    if (capture && capture.expires <= Date.now()) { retire(operationKey!); throw new Error('Operation context expired; request a fresh preview') }
    if (!capture) {
      const view = options.origin()
      if (retained) (view?.assertRetained || view?.assertCurrent)?.()
      else view?.assertCurrent()
      let binding = bindings.get(keyOf(view))
      if (!binding || binding.expires <= Date.now()) {
        // Retained controls can use an existing pin while disabled, but cannot
        // mint a fresh business UI session for a disabled view.
        view?.assertCurrent()
        binding = pin(await options.createSession(view), view)
      }
      if (disposed) throw new Error('Plugin view closed')
      options.assertFrame(retained)
      capture = { view, binding, pending: 0, expires: binding.expires }
      if (operationKey) { makeRoom(); captures.set(operationKey, capture) }
    }
    const snapshot = capture
    const assertCurrent = () => {
      if (disposed || snapshot.expires <= Date.now()) throw new Error('Plugin operation context expired')
      options.assertFrame(retained)
      if (retained) (snapshot.view?.assertRetained || snapshot.view?.assertCurrent)?.()
      else snapshot.view?.assertCurrent()
    }
    assertCurrent()
    snapshot.pending++
    const dispatch: PluginDispatchContext = { view: snapshot.view, requestBinding: snapshot.binding.requestBinding, retained }
    return {
      dispatch,
      assertCurrent,
      canPublish() {
        try {
          assertCurrent()
          return operationKey ? captures.get(operationKey) === snapshot : options.revision() === revision
        } catch { return false }
      },
      release() { snapshot.pending = Math.max(0, snapshot.pending - 1) }
    }
  }
  return { prime: pin, acquire, clear() { disposed = true; bindings.clear(); captures.clear(); retired.clear() } }
}
