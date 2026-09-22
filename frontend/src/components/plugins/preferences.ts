// Browser preferences are scoped to the current user and stable plugin key.
// The existing connection-test preference stays authoritative for the core
// protocol dialog and independently packaged batch UI.
export const pluginPreferenceEvent = 'sub2api:ui-preference'
export function pluginPreferenceKey(origin: string, userID: number, pluginKey: string, key: string) {
  if (!userID || !/^[a-z0-9]+(?:[._-][a-z0-9]+)+$/.test(pluginKey) || !/^[a-z][a-z0-9_-]{0,63}$/.test(key)) throw new Error('Invalid preference context')
  if (pluginKey === 'codexrip.account-tools' && key === 'test-prompt') return `account-test-text:${origin}:${userID}`
  if (key === 'table-page-size') return 'table-page-size'
  return `sub2api:plugin-pref:${origin}:${userID}:${pluginKey}:${key}`
}

export function readPluginPreference(origin: string, userID: number, pluginKey: string, key: string): string | null {
  return localStorage.getItem(pluginPreferenceKey(origin, userID, pluginKey, key))
}

export function writeBrowserPreference(key: string, value: string) {
  try { localStorage.setItem(key, value) } catch { /* A browser can disable storage. */ }
  window.dispatchEvent(new CustomEvent(pluginPreferenceEvent, { detail: { key, value } }))
}
