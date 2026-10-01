import { describe, expect, it } from 'vitest'
import { nameColumnWidth, nameLinePieces, nameLines, nameRuns, nameWrapParts, type NameCell } from '../softWrap'

// one unit per character: enough to tell which split the widths come from
const chars = (text: string) => text.length
const cell = (text: string): NameCell => ({ texts: [nameRuns(text, chars)] })

describe('nameWrapParts', () => {
  it('breaks an e-mail at its words and before the @', () => {
    expect(nameWrapParts('NightingaleGiovanna7598@hotmail.com')).toEqual(['Nightingale', 'Giovanna', '7598', '@hotmail.com'])
    expect(nameWrapParts('Aetin59137@outlook.com')).toEqual(['Aetin', '59137', '@outlook.com'])
  })

  it('keeps a word with figures inside it whole', () => {
    expect(nameWrapParts('eucadc5ca109engnmw@outlook.com')).toEqual(['eucadc5ca109engnmw', '@outlook.com'])
    expect(nameWrapParts('cindy-global-am332d23b406hthnne-dylancooper2840-2dc78b3c')).toEqual([
      'cindy-global-am332d23b406hthnne-dylancooper',
      '2840-2dc78b3c'
    ])
    expect(nameWrapParts('')).toEqual([])
  })

  it('breaks a URL after its slashes, never at a dot of its host', () => {
    expect(nameWrapParts('claude-https://api2.aigcbest.top/v1')).toEqual(['claude-https://', 'api2.aigcbest.top/', 'v1'])
  })
})

describe('nameLinePieces', () => {
  it('lays a cindy name out at its hyphens, never inside its code', () => {
    expect(nameLinePieces('cindy-global-am332d23b406hthnne-dylancooper2840-2dc78b3c')).toEqual([
      'cindy-', 'global-', 'am332d23b406hthnne-', 'dylancooper', '2840-', '2dc78b3c'
    ])
  })

  it('breaks after a hyphen, not inside a range of figures', () => {
    expect(nameLinePieces('claude-3-5-sonnet')).toEqual(['claude-', '3-5-', 'sonnet'])
    expect(nameLinePieces('ab-1-2')).toEqual(['ab-', '1-2'])
  })

  it('cuts a URL after its slashes and its hyphens', () => {
    expect(nameLinePieces('claude-https://api2.aigcbest.top/v1')).toEqual(['claude-', 'https://', 'api2.aigcbest.top/', 'v1'])
  })
})

describe('nameLines', () => {
  it('ends each line at the last join that fits', () => {
    const mail = nameRuns('NightingaleGiovanna7598@hotmail.com', chars)
    expect(nameLines(mail, 35)).toEqual({ lines: 1, widest: 35 })
    expect(nameLines(mail, 23)).toEqual({ lines: 2, widest: 23 })
    // NightingaleGiovanna | 7598@hotmail.com: an e-mail wider than the column breaks at its word joins too
    expect(nameLines(mail, 19)).toEqual({ lines: 2, widest: 19 })
    // Nightingale | Giovanna7598 | @hotmail.com
    expect(nameLines(mail, 12)).toEqual({ lines: 3, widest: 12 })
  })

  it('hangs a space at the line end', () => {
    // 「openai_28 」 | 「(Copy)」: the first line is 9 wide, its space not counted
    expect(nameLines(nameRuns('openai_28 (Copy)', chars), 10)).toEqual({ lines: 2, widest: 9 })
  })
})

describe('nameColumnWidth', () => {
  it('takes the width of the page\'s usual e-mails; a longer one takes a third line', () => {
    const page = [...Array.from({ length: 10 }, () => cell('Abcde12345@outlook.com')), cell('NightingaleGiovanna7598@hotmail.com')]
    // every Abcde12345 | @outlook.com fills 12; the long one is Nightingale | Giovanna7598 | @hotmail.com, not a
    // 19-wide column for the whole page
    expect(nameColumnWidth(page)).toEqual({ width: 12, piece: 12 })
    expect(nameLines(page[10].texts[0], 12).lines).toBe(3)
  })

  it('never splits a code, so its width is the floor', () => {
    const page = [...Array.from({ length: 5 }, () => cell('cun_001')), cell('eucadc5ca109engnmw@outlook.com')]
    expect(nameColumnWidth(page)).toEqual({ width: 18, piece: 18 })
  })

  it('keeps names on two lines where a narrower column would cost each a line', () => {
    const page = Array.from({ length: 3 }, () => cell('cindy-global-am332d23b406hthnne-dylancooper2840-2dc78b3c'))
    // cindy-global-am332d23b406hthnne- | dylancooper2840-2dc78b3c
    expect(nameColumnWidth(page)).toEqual({ width: 32, piece: 19 })
  })

  it('counts the #id and is empty without names', () => {
    expect(nameColumnWidth([{ texts: [nameRuns('kimi', chars)], fixed: 6 }])).toEqual({ width: 6, piece: 4 })
    expect(nameColumnWidth([])).toEqual({ width: 0, piece: 0 })
  })

  it('lets a token wider than the cap break inside rather than set the column', () => {
    const page = [...Array.from({ length: 5 }, () => cell('cun_0001')), cell('x'.repeat(40))]
    expect(nameColumnWidth(page)).toEqual({ width: 40, piece: 40 })
    expect(nameColumnWidth(page, { pieceCap: 16 })).toEqual({ width: 16, piece: 40 })
  })
})
