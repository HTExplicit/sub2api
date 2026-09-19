import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { ref } from 'vue'

import ExtensionSlot from '../ExtensionSlot.vue'
import { usePluginExtensions } from '@/stores/pluginExtensions'

const mocks = vi.hoisted(() => ({ contributions: vi.fn() }))
vi.mock('@/api/admin/plugins', () => ({ contributions: mocks.contributions, default: { contributions: mocks.contributions } }))
vi.mock('../ExtensionDialog.vue', () => ({ default: { template: '<span />' } }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ locale: ref('zh'), t: (key: string) => key }) }
})

const account = (id: number, type = 'oauth') => ({
  id,
  platform: 'openai',
  type,
  status: 'active',
  parent_account_id: null
})

function contribution(available = true) {
  return {
    id: 'ticket-harvest',
    slot: 'account.actions',
    label: { zh: '采集292票据', en: 'Acquire ticket' },
    permission: 'admin',
    action: 'harvest',
    plugin_id: 7,
    available,
    account_filter: { platforms: ['openai'], types: ['oauth', 'setup-token'], exclude_shadows: true }
  }
}

describe('ExtensionSlot', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    const registry = usePluginExtensions()
    registry.items = [contribution()]
    registry.loaded = true
  })

  it('requires every selected account identity to satisfy the declared filter', () => {
    const eligible = mount(ExtensionSlot, { props: { name: 'account.actions', accountIds: [1, 2], accounts: [account(1), account(2, 'setup-token')] } })
    expect(eligible.findAll('button')).toHaveLength(1)
    eligible.unmount()

    const unknown = mount(ExtensionSlot, { props: { name: 'account.actions', accountIds: [1, 2], accounts: [account(1)] } })
    expect(unknown.findAll('button')).toHaveLength(0)
    unknown.unmount()

    const mixed = mount(ExtensionSlot, { props: { name: 'account.actions', accountIds: [1, 2], accounts: [account(1), { ...account(2), parent_account_id: 1 }] } })
    expect(mixed.findAll('button')).toHaveLength(0)
  })

  it('keeps failed plugin contributions visible but disabled and enforces the host account limit', () => {
    const registry = usePluginExtensions()
    registry.items = [contribution(false)]
    const failed = mount(ExtensionSlot, { props: { name: 'account.actions', account: account(1), variant: 'menu' } })
    expect(failed.get('button').attributes('disabled')).toBeDefined()
    expect(failed.get('button').attributes('title')).toBe('admin.plugins.extensionUnavailable')
    expect(failed.get('button').classes()).toContain('w-full')
    failed.unmount()

    registry.items = [contribution(true)]
    const accounts = Array.from({ length: 101 }, (_, index) => account(index + 1))
    const limited = mount(ExtensionSlot, { props: { name: 'account.actions', accountIds: accounts.map(item => item.id), accounts } })
    expect(limited.get('button').attributes('disabled')).toBeDefined()
    expect(limited.get('button').attributes('title')).toBe('admin.plugins.accountLimit')
  })
})
