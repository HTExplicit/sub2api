/**
 * Admin Scheduled Tests API endpoints
 * Handles scheduled test plan management for account connectivity monitoring
 */

import { accountViewClient } from './accountViewClient'
import type { CapturedAccountView } from '@/composables/useAccountViewContext'
import type {
  ScheduledTestPlan,
  ScheduledTestResult,
  CreateScheduledTestPlanRequest,
  UpdateScheduledTestPlanRequest
} from '@/types'

/**
 * List all scheduled test plans for an account
 * @param accountId - Account ID
 * @returns List of scheduled test plans
 */
export async function listByAccount(accountId: number, view?: CapturedAccountView): Promise<ScheduledTestPlan[]> {
  const { data } = await accountViewClient(view).get<ScheduledTestPlan[]>(
    `/admin/accounts/${accountId}/scheduled-test-plans`
  )
  return data ?? []
}

/**
 * Create a new scheduled test plan
 * @param req - Plan creation request
 * @returns Created plan
 */
export async function create(req: CreateScheduledTestPlanRequest, view?: CapturedAccountView): Promise<ScheduledTestPlan> {
  const { data } = await accountViewClient(view).post<ScheduledTestPlan>(
    '/admin/scheduled-test-plans',
    req
  )
  return data
}

/**
 * Update an existing scheduled test plan
 * @param id - Plan ID
 * @param req - Fields to update
 * @returns Updated plan
 */
export async function update(id: number, req: UpdateScheduledTestPlanRequest, view?: CapturedAccountView): Promise<ScheduledTestPlan> {
  const { data } = await accountViewClient(view).put<ScheduledTestPlan>(
    `/admin/scheduled-test-plans/${id}`,
    req
  )
  return data
}

/**
 * Delete a scheduled test plan
 * @param id - Plan ID
 */
export async function deletePlan(id: number, view?: CapturedAccountView): Promise<void> {
  await accountViewClient(view).delete(`/admin/scheduled-test-plans/${id}`)
}

/**
 * List test results for a plan
 * @param planId - Plan ID
 * @param limit - Optional max number of results to return
 * @returns List of test results
 */
export async function listResults(planId: number, limit?: number, view?: CapturedAccountView): Promise<ScheduledTestResult[]> {
  const { data } = await accountViewClient(view).get<ScheduledTestResult[]>(
    `/admin/scheduled-test-plans/${planId}/results`,
    {
      params: limit ? { limit } : undefined
    }
  )
  return data ?? []
}

export const scheduledTestsAPI = {
  listByAccount,
  create,
  update,
  delete: deletePlan,
  listResults
}

export default scheduledTestsAPI

export function scheduledTestsForView(view?: CapturedAccountView, core = scheduledTestsAPI): typeof scheduledTestsAPI {
  if (!view) return core
  return {
    listByAccount: id => core.listByAccount(id, view),
    create: request => core.create(request, view),
    update: (id, request) => core.update(id, request, view),
    delete: id => core.delete(id, view),
    listResults: (id, limit) => core.listResults(id, limit, view)
  }
}
