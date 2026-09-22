import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { reactive } from 'vue'
import { viewContributions } from './accountView.fixtures'
import type { PluginContribution } from '@/api/admin/plugins'
import AccountViewPage from '../AccountViewPage.vue'

const registry = reactive({ actorID: 1, loaded: true, items: [] as PluginContribution[], refresh: vi.fn() })
vi.mock('@/stores/pluginExtensions', () => ({ usePluginExtensions: () => registry }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/views/admin/AccountsView.vue', () => ({ default: { name: 'AccountsView', props: ['viewContribution'], data: () => ({ draft: '' }), template: '<div data-test="workbench" :data-available="String(viewContribution.available)"><input v-model="draft" /><span data-test="existing-result">result #9</span></div>' } }))
const mounted: VueWrapper[] = []
beforeEach(() => { registry.actorID = 1; registry.items = viewContributions() })
afterEach(() => { for (const wrapper of mounted.splice(0)) wrapper.unmount() })
const open = () => { const wrapper = mount(AccountViewPage, { props: { pluginKey: 'codexrip.cindy-provider', viewId: 'cindy-accounts' }, global: { stubs: { AppLayout: { template: '<div><slot /></div>' } } } }); mounted.push(wrapper); return wrapper }

describe('owner-qualified account view page', () => {
  it('does not mount a workbench for a missing owner or a same-id impostor', () => {
    registry.items = viewContributions().map(item => ({ ...item, plugin_key: 'other.plugin', plugin_id: 8 }))
    const wrapper = open()
    expect(wrapper.find('[data-test="workbench"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="account-view-unavailable"]').exists()).toBe(true)
  })
  it('retains the same mounted draft and result through disable fault and package replacement', async () => {
    const wrapper = open(), input = wrapper.get('input').element
    await wrapper.get('input').setValue('keep my draft')
    registry.items = []
    await flushPromises()
    expect(wrapper.get('input').element).toBe(input)
    expect((input as HTMLInputElement).value).toBe('keep my draft')
    expect(wrapper.get('[data-test="workbench"]').attributes('data-available')).toBe('false')
    expect(wrapper.get('[data-test="existing-result"]').text()).toBe('result #9')
    registry.items = viewContributions(false)
    await flushPromises()
    expect(wrapper.get('input').element).toBe(input)
    registry.items = viewContributions().map(item => ({ ...item, package_sha256: 'c'.repeat(64) }))
    await flushPromises()
    expect(wrapper.get('input').element).toBe(input)
    expect(wrapper.get('[data-test="workbench"]').attributes('data-available')).toBe('false')
  })
  it('does not expose a previous actor draft to a new actor', async () => {
    const wrapper = open()
    await wrapper.get('input').setValue('actor one')
    registry.actorID = 2; registry.items = []
    await flushPromises()
    expect(wrapper.find('input').exists()).toBe(false)
    registry.items = viewContributions()
    await flushPromises()
    expect((wrapper.get('input').element as HTMLInputElement).value).toBe('')
  })
})
