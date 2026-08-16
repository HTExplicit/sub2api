import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import AccountCardGrid from '../AccountCardGrid.vue'
import AccountCompactList from '../AccountCompactList.vue'
import type { Account, WindowStats } from '@/types'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key === 'admin.accounts.cindyProbe.itemState.healthy'
        ? 'Luna available this run'
        : key
    })
  }
})

const account: Account = {
  id: 71,
  name: 'visible-usage',
  platform: 'openai',
  type: 'apikey',
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
  groups: [],
  tags: [],
  cindy_balance_probe_job_id: 912,
  cindy_balance_probe_outcome: 'healthy',
  cindy_balance_probe_checked_at: '2031-08-16T00:02:00Z'
}

const stats: Record<string, WindowStats> = {
  '71': {
    requests: 9,
    tokens: 12_000,
    cost: 0.75,
    standard_cost: 0.75,
    user_cost: 1.25
  }
}

const AccountUsageCellStub = {
  props: {
    account: Object,
    todayStats: Object,
    todayStatsLoading: Boolean,
    todayStatsError: Boolean,
    todayStatsUpdatedAt: Number,
    manualRefreshToken: Number,
    statusNow: Number,
    variant: String,
    readOnly: Boolean
  },
  template: `
    <div
      data-test="usage-cell"
      :data-variant="variant"
      :data-read-only="String(readOnly)"
      :data-requests="String(todayStats?.requests ?? -1)"
      :data-loading="String(todayStatsLoading)"
      :data-error="String(todayStatsError)"
      :data-refresh-token="String(manualRefreshToken)"
    />
  `
}

const AccountCapacityCellStub = {
  props: { compact: Boolean },
  template: '<div data-test="capacity-cell" :data-compact="String(compact)" />'
}

const globalStubs = {
  AccountUsageCell: AccountUsageCellStub,
  AccountCapacityCell: AccountCapacityCellStub,
  AccountStatusIndicator: { template: '<div data-test="status" />' },
  PlatformTypeBadge: { template: '<div data-test="platform" />' },
  Icon: true
}

const sharedProps = {
  accounts: [account],
  loading: false,
  selectedIds: [],
  todayStats: stats,
  todayStatsLoading: false,
  todayStatsError: true,
  todayStatsUpdatedAt: 123,
  manualRefreshToken: 4,
  statusNow: 456
}

describe('account console usage views', () => {
  it('passes compact usage inputs and keeps usage below account information on mobile', () => {
    const wrapper = mount(AccountCompactList, {
      props: sharedProps,
      global: { stubs: globalStubs }
    })

    const usage = wrapper.get('[data-test="account-compact-usage"]')
    const cell = usage.get('[data-test="usage-cell"]')
    expect(cell.attributes('data-variant')).toBe('compact')
    expect(cell.attributes('data-read-only')).toBe('true')
    expect(cell.attributes('data-requests')).toBe('9')
    expect(cell.attributes('data-error')).toBe('true')
    expect(cell.attributes('data-refresh-token')).toBe('4')
    expect(usage.classes()).toContain('row-start-2')
    expect(usage.get('[data-test="capacity-cell"]').attributes('data-compact')).toBe('true')
    expect(wrapper.find('[data-test="cindy-probe-summary"]').exists()).toBe(false)
  })

  it('places full list usage before taxonomy in cards and forwards refresh state', () => {
    const wrapper = mount(AccountCardGrid, {
      props: sharedProps,
      global: { stubs: globalStubs }
    })

    const usage = wrapper.get('[data-test="account-card-usage"]')
    const taxonomy = wrapper.get('[data-test="account-card-taxonomy"]')
    const cell = usage.get('[data-test="usage-cell"]')
    expect(cell.attributes('data-variant')).toBe('list')
    expect(cell.attributes('data-read-only')).toBe('true')
    expect(cell.attributes('data-loading')).toBe('false')
    expect(cell.attributes('data-refresh-token')).toBe('4')
    expect(
      usage.element.compareDocumentPosition(taxonomy.element) & Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy()
    expect(wrapper.find('[data-test="cindy-probe-summary"]').exists()).toBe(false)
  })

  it('shows the recent Cindy probe in compact and card layouts only when requested', () => {
    const compact = mount(AccountCompactList, {
      props: { ...sharedProps, showCindyProbe: true },
      global: { stubs: globalStubs }
    })
    const cards = mount(AccountCardGrid, {
      props: { ...sharedProps, showCindyProbe: true },
      global: { stubs: globalStubs }
    })

    expect(compact.findAll('[data-test="cindy-probe-summary"]')).toHaveLength(2)
    expect(compact.text()).toContain('#912')
    expect(compact.text()).toContain('Luna available this run')
    expect(cards.get('[data-test="cindy-probe-summary"]').text()).toContain('#912')
    expect(cards.get('[data-test="cindy-probe-summary"]').text()).toContain('Luna available this run')
  })
})
