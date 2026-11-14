import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import Select from '../Select.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

let wrapper: ReturnType<typeof mount> | undefined
let nextFrame: FrameRequestCallback | undefined
let requestFrame: ReturnType<typeof vi.spyOn>
let cancelFrame: ReturnType<typeof vi.spyOn>
let measureAnchor: ReturnType<typeof vi.spyOn>

const rectAt = (top: number): DOMRect => ({
  x: 20, y: top, top, bottom: top + 40, left: 20, right: 180, width: 160, height: 40,
  toJSON: () => ({})
}) as DOMRect

beforeEach(() => {
  nextFrame = undefined
  requestFrame = vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
    nextFrame = callback
    return 23
  })
  cancelFrame = vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => {
    nextFrame = undefined
  })
  measureAnchor = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue(rectAt(20))
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  document.body.innerHTML = ''
  vi.restoreAllMocks()
})

async function open() {
  wrapper = mount(Select, {
    attachTo: document.body,
    props: { modelValue: null, options: [{ value: 'one', label: 'One' }] },
    global: { stubs: { transition: { inheritAttrs: false, template: '<slot />' } } }
  })
  await wrapper.get('button').trigger('click')
  await nextTick()
  measureAnchor.mockClear()
  return document.querySelector<HTMLElement>('.select-dropdown-portal')!
}

async function flushPositionFrame() {
  const callback = nextFrame
  nextFrame = undefined
  callback?.(16)
  await nextTick()
}

describe('Select anchored position updates', () => {
  it('coalesces external scroll and resize into one geometry read per frame', async () => {
    const dropdown = await open()
    measureAnchor.mockReturnValue(rectAt(80))

    document.body.dispatchEvent(new Event('scroll'))
    document.body.dispatchEvent(new Event('scroll'))
    window.dispatchEvent(new Event('resize'))

    expect(requestFrame).toHaveBeenCalledTimes(1)
    expect(measureAnchor).not.toHaveBeenCalled()
    await flushPositionFrame()
    expect(measureAnchor).toHaveBeenCalledTimes(1)
    expect(dropdown.style.top).toBe('124px')

    window.dispatchEvent(new Event('resize'))
    await flushPositionFrame()
    expect(measureAnchor).toHaveBeenCalledTimes(2)
  })

  it('ignores scroll events from the menu and its options', async () => {
    const dropdown = await open()

    dropdown.dispatchEvent(new Event('scroll'))
    dropdown.querySelector('.select-options')!.dispatchEvent(new Event('scroll'))

    expect(requestFrame).not.toHaveBeenCalled()
    expect(measureAnchor).not.toHaveBeenCalled()
  })

  it('flips above the trigger when resize leaves insufficient space below', async () => {
    const dropdown = await open()
    Object.defineProperty(dropdown, 'offsetHeight', { configurable: true, value: 240 })
    measureAnchor.mockReturnValue(rectAt(window.innerHeight - 80))

    window.dispatchEvent(new Event('resize'))
    await flushPositionFrame()

    expect(dropdown.style.bottom).toBe('84px')
    expect(dropdown.style.top).toBe('')
  })

  it('cancels a pending position update when closed and restores keyboard focus', async () => {
    const dropdown = await open()
    window.dispatchEvent(new Event('resize'))
    dropdown.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await nextTick()

    expect(cancelFrame).toHaveBeenCalledWith(23)
    expect(nextFrame).toBeUndefined()
    expect(wrapper!.get('button').attributes('aria-expanded')).toBe('false')
    expect(document.activeElement).toBe(wrapper!.get('button').element)
    expect(measureAnchor).not.toHaveBeenCalled()
  })

  it('cancels a pending position update on unmount', async () => {
    await open()
    window.dispatchEvent(new Event('resize'))
    wrapper!.unmount()
    wrapper = undefined

    expect(cancelFrame).toHaveBeenCalledWith(23)
    expect(nextFrame).toBeUndefined()
  })
})
