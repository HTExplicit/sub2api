import type { CapabilityPlan, CapabilityPlanModel, CapabilityPlanRequest } from '@/api/admin/accountCapabilities'

type ImpactRoute = NonNullable<CapabilityPlan['impact']['added_routes']>[number]

// A restored preview needs its business impact, not the billable request body.
// Exact targets, evidence IDs, configuration fingerprints and JSON diffs stay
// server-side and are never written to browser recovery storage.
export type CapabilityDisplayPlan = Omit<CapabilityPlan, 'impact' | 'exclusions'> & {
  impact: Omit<CapabilityPlan['impact'], 'added_routes'> & {
    added_routes?: Array<Omit<ImpactRoute, 'upstream_model' | 'protocol'>>
  }
  exclusions: Array<Omit<CapabilityPlan['exclusions'][number], 'upstream_model'>>
}

export interface CapabilitySession {
  version: 1
  request: CapabilityPlanRequest
  runID?: number
  receiptKey?: string
  changesetID?: number
  summary?: Pick<CapabilityDisplayPlan, 'scope' | 'impact' | 'exclusions' | 'warnings'>
}

function ids(values: readonly number[] = []): number[] {
  return [...new Set(values)].sort((a, b) => a - b)
}

export function capabilitySessionKey(folderIDs: number[], accountIDs: number[], groupIDs: number[], model = ''): string {
  let actor = 'current-session'
  try {
    const user = JSON.parse(localStorage.getItem('auth_user') || 'null') as { id?: number } | null
    if (Number.isSafeInteger(user?.id) && user!.id! > 0) actor = String(user!.id)
  } catch { /* Browser storage may be disabled. No token is read or persisted. */ }
  return `public-model-organizer-v1:${actor}:${JSON.stringify([ids(folderIDs), ids(accountIDs), ids(groupIDs), model.trim().toLowerCase()])}`
}

function publicModel(value: CapabilityPlanModel): CapabilityPlanModel {
  return { group_id: value.group_id, group_name: value.group_name, public_model: value.public_model }
}

export function capabilitySessionSummary(plan: CapabilityDisplayPlan): NonNullable<CapabilitySession['summary']> {
  return {
    scope: { folder_ids: [...plan.scope.folder_ids], account_ids: [...plan.scope.account_ids] },
    impact: {
      added_models: plan.impact.added_models.map(publicModel),
      added_accounts: plan.impact.added_accounts.map((item) => ({ ...publicModel(item), account_id: item.account_id, account_name: item.account_name })),
      added_routes: plan.impact.added_routes?.map((item) => ({ ...publicModel(item), account_id: item.account_id, account_name: item.account_name })),
      retained_models: plan.impact.retained_models.map(publicModel),
      removed_models: plan.impact.removed_models.map(publicModel),
      removed_accounts: plan.impact.removed_accounts.map((item) => ({ ...publicModel(item), account_id: item.account_id, account_name: item.account_name })),
      reused_success_count: plan.impact.reused_success_count,
    },
    exclusions: plan.exclusions.map((item) => ({ account_id: item.account_id, public_model: item.public_model, reason: /^[a-z][a-z0-9_]*$/.test(item.reason) ? item.reason : 'not_publishable' })),
    warnings: plan.warnings.filter((warning) => /^[a-z][a-z0-9_]*$/.test(warning)),
  }
}

export function writeCapabilitySession(key: string, session: CapabilitySession): void {
  try { sessionStorage.setItem(key, JSON.stringify(session)) } catch { /* Keep the in-memory operation usable if storage is unavailable. */ }
}

function validIDs(value: unknown): value is number[] {
  return Array.isArray(value) && value.every((id) => Number.isSafeInteger(id) && id > 0)
}

export function readCapabilitySession(key: string): CapabilitySession | null {
  try {
    const saved = JSON.parse(sessionStorage.getItem(key) || 'null') as CapabilitySession | null
    if (saved?.version !== 1 || !validIDs(saved.request?.scope?.folder_ids) || !validIDs(saved.request.scope.account_ids)) return null
    if (!saved.runID && !saved.receiptKey && !saved.changesetID) return null
    if ([saved.runID, saved.changesetID].some((id) => id !== undefined && (!Number.isSafeInteger(id) || id <= 0))) return null
    if (saved.receiptKey !== undefined && (typeof saved.receiptKey !== 'string' || !saved.receiptKey || saved.receiptKey.length > 255)) return null
    if (saved.request.group_ids !== undefined && !validIDs(saved.request.group_ids)) return null
    if (saved.request.models !== undefined && (!Array.isArray(saved.request.models) || saved.request.models.some((model) => typeof model?.public_model !== 'string'))) return null
    if (saved.changesetID && (!saved.summary || !Array.isArray(saved.summary.impact?.added_models) || !Array.isArray(saved.summary.impact.added_accounts) || !Array.isArray(saved.summary.impact.retained_models) || !Array.isArray(saved.summary.impact.removed_models) || !Array.isArray(saved.summary.impact.removed_accounts) || !Array.isArray(saved.summary.exclusions) || !Array.isArray(saved.summary.warnings))) return null
    return saved
  } catch { return null }
}

export function clearCapabilitySession(key: string): void {
  try { sessionStorage.removeItem(key) } catch { /* Recovery storage is optional; no business configuration is changed. */ }
}
