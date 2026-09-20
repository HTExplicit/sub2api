import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { usePluginExtensions } from '@/stores/pluginExtensions'
import type { PluginContribution } from '@/api/admin/plugins'
import manifest from '../../../../../plugins/cindy-provider/manifest.source.json'
import CindyBalanceProbeSummary from '../CindyBalanceProbeSummary.vue'
import type { Account } from '@/types'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      locale: { value: 'en' },
      t: (key: string) => key,
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
  beforeEach(() => {
    setActivePinia(createPinia())
    const registry = usePluginExtensions()
    registry.loaded = true
    registry.items = manifest.contributions.map(item => ({ ...item, plugin_id: 7, available: true })) as PluginContribution[]
  })
  it('shows the job, translated outcome, and formatted check time', () => {
    const wrapper = mount(CindyBalanceProbeSummary, { props: { account, showLabel: true } })

    expect(wrapper.get('[data-display-key="job"]').text()).toBe('#912')
    expect(wrapper.get('[data-display-key="outcome"]').text()).toBe('Luna available this run')
    expect(wrapper.get('[data-display-key="checked_at"]').text()).toContain('2031')
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

    expect(wrapper.get('[data-display-empty]').text()).toBe('--')
    expect(wrapper.find('[data-display-key="job"]').exists()).toBe(false)
  })

  it('withdraws the column data when disabled and retains saved values on failure', async () => {
    const registry = usePluginExtensions()
    const wrapper = mount(CindyBalanceProbeSummary, { props: { account } })
    registry.items = registry.items.map(item => ({ ...item, available: false }))
    await wrapper.vm.$nextTick()
    expect(wrapper.text()).toContain('#912')
    registry.items = []
    await wrapper.vm.$nextTick()
    expect(wrapper.text()).toBe('')
  })
})
