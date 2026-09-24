import type { AccountSelectionIdentity } from '@/composables/useAccountSelectionMetadata'

export type CodexTicketOperation = 'harvest' | 'stop'
export const codexTicketAccountLimit = 100

export function supportsCodexTickets(account: AccountSelectionIdentity): boolean {
  return account.platform === 'openai' && ['oauth', 'setup-token'].includes(account.type) && account.parent_account_id == null
}

export function isCodexTicketSelection(ids: number[], accounts: AccountSelectionIdentity[]): boolean {
  if (!ids.length || ids.length > codexTicketAccountLimit || new Set(ids).size !== ids.length) return false
  const known = new Map(accounts.map(account => [account.id, account]))
  return ids.every(id => Number.isSafeInteger(id) && id > 0 && !!known.get(id) && supportsCodexTickets(known.get(id)!))
}
