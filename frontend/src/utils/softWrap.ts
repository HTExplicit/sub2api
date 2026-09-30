// Where a long value in a narrow table cell may break (the template puts a <wbr> between the parts). Joined back,
// the parts are the text unchanged; an empty or missing value has no parts.

const isLower = (c: string | undefined) => c !== undefined && c >= 'a' && c <= 'z'
const isUpper = (c: string | undefined) => c !== undefined && c >= 'A' && c <= 'Z'
const isDigit = (c: string | undefined) => c !== undefined && c >= '0' && c <= '9'

/**
 * An account name or e-mail breaks at a word, never inside one: before the @, where a lower-case letter meets a
 * capital (NightingaleGiovanna -> Nightingale | Giovanna) and where a letter meets a run of three or more digits
 * (Giovanna7598 -> Giovanna | 7598).
 */
export function nameWrapParts(value: unknown): string[] {
  const text = value == null ? '' : String(value)
  if (!text) return []
  const parts: string[] = []
  let start = 0
  for (let i = 1; i < text.length; i++) {
    const prev = text[i - 1]
    const ch = text[i]
    const digitRun = isDigit(ch) && isDigit(text[i + 1]) && isDigit(text[i + 2])
    if (ch === '@' || (isLower(prev) && isUpper(ch)) || ((isLower(prev) || isUpper(prev)) && digitRun)) {
      parts.push(text.slice(start, i))
      start = i
    }
  }
  parts.push(text.slice(start))
  return parts
}

/** The longest model-id part drawn whole; a usual id (claude-sonnet-4-5-20250929) is far shorter. */
const MODEL_PART_WHOLE_MAX = 32

const isLetter = (c: string | undefined) => isLower(c) || isUpper(c)

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
