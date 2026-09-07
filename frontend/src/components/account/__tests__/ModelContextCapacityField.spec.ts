import { defineComponent } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import type { ModelContextCapacityRow } from '@/api/admin/accounts'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) => `${key}${params ? ` ${Object.values(params).join(' ')}` : ''}`
  })
}))

import ModelContextCapacityField from '../ModelContextCapacityField.vue'

function capacityRow(overrides: Partial<ModelContextCapacityRow> = {}): ModelContextCapacityRow {
  return {
    upstream_model_id: 'gpt-5.2',
    aliases: ['public-model'],
    editable: true,
    upstream: { context_window: 258_000, observed_at: '2026-09-07T10:00:00Z' },
    automatic_context_window: 400_000,
    automatic_source: 'official',
    effective_context_window: 400_000,
    effective_source: 'official',
    capacity_basis: 'total_context',
    ...overrides
  }
}

function mountField(row: ModelContextCapacityRow | undefined = capacityRow(), draft?: string, modelId = 'gpt-5.2') {
  return mount(ModelContextCapacityField, { props: { row, draft, modelId } })
}

describe('ModelContextCapacityField', () => {
  it('shows compact capacity and its source without hover or another panel', () => {
    const wrapper = mountField()
    expect(wrapper.get('[data-testid="context-capacity-value"]').text()).toBe('[400K]')
    expect(wrapper.get('[data-testid="context-capacity-source"]').text()).toContain('sources.official')
    expect(wrapper.get('[data-testid="context-capacity-source"]').isVisible()).toBe(true)
    expect(wrapper.text()).not.toContain('gpt-5.2')
    expect(wrapper.find('input').exists()).toBe(false)
    expect(wrapper.get('button').attributes('type')).toBe('button')
    expect(wrapper.get('button').attributes('title')).toContain('400000')
    expect(wrapper.get('button').attributes('title')).toContain('exceedsUpstream')
    expect(wrapper.emitted('commit')).toBeUndefined()
  })

  it('opens the displayed value in the same field and keeps typing local until Enter', async () => {
    const wrapper = mountField()
    const buttonWidth = wrapper.get('button').attributes('style')
    await wrapper.get('button').trigger('click')
    expect(wrapper.find('button').exists()).toBe(false)
    const input = wrapper.get('input')
    expect((input.element as HTMLInputElement).value).toBe('400K')
    expect(input.attributes('style')).toBe(buttonWidth)
    expect(wrapper.emitted('editing')).toEqual([[true]])
    await input.setValue('1.048576M')
    expect(wrapper.emitted('commit')).toBeUndefined()
    expect(wrapper.get('[data-testid="context-capacity-source"]').text()).toContain('sources.official')
    await input.trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('commit')).toEqual([['1.048576M']])
    expect(wrapper.emitted('editing')).toEqual([[true], [false]])
    expect(wrapper.find('input').exists()).toBe(false)
    await input.trigger('blur')
    expect(wrapper.emitted('commit')).toEqual([['1.048576M']])
    await wrapper.setProps({ draft: '1.048576M' })
    expect(wrapper.get('[data-testid="context-capacity-value"]').text()).toBe('[1.048576M]')
    expect(wrapper.get('[data-testid="context-capacity-source"]').text()).toContain('sources.custom')
  })

  it('confirms a changed valid value on blur without saving or mutating props', async () => {
    const wrapper = mountField()
    await wrapper.get('button').trigger('click')
    await wrapper.get('input').setValue(' 900000 ')
    await wrapper.get('input').trigger('blur')
    expect(wrapper.emitted('commit')).toEqual([['900000']])
    expect(wrapper.props('draft')).toBeUndefined()
    expect(wrapper.props('row')?.custom_context_window).toBeUndefined()
  })

  it.each(['400K', '400000', '0.4M'])('does not create an override for an unchanged automatic value %s', async raw => {
    const wrapper = mountField()
    await wrapper.get('button').trigger('click')
    await wrapper.get('input').setValue(raw)
    await wrapper.get('input').trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('commit')).toBeUndefined()
    expect(wrapper.get('[data-testid="context-capacity-source"]').text()).toContain('sources.official')
  })

  it('does not rewrite an existing saved override just because its input format changes', async () => {
    const wrapper = mountField(capacityRow({ custom_context_window: 1_000_000, effective_context_window: 1_000_000, effective_source: 'custom' }))
    await wrapper.get('button').trigger('click')
    await wrapper.get('input').setValue('1.000000M')
    await wrapper.get('input').trigger('blur')
    expect(wrapper.emitted('commit')).toBeUndefined()
    expect(wrapper.get('[data-testid="context-capacity-source"]').text()).toContain('sources.custom')
  })

  it('clears explicitly and previews automatic capacity only after parent draft confirmation', async () => {
    const wrapper = mountField(capacityRow({ custom_context_window: 1_000_000, effective_context_window: 1_000_000, effective_source: 'custom' }))
    await wrapper.get('button').trigger('click')
    await wrapper.get('input').setValue('')
    await wrapper.get('input').trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('commit')).toEqual([['']])
    await wrapper.setProps({ draft: '' })
    expect(wrapper.get('[data-testid="context-capacity-value"]').text()).toBe('[400K]')
    expect(wrapper.get('[data-testid="context-capacity-source"]').text()).toContain('sources.official')
    await wrapper.get('button').trigger('click')
    await wrapper.get('input').trigger('blur')
    expect(wrapper.emitted('commit')).toEqual([['']])
  })

  it('cancels with Escape and cannot accidentally confirm on the following blur', async () => {
    const wrapper = mountField(capacityRow(), '900K')
    await wrapper.get('button').trigger('click')
    const input = wrapper.get('input')
    await input.setValue('2M')
    await input.trigger('keydown', { key: 'Escape' })
    await input.trigger('blur')
    expect(wrapper.emitted('commit')).toBeUndefined()
    expect(wrapper.get('[data-testid="context-capacity-value"]').text()).toBe('[900K]')
    expect(wrapper.get('[data-testid="context-capacity-source"]').text()).toContain('sources.custom')
    expect(wrapper.emitted('validity')?.at(-1)).toEqual([true])
  })

  it('keeps invalid input inline, rejects Enter and blur, then releases validity on cancel', async () => {
    const wrapper = mountField()
    expect(wrapper.emitted('validity')).toEqual([[true]])
    await wrapper.get('button').trigger('click')
    const input = wrapper.get('input')
    await input.setValue('1.0000001M')
    expect(wrapper.emitted('validity')?.at(-1)).toEqual([false])
    expect(input.attributes('aria-invalid')).toBe('true')
    expect(wrapper.get('[role="alert"]').text()).toContain('contextCapacity.invalid')
    await input.trigger('keydown', { key: 'Enter' })
    await input.trigger('blur')
    expect(wrapper.find('input').exists()).toBe(true)
    expect(wrapper.emitted('commit')).toBeUndefined()
    await input.trigger('keydown', { key: 'Escape' })
    expect(wrapper.emitted('validity')?.at(-1)).toEqual([true])
  })

  it('keeps a dirty editor when fresh metadata arrives for the same target', async () => {
    const wrapper = mountField()
    await wrapper.get('button').trigger('click')
    await wrapper.get('input').setValue('2M')
    await wrapper.setProps({ row: capacityRow({ automatic_context_window: 1_050_000, effective_context_window: 1_050_000 }) })
    expect((wrapper.get('input').element as HTMLInputElement).value).toBe('2M')
    await wrapper.get('input').trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('commit')).toEqual([['2M']])
  })

  it('cancels and releases invalid-editor guards when the target changes or unmounts', async () => {
    const wrapper = mountField()
    await wrapper.get('button').trigger('click')
    await wrapper.get('input').setValue('invalid')
    await wrapper.setProps({ modelId: 'another', row: capacityRow({ upstream_model_id: 'another' }) })
    expect(wrapper.find('input').exists()).toBe(false)
    expect(wrapper.emitted('validity')?.at(-1)).toEqual([true])
    expect(wrapper.emitted('commit')).toBeUndefined()
    await wrapper.get('button').trigger('click')
    await wrapper.get('input').setValue('invalid')
    wrapper.unmount()
    expect(wrapper.emitted('validity')?.at(-1)).toEqual([true])
    expect(wrapper.emitted('editing')?.at(-1)).toEqual([false])
  })

  it('keeps protected catalogs read-only and ignores supplied drafts', async () => {
    const wrapper = mountField(capacityRow({ editable: false, effective_context_window: 372_000, effective_source: 'protected' }), '2M')
    expect(wrapper.find('button').exists()).toBe(false)
    expect(wrapper.find('input').exists()).toBe(false)
    expect(wrapper.get('[data-testid="context-capacity-value"]').text()).toBe('[372K]')
    expect(wrapper.get('[data-testid="context-capacity-source"]').text()).toContain('sources.protected')
    await wrapper.setProps({ row: capacityRow({ editable: true, effective_context_window: 372_000, effective_source: 'protected' }) })
    expect(wrapper.find('button').exists()).toBe(false)
    expect(wrapper.emitted('commit')).toBeUndefined()
  })

  it('cancels pending input if the row becomes read-only', async () => {
    const wrapper = mountField()
    await wrapper.get('button').trigger('click')
    const input = wrapper.get('input')
    await input.setValue('2M')
    await wrapper.setProps({ row: capacityRow({ editable: false, effective_source: 'protected' }) })
    await input.trigger('blur')
    expect(wrapper.find('input').exists()).toBe(false)
    expect(wrapper.emitted('commit')).toBeUndefined()
  })

  it('accepts exact aliases without changing the model identity', async () => {
    const wrapper = mountField(capacityRow(), undefined, 'public-model')
    expect(wrapper.get('[data-testid="model-context-capacity-field"]').attributes('data-model-id')).toBe('gpt-5.2')
    await wrapper.get('button').trigger('click')
    await wrapper.get('input').setValue('2M')
    await wrapper.get('input').trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('commit')).toEqual([['2M']])
    expect(wrapper.props('modelId')).toBe('public-model')
    expect(wrapper.props('row')?.upstream_model_id).toBe('gpt-5.2')
  })

  it.each(['', ' ', 'gpt-*', ' gpt-5.2', 'gpt-5.2\n', 'Public-model', 'unrelated', '模'.repeat(171)])(
    'does not expose an editor for an invalid or unbound model %j', modelId => {
      const wrapper = mountField(capacityRow(), '2M', modelId)
      expect(wrapper.find('button').exists()).toBe(false)
      expect(wrapper.get('[data-testid="context-capacity-value"]').text()).toBe('[—]')
      expect(wrapper.get('[data-testid="context-capacity-source"]').text()).toContain('contextCapacity.unknown')
    }
  )

  it('does not make a missing or wildcard upstream target editable', async () => {
    const wrapper = mount(ModelContextCapacityField, { props: { modelId: 'unknown-model' } })
    expect(wrapper.find('button').exists()).toBe(false)
    expect(wrapper.get('[data-testid="context-capacity-value"]').text()).toBe('[—]')
    await wrapper.setProps({ modelId: 'public-model', row: capacityRow({ upstream_model_id: '*' }) })
    expect(wrapper.find('button').exists()).toBe(false)
  })

  it('isolates pointer and keyboard activity while preserving native input behavior', async () => {
    const parentEvent = vi.fn()
    const submit = vi.fn()
    const wrapper = mount(defineComponent({
      components: { ModelContextCapacityField },
      setup: () => ({ row: capacityRow(), parentEvent, submit }),
      template: `<form @submit.prevent="submit"><div @click="parentEvent" @dblclick="parentEvent" @mousedown="parentEvent" @mouseup="parentEvent" @pointerdown="parentEvent" @pointerup="parentEvent" @keydown="parentEvent" @keyup="parentEvent" @keypress="parentEvent"><ModelContextCapacityField model-id="gpt-5.2" :row="row" /></div></form>`
    }))
    const field = wrapper.getComponent(ModelContextCapacityField)
    const button = field.get('button')
    for (const event of ['pointerdown', 'mousedown', 'mouseup', 'pointerup', 'click']) await button.trigger(event)
    const input = field.get('input')
    for (const event of ['pointerdown', 'mousedown', 'mouseup', 'pointerup', 'click', 'dblclick']) await input.trigger(event)
    for (const key of ['ArrowLeft', 'ArrowRight', 'Backspace', 'Delete', ' ', 'Tab']) {
      const event = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true })
      input.element.dispatchEvent(event)
      expect(event.defaultPrevented).toBe(false)
      await input.trigger('keyup', { key })
      await input.trigger('keypress', { key })
    }
    await input.setValue('2M')
    const enter = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })
    input.element.dispatchEvent(enter)
    expect(enter.defaultPrevented).toBe(true)
    expect(field.emitted('commit')).toEqual([['2M']])
    expect(parentEvent).not.toHaveBeenCalled()
    expect(submit).not.toHaveBeenCalled()
  })

  it('does not commit an Enter used to finish IME composition', async () => {
    const wrapper = mountField()
    await wrapper.get('button').trigger('click')
    await wrapper.get('input').setValue('2M')
    await wrapper.get('input').trigger('keydown', { key: 'Enter', isComposing: true })
    expect(wrapper.find('input').exists()).toBe(true)
    expect(wrapper.emitted('commit')).toBeUndefined()
  })
})
