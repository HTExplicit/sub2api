import type { AccountAvailableModel, AccountTestPlanView } from './api'

export function isAccountTestReasoningValid(model: AccountAvailableModel | undefined, effort: string): boolean {
  return effort === '' || model?.reasoning_efforts?.includes(effort) === true
}

// Only structural validation is duplicated across isolated UI builds. Model
// eligibility, order and defaults arrive from the single server-side view.
export function validateAccountTestPlan(value: unknown, accountID: number): AccountTestPlanView {
  const plan = value as AccountTestPlanView | null
  if (!plan || plan.schema_version !== 1 || plan.account_id !== accountID || !Number.isSafeInteger(accountID) || accountID <= 0 ||
      typeof plan.wire_platform !== 'string' || !plan.wire_platform || typeof plan.default_mode !== 'string' ||
      !Array.isArray(plan.models) || !plan.mode_views || typeof plan.mode_views !== 'object' || Array.isArray(plan.mode_views) ||
      !Object.prototype.hasOwnProperty.call(plan.mode_views, plan.default_mode)) throw new Error('Invalid account test plan')
  const ids = new Set<string>()
  for (const model of plan.models) {
    if (!model || typeof model.id !== 'string' || !model.id || ids.has(model.id) ||
        typeof model.display_name !== 'string' || !model.display_name) throw new Error('Invalid account test model')
    ids.add(model.id)
  }
  for (const view of Object.values(plan.mode_views)) {
    if (!view || !Array.isArray(view.model_ids) || new Set(view.model_ids).size !== view.model_ids.length ||
        view.model_ids.some(id => typeof id !== 'string' || !ids.has(id)) || typeof view.default_model_id !== 'string' ||
        (view.default_model_id !== '' && !view.model_ids.includes(view.default_model_id))) throw new Error('Invalid account test mode view')
  }
  return plan
}

export function accountTestModelsForMode(plan: AccountTestPlanView): AccountAvailableModel[] {
  const byID = new Map(plan.models.map(model => [model.id, model]))
  return plan.mode_views[plan.default_mode]!.model_ids.map(id => byID.get(id)!).filter(Boolean)
}

export function defaultAccountTestModel(plan: AccountTestPlanView): string {
  return plan.mode_views[plan.default_mode]!.default_model_id
}
