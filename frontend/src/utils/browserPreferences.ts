export const browserPreferenceEvent = 'sub2api:ui-preference'

export function writeBrowserPreference(key: string, value: string) {
  try { localStorage.setItem(key, value) } catch { /* A browser can disable storage. */ }
  window.dispatchEvent(new CustomEvent(browserPreferenceEvent, { detail: { key, value } }))
}
