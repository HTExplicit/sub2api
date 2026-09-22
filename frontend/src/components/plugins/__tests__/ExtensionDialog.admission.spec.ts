import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { usePluginExtensions } from '@/stores/pluginExtensions'
import ExtensionDialog from '../ExtensionDialog.vue'

vi.mock('@/api/admin/accounts', () => { const list = vi.fn().mockResolvedValue({ items: [] }); return { list, default: { list } } })
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ locale: { value: 'en' }, t: (key: string) => key }) }))
vi.mock('../PluginFrame.vue', () => ({ default: { name: 'PluginFrame', props: ['admission', 'context'], template: '<div data-frame />' } }))
vi.mock('@/components/admin/account-jobs/AccountOperationDialog.vue', () => ({ default: { template: '<div><slot /></div>' } }))

describe('contribution dialog admission', () => {
  it('passes the same whole-selection decision to the frame and keeps missing accounts denied', async () => {
    const pinia = createPinia(), registry = usePluginExtensions(pinia)
    registry.loaded = true
    const item = { id: 'harvest', plugin_id: 7, slot: 'account.actions', permission: 'admin', action: 'harvest', available: true, label: { en: 'Harvest' }, account_scope: { version: 1 as const, bindings: [{ platform: 'openai', account_type: 'oauth', rollout_percent: 50 }] } }
    registry.items = [item]
    const accounts = [2, 3].map(id => ({ id, platform: 'openai', type: 'oauth', parent_account_id: null }))
    const wrapper = mount(ExtensionDialog, { props: { contribution: item, accountIds: [2, 3], accounts }, global: { plugins: [pinia] } })
    await flushPromises()
    expect(wrapper.find('[inert]').exists()).toBe(true)
    expect(wrapper.findComponent({ name: 'PluginFrame' }).props('admission').allowed).toBe(false)
    await wrapper.setProps({ accountIds: [2] })
    await flushPromises()
    expect(wrapper.find('[inert]').exists()).toBe(true)
    expect(wrapper.findComponent({ name: 'PluginFrame' }).props('context').account_ids).toEqual([2, 3])
    await wrapper.setProps({ launchKey: 1 })
    await flushPromises()
    expect(wrapper.find('[inert]').exists()).toBe(false)
    expect(wrapper.findComponent({ name: 'PluginFrame' }).props('admission').allowed).toBe(true)
    expect(wrapper.findComponent({ name: 'PluginFrame' }).props('context').account_ids).toEqual([2])
    await wrapper.setProps({ accountIds: [2, 999], launchKey: 2 })
    await flushPromises()
    expect(wrapper.findComponent({ name: 'PluginFrame' }).props('context').account_ids).toEqual([2, 999])
    expect(wrapper.find('[inert]').exists()).toBe(true)
    expect(wrapper.findComponent({ name: 'PluginFrame' }).props('admission').allowed).toBe(false)
    wrapper.unmount()
  })
})
