import type { RouteRecordRaw } from 'vue-router'
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

type GuardTarget = {
  path: string
  fullPath: string
  name?: RouteRecordRaw['name']
  params: Record<string, string>
  meta: Record<string, unknown>
}
type NavigationGuard = (to: GuardTarget, from: Record<string, unknown>, next: ReturnType<typeof vi.fn>) => Promise<void>

const harness = vi.hoisted(() => ({
  routes: [] as RouteRecordRaw[],
  guard: null as NavigationGuard | null,
}))
const authStore = vi.hoisted(() => ({
  checkAuth: vi.fn(),
  isAuthenticated: true,
  isAdmin: true,
  isSimpleMode: false,
  hasPendingAuthSession: false,
}))
const appStore = vi.hoisted(() => ({
  siteName: 'Sub2API',
  backendModeEnabled: false,
  publicSettingsLoaded: true,
  cachedPublicSettings: { custom_menu_items: [] },
}))

vi.mock('vue-router', () => ({
  createWebHistory: vi.fn(() => ({})),
  createRouter: vi.fn((options: { routes: RouteRecordRaw[] }) => {
    harness.routes = options.routes
    return {
      beforeEach: vi.fn((guard: NavigationGuard) => { harness.guard = guard }),
      afterEach: vi.fn(),
      onError: vi.fn(),
    }
  }),
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => authStore }))
vi.mock('@/stores/app', () => ({ useAppStore: () => appStore }))
vi.mock('@/stores/adminSettings', () => ({
  useAdminSettingsStore: () => ({ customMenuItems: [] }),
}))
vi.mock('@/stores/adminCompliance', () => ({
  useAdminComplianceStore: () => ({ initialized: true }),
}))
vi.mock('@/composables/useNavigationLoading', () => ({
  useNavigationLoadingState: () => ({ startNavigation: vi.fn(), endNavigation: vi.fn() }),
}))
vi.mock('@/composables/useRoutePrefetch', () => ({
  useRoutePrefetch: () => ({ triggerPrefetch: vi.fn() }),
}))
vi.mock('@/router/title', () => ({ resolveRouteDocumentTitle: () => 'Sub2API' }))

const borrowRoutes = [
  { path: '/admin/codex-gateway-borrow', name: 'AdminCodexGatewayBorrow' },
  { path: '/admin/codex-gateway-borrow/status', name: 'AdminCodexGatewayBorrowStatus' },
  { path: '/admin/codex-pelican-comparison', name: 'AdminCodexPelicanComparison' },
]

async function navigate(path: string) {
  const record = harness.routes.find(route => route.path === path)
  if (!record || !harness.guard) throw new Error(`Missing route or guard for ${path}`)
  const next = vi.fn()
  await harness.guard({ path, fullPath: path, name: record.name, params: {}, meta: record.meta ?? {} }, {}, next)
  return next
}

describe('Codex borrow page routes', () => {
  beforeAll(async () => { await import('@/router') })
  beforeEach(() => {
    authStore.isAuthenticated = true
    authStore.isAdmin = true
    authStore.isSimpleMode = false
  })

  it.each(borrowRoutes)('keeps $path directly addressable and protected', ({ path, name }) => {
    const record = harness.routes.find(route => route.path === path)

    expect(record?.name).toBe(name)
    expect(record?.meta).toMatchObject({ requiresAuth: true, requiresAdmin: true })
    expect(record).not.toHaveProperty('redirect')
  })

  it.each(borrowRoutes)('allows admins to open $path in simple mode', async ({ path }) => {
    authStore.isSimpleMode = true
    const next = await navigate(path)

    expect(next).toHaveBeenCalledOnce()
    expect(next).toHaveBeenCalledWith()
  })

  it.each(borrowRoutes)('blocks regular users from $path', async ({ path }) => {
    authStore.isAdmin = false
    expect(await navigate(path)).toHaveBeenCalledWith('/dashboard')
  })

  it.each(borrowRoutes)('requires sign-in for $path and preserves the return address', async ({ path }) => {
    authStore.isAuthenticated = false
    authStore.isAdmin = false
    expect(await navigate(path)).toHaveBeenCalledWith({ path: '/login', query: { redirect: path } })
  })
})
