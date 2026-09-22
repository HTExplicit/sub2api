import { computed, inject, onBeforeUnmount, provide, shallowRef, watch, type ComputedRef, type InjectionKey } from 'vue'
import { CanceledError } from 'axios'
import type { PluginContribution } from '@/api/admin/plugins'
import type { AccountViewContextV1, AccountViewQueryV1 } from '@sub2api/plugin-ui/account-view'
import { cloneViewContext, resolveAccountView, viewIdentity } from '@/components/plugins/accountView'

/** Host-only request capability. Functions/guards never cross the iframe bridge. */
export interface CapturedAccountView {
  readonly context: AccountViewContextV1
  readonly actorID: number
  assertCurrent(): void
  assertRetained?(): void
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

export function captureAccountView(options: {
  contribution: PluginContribution; presetID: string; query: AccountViewQueryV1; actorID: number
  currentActor: () => number | undefined; currentItems: () => PluginContribution[]; active?: () => boolean
}): CapturedAccountView {
  const context = cloneViewContext({ ...viewIdentity(options.contribution, options.presetID), query: options.query })
  const admittedScope = JSON.stringify(options.contribution.account_scope)
  const scope: CapturedAccountView = {
    context, actorID: options.actorID,
    assertRetained() {
      if (!options.actorID || options.currentActor() !== options.actorID || options.active?.() === false) throw new CanceledError('Account view context changed')
      const sameOwner = options.currentItems().filter(item => item.plugin_id === context.plugin_id)
      if (sameOwner.some(item => item.package_sha256 && item.package_sha256 !== context.package_sha256)) throw new CanceledError('Account view package changed')
      const current = resolveAccountView(options.currentItems(), context.plugin_key, context.view_id)
      if (current && current.view_definition_digest !== context.view_definition_digest) throw new CanceledError('Account view definition changed')
    },
    assertCurrent() {
      if (!options.actorID || options.currentActor() !== options.actorID || options.active?.() === false) throw new CanceledError('Account view context changed')
      const current = resolveAccountView(options.currentItems(), context.plugin_key, context.view_id)
      if (!current?.available || current.plugin_id !== context.plugin_id || current.package_sha256 !== context.package_sha256 ||
          current.view_definition_digest !== context.view_definition_digest || JSON.stringify(current.account_scope) !== admittedScope ||
          !current.account_view.presets.some(preset => preset.id === context.preset_id)) throw new CanceledError('Account view unavailable')
    }
  }
  scope.assertCurrent()
  return scope
}

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
  return { context: scope.context, actorID: scope.actorID, assertCurrent() {
    scope.assertCurrent()
    if (!current()) throw new CanceledError('Account view query changed')
  } }
}

export function narrowAccountViewSelection(scope: CapturedAccountView | undefined, ids: number[]): CapturedAccountView | undefined {
  if (!scope || !ids.length) return scope
  const selected = [...new Set(ids)]
  if (selected.some(id => !Number.isSafeInteger(id) || id <= 0)) throw new Error('Invalid account selection')
  const existing = scope.context.query.account_ids
  if (existing?.length && selected.some(id => !existing.includes(id))) throw new Error('Selection is outside the captured account view')
  const context = cloneViewContext(scope.context)
  context.query.account_ids = selected.sort((a, b) => a - b)
  return { ...scope, context }
}
