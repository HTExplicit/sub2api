// Manual quality check for the admin account connection test (OpenAI OAuth):
// one fixed question with a known answer, judged from the final number of the
// reply. The verdict is a hint for the administrator only; it is never stored
// and never affects scheduling.

// Canonical candy question, byte for byte.
// UTF-8 SHA-256: 05cc85bbee16e9152990a2ceb3163dea65e3ad3baff188ccbc8a570295f5a7a7
export const CANDY_QUALITY_PROMPT = [
  '在一个黑色的袋子里放有三种口味的糖果，每种糖果有两种不同的形状（圆形和五角星形，不同的形状靠手感可以分辨）。现已知不同口味的糖和不同形状的数量统计如下表。参赛者需要在活动前决定摸出的糖果数目，那么，最少取出多少个糖果才能保证手中同时拥有不同形状的苹果味和桃子味的糖？（同时手中有圆形苹果味匹配五角星桃子味糖果，或者有圆形桃子味匹配五角星苹果味糖果都满足要求）',
  '',
  '| 形状   | 苹果味 | 桃子味 | 西瓜味 |',
  '| ---- | --: | --: | --: |',
  '| 圆形   |   7 |   9 |   8 |',
  '| 五角星形 |   7 |   6 |   4 |'
].join('\n')

export const CANDY_QUALITY_ANSWER = 21

// Answers given with exactly this many reasoning tokens were observed to be
// wrong. It is shown as a hint and never changes the verdict by itself.
export const CANDY_QUALITY_SUSPECT_REASONING_TOKENS = 516

// Presentation only: markdown emphasis, LaTeX delimiters and \text{} wrappers
// are dropped; display math joins the line it completes.
function normalizeAnswer(text: string): string {
  return text
    .replace(/\r\n?/g, '\n')
    .replace(/[\uFF10-\uFF19]/g, digit => String.fromCharCode(digit.charCodeAt(0) - 0xFEE0))
    .replace(/＝/g, '=')
    .replace(/＋/g, '+')
    .replace(/\\(?:text|textbf|textrm|mathrm|mathbf|mbox)\s*\{([^{}]*)\}/g, ' $1 ')
    .replace(/\s*(?:\\\[([\s\S]*?)\\\]|\$\$([\s\S]*?)\$\$)/g, (_match, bracket = '', dollar = '') =>
      ` ${String(bracket || dollar).replace(/\s+/g, ' ')} `)
    .replace(/\\[()]|\$/g, ' ')
    .replace(/\\(?:[,;:! ]|quad|qquad)|~/g, ' ')
    .replace(/\\(?:times|cdot)/g, '×')
    .replace(/\*\*|__|`/g, '')
}

// A number, or a short arithmetic chain such as 28+1=29 whose value is the
// result after its last "=". The chain stays on one line, so a list item after
// the answer ("21\n\n- 12 个…") is not read as arithmetic.
const NUMBER = String.raw`\d+(?:[ \t]*[+\-−×*/][ \t]*\d+)*(?:[ \t]*=[ \t]*\d+(?:[ \t]*[+\-−×*/][ \t]*\d+)*)*`
// Each connector alternative parses one way only, so a failed match cannot
// backtrack exponentially over untrusted upstream text.
const KEYWORD_MARKER = new RegExp(
  String.raw`(?:最终答案|正确答案|答案|结论|答(?=\s*[:：])|(?:至少|最少)(?:需要|需|要|得)?(?:取出|摸出|拿出|取|摸|拿)?|\bat\s+least\b|\banswer\b)` +
  String.raw`(?:[ \t\u3000*_"'“”‘’「」:：=]|\\boxed\s*\{|就是|应该是|应为|应是|则是|会是|也是|仍是|即|是|为|一共|总共|共|取出|摸出|拿出|取|摸|拿|\b(?:is|was|be|would|will|of|equals?|to|draw|pick|take)\b)*?` +
  `(${NUMBER})`,
  'gi'
)
const BOXED_MARKER = new RegExp(String.raw`\\boxed\s*\{\s*(${NUMBER})`, 'g')
// A count of one shape or flavour, a date, a percentage or a decimal is never
// the number of candies to draw.
const NOT_A_TOTAL = /^\s*(?:颗|个|粒|块|枚)?\s*(?:的\s*)?(?:圆形|圆|五角星|星形|苹果|桃子|西瓜|round|circular|star|apple|peach|watermelon|种|类|步|次|年|月|日|号|%|％|\.\d)/i
// Hypothetical alternatives ("if the draw were random, the answer would be
// 29") are not the conclusion. A conditional (如果/若/…的话/…时，/…的情况下/
// if/when/had…) makes the rest of its sentence hypothetical. A later
// 所以/therefore starts the conclusion again, unless a consequent (那么/则/会是/
// then/would) follows the conditional: in "if P, then Q, so R", R is still
// hypothetical. An irrealis modal (则/会是/would/could) makes its own clause
// hypothetical. A first group matches ordinary words that contain a signal
// (此时, 最坏的情况下, 只要是, even if, 规则); those matches are skipped.
// No lookbehind is used, so older browsers still parse this module.
const CONDITIONAL = new RegExp(
  String.raw`([此这那同及随有平当届暂临到]时[，,]|(?:最坏|最差|最糟|最不利|极端|这样|那样)的情况下|[主只需总]要是|\beven\s+(?:if|when(?:ever)?|assuming|supposing)\b)|` +
  String.raw`如果|假如|假若|假设|倘若|要是|若(?!干)|否则|的话|时[，,]|的情况下|\bif\b|\bwhen(?:ever)?\b|\botherwise\b|\bsuppos(?:e|ing)\b|\bassum(?:e|ing)\b|\bunless\b|^[\s(（"“*_-]*(?:had|were)\b`,
  'gi'
)
const ORDINARY_ZE = '([规否原法准实细总守四]则)'
const CONSEQUENT = new RegExp(String.raw`${ORDINARY_ZE}|那么|则|就会|会是|会为|将会|\bthen\b|\bwould\b|\bcould\b|\bmight\b`, 'gi')
const IRREALIS = new RegExp(String.raw`${ORDINARY_ZE}|则|会是|会为|\bwould\b|\bcould\b|\bmight\b`, 'gi')
const CONCLUSION = /所以|因此|因而|故而|于是|综上|总之|可见|\btherefore\b|\bthus\b|\bhence\b|\bso\b/gi
// A goal is not a hypothetical: 如果要保证…, 若我们想…, 要是想…, "if we want
// to…", "if we are to guarantee…".
const CONDITIONAL_ZH_WORD = /^(?:如果|假如|假若|假设|倘若|要是|若)$/
const PURPOSE_ZH = /^\s*(?:我们|咱们|你们|你|我|大家|参赛者)?\s*(?:只|真的|一定)?\s*(?:想|要(?!求)|需|希望|打算|为了|保证|确保)/
const PURPOSE_EN = /^(?:\s+(?:we|you|one|i)\s+(?:want|wants|need|needs|wish|wishes|are\s+to|is\s+to|am\s+to)\b|[^,，;；.]*?\bto\s+(?:guarantee|ensure|be\s+(?:sure|certain)|make\s+sure)\b)/i

// The end of the last signal of pattern in text, or -1.
function lastSignalEnd(text: string, pattern: RegExp): number {
  let end = -1
  for (const match of text.matchAll(pattern)) {
    if (match[1] !== undefined) continue
    const matchEnd = match.index! + match[0].length
    const purpose = CONDITIONAL_ZH_WORD.test(match[0]) ? PURPOSE_ZH : /^if$/i.test(match[0]) ? PURPOSE_EN : null
    if (purpose?.test(text.slice(matchEnd, matchEnd + 60))) continue
    end = matchEnd
  }
  return end
}

function expressionValue(expression: string): number | null {
  const result = expression.split('=').pop()!.trim()
  return /^\d+$/.test(result) ? Number(result) : null
}

function sentencePrefix(text: string, index: number): string {
  let start = index
  while (start > 0) {
    const previous = text[start - 1]
    if (/[。！？!?；;\n]/.test(previous) || (previous === '.' && /\s/.test(text[start] ?? ''))) break
    start -= 1
  }
  return text.slice(start, index)
}

// prefix runs from the start of the sentence to the number.
function isHypothetical(prefix: string): boolean {
  const conditionalEnd = lastSignalEnd(prefix, CONDITIONAL)
  const conclusionEnd = lastSignalEnd(prefix, CONCLUSION)
  if (conditionalEnd >= 0 && (conclusionEnd < conditionalEnd || lastSignalEnd(prefix.slice(conditionalEnd), CONSEQUENT) >= 0)) return true
  const clauseStart = Math.max(conclusionEnd, prefix.lastIndexOf('，') + 1, prefix.lastIndexOf(',') + 1)
  return lastSignalEnd(prefix.slice(clauseStart), IRREALIS) >= 0
}

function isTotalAt(text: string, start: number, end: number): boolean {
  return !NOT_A_TOTAL.test(text.slice(end, end + 12)) && !isHypothetical(sentencePrefix(text, start))
}

function lastMarkedNumber(text: string): number | null {
  let last: { index: number; value: number } | null = null
  for (const pattern of [KEYWORD_MARKER, BOXED_MARKER]) {
    for (const match of text.matchAll(pattern)) {
      const value = expressionValue(match[1])
      const end = match.index! + match[0].length
      // The marker's own words (答案会是, would be) belong to the sentence prefix.
      if (value === null || !isTotalAt(text, end - match[1].length, end)) continue
      if (!last || match.index! > last.index) last = { index: match.index!, value }
    }
  }
  return last ? last.value : null
}

function lastNumberOfFinalParagraph(text: string): number | null {
  const paragraphs = text.split(/\n\s*\n/).map(paragraph => paragraph.trim()).filter(Boolean)
  const paragraph = paragraphs[paragraphs.length - 1]
  if (!paragraph) return null
  let value: number | null = null
  for (const match of paragraph.matchAll(/\d+/g)) {
    const start = match.index!
    // The fractional digits of a decimal; its integer part fails NOT_A_TOTAL.
    if (/\d\.$/.test(paragraph.slice(Math.max(0, start - 2), start))) continue
    if (isTotalAt(paragraph, start, start + match[0].length)) value = Number(match[0])
  }
  return value
}

// The final answer number: the last explicit final-answer marker (\boxed{N},
// 答案/最终答案/结论/至少需要/最少需要/最少要/at least/answer is N, optionally
// followed by 颗/个/candies); otherwise the last integer of the final paragraph;
// otherwise null.
export function extractFinalAnswerNumber(answer: string): number | null {
  const text = normalizeAnswer(answer)
  return lastMarkedNumber(text) ?? lastNumberOfFinalParagraph(text)
}

export type CandyQualityOutcome = 'completed' | 'incomplete' | 'failed'
export type CandyQualityVerdictKind = 'normal' | 'suspect' | 'undetermined'
export type CandyQualityUndeterminedReason = 'failed' | 'incomplete' | 'no_number'

export interface CandyQualityRun {
  answer: string
  outcome: CandyQualityOutcome
  reasoningTokens: number | null
  requestedModel: string
  declaredModel: string
}

export interface CandyQualityVerdict {
  kind: CandyQualityVerdictKind
  reason: CandyQualityUndeterminedReason | null
  finalNumber: number | null
  reasoningTokens: number | null
  reasoningTokensSuspect: boolean
  requestedModel: string
  declaredModel: string
  // null when the upstream declared no model.
  modelMatches: boolean | null
}

export function judgeCandyQualityRun(run: CandyQualityRun): CandyQualityVerdict {
  const finalNumber = run.outcome === 'completed' ? extractFinalAnswerNumber(run.answer) : null
  const reason: CandyQualityUndeterminedReason | null =
    run.outcome !== 'completed' ? run.outcome : finalNumber === null ? 'no_number' : null
  const requestedModel = run.requestedModel.trim()
  const declaredModel = run.declaredModel.trim()
  return {
    kind: reason ? 'undetermined' : finalNumber === CANDY_QUALITY_ANSWER ? 'normal' : 'suspect',
    reason,
    finalNumber,
    reasoningTokens: run.reasoningTokens,
    reasoningTokensSuspect: run.reasoningTokens === CANDY_QUALITY_SUSPECT_REASONING_TOKENS,
    requestedModel,
    declaredModel,
    modelMatches: declaredModel ? declaredModel === requestedModel : null
  }
}
