import { onBeforeUnmount, onMounted, shallowRef } from 'vue'

// Canvas consumers need resolved colors. CSS custom properties keep optional
// presentation policy outside their data, chart and billing logic.
const defaults = {
  text: ['#374151', '#e5e7eb'], grid: ['#e5e7eb', '#374151'],
  axis: ['#6b7280', '#9ca3af'], axisGrid: ['#f3f4f6', '#374151'],
  tooltip: ['#ffffff', '#1f2937'], title: ['#111827', '#f9fafb'],
  body: ['#4b5563', '#d1d5db'], gray: ['#9ca3af', '#9ca3af']
} as const
// Categorical series (distribution charts, token trend). Defaults are the upstream palette; the
// console theme supplies --theme-chart-series-1..12 as #rrggbb (consumers append hex alpha).
const seriesDefaults = [
  '#3b82f6', '#10b981', '#f59e0b', '#ef4444', '#8b5cf6', '#ec4899',
  '#14b8a6', '#f97316', '#6366f1', '#84cc16', '#06b6d4', '#a855f7'
] as const
type Colors = Record<keyof typeof defaults, string> & { series: string[] }

export function usePresentationColors() {
  const colors = shallowRef<Colors>({
    ...(Object.fromEntries(Object.entries(defaults).map(([key, values]) => [key, values[0]])) as Record<keyof typeof defaults, string>),
    series: [...seriesDefaults]
  })
  let observer: MutationObserver | null = null, active = false, queued = false
  const read = () => {
    queued = false
    if (!active) return
    const root = document.documentElement, styles = getComputedStyle(root), dark = root.classList.contains('dark')
    const isColor = (value: string) => !!value && typeof CSS !== 'undefined' && CSS.supports('color', value)
    const next = { ...colors.value }
    for (const key of Object.keys(defaults) as Array<keyof typeof defaults>) {
      const value = styles.getPropertyValue(`--theme-chart-${key.replace(/[A-Z]/g, letter => '-' + letter.toLowerCase())}`).trim()
      next[key] = isColor(value) ? value : defaults[key][dark ? 1 : 0]
    }
    next.series = seriesDefaults.map((fallback, index) => {
      const value = styles.getPropertyValue(`--theme-chart-series-${index + 1}`).trim()
      return /^#[0-9a-f]{6}$/i.test(value) && isColor(value) ? value : fallback
    })
    const changed = (Object.keys(defaults) as Array<keyof typeof defaults>).some(key => next[key] !== colors.value[key]) ||
      next.series.some((value, index) => value !== colors.value.series[index])
    if (changed) colors.value = next
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
