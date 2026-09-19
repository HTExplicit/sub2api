import type { Account } from '@/types'

export type TicketAccountIdentity = Pick<Account, 'id' | 'platform' | 'type' | 'parent_account_id'>

// Match the backend's IsOpenAIOAuthLike + non-shadow ownership contract.
// Account status and business schedulability do not control ticket ownership.
export function canManageCodexTickets(account?: TicketAccountIdentity | null): boolean {
  return !!account && account.platform === 'openai'
    && (account.type === 'oauth' || account.type === 'setup-token')
    && account.parent_account_id == null
}

export function allCanManageCodexTickets(ids: number[], accounts: Map<number, TicketAccountIdentity>): boolean {
  return ids.length > 0 && ids.every(id => canManageCodexTickets(accounts.get(id)))
}
