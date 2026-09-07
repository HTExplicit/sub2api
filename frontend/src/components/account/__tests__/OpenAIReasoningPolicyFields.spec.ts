import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import OpenAIReasoningPolicyFields from '../OpenAIReasoningPolicyFields.vue'
import { defaultOpenAIReasoningPolicy, emptyOpenAIReasoningPolicySelection } from '@/utils/openaiReasoningPolicy'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('OpenAI reasoning policy fields', () => {
  it('exposes independent accessible switches and tracks only deliberate edits', async () => {
    const wrapper = mount(OpenAIReasoningPolicyFields, {
      props: { modelValue: defaultOpenAIReasoningPolicy(), selected: emptyOpenAIReasoningPolicySelection(), idPrefix: 'test' }
    })
    expect(wrapper.get('[data-testid="openai-reasoning-chatReplay-toggle"]').attributes('aria-checked')).toBe('true')
    expect(wrapper.find('input[type="checkbox"]').exists()).toBe(false)
    await wrapper.get('[data-testid="openai-reasoning-chatReplay-toggle"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([{ chatReplay: false, signatureRecovery: true }])
    expect(wrapper.emitted('update:selected')?.[0]).toEqual([{ chatReplay: true, signatureRecovery: false }])
    expect(wrapper.text()).toContain('admin.accounts.openai.reasoningPolicy.boundaryHint')
  })

  it('does not allow bulk toggles to change an unselected setting', async () => {
    const wrapper = mount(OpenAIReasoningPolicyFields, {
      props: { modelValue: defaultOpenAIReasoningPolicy(), selected: emptyOpenAIReasoningPolicySelection(), idPrefix: 'bulk', bulk: true }
    })
    const toggle = wrapper.get('[data-testid="openai-reasoning-signatureRecovery-toggle"]')
    expect(toggle.attributes('disabled')).toBeDefined()
    await toggle.trigger('click')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    await wrapper.get('[data-testid="openai-reasoning-signatureRecovery-selected"]').setValue(true)
    expect(wrapper.emitted('update:selected')?.[0]).toEqual([{ chatReplay: false, signatureRecovery: true }])
    await wrapper.setProps({ selected: { chatReplay: false, signatureRecovery: true } })
    await toggle.trigger('click')
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([{ chatReplay: true, signatureRecovery: false }])
  })
})
