import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { mount } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'
import manifest from '../../../../../plugins/account-tools/manifest.source.json'
import type { PluginContribution } from '@/api/admin/plugins'
import { usePluginExtensions } from '@/stores/pluginExtensions'
import { useAuthStore } from '@/stores/auth'
import ExtensionFields from '../ExtensionFields.vue'
import ExtensionSurface from '../ExtensionSurface.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'

vi.mock('vue-i18n', async importOriginal => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ locale: { value: 'zh' }, t: (key: string) => key }) }))
vi.mock('@/components/common/Select.vue', () => ({ default: { props: ['modelValue', 'options', 'disabled'], emits: ['update:modelValue'], template: '<select :value="modelValue" :disabled="disabled" @change="$emit(\'update:modelValue\', $event.target.value)"><option v-for="option in options" :key="option.value" :value="option.value">{{ option.label }}</option></select>' } }))

describe('declared plugin fields and surfaces', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    const registry = usePluginExtensions()
    registry.loaded = true
    registry.items = manifest.contributions.map(item => ({ ...item, plugin_id: 7, available: true })) as PluginContribution[]
  })

  it('renders model choices and the actual default from the plugin field contract', async () => {
    const wrapper = mount(ExtensionFields, { props: { name: 'account.test', values: { reasoning_effort: '' }, context: { reasoning_efforts: ['low', 'high'], default_reasoning_effort: 'high' } } })
    expect(wrapper.findAll('option').map(option => option.text())).toEqual(['默认 (high)', 'low', 'high'])
    await wrapper.get('select').setValue('high')
    expect(wrapper.emitted('update:values')?.at(-1)).toEqual([{ reasoning_effort: 'high' }])
    wrapper.unmount()
  })

  it('renders plugin-owned text fields and keeps the draft when the capability is disabled', async () => {
    const wrapper = mount(ExtensionFields, { props: { name: 'account.test.prompt', values: { prompt: '😀'.repeat(8192) }, context: {} } })
    expect(wrapper.get('textarea').attributes('placeholder')).toBe('hi')
    expect(wrapper.emitted('validity')?.at(-1)).toEqual([true])
    await wrapper.setProps({ values: { prompt: '😀'.repeat(8193) } })
    expect(wrapper.emitted('validity')?.at(-1)).toEqual([false])
    usePluginExtensions().items = []
    await nextTick()
    expect(wrapper.find('textarea').exists()).toBe(false)
    expect(wrapper.emitted('update:values')).toBeUndefined()
    wrapper.unmount()
  })

  it('preserves choices during failure and keeps drafts after disable', async () => {
    const wrapper = mount(ExtensionFields, { props: { name: 'account.test', values: { reasoning_effort: 'high' }, context: { reasoning_efforts: ['high'] } } })
    const registry = usePluginExtensions()
    registry.items = registry.items.map(item => ({ ...item, available: false }))
    await nextTick()
    expect(wrapper.get('select').attributes('disabled')).toBeDefined()
    expect(wrapper.emitted('validity')?.at(-1)).toEqual([false])
    expect(wrapper.emitted('update:values')).toBeUndefined()
    registry.items = []
    await nextTick()
    expect(wrapper.find('select').exists()).toBe(false)
    expect(wrapper.emitted('update:values')).toBeUndefined()
    wrapper.unmount()
  })

  it('keeps an out-of-scope draft and permits explicit reset without enabling field execution', async () => {
    const registry = usePluginExtensions()
    registry.items = registry.items.map(item => ({ ...item, account_scope: { version: 1, bindings: [{ platform: 'openai', account_type: 'oauth', rollout_percent: 50 }] } }))
    const wrapper = mount(ExtensionFields, { props: { name: 'account.test.prompt', values: { prompt: 'retained draft' }, context: {}, account: { id: 3, platform: 'openai', type: 'oauth', parent_account_id: null } } })
    expect(wrapper.get('textarea').attributes('disabled')).toBeDefined()
    expect((wrapper.get('textarea').element as HTMLTextAreaElement).value).toBe('retained draft')
    expect(wrapper.emitted('update:values')).toBeUndefined()
    expect(wrapper.get('button').attributes('disabled')).toBeUndefined()
    await wrapper.get('button').trigger('click')
    expect(wrapper.emitted('update:values')?.at(-1)).toEqual([{ prompt: '' }])
    wrapper.unmount()
  })

  it('propagates unavailable state into teleported dialogs while retaining drafts until the actor changes', async () => {
    const registry = usePluginExtensions()
    registry.items = registry.items.map(item => ({ ...item, plugin_key: 'codexrip.account-tools', package_sha256: 'a'.repeat(64) }))
    const original = registry.items.find(item => item.id === 'account-taxonomy')!
    const wrapper = mount(defineComponent({ components: { ExtensionSurface, BaseDialog }, template: '<ExtensionSurface name="account-taxonomy"><BaseDialog :show="true" title="Taxonomy" :show-close-button="false"><input data-test="retained-dialog-input" value="initial"/><button>change</button></BaseDialog></ExtensionSurface>' }), { attachTo: document.body, global: { stubs: { Icon: true } } })
    await nextTick()
    const input = document.querySelector<HTMLInputElement>('[data-test="retained-dialog-input"]')!
    input.value = 'owned draft'
    registry.items = registry.items.map(item => ({ ...item, available: false }))
    await nextTick()
    expect(document.querySelector('.modal-body')?.hasAttribute('inert')).toBe(true)
    expect(document.querySelector('[aria-label="Close modal"]')).not.toBeNull()
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('admin.plugins.extensionUnavailable')
    registry.items = []
    await nextTick()
    expect(document.querySelector('[data-test="retained-dialog-input"]')).toBe(input)
    expect(input.value).toBe('owned draft')
    expect(document.querySelector('.modal-body')?.hasAttribute('inert')).toBe(true)
    const replacement = { ...original, package_sha256: 'b'.repeat(64), available: true }
    registry.items = [replacement]
    await nextTick()
    // A replacement package cannot turn the old mounted form into a new grant.
    expect(document.querySelector('[data-test="retained-dialog-input"]')).toBe(input)
    expect(document.querySelector('.modal-body')?.hasAttribute('inert')).toBe(true)
    useAuthStore().user = { id: 42, role: 'admin' } as never
    await nextTick()
    expect(document.querySelector('[role="dialog"]')).toBeNull()
    registry.loaded = true; registry.items = [{ ...replacement }]
    await nextTick()
    const nextInput = document.querySelector<HTMLInputElement>('[data-test="retained-dialog-input"]')!
    expect(nextInput).not.toBe(input)
    expect(nextInput.value).toBe('initial')
    wrapper.unmount()
  })

  it('keeps declared retained controls usable after disable without admitting new business IO', async () => {
    const registry = usePluginExtensions()
    registry.items = [{ id: 'image-studio', slot: 'surface', plugin_id: 9, plugin_key: 'fixture.images', package_sha256: 'a'.repeat(64), permission: 'user', label: { en: 'Images' }, available: true, retained_controls: true }]
    const start = vi.fn(), history = vi.fn()
    const wrapper = mount(defineComponent({ components: { ExtensionSurface }, setup: () => ({ start, history }),
      template: '<ExtensionSurface name="image-studio" v-slot="{ available }"><input data-test="retained-input"/><span data-test="availability">{{ available }}</span><button data-test="new-business" :disabled="!available" @click="available && start()">create</button><button data-test="existing-history" @click="history">history</button></ExtensionSurface>' }))
    const input = wrapper.get('[data-test="retained-input"]').element
    await wrapper.get('[data-test="retained-input"]').setValue('saved input')
    await wrapper.get('[data-test="new-business"]').trigger('click')
    expect(start).toHaveBeenCalledTimes(1)
    registry.items = registry.items.map(item => ({ ...item, available: false }))
    await nextTick()
    expect(wrapper.find('[inert]').exists()).toBe(false)
    expect(wrapper.get('[data-test="availability"]').text()).toBe('false')
    await wrapper.get('[data-test="new-business"]').trigger('click')
    expect(start).toHaveBeenCalledTimes(1)
    registry.items = []
    await nextTick()
    expect(wrapper.get('[data-test="retained-input"]').element).toBe(input)
    expect((input as HTMLInputElement).value).toBe('saved input')
    expect(wrapper.get('[data-test="availability"]').text()).toBe('false')
    expect(wrapper.get('[data-test="new-business"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-test="existing-history"]').trigger('click')
    expect(history).toHaveBeenCalledTimes(1)
    expect(start).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
})
