// Account prompt bindings apply to OpenAI and Cindy accounts that call the
// upstream with their own credentials.
export function supportsAccountPromptBinding(account: { platform: string; type: string }): boolean {
  return ['openai', 'cindy'].includes(account.platform) && ['oauth', 'setup-token', 'apikey'].includes(account.type)
}

// A binding dialog handles at most 100 accounts, all of them supported.
export const accountPromptBindingLimit = 100
