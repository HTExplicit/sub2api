import { afterEach, describe, expect, it } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import Input from '../Input.vue'
import SearchInput from '../SearchInput.vue'

enableAutoUnmount(afterEach)

describe('declarative empty-field placeholders', () => {
  it('keeps an empty shared input identifiable without document mutation', async () => {
    const wrapper = mount(Input, { props: { modelValue: '', label: 'Account name' } })
    expect(wrapper.get('input').attributes('placeholder')).toBe(' ')

    await wrapper.setProps({ placeholder: 'Enter a name' })
    expect(wrapper.get('input').attributes('placeholder')).toBe('Enter a name')

    await wrapper.setProps({ placeholder: '' })
    expect(wrapper.get('input').attributes('placeholder')).toBe(' ')
  })

  it('keeps blank search hints explicit while retaining its default hint', async () => {
    const wrapper = mount(SearchInput, {
      props: { modelValue: '' },
      global: { stubs: { Icon: true } }
    })
    expect(wrapper.get('input').attributes('placeholder')).toBe('Search...')

    await wrapper.setProps({ placeholder: '' })
    expect(wrapper.get('input').attributes('placeholder')).toBe(' ')
  })
})
