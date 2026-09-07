import { describe, expect, it } from 'vitest'
import {
  areContextCapacityDraftsValid,
  buildContextOverridePatch,
  findContextCapacityRow,
  formatContextCapacity,
  parseContextCapacityInput
} from '@/utils/modelContextCapacity'

describe('modelContextCapacity', () => {
  it('resolves a unique explicit alias before a colliding direct upstream ID', () => {
    const direct = { upstream_model_id: 'public-model', aliases: [] }
    const target = { upstream_model_id: 'real-target', aliases: ['public-model'] }
    expect(findContextCapacityRow([direct, target], 'public-model')).toBe(target)
    expect(findContextCapacityRow([direct, target], 'real-target')).toBe(target)
    expect(findContextCapacityRow([target, direct], 'public-model')).toBe(target)
  })

  it('does not guess when multiple actual targets claim the same public alias', () => {
    const rows = [
      { upstream_model_id: 'public-model', aliases: [] },
      { upstream_model_id: 'target-a', aliases: ['public-model'] },
      { upstream_model_id: 'target-b', aliases: ['public-model'] }
    ]
    expect(findContextCapacityRow(rows, 'public-model')).toBeUndefined()
    expect(findContextCapacityRow([{ upstream_model_id: 'same', aliases: [] }, { upstream_model_id: 'same', aliases: [] }], 'same')).toBeUndefined()
  })

  it('keeps lookups exact and returns no row for unknown or empty public IDs', () => {
    const rows = [{ upstream_model_id: 'gpt-5.2', aliases: ['Public-Model'] }]
    expect(findContextCapacityRow(rows, 'Public-Model')).toBe(rows[0])
    for (const modelID of ['', ' ', ' gpt-5.2', 'public-model', 'GPT-5.2', 'missing']) {
      expect(findContextCapacityRow(rows, modelID)).toBeUndefined()
    }
  })

  it.each([
    ['258K', 258_000],
    ['1M', 1_000_000],
    ['1.05M', 1_050_000],
    ['  262.144k  ', 262_144],
    ['1.048576M', 1_048_576],
    ['0.000001M', 1],
    ['1.0000000M', 1_000_000],
    ['258000', 258_000],
    ['1.0', 1],
    ['9007199254740991', Number.MAX_SAFE_INTEGER],
    ['9007199254.740991M', Number.MAX_SAFE_INTEGER],
    ['', null],
    ['  ', null]
  ])('parses %s to an exact token integer or clear', (input, expected) => {
    expect(parseContextCapacityInput(input)).toEqual({ valid: true, value: expected })
  })

  it.each([
    '0', '-1', '1.5', '0.0000001M', '1.00000000000000001M',
    '9007199254740992', '9007199254.740992M', 'NaN', 'Infinity',
    '1e6', '258,000', '1Gi', '1KK', '1.2.3M', '1M tokens'
  ])('rejects %s without rounding', (input) => {
    expect(parseContextCapacityInput(input).valid).toBe(false)
  })

  it.each([1, 999, 1_000, 258_000, 262_144, 1_048_576, 1_050_000, Number.MAX_SAFE_INTEGER])(
    'round-trips every token of %s through the decimal display', (tokens) => {
      expect(parseContextCapacityInput(formatContextCapacity(tokens))).toEqual({ valid: true, value: tokens })
    }
  )

  it('formats K/M and handles unknown limits without advertising zero', () => {
    expect(formatContextCapacity(258_000)).toBe('258K')
    expect(formatContextCapacity(1_050_000)).toBe('1.05M')
    expect(formatContextCapacity(1_048_576)).toBe('1.048576M')
    expect(formatContextCapacity(0)).toBe('—')
    expect(formatContextCapacity(undefined)).toBe('—')
  })

  it('builds an incremental patch including explicit clears', () => {
    expect(buildContextOverridePatch({ actual: '1.05M', old: '' })).toEqual({ actual: 1_050_000, old: null })
    expect(buildContextOverridePatch({})).toEqual({})
    expect(areContextCapacityDraftsValid({ actual: '258K', old: '' })).toBe(true)
    expect(areContextCapacityDraftsValid({ invalid: '1.0001K' })).toBe(false)
    expect(() => buildContextOverridePatch({ invalid: '1.0001K' })).toThrow('Invalid context capacity')
  })
})
