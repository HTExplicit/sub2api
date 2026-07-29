import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AccountUsageCell from '../AccountUsageCell.vue'
import type { Account, AccountUsageInfo } from '@/types'

const { getUsage } = vi.hoisted(() => ({
  getUsage: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: { getUsage }
  }
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

const makeAccount = (overrides: Partial<Account> = {}): Account => ({
  id: 5100,
  name: 'usage-account',
  platform: 'openai',
  type: 'oauth',
  proxy_id: null,
  concurrency: 10,
  priority: 0,
  status: 'active',
  error_message: null,
  last_used_at: null,
  expires_at: null,
  auto_pause_on_expired: true,
  created_at: '2026-07-29T00:00:00Z',
  updated_at: '2026-07-29T00:00:00Z',
  schedulable: true,
  rate_limited_at: null,
  rate_limit_reset_at: null,
  overload_until: null,
  temp_unschedulable_until: null,
  temp_unschedulable_reason: null,
  session_window_start: null,
  session_window_end: null,
  session_window_status: null,
  ...overrides
})

const makeOAuthUsage = (
  fiveHour = 27,
  sevenDay = 63,
  updatedAt = '2026-07-29T12:00:00Z'
): AccountUsageInfo => ({
  source: 'active',
  updated_at: updatedAt,
  five_hour: {
    utilization: fiveHour,
    resets_at: '2026-07-29T17:00:00Z',
    remaining_seconds: 18_000
  },
  seven_day: {
    utilization: sevenDay,
    resets_at: '2026-08-05T12:00:00Z',
    remaining_seconds: 604_800
  },
  seven_day_sonnet: null,
  seven_day_fable: null
})

const UsageBarStub = {
  props: ['label', 'utilization', 'density'],
  template: '<span class="usage-bar">{{ label }}|{{ utilization }}|{{ density }}</span>'
}

const stubs = {
  UsageProgressBar: UsageBarStub,
  AccountQuotaInfo: true,
  OllamaCloudUsageCell: true,
  GrokQuotaProbeCell: { template: '<button data-test="grok-probe">probe</button>' },
  OpenAIQuotaResetCell: { template: '<button data-test="quota-action">quota</button>' }
}

describe('AccountUsageCell list variants', () => {
  beforeEach(() => {
    getUsage.mockReset()
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      writable: true,
      value: vi.fn().mockReturnValue({
        matches: true,
        media: '(min-width: 768px)',
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn()
      })
    })
  })

  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  it('renders OAuth 5h/7d in full list mode without query or reset controls', async () => {
    getUsage.mockResolvedValue(makeOAuthUsage())
    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 5101 }),
        variant: 'list',
        readOnly: true,
        statusNow: Date.parse('2026-07-29T12:05:00Z')
      },
      global: { stubs }
    })
    await flushPromises()

    expect(wrapper.text()).toContain('5h|27|list')
    expect(wrapper.text()).toContain('7d|63|list')
    expect(wrapper.find('[data-test="quota-action"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="grok-probe"]').exists()).toBe(false)
  })

  it('keeps quota actions available in the default details mode', async () => {
    getUsage.mockResolvedValue(makeOAuthUsage())
    const wrapper = mount(AccountUsageCell, {
      props: { account: makeAccount({ id: 5113 }) },
      global: { stubs }
    })
    await flushPromises()
    expect(wrapper.find('[data-test="quota-action"]').exists()).toBe(true)
  })

  it('renders the OAuth percentages as a compact colored summary', async () => {
    getUsage.mockResolvedValue(makeOAuthUsage())
    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 5102 }),
        variant: 'compact',
        readOnly: true
      },
      global: { stubs }
    })
    await flushPromises()

    expect(wrapper.get('[data-test="usage-compact-summary"]').text()).toContain('5h|27|compact')
    expect(wrapper.get('[data-test="usage-compact-summary"]').text()).toContain('7d|63|compact')
  })

  it('renders API Key today requests, tokens, both costs, and configured quotas', () => {
    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({
          id: 5103,
          type: 'apikey',
          quota_daily_limit: 100,
          quota_daily_used: 25,
          quota_weekly_limit: 200,
          quota_weekly_used: 160,
          quota_limit: 1_000,
          quota_used: 1_000
        }),
        variant: 'list',
        readOnly: true,
        todayStats: {
          requests: 12,
          tokens: 34_000,
          cost: 1.25,
          standard_cost: 1.25,
          user_cost: 2.5
        }
      },
      global: { stubs }
    })

    expect(wrapper.text()).toContain('12 req')
    expect(wrapper.text()).toContain('34.0K')
    expect(wrapper.text()).toContain('A $1.25')
    expect(wrapper.text()).toContain('U $2.50')
    expect(wrapper.text()).toContain('1d|25|list')
    expect(wrapper.text()).toContain('7d|80|list')
    expect(wrapper.text()).toContain('total|100|list')
    expect(getUsage).not.toHaveBeenCalled()
  })

  it('distinguishes no data from a failed first today-stats request without fake zero costs', () => {
    const account = makeAccount({ id: 5104, type: 'apikey' })
    const empty = mount(AccountUsageCell, {
      props: { account, variant: 'compact', readOnly: true },
      global: { stubs }
    })
    expect(empty.get('[data-test="usage-no-data"]').text()).toBe('admin.accounts.usageNoData')

    const failed = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 5105, type: 'apikey' }),
        variant: 'compact',
        readOnly: true,
        todayStatsError: true
      },
      global: { stubs }
    })
    expect(failed.get('[data-test="usage-fetch-failed"]').text()).toBe('admin.accounts.usageFetchFailed')
    expect(failed.text()).not.toContain('$0.00')
  })

  it('keeps old API Key today stats visible and marks them stale after a batch failure', () => {
    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 5111, type: 'apikey' }),
        variant: 'compact',
        readOnly: true,
        todayStats: {
          requests: 6,
          tokens: 7_500,
          cost: 0.5,
          standard_cost: 0.5,
          user_cost: 0.8
        },
        todayStatsError: true,
        todayStatsUpdatedAt: Date.parse('2026-07-29T11:00:00Z'),
        statusNow: Date.parse('2026-07-29T12:00:00Z')
      },
      global: { stubs }
    })

    expect(wrapper.text()).toContain('6 req')
    expect(wrapper.text()).toContain('A $0.50')
    expect(wrapper.get('[data-test="usage-stale"]').exists()).toBe(true)
  })

  it('treats a degraded usage response with no snapshot as a failed first query', async () => {
    getUsage.mockResolvedValue({
      updated_at: null,
      five_hour: null,
      seven_day: null,
      seven_day_sonnet: null,
      seven_day_fable: null,
      error: 'upstream unavailable',
      error_code: 'network_error'
    })
    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 5112 }),
        variant: 'compact',
        readOnly: true
      },
      global: { stubs }
    })
    await flushPromises()

    expect(wrapper.get('[data-test="usage-fetch-failed"]').text()).toBe('admin.accounts.usageFetchFailed')
  })

  it('retains the last successful OAuth value after a forced refresh fails and marks it stale', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    getUsage
      .mockResolvedValueOnce(makeOAuthUsage())
      .mockRejectedValueOnce(new Error('refresh failed'))
    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 5106 }),
        variant: 'list',
        readOnly: true,
        manualRefreshToken: 0
      },
      global: { stubs }
    })
    await flushPromises()

    await wrapper.setProps({ manualRefreshToken: 1 })
    await flushPromises()

    expect(getUsage).toHaveBeenLastCalledWith(5106, undefined, true)
    expect(wrapper.text()).toContain('5h|27|list')
    expect(wrapper.get('[data-test="usage-stale"]').text()).toBe('admin.accounts.usageStale')
    expect(consoleError).toHaveBeenCalled()
  })

  it('retains the last successful OAuth value when a refresh returns a degraded response', async () => {
    getUsage
      .mockResolvedValueOnce(makeOAuthUsage())
      .mockResolvedValueOnce({
        updated_at: null,
        five_hour: null,
        seven_day: null,
        seven_day_sonnet: null,
        seven_day_fable: null,
        error: 'upstream unavailable',
        error_code: 'network_error'
      })
    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 5114 }),
        variant: 'list',
        readOnly: true,
        manualRefreshToken: 0
      },
      global: { stubs }
    })
    await flushPromises()

    await wrapper.setProps({ manualRefreshToken: 1 })
    await flushPromises()

    expect(wrapper.text()).toContain('5h|27|list')
    expect(wrapper.text()).toContain('7d|63|list')
    expect(wrapper.get('[data-test="usage-stale"]').exists()).toBe(true)
  })

  it('keeps a failed refresh marker when the cached usage is reused by another view', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    getUsage
      .mockResolvedValueOnce(makeOAuthUsage())
      .mockRejectedValueOnce(new Error('refresh failed'))
    const first = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 5115 }),
        variant: 'list',
        readOnly: true,
        manualRefreshToken: 0
      },
      global: { stubs }
    })
    await flushPromises()
    await first.setProps({ manualRefreshToken: 1 })
    await flushPromises()
    first.unmount()

    const second = mount(AccountUsageCell, {
      props: { account: makeAccount({ id: 5115 }), variant: 'compact', readOnly: true },
      global: { stubs }
    })
    await flushPromises()

    expect(getUsage).toHaveBeenCalledTimes(2)
    expect(second.text()).toContain('5h|27|compact')
    expect(second.get('[data-test="usage-stale"]').exists()).toBe(true)
    expect(consoleError).toHaveBeenCalled()
  })

  it('reuses the five-minute module cache when switching list variants', async () => {
    getUsage.mockResolvedValue(makeOAuthUsage())
    const first = mount(AccountUsageCell, {
      props: { account: makeAccount({ id: 5107 }), variant: 'list', readOnly: true },
      global: { stubs }
    })
    await flushPromises()
    first.unmount()

    const second = mount(AccountUsageCell, {
      props: { account: makeAccount({ id: 5107 }), variant: 'compact', readOnly: true },
      global: { stubs }
    })
    await flushPromises()

    expect(getUsage).toHaveBeenCalledTimes(1)
    expect(second.text()).toContain('5h|27|compact')
  })

  it('deduplicates simultaneous list and drawer requests for one account', async () => {
    let resolveUsage!: (value: AccountUsageInfo) => void
    const pending = new Promise<AccountUsageInfo>((resolve) => { resolveUsage = resolve })
    getUsage.mockReturnValue(pending)

    const list = mount(AccountUsageCell, {
      props: { account: makeAccount({ id: 5108 }), variant: 'list', readOnly: true },
      global: { stubs }
    })
    const drawer = mount(AccountUsageCell, {
      props: { account: makeAccount({ id: 5108 }) },
      global: { stubs }
    })

    expect(getUsage).toHaveBeenCalledTimes(1)
    resolveUsage(makeOAuthUsage())
    await flushPromises()
    expect(list.text()).toContain('5h|27|list')
    expect(drawer.text()).toContain('5h|27|detail')
  })

  it('defers mobile usage loading until the row enters the viewport', async () => {
    let observerCallback: IntersectionObserverCallback | null = null
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      value: vi.fn().mockReturnValue({
        matches: false,
        media: '(min-width: 768px)',
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn()
      })
    })
    vi.stubGlobal('IntersectionObserver', class {
      constructor(callback: IntersectionObserverCallback) { observerCallback = callback }
      observe = vi.fn()
      disconnect = vi.fn()
      unobserve = vi.fn()
      takeRecords = vi.fn(() => [])
      root = null
      rootMargin = ''
      thresholds = []
    })
    getUsage.mockResolvedValue(makeOAuthUsage())

    const wrapper = mount(AccountUsageCell, {
      props: { account: makeAccount({ id: 5109 }), variant: 'compact', readOnly: true },
      global: { stubs }
    })
    await wrapper.vm.$nextTick()
    expect(getUsage).not.toHaveBeenCalled()
    expect(observerCallback).not.toBeNull()

    observerCallback!([{ isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver)
    await flushPromises()
    expect(getUsage).toHaveBeenCalledWith(5109)
  })

  it('marks observations older than fifteen minutes as stale', async () => {
    getUsage.mockResolvedValue(makeOAuthUsage(27, 63, '2026-07-29T11:30:00Z'))
    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 5110 }),
        variant: 'list',
        readOnly: true,
        statusNow: Date.parse('2026-07-29T12:00:01Z')
      },
      global: { stubs }
    })
    await flushPromises()
    expect(wrapper.find('[data-test="usage-stale"]').exists()).toBe(true)
  })
})
