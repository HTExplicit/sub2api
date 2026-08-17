import type { AccountAvailableModel } from '@/types'

export const CINDY_OPENAI_DEFAULTS = {
  responsesMode: 'force_responses',
  alphaSearchMode: 'responses_web_search',
  promptCacheKeyMode: 'sha256_64'
} as const

export interface CindyAccountLike {
  platform?: unknown
  type?: unknown
  is_cindy?: unknown
  credentials?: Record<string, unknown> | null
}

export function isCindyOpenAIAPIKeyAccount(account: CindyAccountLike | null | undefined): boolean {
  if (!account || account.platform !== 'openai' || account.type !== 'apikey') return false
  if (account.is_cindy === true) return true

  const rawBaseUrl = account.credentials?.base_url
  if (typeof rawBaseUrl !== 'string') return false
  try {
    const parsed = new URL(rawBaseUrl.trim())
    return (
      parsed.protocol === 'https:' &&
      parsed.hostname.toLowerCase() === 'api.laxarouter.ai' &&
      parsed.port === '' &&
      parsed.username === '' &&
      parsed.password === '' &&
      parsed.search === '' &&
      parsed.hash === '' &&
      (parsed.pathname === '' || parsed.pathname === '/')
    )
  } catch {
    return false
  }
}

const CINDY_ACCOUNT_TEST_ENDPOINTS = new Set(['responses', 'images.generations'])

export function filterCindyAccountTestModels(
  account: CindyAccountLike | null | undefined,
  models: AccountAvailableModel[]
): AccountAvailableModel[] {
  if (!isCindyOpenAIAPIKeyAccount(account)) return models

  return models.filter(model =>
    model.managed === true &&
    model.verified === true &&
    model.endpoints?.some(endpoint => CINDY_ACCOUNT_TEST_ENDPOINTS.has(endpoint)) === true
  )
}

export function pickCindyAccountTestDefault(
  account: CindyAccountLike | null | undefined,
  models: AccountAvailableModel[]
): AccountAvailableModel | undefined {
  if (!isCindyOpenAIAPIKeyAccount(account)) return undefined

  const responsesModels = models.filter(model => model.endpoints?.includes('responses'))
  return responsesModels.find(model => model.id === 'gpt-5.6-luna') ||
    responsesModels.find(model => model.id === 'gpt-5.6-sol') ||
    responsesModels[0]
}
