import type { PelicanAccountOption, PelicanTestRequest } from '@/api/admin/pelicanTests'

export type PelicanCombination = PelicanTestRequest['targets'][number] & { key: number }

export function pelicanDefaultModel(account?: PelicanAccountOption): string {
  const candidate = account?.models.find(model => model.id === account.default_model_id)
  return candidate?.text_supported ? candidate.id : ''
}

export function pelicanCombinationIssue(row: PelicanCombination, account?: PelicanAccountOption): string {
  if (!account) return 'options_pending'
  if (!row.model_id.trim()) return 'model_required'
  const model = account.models.find(model => model.id === row.model_id.trim())
  if (model && !model.text_supported) return model.capability_reason || 'unsupported'
  if (!model && !account.manual_model_allowed) return account.capability_reason || 'unsupported'
  if (row.effort && (!model || !model.reasoning_efforts.includes(row.effort))) return 'effort_unsupported'
  // A platform without a sender cannot be enabled by manually entering a model.
  if (account.capability_reason && !account.models.length) return account.capability_reason
  return ''
}

export function pelicanDuplicateKeys(rows: PelicanCombination[]): Set<number> {
  const first = new Map<string, number>(), duplicates = new Set<number>()
  for (const row of rows) {
    const identity = `${row.account_id}\0${row.model_id.trim()}`
    if (first.has(identity)) { duplicates.add(first.get(identity)!); duplicates.add(row.key) }
    else first.set(identity, row.key)
  }
  return duplicates
}

// Batch edits leave incompatible rows intact and report them. An unknown manual
// model is allowed only when entered explicitly on that individual account.
export function applyPelicanBulk(rows: PelicanCombination[], accounts: Record<number, PelicanAccountOption>, model: string, effort: string) {
  const skipped: number[] = []
  const updated = rows.map(row => {
    const selected = model.trim() || row.model_id
    const known = accounts[row.account_id]?.models.find(candidate => candidate.id === selected)
    if (!known?.text_supported || (effort && !known.reasoning_efforts.includes(effort))) { skipped.push(row.account_id); return row }
    return { ...row, model_id: selected, effort }
  })
  if (pelicanDuplicateKeys(updated).size) return { rows, skipped: rows.map(row => row.account_id), duplicate: true }
  return { rows: updated, skipped, duplicate: false }
}
