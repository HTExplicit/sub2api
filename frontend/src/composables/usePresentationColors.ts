import { onBeforeUnmount, onMounted, shallowRef } from 'vue'

// Canvas consumers need resolved colors. CSS custom properties keep optional
// presentation policy outside their data, chart and billing logic.
const defaults = {
  text: ['#374151', '#e5e7eb'], grid: ['#e5e7eb', '#374151'],
  axis: ['#6b7280', '#9ca3af'], axisGrid: ['#f3f4f6', '#374151'],
  tooltip: ['#ffffff', '#1f2937'], title: ['#111827', '#f9fafb'],
  body: ['#4b5563', '#d1d5db'], gray: ['#9ca3af', '#9ca3af']
} as const
type Colors = Record<keyof typeof defaults, string>

export function usePresentationColors() {
  const colors = shallowRef<Colors>(Object.fromEntries(Object.entries(defaults).map(([key, values]) => [key, values[0]])) as Colors)
  let observer: MutationObserver | null = null, active = false, queued = false
  const read = () => {
    queued = false
    if (!active) return
    const root = document.documentElement, styles = getComputedStyle(root), dark = root.classList.contains('dark')
    const next = { ...colors.value }
    for (const key of Object.keys(defaults) as Array<keyof Colors>) {
      const value = styles.getPropertyValue(`--theme-chart-${key.replace(/[A-Z]/g, letter => '-' + letter.toLowerCase())}`).trim()
      next[key] = value && typeof CSS !== 'undefined' && CSS.supports('color', value) ? value : defaults[key][dark ? 1 : 0]
    }
    if (Object.keys(next).some(key => next[key as keyof Colors] !== colors.value[key as keyof Colors])) colors.value = next
  }
  const schedule = () => { if (active && !queued) { queued = true; queueMicrotask(read) } }
  onMounted(() => {
    active = true
    read()
    observer = new MutationObserver(schedule)
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['class', 'style'] })
    observer.observe(document.head, { childList: true, subtree: true, attributes: true, attributeFilter: ['href', 'media', 'disabled'] })
    document.head.addEventListener('load', schedule, true)
    document.head.addEventListener('error', schedule, true)
  })
  onBeforeUnmount(() => {
    active = false
    observer?.disconnect()
    document.head.removeEventListener('load', schedule, true)
    document.head.removeEventListener('error', schedule, true)
  })
  return colors
}
