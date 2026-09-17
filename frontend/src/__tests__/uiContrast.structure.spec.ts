/**
 * UI contrast regression guard (source contracts).
 *
 * Why this exists: the "experiential" restyle swapped the neutral palette and
 * mechanically stripped alpha suffixes from utility pairs (`dark:bg-white/10`
 * became `dark:bg-white`, `bg-black/5` became `bg-black`, ...) and no contrast
 * pass followed. The fallout was invisible text (white on white), muted text on
 * muted dark-mode surfaces, hover states identical to the resting state, and
 * doubled 1px borders (`border` next to `shadow-outline`). The contrast polish
 * fixed the surfaces and swept the tree; this spec pins that end state so the
 * same classes of regression cannot creep back through copy/paste.
 *
 * Scanned segments: static `class="..."` attributes, single-quoted JS literals,
 * template literals and `@apply ...;` statements in src/**\/*.{vue,ts,css}
 * (tests, specs and .d.ts excluded). Every offender is reported as
 * `file:line: segment` so the failure output is directly actionable.
 */
import { readdirSync, readFileSync } from 'node:fs'
import { relative, resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { KEY_GROUP_PROVIDER_ICONS } from '@/utils/keyGroupProviders'

// src/__tests__ -> frontend/
const frontendRoot = resolve(__dirname, '../..')
const srcRoot = resolve(frontendRoot, 'src')

const SCANNED_EXTENSIONS = ['.vue', '.ts', '.css'] as const

interface SourceFile {
  /** frontend-relative POSIX path, e.g. `src/views/HomeView.vue`. */
  rel: string
  text: string
  /** Offset of every line start, for mapping match indices to 1-based lines. */
  lineStarts: number[]
}

function toPosix(path: string): string {
  return path.replace(/\\/g, '/')
}

function walk(dir: string, out: string[]): string[] {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = resolve(dir, entry.name)
    if (entry.isDirectory()) walk(full, out)
    else if (entry.isFile()) out.push(full)
  }
  return out
}

function isScannedSource(rel: string): boolean {
  if (rel.includes('/__tests__/')) return false
  if (rel.endsWith('.spec.ts') || rel.endsWith('.test.ts') || rel.endsWith('.d.ts')) return false
  return SCANNED_EXTENSIONS.some((ext) => rel.endsWith(ext))
}

function lineStartsOf(text: string): number[] {
  const starts = [0]
  for (let i = 0; i < text.length; i += 1) {
    if (text.charCodeAt(i) === 10) starts.push(i + 1)
  }
  return starts
}

function loadFile(abs: string): SourceFile {
  const text = readFileSync(abs, 'utf8')
  return { rel: toPosix(relative(frontendRoot, abs)), text, lineStarts: lineStartsOf(text) }
}

function lineAt(file: SourceFile, index: number): number {
  // Largest line start <= index (binary search), reported 1-based.
  let lo = 0
  let hi = file.lineStarts.length - 1
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1
    if (file.lineStarts[mid] <= index) lo = mid
    else hi = mid - 1
  }
  return lo + 1
}

function read(rel: string): string {
  return readFileSync(resolve(frontendRoot, rel), 'utf8')
}

const sourceFiles: SourceFile[] = walk(srcRoot, [])
  .filter((abs) => isScannedSource(toPosix(relative(frontendRoot, abs))))
  .sort()
  .map(loadFile)

// ---------------------------------------------------------------------------
// Segment extraction shared by the attribute-level rules.
// ---------------------------------------------------------------------------

type SegmentKind = 'class' | 'literal' | 'template' | 'apply'

interface Segment {
  file: SourceFile
  line: number
  kind: SegmentKind
  text: string
}

const SEGMENT_PATTERNS: ReadonlyArray<readonly [SegmentKind, RegExp]> = [
  // Static class attributes only (may span lines). `:class="..."` is covered by
  // its inner literals; `enter-active-class="..."` and friends are excluded by
  // the leading `-`.
  ['class', /(?<![:\w-])class="([^"]*)"/g],
  ['literal', /'([^'\n]*)'/g],
  ['template', /`([^`]*)`/g],
  ['apply', /@apply\s+([^;{}]*);/g],
]

function segmentsOf(file: SourceFile): Segment[] {
  const segments: Segment[] = []
  for (const [kind, pattern] of SEGMENT_PATTERNS) {
    for (const match of file.text.matchAll(pattern)) {
      const text = match[1]
      // HTML snippets embedded in strings are covered by their own class="..."
      // attributes; scanning the whole snippet as one segment would pair tokens
      // that belong to different elements.
      if (kind !== 'class' && text.includes('class="')) continue
      segments.push({ file, kind, line: lineAt(file, match.index ?? 0), text })
    }
  }
  // Report offenders top-to-bottom regardless of which pattern extracted them.
  return segments.sort((a, b) => a.line - b.line)
}

const allSegments: Segment[] = sourceFiles.flatMap(segmentsOf)

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\/]/g, '\\$&')
}

/**
 * Whole-utility match: TOKEN must not be a prefix/suffix of another utility,
 * carry a variant prefix (`hover:`, `dark:`) or an alpha suffix (`/10`).
 */
function bareTokenPattern(token: string, flags = ''): RegExp {
  return new RegExp(`(?<![\\w:/\\-\\[.])${escapeRegExp(token)}(?![\\w/\\-])`, flags)
}

const bareTokenCache = new Map<string, RegExp>()

function hasBare(text: string, token: string): boolean {
  let pattern = bareTokenCache.get(token)
  if (!pattern) {
    pattern = bareTokenPattern(token)
    bareTokenCache.set(token, pattern)
  }
  return pattern.test(text)
}

function report(segment: Segment, note = ''): string {
  const text = segment.text.replace(/\s+/g, ' ').trim()
  return `${segment.file.rel}:${segment.line}: ${text}${note ? `  [${note}]` : ''}`
}

function explain(rule: string, offenders: string[]): string {
  return offenders.length === 0 ? rule : `${rule}\n${offenders.join('\n')}`
}

function cssBlock(css: string, selector: string): string {
  const match = new RegExp(`${escapeRegExp(selector)}\\s*\\{([^}]*)\\}`).exec(css)
  expect(match, `expected a \`${selector} {\` block`).not.toBeNull()
  return match ? match[1] : ''
}

function quotedLiteralsStartingWith(text: string, prefix: string): string[] {
  const pattern = new RegExp(`'(${escapeRegExp(prefix)}[^'\\n]*)'`, 'g')
  return Array.from(text.matchAll(pattern), (match) => match[1])
}

// ---------------------------------------------------------------------------
// Rule tables.
// ---------------------------------------------------------------------------

/** Bare `dark:bg-white` is a white plate in dark mode; only two are intentional. */
const DARK_BG_WHITE_ALLOWLIST: Record<string, number> = {
  // Inverted CTA: dark text on a white button.
  'src/views/HomeView.vue': 1,
  // QR plate: scanners need a white background regardless of theme.
  'src/components/user/profile/TotpSetupModal.vue': 1,
}

/** Literal pairs that fail contrast on the neutral palette in at least one theme. */
const FORBIDDEN_PAIRS = [
  'text-gray-400 dark:text-gray-500',
  'text-gray-400 dark:text-dark-500',
  'text-gray-400 dark:text-dark-400',
  'text-gray-300 dark:text-dark-600',
  'text-slate-500 dark:text-slate-400',
] as const

/** Dark surfaces that swallow the muted text tones listed next to them. */
const MUDDY_DARK_BG = [
  'dark:bg-dark-500',
  'dark:bg-dark-600',
  'dark:bg-dark-700',
  'dark:bg-gray-700',
  'dark:bg-gray-800',
] as const
const MUDDY_DARK_TEXT = ['dark:text-gray-400', 'dark:text-dark-400', 'dark:text-dark-500'] as const

/** `hover:bg-X` / `dark:hover:bg-X` with the utility body captured (arbitrary values and alpha suffixes included). */
const HOVER_BG = /(?<![\w:/\-\[.])(dark:)?hover:(bg-(?:\[[^\]\s]+\]|[\w-]+(?:\/(?:\d+|\[[^\]\s]+\]))?))(?![\w/\-])/g

const BORDER_WIDTH_TOKENS = ['border', 'border-2', 'border-4'] as const

function inDoubleBorderScope(rel: string): boolean {
  return rel.startsWith('src/components/common/') || rel === 'src/style.css'
}

const ACCOUNT_TEST_MODALS = [
  'src/components/admin/account/AccountTestModal.vue',
  'src/components/account/AccountTestModal.vue',
] as const
const ACCOUNT_STATS_MODALS = [
  'src/components/admin/account/AccountStatsModal.vue',
  'src/components/account/AccountStatsModal.vue',
] as const

// ---------------------------------------------------------------------------

describe('ui contrast guard', () => {
  it('scans a non-trivial slice of the source tree', () => {
    expect(sourceFiles.length).toBeGreaterThan(100)
    expect(sourceFiles.some((file) => file.rel === 'src/style.css')).toBe(true)
    expect(sourceFiles.some((file) => file.rel.includes('/__tests__/'))).toBe(false)
    expect(sourceFiles.some((file) => file.rel.endsWith('.spec.ts'))).toBe(false)
  })

  it('(1) bare dark:bg-white survives only on the two intentional white plates', () => {
    const counts: Record<string, number> = {}
    const hits: string[] = []
    for (const file of sourceFiles) {
      for (const match of file.text.matchAll(bareTokenPattern('dark:bg-white', 'g'))) {
        counts[file.rel] = (counts[file.rel] ?? 0) + 1
        hits.push(`${file.rel}:${lineAt(file, match.index ?? 0)}`)
      }
    }
    expect(
      counts,
      explain(
        'bare dark:bg-white (no alpha suffix) is only allowed once in HomeView.vue and once in TotpSetupModal.vue; use dark:bg-white/10-style tints or a surface token elsewhere',
        hits,
      ),
    ).toEqual(DARK_BG_WHITE_ALLOWLIST)
  })

  it('(2) forbidden low-contrast utility pairs are gone', () => {
    const offenders: string[] = []
    for (const file of sourceFiles) {
      file.text.split('\n').forEach((line, index) => {
        for (const pair of FORBIDDEN_PAIRS) {
          if (line.includes(pair)) offenders.push(`${file.rel}:${index + 1}: ${line.trim()}  [${pair}]`)
        }
      })
    }
    expect(
      offenders,
      explain('low-contrast text pairs (fixed by the contrast sweep) reappeared; use text-muted or the text-gray-500 dark:text-dark-300 pairing', offenders),
    ).toEqual([])
  })

  it('(3a) white text never sits on a white background in the same segment', () => {
    const offenders: string[] = []
    for (const segment of allSegments) {
      const { text } = segment
      if (hasBare(text, 'bg-white') && hasBare(text, 'text-white')) {
        offenders.push(report(segment, 'bg-white + text-white'))
      } else if (hasBare(text, 'hover:bg-white') && hasBare(text, 'hover:text-white')) {
        offenders.push(report(segment, 'hover:bg-white + hover:text-white'))
      }
    }
    expect(offenders, explain('white-on-white text (invisible in light mode)', offenders)).toEqual([])
  })

  it('(3b) muted dark-mode text never sits on a muddy dark-mode surface in the same segment', () => {
    const offenders: string[] = []
    for (const segment of allSegments) {
      const bg = MUDDY_DARK_BG.find((token) => hasBare(segment.text, token))
      if (!bg) continue
      const fg = MUDDY_DARK_TEXT.find((token) => hasBare(segment.text, token))
      if (fg) offenders.push(report(segment, `${bg} + ${fg}`))
    }
    expect(
      offenders,
      explain('muted-on-muted dark-mode pairing (fails contrast); pair those surfaces with dark:text-dark-300 or lighter', offenders),
    ).toEqual([])
  })

  it('(3c) hover backgrounds differ from the resting background in the same segment', () => {
    const offenders: string[] = []
    for (const segment of allSegments) {
      for (const match of segment.text.matchAll(HOVER_BG)) {
        const base = `${match[1] ?? ''}${match[2]}`
        if (hasBare(segment.text, base)) offenders.push(report(segment, `${match[0]} is dead: ${base} already set`))
      }
    }
    expect(offenders, explain('dead hover state (hover background equals resting background)', offenders)).toEqual([])
  })

  it('(3d) common components and style.css never stack a border width on top of shadow-outline', () => {
    const offenders: string[] = []
    for (const segment of allSegments) {
      if (!inDoubleBorderScope(segment.file.rel)) continue
      if (!hasBare(segment.text, 'shadow-outline')) continue
      const width = BORDER_WIDTH_TOKENS.find((token) => hasBare(segment.text, token))
      if (width) offenders.push(report(segment, `${width} + shadow-outline`))
    }
    expect(
      offenders,
      explain('doubled 1px outline (shadow-outline already draws a 1px ring; drop the border width or the shadow)', offenders),
    ).toEqual([])
  })

  it('(4) theme tokens: --ui-line-strong is defined for both themes and mapped in tailwind', () => {
    const theme = read('src/styles/theme.css')
    const light = cssBlock(theme, ':root')
    const dark = cssBlock(theme, ':root.dark')
    // RGB channel triples, so `rgb(var(--ui-line-strong) / <alpha-value>)` keeps working.
    expect(light, ':root must define --ui-line-strong as an RGB triple').toMatch(/--ui-line-strong:\s*\d+\s+\d+\s+\d+\s*;/)
    expect(dark, ':root.dark must define --ui-line-strong as an RGB triple').toMatch(/--ui-line-strong:\s*\d+\s+\d+\s+\d+\s*;/)

    const tailwind = read('tailwind.config.js')
    expect(tailwind).toContain("'line-strong': 'rgb(var(--ui-line-strong) / <alpha-value>)'")
  })

  it('(4) style.css: buttons carry no shadow-outline, one .glass surface, tokenised card/sidebar/empty-state chrome', () => {
    const css = read('src/style.css')

    const buttonBlocks = css.match(/\.btn[\w-]*\s*\{[^}]*\}/g) ?? []
    expect(buttonBlocks.length).toBeGreaterThan(0)
    const shadowedButtons = buttonBlocks
      .filter((block) => block.includes('shadow-outline'))
      .map((block) => block.slice(0, block.indexOf('{')).trim())
    expect(shadowedButtons, explain('.btn-* blocks must not apply shadow-outline (buttons draw their own border)', shadowedButtons)).toEqual([])

    expect((css.match(/\.glass\s*\{/g) ?? []).length, 'exactly one `.glass {` block').toBe(1)

    expect(cssBlock(css, '.card')).toMatch(/border-line/)
    expect(cssBlock(css, '.sidebar-section-title')).toMatch(/text-muted/)
    expect(cssBlock(css, '.empty-state-icon')).toMatch(/text-muted/)
  })

  it('(5) admin and user AccountTestModal headers stay in sync', () => {
    const headers = ACCOUNT_TEST_MODALS.map((rel) => {
      const src = read(rel)
      expect(src, `${rel}: header container must be the flat bordered raised surface`).toContain(
        'rounded-none border border-line bg-raised p-3',
      )
      const chip = /class="([^"]*)"\s*>\s*\{\{\s*account\.type\s*\}\}/.exec(src)
      expect(chip, `${rel}: type chip (class="..." wrapping {{ account.type }}) not found`).not.toBeNull()
      return { rel, chipClass: chip ? chip[1].replace(/\s+/g, ' ').trim() : '' }
    })

    for (const { rel, chipClass } of headers) {
      expect(chipClass, `${rel}: type chip`).toContain('text-[10px]')
      expect(chipClass, `${rel}: type chip`).toContain('uppercase')
      expect(chipClass, `${rel}: type chip must use the border-line token`).toContain('border-line')
    }
    expect(headers[1].chipClass, 'type chip class drifted between admin/account and account copies').toBe(
      headers[0].chipClass,
    )
  })

  it('(5) status chip arms are identical across all four AccountTestModal/AccountStatsModal copies', () => {
    const files = [...ACCOUNT_TEST_MODALS, ...ACCOUNT_STATS_MODALS]
    const arms = files.map((rel) => {
      const src = read(rel)
      return {
        rel,
        inactive: quotedLiteralsStartingWith(src, 'bg-gray-100 text-gray-700'),
        active: quotedLiteralsStartingWith(src, 'bg-green-100 text-green-700'),
      }
    })

    for (const { rel, inactive, active } of arms) {
      expect(inactive.length, `${rel}: inactive status chip arm ('bg-gray-100 text-gray-700 ...') not found`).toBeGreaterThan(0)
      expect(active.length, `${rel}: active status chip arm ('bg-green-100 text-green-700 ...') not found`).toBeGreaterThan(0)
    }

    const describeArms = (pick: (entry: (typeof arms)[number]) => string[]) =>
      arms.map((entry) => `${entry.rel}: ${pick(entry).join(' | ')}`).join('\n')

    const inactiveVariants = [...new Set(arms.flatMap((entry) => entry.inactive))]
    expect(inactiveVariants, `inactive status chip arm drifted between copies:\n${describeArms((e) => e.inactive)}`).toHaveLength(1)
    expect(inactiveVariants[0]).toContain('dark:text-dark-300')

    const activeVariants = [...new Set(arms.flatMap((entry) => entry.active))]
    expect(activeVariants, `active status chip arm drifted between copies:\n${describeArms((e) => e.active)}`).toHaveLength(1)
    expect(activeVariants[0]).toContain('dark:text-green-400')
  })

  it('(6) provider cards keep at most three 24px tiles inside a clipped, non-wrapping icon row', () => {
    // The create-key provider card (BaseDialog "normal", sm:grid-cols-4) has roughly
    // 91.5px of content width: 3 x 24px tiles + 2 x 6px gaps = 84px fit; a fourth
    // tile (+30px) would overflow the card, so every provider lists <= 3 icons.
    for (const [provider, icons] of Object.entries(KEY_GROUP_PROVIDER_ICONS)) {
      expect(icons.length, `${provider} lists ${icons.length} icons; the 91.5px card budget fits at most 3`).toBeLessThanOrEqual(3)
    }

    const src = read('src/views/user/KeysView.vue')
    const start = src.indexOf('data-tour="key-form-provider"')
    expect(start, 'KeysView provider fieldset (data-tour="key-form-provider") is missing').toBeGreaterThanOrEqual(0)
    const end = src.indexOf('</fieldset>', start)
    expect(end, 'provider fieldset is never closed').toBeGreaterThan(start)
    const fieldset = src.slice(start, end)

    expect(fieldset).toMatch(/flex h-8 w-full min-w-0 items-center justify-center gap-1\.5 overflow-hidden/)
    expect(fieldset).toMatch(/h-6 w-6 shrink-0/)
    expect(fieldset).toMatch(/<PlatformIcon :platform="platform" size="md" \/>/)
  })
})
