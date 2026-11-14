import { computed, nextTick, onMounted, onBeforeUnmount, ref, watch, type ComponentPublicInstance, type Ref } from 'vue'
import { useMediaQuery } from '@vueuse/core'
import { defaultRangeExtractor, observeElementRect, useVirtualizer, useWindowVirtualizer, type Virtualizer } from '@tanstack/vue-virtual'
import type { Account } from '@/types'

/** Account lists and card grids share one measured window; selection stays in the page. */
export function useAccountVirtualRows(
  accounts: Ref<Account[]>,
  scrollElement: Ref<HTMLElement | null>,
  columns: Ref<number>,
  estimateSize: number,
  gap = 0,
  stableSlots = false,
) {
  const virtual = computed(() => accounts.value.length > 50)
  const supportsSlotOrder = stableSlots &&
    globalThis.CSS?.supports?.('reading-flow', 'source-order') === true &&
    globalThis.CSS.supports('reading-order', '1')
  const slotWindow = computed(() => virtual.value && supportsSlotOrder)
  const windowScrolling = useMediaQuery('(max-width: 1023px)')
  const scrollMargin = ref(0)
  const rows = computed(() => Array.from(
    { length: Math.ceil(accounts.value.length / columns.value) },
    (_, index) => {
      const items = accounts.value.slice(index * columns.value, (index + 1) * columns.value)
      return { index, accounts: items, key: items.map(account => account.id).join(':') }
    },
  ))
  let slotWindowSize = 0
  const stableRangeExtractor = (range: Parameters<typeof defaultRangeExtractor>[0]) => {
    slotWindowSize = Math.min(range.count, Math.max(slotWindowSize,
      range.endIndex - range.startIndex + 1 + 2 * range.overscan))
    const start = Math.max(0, Math.min(range.startIndex - range.overscan, range.count - slotWindowSize))
    return Array.from({ length: slotWindowSize }, (_, index) => start + index)
  }
  const commonOptions = computed(() => {
    const currentRows = rows.value
    return {
      count: currentRows.length,
      getItemKey: (index: number) => currentRows[index].key,
      estimateSize: () => estimateSize,
      overscan: columns.value === 1 ? 5 : 2,
      rangeExtractor: slotWindow.value ? stableRangeExtractor : defaultRangeExtractor,
      gap,
      indexAttribute: 'data-account-virtual-index',
      // ResizeObserver already batches geometry; avoid another frame of size correction.
      useAnimationFrameWithResizeObserver: false,
    }
  })
  const elementVirtualizer = useVirtualizer(computed(() => ({
    ...commonOptions.value,
    enabled: virtual.value && !windowScrolling.value,
    getScrollElement: () => scrollElement.value,
    initialRect: { width: 0, height: Math.max(window.innerHeight - 320, 400) },
    // A temporarily hidden flex container must not erase the last usable viewport.
    observeElementRect: (instance: Virtualizer<HTMLElement, Element>, callback: (rect: { width: number; height: number }) => void) => observeElementRect(instance, rect => {
      if (rect.height > 0) callback(rect)
    }),
  })))
  const windowVirtualizer = useWindowVirtualizer(computed(() => ({
    ...commonOptions.value,
    enabled: virtual.value && windowScrolling.value,
    scrollMargin: scrollMargin.value,
  })))
  const elementInstance = elementVirtualizer.value
  const windowInstance = windowVirtualizer.value
  // Read the active shallowRef directly: a computed returning the same Virtualizer object suppresses triggerRef updates.
  const activeVirtualizer = () => windowScrolling.value ? windowVirtualizer.value : elementVirtualizer.value

  const updateScrollMargin = () => {
    if (windowScrolling.value && scrollElement.value) {
      scrollMargin.value = scrollElement.value.getBoundingClientRect().top + window.scrollY
    }
  }
  let positionObserver: ResizeObserver | null = null
  onMounted(() => {
    updateScrollMargin()
    window.addEventListener('resize', updateScrollMargin)
    positionObserver = new ResizeObserver(updateScrollMargin)
    if (scrollElement.value) {
      positionObserver.observe(scrollElement.value)
      // Filter and taxonomy wrapping can move the list without a viewport resize.
      scrollElement.value.closest('.table-page-layout')?.querySelectorAll('.layout-section-fixed, .layout-section-scrollable')
        .forEach(section => positionObserver!.observe(section))
    }
  })
  onBeforeUnmount(() => {
    positionObserver?.disconnect()
    window.removeEventListener('resize', updateScrollMargin)
  })
  watch([windowScrolling, columns], () => { void nextTick(updateScrollMargin) }, { flush: 'post' })
  // TanStack memoizes the items array while the range is unchanged. Preserve that boundary before mapping rows.
  const virtualItems = computed(() => activeVirtualizer().getVirtualItems())
  const totalSize = computed(() => activeVirtualizer().getTotalSize())
  const rowScrollMargin = computed(() => activeVirtualizer().options.scrollMargin)
  const renderRows = computed(() => virtual.value
    ? virtualItems.value.map(item => ({ ...rows.value[item.index], start: item.start }))
    : rows.value.map(row => ({ ...row, start: 0 })),
  )
  type RenderRow = (typeof renderRows.value)[number]
  const accountSlots = new Map<string, number>()
  let slotCount = 0
  const renderSlots = computed(() => {
    const visibleRows = renderRows.value
    if (!slotWindow.value) {
      accountSlots.clear()
      slotCount = 0
      return visibleRows.map(row => ({ key: row.key, row: row as RenderRow | null }))
    }
    const visibleKeys = new Set(visibleRows.map(row => row.key))
    for (const key of accountSlots.keys()) {
      if (!visibleKeys.has(key)) accountSlots.delete(key)
    }
    const instance = windowScrolling.value ? windowInstance : elementInstance
    const reserve = instance.isScrolling ? 0 : instance.options.overscan
    slotCount = Math.max(slotCount, visibleRows.length + reserve)
    const occupiedSlots = new Set(accountSlots.values())
    const freeSlots = Array.from({ length: slotCount }, (_, index) => index)
      .filter(index => !occupiedSlots.has(index))
    const rowsBySlot = new Map<number, RenderRow>()
    for (const row of visibleRows) {
      let slot = accountSlots.get(row.key)
      if (slot === undefined) {
        slot = freeSlots.shift() ?? slotCount++
        accountSlots.set(row.key, slot)
      }
      rowsBySlot.set(slot, row)
    }
    // Keep canvas children in physical slot order; their account content is managed within each slot.
    return Array.from({ length: slotCount }, (_, index) => ({
      key: `slot:${index}`,
      row: rowsBySlot.get(index) ?? null,
    }))
  })
  const containerStyle = computed(() => virtual.value
    ? { position: 'relative' as const,
      ...(slotWindow.value ? { readingFlow: 'source-order' } : {}) }
    : undefined,
  )
  const spacerStyle = computed(() => virtual.value ? { height: `${totalSize.value}px`, flexShrink: 0 } : undefined)
  const rowStyle = (start: number) => virtual.value
    ? { position: 'absolute' as const, top: 0, left: 0, width: '100%', transform: `translateY(${start - rowScrollMargin.value}px)` }
    : undefined
  const slotStyle = (row: RenderRow | null) => row
    ? { ...rowStyle(row.start), ...(slotWindow.value ? { readingOrder: row.index } : {}) }
    : { display: 'none' }
  const pendingMeasurements = new Set<HTMLElement>()
  const recycledMeasurements = new WeakMap<HTMLElement, number | undefined>()
  let measurementQueued = false
  const scheduleMeasurements = () => {
    if (measurementQueued) return
    measurementQueued = true
    void nextTick(() => {
      measurementQueued = false
      const current = activeVirtualizer()
      const recycledSizes = Array.from(pendingMeasurements)
        .filter(node => node.isConnected && recycledMeasurements.has(node))
        .map(node => [node, recycledMeasurements.get(node) ?? node.offsetHeight] as const)
      current.measureElement(null)
      // An unchanged physical row emits no RO entry; seed its new key before idle measurement can refine it.
      for (const [node, size] of recycledSizes) {
        current.resizeItem(current.indexFromElement(node), size)
      }
      for (const node of pendingMeasurements) {
        if (node.isConnected) current.measureElement(node)
        recycledMeasurements.delete(node)
      }
      pendingMeasurements.clear()
    })
  }
  const measureRow = (element: Element | ComponentPublicInstance | null) => {
    if (!virtual.value || !(element instanceof HTMLElement)) return
    // Function refs run inside the component patch effect; do not subscribe that effect to scroll triggerRef calls.
    const instance = windowScrolling.value ? windowInstance : elementInstance
    const key = instance.options.getItemKey(instance.indexFromElement(element))
    for (const [cachedKey, node] of instance.elementsCache) {
      if (node === element && cachedKey !== key) {
        instance['observer'].unobserve(element)
        instance.elementsCache.delete(cachedKey)
        // Read only TanStack 3.13.23's locked measured-size cache, never its estimated measurements.
        const sizes = instance['itemSizeCache']
        recycledMeasurements.set(element, sizes.get(key) ?? sizes.get(cachedKey))
      }
    }
    if (instance.elementsCache.get(key) === element) return
    pendingMeasurements.add(element)
    scheduleMeasurements()
  }

  // Card regrouping changes height ownership; a new page also releases detached measurements.
  watch(
    () => `${windowScrolling.value}/${columns.value}/${accounts.value.map(account => account.id).join(',')}`,
    () => {
      const instance = activeVirtualizer()
      instance.measureElement(null)
      instance.measure()
      // Explicit geometry invalidation also remeasures retained rows, once after this patch.
      for (const element of instance.elementsCache.values()) {
        pendingMeasurements.add(element as HTMLElement)
      }
      if (pendingMeasurements.size) scheduleMeasurements()
    },
    { flush: 'post' },
  )

  return { renderRows, renderSlots, slotWindow, windowScrolling, containerStyle, spacerStyle, rowStyle, slotStyle, measureRow }
}
