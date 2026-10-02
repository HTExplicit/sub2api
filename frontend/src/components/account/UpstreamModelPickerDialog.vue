<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.syncUpstreamPicker.title')"
    width="wide"
    @close="emit('close')"
  >
    <div data-testid="upstream-model-picker" class="space-y-3">
      <div
        v-if="snapshot?.model_list_source === 'configured'"
        data-testid="upstream-picker-configured-notice"
        class="rounded-lg border border-blue-200 bg-blue-50 p-3 text-sm text-blue-700 dark:border-blue-800 dark:bg-blue-900/20 dark:text-blue-300"
      >
        {{ t('admin.accounts.syncUpstreamPicker.configuredSource') }}
      </div>
      <div
        v-if="metadataNotice"
        data-testid="upstream-picker-metadata-notice"
        class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-700 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-400"
      >
        {{ metadataNotice }}
      </div>

      <div class="flex flex-wrap items-center gap-2">
        <div data-ui="search-box" class="relative min-w-[12rem] flex-1">
          <Icon name="search" size="md" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
          <input
            ref="searchRef"
            v-model="searchQuery"
            type="text"
            data-testid="upstream-picker-search"
            class="input pl-10"
            :placeholder="t('admin.accounts.syncUpstreamPicker.searchPlaceholder')"
            :aria-label="t('admin.accounts.syncUpstreamPicker.searchPlaceholder')"
            @keydown.enter.prevent
          />
        </div>
        <button type="button" data-testid="upstream-picker-select-all" class="btn btn-secondary" @click="selectAll">
          {{ t('admin.accounts.syncUpstreamPicker.selectAll') }}
        </button>
        <button type="button" data-testid="upstream-picker-invert" class="btn btn-secondary" @click="invertSelection">
          {{ t('admin.accounts.syncUpstreamPicker.invert') }}
        </button>
      </div>

      <div class="flex flex-wrap items-center justify-between gap-2">
        <div
          role="radiogroup"
          data-ui="seg"
          data-testid="upstream-picker-filter"
          class="inline-flex flex-wrap gap-0.5 rounded-lg bg-gray-100 p-0.5 dark:bg-dark-700"
          :aria-label="t('admin.accounts.syncUpstreamPicker.filterLabel')"
          @keydown="moveFilter"
        >
          <button
            v-for="option in filterOptions"
            :key="option.value"
            type="button"
            role="radio"
            data-testid="upstream-picker-filter-option"
            :data-filter="option.value"
            :aria-checked="statusFilter === option.value"
            :tabindex="statusFilter === option.value ? 0 : -1"
            :class="[
              'rounded-md px-3 py-1.5 text-xs font-medium transition-colors',
              statusFilter === option.value
                ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-600 dark:text-white'
                : 'text-gray-600 hover:text-gray-900 dark:text-gray-400 dark:hover:text-gray-200'
            ]"
            @click="statusFilter = option.value"
          >
            {{ option.label }}
            <span class="ml-1 tabular-nums text-gray-400 dark:text-gray-500">{{ option.count }}</span>
          </button>
        </div>
        <span data-testid="upstream-picker-counter" class="text-xs tabular-nums text-gray-500 dark:text-gray-400">
          {{ t('admin.accounts.syncUpstreamPicker.selectedCount', { selected: checkedCount, total: rows.length }) }}
        </span>
      </div>

      <!-- The list area keeps the height it opened with (listHeight), so the search and the status filter never
           resize the dialog; a long list renders only the rows in and near view between two spacers. -->
      <div
        ref="listRef"
        data-testid="upstream-picker-list"
        data-ui="upstream-picker-list"
        class="max-h-[50vh] min-h-[4.5rem] overflow-y-auto overscroll-contain rounded-lg border border-gray-200 dark:border-dark-600"
        :style="listHeight ? { height: listHeight } : undefined"
      >
        <ul v-if="visibleRows.length > 0">
          <li v-if="paddingTop > 0" aria-hidden="true" :style="{ height: `${paddingTop}px` }"></li>
          <li
            v-for="{ row, index } in renderedRows"
            :key="row.id"
            v-memo="[row, index, checked.has(row.id), labels]"
            :ref="measureRow"
            data-testid="upstream-picker-row"
            data-ui="upstream-picker-row"
            :data-index="index"
            :data-model-id="row.id"
            :data-checked="checked.has(row.id)"
            class="flex cursor-pointer items-start gap-2.5 border-t border-gray-100 px-3 py-2 first:border-t-0 hover:bg-gray-50 dark:border-dark-700 dark:hover:bg-dark-700/60"
            @click="toggle(row, index, $event)"
          >
            <input
              type="checkbox"
              data-testid="upstream-picker-checkbox"
              class="mt-0.5 h-4 w-4 shrink-0 cursor-pointer rounded border-gray-300 text-primary-600 focus:ring-primary-500 dark:border-dark-500"
              :checked="checked.has(row.id)"
              :aria-label="row.id"
              :aria-describedby="describedBy(row)"
              @click.stop="toggle(row, index, $event)"
            />
            <ModelIcon :model="row.id" size="18px" class="mt-px shrink-0" />
            <div class="min-w-0 flex-1">
              <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
                <span
                  data-testid="upstream-picker-model-id"
                  data-ui="upstream-picker-id"
                  class="min-w-0 break-all text-sm font-medium text-gray-900 dark:text-white"
                >{{ row.id }}</span>
                <span
                  v-if="row.status === 'new'"
                  :id="partId(row, 'tag')"
                  data-testid="upstream-picker-tag-new"
                  class="badge badge-primary"
                >
                  {{ labels.newTag }}
                </span>
                <span
                  v-else-if="row.status === 'missing'"
                  :id="partId(row, 'tag')"
                  data-testid="upstream-picker-tag-missing"
                  class="badge badge-warning"
                >
                  {{ labels.missingTag }}
                </span>
              </div>
              <p
                v-if="row.hasDetails"
                :id="partId(row, 'details')"
                data-testid="upstream-picker-details"
                data-ui="upstream-picker-details"
                class="mt-0.5 break-words text-xs text-gray-500 dark:text-gray-400"
              >
                {{ detailsOf(row) }}
              </p>
            </div>
            <button
              type="button"
              data-testid="upstream-picker-copy"
              data-ui="upstream-picker-copy"
              class="-my-1 shrink-0 rounded p-1.5 text-gray-400 transition-colors hover:bg-gray-200 hover:text-primary-600 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-gray-500 dark:hover:bg-dark-600 dark:hover:text-primary-400"
              :title="`${labels.copy} ${row.id}`"
              :aria-label="`${labels.copy} ${row.id}`"
              @click.stop="copyModelId(row.id)"
            >
              <Icon name="copy" size="sm" />
            </button>
          </li>
          <li v-if="paddingBottom > 0" aria-hidden="true" :style="{ height: `${paddingBottom}px` }"></li>
        </ul>
        <p
          v-else
          data-testid="upstream-picker-empty"
          class="px-3 py-6 text-center text-sm text-gray-500 dark:text-gray-400"
        >
          {{ t('admin.accounts.noMatchingModels') }}
        </p>
      </div>
    </div>

    <template #footer>
      <div data-testid="upstream-picker-footer" class="flex w-full flex-wrap items-center justify-between gap-3">
        <div class="min-w-0 text-sm">
          <p data-testid="upstream-picker-summary" class="text-gray-700 dark:text-gray-300">
            {{ t('admin.accounts.syncUpstreamPicker.summary', changes) }}
          </p>
          <p v-if="checkedCount === 0" data-testid="upstream-picker-empty-hint" class="mt-0.5 text-xs text-amber-600 dark:text-amber-400">
            {{ t('admin.accounts.syncUpstreamPicker.emptySelectionHint') }}
          </p>
        </div>
        <div class="flex shrink-0 gap-2">
          <button type="button" data-testid="upstream-picker-cancel" class="btn btn-secondary" @click="emit('close')">
            {{ t('common.cancel') }}
          </button>
          <button
            type="button"
            data-testid="upstream-picker-confirm"
            class="btn btn-primary"
            :disabled="checkedCount === 0"
            @click="confirm"
          >
            {{ t('common.confirm') }}
          </button>
        </div>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, shallowRef, useId, watch } from 'vue'
import type { ComponentPublicInstance } from 'vue'
import { observeElementRect, useVirtualizer } from '@tanstack/vue-virtual'
import type { Rect, Virtualizer } from '@tanstack/vue-virtual'
import { useI18n } from 'vue-i18n'
import type { SyncUpstreamModelsResult, UpstreamModelMetadata } from '@/api/admin/accounts'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ModelIcon from '@/components/common/ModelIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import { useClipboard } from '@/composables/useClipboard'
import { formatContextCapacity } from '@/utils/modelContextCapacity'

type RowStatus = 'kept' | 'new' | 'missing'
type StatusFilter = 'all' | 'checked' | 'unchecked' | 'new' | 'missing'

interface PickerRow {
  id: string
  // the row's place in the sorted snapshot: stable while the dialog is open, so element ids derive from it
  position: number
  // kept: returned and whitelisted; new: returned only; missing: whitelisted but not returned
  status: RowStatus
  metadata?: UpstreamModelMetadata
  // whether the capability line has anything to show (its text is built when the row first renders)
  hasDetails: boolean
  idKey: string
  nameKey: string
}

const props = defineProps<{
  show: boolean
  result?: SyncUpstreamModelsResult
  current: string[]
}>()

const emit = defineEmits<{
  confirm: [models: string[], changes: { added: number; removed: number }]
  close: []
}>()

const { t } = useI18n()
const { copyToClipboard } = useClipboard()

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' })
const compareModelIds = (a: string, b: string) => collator.compare(a, b) || (a < b ? -1 : a > b ? 1 : 0)
const FILTER_STEPS: Record<string, number> = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }

// A list longer than this renders only the rows in and near view (DataTable's threshold); a shorter one renders whole.
const VIRTUALIZE_THRESHOLD = 100
// Row heights before a row is measured: the ID line alone, and the ID line over the capability line.
const ROW_HEIGHT = 36
const ROW_WITH_DETAILS_HEIGHT = 54
const ROW_OVERSCAN = 6
// The list area's cap (its max-h-[50vh] class).
const LIST_MAX_HEIGHT = '50vh'

// Rows and the whitelist are a snapshot taken when the dialog opens; per-row lookups stay O(1). The snapshot
// outlives a close, so the closing transition still shows it; the next open replaces it.
const snapshot = shallowRef<SyncUpstreamModelsResult>()
const rows = shallowRef<PickerRow[]>([])
const currentModels = shallowRef<string[]>([])
const checked = shallowRef(new Set<string>())
const searchQuery = ref('')
const statusFilter = ref<StatusFilter>('all')
const searchRef = ref<HTMLInputElement | null>(null)
const listRef = ref<HTMLElement | null>(null)
// The list area's CSS height, fixed after each open: '' while it takes its natural height at open.
const listHeight = ref('')

const idPrefix = `upstream-picker-${useId()}`

const isTokenCount = (value?: number): value is number => !!value && Number.isSafeInteger(value) && value > 0

const displayName = (id: string, metadata?: UpstreamModelMetadata) => {
  const name = metadata?.display_name?.trim()
  return name && name !== id ? name : ''
}

// The capability line costs two translations, so it is built when its row first renders (a long list renders few
// of its rows) and kept per row; a new locale builds the lines again.
let detailsCache = new WeakMap<PickerRow, string>()
const detailsOf = (row: PickerRow) => {
  let details = detailsCache.get(row)
  if (details === undefined) {
    const context = row.metadata?.context_window
    const output = row.metadata?.max_output_tokens
    const parts = [displayName(row.id, row.metadata)]
    if (isTokenCount(context)) {
      parts.push(t('admin.accounts.syncUpstreamPicker.contextWindow', { value: formatContextCapacity(context) }))
    }
    if (isTokenCount(output)) {
      parts.push(t('admin.accounts.syncUpstreamPicker.maxOutput', { value: formatContextCapacity(output) }))
    }
    details = parts.filter(Boolean).join(' · ')
    detailsCache.set(row, details)
  }
  return details
}

// The list area keeps the height it has for the whole list at open: a list that overflows keeps the cap (which
// follows the window), a shorter one its own height. Fewer rows later leave space inside the area instead of
// shrinking the dialog. Nothing is fixed while the list is not laid out (jsdom, a detached list).
const fixListHeight = () => {
  const list = listRef.value
  if (!list?.isConnected || list.offsetHeight === 0) return
  listHeight.value = list.scrollHeight > list.clientHeight ? LIST_MAX_HEIGHT : `${list.offsetHeight}px`
}

const open = (result: SyncUpstreamModelsResult, current: string[]) => {
  const upstream = new Set(result.models.map(model => model.trim()).filter(Boolean))
  const whitelist = [...new Set(current.filter(model => model.trim()))]
  const whitelisted = new Set(whitelist)
  const ids = [...upstream, ...whitelist.filter(model => !upstream.has(model))].sort(compareModelIds)
  rows.value = ids.map((id, position) => {
    const metadata = result.metadata?.[id]
    const status: RowStatus = !upstream.has(id) ? 'missing' : whitelisted.has(id) ? 'kept' : 'new'
    return {
      id,
      position,
      status,
      metadata,
      hasDetails:
        !!displayName(id, metadata) || isTokenCount(metadata?.context_window) || isTokenCount(metadata?.max_output_tokens),
      idKey: id.toLowerCase(),
      nameKey: metadata?.display_name?.toLowerCase() ?? ''
    }
  })
  currentModels.value = whitelist
  // Every model the upstream returned starts checked; a whitelist entry it did not return starts unchecked.
  checked.value = new Set(ids.filter(id => upstream.has(id)))
  searchQuery.value = ''
  statusFilter.value = 'all'
  snapshot.value = result
  listHeight.value = ''
  void nextTick(fixListHeight)
}

watch(
  () => (props.show ? props.result : undefined),
  result => {
    if (result) open(result, props.current)
  },
  { immediate: true }
)

const metadataNotice = computed(() => {
  const codes = new Set((snapshot.value?.warnings ?? []).map(warning => warning.code))
  if (codes.has('upstream_model_metadata_incomplete')) return t('admin.accounts.syncUpstreamModelsMetadataIncomplete')
  if (codes.has('upstream_model_metadata_partial')) return t('admin.accounts.syncUpstreamModelsMetadataPartial')
  return ''
})

const labels = computed(() => ({
  newTag: t('admin.accounts.syncUpstreamPicker.tags.new'),
  missingTag: t('admin.accounts.syncUpstreamPicker.tags.missing'),
  copy: t('common.copy')
}))
// labels is in each row's v-memo, so the rows render again in the new locale
watch(labels, () => {
  detailsCache = new WeakMap()
})

// A row's checkbox is described by its tag and its capability line, not only named by the ID.
const partId = (row: PickerRow, part: 'tag' | 'details') => `${idPrefix}-${part}-${row.position}`
const describedBy = (row: PickerRow) => {
  const ids: string[] = []
  if (row.status !== 'kept') ids.push(partId(row, 'tag'))
  if (row.hasDetails) ids.push(partId(row, 'details'))
  return ids.length > 0 ? ids.join(' ') : undefined
}

const checkedCount = computed(() => checked.value.size)

const statusCounts = computed(() => {
  const counts = { new: 0, missing: 0 }
  for (const row of rows.value) {
    if (row.status !== 'kept') counts[row.status] += 1
  }
  return counts
})

const filterOptions = computed(() => {
  const total = rows.value.length
  return [
    { value: 'all' as const, label: t('admin.accounts.syncUpstreamPicker.filters.all'), count: total },
    { value: 'checked' as const, label: t('admin.accounts.syncUpstreamPicker.filters.checked'), count: checkedCount.value },
    { value: 'unchecked' as const, label: t('admin.accounts.syncUpstreamPicker.filters.unchecked'), count: total - checkedCount.value },
    { value: 'new' as const, label: t('admin.accounts.syncUpstreamPicker.filters.new'), count: statusCounts.value.new },
    { value: 'missing' as const, label: t('admin.accounts.syncUpstreamPicker.filters.missing'), count: statusCounts.value.missing }
  ]
})

// The status filter and the search combine by AND; neither limits 全选 / 反选.
const visibleRows = computed(() => {
  const query = searchQuery.value.trim().toLowerCase()
  const filter = statusFilter.value
  const picked = filter === 'checked' || filter === 'unchecked' ? checked.value : undefined
  return rows.value.filter(row => {
    if ((filter === 'new' || filter === 'missing') && row.status !== filter) return false
    if (picked && picked.has(row.id) !== (filter === 'checked')) return false
    return !query || row.idKey.includes(query) || row.nameKey.includes(query)
  })
})

// --- Windowed rendering (the DataTable precedent) ---
const shouldVirtualize = computed(() => visibleRows.value.length > VIRTUALIZE_THRESHOLD)

// As in DataTable: a zero-height reading (the list not laid out yet) must not pin the viewport to no rows.
const observeListRect = (instance: Virtualizer<HTMLElement, HTMLLIElement>, cb: (rect: Rect) => void) =>
  observeElementRect(instance, rect => {
    if (rect.height > 0) cb(rect)
  })

const rowVirtualizer = useVirtualizer<HTMLElement, HTMLLIElement>(computed(() => {
  const list = visibleRows.value
  return {
    count: shouldVirtualize.value ? list.length : 0,
    getScrollElement: () => listRef.value,
    // Sizes are cached per model ID: a row keeps its measured height wherever the list moves it.
    getItemKey: (index: number) => list[index]?.id ?? index,
    estimateSize: (index: number) => (list[index]?.hasDetails ? ROW_WITH_DETAILS_HEIGHT : ROW_HEIGHT),
    overscan: ROW_OVERSCAN,
    // Rows for the capped area before its first real reading arrives.
    initialRect: { width: 0, height: Math.round(window.innerHeight / 2) },
    observeElementRect: observeListRect,
    useAnimationFrameWithResizeObserver: true
  }
}))

const virtualItems = computed(() => rowVirtualizer.value.getVirtualItems())

const renderedRows = computed(() => {
  const list = visibleRows.value
  if (!shouldVirtualize.value) return list.map((row, index) => ({ row, index }))
  const items: { row: PickerRow; index: number }[] = []
  for (const { index } of virtualItems.value) {
    const row = list[index]
    if (row) items.push({ row, index })
  }
  return items
})

const paddingTop = computed(() => (shouldVirtualize.value ? virtualItems.value[0]?.start ?? 0 : 0))
const paddingBottom = computed(() => {
  if (!shouldVirtualize.value) return 0
  const items = virtualItems.value
  const last = items[items.length - 1]
  return last ? rowVirtualizer.value.getTotalSize() - last.end : 0
})

// Only laid-out rows are measured: a row reads 0 while the opening dialog mounts it detached (and always in jsdom),
// and a 0 would collapse its slot. Unchanged rows keep their size (v-memo skips them; the virtualizer's
// ResizeObserver still follows a row whose height changes).
const measureRows = (elements: Iterable<HTMLLIElement>) => {
  const virtualizer = rowVirtualizer.value
  for (const element of elements) {
    if (element.isConnected && element.offsetHeight > 0) virtualizer.measureElement(element)
  }
}

// Rows (re)rendered by a patch are measured together after it: one layout instead of one per row.
let rowsToMeasure: HTMLLIElement[] = []
const measureQueuedRows = () => {
  const elements = rowsToMeasure
  rowsToMeasure = []
  if (shouldVirtualize.value) measureRows(elements)
}
const measureRow = (element: Element | ComponentPublicInstance | null) => {
  if (!(element instanceof HTMLLIElement) || !shouldVirtualize.value) return
  if (rowsToMeasure.push(element) === 1) void nextTick(measureQueuedRows)
}

// Set by a toggle under 已勾选 / 未勾选, where the toggled row leaves the list and the rows around it stay put.
let rowLeftList = false
const sameRows = (next: PickerRow[], previous: PickerRow[] | undefined) =>
  next.length === previous?.length && next.every((row, index) => row === previous[index])

watch(
  visibleRows,
  (next, previous) => {
    const virtualizer = rowVirtualizer.value
    // Release the rows that left the DOM.
    virtualizer.measureElement(null)
    const keepPlace = rowLeftList || sameRows(next, previous)
    rowLeftList = false
    if (keepPlace) return
    // A different list (open, search, filter, a bulk change under 已勾选 / 未勾选): start at its top with fresh
    // measurements. Rows that kept their place were skipped by v-memo, so every rendered row is measured again.
    const list = listRef.value
    if (list) list.scrollTop = 0
    // the scroll event would report the top only next frame; the next render already starts there
    virtualizer.scrollOffset = 0
    virtualizer.measure()
    if (list && shouldVirtualize.value) measureRows(list.querySelectorAll<HTMLLIElement>('li[data-index]'))
  },
  { flush: 'post' }
)

// Relative to the whitelist at open: checked models it lacks, and its entries left unchecked.
const changes = computed(() => {
  const picked = checked.value
  let added = 0
  for (const row of rows.value) {
    if (row.status === 'new' && picked.has(row.id)) added += 1
  }
  const removed = currentModels.value.filter(model => !picked.has(model)).length
  return { added, removed }
})

const rowCheckbox = (index: number) =>
  listRef.value?.querySelector<HTMLInputElement>(`li[data-index="${index}"] input[type="checkbox"]`) ?? null

// After the focused row left the list, the focus goes to the row now in its place (the previous one when it was
// the last), or to the search when no row is left. A windowed list scrolls that row into its window first.
const focusRowAt = async (index: number) => {
  await nextTick()
  const count = visibleRows.value.length
  if (count === 0) {
    searchRef.value?.focus()
    return
  }
  const target = Math.min(index, count - 1)
  let checkbox = rowCheckbox(target)
  if (!checkbox && shouldVirtualize.value) {
    rowVirtualizer.value.scrollToIndex(target)
    // the scroll event moves the window before the next frame
    await new Promise(resolve => requestAnimationFrame(resolve))
    await nextTick()
    checkbox = rowCheckbox(target)
  }
  checkbox?.focus()
}

const toggle = (row: PickerRow, index: number, event: Event) => {
  const filter = statusFilter.value
  const leavesList = filter === 'checked' || filter === 'unchecked'
  const rowElement = (event.currentTarget as HTMLElement | null)?.closest('li')
  const heldFocus = leavesList && !!rowElement?.contains(document.activeElement)
  const next = new Set(checked.value)
  if (!next.delete(row.id)) next.add(row.id)
  if (leavesList) rowLeftList = true
  checked.value = next
  // after the state change, so its nextTick waits for the patch that removes the row
  if (heldFocus) void focusRowAt(index)
}

const selectAll = () => {
  checked.value = new Set(rows.value.map(row => row.id))
}

const invertSelection = () => {
  const picked = checked.value
  checked.value = new Set(rows.value.filter(row => !picked.has(row.id)).map(row => row.id))
}

// One tab stop for the status filter: the arrow keys move the choice, as in a native radio group.
const moveFilter = (event: KeyboardEvent) => {
  const step = FILTER_STEPS[event.key]
  if (!step) return
  event.preventDefault()
  const values = filterOptions.value.map(option => option.value)
  const next = values[(values.indexOf(statusFilter.value) + step + values.length) % values.length]
  statusFilter.value = next
  const group = event.currentTarget as HTMLElement
  void nextTick(() => group.querySelector<HTMLElement>(`[data-filter="${next}"]`)?.focus())
}

const copyModelId = (id: string) => {
  void copyToClipboard(id)
}

// Whitelist entries keep their order; newly checked models follow in list order.
const confirm = () => {
  const picked = checked.value
  if (picked.size === 0) return
  const kept = currentModels.value.filter(model => picked.has(model))
  const added = rows.value.filter(row => row.status === 'new' && picked.has(row.id)).map(row => row.id)
  emit('confirm', [...kept, ...added], { ...changes.value })
}
</script>
