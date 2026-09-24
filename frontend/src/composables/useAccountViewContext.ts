import { computed, inject, onBeforeUnmount, provide, shallowRef, watch, type ComputedRef, type InjectionKey } from 'vue'
import { CanceledError } from 'axios'

/** Read guard retained by an opened native account operation. */
export interface CapturedAccountView {
  readonly actorID?: number
  assertCurrent(): void
}

export interface AccountViewController {
  capture(): CapturedAccountView | undefined
  available(): boolean
  revision(): string
}

const accountViewContextKey: InjectionKey<AccountViewController> = Symbol('account-view-context')
export const accountViewTitleKey: InjectionKey<ComputedRef<string | undefined>> = Symbol('account-view-title')

export function provideAccountViewContext(controller: AccountViewController) { provide(accountViewContextKey, controller) }
export function useAccountViewContext() { return inject(accountViewContextKey, null) }



/** An opened modal owns its snapshot; later page selection/filter changes do not retarget it. */
export function useAccountViewOperation(active: () => boolean, identity: () => unknown = () => undefined, origin?: () => CapturedAccountView | undefined) {
  const controller = useAccountViewContext()
  const captured = shallowRef<CapturedAccountView | undefined>()
  const capturedRevision = shallowRef('')
  let operationVersion = 0
  let hasSnapshot = false
  const captureError = shallowRef<unknown>()
  watch([active, identity], ([open]) => {
    operationVersion++
    if (!open) return
    hasSnapshot = true
    capturedRevision.value = controller?.revision() || ''
    captureError.value = undefined
    try { captured.value = origin?.() || controller?.capture() } catch (error) { captureError.value = error }
  }, { immediate: true, flush: 'sync' })
  onBeforeUnmount(() => { operationVersion++ })
  function capture() {
    if (captureError.value) throw captureError.value
    return hasSnapshot ? captured.value : controller?.capture()
  }
  return {
    revision: () => `${operationVersion}:${hasSnapshot ? capturedRevision.value : controller?.revision() || ''}`,
    available: computed(() => {
      if (captureError.value) return false
      if (!hasSnapshot) return controller?.available() ?? true
      try { captured.value?.assertCurrent(); return true } catch { return false }
    }),
    capture,
    async read<T>(read: (scope?: CapturedAccountView) => Promise<T>): Promise<T> {
      if (!active()) throw new CanceledError('Account operation closed')
      const version = operationVersion, scope = capture()
      const ensureCurrent = () => {
        scope?.assertCurrent()
        if (!active() || version !== operationVersion) throw new CanceledError('Account operation changed')
      }
      ensureCurrent()
      let result: T
      try { result = await read(scope) } catch (error) { ensureCurrent(); throw error }
      ensureCurrent()
      return result
    }
  }
}

export async function readWithAccountView<T>(controller: AccountViewController | null, read: (view?: CapturedAccountView) => Promise<T>): Promise<T> {
  const revision = controller?.revision()
  const view = controller?.capture()
  const result = await read(view)
  view?.assertCurrent()
  if (controller?.revision() !== revision) throw new CanceledError('Account view query changed')
  return result
}

export function guardAccountViewRead(scope: CapturedAccountView | undefined, current: () => boolean): CapturedAccountView | undefined {
  if (!scope) return undefined
  return { ...scope, assertCurrent() {
    scope.assertCurrent()
    if (!current()) throw new CanceledError('Account view query changed')
  } }
}
