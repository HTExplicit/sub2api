import { computed, nextTick, onMounted, onBeforeUnmount, ref, watch, type ComponentPublicInstance, type Ref } from 'vue'
import { useMediaQuery } from '@vueuse/core'
import { observeElementRect, useVirtualizer, useWindowVirtualizer, type Virtualizer } from '@tanstack/vue-virtual'
import type { Account } from '@/types'

/** Account lists and card grids share one measured window; selection stays in the page. */
export function useAccountVirtualRows(
  accounts: Ref<Account[]>,
  scrollElement: Ref<HTMLElement | null>,
  columns: Ref<number>,
  estimateSize: number,
  gap = 0,
) {
  const virtual = computed(() => accounts.value.length > 50)
  const windowScrolling = useMediaQuery('(max-width: 1023px)')
  const scrollMargin = ref(0)
  const rows = computed(() => Array.from(
    { length: Math.ceil(accounts.value.length / columns.value) },
    (_, index) => {
      const items = accounts.value.slice(index * columns.value, (index + 1) * columns.value)
      return { index, accounts: items, key: items.map(account => account.id).join(':') }
    },
  ))
  const commonOptions = computed(() => {
    const currentRows = rows.value
    return {
      count: currentRows.length,
      getItemKey: (index: number) => currentRows[index].key,
      estimateSize: () => estimateSize,
      overscan: columns.value === 1 ? 5 : 2,
      gap,
      useAnimationFrameWithResizeObserver: true,
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
  const containerStyle = computed(() => virtual.value
    ? { height: `${totalSize.value}px`, position: 'relative' as const }
    : undefined,
  )
  const rowStyle = (start: number) => virtual.value
    ? { position: 'absolute' as const, top: 0, left: 0, width: '100%', transform: `translateY(${start - rowScrollMargin.value}px)` }
    : undefined
  const pendingMeasurements = new Set<HTMLElement>()
  let measurementQueued = false
  const scheduleMeasurements = () => {
    if (measurementQueued) return
    measurementQueued = true
    void nextTick(() => {
      measurementQueued = false
      const current = activeVirtualizer()
      current.measureElement(null)
      for (const node of pendingMeasurements) {
        if (node.isConnected) current.measureElement(node)
      }
      pendingMeasurements.clear()
    })
  }
  const measureRow = (element: Element | ComponentPublicInstance | null) => {
    if (!virtual.value || !(element instanceof HTMLElement)) return
    // Function refs run inside the component patch effect; do not subscribe that effect to scroll triggerRef calls.
    const instance = windowScrolling.value ? windowInstance : elementInstance
    const key = instance.options.getItemKey(Number(element.dataset.index))
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

  return { renderRows, containerStyle, rowStyle, measureRow }
}
