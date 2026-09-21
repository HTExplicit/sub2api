// @vitest-environment node
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import { afterEach, expect, it, vi } from 'vitest'

const source = readFileSync(new URL('../../../../../backend/pkg/extensionapi/ui/bridge.js', import.meta.url), 'utf8')

function fixture() {
  const parent = { postMessage: vi.fn() }
  const window = { addEventListener: vi.fn(), removeEventListener: vi.fn() }
  const scope = { parent, window, location: { hash: '#bridge_token=fixture-token' }, URLSearchParams, DOMException, setTimeout, clearTimeout }
  const Bridge = runInNewContext(`${source}\nSub2APIPluginBridge`, scope)
  return { bridge: new Bridge(), parent, window }
}

afterEach(() => vi.useRealTimers())

it('removes the request abort listener after a timeout, before a later dispose', async () => {
  vi.useFakeTimers()
  const { bridge } = fixture()
  const controller = new AbortController()
  const remove = vi.spyOn(controller.signal, 'removeEventListener')
  const result = bridge.resource('accounts.fixture', {}, controller.signal).catch((error: Error) => error.message)

  await vi.advanceTimersByTimeAsync(30000)

  expect(await result).toBe('Operation timed out')
  expect(bridge.pending.size).toBe(0)
  expect(remove).toHaveBeenCalledTimes(1)
  expect(remove).toHaveBeenCalledWith('abort', expect.any(Function))
  bridge.dispose()
  expect(remove).toHaveBeenCalledTimes(1)
})
