import { describe, expect, it } from 'vitest'

import {
  CINDY_OPENAI_DEFAULTS,
  cindyFirst,
  isCindyOpenAIAPIKeyAccount
} from '@/utils/cindyOpenAIDefaults'

describe('Cindy OpenAI defaults', () => {
  it('detects only the strict Cindy API-key endpoint or backend marker', () => {
    expect(
      isCindyOpenAIAPIKeyAccount({
        platform: 'openai',
        type: 'apikey',
        credentials: { base_url: 'https://API.LAXAROUTER.AI/' }
      })
    ).toBe(true)
    expect(
      isCindyOpenAIAPIKeyAccount({
        platform: 'openai',
        type: 'apikey',
        is_cindy: true,
        credentials: {}
      })
    ).toBe(true)
    expect(
      isCindyOpenAIAPIKeyAccount({
        platform: 'openai',
        type: 'apikey',
        credentials: { base_url: 'https://api.laxarouter.ai/v1' }
      })
    ).toBe(false)
    expect(
      isCindyOpenAIAPIKeyAccount({
        platform: 'openai',
        type: 'apikey',
        credentials: { base_url: 'https://api.openai.com' }
      })
    ).toBe(false)
  })

  it('puts Cindy defaults first without changing ordinary OpenAI ordering', () => {
    const alpha = [{ value: 'direct' }, { value: 'responses_web_search' }, { value: 'disabled' }]
    const cache = [{ value: 'passthrough' }, { value: 'sha256_64' }]

    expect(cindyFirst(alpha, true, CINDY_OPENAI_DEFAULTS.alphaSearchMode)[0].value).toBe(
      'responses_web_search'
    )
    expect(cindyFirst(cache, true, CINDY_OPENAI_DEFAULTS.promptCacheKeyMode)[0].value).toBe('sha256_64')
    expect(cindyFirst(alpha, false, CINDY_OPENAI_DEFAULTS.alphaSearchMode)[0].value).toBe('direct')
    expect(cindyFirst(cache, false, CINDY_OPENAI_DEFAULTS.promptCacheKeyMode)[0].value).toBe('passthrough')
  })
})
