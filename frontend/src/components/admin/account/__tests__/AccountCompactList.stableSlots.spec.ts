import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent, onMounted, type PropType } from 'vue'
import { createPinia, disposePinia, type Pinia } from 'pinia'
import type { Virtualizer } from '@tanstack/vue-virtual'
import AccountCompactList from '../AccountCompactList.vue'
import AccountUsageCell from '@/components/account/AccountUsageCell.vue'
import AccountStatusIndicator from '@/components/account/AccountStatusIndicator.vue'
import AccountGroupsCell from '@/components/account/AccountGroupsCell.vue'
import type { Account, AccountUsageInfo, Group } from '@/types'

const { instances, getUsage } = vi.hoisted(() => ({
  instances: [] as Virtualizer<HTMLElement, Element>[], getUsage: vi.fn(),
}))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { getUsage } } }))
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
vi.mock('@/utils/format', async importOriginal => ({
  ...await importOriginal<typeof import('@/utils/format')>(),
  formatCountdown: () => '1h',
}))

const accounts = Array.from({ length: 1000 }, (_, index) => ({
  id: index + 1, name: `account-${index + 1}`, platform: 'openai', type: 'oauth',
  status: 'active', concurrency: 10, groups: [], extra: {},
}) as Account)
const Usage = defineComponent({
  props: { account: { type: Object as PropType<Account>, required: true }, requestBatchedUsage: Function },
  setup(props) {
    const mountedAccount = props.account.id
    onMounted(() => props.requestBatchedUsage?.(props.account))
    return { mountedAccount }
  },
  template: '<div data-test="usage" :data-mounted-account="mountedAccount" />',
})
const props = {
  accounts, loading: false, selectedIds: [], todayStats: {}, todayStatsLoading: false,
  todayStatsError: false, todayStatsUpdatedAt: null, manualRefreshToken: 0, statusNow: 0,
}
const stubs = {
  AccountUsageCell: Usage, AccountCapacityCell: true, AccountStatusIndicator: true,
  AccountIdentityBadges: true, AccountGroupsCell: true, Icon: true,
}
let wrapper: VueWrapper | undefined
let pinia: Pinia
let restoreScrollTo: () => void
const measuredAccountIds: number[] = []

beforeEach(() => {
  instances.length = 0
  measuredAccountIds.length = 0
  getUsage.mockReset()
  pinia = createPinia()
  vi.stubGlobal('CSS', { supports: vi.fn(() => true) })
  vi.spyOn(window, 'matchMedia').mockImplementation(query => ({
    matches: query.includes('min-width'), media: query,
    addEventListener: vi.fn(), removeEventListener: vi.fn(),
    addListener: vi.fn(), removeListener: vi.fn(),
  }) as unknown as MediaQueryList)
  vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockImplementation(function () {
    if (this.hasAttribute('data-account-virtual-index')) {
      measuredAccountIds.push(Number(this.dataset.accountId))
      return 112
    }
    return 400
  })
  vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockReturnValue(1200)
  const descriptor = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollTo')
  Object.defineProperty(HTMLElement.prototype, 'scrollTo', { configurable: true, value: vi.fn() })
  restoreScrollTo = () => {
    if (descriptor) Object.defineProperty(HTMLElement.prototype, 'scrollTo', descriptor)
    else Reflect.deleteProperty(HTMLElement.prototype, 'scrollTo')
  }
})
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  disposePinia(pinia)
  restoreScrollTo()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

async function scroll(top: number) {
  const element = wrapper!.element as HTMLElement
  element.scrollTop = top
  await wrapper!.trigger('scroll')
  await flushPromises()
}

function canvasSlots(canvas: HTMLElement) {
  return Array.from(canvas.children).filter(child => !child.hasAttribute('data-account-virtual-spacer'))
}

function mockRowSizes(size: (accountId: number) => number) {
  vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockImplementation(function () {
    if (!this.hasAttribute('data-account-virtual-index')) return 400
    const id = Number(this.dataset.accountId)
    measuredAccountIds.push(id)
    return size(id)
  })
}

function observeRowSizes() {
  const callbacks = new Map<Element, ResizeObserverCallback>()
  vi.stubGlobal('ResizeObserver', class {
    constructor(private callback: ResizeObserverCallback) {}
    observe(target: Element) { callbacks.set(target, this.callback) }
    unobserve(target: Element) { callbacks.delete(target) }
    disconnect() {}
  })
  return (rows: HTMLElement[], size: (accountId: number) => number) => {
    const entries = rows.map(row => ({ target: row, borderBoxSize: [{ blockSize: size(Number(row.dataset.accountId)), inlineSize: 1200 }] }))
    callbacks.get(rows[0])!(entries as unknown as ResizeObserverEntry[], {} as ResizeObserver)
  }
}

describe('compact stable slot windows', () => {
  it('keeps canvas children fixed, resets account children, and retains measured account IDs', async () => {
    wrapper = mount(AccountCompactList, {
      attachTo: document.body, props, global: { stubs },
    })
    await flushPromises()
    const canvas = wrapper.element.firstElementChild as HTMLElement
    const slots = canvasSlots(canvas)
    expect(slots.length).toBeLessThan(30)
    expect(slots.length).toBeGreaterThan(wrapper.findAll('[data-account-id]').length)
    expect((canvas.style as unknown as { readingFlow: string }).readingFlow).toBe('source-order')
    const oldRow = wrapper.get('[data-account-id="1"]').element
    const oldFocus = wrapper.get('[data-account-id="1"] button').element as HTMLButtonElement
    oldFocus.focus()
    expect(document.activeElement).toBe(oldFocus)

    const childChanges: MutationRecord[] = []
    const observer = new MutationObserver(records => childChanges.push(...records))
    observer.observe(canvas, { childList: true })
    try {
      await scroll(5600)
      await scroll(5712)
      expect(canvasSlots(canvas)).toEqual(slots)
      expect(childChanges).toHaveLength(0)
      expect(oldRow.isConnected).toBe(false)
      expect(oldFocus.isConnected).toBe(false)
      expect(document.activeElement).toBe(document.body)
      const rows = wrapper.findAll('[data-account-id]')
      const ids = rows.map(row => Number(row.attributes('data-account-id')))
      expect(ids).not.toEqual([...ids].sort((a, b) => a - b))
      const ownedIds = canvas.getAttribute('aria-owns')!.split(' ').map(id =>
        Number(document.getElementById(id)!.querySelector<HTMLElement>('[data-account-id]')!.dataset.accountId),
      )
      expect(ownedIds).toEqual([...ids].sort((a, b) => a - b))
      for (const row of rows) {
        const id = Number(row.attributes('data-account-id'))
        expect(row.get('[data-test="usage"]').attributes('data-mounted-account')).toBe(String(id))
        const slot = row.element.parentElement as HTMLElement
        expect((slot.style as unknown as { readingOrder: number }).readingOrder).toBe(id - 1)
        expect(slot.getAttribute('aria-posinset')).toBe(String(id))
        expect(slot.getAttribute('aria-setsize')).toBe('1000')
        expect(instances[0].elementsCache.get(String(id))).toBe(row.element)
      }
      expect(instances[0].elementsCache.size).toBe(rows.length)
      const nextAccount = ids[0]
      await wrapper.get(`[data-account-id="${nextAccount}"] input`).setValue(true)
      expect(wrapper.emitted('toggle')).toEqual([[nextAccount]])
      await wrapper.get(`[data-account-id="${nextAccount}"] button`).trigger('click')
      expect((wrapper.emitted('rowClick')![0][0] as Account).id).toBe(nextAccount)
      await scroll(1_000_000)
      expect(wrapper.find('[data-account-id="1000"]').exists()).toBe(true)
      expect(canvasSlots(canvas)).toEqual(slots)
      for (const slot of slots.filter(slot => slot.childElementCount === 0)) {
        expect(slot.getAttribute('aria-hidden')).toBe('true')
        expect(slot.hasAttribute('inert')).toBe(true)
        expect(slot.hasAttribute('hidden')).toBe(false)
      }
      expect(CSS.supports).toHaveBeenCalledTimes(2)
    } finally {
      observer.disconnect()
    }
  })

  it('reuses managed desktop controls, clears old focus, and updates real usage ownership', async () => {
    const usageById = Object.fromEntries(accounts.map(account => [String(account.id), {
      updated_at: '2026-11-14T00:00:00Z',
      five_hour: { utilization: account.id < 100 ? 13 : 76, resets_at: '2099-01-01T00:00:00Z' },
    } as AccountUsageInfo]))
    const requestBatchedUsage = vi.fn()
    wrapper = mount(AccountCompactList, {
      attachTo: document.body,
      props: { ...props, batchedUsage: usageById, requestBatchedUsage },
      global: { plugins: [pinia], stubs: { ...stubs, AccountUsageCell: false } },
    })
    await flushPromises()
    const canvas = wrapper.element.firstElementChild as HTMLElement
    const slots = canvasSlots(canvas)
    const row = wrapper.get('[data-account-id="1"]').element
    const oldButton = row.querySelector('button')!
    const oldCheckbox = row.querySelector('input')!
    const oldStatus = row.querySelector('account-status-indicator-stub')!
    const oldGroups = row.querySelector('account-groups-cell-stub')!
    const oldEdit = row.querySelector<HTMLButtonElement>('.icon-button')!
    const usageUid = wrapper.findAllComponents(AccountUsageCell).find(cell => cell.props('account').id === 1)!.vm.$.uid
    expect(row.textContent).toContain('13%')
    oldButton.focus()
    oldEdit.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    requestBatchedUsage.mockClear()
    measuredAccountIds.length = 0

    await scroll(56_000)
    expect(canvasSlots(canvas)).toEqual(slots)
    expect(row.isConnected).toBe(true)
    const newId = Number((row as HTMLElement).dataset.accountId)
    expect(newId).toBeGreaterThan(100)
    expect(oldButton.isConnected).toBe(true)
    expect(oldCheckbox.isConnected).toBe(true)
    expect(oldStatus.isConnected).toBe(true)
    expect(oldGroups.isConnected).toBe(true)
    expect(row.querySelector('button')).toBe(oldButton)
    expect(row.querySelector('input')).toBe(oldCheckbox)
    expect(document.activeElement).toBe(document.body)
    expect(oldEdit.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, detail: 1 }))).toBe(false)
    expect(wrapper.emitted('edit')).toBeUndefined()
    expect(wrapper.emitted('rowClick')).toBeUndefined()
    // Keyboard/programmatic activation still targets the currently rendered account.
    oldEdit.dispatchEvent(new MouseEvent('click', { bubbles: true, detail: 0 }))
    expect((wrapper.emitted('edit')![0][0] as Account).id).toBe(newId)
    const nextUsage = wrapper.findAllComponents(AccountUsageCell).find(cell => cell.props('account').id === newId)!
    expect(nextUsage.vm.$.uid).toBe(usageUid)
    expect(row.textContent).toContain('76%')
    expect(row.textContent).not.toContain('13%')
    expect(measuredAccountIds).toContain(newId)
    expect(requestBatchedUsage.mock.calls.some(([account]) => account.id === newId)).toBe(true)
    expect(requestBatchedUsage.mock.calls.some(([, options]) => options?.force)).toBe(false)
    expect(getUsage).not.toHaveBeenCalled()

    for (const top of [67_200, 78_400, 89_600, 100_800]) {
      await scroll(top)
      const rows = wrapper.findAll('[data-account-id]')
      expect(instances[0].elementsCache.size).toBe(rows.length)
      for (const current of rows) {
        expect(instances[0].elementsCache.get(current.attributes('data-account-id'))).toBe(current.element)
      }
    }
    const currentId = Number((row as HTMLElement).dataset.accountId)
    requestBatchedUsage.mockClear()
    const changed = accounts.map(account => account.id === currentId
      ? { ...account, updated_at: '2026-11-14T00:01:00Z' } : account)
    await wrapper.setProps({ accounts: changed })
    expect(requestBatchedUsage.mock.calls.some(([account, options]) => account.id === currentId && options?.force)).toBe(true)
    await wrapper.setProps({
      accounts: changed.map(account => account.id === currentId ? { ...account, type: 'apikey' as const } : account),
      batchedUsage: { ...usageById, [String(currentId)]: null },
    })
    expect(row.textContent).not.toContain('76%')
    const current = wrapper.get(`[data-account-id="${currentId}"]`)
    await current.get('input').setValue(true)
    expect(wrapper.emitted('toggle')).toEqual([[currentId]])
    await current.get('button').trigger('click')
    expect((wrapper.emitted('rowClick')![0][0] as Account).id).toBe(currentId)
  })

  it('holds an equal-sized connected window across fractional rows and at the final account', async () => {
    wrapper = mount(AccountCompactList, {
      attachTo: document.body, props: { ...props, requestBatchedUsage: vi.fn() }, global: { stubs },
    })
    await flushPromises()
    await scroll(5600)
    await scroll(5656)
    const canvas = wrapper.element.firstElementChild as HTMLElement
    const slots = canvasSlots(canvas)
    const rows = wrapper.findAll('[data-account-id]').map(row => row.element)
    const size = rows.length
    const elementMutations: MutationRecord[] = []
    const observer = new MutationObserver(records => elementMutations.push(...records.filter(record =>
      [...record.addedNodes, ...record.removedNodes].some(node => node.nodeType === Node.ELEMENT_NODE),
    )))
    observer.observe(canvas, { childList: true, subtree: true })
    try {
      for (const top of [5712, 5768, 5824, 5880, 5936, 5992, 6048, 6104, 1_000_000]) {
        await scroll(top)
        expect(wrapper.findAll('[data-account-id]')).toHaveLength(size)
        expect(rows.every(row => row.isConnected)).toBe(true)
        expect(canvasSlots(canvas)).toEqual(slots)
        const owned = canvas.getAttribute('aria-owns')!.split(' ').map(id =>
          Number(document.getElementById(id)!.querySelector<HTMLElement>('[data-account-id]')!.dataset.accountId),
        )
        expect(owned).toEqual([...owned].sort((a, b) => a - b))
        expect(new Set(owned).size).toBe(size)
      }
      expect(elementMutations).toHaveLength(0)
      expect(wrapper.find('[data-account-id="1000"]').exists()).toBe(true)
      const visibleIds = wrapper.findAll('[data-account-id]').map(row => Number(row.attributes('data-account-id')))
      expect(Math.min(...visibleIds)).toBe(1001 - size)
      expect(size).toBeLessThan(25)
    } finally {
      observer.disconnect()
    }
  })

  it('reuses a measured same-height row without synchronous geometry reads', async () => {
    mockRowSizes(() => 63)
    wrapper = mount(AccountCompactList, {
      attachTo: document.body, props: { ...props, requestBatchedUsage: vi.fn() },
      global: { stubs: { ...stubs, AccountUsageCell: true } },
    })
    await flushPromises()
    const row = wrapper.get('[data-account-id="1"]').element as HTMLElement
    expect(instances[0]['itemSizeCache'].get('1')).toBe(63)
    measuredAccountIds.length = 0
    await scroll(5600)
    expect(row.isConnected).toBe(true)
    expect(Number(row.dataset.accountId)).toBeGreaterThan(50)
    expect(measuredAccountIds).toEqual([])
    expect(instances[0].measurementsCache[instances[0].indexFromElement(row)].size).toBe(63)
    expect(instances[0].elementsCache.get(row.dataset.accountId!)).toBe(row)
  })

  it('lets ResizeObserver correct a taller recycled row from its initial measured seed', async () => {
    const size = (id: number) => id < 100 ? 63 : 168
    mockRowSizes(size)
    const reportSizes = observeRowSizes()
    wrapper = mount(AccountCompactList, {
      attachTo: document.body, props: { ...props, requestBatchedUsage: vi.fn() },
      global: { stubs: { ...stubs, AccountUsageCell: true } },
    })
    await flushPromises()
    const row = wrapper.get('[data-account-id="1"]').element as HTMLElement
    measuredAccountIds.length = 0
    await scroll(56_000)
    expect(Number(row.dataset.accountId)).toBeGreaterThan(100)
    expect(instances[0].measurementsCache[instances[0].indexFromElement(row)].size).toBe(63)
    expect(measuredAccountIds).toEqual([])
    reportSizes(wrapper.findAll('[data-account-id]').map(row => row.element as HTMLElement), size)
    await vi.waitFor(() => expect(instances[0].measurementsCache[instances[0].indexFromElement(row)].size).toBe(168))
    expect(measuredAccountIds).toEqual([])
    expect(instances[0].elementsCache.get(row.dataset.accountId!)).toBe(row)
  })

  it('keeps the returning account measured size instead of the previous slot account size', async () => {
    const size = (id: number) => id === 1 ? 224 : 63
    mockRowSizes(size)
    const reportSizes = observeRowSizes()
    wrapper = mount(AccountCompactList, {
      attachTo: document.body, props: { ...props, requestBatchedUsage: vi.fn() },
      global: { stubs: { ...stubs, AccountUsageCell: true } },
    })
    await flushPromises()
    expect(instances[0]['itemSizeCache'].get('1')).toBe(224)
    await scroll(5600)
    reportSizes(wrapper.findAll('[data-account-id]').map(row => row.element as HTMLElement), size)
    await vi.waitFor(() => expect(wrapper!.findAll('[data-account-id]').every(row =>
      instances[0].measurementsCache[instances[0].indexFromElement(row.element)].size === 63,
    )).toBe(true))
    measuredAccountIds.length = 0
    await scroll(0)
    const row = wrapper.get('[data-account-id="1"]').element as HTMLElement
    expect(instances[0].measurementsCache[instances[0].indexFromElement(row)].size).toBe(224)
    expect(measuredAccountIds).toEqual([])
  })

  it('reobserves a recycled node so an unchanged physical height corrects a stale account size', async () => {
    let firstSize = 224
    const size = (id: number) => id === 1 ? firstSize : 63
    mockRowSizes(size)
    const reportSizes = observeRowSizes()
    wrapper = mount(AccountCompactList, {
      attachTo: document.body, props: { ...props, requestBatchedUsage: vi.fn() },
      global: { stubs: { ...stubs, AccountUsageCell: true } },
    })
    await flushPromises()
    const instance = instances[0]
    expect(instance['itemSizeCache'].get('1')).toBe(224)
    await scroll(5600)
    reportSizes(wrapper.findAll('[data-account-id]').map(row => row.element as HTMLElement), size)
    await vi.waitFor(() => expect(wrapper!.findAll('[data-account-id]').every(row =>
      instance.measurementsCache[instance.indexFromElement(row.element)].size === 63,
    )).toBe(true))

    // Account 1 changes while off screen; the recycled physical rows are already 63px tall.
    firstSize = 63
    await wrapper.setProps({ accounts: accounts.map(account => account.id === 1 ? { ...account, name: 'shorter-account-1' } : account) })
    expect(instance['itemSizeCache'].get('1')).toBe(224)
    const unobserve = vi.spyOn(instance['observer'], 'unobserve')
    const observe = vi.spyOn(instance['observer'], 'observe')
    measuredAccountIds.length = 0
    await scroll(0)
    const row = wrapper.get('[data-account-id="1"]').element as HTMLElement
    expect(instance.measurementsCache[0].size).toBe(224)
    expect(measuredAccountIds).toEqual([])
    const detach = unobserve.mock.calls.findIndex(([node]) => node === row)
    const attach = observe.mock.calls.findIndex(([node]) => node === row)
    expect(detach).toBeGreaterThanOrEqual(0)
    expect(attach).toBeGreaterThanOrEqual(0)
    expect(observe.mock.invocationCallOrder[attach]).toBeGreaterThan(unobserve.mock.invocationCallOrder[detach])

    // A newly registered native observation reports an initial border box even when its height stayed 63px.
    reportSizes([row], size)
    await vi.waitFor(() => expect(instance.measurementsCache[0].size).toBe(63))
    expect(instance.measurementsCache[1].start).toBe(63)
    expect(instance['itemSizeCache'].get('1')).toBe(63)
    expect(measuredAccountIds).toEqual([])
  })

  it('measures a recycled row when neither account has an actual size cache entry', async () => {
    wrapper = mount(AccountCompactList, {
      attachTo: document.body, props: { ...props, requestBatchedUsage: vi.fn() },
      global: { stubs: { ...stubs, AccountUsageCell: true } },
    })
    await flushPromises()
    const row = wrapper.get('[data-account-id="1"]').element as HTMLElement
    // A size equal to estimateSize does not create an itemSizeCache entry in the locked TanStack version.
    expect(instances[0]['itemSizeCache'].has('1')).toBe(false)
    measuredAccountIds.length = 0
    await scroll(5600)
    const nextId = Number(row.dataset.accountId)
    expect(nextId).toBeGreaterThan(1)
    expect(measuredAccountIds).toContain(nextId)
    expect(instances[0].measurementsCache[instances[0].indexFromElement(row)].size).toBe(112)
  })

  it('prefetches only adjacent windows through the existing non-force batch callback', async () => {
    const requestBatchedUsage = vi.fn()
    wrapper = mount(AccountCompactList, {
      attachTo: document.body, props: { ...props, requestBatchedUsage },
      global: { stubs: { ...stubs, AccountUsageCell: true } },
    })
    await flushPromises()
    const visibleIds = () => wrapper!.findAll('[data-account-id]')
      .map(row => Number(row.attributes('data-account-id'))).sort((a, b) => a - b)
    const requestedIds = () => [...new Set(requestBatchedUsage.mock.calls.map(([account]) => account.id as number))]
      .sort((a, b) => a - b)
    const initial = visibleIds()
    expect(requestedIds()).toEqual(accounts.slice(initial.length, 2 * initial.length).map(account => account.id))
    expect(requestedIds().length).toBeLessThan(25)
    expect(requestBatchedUsage.mock.calls.every(call => call.length === 1)).toBe(true)

    requestBatchedUsage.mockClear()
    await scroll(56_000)
    const current = visibleIds()
    const first = current[0] - 1
    const end = current[current.length - 1]
    const expected = [
      ...accounts.slice(first - current.length, first),
      ...accounts.slice(end, end + current.length),
    ].map(account => account.id)
    expect(requestedIds()).toEqual(expected)
    expect(requestedIds().length).toBe(2 * current.length)
    expect(current.every(id => !requestedIds().includes(id))).toBe(true)
    expect(requestBatchedUsage.mock.calls.every(call => call.length === 1)).toBe(true)

    const targetIds = () => {
      const ids = visibleIds()
      return [
        ...accounts.slice(Math.max(0, ids[0] - 1 - ids.length), ids[0] - 1),
        ...accounts.slice(ids[ids.length - 1], ids[ids.length - 1] + ids.length),
      ].map(account => account.id)
    }
    const failedTarget = current[current.length - 1] + Math.floor(current.length / 2)
    await wrapper.setProps({ batchedUsageLoading: { [failedTarget]: false }, batchedUsageErrors: { [failedTarget]: 'failed' } })
    let previousTargets = expected
    for (const top of [56_112, 56_224]) {
      requestBatchedUsage.mockClear()
      await scroll(top)
      const targets = targetIds()
      expect(requestedIds()).toEqual(targets.filter(id => !previousTargets.includes(id)))
      expect(requestedIds()).not.toContain(failedTarget)
      previousTargets = targets
    }
    requestBatchedUsage.mockClear()
    await wrapper.setProps({ accounts: accounts.map(account => ({ ...account, name: `${account.name}-updated` })) })
    expect(requestedIds()).toEqual(targetIds())
    const replacementRequest = vi.fn()
    await wrapper.setProps({ requestBatchedUsage: replacementRequest })
    expect(replacementRequest.mock.calls.map(([account]) => account.id)).toEqual(targetIds())
    replacementRequest.mockClear()
    await wrapper.setProps({ requestBatchedUsage: undefined })
    await scroll(67_200)
    expect(replacementRequest).not.toHaveBeenCalled()
  })

  it.each(['small', 'unsupported'] as const)('does not prefetch for the %s path', async mode => {
    if (mode === 'unsupported') vi.mocked(CSS.supports).mockReturnValue(false)
    const requestBatchedUsage = vi.fn()
    wrapper = mount(AccountCompactList, {
      attachTo: document.body,
      props: { ...props, accounts: mode === 'small' ? accounts.slice(0, 2) : accounts, requestBatchedUsage },
      global: { stubs: { ...stubs, AccountUsageCell: true } },
    })
    await flushPromises()
    await scroll(5600)
    expect(requestBatchedUsage).not.toHaveBeenCalled()
  })

  it('closes real status and group popovers when a connected row changes account', async () => {
    const detailAccounts = accounts.map(account => ({
      ...account,
      groups: Array.from({ length: 5 }, (_, index) => ({ id: index + 1, name: `group-${account.id}-${index}` }) as Group),
      extra: {
        model_rate_limits: {
          'gpt-6-sol': {
            rate_limited_at: '2026-11-14T00:00:00Z', rate_limit_reset_at: '2099-01-01T00:00:00Z',
            upstream_message: `upstream-${account.id}`,
          },
        },
      },
    }))
    wrapper = mount(AccountCompactList, {
      attachTo: document.body,
      props: { ...props, accounts: detailAccounts, requestBatchedUsage: vi.fn() },
      global: { plugins: [pinia], stubs: {
        ...stubs, AccountStatusIndicator: false, AccountGroupsCell: false,
        GroupBadge: { props: ['name'], template: '<span>{{ name }}</span>' },
      } },
    })
    await flushPromises()
    const row = wrapper.get('[data-account-id="1"]').element as HTMLElement
    const status = wrapper.findAllComponents(AccountStatusIndicator).find(component => component.props('account').id === 1)!
    const groups = wrapper.findAllComponents(AccountGroupsCell).find(component => component.props('resetKey') === 1)!
    const statusUid = status.vm.$.uid
    const groupsUid = groups.vm.$.uid
    const statusTrigger = status.get('[data-testid="model-status-trigger"]')
    const groupTrigger = groups.get('button')
    await statusTrigger.trigger('click')
    const oldStatusPanel = document.querySelector('#overlay-root [role="dialog"]') as HTMLElement
    expect(oldStatusPanel.textContent).toContain('upstream-1')
    expect(document.activeElement).toBe(oldStatusPanel)
    await groupTrigger.trigger('click')
    const oldGroupPanel = document.querySelector('#overlay-root .min-w-48') as HTMLElement
    expect(oldGroupPanel.textContent).toContain('group-1-4')

    await scroll(56_000)
    const nextId = Number(row.dataset.accountId)
    expect(nextId).toBeGreaterThan(100)
    expect(status.vm.$.uid).toBe(statusUid)
    expect(groups.vm.$.uid).toBe(groupsUid)
    expect(status.props('account').id).toBe(nextId)
    expect(groups.props('resetKey')).toBe(nextId)
    expect(oldStatusPanel.isConnected).toBe(false)
    await vi.waitFor(() => expect(oldGroupPanel.isConnected).toBe(false))
    expect(document.activeElement).toBe(document.body)
    expect(statusTrigger.element.isConnected).toBe(true)
    expect(groupTrigger.element.isConnected).toBe(true)

    await statusTrigger.trigger('click')
    const nextStatusPanel = document.querySelector('#overlay-root [role="dialog"]') as HTMLElement
    expect(nextStatusPanel.textContent).toContain(`upstream-${nextId}`)
    expect(nextStatusPanel.textContent).not.toContain('upstream-1')
    expect(document.activeElement).toBe(nextStatusPanel)
    await groupTrigger.trigger('click')
    expect(document.querySelector('#overlay-root .min-w-48')!.textContent).toContain(`group-${nextId}-4`)
  })

  it('uses data DOM order unless both native reading order properties are supported', async () => {
    vi.mocked(CSS.supports).mockImplementation(property => property === 'reading-flow')
    wrapper = mount(AccountCompactList, { attachTo: document.body, props, global: { stubs } })
    await flushPromises()
    await scroll(5600)
    await scroll(5712)
    const ids = wrapper.findAll('[data-account-id]').map(row => Number(row.attributes('data-account-id')))
    expect(ids).toEqual([...ids].sort((a, b) => a - b))
    const canvas = wrapper.element.firstElementChild as HTMLElement
    expect((canvas.style as unknown as { readingFlow: string }).readingFlow).toBe('')
    expect(canvasSlots(canvas)).toHaveLength(ids.length)
  })

  it('keeps small lists in normal flow without reserved slots', async () => {
    wrapper = mount(AccountCompactList, { props: { ...props, accounts: accounts.slice(0, 2) }, global: { stubs } })
    await flushPromises()
    const canvas = wrapper.element.firstElementChild as HTMLElement
    expect(canvas.children).toHaveLength(2)
    expect(wrapper.find('[data-account-virtual-spacer]').exists()).toBe(false)
    expect(canvas.style.position).toBe('')
    expect(canvas.style.height).toBe('')
    expect(Array.from(canvas.children).every(slot => !(slot as HTMLElement).style.position)).toBe(true)
  })
})
