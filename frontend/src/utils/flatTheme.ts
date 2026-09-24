// The flat site theme (styles/flat-theme.css) applies while <html> carries the
// flat-theme class. It follows the public flat_theme_enabled setting and stays
// on when the setting is absent.
export function applyFlatTheme(enabled: boolean | undefined): void {
  document.documentElement.classList.toggle('flat-theme', enabled !== false)
}
