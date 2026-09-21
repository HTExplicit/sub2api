import { describe, expect, it } from 'vitest'

import {
  CINDY_OPENAI_DEFAULTS,
  isCindyOpenAIAPIKeyAccount
} from '@/utils/cindyOpenAIDefaults'

describe('Cindy OpenAI defaults', () => {
  it('detects canonical Cindy and the exact legacy import projection only', () => {
    expect(
      isCindyOpenAIAPIKeyAccount({
        platform: 'cindy',
        type: 'apikey',
        credentials: {}
      })
    ).toBe(true)
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

  it('keeps only the account-level Responses transport default', () => {
    expect(CINDY_OPENAI_DEFAULTS).toEqual({
      responsesMode: 'force_responses'
    })
  })

  // Test-model eligibility/default assertions now live with their sole owner:
  // plugins/cindy-provider/catalog/account_test_projection_test.go.
})
