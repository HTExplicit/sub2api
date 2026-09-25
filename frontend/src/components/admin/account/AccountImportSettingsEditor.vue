<template>
  <div class="grid grid-cols-1 gap-x-4 gap-y-3 sm:grid-cols-2">
    <template v-if="mode === 'uniform'">
      <SettingField v-model:enabled="draft.enabled.namePrefix" :label="t('admin.accounts.importNamePrefix')">
        <input v-model="draft.namePrefix" class="input h-9 py-1.5 text-sm" :disabled="!draft.enabled.namePrefix" />
      </SettingField>
      <SettingField v-model:enabled="draft.enabled.nameSuffix" :label="t('admin.accounts.importNameSuffix')">
        <input v-model="draft.nameSuffix" class="input h-9 py-1.5 text-sm" :disabled="!draft.enabled.nameSuffix" />
      </SettingField>
    </template>
    <SettingField v-else v-model:enabled="draft.enabled.name" :label="t('admin.accounts.columns.name')" class="sm:col-span-2">
      <input v-model="draft.name" class="input h-9 py-1.5 text-sm" :disabled="!draft.enabled.name" />
    </SettingField>

    <SettingField v-model:enabled="draft.enabled.notes" :label="t('admin.accounts.notes')" class="sm:col-span-2">
      <div class="grid grid-cols-[7rem_minmax(0,1fr)] gap-2">
        <select v-model="draft.notesMode" class="input h-9 py-1.5 text-sm" :disabled="!draft.enabled.notes">
          <option value="append">{{ t('admin.accounts.importNotesAppend') }}</option>
          <option value="replace">{{ t('admin.accounts.importNotesReplace') }}</option>
        </select>
        <input v-model="draft.notesValue" class="input h-9 py-1.5 text-sm" :disabled="!draft.enabled.notes" />
      </div>
    </SettingField>

    <SettingField v-model:enabled="draft.enabled.folder" :label="t('admin.accounts.folder')">
      <input
        v-model="draft.folder"
        class="input h-9 py-1.5 text-sm"
        :disabled="!draft.enabled.folder"
        :list="folderListID"
        :placeholder="t('admin.accounts.folderUncategorized')"
      />
      <datalist :id="folderListID">
        <option v-for="folder in folders" :key="folder.id" :value="folder.name" />
      </datalist>
    </SettingField>

    <SettingField v-model:enabled="draft.enabled.tags" :label="t('admin.accounts.tags')">
      <input
        v-model="draft.tagsText"
        class="input h-9 py-1.5 text-sm"
        :disabled="!draft.enabled.tags"
        :placeholder="t('admin.accounts.importTagsPlaceholder')"
      />
    </SettingField>

    <SettingField v-model:enabled="draft.enabled.groups" :label="t('admin.accounts.columns.groups')" class="sm:col-span-2">
      <div class="flex min-h-9 flex-wrap items-center gap-1.5 rounded-md border border-gray-300 px-2 py-1.5 dark:border-dark-600" :class="!draft.enabled.groups ? 'opacity-50' : ''">
        <label v-for="group in groups" :key="group.id" class="inline-flex items-center gap-1 text-xs text-gray-700 dark:text-gray-200">
          <input v-model="draft.groupIDs" type="checkbox" class="rounded border-gray-300 text-primary-600" :value="group.id" :disabled="!draft.enabled.groups" />
          <span>{{ group.name }}</span>
        </label>
        <span v-if="groups.length === 0" class="text-xs text-gray-400">{{ t('common.noData') }}</span>
      </div>
    </SettingField>

    <SettingField v-if="showProxy" v-model:enabled="draft.enabled.proxy" :label="t('admin.accounts.columns.proxy')">
      <select v-model="draft.proxyID" class="input h-9 py-1.5 text-sm" :disabled="!draft.enabled.proxy">
        <option value="0">{{ t('admin.accounts.directConnection') }}</option>
        <option v-for="proxy in proxies" :key="proxy.id" :value="String(proxy.id)">{{ proxy.name }}</option>
      </select>
    </SettingField>

    <SettingField v-model:enabled="draft.enabled.status" :label="t('admin.accounts.columns.status')">
      <select v-model="draft.status" class="input h-9 py-1.5 text-sm" :disabled="!draft.enabled.status">
        <option value="active">{{ t('admin.accounts.status.active') }}</option>
        <option value="disabled">{{ t('admin.accounts.status.inactive') }}</option>
        <option value="error">{{ t('admin.accounts.status.error') }}</option>
      </select>
    </SettingField>

    <SettingField v-model:enabled="draft.enabled.concurrency" :label="t('admin.accounts.concurrency')">
      <input v-model.number="draft.concurrency" type="number" min="0" step="1" class="input h-9 py-1.5 text-sm" :disabled="!draft.enabled.concurrency" />
    </SettingField>

    <SettingField v-model:enabled="draft.enabled.priority" :label="t('admin.accounts.columns.priority')">
      <input v-model.number="draft.priority" type="number" min="0" step="1" class="input h-9 py-1.5 text-sm" :disabled="!draft.enabled.priority" />
    </SettingField>

    <SettingField v-model:enabled="draft.enabled.rateMultiplier" :label="t('admin.accounts.columns.billingRateMultiplier')">
      <input v-model.number="draft.rateMultiplier" type="number" min="0" step="0.01" class="input h-9 py-1.5 text-sm" :disabled="!draft.enabled.rateMultiplier" />
    </SettingField>

    <SettingField v-model:enabled="draft.enabled.schedulable" :label="t('admin.accounts.schedulable')">
      <select v-model="draft.schedulable" class="input h-9 py-1.5 text-sm" :disabled="!draft.enabled.schedulable">
        <option :value="true">{{ t('common.enabled') }}</option>
        <option :value="false">{{ t('common.disabled') }}</option>
      </select>
    </SettingField>
  </div>
</template>

<script setup lang="ts">
import { computed, defineComponent, h } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AccountManagementFolder, AccountManagementTag, AdminGroup, Proxy } from '@/types'

export interface AccountImportSettingsDraft {
  enabled: {
    name: boolean
    namePrefix: boolean
    nameSuffix: boolean
    notes: boolean
    folder: boolean
    tags: boolean
    groups: boolean
    proxy: boolean
    concurrency: boolean
    priority: boolean
    rateMultiplier: boolean
    status: boolean
    schedulable: boolean
  }
  name: string
  namePrefix: string
  nameSuffix: string
  notesMode: 'append' | 'replace'
  notesValue: string
  folder: string
  tagsText: string
  groupIDs: number[]
  proxyID: string
  concurrency: number
  priority: number
  rateMultiplier: number
  status: 'active' | 'disabled' | 'error'
  schedulable: boolean
}

const props = withDefaults(defineProps<{
  mode: 'uniform' | 'item'
  folders?: AccountManagementFolder[]
  tags?: AccountManagementTag[]
  groups?: AdminGroup[]
  proxies?: Proxy[]
  showProxy?: boolean
  uid?: string
}>(), {
  folders: () => [],
  tags: () => [],
  groups: () => [],
  proxies: () => [],
  showProxy: true,
  uid: 'settings'
})

const draft = defineModel<AccountImportSettingsDraft>({ required: true })
const { t } = useI18n()
const folderListID = computed(() => `account-import-folders-${props.uid}`)

const SettingField = defineComponent({
  props: {
    enabled: { type: Boolean, required: true },
    label: { type: String, required: true }
  },
  emits: ['update:enabled'],
  setup(fieldProps, { emit, slots, attrs }) {
    return () => h('div', { ...attrs }, [
      h('label', { class: 'mb-1 flex items-center gap-2 text-xs font-medium text-gray-500 dark:text-dark-300' }, [
        h('input', {
          type: 'checkbox',
          class: 'h-3.5 w-3.5 rounded border-gray-300 text-primary-600 focus:ring-primary-500',
          checked: fieldProps.enabled,
          onChange: (event: Event) => emit('update:enabled', (event.target as HTMLInputElement).checked)
        }),
        fieldProps.label
      ]),
      slots.default?.()
    ])
  }
})
</script>
