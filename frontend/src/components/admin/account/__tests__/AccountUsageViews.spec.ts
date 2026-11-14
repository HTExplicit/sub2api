import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent, nextTick, onMounted, type PropType } from 'vue'
import { createPinia, disposePinia, type Pinia } from 'pinia'

import AccountCardGrid from '../AccountCardGrid.vue'
import AccountCompactList from '../AccountCompactList.vue'
import type { Account, AccountUsageInfo, WindowStats } from '@/types'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
      locale: { value: 'en' }
    })
  }
})

const account: Account = {
  id: 71,
  name: 'visible-usage',
  platform: 'openai',
  type: 'apikey',
  proxy_id: null,
  concurrency: 10,
  priority: 0,
  status: 'active',
  error_message: null,
  last_used_at: null,
  expires_at: null,
  auto_pause_on_expired: true,
  created_at: '2026-07-29T00:00:00Z',
  updated_at: '2026-07-29T00:00:00Z',
  schedulable: true,
  rate_limited_at: null,
  rate_limit_reset_at: null,
  overload_until: null,
  temp_unschedulable_until: null,
  temp_unschedulable_reason: null,
  session_window_start: null,
  session_window_end: null,
  session_window_status: null,
  groups: [],
  tags: []
}

const stats: Record<string, WindowStats> = {
  '71': {
    requests: 9,
    tokens: 12_000,
    cost: 0.75,
    standard_cost: 0.75,
    user_cost: 1.25
  }
}

const AccountUsageCellStub = defineComponent({
  props: {
    account: Object,
    todayStats: Object,
    todayStatsLoading: Boolean,
    todayStatsError: Boolean,
    todayStatsUpdatedAt: Number,
    manualRefreshToken: Number,
    statusNow: Number,
    variant: String,
    readOnly: Boolean,
    batchedUsage: Object,
    batchedUsageError: String,
    batchedUsageLoading: Boolean,
    requestBatchedUsage: Function as PropType<(account: Account) => void>,
  },
  setup(props) {
    onMounted(() => props.requestBatchedUsage?.(props.account as Account))
  },
  template: `
    <div
      data-test="usage-cell"
      :data-variant="variant"
      :data-read-only="String(readOnly)"
      :data-requests="String(todayStats?.requests ?? -1)"
      :data-loading="String(todayStatsLoading)"
      :data-error="String(todayStatsError)"
      :data-refresh-token="String(manualRefreshToken)"
    />
  `
})

const AccountCapacityCellStub = {
  props: { compact: Boolean },
  template: '<div data-test="capacity-cell" :data-compact="String(compact)" />'
}

const globalStubs = {
  AccountUsageCell: AccountUsageCellStub,
  AccountCapacityCell: AccountCapacityCellStub,
  AccountStatusIndicator: { template: '<div data-test="status" />' },
  PlatformTypeBadge: { template: '<div data-test="platform" />' },
  Icon: true
}

const sharedProps = {
  accounts: [account],
  loading: false,
  selectedIds: [],
  todayStats: stats,
  todayStatsLoading: false,
  todayStatsError: true,
  todayStatsUpdatedAt: 123,
  manualRefreshToken: 4,
  statusNow: 456
}

let pinia: Pinia
const mounted: VueWrapper[] = []
beforeEach(() => {
  pinia = createPinia()
  mockViewport(1600)
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})
afterEach(() => {
  mounted.splice(0).forEach(wrapper => wrapper.unmount())
  disposePinia(pinia)
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

const largeAccounts = Array.from({ length: 1000 }, (_, index) => ({ ...account, id: index + 1, name: `account-${index + 1}` }))

function mockListLayout() {
  vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockImplementation(function () {
    return this.hasAttribute('data-account-virtual-index') ? (this.dataset.accountVirtualIndex === '0' ? 224 : 112) : 400
  })
  vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockReturnValue(1200)
  const descriptor = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollTo')
  Object.defineProperty(HTMLElement.prototype, 'scrollTo', { configurable: true, value: vi.fn() })
  return () => {
    if (descriptor) Object.defineProperty(HTMLElement.prototype, 'scrollTo', descriptor)
    else Reflect.deleteProperty(HTMLElement.prototype, 'scrollTo')
  }
}

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
      get matches() { return entry.matches },
      media: query,
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

describe('account console usage views', () => {
  it('passes compact usage inputs and keeps usage below account information on mobile', () => {
    const wrapper = mount(AccountCompactList, {
      props: sharedProps,
      global: { plugins: [pinia], stubs: globalStubs }
    })
    mounted.push(wrapper)

    const usage = wrapper.get('[data-test="account-compact-usage"]')
    const cell = usage.get('[data-test="usage-cell"]')
    expect(cell.attributes('data-variant')).toBe('compact')
    expect(cell.attributes('data-read-only')).toBe('true')
    expect(cell.attributes('data-requests')).toBe('9')
    expect(cell.attributes('data-error')).toBe('true')
    expect(cell.attributes('data-refresh-token')).toBe('4')
    expect(usage.classes()).toContain('row-start-2')
    expect(usage.get('[data-test="capacity-cell"]').attributes('data-compact')).toBe('true')
  })

  it('places full list usage before taxonomy in cards and forwards refresh state', () => {
    const wrapper = mount(AccountCardGrid, {
      props: sharedProps,
      global: { plugins: [pinia], stubs: globalStubs }
    })
    mounted.push(wrapper)

    const usage = wrapper.get('[data-test="account-card-usage"]')
    const taxonomy = wrapper.get('[data-test="account-card-taxonomy"]')
    const cell = usage.get('[data-test="usage-cell"]')
    expect(cell.attributes('data-variant')).toBe('list')
    expect(cell.attributes('data-read-only')).toBe('true')
    expect(cell.attributes('data-loading')).toBe('false')
    expect(cell.attributes('data-refresh-token')).toBe('4')
    expect(
      usage.element.compareDocumentPosition(taxonomy.element) & Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy()
  })

  it.each([AccountCompactList, AccountCardGrid])('forwards parent batch state and keeps loaded rows while refreshing', async (component) => {
    const usage = { fetched_at: '2026-11-14T00:00:00Z' } as AccountUsageInfo
    const requestBatchedUsage = vi.fn()
    const wrapper = mount(component, {
      props: { ...sharedProps, loading: true, batchedUsage: { '71': usage }, batchedUsageErrors: { '71': 'stale' }, batchedUsageLoading: { '71': true }, requestBatchedUsage },
      global: { plugins: [pinia], stubs: globalStubs },
    })
    mounted.push(wrapper)
    await wrapper.setProps({ loading: false })
    const cell = wrapper.findComponent(AccountUsageCellStub)
    expect(cell.props('batchedUsage')).toEqual(usage)
    expect(cell.props('batchedUsageError')).toBe('stale')
    expect(cell.props('batchedUsageLoading')).toBe(true)
    expect(requestBatchedUsage).toHaveBeenCalledTimes(1)
    expect(requestBatchedUsage).toHaveBeenCalledWith(account)
    await wrapper.setProps({ todayStatsLoading: true })
    expect(wrapper.find('[data-account-id="71"]').exists()).toBe(true)
    expect(wrapper.findComponent(AccountUsageCellStub).element).toBe(cell.element)
  })

  it('windows 1000 compact rows, measures variable heights, and requests only mounted accounts', async () => {
    const restoreScrollTo = mockListLayout()
    const requestBatchedUsage = vi.fn()
    try {
      const wrapper = mount(AccountCompactList, {
        attachTo: document.body,
        props: { ...sharedProps, accounts: largeAccounts, selectedIds: [999], requestBatchedUsage },
        global: { plugins: [pinia], stubs: globalStubs },
      })
      mounted.push(wrapper)
      await flushPromises()
      const rows = wrapper.findAll('[data-account-id]')
      const spacer = wrapper.get('[data-account-virtual-spacer]').element as HTMLElement
      const canvas = spacer.parentElement!
      expect(canvas.style.height).toBe('')
      expect(canvas.style.position).toBe('relative')
      expect(spacer.children).toHaveLength(0)
      expect(spacer.getAttribute('aria-hidden')).toBe('true')
      expect(spacer.hasAttribute('role')).toBe(false)
      expect(Number.parseFloat(spacer.style.height)).toBeGreaterThan(100_000)
      expect(rows.length).toBeGreaterThan(0)
      expect(rows.length).toBeLessThan(25)
      const requestedIds = requestBatchedUsage.mock.calls.map(([row]) => row.id)
      // The first measured row is taller than the estimate, so the initial window contracts by one item.
      expect(requestedIds.length).toBeLessThan(25)
      expect(rows.every(row => requestedIds.includes(Number(row.attributes('data-account-id'))))).toBe(true)
      expect((wrapper.get('[data-account-virtual-index="1"]').element.parentElement as HTMLElement).style.transform).toBe('translateY(224px)')

      wrapper.element.scrollTop = 112_000
      await wrapper.trigger('scroll')
      await flushPromises()
      expect(wrapper.find('[data-account-id="1000"]').exists()).toBe(true)
      expect(wrapper.get('[data-account-virtual-spacer]').element).toBe(spacer)
      expect((wrapper.get('[data-account-id="999"] input').element as HTMLInputElement).checked).toBe(true)
      await wrapper.get('[data-account-id="999"] input').setValue(false)
      expect(wrapper.emitted('toggle')).toEqual([[999]])
      expect(wrapper.findAll('[data-account-id]').length).toBeLessThan(25)
    } finally {
      restoreScrollTo()
    }
  })

  it('regroups a 1000-card window across one, two, and three columns without losing the final account', async () => {
    const restoreScrollTo = mockListLayout()
    const resize = mockViewport(800)
    try {
      const wrapper = mount(AccountCardGrid, {
        attachTo: document.body,
        props: { ...sharedProps, accounts: largeAccounts },
        global: { plugins: [pinia], stubs: globalStubs },
      })
      mounted.push(wrapper)
      await flushPromises()
      const spacer = wrapper.get('[data-account-virtual-spacer]').element as HTMLElement
      expect(spacer.parentElement!.style.height).toBe('')
      expect(spacer.children).toHaveLength(0)
      expect(spacer.getAttribute('aria-hidden')).toBe('true')
      expect(wrapper.get('[data-account-virtual-index="0"]').findAll('article')).toHaveLength(1)
      await resize(1200)
      expect(wrapper.get('[data-account-virtual-index="0"]').findAll('article')).toHaveLength(2)
      await resize(1600)
      expect(wrapper.get('[data-account-virtual-spacer]').element).toBe(spacer)
      expect(wrapper.get('[data-account-virtual-index="0"]').findAll('article')).toHaveLength(3)
      expect(wrapper.findAll('article').length).toBeLessThan(60)

      wrapper.element.scrollTop = 1_000_000
      await wrapper.trigger('scroll')
      await flushPromises()
      expect(wrapper.find('[data-account-id="1000"]').exists()).toBe(true)
      const ids = wrapper.findAll('[data-account-id]').map(row => row.attributes('data-account-id'))
      expect(new Set(ids).size).toBe(ids.length)
      expect(wrapper.findAll('article').length).toBeLessThan(60)
    } finally {
      restoreScrollTo()
    }
  })

  it.each([AccountCompactList, AccountCardGrid])('leaves already measured rows to ResizeObserver on ordinary patches', async (component) => {
    const restoreScrollTo = mockListLayout()
    const heightRead = vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get')
    try {
      const wrapper = mount(component, {
        attachTo: document.body,
        props: { ...sharedProps, accounts: largeAccounts },
        global: { plugins: [pinia], stubs: globalStubs },
      })
      mounted.push(wrapper)
      await flushPromises()
      expect(heightRead).toHaveBeenCalled()
      heightRead.mockClear()

      await wrapper.setProps({ selectedIds: [1], todayStatsLoading: true })
      await flushPromises()
      expect(heightRead).not.toHaveBeenCalled()
      expect((wrapper.get('[data-account-id="1"] input').element as HTMLInputElement).checked).toBe(true)
    } finally {
      restoreScrollTo()
    }
  })

  it.each([AccountCompactList, AccountCardGrid])('windows the natural mobile document even when the list grows to its full content height', async (component) => {
    mockViewport(375)
    vi.stubGlobal('innerHeight', 667)
    vi.stubGlobal('scrollY', 0)
    let documentTop = 480
    vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
    vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockImplementation(function () {
      if (this.hasAttribute('data-account-virtual-index')) return 180
      if (this.hasAttribute('data-test')) return Number.parseFloat(this.querySelector<HTMLElement>('[data-account-virtual-spacer]')?.style.height || '0')
      return 0
    })
    vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockImplementation(function () {
      return this.offsetHeight
    })
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function () {
      return { top: documentTop - window.scrollY, left: 0, width: 375, height: this.offsetHeight } as DOMRect
    })
    const observerTargets = new Map<Element, ResizeObserverCallback>()
    vi.stubGlobal('ResizeObserver', class {
      constructor(private callback: ResizeObserverCallback) {}
      observe(target: Element) { observerTargets.set(target, this.callback) }
      unobserve() {}
      disconnect() {}
    })
    const wrapper = mount(component, {
      attachTo: document.body,
      props: { ...sharedProps, accounts: largeAccounts },
      global: { plugins: [pinia], stubs: globalStubs },
    })
    mounted.push(wrapper)
    await flushPromises()
    expect(window.innerHeight).toBe(667)
    expect(wrapper.element.offsetHeight).toBeGreaterThan(100_000)
    expect(wrapper.element.clientHeight).toBe(wrapper.element.offsetHeight)
    expect(wrapper.element.scrollTop).toBe(0)
    expect(wrapper.findAll('[data-account-id]').length).toBeGreaterThan(0)
    expect(wrapper.findAll('[data-account-id]').length).toBeLessThan(25)
    const firstRowPosition = () => {
      const row = wrapper.get('[data-account-virtual-index="0"]').element as HTMLElement
      return (component === AccountCompactList ? row.parentElement! : row).style.transform
    }
    expect(firstRowPosition()).toBe('translateY(0px)')

    // A wrapped filter moves the document list; the observer updates scrollMargin, not the root height.
    documentTop = 600
    observerTargets.get(wrapper.element)!([], {} as ResizeObserver)
    await flushPromises()
    expect(firstRowPosition()).toBe('translateY(0px)')

    vi.stubGlobal('scrollY', 90_000)
    window.dispatchEvent(new Event('scroll'))
    await flushPromises()
    expect(window.scrollY).toBe(90_000)
    expect(wrapper.find('[data-account-id="1"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-account-id]').length).toBeLessThan(25)
    expect(wrapper.element.scrollTop).toBe(0)

    vi.stubGlobal('scrollY', 1_000_000)
    window.dispatchEvent(new Event('scroll'))
    await flushPromises()
    expect(wrapper.find('[data-account-id="1000"]').exists()).toBe(true)
    expect(wrapper.findAll('[data-account-id]').length).toBeLessThan(25)
  })

  it('keeps small mobile lists in normal document flow', async () => {
    mockViewport(375)
    const wrapper = mount(AccountCompactList, {
      props: { ...sharedProps, accounts: largeAccounts.slice(0, 50) },
      global: { plugins: [pinia], stubs: globalStubs },
    })
    mounted.push(wrapper)
    await flushPromises()
    expect(wrapper.findAll('[data-account-id]')).toHaveLength(50)
    expect(wrapper.find('[data-account-virtual-spacer]').exists()).toBe(false)
    expect(wrapper.get('[data-account-id]').attributes('style')).toBeUndefined()
  })

  it.each([AccountCompactList, AccountCardGrid])('does not rerender or measure a window when native scroll stays in the same row range', async (component) => {
    const restoreScrollTo = mockListLayout()
    const heightRead = vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get')
    const rowWindowUpdated = vi.fn()
    let rootElement: Element
    try {
      const wrapper = mount(component, {
        attachTo: document.body,
        props: { ...sharedProps, accounts: largeAccounts },
        global: {
          plugins: [pinia],
          stubs: globalStubs,
          mixins: [{ updated() { if (this.$el === rootElement) rowWindowUpdated() } }],
        },
      })
      rootElement = wrapper.element
      mounted.push(wrapper)
      await flushPromises()
      const visibleIds = wrapper.findAll('[data-account-id]').map(row => row.attributes('data-account-id'))
      rowWindowUpdated.mockClear()
      heightRead.mockClear()
      for (const top of [1, 2, 3]) {
        wrapper.element.scrollTop = top
        await wrapper.trigger('scroll')
        await flushPromises()
      }
      expect(wrapper.findAll('[data-account-id]').map(row => row.attributes('data-account-id'))).toEqual(visibleIds)
      expect(rowWindowUpdated).not.toHaveBeenCalled()
      expect(heightRead).not.toHaveBeenCalled()
    } finally {
      restoreScrollTo()
    }
  })
})
