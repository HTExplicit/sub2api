interface PublicModelManagerContext {
  accountIDs?: readonly number[]
  folderIDs?: readonly number[]
  groupIDs?: readonly number[]
  model?: string
}

interface SourceAccount {
  id: number
  management_folder?: { id: number } | null
}

function uniqueIDs(values: readonly number[] = []): number[] {
  return [...new Set(values.filter((value) => Number.isSafeInteger(value) && value > 0))].sort((a, b) => a - b)
}

export function publicModelManagerLocation(context: PublicModelManagerContext = {}) {
  const query: Record<string, string> = { tab: 'overview' }
  for (const [key, ids] of [
    ['account_ids', context.accountIDs],
    ['folder_ids', context.folderIDs],
    ['group_ids', context.groupIDs]
  ] as const) {
    const normalized = uniqueIDs(ids)
    if (normalized.length) query[key] = normalized.join(',')
  }
  if (context.model?.trim()) query.model = context.model.trim()
  return { path: '/admin/account-capabilities', query }
}

// Selection persists across pages/folders. An incomplete local lookup must not
// intersect that selection with the visible folder; let the server resolve it.
export function knownAccountFolderIDs(accountIDs: readonly number[], accounts: readonly SourceAccount[]): number[] {
  const requested = uniqueIDs(accountIDs)
  if (!requested.length) return []
  const byID = new Map(accounts.map((account) => [account.id, account]))
  const folderIDs = requested.map((id) => byID.get(id)?.management_folder?.id)
  if (folderIDs.some((id) => !Number.isSafeInteger(id) || Number(id) <= 0)) return []
  return uniqueIDs(folderIDs as number[])
}
