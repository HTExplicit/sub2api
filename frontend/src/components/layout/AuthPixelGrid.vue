<template>
  <div v-if="flatThemeActive" ref="rootEl" class="auth-pixel-grid" aria-hidden="true">
    <canvas ref="canvasEl"></canvas>
  </div>
</template>

<script setup lang="ts">
// Console theme only (utils/flatTheme.ts): the sign-in pages' background, after the reference console's sign-in
// page. A field of 7px squares on an 8px pitch in the ink colour at five faint strengths covers the page; a soft,
// ragged front keeps sweeping in from the left edge, lights the squares it crosses and leaves them to fade, so a band
// on the left shimmers (on a phone it reaches across the page); everything else stays at the faintest strength.
// The canvas is drawn at one canvas pixel per CSS pixel (soft edges on dense screens, as in the reference); the
// layer's 40 % opacity and colour come from styles/console/pages/settings-ops-public.css.
// Under the text of the content column the squares stay at the faintest strength, so every label, hint and link
// keeps its contrast; fields and buttons are opaque and cover the field anyway.
// prefers-reduced-motion: one still frame, no animation. Theme off: nothing is rendered.
import { onBeforeUnmount, ref, watch } from 'vue'
import { flatThemeActive } from '@/utils/flatTheme'

const PITCH = 8
const CELL = 7
const LIGHT_LEVELS = [0.02, 0.06, 0.11, 0.17, 0.24]
const DARK_LEVELS = [0.03, 0.08, 0.16, 0.28, 0.42]
// Squares beyond this many columns never light up and are drawn once.
const LIVE_COLS = 72
// The front: its half-width in columns, speed in columns per second, how far one sweep travels (columns; a narrow
// page lets it cross most of the width), and how strongly it lights a square (plus a per-square shimmer).
const FRONT_HALF = 10
const FRONT_SPEED = 11
const REACH_MIN = 10
const REACH_MAX = 28
const NARROW_COLS = 64
const LIGHT = 2.7
const SHIMMER = 0.7
// Strength per second: rising under the front, the lit target and the shown value fading once it has passed.
const RISE = 3
const FADE = 0.9
// Text keeps a clear margin (px) and a soft edge (px) around it.
const TEXT_PAD = 3
const TEXT_FEATHER = 8

const rootEl = ref<HTMLElement | null>(null)
const canvasEl = ref<HTMLCanvasElement | null>(null)

let ctx: CanvasRenderingContext2D | null = null
let cols = 0
let rows = 0
let liveCols = 0
let value = new Float32Array(0)
let target = new Float32Array(0)
let phase = new Float32Array(0)
let quiet = new Float32Array(0)
let fills: string[] = []
let front = 0
let frontEnd = 0
let lastTime = 0
let lastDraw = 0
let raf = 0
let measureRaf = 0
let reduced = false
let resizeObserver: ResizeObserver | null = null
let themeObserver: MutationObserver | null = null
let textObserver: MutationObserver | null = null
let motionQuery: MediaQueryList | null = null

function hash(a: number, b: number): number {
  let h = Math.imul(a + 0x3c6ef372, 0x9e3779b1) ^ Math.imul(b + 0x632be5ab, 0x85ebca77)
  h = Math.imul(h ^ (h >>> 15), 0x2c1b3c6d)
  h = Math.imul(h ^ (h >>> 12), 0x297a2d39)
  return ((h ^ (h >>> 15)) >>> 0) / 4294967296
}

function column(): HTMLElement | null {
  return rootEl.value?.parentElement?.querySelector<HTMLElement>(':scope > [data-ui="auth-column"]') ?? null
}

function readFills(): void {
  const el = rootEl.value
  if (!el) return
  const m = /rgba?\(\s*(\d+)[\s,]+(\d+)[\s,]+(\d+)/.exec(getComputedStyle(el).color)
  const dark = document.documentElement.classList.contains('dark')
  const rgb = m ? `${m[1]}, ${m[2]}, ${m[3]}` : dark ? '242, 242, 242' : '10, 10, 10'
  fills = (dark ? DARK_LEVELS : LIGHT_LEVELS).map((a) => `rgba(${rgb}, ${a})`)
}

// 1 = free to light up, 0 = under text (faintest strength), soft in between.
function measureQuiet(): void {
  const el = rootEl.value
  quiet = new Float32Array(cols * rows).fill(1)
  const col = column()
  if (!el || !col || !cols || !rows) return
  const base = el.getBoundingClientRect()
  const walker = document.createTreeWalker(col, NodeFilter.SHOW_TEXT)
  const range = document.createRange()
  const reach = TEXT_PAD + TEXT_FEATHER
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    if (!node.nodeValue || !node.nodeValue.trim()) continue
    range.selectNodeContents(node)
    for (const r of Array.from(range.getClientRects())) {
      if (r.width < 1 || r.height < 1) continue
      const left = r.left - base.left
      const right = r.right - base.left
      const top = r.top - base.top
      const bottom = r.bottom - base.top
      const c0 = Math.max(0, Math.floor((left - reach) / PITCH))
      const c1 = Math.min(cols - 1, Math.floor((right + reach) / PITCH))
      const r0 = Math.max(0, Math.floor((top - reach) / PITCH))
      const r1 = Math.min(rows - 1, Math.floor((bottom + reach) / PITCH))
      for (let y = r0; y <= r1; y++) {
        const cy0 = y * PITCH
        const dy = Math.max(top - (cy0 + CELL), cy0 - bottom, 0)
        for (let x = c0; x <= c1; x++) {
          const cx0 = x * PITCH
          const dx = Math.max(left - (cx0 + CELL), cx0 - right, 0)
          const d = Math.hypot(dx, dy) - TEXT_PAD
          const q = d <= 0 ? 0 : Math.min(1, d / TEXT_FEATHER)
          const i = y * cols + x
          if (q < quiet[i]) quiet[i] = q
        }
      }
    }
  }
}

function levelOf(i: number): number {
  const v = value[i] * quiet[i]
  return v > 0 ? Math.min(4, Math.floor(v)) : 0
}

function addCell(path: Path2D, c: number, r: number): void {
  const x = c * PITCH
  const y = r * PITCH
  if (typeof path.roundRect === 'function') path.roundRect(x, y, CELL, CELL, 1)
  else path.rect(x, y, CELL, CELL)
}

function draw(toCol: number): void {
  if (!ctx || !cols || !rows || fills.length !== 5) return
  ctx.clearRect(0, 0, toCol * PITCH, rows * PITCH)
  const paths = fills.map(() => new Path2D())
  for (let r = 0; r < rows; r++) {
    for (let c = 0; c < toCol; c++) addCell(paths[c < liveCols ? levelOf(r * cols + c) : 0], c, r)
  }
  paths.forEach((path, level) => {
    if (!ctx) return
    ctx.fillStyle = fills[level]
    ctx.fill(path)
  })
}

function resize(): void {
  const el = rootEl.value
  const canvas = canvasEl.value
  if (!el || !canvas) return
  const nextCols = Math.floor(el.clientWidth / PITCH)
  const nextRows = Math.floor(el.clientHeight / PITCH)
  if (nextCols !== cols || nextRows !== rows) {
    cols = nextCols
    rows = nextRows
    liveCols = Math.min(cols, LIVE_COLS)
    canvas.width = cols * PITCH
    canvas.height = rows * PITCH
    value = new Float32Array(cols * rows)
    target = new Float32Array(cols * rows)
    phase = new Float32Array(cols * rows)
    for (let i = 0; i < phase.length; i++) phase[i] = hash(i % cols, Math.floor(i / cols)) * Math.PI * 2
  }
  measureQuiet()
  draw(cols)
}

function newSweep(): void {
  const narrow = cols < NARROW_COLS
  const lo = narrow ? cols * 0.35 : REACH_MIN
  const hi = narrow ? cols : REACH_MAX
  front = -FRONT_HALF - 2
  frontEnd = lo + Math.random() * (hi - lo)
}

function step(now: number, dt: number): void {
  if (front > frontEnd) newSweep()
  else front += FRONT_SPEED * dt
  const t = now / 1000
  for (let r = 0; r < rows; r++) {
    const wobble = 2.4 * Math.sin(r * 0.37 + t * 0.6) + 1.2 * Math.sin(r * 1.13)
    for (let c = 0; c < liveCols; c++) {
      const i = r * cols + c
      const d = Math.abs(c - front + wobble + (phase[i] - Math.PI) * 0.8)
      if (d < FRONT_HALF) {
        const lit = (1 - d / FRONT_HALF) * LIGHT + SHIMMER * (1 + Math.sin(phase[i] * 3 + t * 1.9))
        if (lit > target[i]) target[i] = Math.min(4.2, lit)
      } else if (target[i] > 0) {
        target[i] = Math.max(0, target[i] - FADE * dt)
      }
      const v = value[i]
      const g = target[i]
      value[i] = v < g ? Math.min(g, v + RISE * dt) : Math.max(g, v - FADE * dt)
    }
  }
}

function frame(now: number): void {
  raf = requestAnimationFrame(frame)
  // rAF time can precede the moment the loop was armed: never step backwards
  const dt = Math.max(0, Math.min(0.08, (now - (lastTime || now)) / 1000))
  lastTime = now
  step(now, dt)
  if (now - lastDraw >= 33) {
    lastDraw = now
    draw(liveCols)
  }
}

// Reduced motion: six seconds of the sweep computed at once, then left still.
function stillFrame(): void {
  newSweep()
  for (let k = 0; k < 180; k++) step(k * 33.3, 1 / 30)
  draw(cols)
}

function scheduleMeasure(): void {
  if (measureRaf) return
  measureRaf = requestAnimationFrame(() => {
    measureRaf = 0
    measureQuiet()
    if (reduced || !raf) draw(cols)
  })
}

function start(): void {
  stop()
  const canvas = canvasEl.value
  const el = rootEl.value
  if (!canvas || !el || typeof window === 'undefined') return
  try {
    ctx = canvas.getContext('2d')
  } catch {
    ctx = null
  }
  if (!ctx) return
  readFills()
  motionQuery = typeof window.matchMedia === 'function' ? window.matchMedia('(prefers-reduced-motion: reduce)') : null
  reduced = !!motionQuery?.matches
  motionQuery?.addEventListener?.('change', start)
  const col = column()
  if (typeof ResizeObserver !== 'undefined') {
    resizeObserver = new ResizeObserver(() => {
      resize()
      if (reduced) draw(cols)
    })
    resizeObserver.observe(el)
    if (col) resizeObserver.observe(col)
  }
  if (typeof MutationObserver !== 'undefined') {
    themeObserver = new MutationObserver(() => {
      readFills()
      draw(cols)
    })
    themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
    if (col) {
      textObserver = new MutationObserver(scheduleMeasure)
      textObserver.observe(col, { childList: true, subtree: true, characterData: true })
    }
  }
  // web fonts arriving move the text: measure again
  document.fonts?.addEventListener?.('loadingdone', scheduleMeasure)
  resize()
  if (reduced) {
    stillFrame()
    return
  }
  newSweep()
  lastTime = 0
  raf = requestAnimationFrame(frame)
}

function stop(): void {
  if (raf) cancelAnimationFrame(raf)
  if (measureRaf) cancelAnimationFrame(measureRaf)
  raf = 0
  measureRaf = 0
  resizeObserver?.disconnect()
  resizeObserver = null
  themeObserver?.disconnect()
  themeObserver = null
  textObserver?.disconnect()
  textObserver = null
  motionQuery?.removeEventListener?.('change', start)
  motionQuery = null
  if (typeof document !== 'undefined') document.fonts?.removeEventListener?.('loadingdone', scheduleMeasure)
  ctx = null
  cols = 0
  rows = 0
}

watch(canvasEl, (el) => (el ? start() : stop()), { flush: 'post' })

onBeforeUnmount(stop)
</script>
