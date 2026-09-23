import { computed, createApp, ref, readonly, watch, onScopeDispose, toRaw, type Component } from 'vue'
import { createI18n } from 'vue-i18n'
import { extensionAvailabilityKey, extensionUnavailableMessageKey } from './context'
import { createPluginPresentation } from './presentation'
import { createPluginSizing } from './sizing'
import './bridge.js'

export interface UIContext {
  locale?: string
  theme?: string
  available?: boolean
  contribution_id?: string
  theme_tokens?: Record<string, string>
  theme_stylesheets?: string[]
  [key: string]: unknown
}

export interface ResourceInput {
  local_data?: unknown
  operation_key?: string
  params?: Record<string, string | number>
  query?: Record<string, unknown>
  body?: unknown
  form?: Array<[string, string | Blob]>
}

export interface ResourceAvailability {
  name: string
  available: boolean
}

export interface TranslationMessages {
  [key: string]: string | TranslationMessages
}

interface Bridge {
  request(type: string, fields?: Record<string, unknown>): Promise<Record<string, unknown>>
  event(name: string, payload?: unknown): Promise<unknown>
  openJob(id: number): Promise<unknown>
  preference(key: string): Promise<string | null>
  savePreference(key: string, value: string): Promise<unknown>
  onPreferenceChange(listener: (key: string, value: string) => void): () => void
  context(): Promise<UIContext>
  resource(operation: string, input: ResourceInput, signal?: AbortSignal): Promise<unknown>
  notify(type: string, fields?: Record<string, unknown>): void
  resize(height: number): void
  onContextChange(listener: (context: UIContext) => void): () => void
  dispose(): void
}

let connection: Bridge | null = null
const sharedContext = ref<UIContext>({})
export const usePluginContext = () => readonly(sharedContext)
const jsonValue = <T>(value: T): T => value === undefined ? value : JSON.parse(JSON.stringify(value)) as T
export const emitHostEvent = (name: string, payload?: unknown) => bridge().event(name, jsonValue(payload))
export const openHostJob = (id: number) => bridge().openJob(id)
export async function confirmAction(message: string) { return (await bridge().request('ui.confirm', { message })).confirmed === true }
export async function downloadBlob(blob: Blob, filename: string) { await bridge().request('ui.download', { blob, filename }) }

function localValue(value: unknown, ancestors = new Set<object>(), depth = 0): unknown {
  if (depth > 32) throw new Error('Local record is too deeply nested')
  if (value === null || typeof value !== 'object') return value
  const raw = toRaw(value)
  if (raw instanceof Blob || raw instanceof ArrayBuffer) return raw
  if (ancestors.has(raw)) throw new Error('Local record contains a cycle')
  ancestors.add(raw)
  const next = Array.isArray(raw) ? raw.map(item => localValue(item, ancestors, depth + 1)) : Object.fromEntries(Object.entries(raw).map(([key, item]) => [key, localValue(item, ancestors, depth + 1)]))
  ancestors.delete(raw)
  return next
}

export function usePersistentDraft(key: string) {
  const value = ref('')
  let edited = false, receiving = false, disposed = false
  const apply = (next: string) => { receiving = true; value.value = next; receiving = false }
  void bridge().preference(key).then(saved => { if (!disposed && !edited && saved !== null) apply(saved) }).catch(() => {})
  const stop = bridge().onPreferenceChange((changed, next) => { if (changed === key && typeof next === 'string') apply(next) })
  watch(value, next => { if (!receiving) { edited = true; void bridge().savePreference(key, next).catch(() => {}) } }, { flush: 'sync' })
  onScopeDispose(() => { disposed = true; stop() })
  return value
}
function bridge(): Bridge {
  if (!connection) {
    const Constructor = (globalThis as unknown as { Sub2APIPluginBridge: new () => Bridge }).Sub2APIPluginBridge
    connection = new Constructor()
  }
  return connection
}

export async function resource<T>(operation: string, input: ResourceInput = {}, signal?: AbortSignal): Promise<T> {
  // Vue proxies cannot be cloned by postMessage. JSON fields use the same
  // representation as HTTP JSON; File/Blob entries remain native objects.
  const payload = { ...jsonValue({ ...input, form: undefined, local_data: undefined }),
    ...(input.local_data === undefined ? {} : { local_data: localValue(input.local_data) }),
    ...(input.form ? { form: Array.from(input.form, ([key, value]) => [key, value] as [string, string | Blob]) } : {}) }
  return await bridge().resource(operation, payload, signal) as T
}

export async function resourceAvailability(): Promise<ResourceAvailability[]> {
  const { resources } = await bridge().request('extension.resources')
  if (!Array.isArray(resources) || resources.some(item => !item || typeof item.name !== 'string' || typeof item.available !== 'boolean')) {
    throw new Error('Invalid resource availability')
  }
  return resources.map(({ name, available }) => ({ name, available }))
}

export function useNotifications() {
  return {
    showError: (message: string) => bridge().notify('ui.notify', { level: 'error', message }),
    showSuccess: (message: string) => bridge().notify('ui.notify', { level: 'success', message }),
    showWarning: (message: string) => bridge().notify('ui.notify', { level: 'warning', message }),
    showInfo: (message: string) => bridge().notify('ui.notify', { level: 'info', message })
  }
}

// A plugin owns its page and locale messages. The public SDK supplies only
// rendering, presentation context and named host operations.
export async function mountPlugin(App: Component, messages: Record<string, TranslationMessages>) {
  const client = bridge()
  const context = sharedContext
  context.value = await client.context()
  const scheduleSizing = { run: () => {} }
  const presentation = createPluginPresentation(document, () => scheduleSizing.run())
  presentation.update(context.value)
  const i18n = createI18n({ legacy: false, locale: String(context.value.locale || 'zh').startsWith('zh') ? 'zh' : 'en', fallbackLocale: 'en', messages })
  const app = createApp(App)
  app.use(i18n)
  app.provide(extensionAvailabilityKey, computed(() => context.value.retained_controls === true || context.value.available !== false))
  app.provide(extensionUnavailableMessageKey, computed(() => typeof context.value.unavailable_message === 'string' ? context.value.unavailable_message : String(context.value.locale).startsWith('zh') ? '插件暂不可用，已保留输入' : 'Plugin unavailable; your input is retained'))
  app.mount('#app')
  const root = document.getElementById('app')!
  root.inert = context.value.retained_controls !== true && context.value.available === false
  const stop = client.onContextChange(next => { context.value = next; root.inert = next.retained_controls !== true && next.available === false; presentation.update(next); i18n.global.locale.value = String(next.locale).startsWith('zh') ? 'zh' : 'en' })
  if (context.value.layout === 'inline') document.body.style.minHeight = '0'
  const sizing = createPluginSizing(root, height => client.resize(height), () => context.value.layout === 'inline')
  scheduleSizing.run = sizing.schedule
  window.addEventListener('pagehide', () => { stop(); sizing.dispose(); presentation.dispose(); app.unmount(); client.dispose() }, { once: true })
}
