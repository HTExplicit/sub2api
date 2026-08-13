import { describe, expect, it } from 'vitest'

import {
  CINDY_OPENAI_DEFAULTS,
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

  it('uses the proven Responses web-search bridge with a Cindy-safe cache key', () => {
    expect(CINDY_OPENAI_DEFAULTS).toEqual({
      responsesMode: 'force_responses',
      alphaSearchMode: 'responses_web_search',
      promptCacheKeyMode: 'sha256_64'
    })
  })
})
