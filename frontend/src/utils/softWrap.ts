// Where a long value in a narrow table cell may break (the template puts a <wbr> between the parts). Joined back,
// the parts are the text unchanged; an empty or missing value has no parts.

const isLower = (c: string | undefined) => c !== undefined && c >= 'a' && c <= 'z'
const isUpper = (c: string | undefined) => c !== undefined && c >= 'A' && c <= 'Z'
const isDigit = (c: string | undefined) => c !== undefined && c >= '0' && c <= '9'
const isLetter = (c: string | undefined) => isLower(c) || isUpper(c)
const isWordChar = (c: string | undefined) => isLetter(c) || isDigit(c)
const isCJK = (c: string | undefined) => c !== undefined && /[\u3400-\u9fff\uf900-\ufaff]/.test(c)

/**
 * An account name or e-mail breaks at a word, never inside one: before the @, and inside a word of letters (with
 * figures only at its end) where a lower-case letter meets a capital (NightingaleGiovanna -> Nightingale | Giovanna)
 * and before its final run of three or more figures (Giovanna7598 -> Giovanna | 7598). A word with figures inside it is
 * a code and stays whole (the 18 characters of cindy-global-am332d23b406hthnne-…, eucadc5ca109engnmw@outlook.com).
 */
export function nameWrapParts(value: unknown): string[] {
  const text = value == null ? '' : String(value)
  if (!text) return []
  const cuts: number[] = []
  for (let start = 0; start < text.length; ) {
    if (!isWordChar(text[start])) {
      if (text[start] === '@' && start > 0) cuts.push(start)
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

export interface NameWidths {
  /** the value on one line */
  one: number
  /** the narrowest width at which it takes at most two lines, broken at a piece join (an e-mail before its @ only) */
  two: number
  /** its widest piece: the narrowest width it can take without breaking inside a word */
  piece: number
}

const EMAIL = /^[^\s@]+@[^\s@]+$/

/** The widths of a name or e-mail laid out in pieces (nameLinePieces), with `measure` giving a text's width. */
export function nameWidths(value: unknown, measure: (text: string) => number): NameWidths {
  const text = value == null ? '' : String(value)
  if (!text) return { one: 0, two: 0, piece: 0 }
  const one = measure(text)
  const pieces = nameLinePieces(text)
  let piece = 0
  for (const p of pieces) piece = Math.max(piece, measure(p.trimEnd()))
  let two = one
  if (EMAIL.test(text)) {
    const at = text.indexOf('@')
    two = Math.min(one, Math.max(measure(text.slice(0, at)), measure(text.slice(at))))
  } else {
    let first = ''
    for (let i = 0; i < pieces.length - 1; i++) {
      first += pieces[i]
      two = Math.min(two, Math.max(measure(first.trimEnd()), measure(pieces.slice(i + 1).join(''))))
    }
  }
  return { one, two: Math.max(two, piece), piece }
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
