import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

vi.mock('@/composables/useOnboardingTour', () => ({
  useOnboardingTour: () => ({ replayTour: vi.fn() }),
}))
vi.mock('vue-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-router')>()
  return {
    ...actual,
    useRouter: () => ({ push: vi.fn() }),
    useRoute: () => ({ name: 'AdminDashboard', params: {}, meta: { title: 'Dashboard' } }),
  }
})
vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

import AppLayout from '@/components/layout/AppLayout.vue'
import AccountTaskDrawer from '@/components/admin/account-jobs/AccountTaskDrawer.vue'
import { i18n } from '@/i18n'
import { useAuthStore } from '@/stores/auth'

describe('account job global surface', () => {
  it('mounts one global drawer and opens it from the administrator header button', async () => {
    const pinia = createPinia()
    setActivePinia(pinia)
    const auth = useAuthStore()
    auth.user = {
      id: 1,
      username: 'admin',
      email: 'admin@example.test',
      role: 'admin',
      balance: 0,
      frozen_balance: 0,
    } as never

    const wrapper = mount(AppLayout, {
      global: {
        plugins: [pinia, i18n],
        stubs: {
          AppSidebar: true,
          LocaleSwitcher: true,
          SubscriptionProgressMini: true,
          AnnouncementBell: true,
          RouterLink: { template: '<a><slot /></a>' },
          Teleport: true,
        },
      },
      slots: { default: '<main>content</main>' },
    })

    expect(wrapper.findAllComponents(AccountTaskDrawer)).toHaveLength(1)
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)

    await wrapper.get('[data-test="account-task-button"]').trigger('click')

    expect(wrapper.find('[role="dialog"]').exists()).toBe(true)
  })
})
