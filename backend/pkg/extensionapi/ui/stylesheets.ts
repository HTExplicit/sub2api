interface PendingStylesheet {
  href: string
  promise: Promise<void>
  cancel: () => void
}

interface StylesheetEntry {
  active?: HTMLLinkElement
  pending?: PendingStylesheet
}

export function pluginThemeURL(value: unknown): string | null {
  if (typeof value !== 'string' || !/^\/api\/v1\/settings\/plugins\/[1-9]\d*\/theme\/[a-f0-9]{64}\/[a-zA-Z0-9_./-]+\.css$/.test(value) || value.split('/').includes('..')) return null
  return value
}

async function preloadThemeFonts(link: HTMLLinkElement): Promise<void> {
  if (typeof FontFace === 'undefined' || !link.sheet) return
  const base = new URL(link.href)
  const directory = new URL('.', base).pathname
  const fonts: Promise<FontFace>[] = []
  for (const rule of Array.from(link.sheet.cssRules)) {
    if (rule.type !== CSSRule.FONT_FACE_RULE) continue
    const style = (rule as CSSFontFaceRule).style
    let valid = true
    const source = style.getPropertyValue('src').replace(/url\(\s*(['"]?)(.*?)\1\s*\)/g, (_match, _quote, path: string) => {
      const url = new URL(path, base)
      if (url.origin !== base.origin || !url.pathname.startsWith(directory) || !url.pathname.endsWith('.woff2')) valid = false
      return `url("${url.href}")`
    })
    if (!valid || !source.includes('url(')) continue
    const family = style.getPropertyValue('font-family').replace(/^['"]|['"]$/g, '')
    const face = new FontFace(family, source, { weight: style.getPropertyValue('font-weight') || 'normal', style: style.getPropertyValue('font-style') || 'normal' })
    fonts.push(face.load())
  }
  await Promise.all(fonts)
}

/** Keep the last loaded appearance until its replacement is ready. */
export function createStylesheetController(doc: Document, changed: () => void = () => {}) {
  const entries = new Map<string, StylesheetEntry>()
  let disposed = false

  function sync(desired: ReadonlyMap<string, string>): Promise<void> {
    if (disposed) return Promise.resolve()
    for (const [key, entry] of entries) {
      if (desired.has(key)) continue
      entry.pending?.cancel()
      entry.active?.remove()
      entries.delete(key)
      changed()
    }
    const loading: Promise<void>[] = []
    for (const [key, href] of desired) {
      let entry = entries.get(key)
      if (!entry) { entry = {}; entries.set(key, entry) }
      if (entry.active && !entry.active.isConnected) entry.active = undefined
      if (entry.pending?.href === href) { loading.push(entry.pending.promise); continue }
      entry.pending?.cancel()
      if (entry.active?.getAttribute('href') === href) continue

      const link = doc.createElement('link')
      link.rel = 'stylesheet'
      link.crossOrigin = 'anonymous'
      link.href = href
      link.dataset.pluginTheme = key
      // Fetch without applying a partially replaced theme to the current page.
      link.media = 'not all'
      const current = entry
      let complete!: (success: boolean) => void
      let loaded!: () => void
      const promise = new Promise<void>(resolve => {
        let timer = setTimeout(() => complete(false), 1500)
        let finished = false
        loaded = () => {
          if (finished) return
          clearTimeout(timer)
          // Fonts are substantially larger than CSS. Their asynchronous load
          // has its own deadline while the current theme remains usable.
          timer = setTimeout(() => complete(false), 15000)
          void preloadThemeFonts(link).then(() => complete(true), () => complete(false))
        }
        complete = success => {
          if (finished) return
          finished = true
          clearTimeout(timer)
          link.onload = link.onerror = null
          if (success && !disposed && entries.get(key) === current) {
            link.media = ''
            current.active?.remove()
            current.active = link
            changed()
          } else link.remove()
          current.pending = undefined
          resolve()
        }
      })
      current.pending = { href, promise, cancel: () => complete(false) }
      link.onload = loaded
      link.onerror = () => complete(false)
      doc.head.append(link)
      loading.push(promise)
    }
    return Promise.all(loading).then(() => {})
  }

  function dispose() {
    disposed = true
    for (const entry of entries.values()) { entry.pending?.cancel(); entry.active?.remove() }
    entries.clear()
  }
  return { sync, dispose }
}
