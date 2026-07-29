<template>
  <div class="border-b border-gray-200 bg-gray-50/80 px-3 py-2 dark:border-dark-700 dark:bg-dark-900/35 sm:px-4">
    <div class="mb-2 flex min-w-0 items-center gap-1 text-xs text-gray-500 dark:text-dark-300">
      <Icon name="home" size="xs" />
      <button type="button" class="truncate hover:text-primary-600" @click="emit('select', '')">
        {{ t('admin.accounts.folderAll') }}
      </button>
      <template v-if="activeLabel">
        <Icon name="chevronRight" size="xs" class="shrink-0 text-gray-300" />
        <span class="truncate font-medium text-gray-800 dark:text-gray-100">{{ activeLabel }}</span>
      </template>
    </div>

    <div class="flex items-center gap-1.5 overflow-x-auto pb-0.5">
      <button
        type="button"
        :class="folderButtonClass(activeFolder === '')"
        @click="emit('select', '')"
      >
        <span>{{ t('admin.accounts.folderAll') }}</span>
        <span class="tabular-nums text-gray-400">{{ total }}</span>
      </button>
      <button
        type="button"
        :class="folderButtonClass(activeFolder === 'uncategorized')"
        @click="emit('select', 'uncategorized')"
      >
        <span>{{ t('admin.accounts.folderUncategorized') }}</span>
        <span class="tabular-nums text-gray-400">{{ uncategorizedCount }}</span>
      </button>
      <button
        v-for="folder in folders"
        :key="folder.id"
        type="button"
        :class="folderButtonClass(activeFolder === String(folder.id))"
        @click="emit('select', String(folder.id))"
      >
        <span class="max-w-40 truncate">{{ folder.name }}</span>
        <span class="tabular-nums text-gray-400">{{ folder.account_count }}</span>
      </button>
      <button
        type="button"
        class="ml-auto inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-gray-500 hover:bg-gray-200 hover:text-gray-800 dark:text-dark-300 dark:hover:bg-dark-700 dark:hover:text-white"
        :title="t('admin.accounts.manageTaxonomy')"
        @click="emit('manage')"
      >
        <Icon name="cog" size="sm" />
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { AccountManagementFolder } from '@/types'

const props = defineProps<{
  folders: AccountManagementFolder[]
  activeFolder: string
  total: number
}>()

const emit = defineEmits<{
  select: [value: string]
  manage: []
}>()

const { t } = useI18n()

const uncategorizedCount = computed(() => {
  const categorized = props.folders.reduce((sum, folder) => sum + folder.account_count, 0)
  return Math.max(0, props.total - categorized)
})

const activeLabel = computed(() => {
  if (props.activeFolder === 'uncategorized') return t('admin.accounts.folderUncategorized')
  if (!props.activeFolder) return ''
  return props.folders.find((folder) => String(folder.id) === props.activeFolder)?.name || ''
})

const folderButtonClass = (active: boolean) => [
  'inline-flex h-8 shrink-0 items-center gap-2 rounded-md border px-2.5 text-sm transition-colors',
  active
    ? 'border-primary-300 bg-white font-medium text-primary-700 shadow-sm dark:border-primary-700 dark:bg-dark-800 dark:text-primary-300'
    : 'border-transparent text-gray-600 hover:border-gray-200 hover:bg-white dark:text-dark-300 dark:hover:border-dark-700 dark:hover:bg-dark-800'
]
</script>
