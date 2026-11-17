import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import EndpointDistributionChart from '../EndpointDistributionChart.vue'
import { getUserBreakdown } from '@/api/admin/dashboard'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

vi.mock('vue-chartjs', () => ({
  Doughnut: {
    props: ['data', 'options', 'updateMode'],
    template: '<div class="chart-data" />',
  },
}))

vi.mock('@/api/admin/dashboard', () => ({
  getUserBreakdown: vi.fn(),
}))

const endpointStats = [{
  endpoint: '/v1/responses',
  requests: 2,
  total_tokens: 1000,
  cost: 1.5,
  actual_cost: 1.2,
  account_cost: 0.75,
}]

const user = {
  user_id: 1,
  email: 'user@example.com',
  requests: 2,
  input_tokens: 800,
  output_tokens: 200,
  cache_tokens: 0,
  total_tokens: 1000,
  cost: 1.5,
  actual_cost: 1.2,
  account_cost: 0.75,
}

describe('EndpointDistributionChart account cost visibility', () => {
  it.each([
    { label: 'administrator default', showAccountCost: undefined, columnCount: 6 },
    { label: 'user usage', showAccountCost: false, columnCount: 5 },
  ])('keeps the $label columns and user breakdown aligned', async ({ showAccountCost, columnCount }) => {
    vi.mocked(getUserBreakdown).mockResolvedValue({ users: [user], start_date: '2026-11-17', end_date: '2026-11-17' })
    const wrapper = mount(EndpointDistributionChart, {
      props: { endpointStats, ...(showAccountCost === undefined ? {} : { showAccountCost }) },
    })

    expect(wrapper.findAll('thead th')).toHaveLength(columnCount)
    expect(wrapper.get('[data-ui="dist-row"]').findAll('td')).toHaveLength(columnCount)
    expect(wrapper.text().includes('admin.dashboard.accountCost')).toBe(columnCount === 6)
    expect(wrapper.text().includes('$0.750')).toBe(columnCount === 6)

    await wrapper.get('[data-ui="dist-row"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-ui="dist-group"] > td').attributes('colspan')).toBe(String(columnCount))
    expect(wrapper.get('[data-ui="dist-group"] table tr').findAll('td')).toHaveLength(columnCount)
    expect(wrapper.get('[data-ui="dist-group"]').text().includes('$0.750')).toBe(columnCount === 6)
    expect(getUserBreakdown).toHaveBeenLastCalledWith(expect.objectContaining({
      endpoint: '/v1/responses',
      endpoint_type: 'inbound',
    }))
    wrapper.unmount()
  })
})
