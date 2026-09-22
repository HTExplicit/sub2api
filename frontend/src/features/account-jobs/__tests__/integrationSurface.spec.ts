import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

const listJobs = vi.hoisted(() => vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 }))
vi.mock('@/api/admin/accountJobs', () => ({ default: { list: listJobs } }))
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
    expect(listJobs).not.toHaveBeenCalled()

    await wrapper.get('[data-test="account-task-button"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[role="dialog"]').exists()).toBe(true)
    expect(listJobs).toHaveBeenCalledTimes(1)
    auth.user = { ...auth.user!, role: 'user' }
    await wrapper.vm.$nextTick()
    expect(wrapper.find('[data-test="account-task-button"]').exists()).toBe(false)
    expect(wrapper.findAllComponents(AccountTaskDrawer)).toHaveLength(0)
    expect(listJobs).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
})
