<template>
  <div ref="rootRef" class="min-w-0 flex-1 space-y-2">
    <div class="flex flex-wrap items-center gap-2">
      <SearchInput
        :model-value="modelValue.search"
        :placeholder="t('admin.accounts.searchAccounts')"
        class="w-full sm:w-64"
        @update:model-value="updateSearch"
        @search="emit('change')"
      />

      <div v-for="menu in menus" :key="menu.key" class="relative">
        <button
          type="button"
          class="inline-flex h-9 items-center gap-1.5 rounded-md border border-gray-300 bg-white px-3 text-sm text-gray-700 transition-colors hover:border-gray-400 hover:bg-gray-50 focus:outline-none focus:ring-2 focus:ring-primary-500/30 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-200 dark:hover:bg-dark-700"
          :aria-expanded="openMenu === menu.key"
          :data-test="`account-filter-${menu.key}`"
          @click.stop="toggleMenu(menu.key)"
        >
          <span>{{ menu.label }}</span>
          <span
            v-if="selectedValues(menu.key).length"
            class="min-w-5 rounded-full bg-primary-100 px-1.5 text-center text-xs font-semibold text-primary-700 dark:bg-primary-900/40 dark:text-primary-300"
          >
            {{ selectedValues(menu.key).length }}
          </span>
          <Icon name="chevronDown" size="xs" class="text-gray-400" />
        </button>

        <div
          v-if="openMenu === menu.key"
          class="absolute left-0 z-50 mt-1 w-64 overflow-hidden rounded-md border border-gray-200 bg-white shadow-lg dark:border-dark-700 dark:bg-dark-800"
          @click.stop
        >
          <div class="max-h-72 overflow-y-auto p-1.5">
            <label
              v-for="option in menu.options"
              :key="String(option.value)"
              class="flex cursor-pointer items-center gap-2 rounded px-2.5 py-2 text-sm hover:bg-gray-100 dark:hover:bg-dark-700"
            >
              <input
                type="checkbox"
                class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                :checked="selectedValues(menu.key).includes(String(option.value))"
                @change="toggleOption(menu.key, String(option.value))"
              />
              <span class="min-w-0 flex-1 truncate text-gray-700 dark:text-gray-200">{{ option.label }}</span>
              <span class="text-xs tabular-nums text-gray-400 dark:text-dark-400">{{ option.count }}</span>
            </label>
            <div v-if="menu.options.length === 0" class="px-3 py-5 text-center text-sm text-gray-400">
              {{ t('common.noData') }}
            </div>
          </div>
          <div v-if="selectedValues(menu.key).length" class="border-t border-gray-100 p-1.5 dark:border-dark-700">
            <button
              type="button"
              class="w-full rounded px-2.5 py-1.5 text-left text-xs font-medium text-gray-500 hover:bg-gray-100 dark:text-dark-300 dark:hover:bg-dark-700"
              @click="clearMenu(menu.key)"
            >
              {{ t('common.clear') }}
            </button>
          </div>
        </div>
      </div>

      <select
        :value="modelValue.group"
        class="input h-9 w-44 py-1.5 text-sm"
        @change="updateGroup(($event.target as HTMLSelectElement).value)"
      >
        <option value="">{{ t('admin.accounts.allGroups') }}</option>
        <option value="ungrouped">{{ t('admin.accounts.ungroupedGroup') }}</option>
        <option v-for="group in groups" :key="group.id" :value="String(group.id)">{{ group.name }}</option>
      </select>
    </div>

    <div v-if="activeChips.length" class="flex flex-wrap items-center gap-1.5">
      <button
        v-for="chip in activeChips"
        :key="chip.key"
        type="button"
        class="inline-flex max-w-full items-center gap-1 rounded bg-gray-100 px-2 py-1 text-xs text-gray-700 hover:bg-gray-200 dark:bg-dark-700 dark:text-gray-200 dark:hover:bg-dark-600"
        :title="chip.label"
        :data-test="`account-filter-chip-${chip.key}`"
        @click="removeChip(chip)"
      >
        <span class="truncate">{{ chip.label }}</span>
        <Icon name="x" size="xs" aria-hidden="true" />
      </button>
      <button
        type="button"
        class="px-1.5 py-1 text-xs font-medium text-primary-600 hover:text-primary-700 dark:text-primary-400"
        @click="clearAll"
      >
        {{ t('common.clear') }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import SearchInput from '@/components/common/SearchInput.vue'
import Icon from '@/components/icons/Icon.vue'
import type {
  AccountConsoleFacets,
  AccountConsoleFilterState,
  AccountFacetOption,
  AdminGroup
} from '@/types'

type MenuKey = 'platforms' | 'types' | 'statuses' | 'plans' | 'proxies' | 'tags'
type MenuOption = AccountFacetOption

const props = defineProps<{
  modelValue: AccountConsoleFilterState
  facets: AccountConsoleFacets | null
  groups: AdminGroup[]
}>()

const emit = defineEmits<{
  'update:modelValue': [value: AccountConsoleFilterState]
  change: []
}>()

const { t } = useI18n()
const rootRef = ref<HTMLElement | null>(null)
const openMenu = ref<MenuKey | null>(null)

const typeLabel = (value: string) => {
  const labels: Record<string, string> = {
    oauth: t('admin.accounts.oauthType'),
    'setup-token': t('admin.accounts.setupToken'),
    apikey: t('admin.accounts.apiKey'),
    bedrock: 'AWS Bedrock',
    upstream: 'Upstream',
    service_account: 'Service Account'
  }
  return labels[value] || value
}

const statusLabel = (value: string) => {
  const labels: Record<string, string> = {
    active: t('admin.accounts.status.active'),
    inactive: t('admin.accounts.status.inactive'),
    disabled: t('admin.accounts.status.inactive'),
    error: t('admin.accounts.status.error'),
    rate_limited: t('admin.accounts.status.rateLimited'),
    temp_unschedulable: t('admin.accounts.status.tempUnschedulable'),
    unschedulable: t('admin.accounts.status.unschedulable')
  }
  return labels[value] || value
}

const normalizedOptions = (items: AccountFacetOption[] | undefined, labeler?: (value: string) => string) =>
  (items || []).map((item) => ({ ...item, label: labeler?.(item.value) || item.label }))

const menus = computed<Array<{ key: MenuKey; label: string; options: MenuOption[] }>>(() => [
  { key: 'platforms', label: t('admin.accounts.filterPlatform'), options: normalizedOptions(props.facets?.platforms) },
  { key: 'types', label: t('admin.accounts.filterType'), options: normalizedOptions(props.facets?.types, typeLabel) },
  { key: 'statuses', label: t('admin.accounts.filterStatus'), options: normalizedOptions(props.facets?.statuses, statusLabel) },
  { key: 'plans', label: t('admin.accounts.filterPlan'), options: normalizedOptions(props.facets?.plans) },
  { key: 'proxies', label: t('admin.accounts.filterProxy'), options: normalizedOptions(props.facets?.proxies) },
  {
    key: 'tags',
    label: t('admin.accounts.filterTags'),
    options: (props.facets?.tags || []).map((tag) => ({ value: String(tag.id), label: tag.name, count: tag.account_count }))
  }
])

const selectedValues = (key: MenuKey): string[] => {
  if (key === 'tags') return props.modelValue.tags.map(String)
  return props.modelValue[key]
}

const withUpdate = (patch: Partial<AccountConsoleFilterState>) => {
  emit('update:modelValue', { ...props.modelValue, ...patch })
  emit('change')
}

const updateSearch = (value: string) => {
  emit('update:modelValue', { ...props.modelValue, search: value })
}

const updateGroup = (value: string) => withUpdate({ group: value })

const toggleMenu = (key: MenuKey) => {
  openMenu.value = openMenu.value === key ? null : key
}

const toggleOption = (key: MenuKey, value: string) => {
  const values = selectedValues(key)
  const next = values.includes(value) ? values.filter((item) => item !== value) : [...values, value]
  if (key === 'tags') withUpdate({ tags: next.map(Number).filter(Number.isFinite) })
  else withUpdate({ [key]: next } as Partial<AccountConsoleFilterState>)
}

const clearMenu = (key: MenuKey) => {
  if (key === 'tags') withUpdate({ tags: [] })
  else withUpdate({ [key]: [] } as Partial<AccountConsoleFilterState>)
}

type FilterChip = { key: string; menu: MenuKey | 'group' | 'account_ids'; value: string; label: string }

const activeChips = computed<FilterChip[]>(() => {
  const chips: FilterChip[] = []
  for (const menu of menus.value) {
    const labels = new Map(menu.options.map((option) => [String(option.value), option.label]))
    for (const value of selectedValues(menu.key)) {
      chips.push({ key: `${menu.key}:${value}`, menu: menu.key, value, label: labels.get(value) || value })
    }
  }
  if (props.modelValue.group) {
    const groupLabel = props.modelValue.group === 'ungrouped'
      ? t('admin.accounts.ungroupedGroup')
      : props.groups.find((group) => String(group.id) === props.modelValue.group)?.name || props.modelValue.group
    chips.push({ key: `group:${props.modelValue.group}`, menu: 'group', value: props.modelValue.group, label: groupLabel })
  }
  if (props.modelValue.account_ids.length) {
    chips.push({
      key: 'account_ids:import', menu: 'account_ids', value: '',
      label: t('admin.accounts.importSessionFilter', { count: props.modelValue.account_ids.length })
    })
  }
  return chips
})

const removeChip = (chip: FilterChip) => {
  if (chip.menu === 'group') {
    withUpdate({ group: '' })
    return
  }
  if (chip.menu === 'account_ids') {
    withUpdate({ account_ids: [] })
    return
  }
  toggleOption(chip.menu, chip.value)
}

const clearAll = () => withUpdate({
  platforms: [], types: [], statuses: [], plans: [], proxies: [], tags: [], group: '', account_ids: []
})

const handleDocumentClick = (event: MouseEvent) => {
  if (!rootRef.value?.contains(event.target as Node)) openMenu.value = null
}

onMounted(() => document.addEventListener('click', handleDocumentClick))
onUnmounted(() => document.removeEventListener('click', handleDocumentClick))
</script>
