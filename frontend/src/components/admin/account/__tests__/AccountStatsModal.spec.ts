import { enableAutoUnmount, flushPromises, shallowMount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import AccountStatsModal from '../AccountStatsModal.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import ModelDistributionChart from '@/components/charts/ModelDistributionChart.vue'
import type { Account, AccountUsageStatsResponse } from '@/types'

const mocks = vi.hoisted(() => ({ getStats: vi.fn() }))

vi.mock('@/api/admin', () => ({ adminAPI: { accounts: mocks } }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))

enableAutoUnmount(afterEach)
beforeEach(() => vi.clearAllMocks())
afterEach(() => vi.restoreAllMocks())

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

const account = (id: number) => ({ id, name: `account-${id}`, status: 'active' } as Account)

const statsFor = (model: string) => ({
  history: [],
  summary: {
    days: 30,
    actual_days_used: 1,
    total_cost: 0,
    total_user_cost: 0,
    total_standard_cost: 0,
    total_requests: 0,
    total_tokens: 0,
    avg_daily_cost: 0,
    avg_daily_user_cost: 0,
    avg_daily_requests: 0,
    avg_daily_tokens: 0,
    avg_duration_ms: 0,
    today: null,
    highest_cost_day: null,
    highest_request_day: null
  },
  models: [{ model }],
  endpoints: [],
  upstream_endpoints: []
} as unknown as AccountUsageStatsResponse)

async function open() {
  const wrapper = shallowMount(AccountStatsModal, {
    props: { show: false, account: account(1) },
    global: {
      stubs: { BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' } }
    }
  })
  await wrapper.setProps({ show: true })
  return wrapper
}

describe('account statistics requests', () => {
  it('does not let a late response replace the next account statistics', async () => {
    const old = deferred<AccountUsageStatsResponse>()
    mocks.getStats.mockReturnValueOnce(old.promise).mockResolvedValueOnce(statsFor('current-model'))
    const wrapper = await open()

    await wrapper.setProps({ account: account(2) })
    await flushPromises()
    old.resolve(statsFor('old-model'))
    await flushPromises()

    expect(mocks.getStats.mock.calls).toEqual([[1, 30], [2, 30]])
    expect(wrapper.getComponent(ModelDistributionChart).props('modelStats')).toEqual([{ model: 'current-model' }])
  })

  it('ignores an old failure without dismissing the current loading state', async () => {
    const old = deferred<AccountUsageStatsResponse>()
    const current = deferred<AccountUsageStatsResponse>()
    mocks.getStats.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {})
    const wrapper = await open()

    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    old.reject(new Error('obsolete'))
    await flushPromises()

    expect(consoleError).not.toHaveBeenCalled()
    expect(wrapper.findComponent(LoadingSpinner).exists()).toBe(true)

    current.resolve(statsFor('current-model'))
    await flushPromises()

    expect(wrapper.getComponent(ModelDistributionChart).props('modelStats')).toEqual([{ model: 'current-model' }])
  })
})
