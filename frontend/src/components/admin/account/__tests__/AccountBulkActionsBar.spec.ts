import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import AccountBulkActionsBar from '../AccountBulkActionsBar.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key
  })
}))

describe('AccountBulkActionsBar', () => {
  it('allows selecting all results before any row is selected', async () => {
    const wrapper = mount(AccountBulkActionsBar, {
      props: {
        selectedIds: [],
        totalResults: 45,
        selectingAll: false,
        allResultsSelected: false
      }
    })

    const button = wrapper.findAll('button').find(item =>
      item.text().includes('admin.accounts.bulkActions.selectAllResults')
    )

    expect(button).toBeDefined()
    await button!.trigger('click')
    expect(wrapper.emitted('select-all-results')).toHaveLength(1)
  })

  it('preserves the upstream billing probe action from v0.1.166', async () => {
    const wrapper = mount(AccountBulkActionsBar, {
      props: {
        selectedIds: [1],
        totalResults: 45,
        selectingAll: false,
        allResultsSelected: false
      }
    })

    const button = wrapper.findAll('button').find(item =>
      item.text().includes('admin.accounts.bulkActions.probeUpstreamBilling')
    )

    expect(button).toBeDefined()
    await button!.trigger('click')
    expect(wrapper.emitted('probe-upstream-billing')).toHaveLength(1)
  })

  it('offers duplicate review only for 2 through 100 selected accounts', async () => {
    const wrapper = mount(AccountBulkActionsBar, {
      props: {
        selectedIds: [1, 2],
        totalResults: 120,
        selectingAll: false,
        allResultsSelected: false
      }
    })

    const duplicateButton = wrapper.find('[data-test="duplicate-review"]')
    expect(duplicateButton.exists()).toBe(true)
    await duplicateButton.trigger('click')
    expect(wrapper.emitted('duplicate-review')).toHaveLength(1)

    await wrapper.setProps({ selectedIds: [1] })
    expect(wrapper.find('[data-test="duplicate-review"]').exists()).toBe(false)

    await wrapper.setProps({ selectedIds: Array.from({ length: 101 }, (_, index) => index + 1) })
    expect(wrapper.find('[data-test="duplicate-review"]').exists()).toBe(false)
  })

  it('emits the account tier refresh action for selected accounts', async () => {
    const wrapper = mount(AccountBulkActionsBar, {
      props: {
        selectedIds: [1],
        totalResults: 1,
        selectingAll: false,
        allResultsSelected: false
      }
    })

    await wrapper.get('[data-test="refresh-tier"]').trigger('click')

    expect(wrapper.emitted('refresh-tier')).toHaveLength(1)
  })
})
