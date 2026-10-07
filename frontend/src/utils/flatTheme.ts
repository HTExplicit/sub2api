import { readonly, ref } from 'vue'

const active = ref(false)

// Console theme: a text field with no placeholder is given a blank one (" ") so CSS can tell an empty
// field (:placeholder-shown) from a filled one. An empty field whose label sits outside the box is
// identified by its border alone, and the console theme draws that border with the 3:1 form-control line
// (styles/console/behaviour.css, B3.2). Nothing is visible: the placeholder is a space.
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

// The console theme (styles/console/*.css) applies while <html> carries the flat-theme class.
// It follows the public flat_theme_enabled setting and stays on when the setting is absent.
// It loads no web fonts: text uses the OS UI font stack of styles/theme.css, as upstream does.
export function applyFlatTheme(enabled: boolean | undefined): void {
  active.value = enabled !== false
  document.documentElement.classList.toggle('flat-theme', active.value)
  watchUnlabelledFields(active.value)
}

/** Reactive: true while the console theme is on. */
export const flatThemeActive = readonly(active)
