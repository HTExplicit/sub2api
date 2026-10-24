import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import AccountConsoleFilters from '../AccountConsoleFilters.vue'
import type { AccountConsoleFacets, AccountConsoleFilterState } from '@/types'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

const state = (): AccountConsoleFilterState => ({
  search: '',
  platform: 'openai',
  types: ['apikey'],
  status: '',
  plans: [],
  proxies: [],
  tags: [4],
  group_id: '',
  privacy_mode: '',
  account_ids: [101, 102]
})

const facets: AccountConsoleFacets = {
  total: 10,
  uncategorized_count: 1,
  platforms: [
    { value: 'openai', label: 'OpenAI', count: 7 },
    { value: 'grok', label: 'Grok', count: 3 }
  ],
  types: [
    { value: 'apikey', label: 'API Key', count: 7 },
    { value: 'oauth', label: 'OAuth', count: 3 }
  ],
  statuses: [],
  plans: [],
  proxies: [],
  folders: [],
  tags: [{
    id: 4,
    name: 'urgent',
    sort_order: 0,
    account_count: 3,
    created_at: '2026-07-29T00:00:00Z',
    updated_at: '2026-07-29T00:00:00Z'
  }]
}

describe('AccountConsoleFilters', () => {
  it('shows facet counts and removes individual active filters', async () => {
    const wrapper = mount(AccountConsoleFilters, {
      props: { modelValue: state(), facets, groups: [] },
      global: { stubs: { Select: true, SearchInput: true, Icon: true } }
    })

    await wrapper.get('[data-test="account-filter-types"]').trigger('click')
    const typeMenu = wrapper.get('[data-test="account-filter-types"]').element.parentElement!
    expect(typeMenu.textContent).toContain('admin.accounts.apiKey')
    expect(typeMenu.textContent).toContain('7')
    expect(typeMenu.textContent).toContain('admin.accounts.oauthType')
    expect(typeMenu.textContent).toContain('3')

    await wrapper.get('[data-test="account-filter-chip-types:apikey"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toMatchObject({ types: [] })
    expect(wrapper.emitted('change')).toHaveLength(1)

    await wrapper.setProps({ modelValue: state() })
    await wrapper.get('[data-test="account-filter-chip-tags:4"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toMatchObject({ tags: [] })

    await wrapper.setProps({ modelValue: state() })
    await wrapper.get('[data-test="account-filter-chip-account_ids:import"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toMatchObject({ account_ids: [] })
    wrapper.unmount()
  })

  it('uses single platform and status selections while other filter menus stay multiple', async () => {
    const wrapper = mount(AccountConsoleFilters, {
      props: { modelValue: state(), facets, groups: [] },
      global: { stubs: { Select: true, SearchInput: true, Icon: true } }
    })

    wrapper.findComponent('[data-test="account-filter-platform"]').vm.$emit('update:modelValue', 'grok')
    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toMatchObject({ platform: 'grok', types: ['apikey'] })
    wrapper.findComponent('[data-test="account-filter-status"]').vm.$emit('update:modelValue', 'error')
    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toMatchObject({ status: 'error' })
    wrapper.findComponent('[data-test="account-filter-platform"]').vm.$emit('update:modelValue', '')
    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toMatchObject({ platform: '' })

    await wrapper.get('[data-test="account-filter-types"]').trigger('click')
    await wrapper.findAll('input[type="checkbox"]')[1].setValue(true)
    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toMatchObject({ types: ['apikey', 'oauth'] })
    expect(wrapper.find('[data-test="account-filter-chip-platforms:openai"]').exists()).toBe(false)

    await wrapper.get('[data-ui="account-filters-clear"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toMatchObject({ platform: '', status: '', types: [], tags: [] })
    wrapper.unmount()
  })
})


it('shows all filters inline and preserves selections across split theme parts', async () => {
  const filters = { ...state(), types: ['apikey'], group_id: '12', privacy_mode: 'training_off' }
  const wrapper = mount(AccountConsoleFilters, {
    props: { modelValue: filters, facets, groups: [] },
    global: { stubs: { Select: true, SearchInput: true, Icon: true } }
  })
  expect(wrapper.find('[data-test="account-more-filters"]').exists()).toBe(false)
  for (const key of ['platform', 'types', 'status', 'plans', 'proxies', 'tags']) {
    expect(wrapper.get(`[data-test="account-filter-${key}"]`).isVisible()).toBe(true)
  }
  expect(wrapper.get('[data-ui="account-inline-filters"]').classes()).toContain('grid-cols-2')
  await wrapper.setProps({ part: 'search' })
  expect(wrapper.find('[data-ui="account-inline-filters"]').exists()).toBe(false)
  await wrapper.setProps({ part: 'filters' })
  expect(wrapper.get('[data-test="account-filter-types"]').isVisible()).toBe(true)
  expect(wrapper.props('modelValue')).toEqual(filters)
  expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  await wrapper.get('[data-test="account-filter-chip-privacy_mode"]').trigger('click')
  expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toMatchObject({ privacy_mode: '', types: ['apikey'], group_id: '12' })
  wrapper.unmount()
})
