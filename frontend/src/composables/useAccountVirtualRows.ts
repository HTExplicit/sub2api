import { computed, nextTick, watch, type ComponentPublicInstance, type Ref } from 'vue'
import { observeElementRect, useVirtualizer, type Virtualizer } from '@tanstack/vue-virtual'
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
  const rows = computed(() => Array.from(
    { length: Math.ceil(accounts.value.length / columns.value) },
    (_, index) => {
      const items = accounts.value.slice(index * columns.value, (index + 1) * columns.value)
      return { index, accounts: items, key: items.map(account => account.id).join(':') }
    },
  ))
  const virtualizer = useVirtualizer(computed(() => ({
    count: virtual.value ? rows.value.length : 0,
    getScrollElement: () => scrollElement.value,
    getItemKey: (index: number) => rows.value[index].key,
    estimateSize: () => estimateSize,
    overscan: columns.value === 1 ? 5 : 2,
    gap,
    initialRect: { width: 0, height: Math.max(window.innerHeight - 320, 400) },
    // A temporarily hidden flex container must not erase the last usable viewport.
    observeElementRect: (instance: Virtualizer<HTMLElement, Element>, callback: (rect: { width: number; height: number }) => void) => observeElementRect(instance, rect => {
      if (rect.height > 0) callback(rect)
    }),
    useAnimationFrameWithResizeObserver: true,
  })))
  const renderRows = computed(() => virtual.value
    ? virtualizer.value.getVirtualItems().map(item => ({ ...rows.value[item.index], start: item.start }))
    : rows.value.map(row => ({ ...row, start: 0 })),
  )
  const containerStyle = computed(() => virtual.value
    ? { height: `${virtualizer.value.getTotalSize()}px`, position: 'relative' as const }
    : undefined,
  )
  const rowStyle = (start: number) => virtual.value
    ? { position: 'absolute' as const, top: 0, left: 0, width: '100%', transform: `translateY(${start}px)` }
    : undefined
  const pendingMeasurements = new Set<HTMLElement>()
  let measurementQueued = false
  const scheduleMeasurements = () => {
    if (measurementQueued) return
    measurementQueued = true
    void nextTick(() => {
      measurementQueued = false
      const current = virtualizer.value
      current.measureElement(null)
      for (const node of pendingMeasurements) {
        if (node.isConnected) current.measureElement(node)
      }
      pendingMeasurements.clear()
    })
  }
  const measureRow = (element: Element | ComponentPublicInstance | null) => {
    if (!virtual.value || !(element instanceof HTMLElement)) return
    const instance = virtualizer.value
    const key = instance.options.getItemKey(Number(element.dataset.index))
    if (instance.elementsCache.get(key) === element) return
    pendingMeasurements.add(element)
    scheduleMeasurements()
  }

  // Card regrouping changes height ownership; a new page also releases detached measurements.
  watch(
    () => `${columns.value}/${accounts.value.map(account => account.id).join(',')}`,
    () => {
      virtualizer.value.measureElement(null)
      virtualizer.value.measure()
      // Explicit geometry invalidation also remeasures retained rows, once after this patch.
      for (const element of virtualizer.value.elementsCache.values()) {
        pendingMeasurements.add(element as HTMLElement)
      }
      if (pendingMeasurements.size) scheduleMeasurements()
    },
    { flush: 'post' },
  )

  return { renderRows, containerStyle, rowStyle, measureRow }
}
