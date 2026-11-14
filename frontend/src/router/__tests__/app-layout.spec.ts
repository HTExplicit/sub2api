import { beforeAll, describe, expect, it, vi } from 'vitest'
import type { RouteRecordRaw } from 'vue-router'

const harness = vi.hoisted(() => ({ routes: [] as RouteRecordRaw[] }))
vi.mock('vue-router', () => ({
  createWebHistory: () => ({}),
  createRouter: (options: { routes: RouteRecordRaw[] }) => {
    harness.routes = options.routes
    return { beforeEach: vi.fn(), afterEach: vi.fn(), onError: vi.fn() }
  },
}))
vi.mock('@/composables/useNavigationLoading', () => ({
  useNavigationLoadingState: () => ({ startNavigation: vi.fn(), endNavigation: vi.fn() }),
}))
vi.mock('@/composables/useRoutePrefetch', () => ({ useRoutePrefetch: vi.fn() }))

describe('console layout route ownership', () => {
  beforeAll(async () => { await import('@/router') })

  it.each([
    '/dashboard', '/keys', '/usage', '/admin/dashboard', '/admin/accounts', '/admin/usage',
    '/admin/users', '/admin/groups', '/admin/settings', '/admin/audit-logs',
    '/monitor', '/admin/affiliates/invites', '/admin/affiliates/rebates',
    '/admin/affiliates/transfers', '/payment/airwallex',
  ])('uses the persistent console shell for %s', (path) => {
    const route = harness.routes.find(record => record.path === path)
    expect(route, `Missing route ${path}`).toBeDefined()
    expect(route?.meta?.appLayout).toBe(true)
  })

  it.each([
    '/admin/ops', '/payment/stripe', '/payment/stripe-popup', '/model-plaza',
    '/payment/result', '/login', '/home', '/key-usage',
  ])('keeps conditional and standalone layouts page-owned for %s', (path) => {
    const route = harness.routes.find(record => record.path === path)
    expect(route, `Missing route ${path}`).toBeDefined()
    expect(route?.meta?.appLayout).not.toBe(true)
  })
})
