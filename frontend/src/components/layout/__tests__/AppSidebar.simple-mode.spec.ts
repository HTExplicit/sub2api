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
      image_studio_enabled: true,
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
    useI18n: () => ({ t: (key: string) => key }),
  }
})

async function renderSidebar(options: { admin?: boolean; imageStudio?: boolean } = {}) {
  mocks.authStore.isAdmin = options.admin === true
  mocks.authStore.isSimpleMode = true
  mocks.appStore.cachedPublicSettings.image_studio_enabled = options.imageStudio !== false

  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { template: '<div />' } }],
  })
  await router.push(options.admin ? '/admin/dashboard' : '/dashboard')
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

  it('renders Image Studio for a regular user when the opt-in flag is enabled', async () => {
    const wrapper = await renderSidebar({ imageStudio: true })

    expect(extensionLinks(wrapper)).toEqual(['/image-studio'])
    expect(wrapper.find('a[href="/usage"]').exists()).toBe(false)
  })

  it('renders Cindy Accounts and Image Studio for an admin in simple mode', async () => {
    const wrapper = await renderSidebar({ admin: true, imageStudio: true })

    expect(extensionLinks(wrapper)).toEqual(['/admin/cindy-accounts', '/image-studio'])
    expect(wrapper.text()).not.toContain('nav.myAccount')
  })

  it('keeps Cindy Accounts visible while hiding Image Studio when its flag is disabled', async () => {
    const wrapper = await renderSidebar({ admin: true, imageStudio: false })

    expect(extensionLinks(wrapper)).toEqual(['/admin/cindy-accounts'])
  })
})
