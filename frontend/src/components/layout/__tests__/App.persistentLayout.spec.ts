import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { defineComponent, h, onMounted, reactive } from 'vue'
import { describe, expect, it, vi } from 'vitest'

const stores = vi.hoisted(() => ({
  app: {
    sidebarCollapsed: false,
    siteName: 'Sub2API',
    siteLogo: '',
    cachedPublicSettings: { custom_menu_items: [] },
    fetchPublicSettings: vi.fn().mockResolvedValue(undefined),
  },
  auth: { isAuthenticated: false, isAdmin: true, user: { id: 1, role: 'admin' } },
  announcements: { fetchAnnouncements: vi.fn(), reset: vi.fn() },
  subscriptions: { clear: vi.fn() },
  compliance: { reset: vi.fn(), requireAcknowledgement: vi.fn() },
  adminSettings: { customMenuItems: [] },
  jobs: { clear: vi.fn(), startPolling: vi.fn() },
}))

vi.mock('@/stores', () => ({
  useAppStore: () => stores.app,
  useAuthStore: () => stores.auth,
  useAnnouncementStore: () => stores.announcements,
  useSubscriptionStore: () => stores.subscriptions,
  useAdminComplianceStore: () => stores.compliance,
  useAdminSettingsStore: () => stores.adminSettings,
  useAccountJobsStore: () => stores.jobs,
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => stores.auth }))
vi.mock('@/stores/onboarding', () => ({
  useOnboardingStore: () => ({ setReplayCallback: vi.fn() }),
}))
vi.mock('@/composables/useOnboardingTour', () => ({
  useOnboardingTour: () => ({ replayTour: vi.fn() }),
}))
vi.mock('@/api/setup', () => ({
  getSetupStatus: vi.fn().mockResolvedValue({ needs_setup: false }),
}))
vi.mock('@/utils/branding', () => ({ updateFavicon: vi.fn() }))
vi.mock('@/utils/featureFlags', () => ({
  FeatureFlags: { subscription: {} },
  isFeatureFlagEnabled: () => false,
  resolveFeatureFlag: () => false,
}))
vi.mock('@/components/common/Toast.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/common/AnnouncementPopup.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/common/NavigationProgress.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/admin/AdminComplianceDialog.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/layout/AppSidebar.vue', () => ({
  default: { template: '<aside><nav data-test="sidebar-nav"><input aria-label="Sidebar state" /></nav></aside>' },
}))
vi.mock('@/components/layout/AppHeader.vue', () => ({ default: { template: '<header>Header</header>' } }))
vi.mock('@/components/admin/account-jobs/AccountTaskDrawer.vue', () => ({
  default: { template: '<section data-test="task-drawer" />' },
}))

import App from '@/App.vue'
import AppLayout from '@/components/layout/AppLayout.vue'

describe('persistent console layout', () => {
  it('keeps the shell and its DOM state while replacing console pages', async () => {
    const firstMounted = vi.fn()
    const secondMounted = vi.fn()
    const page = (name: string, mounted: () => void) => defineComponent({
      setup() {
        onMounted(mounted)
        return () => h('article', name)
      },
    })
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/first', component: page('First', firstMounted), meta: { appLayout: true } },
        { path: '/second', component: page('Second', secondMounted), meta: { appLayout: true } },
      ],
    })
    await router.push('/first')
    const wrapper = mount(App, { global: { plugins: [router] } })
    await flushPromises()

    const layoutId = wrapper.findComponent(AppLayout).vm.$.uid
    const sidebar = wrapper.get('aside').element
    const header = wrapper.get('header').element
    const drawer = wrapper.get('[data-test="task-drawer"]').element
    const nav = wrapper.get('[data-test="sidebar-nav"]').element as HTMLElement
    nav.scrollTop = 96
    await wrapper.get('input').setValue('retained')

    await router.push('/second')
    await flushPromises()

    expect(wrapper.findComponent(AppLayout).vm.$.uid).toBe(layoutId)
    expect(wrapper.get('aside').element).toBe(sidebar)
    expect(wrapper.get('header').element).toBe(header)
    expect(wrapper.get('[data-test="task-drawer"]').element).toBe(drawer)
    expect(nav.scrollTop).toBe(96)
    expect((wrapper.get('input').element as HTMLInputElement).value).toBe('retained')
    expect(wrapper.get('article').text()).toBe('Second')
    expect(firstMounted).toHaveBeenCalledOnce()
    expect(secondMounted).toHaveBeenCalledOnce()
    expect(wrapper.findAllComponents(AppLayout)).toHaveLength(1)
    wrapper.unmount()
  })

  it('leaves query-driven layouts page-owned and does not remount their page on query changes', async () => {
    const mounted = vi.fn()
    const state = reactive({ count: 0 })
    const page = defineComponent({
      setup() {
        onMounted(mounted)
        return () => h('article', { onClick: () => state.count++ }, String(state.count))
      },
    })
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/conditional', component: page }],
    })
    await router.push('/conditional')
    const wrapper = mount(App, { global: { plugins: [router] } })
    await flushPromises()
    const content = wrapper.get('article').element
    await wrapper.get('article').trigger('click')

    for (const query of [{ fullscreen: '1' }, { method: 'alipay' }, { embedded: '1' }, {}]) {
      await router.replace({ query })
      await flushPromises()
      expect(wrapper.findComponent(AppLayout).exists()).toBe(false)
      expect(wrapper.get('article').element).toBe(content)
      expect(wrapper.get('article').text()).toBe('1')
    }
    expect(mounted).toHaveBeenCalledOnce()
    wrapper.unmount()
  })
})
