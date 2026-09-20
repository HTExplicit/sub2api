import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { mount } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'
import manifest from '../../../../../plugins/account-tools/manifest.source.json'
import type { PluginContribution } from '@/api/admin/plugins'
import { usePluginExtensions } from '@/stores/pluginExtensions'
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

  it('preserves choices during failure and removes stale choices after disable', async () => {
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
    expect(wrapper.emitted('update:values')?.at(-1)).toEqual([{ reasoning_effort: '' }])
    wrapper.unmount()
  })

  it('propagates unavailable state into teleported dialogs while retaining close', async () => {
    const wrapper = mount(defineComponent({ components: { ExtensionSurface, BaseDialog }, template: '<ExtensionSurface name="account-taxonomy"><BaseDialog :show="true" title="Taxonomy" :show-close-button="false"><button>change</button></BaseDialog></ExtensionSurface>' }), { attachTo: document.body, global: { stubs: { Icon: true } } })
    await nextTick()
    const registry = usePluginExtensions()
    registry.items = registry.items.map(item => ({ ...item, available: false }))
    await nextTick()
    expect(document.querySelector('.modal-body')?.hasAttribute('inert')).toBe(true)
    expect(document.querySelector('[aria-label="Close modal"]')).not.toBeNull()
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('admin.plugins.extensionUnavailable')
    registry.items = []
    await nextTick()
    expect(document.querySelector('[role="dialog"]')).toBeNull()
    wrapper.unmount()
  })
})
