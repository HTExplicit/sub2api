import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import CindyBalanceProbeSummary from '../CindyBalanceProbeSummary.vue'
import type { Account } from '@/types'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key === 'admin.accounts.cindyProbe.itemState.healthy'
        ? 'Luna available this run'
        : key,
    }),
  }
})

const account = {
  id: 71,
  name: 'cindy-account',
  platform: 'openai',
  type: 'apikey',
  cindy_balance_probe_job_id: 912,
  cindy_balance_probe_outcome: 'healthy',
  cindy_balance_probe_checked_at: '2031-08-16T00:02:00Z',
} as Account

describe('CindyBalanceProbeSummary', () => {
  it('shows the job, translated outcome, and formatted check time', () => {
    const wrapper = mount(CindyBalanceProbeSummary, { props: { account, showLabel: true } })

    expect(wrapper.get('[data-test="cindy-probe-summary-job"]').text()).toBe('#912')
    expect(wrapper.get('[data-test="cindy-probe-summary-outcome"]').text()).toBe('Luna available this run')
    expect(wrapper.get('[data-test="cindy-probe-summary-time"]').text()).toContain('2031')
  })

  it('uses a compact double dash when no probe record exists', () => {
    const wrapper = mount(CindyBalanceProbeSummary, {
      props: {
        account: {
          ...account,
          cindy_balance_probe_job_id: null,
          cindy_balance_probe_outcome: null,
          cindy_balance_probe_checked_at: null,
        },
      },
    })

    expect(wrapper.get('[data-test="cindy-probe-summary-empty"]').text()).toBe('--')
    expect(wrapper.find('[data-test="cindy-probe-summary-job"]').exists()).toBe(false)
  })
})
