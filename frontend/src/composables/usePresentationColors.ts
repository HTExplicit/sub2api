import { onBeforeUnmount, onMounted, shallowRef } from 'vue'
import { Chart, type ScriptableContext } from 'chart.js'

// Console theme: canvas text uses the UI font stack (Geist + MiSans) instead of Chart.js'
// Helvetica/Arial default, lines are 2px and doughnuts are rings. Resting point markers are dropped
// except where a value would otherwise be invisible or hard to place: a series with at most three
// values, and a value with no neighbour on either side (a single point, or one between two gaps).
// Chart.js' own defaults are saved before the first change and restored when the theme is switched
// off at runtime. Charts re-resolve these defaults on the update that follows the next color read.
const SPARSE_SERIES_MAX_POINTS = 3
type ChartDefaultsSnapshot = {
  fontFamily: typeof Chart.defaults.font.family
  lineBorderWidth: typeof Chart.defaults.elements.line.borderWidth
  pointRadius: typeof Chart.defaults.elements.point.radius
  pointHoverRadius: typeof Chart.defaults.elements.point.hoverRadius
  doughnutCutout: typeof Chart.overrides.doughnut.cutout
  // cutout normally lives in the doughnut dataset defaults, not in the overrides: restore by deleting
  doughnutCutoutOwn: boolean
}
let upstreamChartDefaults: ChartDefaultsSnapshot | null = null
const valueCounts = new WeakMap<object, { length: number; count: number }>()

function hasChartValue(value: unknown): boolean {
  if (value === null || value === undefined) return false
  if (typeof value === 'number') return Number.isFinite(value)
  if (Array.isArray(value)) return value.length > 1 && hasChartValue(value[1])
  if (typeof value === 'object' && 'y' in (value as Record<string, unknown>)) return hasChartValue((value as { y: unknown }).y)
  return true
}

function countChartValues(data: unknown[]): number {
  const cached = valueCounts.get(data)
  if (cached && cached.length === data.length) return cached.count
  const count = data.reduce<number>((total, value) => total + (hasChartValue(value) ? 1 : 0), 0)
  valueCounts.set(data, { length: data.length, count })
  return count
}

function restingPointRadius(): number {
  const radius = upstreamChartDefaults?.pointRadius
  return typeof radius === 'number' && radius > 0 ? radius : 3
}

function consolePointRadius(ctx: ScriptableContext<'line'>): number {
  const data = (ctx.dataset?.data ?? []) as unknown[]
  if (countChartValues(data) <= SPARSE_SERIES_MAX_POINTS) return restingPointRadius()
  const index = ctx.dataIndex
  if (typeof index !== 'number') return 0
  return !hasChartValue(data[index - 1]) && !hasChartValue(data[index + 1]) ? restingPointRadius() : 0
}

function applyConsoleChartDefaults(styles: CSSStyleDeclaration) {
  if (!upstreamChartDefaults) {
    upstreamChartDefaults = {
      fontFamily: Chart.defaults.font.family,
      lineBorderWidth: Chart.defaults.elements.line.borderWidth,
      pointRadius: Chart.defaults.elements.point.radius,
      pointHoverRadius: Chart.defaults.elements.point.hoverRadius,
      doughnutCutout: Chart.overrides.doughnut.cutout,
      doughnutCutoutOwn: Object.prototype.hasOwnProperty.call(Chart.overrides.doughnut, 'cutout')
    }
  }
  const font = styles.getPropertyValue('--ui-font').trim()
  if (font) Chart.defaults.font.family = font
  Chart.defaults.elements.line.borderWidth = 2
  Chart.defaults.elements.point.radius = consolePointRadius as unknown as typeof Chart.defaults.elements.point.radius
  Chart.defaults.elements.point.hoverRadius = 4
  Chart.overrides.doughnut.cutout = '68%'
}

function restoreUpstreamChartDefaults() {
  if (!upstreamChartDefaults) return
  Chart.defaults.font.family = upstreamChartDefaults.fontFamily
  Chart.defaults.elements.line.borderWidth = upstreamChartDefaults.lineBorderWidth
  Chart.defaults.elements.point.radius = upstreamChartDefaults.pointRadius
  Chart.defaults.elements.point.hoverRadius = upstreamChartDefaults.pointHoverRadius
  if (upstreamChartDefaults.doughnutCutoutOwn) Chart.overrides.doughnut.cutout = upstreamChartDefaults.doughnutCutout
  else delete (Chart.overrides.doughnut as unknown as Record<string, unknown>).cutout
  upstreamChartDefaults = null
}

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
    if (root.classList.contains('flat-theme')) applyConsoleChartDefaults(styles)
    else restoreUpstreamChartDefaults()
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
