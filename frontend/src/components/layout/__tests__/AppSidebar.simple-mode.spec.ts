import { mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AppSidebar from '../AppSidebar.vue'

const mocks = vi.hoisted(() => ({
  appStore: {
    sidebarCollapsed: false,
    mobileOpen: false,
    sidebarScrollTop: 0,
    siteName: 'Sub2API',
    siteLogo: '',
    siteVersion: 'test',
    publicSettingsLoaded: true,
    backendModeEnabled: false,
    cachedPublicSettings: {
      custom_menu_items: [],
    },
    toggleSidebar: vi.fn(),
    setMobileOpen: vi.fn(),
  },
  authStore: {
    isAdmin: false,
    isSimpleMode: true,
  },
  onboardingStore: {
    isCurrentStep: vi.fn(() => false),
    nextStep: vi.fn(),
  },
  adminSettingsStore: {
    opsMonitoringEnabled: false,
    paymentEnabled: false,
    customMenuItems: [],
    fetch: vi.fn(),
  },
  refreshBatchImageAccess: vi.fn(async () => false),
}))

vi.mock('@/stores', () => ({
  useAppStore: () => mocks.appStore,
  useAuthStore: () => mocks.authStore,
  useOnboardingStore: () => mocks.onboardingStore,
  useAdminSettingsStore: () => mocks.adminSettingsStore,
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => mocks.appStore,
}))

vi.mock('@/composables/useBatchImageAccess', () => ({
  useBatchImageAccess: () => ({
    canUseBatchImage: { value: false },
    refreshBatchImageAccess: mocks.refreshBatchImageAccess,
  }),
}))

vi.mock('vue-i18n', async importOriginal => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({ locale: { value: 'en' }, t: (key: string) => key }),
  }
})

async function renderSidebar(options: { admin?: boolean; simple?: boolean; path?: string } = {}) {
  mocks.authStore.isAdmin = options.admin === true
  mocks.authStore.isSimpleMode = options.simple !== false

  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { template: '<div />' } }],
  })
  await router.push(options.path ?? (options.admin ? '/admin/dashboard' : '/dashboard'))
  await router.isReady()

  return mount(AppSidebar, {
    global: {
      plugins: [router],
      stubs: {
        VersionBadge: { template: '<span data-test="version" />' },
      },
    },
  })
}

function extensionLinks(wrapper: ReturnType<typeof mount>): string[] {
  return wrapper
    .get('[data-testid="sidebar-extensions"]')
    .findAll('a')
    .map(link => link.attributes('href'))
}

describe('AppSidebar simple mode extensions', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    document.documentElement.classList.remove('dark')
  })

  it.each([true, false])('renders no extensions section for a regular user (simple: %s)', async simple => {
    const wrapper = await renderSidebar({ simple })

    expect(wrapper.find('[data-testid="sidebar-extensions"]').exists()).toBe(false)
    if (simple) expect(wrapper.find('a[href="/usage"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it.each([true, false])('renders independent borrowing and Pelican entries (simple: %s)', async simple => {
    const wrapper = await renderSidebar({ admin: true, simple })

    expect(extensionLinks(wrapper)).toEqual([
      '/admin/system-prompts',
      '/admin/codex',
      '/admin/pelican-tests',
    ])
    if (simple) expect(wrapper.text()).not.toContain('nav.myAccount')

    wrapper.unmount()
    const regularUser = await renderSidebar({ simple })
    expect(regularUser.find('a[href="/admin/codex"]').exists()).toBe(false)
    expect(regularUser.find('a[href="/admin/codex/identity"]').exists()).toBe(false)
    expect(regularUser.find('a[href="/admin/codex-pelican-comparison"]').exists()).toBe(false)
    expect(regularUser.find('a[href="/admin/pelican-tests"]').exists()).toBe(false)
    regularUser.unmount()
  })

  it.each([
    '/admin/codex',
    '/admin/codex/identity',
  ])('keeps the single borrowing entry active on %s', async path => {
    const wrapper = await renderSidebar({ admin: true, path })
    const section = wrapper.get('[data-testid="sidebar-extensions"]')

    expect(section.get('a[href="/admin/codex"]').classes()).toContain('sidebar-link-active')
    expect(section.find('a[href="/admin/codex/identity"]').exists()).toBe(false)
    expect(section.find('a[href="/admin/codex-pelican-comparison"]').exists()).toBe(false)
    expect(section.findAll('.sidebar-link-active')).toHaveLength(1)
    wrapper.unmount()
  })

  it('highlights only Pelican Tests on its independent page', async () => {
    const wrapper = await renderSidebar({ admin: true, path: '/admin/pelican-tests' })
    const section = wrapper.get('[data-testid="sidebar-extensions"]')
    expect(section.get('a[href="/admin/pelican-tests"]').classes()).toContain('sidebar-link-active')
    expect(section.get('a[href="/admin/codex"]').classes()).not.toContain('sidebar-link-active')
    expect(section.findAll('.sidebar-link-active')).toHaveLength(1)
    wrapper.unmount()
  })
})
