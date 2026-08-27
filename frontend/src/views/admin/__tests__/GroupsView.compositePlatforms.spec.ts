import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { CONCRETE_PLATFORM_OPTIONS } from '@/constants/platforms'

describe('GroupsView Composite route options', () => {
  it('offers Kimi, Zhipu GLM, and DeepSeek as route targets', () => {
    expect(CONCRETE_PLATFORM_OPTIONS.map((option) => option.value)).toEqual(
      expect.arrayContaining(['kimi', 'zhipu', 'deepseek'])
    )
  })

  it('keeps canonical Cindy out of Composite route targets', () => {
    const source = readFileSync('src/views/admin/GroupsView.vue', 'utf8')
    expect(source).toMatch(/compositeRoutePlatformOptions[\s\S]*?option\.value !== ["']cindy["']/)
  })
})
