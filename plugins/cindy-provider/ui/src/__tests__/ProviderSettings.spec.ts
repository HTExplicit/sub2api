import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'

const source = readFileSync(resolve(dirname(fileURLToPath(import.meta.url)), '../../assets/app.js'), 'utf8')
const initial = { balance_detection: false, catalog_enabled: true, search_enabled: true }
let contextListener: (value: { available: boolean; theme: string; locale: string }) => void
const bridge = {
  context: vi.fn(), config: vi.fn(), save: vi.fn(), invoke: vi.fn(),
  onContextChange: vi.fn(), dispose: vi.fn(), resize: vi.fn(),
}

async function render() {
  document.body.innerHTML = '<main id="app"></main>'
  new Function('Sub2APIPluginBridge', 'ResizeObserver', source)(
    function () { return bridge }, class { observe() {} disconnect() {} },
  )
  await flushPromises()
}

describe('Cindy provider settings', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    bridge.context.mockResolvedValue({ available: true, theme: 'light', locale: 'en' })
    bridge.config.mockResolvedValue({ ...initial })
    bridge.invoke.mockResolvedValue([])
    bridge.onContextChange.mockImplementation(listener => { contextListener = listener; return () => {} })
  })
  afterEach(() => {
    window.dispatchEvent(new Event('pagehide'))
    document.body.innerHTML = ''
  })

  it('preserves checkbox edits made during a settings save', async () => {
    let finish!: (value: typeof initial) => void
    bridge.save.mockReturnValueOnce(new Promise(resolve => { finish = resolve }))
    await render()
    const input = document.querySelector<HTMLInputElement>('input[type=checkbox]')!
    input.checked = true
    input.dispatchEvent(new Event('change'))
    document.querySelector<HTMLButtonElement>('button')!.click()
    input.checked = false
    input.dispatchEvent(new Event('change'))
    finish({ ...initial, balance_detection: true })
    await flushPromises()
    expect(input.checked).toBe(false)
    expect(document.querySelector('[role=status]')!.textContent).toContain('unsaved')
  })

  it('tracks failure and theme context without discarding settings inputs', async () => {
    await render()
    const input = document.querySelector<HTMLInputElement>('input[type=checkbox]')!
    input.checked = true
    contextListener({ available: false, theme: 'dark', locale: 'en' })
    expect(document.documentElement.dataset.theme).toBe('dark')
    expect(input.checked).toBe(true)
    expect(document.querySelector<HTMLButtonElement>('button')!.disabled).toBe(true)
    document.querySelector<HTMLButtonElement>('button')!.click()
    expect(bridge.save).not.toHaveBeenCalled()
    contextListener({ available: true, theme: 'light', locale: 'en' })
    expect(document.querySelector<HTMLButtonElement>('button')!.disabled).toBe(false)
  })

  it('does not chain a catalog refresh after settings view closes', async () => {
    let finish!: (value: typeof initial) => void
    bridge.save.mockReturnValueOnce(new Promise(resolve => { finish = resolve }))
    await render()
    document.querySelector<HTMLButtonElement>('button')!.click()
    window.dispatchEvent(new Event('pagehide'))
    finish({ ...initial })
    await flushPromises()
    expect(bridge.invoke).toHaveBeenCalledTimes(1)
  })
})
