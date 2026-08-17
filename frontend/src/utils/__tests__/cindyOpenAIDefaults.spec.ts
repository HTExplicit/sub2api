import { describe, expect, it } from 'vitest'

import {
  CINDY_OPENAI_DEFAULTS,
  filterCindyAccountTestModels,
  pickCindyAccountTestDefault,
  isCindyOpenAIAPIKeyAccount
} from '@/utils/cindyOpenAIDefaults'
import type { AccountAvailableModel } from '@/types'

const model = (
  id: string,
  endpoints: string[],
  options: Partial<AccountAvailableModel> = {}
): AccountAvailableModel => ({
  id,
  type: 'model',
  display_name: id,
  created_at: '',
  managed: true,
  verified: true,
  endpoints,
  ...options
})

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

  it('keeps only verified Cindy account-test endpoints and prefers Luna', () => {
    const account = {
      platform: 'openai',
      type: 'apikey',
      credentials: { base_url: 'https://api.laxarouter.ai' }
    }
    const models = [
      model('cindy/auto-review', ['cindy.reviews']),
      model('candidate-unverified', ['responses'], { verified: false }),
      model('claude-sonnet-5', ['messages']),
      model('gpt-image-2', ['images.generations']),
      model('gpt-5.6-sol', ['responses']),
      model('gpt-5.6-luna', ['responses'])
    ]

    const filtered = filterCindyAccountTestModels(account, models)
    expect(filtered.map(item => item.id)).toEqual([
      'gpt-image-2',
      'gpt-5.6-sol',
      'gpt-5.6-luna'
    ])
    expect(pickCindyAccountTestDefault(account, filtered)?.id).toBe('gpt-5.6-luna')
  })
})
