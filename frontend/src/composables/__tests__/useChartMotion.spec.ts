import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, nextTick } from 'vue'
import { useChartMotion } from '../useChartMotion'

describe('useChartMotion', () => {
  afterEach(() => vi.restoreAllMocks())

  it('uses a short initial animation and follows live reduced motion changes', async () => {
    const listeners = new Set<(event: MediaQueryListEvent) => void>()
    const media = {
      matches: false,
      addEventListener: vi.fn((_name: string, listener: (event: MediaQueryListEvent) => void) => listeners.add(listener)),
      removeEventListener: vi.fn((_name: string, listener: (event: MediaQueryListEvent) => void) => listeners.delete(listener))
    }
    vi.spyOn(window, 'matchMedia').mockReturnValue(media as unknown as MediaQueryList)
    const wrapper = mount(defineComponent({
      setup() { return { motion: useChartMotion() } },
      template: '<div>{{ JSON.stringify(motion) }}</div>'
    }))
    await nextTick()
    expect(JSON.parse(wrapper.text())).toMatchObject({
      animation: { duration: 180 },
      transitions: { active: { animation: { duration: 0 } } }
    })

    media.matches = true
    listeners.forEach(listener => listener({ matches: true } as MediaQueryListEvent))
    await nextTick()
    expect(JSON.parse(wrapper.text()).animation).toBe(false)

    media.matches = false
    listeners.forEach(listener => listener({ matches: false } as MediaQueryListEvent))
    await nextTick()
    expect(JSON.parse(wrapper.text()).animation.duration).toBe(180)
    wrapper.unmount()
    expect(listeners.size).toBe(0)
  })
})
