import { publicContributions, type PluginContribution } from '@/api/admin/plugins'
import { createStylesheetController, pluginThemeURL } from '@sub2api/plugin-ui/stylesheets'

let flight: Promise<void> | null = null
let dirty = false

let stylesheets: ReturnType<typeof createStylesheetController> | undefined

export function applyPluginThemeContributions(items: PluginContribution[]): Promise<void> {
  const desired = new Map<string, string>()
  for (const item of items) {
    if (item.slot !== 'theme' || item.permission !== 'public' || !item.available) continue
    const href = pluginThemeURL(item.stylesheet_url)
    if (!href) continue
    const key = `${item.plugin_id}:${item.id}`
    desired.set(key, href)
  }
  stylesheets ||= createStylesheetController(document)
  return stylesheets.sync(desired)
}

export function refreshPluginThemes(): Promise<void> {
  if (flight) { dirty = true; return flight }
  dirty = false
  flight = (async () => {
    try { await applyPluginThemeContributions(await publicContributions()) }
    // A failed metadata read does not revoke the last loaded appearance.
    // Explicit unavailable/removed contributions are handled by sync above.
    catch { /* keep the last successful appearance */ }
    finally { flight = null; if (dirty) void refreshPluginThemes() }
  })()
  return flight
}

export async function initializePluginThemes(): Promise<void> {
  // Theme availability never holds application startup indefinitely.
  let timer: ReturnType<typeof setTimeout> | undefined
  await Promise.race([refreshPluginThemes(), new Promise<void>(resolve => { timer = setTimeout(resolve, 1000) })])
  if (timer) clearTimeout(timer)
}

export function startPluginThemeRefresh(): () => void {
  const refresh = () => { void refreshPluginThemes() }
  const timer = setInterval(() => { if (document.visibilityState === 'visible') refresh() }, 15000)
  window.addEventListener('sub2api:plugins-changed', refresh)
  return () => { clearInterval(timer); window.removeEventListener('sub2api:plugins-changed', refresh) }
}
