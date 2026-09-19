import { publicContributions, type PluginContribution } from '@/api/admin/plugins'

let flight: Promise<void> | null = null
let dirty = false

function themeURL(value: string | undefined): string | null {
  if (!value || !/^\/api\/v1\/settings\/plugins\/[1-9]\d*\/theme\/[a-f0-9]{64}\/[a-zA-Z0-9_./-]+\.css$/.test(value) || value.split('/').includes('..')) return null
  return value
}

export function applyPluginThemeContributions(items: PluginContribution[]): Promise<void> {
  const links = new Map(Array.from(document.querySelectorAll<HTMLLinkElement>('link[data-plugin-theme]')).map(link => [link.dataset.pluginTheme!, link]))
  const loading: Promise<void>[] = []
  for (const item of items) {
    if (item.slot !== 'theme' || item.permission !== 'public' || !item.available) continue
    const href = themeURL(item.stylesheet_url)
    if (!href) continue
    const key = `${item.plugin_id}:${item.id}`
    const previous = links.get(key)
    links.delete(key)
    if (previous?.getAttribute('href') === href) continue
    previous?.remove()
    const link = document.createElement('link')
    link.rel = 'stylesheet'
    link.dataset.pluginTheme = key
    link.href = href
    loading.push(new Promise(resolve => {
      const timer = setTimeout(() => { link.remove(); resolve() }, 1500)
      link.onload = () => { clearTimeout(timer); resolve() }
      link.onerror = () => { clearTimeout(timer); link.remove(); resolve() }
    }))
    document.head.append(link)
  }
  for (const link of links.values()) link.remove()
  return Promise.all(loading).then(() => {})
}

export function refreshPluginThemes(): Promise<void> {
  if (flight) { dirty = true; return flight }
  dirty = false
  flight = (async () => {
    try { await applyPluginThemeContributions(await publicContributions()) }
    catch { await applyPluginThemeContributions([]) }
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
