import { createStylesheetController, pluginThemeURL } from './stylesheets'

interface Presentation {
  theme?: string
  theme_tokens?: Record<string, string>
  theme_stylesheets?: string[]
}

export function createPluginPresentation(doc: Document, changed: () => void) {
  const ownedTokens = new Set<string>()
  const stylesheets = createStylesheetController(doc, changed)
  function update(context: Presentation) {
    doc.documentElement.classList.toggle('dark', context.theme === 'dark')
    const style = doc.documentElement.style
    const next = new Map(Object.entries(context.theme_tokens || {}).filter(([key, value]) =>
      /^--(?:ui|theme)-[a-z0-9-]+$/.test(key) && typeof value === 'string' && value.length <= 512 && !/url\s*\(/i.test(value)))
    for (const key of ownedTokens) if (!next.has(key)) { style.removeProperty(key); ownedTokens.delete(key) }
    for (const [key, value] of next) {
      if (style.getPropertyValue(key) !== value) style.setProperty(key, value)
      ownedTokens.add(key)
    }
    const desired = new Map<string, string>()
    for (const value of context.theme_stylesheets || []) {
      const href = pluginThemeURL(value)
      if (!href) continue
      // A new digest replaces the same plugin asset, rather than removing its
      // old stylesheet before the replacement has loaded.
      const key = href.replace(/\/theme\/[a-f0-9]{64}\//, '/theme/')
      desired.set(key, href)
    }
    void stylesheets.sync(desired)
    changed()
  }
  return { update, dispose: stylesheets.dispose }
}
