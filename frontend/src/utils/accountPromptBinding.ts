// Bindings are supported by the existing conversation account platforms. The
// resolver remains authoritative for account-specific capability checks.
export function supportsAccountPromptBinding(account: { platform: string; type: string }): boolean {
  return ['anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'cindy', 'minimax', 'opencode_go'].includes(account.platform)
}

// A binding dialog handles at most 100 accounts, all of them supported.
export const accountPromptBindingLimit = 100
