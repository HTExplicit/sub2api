import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent, nextTick, type PropType } from 'vue'
import type { Virtualizer } from '@tanstack/vue-virtual'
import AccountCardGrid from '../AccountCardGrid.vue'
import type { Account } from '@/types'

const { instances } = vi.hoisted(() => ({ instances: [] as Virtualizer<HTMLElement, Element>[] }))
vi.mock('@tanstack/vue-virtual', async importOriginal => {
  const actual = await importOriginal<typeof import('@tanstack/vue-virtual')>()
  return {
    ...actual,
    useVirtualizer: (...args: Parameters<typeof actual.useVirtualizer>) => {
      const result = actual.useVirtualizer(...args)
      instances.push(result.value as Virtualizer<HTMLElement, Element>)
      return result
    },
  }
})
vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key, locale: { value: 'en' } }),
}))

const accounts = Array.from({ length: 1000 }, (_, index) => ({
  id: index + 1, name: `account-${index + 1}`, platform: 'openai', type: 'oauth',
  status: 'active', concurrency: 10, groups: [], tags: [], extra: {},
}) as Account)
const Usage = defineComponent({
  props: { account: { type: Object as PropType<Account>, required: true } },
  setup(props) { return { mountedAccount: props.account.id } },
  template: '<div data-test="usage" :data-mounted-account="mountedAccount" :data-account="account.id" />',
})
const Groups = defineComponent({
  props: { groups: Array, resetKey: [Number, String] },
  template: '<div data-test="groups" :data-reset-key="resetKey" />',
})
const sharedProps = {
  accounts, loading: false, selectedIds: [], todayStats: {}, todayStatsLoading: false,
  todayStatsError: false, todayStatsUpdatedAt: null, manualRefreshToken: 0, statusNow: 0,
}
const stubs = {
  AccountUsageCell: Usage, AccountGroupsCell: Groups, AccountCapacityCell: true,
  AccountStatusIndicator: true, AccountIdentityBadges: true, Icon: true,
}

let wrapper: VueWrapper | undefined
let scrollToDescriptor: PropertyDescriptor | undefined
let rowHeight = 360

beforeEach(() => {
  instances.length = 0
  rowHeight = 360
  vi.stubGlobal('CSS', { supports: vi.fn(() => true) })
  vi.stubGlobal('innerHeight', 720)
  vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockImplementation(function () {
    return this.hasAttribute('data-account-virtual-index') ? rowHeight : 400
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

async function scroll(top: number) {
  wrapper!.element.scrollTop = top
  await wrapper!.trigger('scroll')
  await flushPromises()
}
const canvas = () => wrapper!.get('[role="list"]').element as HTMLElement
const slots = () => Array.from(canvas().children).filter(child => !child.hasAttribute('data-account-virtual-spacer'))
const activeRows = () => wrapper!.findAll('[data-account-virtual-index]')
const idsIn = (row: Element) => Array.from(row.querySelectorAll<HTMLElement>('[data-account-id]')).map(card => Number(card.dataset.accountId))
function logicalIds() {
  return canvas().getAttribute('aria-owns')!.split(' ').flatMap(id => idsIn(document.getElementById(id)!))
}
function expectRowsInOrder(columns: number) {
  const ids = activeRows().flatMap(row => {
    const index = Number(row.attributes('data-account-virtual-index'))
    const currentIds = idsIn(row.element)
    expect(currentIds).toEqual(accounts.slice(index * columns, (index + 1) * columns).map(account => account.id))
    expect(row.attributes('role')).toBe('listitem')
    expect(row.attributes('aria-posinset')).toBe(String(index + 1))
    expect(row.attributes('aria-setsize')).toBe(String(Math.ceil(accounts.length / columns)))
    expect((row.element as HTMLElement).style.position).toBe('absolute')
    expect(instances[0].elementsCache.get(currentIds.join(':'))).toBe(row.element)
    return currentIds
  })
  expect(new Set(ids).size).toBe(ids.length)
  expect(logicalIds()).toEqual([...ids].sort((a, b) => a - b))
  expect(instances[0].elementsCache.size).toBe(activeRows().length)
}

function observeRows() {
  const callbacks = new Map<Element, ResizeObserverCallback>()
  vi.stubGlobal('ResizeObserver', class {
    constructor(private callback: ResizeObserverCallback) {}
    observe(target: Element) { callbacks.set(target, this.callback) }
    unobserve(target: Element) { callbacks.delete(target) }
    disconnect() {}
  })
  return (height: number) => {
    const rows = activeRows().map(row => row.element)
    const entries = rows.map(row => ({ target: row, borderBoxSize: [{ blockSize: height, inlineSize: 1200 }] }))
    callbacks.get(rows[0])!(entries as unknown as ResizeObserverEntry[], {} as ResizeObserver)
  }
}

describe('card stable slot windows', () => {
  it('keeps canvas children fixed and exposes row order with natural column order', async () => {
    mockViewport(1200)
    wrapper = mount(AccountCardGrid, {
      attachTo: document.body, props: { ...sharedProps, requestBatchedUsage: vi.fn() }, global: { stubs },
    })
    await flushPromises()
    const originalSlots = slots()
    expect(originalSlots.length).toBeLessThan(15)
    expect(originalSlots.length).toBeGreaterThan(activeRows().length)
    expect((canvas().style as unknown as { readingFlow: string }).readingFlow).toBe('source-order')
    const changes: MutationRecord[] = []
    const observer = new MutationObserver(records => changes.push(...records))
    observer.observe(canvas(), { childList: true })
    try {
      for (const top of [20_000, 20_372, 20_744, 21_116, 1_000_000]) {
        await scroll(top)
        expect(slots()).toEqual(originalSlots)
        expect(changes).toHaveLength(0)
        expectRowsInOrder(2)
      }
      expect(wrapper.find('[data-account-id="1000"]').exists()).toBe(true)
      for (const empty of slots().filter(slot => slot.childElementCount === 0)) {
        expect(empty.getAttribute('aria-hidden')).toBe('true')
        expect(empty.hasAttribute('inert')).toBe(true)
        expect(empty.hasAttribute('hidden')).toBe(false)
      }
    } finally {
      observer.disconnect()
    }
  })

  it('clears recycled focus and pointer ownership while syncing managed usage to the new account', async () => {
    mockViewport(1200)
    wrapper = mount(AccountCardGrid, {
      attachTo: document.body, props: { ...sharedProps, requestBatchedUsage: vi.fn() }, global: { stubs },
    })
    await flushPromises()
    const card = wrapper.get('[data-account-id="1"]').element as HTMLElement
    const button = card.querySelector('button')!
    const checkbox = card.querySelector('input')!
    const edit = card.querySelector<HTMLButtonElement>('.icon-button')!
    const usage = wrapper.findAllComponents(Usage).find(cell => cell.props('account').id === 1)!
    const oldUsageUid = usage.vm.$.uid
    const oldUsageElement = usage.element
    const groups = wrapper.findAllComponents(Groups).find(cell => cell.props('resetKey') === 1)!
    const oldGroupsUid = groups.vm.$.uid
    button.focus()
    edit.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    await scroll(20_000)
    const nextId = Number(card.dataset.accountId)
    expect(nextId).toBeGreaterThan(20)
    expect(card.isConnected).toBe(true)
    expect(card.querySelector('button')).toBe(button)
    expect(card.querySelector('input')).toBe(checkbox)
    expect(document.activeElement).toBe(document.body)
    expect(edit.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, detail: 1 }))).toBe(false)
    expect(wrapper.emitted('edit')).toBeUndefined()
    expect(wrapper.emitted('rowClick')).toBeUndefined()
    edit.dispatchEvent(new MouseEvent('click', { bubbles: true, detail: 0 }))
    expect((wrapper.emitted('edit')![0][0] as Account).id).toBe(nextId)
    const nextUsage = wrapper.findAllComponents(Usage).find(cell => cell.props('account').id === nextId)!
    expect(nextUsage.vm.$.uid).toBe(oldUsageUid)
    expect(nextUsage.element).toBe(oldUsageElement)
    expect(oldUsageElement.isConnected).toBe(true)
    expect(nextUsage.props('account').id).toBe(nextId)
    expect(nextUsage.attributes('data-account')).toBe(String(nextId))
    expect(nextUsage.attributes('data-mounted-account')).toBe('1')
    const nextGroups = wrapper.findAllComponents(Groups).find(cell => cell.props('resetKey') === nextId)!
    expect(nextGroups.vm.$.uid).toBe(oldGroupsUid)
    expect(nextGroups.attributes('data-reset-key')).toBe(String(nextId))
    await wrapper.get(`[data-account-id="${nextId}"] input`).setValue(true)
    expect(wrapper.emitted('toggle')).toEqual([[nextId]])
  })

  it('rebuilds usage instances when managed eligibility leaves and re-enters its key bucket', async () => {
    mockViewport(1200)
    const requestBatchedUsage = vi.fn()
    wrapper = mount(AccountCardGrid, {
      attachTo: document.body, props: { ...sharedProps, requestBatchedUsage }, global: { stubs },
    })
    await flushPromises()
    const usageForFirst = () => wrapper!.findAllComponents(Usage).find(cell => cell.props('account').id === 1)!
    const managed = usageForFirst()
    const managedUid = managed.vm.$.uid
    const managedElement = managed.element

    await wrapper.setProps({ requestBatchedUsage: undefined })
    const standalone = usageForFirst()
    const standaloneUid = standalone.vm.$.uid
    const standaloneElement = standalone.element
    expect(standaloneUid).not.toBe(managedUid)
    expect(managedElement.isConnected).toBe(false)
    expect(standalone.attributes('data-mounted-account')).toBe('1')
    expect(standalone.attributes('data-account')).toBe('1')

    await wrapper.setProps({ requestBatchedUsage })
    const restoredManaged = usageForFirst()
    expect(restoredManaged.vm.$.uid).not.toBe(standaloneUid)
    expect(restoredManaged.vm.$.uid).not.toBe(managedUid)
    expect(standaloneElement.isConnected).toBe(false)
    expect(restoredManaged.attributes('data-account')).toBe('1')
  })

  it('regroups two and three columns, measures long rows through RO, and retains the partial final row', async () => {
    const resize = mockViewport(1200)
    const reportRows = observeRows()
    wrapper = mount(AccountCardGrid, {
      attachTo: document.body, props: { ...sharedProps, requestBatchedUsage: vi.fn() }, global: { stubs },
    })
    await flushPromises()
    expectRowsInOrder(2)
    rowHeight = 720
    reportRows(rowHeight)
    await vi.waitFor(() => expect(activeRows().every(row =>
      instances[0].measurementsCache[Number(row.attributes('data-account-virtual-index'))].size === 720,
    )).toBe(true))
    await resize(1600)
    expectRowsInOrder(3)
    expect(wrapper.get('[data-account-virtual-index="0"]').findAll('article')).toHaveLength(3)
    reportRows(rowHeight)
    await vi.waitFor(() => expect(activeRows().every(row =>
      instances[0].measurementsCache[Number(row.attributes('data-account-virtual-index'))].size === 720,
    )).toBe(true))
    await scroll(1_000_000)
    expectRowsInOrder(3)
    expect(wrapper.get('[data-account-virtual-index="333"]').findAll('article')).toHaveLength(1)
    expect(wrapper.find('[data-account-id="1000"]').exists()).toBe(true)
    const ids = activeRows().flatMap(row => idsIn(row.element))
    expect(new Set(ids).size).toBe(ids.length)
  })

  it.each(['unsupported', 'no batch', 'mobile'] as const)('keeps account keyed content on the %s path', async mode => {
    mockViewport(mode === 'mobile' ? 800 : 1200)
    if (mode === 'unsupported') vi.mocked(CSS.supports).mockReturnValue(false)
    wrapper = mount(AccountCardGrid, {
      attachTo: document.body,
      props: { ...sharedProps, requestBatchedUsage: mode === 'no batch' ? undefined : vi.fn() },
      global: { stubs },
    })
    await flushPromises()
    const oldCard = wrapper.get('[data-account-id="1"]').element
    const oldUsage = wrapper.findAllComponents(Usage).find(cell => cell.props('account').id === 1)!
    const oldUsageUid = oldUsage.vm.$.uid
    const oldUsageElement = oldUsage.element
    if (mode === 'mobile') {
      vi.stubGlobal('scrollY', 20_000)
      window.dispatchEvent(new Event('scroll'))
      await flushPromises()
    } else await scroll(20_000)
    expect(oldCard.isConnected).toBe(false)
    expect(oldUsageElement.isConnected).toBe(false)
    expect(wrapper.findAllComponents(Usage).every(cell => cell.vm.$.uid !== oldUsageUid)).toBe(true)
    const rows = activeRows()
    const currentIds = rows.flatMap(row => idsIn(row.element))
    if (mode === 'unsupported') {
      expect(canvas().hasAttribute('aria-owns')).toBe(false)
      expect(currentIds).toEqual([...currentIds].sort((a, b) => a - b))
    } else expect(logicalIds()).toEqual([...currentIds].sort((a, b) => a - b))
    for (const card of wrapper.findAll('[data-account-id]')) {
      expect(card.get('[data-test="usage"]').attributes('data-mounted-account')).toBe(card.attributes('data-account-id'))
    }
  })

  it('keeps small lists in natural order and normal flow', async () => {
    mockViewport(1200)
    wrapper = mount(AccountCardGrid, {
      attachTo: document.body,
      props: { ...sharedProps, accounts: accounts.slice(0, 50), requestBatchedUsage: vi.fn() },
      global: { stubs },
    })
    await flushPromises()
    expect(wrapper.findAll('[data-account-id]').map(card => Number(card.attributes('data-account-id')))).toEqual(
      accounts.slice(0, 50).map(account => account.id),
    )
    expect(canvas().hasAttribute('aria-owns')).toBe(false)
    expect(wrapper.find('[data-account-virtual-spacer]').exists()).toBe(false)
    expect(activeRows().every(row => (row.element as HTMLElement).style.position === '')).toBe(true)
  })
})
