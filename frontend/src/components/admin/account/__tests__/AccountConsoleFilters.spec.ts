import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import AccountConsoleFilters from '../AccountConsoleFilters.vue'
import type { AccountConsoleFacets, AccountConsoleFilterState } from '@/types'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

const state = (): AccountConsoleFilterState => ({
  search: '',
  platforms: ['openai'],
  types: [],
  statuses: [],
  plans: [],
  proxies: [],
  tags: [4],
  group: '',
  privacy_mode: '',
  account_ids: [101, 102]
})

const facets: AccountConsoleFacets = {
  total: 10,
  platforms: [
    { value: 'openai', label: 'OpenAI', count: 7 },
    { value: 'grok', label: 'Grok', count: 3 }
  ],
  types: [],
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
      global: { stubs: { SearchInput: true, Icon: true } }
    })

    await wrapper.get('[data-test="account-filter-platforms"]').trigger('click')
    const platformMenu = wrapper.get('[data-test="account-filter-platforms"]').element.parentElement!
    expect(platformMenu.textContent).toContain('OpenAI')
    expect(platformMenu.textContent).toContain('7')
    expect(platformMenu.textContent).toContain('Grok')
    expect(platformMenu.textContent).toContain('3')

    await wrapper.get('[data-test="account-filter-chip-platforms:openai"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toMatchObject({ platforms: [] })
    expect(wrapper.emitted('change')).toHaveLength(1)

    await wrapper.setProps({ modelValue: state() })
    await wrapper.get('[data-test="account-filter-chip-tags:4"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toMatchObject({ tags: [] })

    await wrapper.setProps({ modelValue: state() })
    await wrapper.get('[data-test="account-filter-chip-account_ids:import"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toMatchObject({ account_ids: [] })
  })
})
