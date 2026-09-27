import { readonly, ref } from 'vue'

const active = ref(false)

// Console theme: a text field with no placeholder is given a blank one (" ") so CSS can tell an empty
// field (:placeholder-shown) from a filled one. An empty field whose label sits outside the box is
// identified by its border alone, and the console theme draws that border with the 3:1 control line
// (style.css, "Text fields with no placeholder"). Nothing is visible: the placeholder is a space.
// An empty placeholder (placeholder="") is treated like a missing one: browsers disagree on whether it
// counts as :placeholder-shown. Fields are re-checked when their placeholder or class changes later.
const BLANK_PLACEHOLDER = ' '
// (a plain selector list, no :is(): jsdom's selector engine rejects :is() with a :not() inside)
const FIELD_TYPES = ':not([type="checkbox"]):not([type="radio"]):not([type="hidden"])'
const UNLABELLED_FIELDS = `input.input:not([placeholder])${FIELD_TYPES}, input.input[placeholder=""]${FIELD_TYPES}`
let fieldObserver: MutationObserver | null = null

function markBlankPlaceholders(root: ParentNode): void {
  root.querySelectorAll(UNLABELLED_FIELDS).forEach((field) => field.setAttribute('placeholder', BLANK_PLACEHOLDER))
}

function markField(node: Node): void {
  if (node instanceof Element && node.tagName === 'INPUT' && node.matches(UNLABELLED_FIELDS)) {
    node.setAttribute('placeholder', BLANK_PLACEHOLDER)
  }
}

function watchUnlabelledFields(on: boolean): void {
  if (typeof MutationObserver === 'undefined') return
  if (on && !fieldObserver) {
    markBlankPlaceholders(document)
    fieldObserver = new MutationObserver((records) => {
      for (const record of records) {
        if (record.type === 'attributes') {
          markField(record.target)
          continue
        }
        record.addedNodes.forEach((node) => {
          if (!(node instanceof Element)) return
          markField(node)
          markBlankPlaceholders(node)
        })
      }
    })
    fieldObserver.observe(document.documentElement, {
      childList: true,
      subtree: true,
      attributes: true,
      attributeFilter: ['placeholder', 'class']
    })
  } else if (!on && fieldObserver) {
    fieldObserver.disconnect()
    fieldObserver = null
  }
}

// MiSans (Chinese text of the console theme) is not shipped with the app: it is loaded from Xiaomi's
// official font service (one stylesheet with both weights, unicode-range slices, font-display: swap)
// while the console theme is on, and removed when it is switched off. The CSP allows both hosts
// (backend config DefaultCSPPolicy). If the service is unreachable, the font stack in flat-theme.css
// falls back to PingFang SC / Microsoft YaHei and the other system fonts.
const MISANS_FONT_ORIGIN = 'https://cdn-file.hyperos.mi.com'
const MISANS_STYLESHEET = 'https://font.sec.miui.com/font/css?family=MiSans:400,500:Chinese_Simplify,Latin'

function syncMiSansStylesheet(on: boolean): void {
  if (typeof document === 'undefined' || !document.head) return
  const existing = document.head.querySelectorAll<HTMLLinkElement>('link[data-misans]')
  if (!on) {
    existing.forEach((link) => link.remove())
    return
  }
  if (existing.length) return
  const preconnect = document.createElement('link')
  preconnect.rel = 'preconnect'
  preconnect.href = MISANS_FONT_ORIGIN
  preconnect.crossOrigin = 'anonymous'
  preconnect.dataset.misans = 'preconnect'
  const stylesheet = document.createElement('link')
  stylesheet.rel = 'stylesheet'
  stylesheet.href = MISANS_STYLESHEET
  stylesheet.dataset.misans = 'stylesheet'
  document.head.append(preconnect, stylesheet)
}

// The console theme (styles/flat-theme.css) applies while <html> carries the flat-theme class.
// It follows the public flat_theme_enabled setting and stays on when the setting is absent.
export function applyFlatTheme(enabled: boolean | undefined): void {
  active.value = enabled !== false
  document.documentElement.classList.toggle('flat-theme', active.value)
  watchUnlabelledFields(active.value)
  syncMiSansStylesheet(active.value)
}

/** Reactive: true while the console theme is on (identity colours turn neutral). */
export const flatThemeActive = readonly(active)
