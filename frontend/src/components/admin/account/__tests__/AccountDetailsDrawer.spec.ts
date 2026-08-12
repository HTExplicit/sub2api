import { shallowMount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import AccountDetailsDrawer from '../AccountDetailsDrawer.vue'
import type { Account } from '@/types'

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: { setTaxonomy: vi.fn() }
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

  it('shows the masked Cindy device identity and source', () => {
	const cindyAccount = {
	  ...account,
	  type: 'apikey',
	  extra: {
		cindy_device_id: 'aaaaaaaa...aaaaaaaa',
		cindy_device_id_source: 'registration-record'
	  }
	} as Account
	const wrapper = shallowMount(AccountDetailsDrawer, {
	  props: {
		account: cindyAccount,
		folders: [],
		tags: [],
		todayStats: null,
		todayStatsLoading: false,
		manualRefreshToken: 0
	  },
	  global: { stubs: { Teleport: true, Transition: false } }
	})

	expect(wrapper.get('[data-test="cindy-device-id"]').text()).toBe('aaaaaaaa...aaaaaaaa')
	expect(wrapper.get('[data-test="cindy-device-id-source"]').text()).toBe('registration-record')
  })
})
