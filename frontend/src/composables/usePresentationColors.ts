import { onBeforeUnmount, onMounted, shallowRef } from 'vue'
import { Chart } from 'chart.js'

// Chart.js takes resolved values, not CSS. While the console theme is on (html.flat-theme), canvas text uses the
// UI font stack and the text and grid lines a chart does not colour itself take the theme's axis and grid tokens
// (--theme-chart-axis, --theme-chart-grid in styles/console/tokens.css); everything else stays Chart.js' default.
// Chart.js' own defaults are saved before the first change and restored when the theme is switched off at
// runtime. Charts re-resolve these defaults on the update that follows the next color read.
type ChartDefaultsSnapshot = {
  color: typeof Chart.defaults.color
  borderColor: typeof Chart.defaults.borderColor
  fontFamily: typeof Chart.defaults.font.family
}
let upstreamChartDefaults: ChartDefaultsSnapshot | null = null

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

function applyConsoleChartDefaults(styles: CSSStyleDeclaration) {
  if (!upstreamChartDefaults) {
    upstreamChartDefaults = {
      color: Chart.defaults.color,
      borderColor: Chart.defaults.borderColor,
      fontFamily: Chart.defaults.font.family
    }
  }
  const token = (name: string) => styles.getPropertyValue(name).trim()
  Chart.defaults.font.family = token('--ui-font') || upstreamChartDefaults.fontFamily
  Chart.defaults.color = token('--theme-chart-axis') || upstreamChartDefaults.color
  Chart.defaults.borderColor = token('--theme-chart-grid') || upstreamChartDefaults.borderColor
}

function restoreUpstreamChartDefaults() {
  if (!upstreamChartDefaults) return
  Chart.defaults.color = upstreamChartDefaults.color
  Chart.defaults.borderColor = upstreamChartDefaults.borderColor
  Chart.defaults.font.family = upstreamChartDefaults.fontFamily
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
    if (root.classList.contains('flat-theme')) applyConsoleChartDefaults(styles)
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
