import type { PluginContribution } from '@/api/admin/plugins'
import type { AccountSelectionIdentity } from '@/composables/useAccountSelectionMetadata'

export interface ContributionSelection {
  account?: AccountSelectionIdentity | null
  accountIds?: number[]
  accounts?: AccountSelectionIdentity[]
}
export interface ContributionAdmission { allowed: boolean; reason?: 'unavailable' | 'unknown' | 'outside_scope' }

/** account_scope is the host's intersection of this owner's required capabilities. */
export function createContributionAdmission(item: PluginContribution, target: { platform: string; accountType: string }): ContributionAdmission {
  if (!item.available) return { allowed: false, reason: 'unavailable' }
  const profile = item.account_create
  if (item.slot !== 'account.create.v1' || item.permission !== 'admin' || item.capability !== 'extensions.provider.v1' ||
      !profile || profile.version !== 1 || profile.platform !== target.platform || profile.account_type !== target.accountType ||
      item.account_filter || item.all_accounts || item.retained_controls || item.action || item.entrypoint) return { allowed: false, reason: 'unknown' }
  const scope = item.account_scope
  if (scope?.version !== 1 || !scope.bindings.some(binding =>
    (binding.platform === '*' || binding.platform === target.platform) &&
    (binding.account_type === '*' || binding.account_type === target.accountType) && binding.rollout_percent === 100)) {
    return { allowed: false, reason: 'outside_scope' }
  }
  return { allowed: true }
}

// Keep uint64 overflow identical to stablePluginBucket in the host; Number
// multiplication loses enough bits to choose a different gray-release bucket.
export function contributionAccountBucket(id: number): number | null {
  if (!Number.isSafeInteger(id) || id <= 0) return null
  let value = BigInt(id)
  value ^= value >> 33n
  value = BigInt.asUintN(64, value * 0xff51afd7ed558ccdn)
  value ^= value >> 33n
  return Number(value % 100n)
}

export function contributionAdmission(item: PluginContribution, selection: ContributionSelection = {}): ContributionAdmission {
  if (!item.available) return { allowed: false, reason: 'unavailable' }
  const ids = selection.account ? [selection.account.id] : selection.accountIds || []
  if (!ids.length) {
    if (item.account_filter || (item.account_scope && item.slot.startsWith('account.'))) return { allowed: false, reason: 'unknown' }
    // Shared management has no account target. Global-only resources and
    // public themes are checked by the host, not by hashing an artificial ID 0.
    return { allowed: true }
  }
  const known = new Map((selection.accounts || []).map(account => [account.id, account]))
  if (selection.account) known.set(selection.account.id, selection.account)
  for (const id of ids) {
    const account = known.get(id), bucket = contributionAccountBucket(id)
    if (!account || bucket === null) return { allowed: false, reason: 'unknown' }
    const filter = item.account_filter
    if (filter && ((filter.platforms?.length && !filter.platforms.includes(account.platform)) ||
      (filter.types?.length && !filter.types.includes(account.type)) ||
      (filter.statuses?.length && !filter.statuses.includes(account.status || '')) ||
      (filter.exclude_shadows && account.parent_account_id != null))) return { allowed: false, reason: 'outside_scope' }
    const scope = item.account_scope
    if (scope && (scope.version !== 1 || !scope.bindings.some(binding =>
      (binding.platform === '*' || binding.platform === account.platform) &&
      (binding.account_type === '*' || binding.account_type === account.type) &&
      Number.isInteger(binding.rollout_percent) && binding.rollout_percent >= 0 && binding.rollout_percent <= 100 &&
      bucket < binding.rollout_percent))) return { allowed: false, reason: 'outside_scope' }
  }
  return { allowed: true }
}
