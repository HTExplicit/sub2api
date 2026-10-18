import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import PelicanPreview from '../PelicanPreview.vue'

vi.mock('@/api/client', () => ({ buildApiUrl: (path: string) => `/api/v1${path}` }))

const previewUrl = `/api/v1/pelican-tests/preview/${'a'.repeat(43)}/index.html`

afterEach(() => { vi.unstubAllGlobals() })

describe('Pelican isolated preview', () => {
  it('plays a new SVG capability as an image without an executable iframe', () => {
    const svgUrl = previewUrl.replace('index.html', 'scene.svg')
    const wrapper = mount(PelicanPreview, { props: { previewUrl: svgUrl, title: 'SVG work' } })
    expect(wrapper.find('iframe').exists()).toBe(false)
    expect(wrapper.get('img').attributes('src')).toBe(new URL(svgUrl, window.location.origin).href)
    wrapper.unmount()
  })
  it('accepts the independent pelican capability route with the same opaque sandbox', () => {
    const standaloneUrl = `/api/v1/pelican-tests/preview/${'b'.repeat(43)}/index.html`
    const wrapper = mount(PelicanPreview, { props: { previewUrl: standaloneUrl, title: 'Standalone output' } })
    expect(wrapper.get('iframe').attributes('src')).toBe(new URL(standaloneUrl, window.location.origin).href)
    expect(wrapper.get('iframe').attributes('sandbox')).toBe('allow-scripts')
    wrapper.unmount()
  })

  it('loads only a server capability with an opaque sandbox and a stable scaled viewport', async () => {
    const disconnect = vi.fn()
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect = disconnect })
    const wrapper = mount(PelicanPreview, { props: { previewUrl, title: 'Target #2 · Sol · high' } })
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
    `/api/v1/codex-gateway-borrow/preview/${'a'.repeat(43)}/index.html`,
    'javascript:alert(1)', 'data:text/html,<script>1</script>', 'blob:http://localhost/one',
    `https://other.example${previewUrl}`, '/api/v1/admin/accounts', `${previewUrl}?auth_token=secret`, `${previewUrl}#fragment`
  ])('does not execute a non-preview URL: %s', value => {
    const wrapper = mount(PelicanPreview, { props: { previewUrl: value, title: 'Output' } })
    expect(wrapper.find('iframe').exists()).toBe(false)
    wrapper.unmount()
  })
})


it('keeps HTML and SVG animations mounted when the same result gets a new capability', async () => {
  for (const asset of ['index.html', 'scene.svg']) {
    const original = previewUrl.replace('index.html', asset)
    const renewed = original.replace('a'.repeat(43), 'b'.repeat(43))
    const wrapper = mount(PelicanPreview, { props: { previewUrl: original, resultId: 'work-1', title: 'Work' } })
    const selector = asset === 'index.html' ? 'iframe' : 'img'
    const animation = wrapper.get(selector).element
    await wrapper.setProps({ previewUrl: renewed })
    expect(wrapper.get(selector).element).toBe(animation)
    expect(wrapper.get(selector).attributes('src')).toBe(new URL(original, window.location.origin).href)
    await wrapper.setProps({ resultId: 'work-2' })
    expect(wrapper.get(selector).attributes('src')).toBe(new URL(renewed, window.location.origin).href)
    wrapper.unmount()
    const reopened = mount(PelicanPreview, { props: { previewUrl: renewed, resultId: 'work-1', title: 'Work' } })
    expect(reopened.get(selector).attributes('src')).toBe(new URL(renewed, window.location.origin).href)
    reopened.unmount()
  }
})

it('removes a mounted animation when its capability is withdrawn or becomes unsafe', async () => {
  const wrapper = mount(PelicanPreview, { props: { previewUrl, resultId: 'work-1', title: 'Work' } })
  await wrapper.setProps({ previewUrl: 'https://other.example/work.html' })
  expect(wrapper.find('iframe').exists()).toBe(false)
  await wrapper.setProps({ previewUrl })
  expect(wrapper.find('iframe').exists()).toBe(true)
  await wrapper.setProps({ previewUrl: '' })
  expect(wrapper.find('iframe').exists()).toBe(false)
  wrapper.unmount()
})
