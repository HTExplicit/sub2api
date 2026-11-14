import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { reactive } from 'vue'
import type { Ref } from 'vue'

import DataTable from '../DataTable.vue'
import type { Column } from '../types'
import { flatThemeActive } from '@/utils/flatTheme'
import DataTableDynamicSlotFixture from './fixtures/DataTableDynamicSlotFixture.vue'
import DataTableEditorProbe from './fixtures/DataTableEditorProbe.vue'
import DataTableShellFixture from './fixtures/DataTableShellFixture.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key
  })
}))

// the console theme switch, off unless a test turns it on (stacked cells are the theme's layout)
vi.mock('@/utils/flatTheme', async () => {
  const { ref } = await import('vue')
  return { flatThemeActive: ref(false) }
})
const setConsoleTheme = (on: boolean) => {
  const theme = flatThemeActive as unknown as Ref<boolean>
  theme.value = on
}

const stubDesktopMatchMedia = () => {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: true,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn()
    }))
  })
}

const stubMobileMatchMedia = () => {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn()
    }))
  })
}

describe('DataTable', () => {
  beforeEach(() => {
    stubDesktopMatchMedia()
    setConsoleTheme(false)
    localStorage.clear()
  })

  it('keeps dividers on logical rows through sorting, loading and empty states', async () => {
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name', sortable: true }],
        data: [{ id: 1, name: 'Alpha' }, { id: 2, name: 'Beta' }, { id: 3, name: 'Gamma' }],
        defaultSortKey: 'name',
        defaultSortOrder: 'asc',
      },
    })
    try {
      await flushPromises()
      const firstAccount = wrapper.get('tr[data-row-id="1"]').element
      expect(wrapper.get('table').classes()).not.toContain('divide-y')
      expect(wrapper.get('tbody').classes()).not.toContain('divide-y')
      expect(wrapper.findAll('tbody tr').map(row => row.attributes('data-table-divider'))).toEqual([undefined, '', ''])

      await wrapper.get('th[data-column="name"]').trigger('click')
      expect(wrapper.get('tr[data-row-id="1"]').element).toBe(firstAccount)
      expect(wrapper.get('tr[data-row-id="1"]').attributes('data-table-divider')).toBe('')
      expect(wrapper.get('tr[data-row-id="3"]').attributes('data-table-divider')).toBeUndefined()

      await wrapper.setProps({ loading: true })
      expect(wrapper.findAll('tbody tr').map(row => row.attributes('data-table-divider'))).toEqual([undefined, '', '', '', ''])
      await wrapper.setProps({ loading: false, data: [] })
      expect(wrapper.findAll('tbody tr')).toHaveLength(1)
      expect(wrapper.get('tbody tr').attributes('data-table-divider')).toBeUndefined()
    } finally {
      wrapper.unmount()
    }
  })

  it('preserves spacer borders while the virtual window reaches the final logical row', async () => {
    const heightRead = vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockImplementation(function () {
      return this.hasAttribute('data-index') ? 56 : 400
    })
    const wrapper = mount(DataTable, {
      attachTo: document.body,
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data: Array.from({ length: 100 }, (_, index) => ({ id: index + 1, name: `Row ${index + 1}` })),
        virtualizeThreshold: 10,
        overscan: 2,
      },
    })
    try {
      await flushPromises()
      expect(wrapper.get('tbody tr[data-index="0"]').attributes('data-table-divider')).toBeUndefined()
      expect(wrapper.get('tbody tr[aria-hidden="true"]').attributes('data-table-divider')).toBe('')

      wrapper.element.scrollTop = 56 * 20
      await wrapper.trigger('scroll')
      await flushPromises()
      const middleSpacers = wrapper.findAll('tbody tr[aria-hidden="true"]')
      expect(middleSpacers).toHaveLength(2)
      expect(middleSpacers[0].attributes('data-table-divider')).toBeUndefined()
      expect(middleSpacers[1].attributes('data-table-divider')).toBe('')
      expect(wrapper.findAll('tbody tr[data-index]').every(row => row.attributes('data-table-divider') === '')).toBe(true)

      const exposed = (wrapper.vm as any).virtualizer
      const instance = exposed?.value ?? exposed
      wrapper.element.scrollTop = instance.getTotalSize() - Math.max(window.innerHeight - 320, 400)
      await wrapper.trigger('scroll')
      await flushPromises()
      expect(wrapper.get('tbody tr[data-index="99"]').attributes('data-table-divider')).toBe('')
      const finalSpacers = wrapper.findAll('tbody tr[aria-hidden="true"]')
      expect(finalSpacers).toHaveLength(1)
      expect(finalSpacers[0].attributes('data-table-divider')).toBeUndefined()
      expect(wrapper.get('tbody tr:last-child').attributes('data-row-id')).toBe('100')
    } finally {
      wrapper.unmount()
      heightRead.mockRestore()
    }
  })

  it('updates overflow on resize without measuring or changing action buttons', async () => {
    const resizeCallbacks = new Map<Element, ResizeObserverCallback[]>()
    const OriginalResizeObserver = globalThis.ResizeObserver
    globalThis.ResizeObserver = class {
      constructor(private callback: ResizeObserverCallback) {}
      observe(target: Element) {
        resizeCallbacks.set(target, [...(resizeCallbacks.get(target) ?? []), this.callback])
      }
      unobserve(target: Element) { resizeCallbacks.delete(target) }
      disconnect() {}
    } as unknown as typeof ResizeObserver

    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }, { key: 'actions', label: 'Actions' }],
        data: [{ id: 1, name: 'Account' }]
      },
      slots: {
        'cell-actions': '<div><button>Edit</button><button>Test</button><button>More</button></div>'
      }
    })

    try {
      await wrapper.vm.$nextTick()
      const table = wrapper.get('.table-wrapper')
      Object.defineProperties(table.element, {
        scrollWidth: { configurable: true, value: 1200 },
        clientWidth: { configurable: true, value: 800 }
      })
      const actionWidthRead = vi.fn(() => 80)
      wrapper.findAll('tbody button').forEach(button => {
        Object.defineProperty(button.element, 'offsetWidth', { get: actionWidthRead })
      })
      resizeCallbacks.get(table.element)!.forEach(callback => {
        callback([{ target: table.element, contentRect: { width: 800, height: 400 } } as ResizeObserverEntry], {} as ResizeObserver)
      })
      await wrapper.vm.$nextTick()

      expect(table.classes()).toContain('is-scrollable')
      expect(wrapper.findAll('tbody button').map(button => button.text())).toEqual(['Edit', 'Test', 'More'])
      expect(actionWidthRead).not.toHaveBeenCalled()
    } finally {
      wrapper.unmount()
      globalThis.ResizeObserver = OriginalResizeObserver
    }
  })

  it('renders paired sort arrows and highlights the active direction', async () => {
    const wrapper = mount(DataTable, {
      props: {
        columns: [
          { key: 'name', label: 'Name', sortable: true },
          { key: 'created_at', label: 'Created', sortable: true }
        ],
        data: [
          { id: 1, name: 'Beta', created_at: '2026-01-02T00:00:00Z' },
          { id: 2, name: 'Alpha', created_at: '2026-01-01T00:00:00Z' }
        ],
        defaultSortKey: 'name',
        defaultSortOrder: 'asc'
      },
      slots: {
        'header-name': '<span data-test="custom-name-header">Name</span>'
      }
    })

    await wrapper.vm.$nextTick()

    const nameHeader = wrapper.findAll('th')[0]
    expect(nameHeader.find('[data-test="custom-name-header"]').exists()).toBe(true)
    expect(nameHeader.attributes('aria-sort')).toBe('ascending')
    expect(nameHeader.findAll('svg')).toHaveLength(2)
    expect(nameHeader.findAll('svg')[0].classes()).toContain('text-primary-600')
    expect(nameHeader.findAll('svg')[1].classes()).toContain('text-gray-300')

    await nameHeader.trigger('click')
    await wrapper.vm.$nextTick()

    expect(nameHeader.attributes('aria-sort')).toBe('descending')
    expect(nameHeader.findAll('svg')[0].classes()).toContain('text-gray-300')
    expect(nameHeader.findAll('svg')[1].classes()).toContain('text-primary-600')
  })

  it('renders every row with no virtual padding spacer for small datasets (virtualization off)', async () => {
    const data = Array.from({ length: 8 }, (_, i) => ({ id: i + 1, name: `Row ${i + 1}` }))
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data
      }
    })

    await wrapper.vm.$nextTick()

    // Virtualization is OFF for a small list…
    expect((wrapper.vm as any).shouldVirtualize).toBe(false)
    // …every row is in the DOM…
    expect(wrapper.findAll('tbody tr[data-index]')).toHaveLength(data.length)
    // …and there are no aria-hidden virtual padding spacer rows.
    expect(wrapper.findAll('tbody tr[aria-hidden="true"]')).toHaveLength(0)
  })

  it('switches to windowed rendering once row count exceeds virtualizeThreshold', async () => {
    const data = Array.from({ length: 12 }, (_, i) => ({ id: i + 1, name: `Row ${i + 1}` }))
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data,
        virtualizeThreshold: 3
      }
    })

    await wrapper.vm.$nextTick()

    // Virtualization is ON: the mode-switch decision flipped…
    expect((wrapper.vm as any).shouldVirtualize).toBe(true)
    // …and the virtualizer drives off the full row count.
    const exposed = (wrapper.vm as any).virtualizer
    const instance = exposed?.value ?? exposed
    expect(instance.options.count).toBe(data.length)
  })

  it('does not synchronously remeasure cached row elements on an ordinary patch', async () => {
    const heightRead = vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockReturnValue(56)
    const wrapper = mount(DataTable, {
      attachTo: document.body,
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data: Array.from({ length: 100 }, (_, index) => ({ id: index + 1, name: `Row ${index + 1}` })),
        virtualizeThreshold: 10,
        selectable: true,
      },
    })
    try {
      await flushPromises()
      expect(heightRead).toHaveBeenCalled()
      heightRead.mockClear()
      await wrapper.setProps({ selectedKeys: [1] })
      await flushPromises()
      expect(heightRead.mock.contexts.filter(element => (element as HTMLElement).hasAttribute('data-index'))).toEqual([])
      expect((wrapper.get('tbody input').element as HTMLInputElement).checked).toBe(true)
    } finally {
      wrapper.unmount()
      heightRead.mockRestore()
    }
  })

  it('does not update its component when native scroll stays in the same row range', async () => {
    const heightRead = vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockImplementation(function () {
      return this.hasAttribute('data-index') ? 56 : 400
    })
    const tableUpdated = vi.fn()
    const wrapper = mount(DataTable, {
      attachTo: document.body,
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data: Array.from({ length: 100 }, (_, index) => ({ id: index + 1, name: `Row ${index + 1}` })),
        virtualizeThreshold: 10,
      },
      global: { mixins: [{ updated() { if (this.$el === rootElement) tableUpdated() } }] },
    })
    const rootElement = wrapper.element
    try {
      await flushPromises()
      const visibleIds = wrapper.findAll('tbody tr[data-index]').map(row => row.attributes('data-index'))
      tableUpdated.mockClear()
      heightRead.mockClear()
      for (const top of [1, 2, 3]) {
        wrapper.element.scrollTop = top
        await wrapper.trigger('scroll')
        await flushPromises()
      }
      expect(wrapper.findAll('tbody tr[data-index]').map(row => row.attributes('data-index'))).toEqual(visibleIds)
      expect(tableUpdated).not.toHaveBeenCalled()
      expect(heightRead.mock.contexts.filter(element => (element as HTMLElement).hasAttribute('data-index'))).toEqual([])
    } finally {
      wrapper.unmount()
      heightRead.mockRestore()
    }
  })

  describe('preserved virtual row shells', () => {
    const rowsForShells = (count: number) => Array.from({ length: count }, (_, index) => ({
      id: index + 1,
      name: `Row ${index + 1}`,
      detail: { label: `Detail ${index + 1}` },
    }))
    const stubShellHeights = () => vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockImplementation(function () {
      if (!this.hasAttribute('data-index')) return 400
      return this.style.display === 'none' ? 0 : 56
    })

    it('keeps 1000 ID shells while retaining active editors, focus, selection and measured sizes', async () => {
      const heightRead = stubShellHeights()
      const rows = rowsForShells(1000)
      const updatedRowIds: number[] = []
      const wrapper = mount(DataTableShellFixture, {
        attachTo: document.body,
        props: { rows, estimateRowHeight: 28 },
        global: {
          mixins: [{
            updated() {
              if (this.$options.__name === 'DataTableRowContent') {
                updatedRowIds.push((this.$props as { row: { id: number } }).row.id)
              }
            },
          }],
        },
      })
      const table = wrapper.getComponent(DataTable)
      try {
        await flushPromises()
        table.element.scrollTop = 56 * 10
        await table.trigger('scroll')
        await flushPromises()

        const beforeRows = table.findAll('tbody tr[data-row-id]')
        const leaving = beforeRows[0]
        const leavingId = Number(leaving.attributes('data-row-id'))
        const retainedId = Number(beforeRows[1].attributes('data-row-id'))
        const retained = table.get(`tr[data-row-id="${retainedId}"]`)
        const retainedElement = retained.element
        const checkbox = retained.get<HTMLInputElement>('[data-test="select-row"]')
        const checkboxElement = checkbox.element
        const editor = wrapper.findAllComponents(DataTableEditorProbe).find(probe => probe.props('rowId') === retainedId)!
        const editorInstance = editor.vm.$
        const input = editor.get<HTMLInputElement>('[data-test="row-editor"]')
        const inputElement = input.element
        await input.setValue('Unsubmitted edit')
        await editor.get('[data-test="editor-counter"]').trigger('click')
        await checkbox.setValue(true)
        inputElement.focus()

        const instance = (table.vm.$.exposed!.virtualizer as Ref<any>).value
        expect(instance.itemSizeCache.get(leavingId)).toBe(56)
        const unobserve = vi.spyOn(instance.observer, 'unobserve')

        updatedRowIds.length = 0
        table.element.scrollTop = 56 * 11
        await table.trigger('scroll')
        await flushPromises()

        const allShells = table.findAll('tbody tr[data-index]')
        const activeRows = table.findAll('tbody tr[data-row-id]')
        expect(allShells).toHaveLength(1000)
        expect(activeRows.length).toBeGreaterThan(0)
        expect(activeRows.length).toBeLessThan(1000)
        expect(table.findAllComponents({ name: 'DataTableRowContent' })).toHaveLength(activeRows.length)
        expect(allShells.every(row => (row.findAll('td').length > 0) === (row.attributes('data-row-id') !== undefined))).toBe(true)
        expect(table.get(`tr[data-index="${leavingId - 1}"]`).element).toBe(leaving.element)
        expect(leaving.element.isConnected).toBe(true)
        expect(leaving.attributes('data-row-id')).toBeUndefined()
        expect(leaving.attributes('aria-hidden')).toBe('true')
        expect((leaving.element as HTMLElement).style.display).toBe('none')
        expect(leaving.findAll('td')).toHaveLength(0)
        expect(unobserve).toHaveBeenCalledWith(leaving.element)
        expect(instance.elementsCache.has(leavingId)).toBe(false)
        expect(instance.itemSizeCache.get(leavingId)).toBe(56)

        const retainedAfter = table.get(`tr[data-row-id="${retainedId}"]`)
        const editorAfter = wrapper.findAllComponents(DataTableEditorProbe).find(probe => probe.props('rowId') === retainedId)!
        expect(updatedRowIds).not.toContain(retainedId)
        expect(retainedAfter.element).toBe(retainedElement)
        expect(retainedAfter.get('[data-test="select-row"]').element).toBe(checkboxElement)
        expect(checkboxElement.checked).toBe(true)
        expect(editorAfter.vm.$).toBe(editorInstance)
        expect(editorAfter.get('[data-test="row-editor"]').element).toBe(inputElement)
        expect(inputElement.value).toBe('Unsubmitted edit')
        expect(editorAfter.get('[data-test="editor-counter"]').text()).toBe('1')
        expect(document.activeElement).toBe(inputElement)

        await table.get('[data-test="select-all"]').setValue(true)
        await flushPromises()
        const selectedAll = table.emitted('update:selectedKeys')?.at(-1)?.[0] as number[]
        expect(new Set(selectedAll)).toEqual(new Set(rows.map(row => row.id)))
        expect(table.findAll<HTMLInputElement>('[data-test="select-row"]').every(row => row.element.checked)).toBe(true)
      } finally {
        wrapper.unmount()
        heightRead.mockRestore()
      }
    })

    it('updates retained compiled stable slots from deep row, usage and locale state without a table patch', async () => {
      const heightRead = stubShellHeights()
      const state = reactive({
        rows: rowsForShells(60),
        usageById: { 1: { value: 'Initial usage' } } as Record<number, { value: string }>,
        localeState: { locale: 'en', label: 'Name' },
      })
      const tableUpdated = vi.fn()
      const wrapper = mount(DataTableShellFixture, {
        attachTo: document.body,
        props: state,
        global: {
          mixins: [{ updated() { if (this.$options.__name === 'DataTable') tableUpdated() } }],
        },
      })
      const table = wrapper.getComponent(DataTable)
      try {
        await flushPromises()
        const rowElement = table.get('tr[data-row-id="1"]').element
        const editor = wrapper.findAllComponents(DataTableEditorProbe).find(probe => probe.props('rowId') === 1)!
        const editorInstance = editor.vm.$
        const input = editor.get<HTMLInputElement>('[data-test="row-editor"]')
        await input.setValue('Keep draft')
        input.element.focus()
        tableUpdated.mockClear()

        state.rows[0].detail.label = 'Changed deep row'
        await flushPromises()
        expect(table.get('tr[data-row-id="1"] [data-test="row-detail"]').text()).toBe('Changed deep row')

        state.usageById[1] = { value: 'Changed keyed usage' }
        await flushPromises()
        expect(table.get('tr[data-row-id="1"] [data-test="row-usage"]').text()).toBe('Changed keyed usage')

        state.localeState.locale = 'zh'
        await flushPromises()
        expect(table.get('tr[data-row-id="1"] [data-test="row-locale"]').text()).toBe('zh | Name')

        state.localeState.label = '名称'
        await flushPromises()

        const retained = table.get('tr[data-row-id="1"]')
        expect(retained.element).toBe(rowElement)
        expect(retained.get('[data-test="row-locale"]').text()).toBe('zh | 名称')
        expect(wrapper.findAllComponents(DataTableEditorProbe).find(probe => probe.props('rowId') === 1)!.vm.$).toBe(editorInstance)
        expect(retained.get<HTMLInputElement>('[data-test="row-editor"]').element.value).toBe('Keep draft')
        expect(document.activeElement).toBe(input.element)
        expect(tableUpdated).not.toHaveBeenCalled()
      } finally {
        wrapper.unmount()
        heightRead.mockRestore()
      }
    })

    it('updates a real compiled dynamic named slot on the retained logical row', async () => {
      const heightRead = stubShellHeights()
      const wrapper = mount(DataTableDynamicSlotFixture, {
        attachTo: document.body,
        props: { rows: rowsForShells(60), slotName: 'cell-name', label: 'First' },
      })
      const table = wrapper.getComponent(DataTable)
      try {
        await flushPromises()
        const row = table.get('tr[data-row-id="1"]')
        const rowElement = row.element
        expect(row.get('td[data-column="name"] [data-test="dynamic-cell"]').text()).toBe('First 1')

        await wrapper.setProps({ slotName: 'cell-usage', label: 'Second' })
        await flushPromises()

        const retained = table.get('tr[data-row-id="1"]')
        expect(retained.element).toBe(rowElement)
        expect(retained.get('td[data-column="name"]').text()).toBe('Row 1')
        expect(retained.find('td[data-column="name"] [data-test="dynamic-cell"]').exists()).toBe(false)
        expect(retained.get('td[data-column="usage"] [data-test="dynamic-cell"]').text()).toBe('Second 1')
      } finally {
        wrapper.unmount()
        heightRead.mockRestore()
      }
    })

    it('preserves the first editor and its focus across 50 to 51 rows and back', async () => {
      const heightRead = stubShellHeights()
      const rows = rowsForShells(51)
      const wrapper = mount(DataTableShellFixture, {
        attachTo: document.body,
        props: { rows: rows.slice(0, 50) },
      })
      const table = wrapper.getComponent(DataTable)
      try {
        await flushPromises()
        expect((table.vm as any).shouldVirtualize).toBe(false)
        const firstRow = table.get('tr[data-row-id="1"]').element
        const editor = wrapper.findAllComponents(DataTableEditorProbe).find(probe => probe.props('rowId') === 1)!
        const editorInstance = editor.vm.$
        const input = editor.get<HTMLInputElement>('[data-test="row-editor"]')
        const inputElement = input.element
        await input.setValue('Draft across threshold')
        await editor.get('[data-test="editor-counter"]').trigger('click')
        inputElement.focus()

        for (const nextRows of [rows, rows.slice(0, 50)]) {
          await wrapper.setProps({ rows: nextRows })
          await flushPromises()
          expect((table.vm as any).shouldVirtualize).toBe(nextRows.length === 51)
          const retained = table.get('tr[data-row-id="1"]')
          const retainedEditor = wrapper.findAllComponents(DataTableEditorProbe).find(probe => probe.props('rowId') === 1)!
          expect(retained.element).toBe(firstRow)
          expect(retainedEditor.vm.$).toBe(editorInstance)
          expect(retainedEditor.get('[data-test="row-editor"]').element).toBe(inputElement)
          expect(inputElement.value).toBe('Draft across threshold')
          expect(retainedEditor.get('[data-test="editor-counter"]').text()).toBe('1')
          expect(document.activeElement).toBe(inputElement)
        }
      } finally {
        wrapper.unmount()
        heightRead.mockRestore()
      }
    })
  })

  it('keys the virtualizer size cache by row identity, not index (avoids stale heights on sort/filter)', async () => {
    const data = Array.from({ length: 12 }, (_, i) => ({ id: 100 + i, name: `Row ${i + 1}` }))
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data,
        rowKey: 'id',
        virtualizeThreshold: 3
      }
    })

    await wrapper.vm.$nextTick()

    const exposed = (wrapper.vm as any).virtualizer
    const instance = exposed?.value ?? exposed
    // getItemKey must resolve to the row's stable key (id), not the positional index.
    expect(instance.options.getItemKey(0)).toBe(100)
    expect(instance.options.getItemKey(5)).toBe(105)
  })

  it('clears stale row and element caches when pagination replaces the row ID set', async () => {
    const firstPage = Array.from({ length: 100 }, (_, i) => ({ id: i + 1, name: `First ${i + 1}` }))
    const secondPage = Array.from({ length: 100 }, (_, i) => ({ id: i + 101, name: `Second ${i + 1}` }))
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data: firstPage,
        rowKey: 'id',
        virtualizeThreshold: 1
      }
    })

    await wrapper.vm.$nextTick()

    const exposed = (wrapper.vm as any).virtualizer
    const instance = exposed?.value ?? exposed
    const firstPageIDs = firstPage.map(row => row.id)
    ;(instance as any).itemSizeCache = new Map(firstPageIDs.map(id => [id, 156]))
    instance.elementsCache.clear()
    for (const id of firstPageIDs) {
      instance.elementsCache.set(id, document.createElement('tr'))
    }
    const measureElementSpy = vi.spyOn(instance, 'measureElement')

    await wrapper.setProps({ data: secondPage })
    await wrapper.vm.$nextTick()

    const sizeCache = (instance as any).itemSizeCache as Map<number, number>
    expect(sizeCache.size).toBeLessThanOrEqual(secondPage.length)
    expect(instance.elementsCache.size).toBeLessThanOrEqual(secondPage.length)
    expect(firstPageIDs.some(id => sizeCache.has(id))).toBe(false)
    expect(firstPageIDs.some(id => instance.elementsCache.has(id))).toBe(false)
    expect(measureElementSpy.mock.calls.some(([node]) => node === null)).toBe(true)
  })

  it('clears stale caches when equal-length pages replace rows without stable keys', async () => {
    const firstPage = Array.from({ length: 12 }, (_, i) => ({ name: `First ${i + 1}` }))
    const secondPage = Array.from({ length: 12 }, (_, i) => ({ name: `Second ${i + 1}` }))
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data: firstPage,
        virtualizeThreshold: 1
      }
    })

    await wrapper.vm.$nextTick()

    const exposed = (wrapper.vm as any).virtualizer
    const instance = exposed?.value ?? exposed
    const measureElementSpy = vi.spyOn(instance, 'measureElement')

    await wrapper.setProps({ data: secondPage })
    await wrapper.vm.$nextTick()

    expect(measureElementSpy.mock.calls.some(([node]) => node === null)).toBe(true)
  })

  it('conservatively clears caches when duplicate row-key multiplicity changes', async () => {
    const firstPage = [
      { id: 1, name: 'First A' },
      { id: 1, name: 'First B' },
      { id: 2, name: 'First C' }
    ]
    const secondPage = [
      { id: 1, name: 'Second A' },
      { id: 2, name: 'Second B' },
      { id: 2, name: 'Second C' }
    ]
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data: firstPage,
        rowKey: 'id',
        virtualizeThreshold: 1
      }
    })

    await wrapper.vm.$nextTick()

    const exposed = (wrapper.vm as any).virtualizer
    const instance = exposed?.value ?? exposed
    const measureElementSpy = vi.spyOn(instance, 'measureElement')

    await wrapper.setProps({ data: secondPage })
    await wrapper.vm.$nextTick()

    expect(measureElementSpy.mock.calls.some(([node]) => node === null)).toBe(true)
  })

  it('preserves cache when rows without stable keys only reorder the same objects', async () => {
    const data = Array.from({ length: 12 }, (_, i) => ({ name: `Row ${i + 1}` }))
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data,
        virtualizeThreshold: 1
      }
    })

    await wrapper.vm.$nextTick()

    const exposed = (wrapper.vm as any).virtualizer
    const instance = exposed?.value ?? exposed
    const measureSpy = vi.spyOn(instance, 'measure')

    await wrapper.setProps({ data: [...data].reverse() })
    await wrapper.vm.$nextTick()

    expect(measureSpy).not.toHaveBeenCalled()
  })

  it('preserves stable row height cache when the same row IDs are only reordered', async () => {
    const data = Array.from({ length: 100 }, (_, i) => ({ id: i + 1, name: `Row ${i + 1}` }))
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data,
        rowKey: 'id',
        virtualizeThreshold: 1
      }
    })

    await wrapper.vm.$nextTick()

    const exposed = (wrapper.vm as any).virtualizer
    const instance = exposed?.value ?? exposed
    ;(instance as any).itemSizeCache = new Map(data.map(row => [row.id, 156]))
    const measureSpy = vi.spyOn(instance, 'measure')

    await wrapper.setProps({ data: [...data].reverse() })
    await wrapper.vm.$nextTick()

    const sizeCache = (instance as any).itemSizeCache as Map<number, number>
    expect(measureSpy).not.toHaveBeenCalled()
    expect(sizeCache.size).toBe(100)
  })

  it('emits controlled current-page selection while preserving off-page keys', async () => {
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data: [
          { id: 1, name: 'One' },
          { id: 2, name: 'Two' }
        ],
        rowKey: 'id',
        selectable: true,
        selectedKeys: [99]
      }
    })

    await wrapper.get('[data-test="select-all"]').setValue(true)

    const selectedAll = wrapper.emitted('update:selectedKeys')?.at(-1)?.[0]
    expect(selectedAll).toEqual([99, 1, 2])

    await wrapper.setProps({ selectedKeys: selectedAll as number[] })
    const rowCheckboxes = wrapper.findAll<HTMLInputElement>('[data-test="select-row"]')
    expect(rowCheckboxes.every((checkbox) => checkbox.element.checked)).toBe(true)

    await rowCheckboxes[0].setValue(false)

    expect(wrapper.emitted('update:selectedKeys')?.at(-1)?.[0]).toEqual([99, 2])
    expect(wrapper.emitted('selectionChange')?.at(-1)?.[0]).toEqual([99, 2])
  })

  it('keeps the single usage field shrinkable in a 320px mobile card', () => {
    stubMobileMatchMedia()
    const viewport = document.createElement('div')
    viewport.style.width = '320px'
    document.body.appendChild(viewport)
    const wrapper = mount(DataTable, {
      attachTo: viewport,
      props: {
        columns: [{ key: 'usage', label: 'Usage' }],
        data: [{ id: 1, usage: 'snapshot' }],
        rowKey: 'id'
      },
      slots: {
        'cell-usage': '<div data-test="usage-cell">snapshot</div>'
      }
    })

    expect(viewport.style.width).toBe('320px')
    expect(wrapper.findAll('[data-field="usage"]')).toHaveLength(1)
    expect(wrapper.find('[data-field="ollama_cloud_usage"]').exists()).toBe(false)
    const field = wrapper.get('[data-field="usage"]')
    expect(field.classes()).toContain('min-w-0')
    expect(field.get('div').classes()).toEqual(expect.arrayContaining(['min-w-0', 'max-w-full']))
    expect(wrapper.findAll('[data-test="usage-cell"]')).toHaveLength(1)

    wrapper.unmount()
    viewport.remove()
  })

  it('offers current-page select all in the mobile card layout', async () => {
    stubMobileMatchMedia()
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data: [
          { id: 1, name: 'One' },
          { id: 2, name: 'Two' }
        ],
        rowKey: 'id',
        selectable: true,
        selectedKeys: [99]
      }
    })

    await wrapper.get('[data-test="select-all-mobile"]').setValue(true)

    expect(wrapper.emitted('update:selectedKeys')?.at(-1)?.[0]).toEqual([99, 1, 2])
  })

  describe('stacked cells', () => {
    const stackedColumns = (extra: Partial<Column> = {}): Column[] => [
      {
        key: 'name',
        label: 'Name',
        sortable: true,
        stack: [
          { key: 'id', label: 'ID', sortable: true },
          { key: 'notes', label: 'Notes', cellLabel: true }
        ],
        ...extra
      },
      { key: 'status', label: 'Status' }
    ]
    const rows = [{ id: 7, name: 'Alpha', notes: 'first', status: 'ok' }]

    // matchMedia whose (min-width: Npx) queries match from the given width up; resize() crosses breakpoints
    const stubViewport = (width: number) => {
      const queries: Array<{ matches: boolean; min: number; listeners: Array<() => void> }> = []
      const minOf = (query: string) => Number(/min-width:\s*(\d+)px/.exec(query)?.[1] ?? 0)
      Object.defineProperty(window, 'matchMedia', {
        writable: true,
        value: vi.fn().mockImplementation((query: string) => {
          const entry = { min: minOf(query), matches: width >= minOf(query), listeners: [] as Array<() => void> }
          queries.push(entry)
          return {
            get matches() {
              return entry.matches
            },
            media: query,
            onchange: null,
            addEventListener: (_type: string, fn: () => void) => entry.listeners.push(fn),
            removeEventListener: vi.fn(),
            addListener: (fn: () => void) => entry.listeners.push(fn),
            removeListener: vi.fn(),
            dispatchEvent: vi.fn()
          }
        })
      })
      return {
        resize(next: number) {
          for (const entry of queries) {
            const matches = next >= entry.min
            if (matches === entry.matches) continue
            entry.matches = matches
            entry.listeners.forEach((fn) => fn())
          }
        }
      }
    }

    it('draws the parts as lines of the host cell, each with its own header label and sort', async () => {
      setConsoleTheme(true)
      const wrapper = mount(DataTable, {
        props: { columns: stackedColumns(), data: rows, serverSideSort: true },
        slots: { 'cell-id': '<template #cell-id="{ row }"><span data-test="id-cell">#{{ row.id }}</span></template>' }
      })
      await wrapper.vm.$nextTick()

      expect(wrapper.findAll('thead th').map((th) => th.attributes('data-column'))).toEqual(['name', 'status'])
      const head = wrapper.get('th[data-column="name"]')
      expect(head.attributes('data-stack')).toBe('')
      // a sortable part: its label sorts on a click, its button (keyboard) names the part and the order
      const idSub = head.get('div[data-ui="th-sub"][data-line="id"]')
      expect(idSub.attributes('data-sort')).toBe('none')
      const idSort = idSub.get('button[data-ui="th-sub-sort"]')
      expect(idSort.attributes('aria-label')).toBe('common.sortByColumn')
      expect(idSort.attributes('aria-sort')).toBeUndefined()
      const notesSub = head.get('div[data-ui="th-sub"][data-line="notes"]')
      expect(notesSub.text()).toBe('Notes')
      expect(notesSub.attributes('data-sort')).toBeUndefined()
      expect(notesSub.find('button').exists()).toBe(false)

      const cell = wrapper.get('td[data-column="name"]')
      expect(cell.text()).toContain('Alpha')
      const lines = cell.findAll('[data-ui="cell-lines"] > [data-ui="cell-line"]')
      expect(lines.map((line) => line.attributes('data-line'))).toEqual(['id', 'notes'])
      expect(lines[0].get('[data-test="id-cell"]').text()).toBe('#7')
      expect(lines[1].get('[data-ui="cell-line-label"]').text()).toBe('Notes')
      expect(lines[1].get('[data-ui="cell-line-value"]').text()).toBe('first')

      await idSort.trigger('click')
      expect(wrapper.emitted('sort')).toEqual([['id', 'asc']])
      expect(idSub.attributes('data-sort')).toBe('ascending')
      expect(idSort.attributes('aria-label')).toBe('common.sortByColumnAscending')
      expect(head.attributes('aria-sort')).toBe('none')

      await idSub.get('span').trigger('click')
      expect(wrapper.emitted('sort')).toEqual([['id', 'asc'], ['id', 'desc']])
      expect(idSub.attributes('data-sort')).toBe('descending')
      await notesSub.trigger('click')
      expect(wrapper.emitted('sort')).toHaveLength(2)
    })

    it('draws every part as a column of its own from stackBelow up and without the console theme', async () => {
      setConsoleTheme(true)
      const viewport = stubViewport(1440)
      const wrapper = mount(DataTable, {
        props: { columns: stackedColumns({ stackBelow: 1800 }), data: rows }
      })
      await wrapper.vm.$nextTick()
      const keys = () => wrapper.findAll('thead th').map((th) => th.attributes('data-column'))
      expect(keys()).toEqual(['name', 'status'])

      viewport.resize(1920)
      await wrapper.vm.$nextTick()
      expect(keys()).toEqual(['name', 'id', 'notes', 'status'])
      expect(wrapper.find('[data-ui="cell-lines"]').exists()).toBe(false)

      viewport.resize(1280)
      await wrapper.vm.$nextTick()
      expect(keys()).toEqual(['name', 'status'])

      setConsoleTheme(false)
      await wrapper.vm.$nextTick()
      expect(keys()).toEqual(['name', 'id', 'notes', 'status'])
    })

    it('keeps a hidden host for its parts without its own content or sort', async () => {
      setConsoleTheme(true)
      const wrapper = mount(DataTable, {
        props: { columns: stackedColumns({ hostHidden: true, sortable: false }), data: rows }
      })
      await wrapper.vm.$nextTick()

      const head = wrapper.get('th[data-column="name"]')
      expect(head.attributes('aria-sort')).toBeUndefined()
      expect(head.text()).not.toContain('Name')
      const cell = wrapper.get('td[data-column="name"]')
      expect(cell.text()).not.toContain('Alpha')
      expect(cell.get('[data-ui="cell-lines"]').attributes('data-host-line')).toBe('')
      expect(cell.findAll('[data-ui="cell-line"]')).toHaveLength(2)
    })
  })
})
