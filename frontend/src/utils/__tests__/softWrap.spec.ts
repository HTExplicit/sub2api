import { describe, expect, it } from 'vitest'
import { nameLinePieces, nameWidths, nameWrapParts } from '../softWrap'

// one unit per character: enough to tell which split the widths come from
const chars = (text: string) => text.length

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
})

describe('nameWidths', () => {
  it('lays a cindy name out at its hyphens, never inside its code', () => {
    const name = 'cindy-global-am332d23b406hthnne-dylancooper2840-2dc78b3c'
    expect(nameLinePieces(name)).toEqual(['cindy-', 'global-', 'am332d23b406hthnne-', 'dylancooper', '2840-', '2dc78b3c'])
    expect(nameWidths(name, chars)).toEqual({ one: name.length, two: 'cindy-global-am332d23b406hthnne-'.length, piece: 19 })
  })

  it('breaks an e-mail before its @ only', () => {
    const mail = 'NightingaleGiovanna7598@hotmail.com'
    expect(nameWidths(mail, chars)).toEqual({ one: mail.length, two: 'NightingaleGiovanna7598'.length, piece: 12 })
  })

  it('breaks after a hyphen, not inside a range of figures', () => {
    expect(nameLinePieces('claude-3-5-sonnet')).toEqual(['claude-', '3-5-', 'sonnet'])
    expect(nameLinePieces('ab-1-2')).toEqual(['ab-', '1-2'])
  })

  it('breaks after a space, the space hanging at the line end', () => {
    // 「openai_28 」 | 「(Copy)」: the first line is 9 wide, its space not counted
    expect(nameWidths('openai_28 (Copy)', chars)).toEqual({ one: 16, two: 9, piece: 9 })
  })
})
