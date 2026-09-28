import { onBeforeUnmount, onMounted, shallowRef } from 'vue'
import { Chart, type Plugin, type ScriptableContext } from 'chart.js'

// Console theme (Apple): canvas text uses the UI font stack (SF Pro / Inter + MiSans) instead of
// Chart.js' Helvetica/Arial default; lines are 2px, doughnuts are rings, bars have rounded ends; legends
// are small solid dots; tooltips are a light (dark) rounded panel without a caret, like a popover; the area
// under a filled line fades from the line colour to transparent (Stocks / Health). Resting point markers
// are dropped except where a value would otherwise be invisible or hard to place: a series with at most
// three values, and a value with no neighbour on either side (a single point, or one between two gaps).
// Text and grid lines that a chart does not colour itself take the theme's axis and grid colours.
// Chart.js' own defaults are saved before the first change and restored when the theme is switched
// off at runtime. Charts re-resolve these defaults on the update that follows the next color read.
const SPARSE_SERIES_MAX_POINTS = 3
const TOOLTIP_KEYS = ['backgroundColor', 'titleColor', 'bodyColor', 'footerColor', 'borderColor', 'borderWidth',
  'cornerRadius', 'padding', 'boxPadding', 'usePointStyle', 'caretSize'] as const
const LEGEND_LABEL_KEYS = ['usePointStyle', 'pointStyle', 'boxWidth', 'boxHeight', 'generateLabels'] as const
type ChartDefaultsSnapshot = {
  color: typeof Chart.defaults.color
  borderColor: typeof Chart.defaults.borderColor
  fontFamily: typeof Chart.defaults.font.family
  lineBorderWidth: typeof Chart.defaults.elements.line.borderWidth
  pointRadius: typeof Chart.defaults.elements.point.radius
  pointHoverRadius: typeof Chart.defaults.elements.point.hoverRadius
  // bar defaults exist only once a chart registered BarElement
  barBorderRadius: unknown
  doughnutCutout: typeof Chart.overrides.doughnut.cutout
  // cutout normally lives in the doughnut dataset defaults, not in the overrides: restore by deleting
  doughnutCutoutOwn: boolean
  tooltip: Record<string, unknown>
  legendLabels: Record<string, unknown>
}
let upstreamChartDefaults: ChartDefaultsSnapshot | null = null
let consoleChartsActive = false
const valueCounts = new WeakMap<object, { length: number; count: number }>()

// `#rrggbb`, `#rrggbbaa`, `rgb(r, g, b)` and `rgba(r, g, b, a)`: channels, alpha, and how the alpha was written.
type ParsedColor = { r: number; g: number; b: number; a: number; hex: boolean; alpha: string | null }
function parseColor(value: string): ParsedColor | null {
  const hex = /^#([0-9a-f]{6})([0-9a-f]{2})?$/i.exec(value)
  if (hex) {
    const [r, g, b] = [0, 2, 4].map((offset) => parseInt(hex[1].slice(offset, offset + 2), 16))
    return { r, g, b, a: hex[2] ? parseInt(hex[2], 16) / 255 : 1, hex: true, alpha: hex[2] ?? null }
  }
  const rgb = /^rgba?\(\s*(\d{1,3})\s*,\s*(\d{1,3})\s*,\s*(\d{1,3})\s*(?:,\s*([\d.]+)\s*)?\)$/i.exec(value)
  if (!rgb) return null
  return { r: Number(rgb[1]), g: Number(rgb[2]), b: Number(rgb[3]), a: rgb[4] === undefined ? 1 : Number(rgb[4]), hex: false, alpha: rgb[4] ?? null }
}

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

type LegendLabel = { fillStyle?: unknown; strokeStyle?: unknown; lineWidth?: number; lineDash?: number[] }
type GenerateLabels = (this: unknown, chart: Chart) => LegendLabel[]

// Legend markers: a line series with a translucent area fill (`#rrggbbAA` or `rgba()`) is shown as a solid dot
// in the line colour instead of a hollow ring.
function consoleLegendLabels(this: unknown, chart: Chart): LegendLabel[] {
  const base = upstreamChartDefaults?.legendLabels.generateLabels as GenerateLabels | undefined
  const items = base ? base.call(this, chart) : []
  for (const item of items) {
    const fill = typeof item.fillStyle === 'string' ? parseColor(item.fillStyle) : null
    if (typeof item.strokeStyle === 'string' && fill && fill.a < 1) {
      item.fillStyle = item.strokeStyle
      item.lineWidth = 0
      item.lineDash = []
    }
  }
  return items
}

// Area fills: a filled line whose fill is a translucent colour (`#rrggbbAA` or `rgba()`) fades from that colour
// at the top of the chart area to transparent at the bottom. Only the resolved element option is swapped, right
// before the filler draws it; the dataset keeps its colour string.
const fadeCache = new WeakMap<object, { key: string; gradient: CanvasGradient }>()
const areaFadePlugin: Plugin = {
  id: 'consoleAreaFade',
  beforeDatasetsDraw(chart) {
    if (!consoleChartsActive) return
    const area = chart.chartArea
    if (!area || !(area.bottom > area.top)) return
    chart.data.datasets.forEach((dataset, index) => {
      const meta = chart.getDatasetMeta(index)
      if (meta.type !== 'line' || meta.hidden || !(dataset as { fill?: unknown }).fill) return
      const element = meta.dataset as unknown as { options?: Record<string, unknown> } | undefined
      const options = element?.options
      const raw = (dataset as { backgroundColor?: unknown }).backgroundColor
      const fill = typeof raw === 'string' ? parseColor(raw) : null
      if (!element || !options || !fill || fill.a >= 1 || Object.isFrozen(options)) return
      const key = `${raw}:${area.top}:${area.bottom}`
      let cached = fadeCache.get(element)
      if (!cached || cached.key !== key) {
        const alpha = Math.min(0.3, fill.a * 2.2)
        const gradient = chart.ctx.createLinearGradient(0, area.top, 0, area.bottom)
        gradient.addColorStop(0, `rgba(${fill.r}, ${fill.g}, ${fill.b}, ${alpha.toFixed(3)})`)
        gradient.addColorStop(1, `rgba(${fill.r}, ${fill.g}, ${fill.b}, 0)`)
        cached = { key, gradient }
        fadeCache.set(element, cached)
      }
      options.backgroundColor = cached.gradient
    })
  }
}
let areaFadeRegistered = false

function applyConsoleChartDefaults(styles: CSSStyleDeclaration, dark: boolean) {
  const tooltip = Chart.defaults.plugins.tooltip as unknown as Record<string, unknown>
  const legendLabels = Chart.defaults.plugins.legend.labels as unknown as Record<string, unknown>
  if (!upstreamChartDefaults) {
    upstreamChartDefaults = {
      color: Chart.defaults.color,
      borderColor: Chart.defaults.borderColor,
      fontFamily: Chart.defaults.font.family,
      lineBorderWidth: Chart.defaults.elements.line.borderWidth,
      pointRadius: Chart.defaults.elements.point.radius,
      pointHoverRadius: Chart.defaults.elements.point.hoverRadius,
      barBorderRadius: Chart.defaults.elements.bar?.borderRadius,
      doughnutCutout: Chart.overrides.doughnut.cutout,
      doughnutCutoutOwn: Object.prototype.hasOwnProperty.call(Chart.overrides.doughnut, 'cutout'),
      tooltip: Object.fromEntries(TOOLTIP_KEYS.map((key) => [key, tooltip[key]])),
      legendLabels: Object.fromEntries(LEGEND_LABEL_KEYS.map((key) => [key, legendLabels[key]]))
    }
  }
  if (!areaFadeRegistered) {
    Chart.register(areaFadePlugin)
    areaFadeRegistered = true
  }
  consoleChartsActive = true
  const font = styles.getPropertyValue('--ui-font').trim()
  if (font) Chart.defaults.font.family = font
  Chart.defaults.color = styles.getPropertyValue('--theme-chart-axis').trim() || (dark ? '#a1a1a6' : '#69696e')
  Chart.defaults.borderColor = styles.getPropertyValue('--theme-chart-grid').trim() || (dark ? '#26262a' : '#ececf0')
  Chart.defaults.elements.line.borderWidth = 2
  Chart.defaults.elements.point.radius = consolePointRadius as unknown as typeof Chart.defaults.elements.point.radius
  Chart.defaults.elements.point.hoverRadius = 4
  if (Chart.defaults.elements.bar) Chart.defaults.elements.bar.borderRadius = 4
  Chart.overrides.doughnut.cutout = '68%'
  const title = styles.getPropertyValue('--theme-chart-title').trim() || (dark ? '#f5f5f7' : '#1d1d1f')
  const body = styles.getPropertyValue('--theme-chart-body').trim() || (dark ? '#a1a1a6' : '#6e6e73')
  Object.assign(tooltip, {
    backgroundColor: dark ? 'rgba(44, 44, 46, 0.96)' : 'rgba(255, 255, 255, 0.96)',
    titleColor: title,
    bodyColor: title,
    footerColor: body,
    borderColor: dark ? 'rgba(255, 255, 255, 0.14)' : 'rgba(0, 0, 0, 0.1)',
    borderWidth: 0.5,
    cornerRadius: 10,
    padding: 10,
    boxPadding: 5,
    usePointStyle: true,
    caretSize: 0
  })
  Object.assign(legendLabels, {
    usePointStyle: true,
    pointStyle: 'circle',
    boxWidth: 7,
    boxHeight: 7,
    generateLabels: consoleLegendLabels
  })
}

function restoreUpstreamChartDefaults() {
  consoleChartsActive = false
  if (!upstreamChartDefaults) return
  Chart.defaults.color = upstreamChartDefaults.color
  Chart.defaults.borderColor = upstreamChartDefaults.borderColor
  Chart.defaults.font.family = upstreamChartDefaults.fontFamily
  Chart.defaults.elements.line.borderWidth = upstreamChartDefaults.lineBorderWidth
  Chart.defaults.elements.point.radius = upstreamChartDefaults.pointRadius
  Chart.defaults.elements.point.hoverRadius = upstreamChartDefaults.pointHoverRadius
  if (Chart.defaults.elements.bar) {
    Chart.defaults.elements.bar.borderRadius = (upstreamChartDefaults.barBorderRadius ?? 0) as typeof Chart.defaults.elements.bar.borderRadius
  }
  if (upstreamChartDefaults.doughnutCutoutOwn) Chart.overrides.doughnut.cutout = upstreamChartDefaults.doughnutCutout
  else delete (Chart.overrides.doughnut as unknown as Record<string, unknown>).cutout
  Object.assign(Chart.defaults.plugins.tooltip as unknown as Record<string, unknown>, upstreamChartDefaults.tooltip)
  Object.assign(Chart.defaults.plugins.legend.labels as unknown as Record<string, unknown>, upstreamChartDefaults.legendLabels)
  upstreamChartDefaults = null
}

// Canvas consumers need resolved colors. CSS custom properties keep optional
// presentation policy outside their data, chart and billing logic.
const defaults = {
  text: ['#374151', '#e5e7eb'], grid: ['#e5e7eb', '#374151'],
  axis: ['#6b7280', '#9ca3af'], axisGrid: ['#f3f4f6', '#374151'],
  tooltip: ['#ffffff', '#1f2937'], title: ['#111827', '#f3f4f6'],
  body: ['#4b5563', '#d1d5db'], gray: ['#9ca3af', '#9ca3af']
} as const
// Categorical series (distribution charts, token trend). Defaults are the upstream palette; the
// console theme supplies --theme-chart-series-1..12 as #rrggbb (consumers append hex alpha).
const seriesDefaults = [
  '#3b82f6', '#10b981', '#f59e0b', '#ef4444', '#8b5cf6', '#ec4899',
  '#14b8a6', '#f97316', '#6366f1', '#84cc16', '#06b6d4', '#a855f7'
] as const
// Colour by hue: upstream charts draw with Tailwind literals (a 500 shade; gray-400 for "other"). With the console
// theme off tone() / toneText() return the literal itself, so the upstream colours stay exact. Under the theme the
// literal's hue family (the families of tailwind.config.js) resolves to the matching chart series token for marks
// (lines, bars, arcs, dots, SVG strokes; >= 3:1 on the chart background) and to the family's text role for text
// drawn in that colour (axis ticks and titles, labels; >= 4.5:1). The literal's alpha and notation are kept, so
// `${hex}20` fills and rgba() fills still fade and still read as translucent in the legend.
type HueFamily = 'info' | 'success' | 'warning' | 'danger' | 'purple' | 'pink' | 'teal' | 'indigo' | 'gray'
const HUES: ReadonlyArray<readonly [number, number, number, HueFamily]> = [
  [59, 130, 246, 'info'], // blue
  [16, 185, 129, 'success'], [34, 197, 94, 'success'], [132, 204, 22, 'success'], // emerald, green, lime
  [245, 158, 11, 'warning'], [249, 115, 22, 'warning'], [234, 179, 8, 'warning'], // amber, orange, yellow
  [239, 68, 68, 'danger'], [244, 63, 94, 'danger'], // red, rose
  [139, 92, 246, 'purple'], [168, 85, 247, 'purple'], [217, 70, 239, 'purple'], // violet, purple, fuchsia
  [236, 72, 153, 'pink'],
  [20, 184, 166, 'teal'], [6, 182, 212, 'teal'], [14, 165, 233, 'teal'], // teal, cyan, sky
  [99, 102, 241, 'indigo'],
  [156, 163, 175, 'gray'] // gray-400
]
const MARK_TOKENS: Record<HueFamily, string> = {
  info: '--theme-chart-series-1', success: '--theme-chart-series-2', warning: '--theme-chart-series-3',
  danger: '--theme-chart-series-4', purple: '--theme-chart-series-5', pink: '--theme-chart-series-6',
  teal: '--theme-chart-series-7', indigo: '--theme-chart-series-9', gray: '--theme-chart-gray'
}
const textToken = (family: HueFamily) => family === 'gray' ? '--theme-text-gray-400' : `--theme-text-${family}-500`
type HuePalette = Partial<Record<HueFamily, ParsedColor>>

function hueFamily(color: ParsedColor): HueFamily | undefined {
  // a channel or two of tolerance: some upstream literals are off by one (rgba(239, 67, 67, ...) for red-500)
  return HUES.find(([r, g, b]) => Math.abs(r - color.r) <= 2 && Math.abs(g - color.g) <= 2 && Math.abs(b - color.b) <= 2)?.[3]
}

function retone(literal: string, palette: HuePalette): string {
  const color = parseColor(literal)
  const family = color && hueFamily(color)
  const target = family ? palette[family] : undefined
  if (!color || !target) return literal
  const hex = (value: number) => value.toString(16).padStart(2, '0')
  if (color.hex) return `#${hex(target.r)}${hex(target.g)}${hex(target.b)}${color.alpha ?? ''}`
  return color.alpha === null ? `rgb(${target.r}, ${target.g}, ${target.b})` : `rgba(${target.r}, ${target.g}, ${target.b}, ${color.alpha})`
}

type Colors = Record<keyof typeof defaults, string> & {
  series: string[]
  // semantic marks (health, errors, success rates): upstream's blue / emerald / amber / red 500
  info: string
  success: string
  warning: string
  danger: string
  tone: (literal: string) => string
  toneText: (literal: string) => string
}

function withPalette(base: Record<keyof typeof defaults, string> & { series: string[] }, marks: HuePalette, inks: HuePalette): Colors {
  const tone = (literal: string) => retone(literal, marks)
  return {
    ...base,
    info: tone('#3b82f6'),
    success: tone('#10b981'),
    warning: tone('#f59e0b'),
    danger: tone('#ef4444'),
    tone,
    toneText: (literal: string) => retone(literal, inks)
  }
}

export function usePresentationColors() {
  const colors = shallowRef<Colors>(withPalette({
    ...(Object.fromEntries(Object.entries(defaults).map(([key, values]) => [key, values[0]])) as Record<keyof typeof defaults, string>),
    series: [...seriesDefaults]
  }, {}, {}))
  // (the palette signature starts as the theme-off one: no hue family resolved)
  let observer: MutationObserver | null = null, active = false, queued = false, paletteKey = '[{},{}]'
  const read = () => {
    queued = false
    if (!active) return
    const root = document.documentElement, styles = getComputedStyle(root), dark = root.classList.contains('dark')
    if (root.classList.contains('flat-theme')) applyConsoleChartDefaults(styles, dark)
    else restoreUpstreamChartDefaults()
    const isColor = (value: string) => !!value && typeof CSS !== 'undefined' && CSS.supports('color', value)
    const next: Record<keyof typeof defaults, string> & { series: string[] } = { ...colors.value }
    for (const key of Object.keys(defaults) as Array<keyof typeof defaults>) {
      const value = styles.getPropertyValue(`--theme-chart-${key.replace(/[A-Z]/g, letter => '-' + letter.toLowerCase())}`).trim()
      next[key] = isColor(value) ? value : defaults[key][dark ? 1 : 0]
    }
    next.series = seriesDefaults.map((fallback, index) => {
      const value = styles.getPropertyValue(`--theme-chart-series-${index + 1}`).trim()
      return /^#[0-9a-f]{6}$/i.test(value) && isColor(value) ? value : fallback
    })
    // hue families resolve only where the theme defines their tokens; otherwise tone() keeps the literal
    const marks: HuePalette = {}, inks: HuePalette = {}
    for (const family of Object.keys(MARK_TOKENS) as HueFamily[]) {
      const mark = styles.getPropertyValue(MARK_TOKENS[family]).trim()
      if (/^#[0-9a-f]{6}$/i.test(mark) && isColor(mark)) marks[family] = parseColor(mark) ?? undefined
      const ink = /^(\d{1,3})\s+(\d{1,3})\s+(\d{1,3})$/.exec(styles.getPropertyValue(textToken(family)).trim())
      if (ink) inks[family] = parseColor(`rgb(${ink[1]}, ${ink[2]}, ${ink[3]})`) ?? undefined
    }
    const nextPaletteKey = JSON.stringify([marks, inks])
    const changed = nextPaletteKey !== paletteKey ||
      (Object.keys(defaults) as Array<keyof typeof defaults>).some(key => next[key] !== colors.value[key]) ||
      next.series.some((value, index) => value !== colors.value.series[index])
    paletteKey = nextPaletteKey
    if (changed) colors.value = withPalette(next, marks, inks)
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
