import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

const api = vi.hoisted(() => ({
  list: vi.fn(),
  get: vi.fn(),
  listItems: vi.fn(),
  cancel: vi.fn(),
  retryFailed: vi.fn(),
  reviewDuplicates: vi.fn(),
  mergeDuplicates: vi.fn(),
}))

vi.mock('@/api/admin/accountJobs', () => ({ default: api }))
vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

import router from '@/router'
import AccountTasksView from '../AccountTasksView.vue'
import { useAccountJobsStore } from '@/stores/accountJobs'

const runningJob = {
  id: 41,
  created_by: 1,
  kind: 'account_import',
  status: 'running' as const,
  metadata: {},
  target_count: 10,
  processed_count: 4,
  succeeded_count: 4,
  failed_count: 0,
  canceled_count: 0,
  attempt: 1,
  created_at: '2026-08-21T00:00:00Z',
  updated_at: '2026-08-21T00:01:00Z',
}

describe('AccountTasksView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    api.list.mockResolvedValue({ items: [runningJob], total: 1, page: 1, page_size: 20 })
    api.get.mockResolvedValue(runningJob)
    api.listItems.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 })
  })

  it('is exposed as an authenticated administrator route', () => {
    const route = router.getRoutes().find((candidate) => candidate.path === '/admin/tasks')

    expect(route?.meta).toMatchObject({ requiresAuth: true, requiresAdmin: true })
  })

  it('loads the requested page and opens a selected task in the global drawer', async () => {
    const wrapper = mount(AccountTasksView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          TablePageLayout: { template: '<div><slot name="filters"/><slot name="table"/><slot name="pagination"/></div>' },
          DataTable: {
            props: ['data'],
            template: '<div><div v-for="row in data" :key="row.id"><slot name="cell-actions" :row="row" /></div></div>',
          },
          Pagination: true,
          Icon: true,
        },
      },
    })
    await flushPromises()

    expect(api.list).toHaveBeenCalledWith({ page: 1, page_size: 20, kind: undefined, status: undefined })
    expect(wrapper.find('option[value="cindy_banned_cleanup"]').exists()).toBe(true)
    await wrapper.get('[data-test="task-open-41"]').trigger('click')
    await flushPromises()

    const store = useAccountJobsStore()
    expect(store.drawerOpen).toBe(true)
    expect(store.currentJob?.id).toBe(41)
  })
})
