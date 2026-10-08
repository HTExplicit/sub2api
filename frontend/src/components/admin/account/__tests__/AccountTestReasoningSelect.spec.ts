import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import AccountTestReasoningSelect from '../AccountTestReasoningSelect.vue'
import { isAccountTestReasoningValid } from '@/utils/accountTestModels'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('AccountTestReasoningSelect native efforts', () => {
  it('never displays ultra from a stale capability list, default or selected value, and resets the old selection', () => {
    const model = { id: 'alias', display_name: 'Astra', reasoning_efforts: ['high', 'max', 'ultra'], default_reasoning_effort: 'ultra' }
    const wrapper = mount(AccountTestReasoningSelect, { props: { model, modelValue: 'ultra' } })
    expect(wrapper.text()).not.toContain('ultra')
    expect(wrapper.findAll('option').map(option => option.attributes('value'))).toEqual(['', 'high', 'max'])
    expect(wrapper.emitted('update:modelValue')).toEqual([['']])
    expect(isAccountTestReasoningValid(model, 'ultra')).toBe(false)
    expect(isAccountTestReasoningValid(model, 'max')).toBe(true)
    wrapper.unmount()
  })
})
