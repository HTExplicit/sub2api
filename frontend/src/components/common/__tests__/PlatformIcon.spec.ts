import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import PlatformIcon from '../PlatformIcon.vue'

describe('PlatformIcon', () => {
  it('renders the Cindy mark instead of the generic fallback', () => {
    const wrapper = mount(PlatformIcon, { props: { platform: 'cindy' } })
    expect(wrapper.find('circle').exists()).toBe(true)
    expect(wrapper.find('path').attributes('d')).toContain('15.5 8.5')
  })
})
