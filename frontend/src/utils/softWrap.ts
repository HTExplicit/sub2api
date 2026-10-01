// Where a long value in a narrow table cell may break (the template puts a <wbr> between the parts). Joined back,
// the parts are the text unchanged; an empty or missing value has no parts.

const isLower = (c: string | undefined) => c !== undefined && c >= 'a' && c <= 'z'
const isUpper = (c: string | undefined) => c !== undefined && c >= 'A' && c <= 'Z'
const isDigit = (c: string | undefined) => c !== undefined && c >= '0' && c <= '9'
const isLetter = (c: string | undefined) => isLower(c) || isUpper(c)
const isWordChar = (c: string | undefined) => isLetter(c) || isDigit(c)
const isCJK = (c: string | undefined) => c !== undefined && /[\u3400-\u9fff\uf900-\ufaff]/.test(c)

/**
 * An account name or e-mail breaks at a word, never inside one: before the @, after a slash that a letter follows (a
 * URL's https:// | api2.aigcbest.top/ | v1; Chromium does not break there by itself), and inside a word of letters
 * (with figures only at its end) where a lower-case letter meets a capital (NightingaleGiovanna -> Nightingale |
 * Giovanna) and before its final run of three or more figures (Giovanna7598 -> Giovanna | 7598). A word with figures
 * inside it is a code and stays whole (the 18 characters of cindy-global-am332d23b406hthnne-…,
 * eucadc5ca109engnmw@outlook.com), and so does a host name (no break at its dots).
 */
export function nameWrapParts(value: unknown): string[] {
  const text = value == null ? '' : String(value)
  if (!text) return []
  const cuts: number[] = []
  for (let start = 0; start < text.length; ) {
    if (!isWordChar(text[start])) {
      if (text[start] === '@' && start > 0) cuts.push(start)
      if (text[start] === '/' && isLetter(text[start + 1])) cuts.push(start + 1)
      start++
      continue
    }
    let end = start
    while (end < text.length && isWordChar(text[end])) end++
    let figures = end
    while (figures > start && isDigit(text[figures - 1])) figures--
    let letters = figures > start
    for (let i = start; letters && i < figures; i++) letters = isLetter(text[i])
    if (letters) {
      for (let i = start + 1; i < figures; i++) if (isLower(text[i - 1]) && isUpper(text[i])) cuts.push(i)
      if (end - figures >= 3) cuts.push(figures)
    }
    start = end
  }
  const parts: string[] = []
  let from = 0
  for (const at of cuts) {
    parts.push(text.slice(from, at))
    from = at
  }
  parts.push(text.slice(from))
  return parts
}

/**
 * Whether the browser may break after the hyphen before text[i]: before a letter, a CJK character or a figure, but not
 * inside a range of figures (the 1-2 of ab-1-2, the 3-5 of claude-3-5). A subset of what Chromium does (it also breaks
 * before a bracket, say), so a width worked out from it never needs a break the browser does not make.
 */
function breaksAfterHyphen(text: string, i: number): boolean {
  const ch = text[i]
  if (isLetter(ch) || isCJK(ch)) return true
  if (!isDigit(ch)) return false
  let k = i - 2
  while (k >= 0 && isDigit(text[k])) k--
  return !(k < i - 2 && k >= 0 && text[k] === '-')
}

/**
 * The pieces a name or e-mail is laid out in where its cell is narrow: the parts of nameWrapParts (the template's
 * <wbr>), cut again where the browser itself breaks: after a space, after a hyphen (breaksAfterHyphen) and between
 * two CJK characters. Joined back, the pieces are the text.
 */
export function nameLinePieces(value: unknown): string[] {
  const parts = nameWrapParts(value)
  const text = parts.join('')
  const cuts = new Set<number>()
  let at = 0
  for (const part of parts.slice(0, -1)) cuts.add((at += part.length))
  for (let i = 1; i < text.length; i++) {
    const prev = text[i - 1]
    const ch = text[i]
    if ((prev === ' ' && ch !== ' ') || (prev === '-' && breaksAfterHyphen(text, i)) || (isCJK(prev) && isCJK(ch))) cuts.add(i)
  }
  const pieces: string[] = []
  let from = 0
  for (const cut of [...cuts].sort((a, b) => a - b)) {
    pieces.push(text.slice(from, cut))
    from = cut
  }
  if (text) pieces.push(text.slice(from))
  return pieces
}

/** A name or e-mail measured for line breaking: its pieces (nameLinePieces) and the width of every run of them. */
export interface NameRuns {
  pieces: string[]
  /** run[a][b]: pieces a .. b-1 on one line (a space at its end hangs outside the line, as in the browser) */
  run: number[][]
}

/** Measures a name or e-mail's pieces and their runs, with `measure` giving a text's width in the cell's font. */
export function nameRuns(value: unknown, measure: (text: string) => number): NameRuns {
  const pieces = nameLinePieces(value)
  const run = pieces.map((_, a) => {
    const widths = new Array<number>(pieces.length + 1).fill(0)
    let line = ''
    for (let b = a + 1; b <= pieces.length; b++) widths[b] = measure((line += pieces[b - 1]).trimEnd())
    return widths
  })
  return { pieces, run }
}

/**
 * The lines a measured name or e-mail takes at a width, as the browser lays it out (each line ends at the last join
 * that still fits), and the widest of them. A piece wider than the width (the column's floor) counts as filling it.
 */
export function nameLines(text: NameRuns, width: number): { lines: number; widest: number } {
  const count = text.pieces.length
  let lines = 0
  let widest = 0
  for (let a = 0; a < count; ) {
    let b = a + 1
    while (b < count && text.run[a][b + 1] <= width) b++
    lines++
    widest = Math.max(widest, Math.min(text.run[a][b], width))
    a = b
  }
  return { lines, widest }
}

/** A row's name cell: its name and e-mail measured, and the width of its lines that do not wrap (the #id). */
export interface NameCell {
  texts: NameRuns[]
  fixed?: number
}

/** What an extra line of a name costs, in pixels of blank beside the names (see nameColumnWidth). */
export const NAME_LINE_COST = 40

/**
 * The account name column's width for a page of rows (owner 2026-10-01, 「名称这里你留这么大的空白干啥」): the width at
 * which the page's names and e-mails leave the least blank beside them, every extra line a name or e-mail takes
 * counting as `lineCost` pixels of blank. So the column follows the page's usual names: a page of e-mails takes their
 * local part's width (Aelmp67526 | @outlook.com), a page of cindy names their two-line width, and one longer name or
 * e-mail takes a third line at its word joins (Nightingale | Giovanna7598 | @hotmail.com) instead of widening the
 * column for the whole page. Never narrower than the widest piece (a code, a host name, the #id: nothing breaks inside
 * a word; a token wider than `pieceCap` breaks inside rather than set the column) and never wider than the narrowest
 * width at which each name and e-mail takes at most two lines (the column does not widen to put names on one line).
 * The same at every screen width: the table's spare width goes to the other columns. Returns whole pixels (0 for no
 * names) and the widest piece.
 */
export function nameColumnWidth(
  cells: NameCell[],
  { lineCost = NAME_LINE_COST, pieceCap = Infinity }: { lineCost?: number; pieceCap?: number } = {}
): { width: number; piece: number } {
  let piece = 0
  let fixed = 0
  let two = 0
  for (const cell of cells) {
    fixed = Math.max(fixed, cell.fixed ?? 0)
    for (const text of cell.texts) {
      const count = text.pieces.length
      let textPiece = 0
      for (let a = 0; a < count; a++) textPiece = Math.max(textPiece, text.run[a][a + 1])
      let textTwo = count ? text.run[0][count] : 0
      for (let s = 1; s < count; s++) textTwo = Math.min(textTwo, Math.max(text.run[0][s], text.run[s][count]))
      piece = Math.max(piece, textPiece)
      two = Math.max(two, textTwo, textPiece)
    }
  }
  const from = Math.ceil(Math.max(Math.min(piece, pieceCap), fixed))
  const to = Math.max(from, Math.ceil(two))
  let width = from
  let least = Infinity
  for (let w = from; w <= to; w++) {
    let cost = 0
    for (const cell of cells) {
      let ink = cell.fixed ?? 0
      for (const text of cell.texts) {
        const laid = nameLines(text, w)
        ink = Math.max(ink, laid.widest)
        cost += lineCost * laid.lines
      }
      cost += w - ink
    }
    if (cost < least) {
      least = cost
      width = w
    }
  }
  return { width: two > 0 ? width : 0, piece: Math.ceil(piece) }
}

/** The longest model-id part drawn whole; a usual id (claude-sonnet-4-5-20250929) is far shorter. */
const MODEL_PART_WHOLE_MAX = 32

/**
 * A model id breaks only after a slash (anthropic/ | claude-opus-4-5); each part is drawn whole. An outlier part longer
 * than 32 characters may also break after a hyphen or underscore that a letter follows, so a version or a date stays
 * with its word (gemini-2.5- | flash- | preview- | native- | audio- | dialog-2026); a piece still longer than that is
 * cut into even pieces. Such an id wraps instead of pushing its table sideways.
 */
export function modelWrapParts(value: unknown): string[] {
  const text = value == null ? '' : String(value)
  if (!text) return []
  const parts: string[] = []
  let start = 0
  for (let i = 0; i < text.length; i++) {
    if (text[i] === '/' && i + 1 < text.length) {
      pushModelPart(parts, text.slice(start, i + 1))
      start = i + 1
    }
  }
  pushModelPart(parts, text.slice(start))
  return parts
}

function pushModelPart(parts: string[], part: string) {
  if (part.length <= MODEL_PART_WHOLE_MAX) {
    parts.push(part)
    return
  }
  let start = 0
  for (let i = 1; i < part.length; i++) {
    const prev = part[i - 1]
    if ((prev === '-' || prev === '_') && isLetter(part[i])) {
      pushModelPiece(parts, part.slice(start, i))
      start = i
    }
  }
  pushModelPiece(parts, part.slice(start))
}

function pushModelPiece(parts: string[], piece: string) {
  const count = Math.ceil(piece.length / MODEL_PART_WHOLE_MAX)
  const size = Math.ceil(piece.length / count)
  for (let i = 0; i < piece.length; i += size) parts.push(piece.slice(i, i + size))
}
