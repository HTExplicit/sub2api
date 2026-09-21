import { computed, nextTick, ref } from 'vue'
import { mount } from '@vue/test-utils'
import { afterEach, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import Select from '../Select.vue'
import { extensionAvailabilityKey } from '@sub2api/plugin-ui/context'

let cleanup: (() => void) | undefined
afterEach(() => { cleanup?.(); cleanup = undefined; vi.useRealTimers() })

it.each(['plugin-unavailable', 'disabled-prop'])('closes a teleported selection when %s without replacing its value', async (cause) => {
  const available = ref(true)
  const wrapper = mount(Select, {
    attachTo: document.body,
    props: { modelValue: 'kept', options: [{ value: 'kept', label: 'Kept selection' }, { value: 'other', label: 'Other' }] },
    global: {
      stubs: { Transition: true },
      plugins: [createI18n({ legacy: false, locale: 'en', messages: { en: { common: { selectOption: 'Select', searchPlaceholder: 'Search', noOptionsFound: 'No options' } } } })],
      provide: { [extensionAvailabilityKey as symbol]: computed(() => available.value) }
    }
  })
  cleanup = () => wrapper.unmount()
  await wrapper.get('button').trigger('click')
  await nextTick()
  expect(document.body.querySelector('[role="listbox"]')).not.toBeNull()

  if (cause === 'plugin-unavailable') available.value = false
  else await wrapper.setProps({ disabled: true })
  await nextTick()

  expect(document.body.querySelector('[role="listbox"]')).toBeNull()
  expect(wrapper.get('button').attributes('disabled')).toBeDefined()
  expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  expect(wrapper.text()).toContain('Kept selection')
  available.value = true
  await wrapper.setProps({ disabled: false })
  await wrapper.get('button').trigger('click')
  await nextTick()
  const selected = document.body.querySelector('[role="option"][aria-selected="true"]')
  expect(selected?.textContent).toContain('Kept selection')
})

it('rejects queued portal clicks and cancels queued search while unavailable, then permits an explicit selection after recovery', async () => {
  vi.useFakeTimers()
  const available = ref(true)
  const wrapper = mount(Select, {
    attachTo: document.body,
    props: { modelValue: 'kept', remote: true, options: [{ value: 'kept', label: 'Kept' }, { value: 'other', label: 'Other' }] },
    global: {
      stubs: { Transition: true },
      plugins: [createI18n({ legacy: false, locale: 'en', messages: { en: { common: { searchPlaceholder: 'Search' } } } })],
      provide: { [extensionAvailabilityKey as symbol]: computed(() => available.value) }
    }
  })
  cleanup = () => wrapper.unmount()
  await wrapper.get('button').trigger('click')
  await nextTick()
  const search = document.body.querySelector('input') as HTMLInputElement
  search.value = 'queued query'
  search.dispatchEvent(new Event('input', { bubbles: true }))
  await nextTick()
  const other = document.body.querySelectorAll<HTMLElement>('[role="option"]')[1]!
  available.value = false
  other.click()
  await nextTick()
  await vi.advanceTimersByTimeAsync(350)
  expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  expect(wrapper.emitted('search')).toBeUndefined()
  available.value = true
  await nextTick()
  await wrapper.get('button').trigger('click')
  await nextTick()
  document.body.querySelectorAll<HTMLElement>('[role="option"]')[1]!.click()
  await nextTick()
  expect(wrapper.emitted('update:modelValue')).toEqual([['other']])
})
