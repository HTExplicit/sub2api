import { computed, createApp, ref, type Component } from 'vue'
import { createI18n } from 'vue-i18n'
import { extensionAvailabilityKey, extensionUnavailableMessageKey } from './context'
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
  params?: Record<string, string | number>
  query?: Record<string, unknown>
  body?: unknown
  form?: Array<[string, string | Blob]>
}

export interface TranslationMessages {
  [key: string]: string | TranslationMessages
}

interface Bridge {
  context(): Promise<UIContext>
  resource(operation: string, input: ResourceInput): Promise<unknown>
  notify(type: string, fields?: Record<string, unknown>): void
  resize(height: number): void
  onContextChange(listener: (context: UIContext) => void): () => void
  dispose(): void
}

let connection: Bridge | null = null
function bridge(): Bridge {
  if (!connection) {
    const Constructor = (globalThis as unknown as { Sub2APIPluginBridge: new () => Bridge }).Sub2APIPluginBridge
    connection = new Constructor()
  }
  return connection
}

export async function resource<T>(operation: string, input: ResourceInput = {}): Promise<T> {
  return await bridge().resource(operation, input) as T
}

export function useNotifications() {
  return {
    showError: (message: string) => bridge().notify('ui.notify', { level: 'error', message }),
    showSuccess: (message: string) => bridge().notify('ui.notify', { level: 'success', message }),
    showWarning: (message: string) => bridge().notify('ui.notify', { level: 'warning', message }),
    showInfo: (message: string) => bridge().notify('ui.notify', { level: 'info', message })
  }
}

function applyPresentation(context: UIContext) {
  document.documentElement.classList.toggle('dark', context.theme === 'dark')
  const current = document.documentElement.style
  for (let index = current.length - 1; index >= 0; index--) {
    const key = current[index]!
    if (/^--(?:ui|theme)-/.test(key)) current.removeProperty(key)
  }
  for (const [key, value] of Object.entries(context.theme_tokens || {})) {
    if (/^--(?:ui|theme)-[a-z0-9-]+$/.test(key) && value.length <= 512 && !/url\s*\(/i.test(value)) current.setProperty(key, value)
  }
  for (const element of document.querySelectorAll('[data-plugin-theme]')) element.remove()
  for (const path of context.theme_stylesheets || []) {
    if (!/^\/api\/v1\/settings\/plugins\/\d+\/theme\/[a-f0-9]+\//.test(path) || /[\\\s]/.test(path)) continue
    const link = document.createElement('link')
    link.rel = 'stylesheet'; link.href = path; link.dataset.pluginTheme = 'true'
    document.head.append(link)
  }
}

// A plugin owns its page and locale messages. The public SDK supplies only
// rendering, presentation context and named host operations.
export async function mountPlugin(App: Component, messages: Record<string, TranslationMessages>) {
  const client = bridge()
  const context = ref(await client.context())
  applyPresentation(context.value)
  const i18n = createI18n({ legacy: false, locale: String(context.value.locale || 'zh').startsWith('zh') ? 'zh' : 'en', fallbackLocale: 'en', messages })
  const app = createApp(App)
  app.use(i18n)
  app.provide(extensionAvailabilityKey, computed(() => context.value.available !== false))
  app.provide(extensionUnavailableMessageKey, computed(() => typeof context.value.unavailable_message === 'string' ? context.value.unavailable_message : String(context.value.locale).startsWith('zh') ? '插件暂不可用，已保留输入' : 'Plugin unavailable; your input is retained'))
  app.mount('#app')
  const root = document.getElementById('app')!
  root.inert = context.value.available === false
  const stop = client.onContextChange(next => { context.value = next; root.inert = next.available === false; applyPresentation(next); i18n.global.locale.value = String(next.locale).startsWith('zh') ? 'zh' : 'en' })
  const observer = new ResizeObserver(() => client.resize(Math.ceil(root.getBoundingClientRect().height) + 24))
  observer.observe(root)
  window.addEventListener('pagehide', () => { stop(); observer.disconnect(); app.unmount(); client.dispose() }, { once: true })
}
