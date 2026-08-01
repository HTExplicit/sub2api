<template>
  <div class="min-w-0 bg-gray-50/80 dark:bg-dark-900/35">
    <div class="relative border-b border-gray-200 p-3 dark:border-dark-700 lg:hidden" ref="mobileMenuRef">
      <button
        ref="mobileTriggerRef"
        type="button"
        class="flex min-h-11 w-full items-center justify-between gap-3 rounded-md border border-gray-200 bg-white px-3 text-left text-sm text-gray-800 shadow-sm dark:border-dark-700 dark:bg-dark-800 dark:text-gray-100"
        aria-haspopup="listbox"
        :aria-expanded="mobileOpen"
        @click="mobileOpen = !mobileOpen"
        @keydown.down.prevent="openMobileOptions('first')"
        @keydown.up.prevent="openMobileOptions('last')"
        @keydown.esc.prevent="closeMobileMenu"
      >
        <span class="min-w-0">
          <span class="block text-[11px] font-medium text-gray-500 dark:text-dark-300">{{ t('admin.accounts.managementClassification') }}</span>
          <span class="block truncate font-medium">{{ activeLabel }}</span>
        </span>
        <Icon name="chevronDown" size="sm" class="shrink-0" />
      </button>
      <div
        v-if="mobileOpen"
        class="absolute inset-x-3 top-[calc(100%-0.5rem)] z-30 max-h-72 overflow-y-auto rounded-md border border-gray-200 bg-white p-1 shadow-lg dark:border-dark-700 dark:bg-dark-800"
        role="listbox"
        :aria-label="t('admin.accounts.managementClassification')"
        @keydown="handleMobileMenuKeydown"
      >
        <button v-if="error" type="button" class="flex min-h-11 w-full items-center justify-between rounded px-3 text-left text-sm text-red-600 hover:bg-red-50 dark:text-red-300 dark:hover:bg-red-900/20" @click="emit('retry')">
          <span>{{ t('admin.accounts.facetsLoadFailed') }}</span>
          <span class="font-medium text-primary-700 dark:text-primary-300">{{ t('common.retry') }}</span>
        </button>
        <div v-else-if="loading" class="space-y-2 p-2" aria-live="polite">
          <div v-for="index in 3" :key="index" class="h-9 animate-pulse rounded bg-gray-200/70 dark:bg-dark-700" />
        </div>
        <template v-else>
        <button
          v-for="item in navigationItems"
          :key="item.value || 'all'"
          type="button"
          role="option"
          :aria-selected="activeFolder === item.value"
          class="flex min-h-11 w-full items-center justify-between gap-3 rounded px-3 text-left text-sm hover:bg-gray-100 dark:hover:bg-dark-700"
          :class="activeFolder === item.value ? 'font-semibold text-primary-700 dark:text-primary-300' : 'text-gray-700 dark:text-gray-200'"
          @click="selectMobile(item.value)"
        >
          <span class="truncate">{{ item.label }}</span>
          <span class="shrink-0 tabular-nums text-gray-400">{{ formatCount(item.count) }}</span>
        </button>
        </template>
        <button type="button" class="flex min-h-11 w-full items-center gap-2 rounded px-3 text-sm text-gray-600 hover:bg-gray-100 dark:text-dark-300 dark:hover:bg-dark-700" @click="manageMobile">
          <Icon name="cog" size="sm" />
          <span>{{ t('admin.accounts.manageTaxonomy') }}</span>
        </button>
      </div>
    </div>

    <div
      class="hidden min-w-0 items-center gap-3 border-b border-gray-200 px-3 py-2 dark:border-dark-700 lg:flex"
      :aria-label="t('admin.accounts.managementClassification')"
      data-test="desktop-taxonomy-bar"
    >
      <h2 class="shrink-0 text-xs font-semibold uppercase text-gray-500 dark:text-dark-300">
        {{ t('admin.accounts.managementClassification') }}
      </h2>

      <nav class="min-w-0 flex-1 overflow-x-auto" :aria-label="t('admin.accounts.managementClassification')" data-test="desktop-taxonomy-nav">
        <div v-if="loading" class="flex min-w-max items-center gap-2 py-1" aria-live="polite">
          <div v-for="index in 3" :key="index" class="h-9 w-28 animate-pulse rounded bg-gray-200/70 dark:bg-dark-700" />
        </div>
        <div v-else-if="error" class="flex min-h-10 items-center gap-3 py-1 text-xs text-red-600 dark:text-red-300" role="status">
          <span>{{ t('admin.accounts.facetsLoadFailed') }}</span>
          <button type="button" class="min-h-9 shrink-0 font-medium text-primary-700 hover:underline dark:text-primary-300" data-test="desktop-taxonomy-retry" @click="emit('retry')">
            {{ t('common.retry') }}
          </button>
        </div>
        <div v-else class="flex min-w-max items-center gap-1 py-1">
          <button
            v-for="item in navigationItems"
            :key="item.value || 'all'"
            type="button"
            class="flex min-h-10 shrink-0 items-center justify-between gap-2 rounded-md px-3 text-left text-sm transition-colors"
            :class="activeFolder === item.value ? 'bg-primary-50 font-semibold text-primary-700 dark:bg-primary-900/25 dark:text-primary-300' : 'text-gray-600 hover:bg-gray-100 dark:text-dark-300 dark:hover:bg-dark-800'"
            data-test="desktop-taxonomy-option"
            @click="emit('select', item.value)"
          >
            <span class="max-w-48 truncate">{{ item.label }}</span>
            <span class="shrink-0 tabular-nums text-gray-400">{{ formatCount(item.count) }}</span>
          </button>
          <span v-if="folders.length === 0" class="px-2 text-xs text-gray-400">{{ t('admin.accounts.noFolders') }}</span>
        </div>
      </nav>

      <button type="button" class="icon-button shrink-0" :title="t('admin.accounts.manageTaxonomy')" data-test="desktop-taxonomy-manage" @click="emit('manage')">
        <Icon name="cog" size="sm" />
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { AccountManagementFolder } from '@/types'

const props = defineProps<{
  folders: AccountManagementFolder[]
  activeFolder: string
  total?: number
  uncategorizedCount?: number
  loading?: boolean
  error?: boolean
}>()

const emit = defineEmits<{
  select: [value: string]
  manage: []
  retry: []
}>()

const { t } = useI18n()
const mobileOpen = ref(false)
const mobileMenuRef = ref<HTMLElement | null>(null)
const mobileTriggerRef = ref<HTMLButtonElement | null>(null)
const safeCount = (value: unknown): number | undefined => {
  const count = Number(value)
  return Number.isFinite(count) && count >= 0 ? count : undefined
}
const formatCount = (value: unknown) => safeCount(value)?.toLocaleString() ?? '-'
const navigationItems = computed(() => [
  { value: '', label: t('admin.accounts.allAccounts'), count: safeCount(props.total) },
  { value: 'uncategorized', label: t('admin.accounts.folderUncategorized'), count: safeCount(props.uncategorizedCount) },
  ...props.folders.map((folder) => ({ value: String(folder.id), label: folder.name || '-', count: safeCount(folder.account_count) }))
])
const activeLabel = computed(() => navigationItems.value.find((item) => item.value === props.activeFolder)?.label || t('admin.accounts.allAccounts'))
const mobileOptions = () => Array.from(mobileMenuRef.value?.querySelectorAll<HTMLButtonElement>('[role="option"]') || [])
const openMobileOptions = async (position: 'first' | 'last') => {
  mobileOpen.value = true
  await nextTick()
  const options = mobileOptions()
  const selected = options.find((option) => option.getAttribute('aria-selected') === 'true')
  const target = position === 'last' ? options.at(-1) : selected || options[0]
  target?.focus()
}
const closeMobileMenu = () => { mobileOpen.value = false }
const handleMobileMenuKeydown = (event: KeyboardEvent) => {
  if (event.key === 'Escape') {
    event.preventDefault()
    mobileOpen.value = false
    void nextTick(() => mobileTriggerRef.value?.focus())
    return
  }
  if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return
  const options = mobileOptions()
  if (!options.length) return
  event.preventDefault()
  const current = options.indexOf(document.activeElement as HTMLButtonElement)
  let next = current
  if (event.key === 'Home') next = 0
  else if (event.key === 'End') next = options.length - 1
  else if (event.key === 'ArrowDown') next = current < 0 ? 0 : (current + 1) % options.length
  else next = current < 0 ? options.length - 1 : (current - 1 + options.length) % options.length
  options[next]?.focus()
}
const selectMobile = (value: string) => {
  mobileOpen.value = false
  emit('select', value)
}
const manageMobile = () => {
  mobileOpen.value = false
  emit('manage')
}
const handleOutsideClick = (event: MouseEvent) => {
  if (mobileMenuRef.value && !mobileMenuRef.value.contains(event.target as Node)) mobileOpen.value = false
}
onMounted(() => document.addEventListener('click', handleOutsideClick))
onUnmounted(() => document.removeEventListener('click', handleOutsideClick))
</script>
