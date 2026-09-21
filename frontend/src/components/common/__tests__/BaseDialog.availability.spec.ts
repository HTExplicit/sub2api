import { computed, h, nextTick, ref } from 'vue'
import { mount } from '@vue/test-utils'
import { expect, it } from 'vitest'
import BaseDialog from '../BaseDialog.vue'
import { extensionAvailabilityKey, extensionUnavailableMessageKey } from '@sub2api/plugin-ui/context'

it('removes inert from body and footer after plugin recovery without replacing the draft', async () => {
  const available = ref(true)
  const wrapper = mount(BaseDialog, {
    attachTo: document.body,
    props: { show: true, title: 'Availability fixture' },
    global: {
      provide: {
        [extensionAvailabilityKey as symbol]: computed(() => available.value),
        [extensionUnavailableMessageKey as symbol]: computed(() => 'Plugin unavailable')
      },
      stubs: { Teleport: true, Transition: false }
    },
    slots: { default: () => h('input', { 'data-test': 'draft' }), footer: () => h('button', 'Save') }
  })
  try {
    await nextTick()
    expect(wrapper.findAll('[inert]')).toHaveLength(0)
    await wrapper.get('input').setValue('keep this draft')
    const input = wrapper.get('input').element
    available.value = false
    await nextTick()
    expect(wrapper.findAll('[inert]')).toHaveLength(2)
    expect(wrapper.get('[role="status"]').text()).toBe('Plugin unavailable')
    expect(wrapper.get('[aria-label="Close modal"]').attributes('inert')).toBeUndefined()
    available.value = true
    await nextTick()
    expect(wrapper.findAll('[inert]')).toHaveLength(0)
    expect(wrapper.get('input').element).toBe(input)
    expect((input as HTMLInputElement).value).toBe('keep this draft')
    expect(wrapper.find('[role="status"]').exists()).toBe(false)
  } finally {
    wrapper.unmount()
  }
})
