import { onBeforeUnmount, onMounted, shallowRef } from 'vue'
import { Chart } from 'chart.js'
import type { Plugin } from 'chart.js'

// Chart.js takes resolved values, not CSS. While the console theme is on (html.flat-theme), the chart defaults follow
// the Experiential Labs console's charts (ui-el REF-CONSOLE §8.14): the UI font at 11px, axis labels in the muted
// text colour, one 1px structural gridline per value step and none across the categories, no axis lines or tick
// marks, three value steps, level labels (the ones that do not fit are skipped); 1.5px lines without dots (a dot on
// hover), a thinner ring; legends under the plot at its left edge, with 8px squares (2px corners) in the series'
// solid colour and the faint text colour; tooltips as the reference's popover (surface, 1px line, 6px radius, 12 / 10 inset, 11px, the popover
// shadow, no caret, solid colour keys). A chart's own options still win over these defaults.
// Every default changed is saved first and restored when the theme is switched off at runtime. Charts re-resolve the
// defaults on the update that follows the next colour read.

// ------------------------------------------------------------------ saved Chart.js defaults
type Saved = { node: Record<string, any>; key: string; kind: 'route' | 'own' | 'absent'; value: unknown }
let savedDefaults: Saved[] | null = null
let consoleCharts = false

// A defaults path such as ['plugins', 'tooltip', 'titleFont']. Chart.js routes some options to others (a getter /
// setter pair backed by a `_name` value, e.g. tooltip fonts -> font): those are saved and restored through the
// backing value, so the route keeps working after a restore. The registry scopes (scales.<type>, datasets.<type>,
// elements.<type>, plugins.<id>) are never created here: a scope that is not registered yet is skipped and picked up
// by the next colour read.
function setDefault(path: readonly string[], value: unknown, saved: Saved[]) {
  let node: Record<string, any> = Chart.defaults as unknown as Record<string, any>
  for (const [depth, part] of path.slice(0, -1).entries()) {
    if (node[part] === undefined) {
      if (depth < 2) return
      saved.push({ node, key: part, kind: 'absent', value: undefined })
      node[part] = {}
    }
    node = node[part]
    if (!node || typeof node !== 'object') return
  }
  const key = path[path.length - 1]
  const descriptor = Object.getOwnPropertyDescriptor(node, key)
  if (descriptor?.get && descriptor.set && Object.prototype.hasOwnProperty.call(node, `_${key}`)) {
    saved.push({ node, key, kind: 'route', value: node[`_${key}`] })
  } else if (descriptor) {
    saved.push({ node, key, kind: 'own', value: node[key] })
  } else {
    saved.push({ node, key, kind: 'absent', value: undefined })
  }
  node[key] = value
}

function restoreDefaults() {
  if (!savedDefaults) return
  for (const entry of [...savedDefaults].reverse()) {
    if (entry.kind === 'absent') delete entry.node[entry.key]
    else entry.node[entry.key] = entry.value
  }
  savedDefaults = null
}

// ------------------------------------------------------------------ legend squares and the tooltip shadow
type LegendItemLike = { fillStyle?: unknown; strokeStyle?: unknown; lineWidth?: number; pointStyle?: unknown; borderRadius?: unknown }
const opaque = (value: unknown) => typeof value === 'string' && !/^#[0-9a-f]{6}[0-9a-f]{2}$/i.test(value) && !/^rgba\(.*,\s*0?\.\d+\s*\)$/i.test(value)

// Legend items take the series' solid colour as an 8px square with 2px corners (a line series' translucent area
// fill is not its colour), whether a chart asked for point styles or boxes.
function consoleLegendLabels(generate: (chart: Chart) => LegendItemLike[]) {
  return function (this: unknown, chart: Chart) {
    const items = generate.call(this, chart)
    if (!consoleCharts) return items
    return items.map((item) => ({
      ...item,
      fillStyle: opaque(item.fillStyle) ? item.fillStyle : (item.strokeStyle ?? item.fillStyle),
      lineWidth: 0,
      pointStyle: 'rectRounded',
      borderRadius: 2
    }))
  }
}

// The tooltip's colour keys are the series' solid colour too, as 8px squares with 2px corners.
type LabelColorLike = { borderColor?: unknown; backgroundColor?: unknown; borderWidth?: unknown; borderRadius?: unknown }
function consoleTooltipLabelColor(labelColor: (item: unknown) => LabelColorLike) {
  return function (this: unknown, item: unknown) {
    const color = labelColor.call(this, item)
    if (!consoleCharts || !color) return color
    const solid = opaque(color.backgroundColor) ? color.backgroundColor : (color.borderColor ?? color.backgroundColor)
    return { ...color, backgroundColor: solid, borderColor: solid, borderWidth: 0, borderRadius: 2 }
  }
}

let tooltipShadowColor = 'rgba(20, 20, 18, 0.08)'
// Chart.js draws no tooltip shadow: the popover shadow (0 6px 20px) is painted as a rounded surface under the
// tooltip before Chart.js draws the tooltip itself.
const consoleTooltipShadow: Plugin = {
  id: 'consoleTooltipShadow',
  beforeTooltipDraw(chart, args) {
    const tooltip = args.tooltip as unknown as { opacity: number; x: number; y: number; width: number; height: number; options: { backgroundColor?: unknown; cornerRadius?: unknown } }
    if (!consoleCharts || !tooltip || !tooltip.opacity) return
    const ctx = chart.ctx
    const radius = typeof tooltip.options.cornerRadius === 'number' ? tooltip.options.cornerRadius : 6
    ctx.save()
    ctx.globalAlpha = tooltip.opacity
    ctx.shadowColor = tooltipShadowColor
    ctx.shadowBlur = 20
    ctx.shadowOffsetY = 6
    ctx.fillStyle = typeof tooltip.options.backgroundColor === 'string' ? tooltip.options.backgroundColor : '#ffffff'
    ctx.beginPath()
    ctx.roundRect(tooltip.x, tooltip.y, tooltip.width, tooltip.height, radius)
    ctx.fill()
    ctx.restore()
  }
}
let tooltipShadowRegistered = false

// Legends sit under the plot, at its left edge (REF-CONSOLE §8.14: the /logs chart's legend). Upstream's line charts
// ask for position 'top' in their own options, which win over the defaults, so while the console charts are on this
// plugin moves every shown legend to bottom / start before the layout pass (the legend plugin may have configured
// its box earlier in the same update, so the box is moved too). The chart's own choice is kept on the chart and put
// back on the first update after the theme is switched off.
type LegendPlacement = { position: unknown; align: unknown }
type LegendOptionsLike = { display?: unknown; position?: unknown; align?: unknown }
const consoleLegendPlacement: Plugin = {
  id: 'consoleLegendPlacement',
  beforeUpdate(chart) {
    const legend = (chart.options.plugins as unknown as { legend?: LegendOptionsLike } | undefined)?.legend
    if (!legend) return
    const holder = chart as unknown as { $consoleLegendPlacement?: LegendPlacement; legend?: { position?: unknown } }
    if (consoleCharts && legend.display !== false) {
      if (!holder.$consoleLegendPlacement) holder.$consoleLegendPlacement = { position: legend.position, align: legend.align }
      legend.position = 'bottom'
      legend.align = 'start'
      if (holder.legend) holder.legend.position = 'bottom'
    } else if (!consoleCharts && holder.$consoleLegendPlacement) {
      const own = holder.$consoleLegendPlacement
      legend.position = own.position
      legend.align = own.align
      if (holder.legend) holder.legend.position = own.position
      delete holder.$consoleLegendPlacement
    }
  }
}

function applyConsoleChartDefaults(styles: CSSStyleDeclaration, dark: boolean) {
  const token = (name: string) => styles.getPropertyValue(name).trim()
  const rgb = (name: string, fallback: string) => {
    const value = /^(\d{1,3})\s+(\d{1,3})\s+(\d{1,3})$/.exec(token(name))
    return value ? `rgb(${value[1]}, ${value[2]}, ${value[3]})` : fallback
  }
  restoreDefaults()
  const saved: Saved[] = []
  const surface = rgb('--ui-surface', dark ? '#1a1a1a' : '#ffffff')
  const line = token('--theme-chart-grid') || rgb('--ui-line', '#ededed')
  const font = token('--ui-font')
  // text and lines
  if (font) setDefault(['font', 'family'], font, saved)
  setDefault(['font', 'size'], 11, saved)
  setDefault(['color'], token('--theme-chart-axis') || rgb('--ui-muted', '#6c6c6c'), saved)
  setDefault(['borderColor'], line, saved)
  // scales: value gridlines only, no axis lines or tick marks, labels 10px, three steps
  setDefault(['scale', 'border', 'display'], false, saved)
  setDefault(['scale', 'grid', 'drawTicks'], false, saved)
  setDefault(['scale', 'ticks', 'padding'], 8, saved)
  setDefault(['scale', 'ticks', 'font'], { size: 10 }, saved)
  // labels stay level: the ones that do not fit are skipped (the reference labels only a few dates)
  setDefault(['scale', 'ticks', 'maxRotation'], 0, saved)
  setDefault(['scale', 'ticks', 'autoSkipPadding'], 24, saved)
  setDefault(['scales', 'category', 'grid', 'display'], false, saved)
  setDefault(['scales', 'linear', 'ticks', 'maxTicksLimit'], 3, saved)
  // marks
  setDefault(['elements', 'line', 'borderWidth'], 1.5, saved)
  setDefault(['elements', 'point', 'radius'], 0, saved)
  setDefault(['elements', 'point', 'hoverRadius'], 3, saved)
  setDefault(['elements', 'point', 'hitRadius'], 8, saved)
  setDefault(['elements', 'arc', 'borderColor'], surface, saved)
  setDefault(['elements', 'bar', 'borderRadius'], 0, saved)
  setDefault(['datasets', 'doughnut', 'cutout'], '70%', saved)
  setDefault(['datasets', 'bar', 'categoryPercentage'], 0.96, saved)
  setDefault(['datasets', 'bar', 'barPercentage'], 1, saved)
  // legend
  const legend = (Chart.defaults.plugins as unknown as { legend?: { labels?: { generateLabels?: (chart: Chart) => LegendItemLike[] } } }).legend
  const generate = legend?.labels?.generateLabels
  setDefault(['plugins', 'legend', 'labels', 'boxWidth'], 8, saved)
  setDefault(['plugins', 'legend', 'labels', 'boxHeight'], 8, saved)
  setDefault(['plugins', 'legend', 'labels', 'padding'], 12, saved)
  setDefault(['plugins', 'legend', 'labels', 'useBorderRadius'], true, saved)
  setDefault(['plugins', 'legend', 'labels', 'borderRadius'], 2, saved)
  setDefault(['plugins', 'legend', 'labels', 'color'], rgb('--ui-faint', '#6c6c6c'), saved)
  if (generate) setDefault(['plugins', 'legend', 'labels', 'generateLabels'], consoleLegendLabels(generate), saved)
  // tooltip: the reference's popover
  const tooltip = (Chart.defaults.plugins as unknown as { tooltip?: { callbacks?: { labelColor?: (item: unknown) => LabelColorLike } } }).tooltip
  const labelColor = tooltip?.callbacks?.labelColor
  if (labelColor) setDefault(['plugins', 'tooltip', 'callbacks', 'labelColor'], consoleTooltipLabelColor(labelColor), saved)
  setDefault(['plugins', 'tooltip', 'backgroundColor'], token('--theme-chart-tooltip') || surface, saved)
  setDefault(['plugins', 'tooltip', 'borderColor'], line, saved)
  setDefault(['plugins', 'tooltip', 'borderWidth'], 1, saved)
  setDefault(['plugins', 'tooltip', 'cornerRadius'], 6, saved)
  setDefault(['plugins', 'tooltip', 'padding'], { top: 10, right: 12, bottom: 10, left: 12 }, saved)
  setDefault(['plugins', 'tooltip', 'caretSize'], 0, saved)
  setDefault(['plugins', 'tooltip', 'caretPadding'], 8, saved)
  setDefault(['plugins', 'tooltip', 'titleColor'], token('--theme-chart-title') || rgb('--ui-ink', '#0a0a0a'), saved)
  setDefault(['plugins', 'tooltip', 'bodyColor'], token('--theme-chart-text') || rgb('--ui-ink-soft', '#5d5d5d'), saved)
  setDefault(['plugins', 'tooltip', 'footerColor'], rgb('--ui-faint', '#6c6c6c'), saved)
  setDefault(['plugins', 'tooltip', 'titleFont'], { size: 11, weight: 600 }, saved)
  setDefault(['plugins', 'tooltip', 'bodyFont'], { size: 11 }, saved)
  setDefault(['plugins', 'tooltip', 'footerFont'], { size: 11, weight: 400 }, saved)
  setDefault(['plugins', 'tooltip', 'titleMarginBottom'], 8, saved)
  setDefault(['plugins', 'tooltip', 'bodySpacing'], 6, saved)
  setDefault(['plugins', 'tooltip', 'footerMarginTop'], 8, saved)
  setDefault(['plugins', 'tooltip', 'boxWidth'], 8, saved)
  setDefault(['plugins', 'tooltip', 'boxHeight'], 8, saved)
  setDefault(['plugins', 'tooltip', 'boxPadding'], 6, saved)
  setDefault(['plugins', 'tooltip', 'multiKeyBackground'], surface, saved)
  savedDefaults = saved
  tooltipShadowColor = dark ? 'rgba(0, 0, 0, 0.55)' : 'rgba(20, 20, 18, 0.08)'
  if (!tooltipShadowRegistered) {
    Chart.register(consoleTooltipShadow, consoleLegendPlacement)
    tooltipShadowRegistered = true
  }
  consoleCharts = true
}

function restoreUpstreamChartDefaults() {
  consoleCharts = false
  restoreDefaults()
}

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

// Canvas consumers need resolved colors. CSS custom properties keep optional
// presentation policy outside their data, chart and billing logic.
const defaults = {
  text: ['#374151', '#e5e7eb'], grid: ['#e5e7eb', '#374151'],
  axis: ['#6b7280', '#9ca3af'], axisGrid: ['#f3f4f6', '#374151'],
  tooltip: ['#ffffff', '#1f2937'], title: ['#111827', '#f3f4f6'],
  body: ['#4b5563', '#d1d5db'], gray: ['#9ca3af', '#9ca3af']
} as const
// Categorical series (distribution charts: slices of a whole). Defaults are the upstream palette; the console theme
// supplies --theme-chart-series-1..12 as #rrggbb (consumers append hex alpha): the reference's usage-by-model colours
// (ink, accent, indigo, amber, cyan) first.
const seriesDefaults = [
  '#3b82f6', '#10b981', '#f59e0b', '#ef4444', '#8b5cf6', '#ec4899',
  '#14b8a6', '#f97316', '#6366f1', '#84cc16', '#06b6d4', '#a855f7'
] as const
// Time series (trend lines): the same upstream palette by default; the console theme supplies --ui-chart-trend-1..12
// (styles/console/data.css): the reference's usage series in upstream's hue order, so an index keeps its hue.
// Colour by hue: upstream charts draw with Tailwind literals (a 500 shade; gray-400 for "other"). With the console
// theme off tone() / toneText() return the literal itself, so the upstream colours stay exact. Under the theme the
// literal's hue family (the family tailwind.config.js folds it into) resolves to that family's chart mark token
// for marks (lines, bars, arcs, dots, SVG strokes; >= 3:1 on the chart background) and to the family's text role
// for text drawn in that colour (axis ticks and titles, labels; >= 4.5:1). The literal's alpha and notation are
// kept, so `${hex}20` fills and rgba() fills still fade and still read as translucent in the legend.
type HueFamily = 'info' | 'success' | 'warning' | 'danger' | 'purple' | 'gray'
const HUES: ReadonlyArray<readonly [number, number, number, HueFamily]> = [
  [59, 130, 246, 'info'], [14, 165, 233, 'info'], [6, 182, 212, 'info'], [99, 102, 241, 'info'], // blue, sky, cyan, indigo
  [16, 185, 129, 'success'], [34, 197, 94, 'success'], [132, 204, 22, 'success'], [20, 184, 166, 'success'], // emerald, green, lime, teal
  [245, 158, 11, 'warning'], [249, 115, 22, 'warning'], [234, 179, 8, 'warning'], // amber, orange, yellow
  [239, 68, 68, 'danger'], [244, 63, 94, 'danger'], // red, rose
  [139, 92, 246, 'purple'], [168, 85, 247, 'purple'], [217, 70, 239, 'purple'], [236, 72, 153, 'purple'], // violet, purple, fuchsia, pink
  [156, 163, 175, 'gray'] // gray-400
]
const MARK_TOKENS: Record<HueFamily, string> = {
  info: '--theme-chart-info', success: '--theme-chart-success', warning: '--theme-chart-warning',
  danger: '--theme-chart-danger', purple: '--theme-chart-purple', gray: '--theme-chart-gray'
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

type Base = Record<keyof typeof defaults, string> & { series: string[]; trend: string[] }
type Colors = Base & {
  // semantic marks (health, errors, success rates): upstream's blue / emerald / amber / red 500
  info: string
  success: string
  warning: string
  danger: string
  tone: (literal: string) => string
  toneText: (literal: string) => string
}

function withPalette(base: Base, marks: HuePalette, inks: HuePalette): Colors {
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
    series: [...seriesDefaults],
    trend: [...seriesDefaults]
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
    const next: Base = { ...colors.value }
    for (const key of Object.keys(defaults) as Array<keyof typeof defaults>) {
      const value = styles.getPropertyValue(`--theme-chart-${key.replace(/[A-Z]/g, letter => '-' + letter.toLowerCase())}`).trim()
      next[key] = isColor(value) ? value : defaults[key][dark ? 1 : 0]
    }
    const palette = (prefix: string) => seriesDefaults.map((fallback, index) => {
      const value = styles.getPropertyValue(`${prefix}${index + 1}`).trim()
      return /^#[0-9a-f]{6}$/i.test(value) && isColor(value) ? value : fallback
    })
    next.series = palette('--theme-chart-series-')
    next.trend = palette('--ui-chart-trend-')
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
      next.series.some((value, index) => value !== colors.value.series[index]) ||
      next.trend.some((value, index) => value !== colors.value.trend[index])
    paletteKey = nextPaletteKey
    if (changed) colors.value = withPalette(next, marks, inks)
  }
  const schedule = () => { if (active && !queued) { queued = true; queueMicrotask(read) } }
  // The first read runs in setup: a chart in this component's template (vue-chartjs builds it in its own onMounted,
  // which runs before this component's) is then created with the console defaults instead of Chart.js' own.
  if (typeof document !== 'undefined') {
    active = true
    read()
  }
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
