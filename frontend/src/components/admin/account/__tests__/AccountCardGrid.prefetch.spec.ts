import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import AccountCardGrid from '../AccountCardGrid.vue'
import type { Account } from '@/types'

vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key }),
}))

const accounts = Array.from({ length: 1000 }, (_, index) => ({
  id: index + 1, name: `account-${index + 1}`, platform: 'openai', type: 'oauth',
  groups: [], tags: [],
}) as Account)
const sharedProps = {
  accounts, loading: false, selectedIds: [], todayStats: {}, todayStatsLoading: false,
  todayStatsError: false, todayStatsUpdatedAt: null, manualRefreshToken: 0, statusNow: 0,
}
const stubs = {
  AccountUsageCell: true, AccountCapacityCell: true, AccountStatusIndicator: true,
  AccountIdentityBadges: true, AccountSelectionCheckbox: true, AccountGroupsCell: true, Icon: true,
}

let wrapper: VueWrapper | undefined
let scrollToDescriptor: PropertyDescriptor | undefined

beforeEach(() => {
  vi.stubGlobal('innerHeight', 720)
  vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockImplementation(function () {
    return this.hasAttribute('data-account-virtual-index') ? 360 : 400
  })
  vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockReturnValue(1200)
  scrollToDescriptor = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollTo')
  Object.defineProperty(HTMLElement.prototype, 'scrollTo', { configurable: true, value: vi.fn() })
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  if (scrollToDescriptor) Object.defineProperty(HTMLElement.prototype, 'scrollTo', scrollToDescriptor)
  else Reflect.deleteProperty(HTMLElement.prototype, 'scrollTo')
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

function mockViewport(initialWidth: number) {
  let width = initialWidth
  vi.stubGlobal('innerWidth', width)
  const queries: Array<{ media: string; matches: boolean; listeners: Set<(event: MediaQueryListEvent) => void> }> = []
  const matches = (query: string) => {
    const minimum = /min-width:\s*(\d+)px/.exec(query)
    const maximum = /max-width:\s*(\d+)px/.exec(query)
    return minimum ? width >= Number(minimum[1]) : maximum ? width <= Number(maximum[1]) : false
  }
  vi.spyOn(window, 'matchMedia').mockImplementation(query => {
    const entry = { media: query, matches: matches(query), listeners: new Set<(event: MediaQueryListEvent) => void>() }
    queries.push(entry)
    return {
      get matches() { return entry.matches }, media: query,
      addEventListener: (_type: string, listener: (event: MediaQueryListEvent) => void) => entry.listeners.add(listener),
      removeEventListener: (_type: string, listener: (event: MediaQueryListEvent) => void) => entry.listeners.delete(listener),
    } as unknown as MediaQueryList
  })
  return async (nextWidth: number) => {
    width = nextWidth
    vi.stubGlobal('innerWidth', width)
    for (const entry of queries) {
      const next = matches(entry.media)
      if (next === entry.matches) continue
      entry.matches = next
      for (const listener of entry.listeners) listener({ matches: next, media: entry.media } as MediaQueryListEvent)
    }
    window.dispatchEvent(new Event('resize'))
    await nextTick()
    await flushPromises()
  }
}

const visibleIds = () => wrapper!.findAll('[data-account-id]')
  .map(row => Number(row.attributes('data-account-id'))).sort((a, b) => a - b)
const targetIds = () => {
  const ids = visibleIds()
  return [
    ...accounts.slice(Math.max(0, ids[0] - 1 - ids.length), ids[0] - 1),
    ...accounts.slice(ids[ids.length - 1], ids[ids.length - 1] + ids.length),
  ].map(account => account.id)
}
const requestedIds = (request: ReturnType<typeof vi.fn>) => request.mock.calls.map(([account]) => account.id as number)
const scroll = async (top: number) => {
  wrapper!.element.scrollTop = top
  await wrapper!.trigger('scroll')
  await flushPromises()
}

describe('card account usage prefetch', () => {
  it('prepares only adjacent account windows and does not retry retained failures while scrolling', async () => {
    mockViewport(1600)
    const requestBatchedUsage = vi.fn()
    wrapper = mount(AccountCardGrid, {
      attachTo: document.body, props: { ...sharedProps, requestBatchedUsage }, global: { stubs },
    })
    await flushPromises()
    const initial = visibleIds()
    expect(initial.length).toBeGreaterThan(0)
    expect(initial.length).toBeLessThan(30)
    expect(requestedIds(requestBatchedUsage)).toEqual(targetIds())
    expect(requestedIds(requestBatchedUsage)).toEqual(accounts.slice(initial.length, 2 * initial.length).map(account => account.id))
    expect(requestBatchedUsage.mock.calls.every(call => call.length === 1)).toBe(true)

    requestBatchedUsage.mockClear()
    await scroll(20_000)
    const visible = visibleIds()
    let previousTargets = targetIds()
    expect(requestedIds(requestBatchedUsage)).toEqual(previousTargets)
    expect(previousTargets.length).toBe(2 * visible.length)
    expect(visible.every(id => !previousTargets.includes(id))).toBe(true)
    const failedTarget = visible[visible.length - 1] + Math.floor(visible.length / 2)
    await wrapper.setProps({ batchedUsageErrors: { [failedTarget]: 'failed' } })
    for (const top of [20_372, 20_744]) {
      requestBatchedUsage.mockClear()
      await scroll(top)
      const nextTargets = targetIds()
      expect(requestedIds(requestBatchedUsage)).toEqual(nextTargets.filter(id => !previousTargets.includes(id)))
      expect(requestedIds(requestBatchedUsage)).not.toContain(failedTarget)
      expect(requestBatchedUsage.mock.calls.every(call => call.length === 1)).toBe(true)
      previousTargets = nextTargets
    }

    requestBatchedUsage.mockClear()
    await wrapper.setProps({ accounts: accounts.map(account => ({ ...account, name: `${account.name}-updated` })) })
    expect(requestedIds(requestBatchedUsage)).toEqual(targetIds())
    const replacementRequest = vi.fn()
    await wrapper.setProps({ requestBatchedUsage: replacementRequest })
    expect(requestedIds(replacementRequest)).toEqual(targetIds())
  })

  it('maps two and three column windows to account bounds, including the partial final row', async () => {
    const resize = mockViewport(1200)
    const requestBatchedUsage = vi.fn()
    wrapper = mount(AccountCardGrid, {
      attachTo: document.body, props: { ...sharedProps, requestBatchedUsage }, global: { stubs },
    })
    await flushPromises()
    expect(wrapper.get('[data-account-virtual-index="0"]').findAll('article')).toHaveLength(2)
    let previousTargets = targetIds()
    expect(requestedIds(requestBatchedUsage)).toEqual(previousTargets)

    requestBatchedUsage.mockClear()
    await resize(1600)
    expect(wrapper.get('[data-account-virtual-index="0"]').findAll('article')).toHaveLength(3)
    const threeColumnTargets = targetIds()
    expect(requestedIds(requestBatchedUsage)).toEqual(threeColumnTargets.filter(id => !previousTargets.includes(id)))
    previousTargets = threeColumnTargets

    requestBatchedUsage.mockClear()
    await scroll(1_000_000)
    expect(visibleIds()).toContain(1000)
    expect(wrapper.get('[data-account-virtual-index="333"]').findAll('article')).toHaveLength(1)
    expect(requestedIds(requestBatchedUsage)).toEqual(targetIds().filter(id => !previousTargets.includes(id)))
    expect(requestedIds(requestBatchedUsage).every(id => id >= 1 && id <= 1000)).toBe(true)
    expect(requestBatchedUsage.mock.calls.every(call => call.length === 1)).toBe(true)
  })

  it.each(['small', 'mobile', 'missing callback'] as const)('does not prefetch on the %s path', async mode => {
    mockViewport(mode === 'mobile' ? 800 : 1600)
    const requestBatchedUsage = vi.fn()
    wrapper = mount(AccountCardGrid, {
      attachTo: document.body,
      props: {
        ...sharedProps, accounts: mode === 'small' ? accounts.slice(0, 50) : accounts,
        requestBatchedUsage: mode === 'missing callback' ? undefined : requestBatchedUsage,
      },
      global: { stubs },
    })
    await flushPromises()
    await scroll(5600)
    expect(requestBatchedUsage).not.toHaveBeenCalled()
    expect(wrapper.findAll('[data-account-id]').length).toBeGreaterThan(0)
  })
})
