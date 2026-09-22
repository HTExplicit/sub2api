import type { CapturedAccountView } from '@/composables/useAccountViewContext'
import { accountViewClient } from './accountViewClient'

export interface PluginDispatchContext {
  view?: CapturedAccountView
  requestBinding?: string
  retained?: boolean
  pluginID?: number
  packageSHA?: string
}
export function pluginDispatchHeaders(context?: PluginDispatchContext): Record<string, string> {
  return {
    ...(context?.requestBinding ? { 'X-Sub2API-Plugin-UI-Session': context.requestBinding } : {}),
    ...(context?.pluginID ? { 'X-Sub2API-Plugin': String(context.pluginID) } : {}),
    ...(context?.packageSHA ? { 'X-Sub2API-Plugin-Package': context.packageSHA } : {})
  }
}
export function pluginDispatchClient(context?: PluginDispatchContext) {
  const view = context?.view
  return accountViewClient(view && context?.retained ? { ...view, assertCurrent: () => (view.assertRetained || view.assertCurrent)() } : view)
}
