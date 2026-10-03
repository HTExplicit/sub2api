import { createHash } from 'node:crypto'
import { describe, expect, it } from 'vitest'
import {
  CANDY_QUALITY_ANSWER,
  CANDY_QUALITY_PROMPT,
  extractFinalAnswerNumber,
  judgeCandyQualityRun,
  type CandyQualityRun
} from '../accountQualityCheck'

describe('candy quality question', () => {
  it('is the canonical question byte for byte', () => {
    expect(createHash('sha256').update(CANDY_QUALITY_PROMPT, 'utf8').digest('hex'))
      .toBe('05cc85bbee16e9152990a2ceb3163dea65e3ad3baff188ccbc8a570295f5a7a7')
    expect(CANDY_QUALITY_ANSWER).toBe(21)
  })
})

// Chinese answers are excerpts of real model answers to the question; English
// answers follow the same shapes.
const finalNumberCases: Array<[string, string, number | null]> = [
  ['zh: concludes 21, then discusses a hypothetical 29 (答案会是)', String.raw`**最少取出 \(21\) 个**，这里利用了题目给出的条件：可以靠手感辨别形状，从而按形状选择糖果。

取 **12 个五角星形糖果和 9 个圆形糖果**：

- 五角星形中，非苹果味共有 \(6+4=10\) 个，因此取 12 个一定有苹果味；非桃子味共有 \(7+4=11\) 个，因此也一定有桃子味。
- 圆形中，西瓜味只有 8 个，因此取 9 个一定有苹果味或桃子味。
- 要得到“圆形苹果味＋五角星形桃子味”，至少需要摸出 \(9\) 个圆形和 \(12\) 个五角星形，共 \(21\) 个。

所以，即使根据已摸出的糖果灵活调整后续选择，20 个仍不能保证成功。答案为 \(\boxed{21}\)。

如果题意规定**不能按手感选择形状，只能完全随机取出**，则答案会是 29 个；题目中特意提供“形状靠手感可以分辨”，通常就是允许按形状选取。`, 21],
  ['zh: concludes 21, then discusses a hypothetical 29 (答案则是 **29 个**)', String.raw`**最少取出 21 个**，前提是利用题目给出的条件：可以凭手感选择摸取哪一种形状。

所以，\(12+9=\boxed{21}\) 个一定够。

因此，无论如何安排两种形状的摸取次数，20 个都不能保证成功，最少就是 **21 个**。

如果题意规定完全随机摸取、不能凭手感挑选形状，答案则是 **29 个**；题目特意说明形状可以靠手感分辨，通常就是允许利用这一条件。`, 21],
  ['zh: \\boxed{21\\text{ 颗}}', String.raw`最少需要取出 **21 颗**。关键是利用“可以凭手感辨别形状”，有选择地摸取糖果。

所以，即使根据已经摸出的糖果调整后续选择，20 颗也无法保证成功。答案为 \(\boxed{21\text{ 颗}}\)。`, 21],
  ['zh: concludes 29 (bold first line, 答案是 at the end)', `最少需要取出 **29 个糖果**。

最坏情况下，可以先取出全部 12 个西瓜味糖果、7 个圆形苹果味和 9 个圆形桃子味，共 28 个，仍然没有不同形状的苹果味和桃子味。

答案是 **29 个**。`, 29],
  ['zh: concludes 29 in display math (至少要取出：28+1=29)', String.raw`最多可以取出 **28 个**而仍无法满足条件，因此至少要取出：

\[
28+1=29
\]`, 29],
  ['zh: concludes 29 with \\boxed inside an arithmetic chain', String.raw`至少取出 **29 个**糖果。

因此取出 \(28+1=\boxed{29}\) 个糖果后，必然同时拥有题目所要求的两种不同形状、不同口味的苹果味和桃子味糖果。`, 29],
  ['zh: concludes 28 although an intermediate table row equals 21', String.raw`最少需要取出 **28 个糖果**。

| 缺少的种类 | 最多取出 |
|---|---:|
| 没有苹果味糖 | 桃子味 \(15\) + 西瓜味 \(12\) = \(27\) |
| 没有五角星形苹果味和五角星形桃子味 | 圆形桃子味 \(9\) + 西瓜味 \(12\) = \(21\) |

所以再取出一个糖果就一定满足要求：

\[
\boxed{27+1=28}
\]`, 28],
  ['zh: concludes 36 in a lone \\boxed', String.raw`最少需要取出 **36 个糖果**。

因此，再取出 1 个糖果后，无论之前怎样取，都必然满足要求：

\[
\boxed{36}
\]`, 36],
  ['zh: concludes 29 and mentions 21 only as a hypothetical', `最少需要取出 **29 个糖果**。

如果可以凭手感挑选形状，答案会是 21 个。`, 29],
  ['zh: 如果要保证 is a purpose clause, not a hypothetical', '如果要保证一定满足条件，最少需要取出 21 个糖果。', 21],
  ['zh: 所以 after a hypothetical starts the conclusion again', '若只取 20 个，仍可能失败，所以最少需要 21 个。', 21],
  ['zh: no marker, last integer of the final paragraph', '取 12 个五角星形和 9 个圆形就能保证。\n\n一共要摸出 21 颗。', 21],
  ['zh: full-width digits', '答案：２１ 个', 21],
  ['zh: no number at all', '这道题的关键是考虑最坏情况，并利用手感区分形状。', null],
  ['zh: answer to a different question has no candy count', '我的知识截止日期是 **2024 年 6 月**。', null],
  ['en: discusses 29 but concludes 21', String.raw`A naive worst-case count gives 28 + 1 = 29. If the candies had to be drawn blindly, the answer would be 29. Because the shapes can be told apart by touch, we can choose 12 star-shaped and 9 round candies, so the answer is \(\boxed{21}\).`, 21],
  ['en: concludes 21 in bold with a unit', `Because the shape can be felt, draw 12 star-shaped candies and 9 round candies.

- Among the star-shaped candies, at most 10 are not apple and at most 11 are not peach, so 12 star candies contain both flavours.
- Among the round candies only 8 are watermelon, so 9 round candies include an apple or a peach one.

Twenty candies can still fail, so the answer is **21 candies**.`, 21],
  ['en: concludes 29 with at least', `In the worst case you could draw all 12 watermelon candies plus the 7 round apple and 9 round peach candies (28 candies) and still have no valid pair.

Therefore you need at least **29** candies.`, 29],
  ['en: a hypothetical 29 after the answer', 'The answer is 21. Otherwise, with blind draws, the answer would be 29.', 21],
  ['zh: 在这种情况下 is the actual case, not a conditional', '在这种情况下，即使根据已摸出的口味灵活选择下一颗的形状，也至少需要 21 个才能成功。', 21],
  // Variants found in review. "If P, then Q, so R": R is still hypothetical.
  ['zh: concludes 29; 那么…因此 21 inside a hypothetical', '最少需要取出 29 个。\n\n如果可以凭手感挑选，那么 12 个五角星加 9 个圆形就够了，因此最少需要 21 个；但题目要求事先决定数目。', 29],
  ['zh: concludes 21; 那么…所以答案会是 29 inside a hypothetical', String.raw`答案为 \(\boxed{21}\)。` + '\n\n如果题意规定只能完全随机取出，那么最坏情况会先取出 28 个，所以答案会是 29 个。', 21],
  ['en: concludes 29; if…, so the answer would be 21', 'The answer is 29.\n\nIf you could pick candies by shape, you would take 12 star-shaped and 9 round ones, so the answer would be 21. But the count is fixed in advance.', 29],
  ['en: concludes 29; if…, therefore at least 21 would be needed', 'You need at least 29 candies.\n\nIf shape could be chosen by touch, 12 stars and 9 rounds would suffice, therefore at least 21 would be needed; the problem rules that out.', 29],
  ['en: concludes 21; if…, so the answer would be 29', 'The answer is 21.\n\nIf the candies had to be drawn blindly, you could draw 28 without success, so the answer would be 29.', 21],
  // Colloquial conditionals and irrealis modals after the conclusion.
  ['zh: …的话 after the answer', '最少需要取出 **29 个**。\n\n说明：最坏情况下先取出 28 个仍不满足；凭手感挑选形状的话，答案是 21 个，但这不符合题意。', 29],
  ['zh: …时 after the answer', '最少需要取出 29 个糖果。\n\n（允许按手感挑选形状时，答案为 21 个；但本题需事先决定数目，因此不适用。）', 29],
  ['zh: …的情况下 after the answer', String.raw`答案为 \(\boxed{21}\)。` + '\n\n在完全随机摸取（不能按手感挑选）的情况下，答案是 29 个。', 21],
  ['zh: 则 without a conditional word', '最少取出 21 个。\n\n（盲摸则至少需要 29 个。）', 21],
  ['en: when after the answer', 'The answer is 29.\n\nNote: when you are allowed to choose shapes by touch, the answer is 21, but that does not apply here.', 29],
  ['en: with…would after the answer', 'You need at least 29 candies.\n\nWith shape selection by touch the answer would be 21, but the count is fixed in advance.', 29],
  ['en: would without a conditional word', 'The answer is 21.\n\n(Blind draws would need at least 29.)', 21],
  ['en: unmarked 29, then had…would 21, cannot be judged', 'So the minimum is 29.\n\nHad we been allowed to pick by shape, the answer would be 21.', null],
  // Purpose clauses state a goal, not a hypothetical.
  ['zh: 如果我们想保证 is a purpose clause', '如果我们想保证手中同时拥有不同形状的苹果味和桃子味糖，最少需要取出 21 个糖果。', 21],
  ['zh: 若我们要确保 is a purpose clause', '若我们要确保成功，最少需要取出 21 个。', 21],
  ['zh: 要是想保证 is a purpose clause', '要是想保证一定成功，至少要取出 21 个。', 21],
  ['en: if we are to guarantee is a purpose clause', 'If we are to guarantee success, we must draw at least 21 candies.', 21],
  // A markdown list after the answer is not arithmetic.
  ['zh: "-" list after 答案：21', '**答案：21**\n\n- 12 个五角星形：保证同时有苹果味和桃子味\n- 9 个圆形：保证有苹果味或桃子味', 21],
  ['zh: "*" list after 最终答案：21', '**最终答案：21**\n\n* 12 个五角星形：保证同时有苹果味和桃子味\n* 9 个圆形：保证有苹果味或桃子味', 21],
  ['zh: "-" list after 答案：29, then a rejected 21 scheme', '**答案：29**\n\n- 28 个是最坏情况：12 个西瓜味、7 个圆形苹果味、9 个圆形桃子味\n\n凭手感挑选形状的方案（12 个五角星 + 9 个圆形 = 21 个）在本题不适用。', 29],
  ['en: "-" list after the answer is 21', 'The answer is 21\n\n- 12 star-shaped candies guarantee both flavours\n- 9 round candies guarantee one of them', 21],
  ['en: Final answer marker', 'Final answer: **21**', 21],
  ['en: number written in words only', 'The minimum number of candies is twenty-one.', null],
  ['empty answer', '', null]
]

describe('extractFinalAnswerNumber', () => {
  it.each(finalNumberCases)('%s', (_name, answer, expected) => {
    expect(extractFinalAnswerNumber(answer)).toBe(expected)
  })
})

function run(overrides: Partial<CandyQualityRun>): CandyQualityRun {
  return { answer: '答案是 **21 个**。', outcome: 'completed', reasoningTokens: 700,
    requestedModel: 'gpt-6-astra', declaredModel: 'gpt-6-astra', ...overrides }
}

describe('judgeCandyQualityRun', () => {
  it('sees no anomaly for a completed answer of 21', () => {
    expect(judgeCandyQualityRun(run({}))).toEqual({
      kind: 'normal', reason: null, finalNumber: 21, reasoningTokens: 700, reasoningTokensSuspect: false,
      requestedModel: 'gpt-6-astra', declaredModel: 'gpt-6-astra', modelMatches: true
    })
  })

  it('suspects degradation for another final number and reports it', () => {
    const verdict = judgeCandyQualityRun(run({ answer: '最少需要取出 **29 个糖果**。', reasoningTokens: 516 }))
    expect(verdict).toMatchObject({ kind: 'suspect', reason: null, finalNumber: 29, reasoningTokens: 516, reasoningTokensSuspect: true })
  })

  it('never changes the verdict because of 516 reasoning tokens alone', () => {
    expect(judgeCandyQualityRun(run({ reasoningTokens: 516 }))).toMatchObject({ kind: 'normal', finalNumber: 21, reasoningTokensSuspect: true })
  })

  it.each([
    ['failed', run({ outcome: 'failed' }), 'failed'],
    ['incomplete', run({ outcome: 'incomplete' }), 'incomplete'],
    ['no final number', run({ answer: '这道题需要考虑最坏情况。' }), 'no_number']
  ] as const)('cannot judge when %s', (_name, input, reason) => {
    expect(judgeCandyQualityRun(input)).toMatchObject({ kind: 'undetermined', reason, finalNumber: null })
  })

  it('suspects degradation when another model answered, whatever the answer', () => {
    expect(judgeCandyQualityRun(run({ declaredModel: 'gpt-6-mini' }))).toMatchObject({ kind: 'suspect', reason: null, finalNumber: 21, modelMatches: false })
    expect(judgeCandyQualityRun(run({ declaredModel: 'gpt-6-mini', answer: '这道题需要考虑最坏情况。' }))).toMatchObject({ kind: 'suspect', reason: null, finalNumber: null })
    expect(judgeCandyQualityRun(run({ declaredModel: 'gpt-6-mini', outcome: 'incomplete' }))).toMatchObject({ kind: 'suspect', reason: null })
  })

  it('compares the declared model with the requested model', () => {
    expect(judgeCandyQualityRun(run({ declaredModel: 'gpt-6-mini' })).modelMatches).toBe(false)
    expect(judgeCandyQualityRun(run({ declaredModel: '' })).modelMatches).toBeNull()
    expect(judgeCandyQualityRun(run({ reasoningTokens: null })).reasoningTokens).toBeNull()
  })
})
