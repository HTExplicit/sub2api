export const CINDY_OPENAI_DEFAULTS = {
  responsesMode: 'force_responses',
  alphaSearchMode: 'direct',
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
