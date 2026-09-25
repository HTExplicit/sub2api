import { readonly, ref } from 'vue'

const active = ref(false)

// The console theme (styles/flat-theme.css) applies while <html> carries the flat-theme class.
// It follows the public flat_theme_enabled setting and stays on when the setting is absent.
export function applyFlatTheme(enabled: boolean | undefined): void {
  active.value = enabled !== false
  document.documentElement.classList.toggle('flat-theme', active.value)
}

/** Reactive: true while the console theme is on (identity colours turn neutral). */
export const flatThemeActive = readonly(active)
