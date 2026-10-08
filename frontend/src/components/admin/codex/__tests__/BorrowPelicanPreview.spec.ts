import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import BorrowPelicanPreview from '../BorrowPelicanPreview.vue'

vi.mock('@/api/client', () => ({ buildApiUrl: (path: string) => `/api/v1${path}` }))

const previewUrl = `/api/v1/codex-gateway-borrow/preview/${'a'.repeat(43)}/index.html`

afterEach(() => { vi.unstubAllGlobals() })

describe('Borrow pelican isolated preview', () => {
  it('accepts the independent pelican capability route with the same opaque sandbox', () => {
    const standaloneUrl = `/api/v1/pelican-tests/preview/${'b'.repeat(43)}/index.html`
    const wrapper = mount(BorrowPelicanPreview, { props: { previewUrl: standaloneUrl, title: 'Standalone output' } })
    expect(wrapper.get('iframe').attributes('src')).toBe(new URL(standaloneUrl, window.location.origin).href)
    expect(wrapper.get('iframe').attributes('sandbox')).toBe('allow-scripts')
    wrapper.unmount()
  })

  it('loads only a server capability with an opaque sandbox and a stable scaled viewport', async () => {
    const disconnect = vi.fn()
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect = disconnect })
    const wrapper = mount(BorrowPelicanPreview, { props: { previewUrl, title: 'Target #2 · Sol · high' } })
    const frame = wrapper.get('iframe')
    expect(frame.attributes('src')).toBe(new URL(previewUrl, window.location.origin).href)
    expect(frame.attributes('sandbox')).toBe('allow-scripts')
    expect(frame.attributes('referrerpolicy')).toBe('no-referrer')
    expect(frame.attributes('srcdoc')).toBeUndefined()
    expect(frame.attributes('tabindex')).toBe('-1')
    expect(frame.attributes('title')).toBe('Target #2 · Sol · high')
    expect(frame.attributes('style')).toContain('width: 1024px; height: 768px;')
    Object.defineProperty(wrapper.element, 'clientWidth', { value: 320 })
    Object.defineProperty(wrapper.element, 'clientHeight', { value: 240 })
    window.dispatchEvent(new Event('resize'))
    await wrapper.vm.$nextTick()
    expect(frame.attributes('style')).toContain('scale(0.3125)')
    await wrapper.setProps({ interactive: true })
    expect(frame.attributes('tabindex')).toBeUndefined()
    expect(frame.attributes('style')).toContain('pointer-events: auto')
    wrapper.unmount()
    expect(disconnect).toHaveBeenCalledOnce()
  })

  it.each([
    'javascript:alert(1)', 'data:text/html,<script>1</script>', 'blob:http://localhost/one',
    `https://other.example${previewUrl}`, '/api/v1/admin/accounts', `${previewUrl}?auth_token=secret`, `${previewUrl}#fragment`
  ])('does not execute a non-preview URL: %s', value => {
    const wrapper = mount(BorrowPelicanPreview, { props: { previewUrl: value, title: 'Output' } })
    expect(wrapper.find('iframe').exists()).toBe(false)
    wrapper.unmount()
  })
})
