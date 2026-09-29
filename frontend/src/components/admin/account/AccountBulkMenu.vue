<template>
  <div ref="rootRef" class="relative" data-ui="account-bulk-menu">
    <button
      ref="triggerRef"
      type="button"
      class="btn btn-secondary"
      aria-haspopup="menu"
      :aria-expanded="open"
      data-test="account-bulk-menu"
      @click="open = !open"
      @keydown.down.prevent="openAndFocus('first')"
      @keydown.up.prevent="openAndFocus('last')"
    >
      <span>{{ t('admin.accounts.bulkEdit.title') }}</span>
      <Icon name="chevronDown" size="xs" />
    </button>
    <div
      v-if="open"
      class="absolute right-0 top-full z-50 mt-1 min-w-[13rem] max-w-xs"
      role="menu"
      :aria-label="t('admin.accounts.bulkEdit.title')"
      data-ui="menu"
      @keydown="handleMenuKeydown"
    >
      <button
        v-if="!allResultsSelected && totalResults > selectedCount"
        type="button"
        role="menuitem"
        class="flex w-full items-center text-left"
        data-ui="menu-item"
        data-test="bulk-menu-select-all-results"
        :disabled="selectingAll"
        @click="run('select-all-results')"
      >
        {{ selectingAll ? t('admin.accounts.bulkActions.selectingAll') : t('admin.accounts.bulkActions.selectAllResults', { count: totalResults }) }}
      </button>
      <button type="button" role="menuitem" class="flex w-full items-center text-left" data-ui="menu-item" data-test="bulk-menu-edit-filtered" @click="run('edit-filtered')">
        {{ t('admin.accounts.bulkEdit.submit') }}
      </button>
      <button type="button" role="menuitem" class="flex w-full items-center text-left" data-ui="menu-item" data-test="bulk-menu-taxonomy-filtered" @click="run('taxonomy-filtered')">
        {{ t('admin.accounts.bulkTaxonomy.filteredAction') }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
// Console theme: the bulk bar only shows while accounts are selected, so the entries its idle state offered
// (select every result, bulk update, classify the filtered results) stay one click away in this toolbar menu.
// It emits the same events as AccountBulkActionsBar; AccountsView wires them to the same handlers.
import { nextTick, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'

defineProps<{
  totalResults: number
  selectedCount: number
  selectingAll: boolean
  allResultsSelected: boolean
}>()

const emit = defineEmits<{
  'select-all-results': []
  'edit-filtered': []
  'taxonomy-filtered': []
}>()

const { t } = useI18n()
const open = ref(false)
const rootRef = ref<HTMLElement | null>(null)
const triggerRef = ref<HTMLButtonElement | null>(null)

const items = () => Array.from(rootRef.value?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]:not(:disabled)') || [])

const openAndFocus = async (position: 'first' | 'last') => {
  open.value = true
  await nextTick()
  const list = items()
  ;(position === 'last' ? list.at(-1) : list[0])?.focus()
}

const close = (refocus: boolean) => {
  open.value = false
  if (refocus) void nextTick(() => triggerRef.value?.focus())
}

const handleMenuKeydown = (event: KeyboardEvent) => {
  if (event.key === 'Escape') {
    event.preventDefault()
    close(true)
    return
  }
  if (event.key === 'Tab') {
    close(false)
    return
  }
  if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return
  const list = items()
  if (!list.length) return
  event.preventDefault()
  const current = list.indexOf(document.activeElement as HTMLButtonElement)
  let next = current
  if (event.key === 'Home') next = 0
  else if (event.key === 'End') next = list.length - 1
  else if (event.key === 'ArrowDown') next = current < 0 ? 0 : (current + 1) % list.length
  else next = current < 0 ? list.length - 1 : (current - 1 + list.length) % list.length
  list[next]?.focus()
}

const run = (action: 'select-all-results' | 'edit-filtered' | 'taxonomy-filtered') => {
  close(true)
  if (action === 'select-all-results') emit('select-all-results')
  else if (action === 'edit-filtered') emit('edit-filtered')
  else emit('taxonomy-filtered')
}

const handleOutsideClick = (event: MouseEvent) => {
  if (open.value && rootRef.value && !rootRef.value.contains(event.target as Node)) open.value = false
}

onMounted(() => document.addEventListener('click', handleOutsideClick))
onUnmounted(() => document.removeEventListener('click', handleOutsideClick))
</script>
