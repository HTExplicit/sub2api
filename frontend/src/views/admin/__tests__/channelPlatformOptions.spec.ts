import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

describe('Composite channel platform options', () => {
  it('includes all ten concrete providers for pricing and model mapping', () => {
    const source = readFileSync(resolve('src/views/admin/ChannelsView.vue'), 'utf8')
    const declaration = source.match(/const compositePlatforms:[^=]+=[^\n]+/)?.[0]
    const order = source.match(/const platformOrder:[^=]+=[^\n]+/)?.[0]
    const expectedPlatforms = ['anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'minimax', 'cindy']

    expect(declaration?.match(/'([^']+)'/g)?.map(value => value.slice(1, -1))).toEqual(expectedPlatforms)
    expect(order?.match(/'([^']+)'/g)?.map(value => value.slice(1, -1))).toEqual(expectedPlatforms)
  })
})
