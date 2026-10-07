import { flushPromises, shallowMount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, onUnmounted, type PropType } from 'vue'
import AccountDetailsDrawer from '../AccountDetailsDrawer.vue'

import type { Account } from '@/types'

const { getUsage } = vi.hoisted(() => ({ getUsage: vi.fn() }))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: { setTaxonomy: vi.fn(), getUsage }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn()
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

const account = {
  id: 71,
  name: 'openai-account',
  platform: 'openai',
  type: 'oauth',
  status: 'active',
  schedulable: true,
  concurrency: 10,
  priority: 0,
  rate_multiplier: 1,
  credentials: {},
  extra: {},
  groups: [],
  tags: [],
  created_at: '2026-08-05T00:00:00Z',
  updated_at: '2026-08-05T00:00:00Z'
} as Account

describe('AccountDetailsDrawer', () => {
  beforeEach(() => getUsage.mockReset())

  it('forwards quota account updates to the accounts view', async () => {
    const updatedAccount = { ...account, updated_at: '2026-08-05T00:01:00Z' }
    const wrapper = shallowMount(AccountDetailsDrawer, {
      props: {
        account,
        folders: [],
        tags: [],
        todayStats: null,
        todayStatsLoading: false,
        manualRefreshToken: 0
      },
      global: {
        stubs: {
          Teleport: true,
          Transition: false,
          AccountUsageCell: {
            emits: ['account-updated'],
            template: '<button data-test="quota-update" @click="$emit(\'account-updated\', updatedAccount)" />',
            setup: () => ({ updatedAccount })
          }
        }
      }
    })

    await wrapper.get('[data-test="quota-update"]').trigger('click')

    expect(wrapper.emitted<Account[]>('updated')).toEqual([[updatedAccount]])
  })

  it('rebuilds the usage cell when the drawer moves to another account', async () => {
    const mounted: number[] = []
    const unmounted: number[] = []
    const wrapper = shallowMount(AccountDetailsDrawer, {
      props: {
        account,
        folders: [],
        tags: [],
        todayStats: null,
        todayStatsLoading: false,
        manualRefreshToken: 0
      },
      global: {
        stubs: {
          Teleport: false,
          Transition: false,
          AccountUsageCell: defineComponent({
            props: { account: { type: Object as PropType<Account>, required: true } },
            setup(props) {
              const id = props.account.id
              mounted.push(id)
              onUnmounted(() => unmounted.push(id))
              return () => null
            }
          })
        }
      }
    })

    await wrapper.setProps({ account: { ...account, updated_at: '2026-08-05T00:01:00Z' } })
    expect(mounted).toEqual([71])

    await wrapper.setProps({ account: { ...account, id: 72 } })
    expect(mounted).toEqual([71, 72])
    expect(unmounted).toEqual([71])
    wrapper.unmount()
  })

  it('shows the cached usage when an account is reopened and queries again on a manual refresh', async () => {
    getUsage.mockResolvedValue({
      five_hour: { utilization: 10, resets_at: null, remaining_seconds: 0 },
      seven_day: null
    })
    const opened = { ...account, id: 73 }
    const wrapper = shallowMount(AccountDetailsDrawer, {
      props: {
        account: opened,
        folders: [],
        tags: [],
        todayStats: null,
        todayStatsLoading: false,
        manualRefreshToken: 0
      },
      global: { stubs: { Teleport: false, Transition: false, AccountUsageCell: false } }
    })
    await flushPromises()
    expect(getUsage.mock.calls).toEqual([[73]])

    await wrapper.setProps({ account: null })
    await wrapper.setProps({ account: opened })
    await flushPromises()
    expect(getUsage).toHaveBeenCalledTimes(1)

    await wrapper.setProps({ manualRefreshToken: 1 })
    await flushPromises()
    expect(getUsage.mock.calls).toEqual([[73], [73, undefined, true]])
    wrapper.unmount()
  })
})
