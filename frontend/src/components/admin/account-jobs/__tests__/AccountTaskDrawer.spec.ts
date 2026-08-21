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
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

import AccountTaskDrawer from '../AccountTaskDrawer.vue'
import { useAccountJobsStore } from '@/stores/accountJobs'
import type { AccountJob } from '@/api/admin/accountJobs'

const reviewJob: AccountJob = {
  id: 51,
  created_by: 1,
  kind: 'account_duplicate_review',
  status: 'succeeded',
  metadata: { internal_marker: 'MustNotRender' },
  target_count: 2,
  processed_count: 2,
  succeeded_count: 2,
  failed_count: 0,
  canceled_count: 0,
  attempt: 1,
  created_at: '2026-08-21T00:00:00Z',
  updated_at: '2026-08-21T00:01:00Z',
}

describe('AccountTaskDrawer', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    api.mergeDuplicates.mockResolvedValue({
      ...reviewJob,
      id: 52,
      kind: 'account_duplicate_merge',
      status: 'pending',
      processed_count: 0,
      succeeded_count: 0,
    })
  })

  it('uses only safe duplicate review metadata to submit a confirmed merge', async () => {
    const store = useAccountJobsStore()
    store.track(reviewJob)
    store.items = [{
      id: 101,
      job_id: 51,
      ordinal: 1,
      action: 'review',
      status: 'succeeded',
      metadata: {
        confirmation_hash: 'opaque-confirmation-token',
        accounts: [
          { account_id: 7, name: 'Keep me', group_count: 2, tag_count: 1, configuration_score: 9 },
          { account_id: 8, name: 'Merge me', group_count: 1, tag_count: 0, configuration_score: 4 },
        ],
        api_key: 'MustNotRender',
        confirmation_fingerprint: 'MustNotRender',
      },
      created_at: '2026-08-21T00:00:00Z',
      updated_at: '2026-08-21T00:01:00Z',
    }]

    const wrapper = mount(AccountTaskDrawer, {
      global: {
        stubs: {
          Teleport: true,
          RouterLink: { template: '<a><slot /></a>' },
          Icon: true,
        },
      },
    })

    expect(wrapper.text()).toContain('Keep me')
    expect(wrapper.text()).toContain('Merge me')
    expect(wrapper.text()).not.toContain('MustNotRender')
    expect(wrapper.html()).not.toContain('confirmation_fingerprint')

    await wrapper.get('[data-test="duplicate-survivor-7"]').setValue(true)
    await wrapper.get('[data-test="duplicate-merge-submit"]').trigger('click')
    await flushPromises()

    expect(api.mergeDuplicates).toHaveBeenCalledWith({
      survivor_account_id: 7,
      loser_account_ids: [8],
      confirmation_hash: 'opaque-confirmation-token',
    })
    expect(store.currentJob?.id).toBe(52)
  })

  it('shows progress and permits cancel or retry only in matching states', async () => {
    const store = useAccountJobsStore()
    store.track({ ...reviewJob, status: 'running', processed_count: 1 })
    api.cancel.mockResolvedValue({ ...reviewJob, status: 'running', cancel_requested_at: '2026-08-21T00:02:00Z' })
    const wrapper = mount(AccountTaskDrawer, {
      global: { stubs: { Teleport: true, RouterLink: true, Icon: true } },
    })

    expect(wrapper.get('[role="progressbar"]').attributes('aria-valuenow')).toBe('50')
    expect(wrapper.find('[data-test="task-cancel"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="task-retry"]').exists()).toBe(false)

    store.currentJob = {
      ...reviewJob,
      status: 'failed',
      failed_count: 2,
      error_message: 'redacted task failure',
    }
    await wrapper.vm.$nextTick()
    expect(wrapper.find('[data-test="task-cancel"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="task-retry"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('redacted task failure')
  })
})
