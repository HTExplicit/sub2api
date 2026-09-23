import { afterEach, expect, it, vi } from 'vitest'
import { createPluginPresentation } from '@sub2api/plugin-ui/presentation'
import { createPluginSizing } from '@sub2api/plugin-ui/sizing'

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); document.body.replaceChildren(); document.documentElement.removeAttribute('style') })

it('reuses stylesheet nodes and theme variables across repeated data-only context updates', () => {
  const presentation = createPluginPresentation(document, vi.fn())
  const context = { theme: 'dark', theme_tokens: { '--ui-font': 'fixture-font' }, theme_stylesheets: [`/api/v1/settings/plugins/7/theme/${'a'.repeat(64)}/assets/theme.css`] }
  presentation.update(context)
  const link = document.querySelector<HTMLLinkElement>('link[data-plugin-theme]')!
  link.dispatchEvent(new Event('load'))
  const set = vi.spyOn(document.documentElement.style, 'setProperty')
  const remove = vi.spyOn(document.documentElement.style, 'removeProperty')
  for (let i = 0; i < 4; i++) presentation.update({ ...context })
  expect(document.querySelector('link[data-plugin-theme]')).toBe(link)
  expect(set).not.toHaveBeenCalled()
  expect(remove).not.toHaveBeenCalled()
  presentation.update({ ...context, theme_tokens: {} })
  expect(remove).toHaveBeenCalledWith('--ui-font')
  presentation.dispose()
})

it('coalesces measurement, ignores scroll offsets, and shrinks after an overlay closes', () => {
  let resize!: () => void
  let mutate!: () => void
  const unobserve = vi.fn()
  vi.stubGlobal('ResizeObserver', class { constructor(callback: () => void) { resize = callback } observe() {} unobserve = unobserve; disconnect() {} })
  vi.stubGlobal('MutationObserver', class { constructor(callback: () => void) { mutate = callback } observe() {} disconnect() {} })
  const frames = new Map<number, FrameRequestCallback>()
  let sequence = 0
  vi.spyOn(window, 'requestAnimationFrame').mockImplementation(callback => { frames.set(++sequence, callback); return sequence })
  vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(id => { frames.delete(id) })
  function paint() { const callbacks = [...frames.values()]; frames.clear(); callbacks.forEach(callback => callback(0)) }
  const root = document.createElement('div')
  document.body.append(root)
  let scroll = 0
  vi.spyOn(window, 'scrollY', 'get').mockImplementation(() => scroll)
  vi.spyOn(root, 'getBoundingClientRect').mockImplementation(() => new DOMRect(0, -scroll, 600, 100))
  Object.defineProperties(root, { offsetHeight: { value: 100 }, scrollHeight: { value: 100 } })
  const send = vi.fn()
  const sizing = createPluginSizing(root, send, () => true)
  resize(); mutate(); resize()
  expect(frames.size).toBe(1)
  paint()
  expect(send).toHaveBeenLastCalledWith(100)
  scroll = 30
  resize(); mutate(); paint()
  expect(send).toHaveBeenCalledTimes(1)
  const dialog = document.createElement('div')
  dialog.setAttribute('role', 'dialog')
  dialog.innerHTML = '<div class="modal-content"></div>'
  const panel = dialog.firstElementChild as HTMLElement
  vi.spyOn(panel, 'getBoundingClientRect').mockReturnValue(new DOMRect(0, 0, 500, 350))
  Object.defineProperty(panel, 'scrollHeight', { value: 350 })
  document.body.append(dialog)
  mutate(); paint()
  expect(send).toHaveBeenLastCalledWith(382)
  dialog.remove(); mutate(); paint()
  expect(send).toHaveBeenLastCalledWith(100)
  expect(unobserve).toHaveBeenCalledWith(panel)
  resize(); sizing.dispose(); paint()
  expect(send).toHaveBeenCalledTimes(3)
})
