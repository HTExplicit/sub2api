<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.bulkTaxonomy.title')"
    width="wide"
    close-on-click-outside
    @close="emit('close')"
  >
    <div class="space-y-5">
      <div class="rounded-md border border-primary-200 bg-primary-50 px-3 py-2 text-sm text-primary-900 dark:border-primary-800 dark:bg-primary-900/20 dark:text-primary-100">
        <p class="font-medium">{{ targetLabel }}</p>
        <p class="mt-1 text-xs opacity-80">{{ operationSummary }}</p>
      </div>

      <fieldset class="space-y-2">
        <legend class="text-sm font-semibold text-gray-800 dark:text-gray-100">{{ t('admin.accounts.folder') }}</legend>
        <label v-for="option in folderOptions" :key="option.value" class="flex min-h-11 cursor-pointer items-center gap-3 rounded-md border border-gray-200 px-3 text-sm dark:border-dark-700">
          <input v-model="folderAction" type="radio" :value="option.value" :data-test="`bulk-taxonomy-folder-${option.value || 'keep'}`" class="h-4 w-4 text-primary-600 focus:ring-primary-500" />
          <span>{{ option.label }}</span>
        </label>
        <select v-if="folderAction === 'set'" v-model="folderID" data-test="bulk-taxonomy-folder-select" class="input min-h-11 w-full" :aria-label="t('admin.accounts.folder')">
          <option value="" disabled>{{ t('admin.accounts.bulkTaxonomy.selectFolder') }}</option>
          <option v-for="folder in folders" :key="folder.id" :value="String(folder.id)">{{ folder.name }}</option>
        </select>
      </fieldset>

      <div>
        <h3 class="mb-2 text-sm font-semibold text-gray-800 dark:text-gray-100">{{ t('admin.accounts.tags') }}</h3>
        <p class="mb-3 text-xs text-gray-500 dark:text-dark-300">{{ t('admin.accounts.bulkTaxonomy.tagsHint') }}</p>
        <div v-if="tags.length" class="overflow-hidden rounded-md border border-gray-200 dark:border-dark-700">
          <div v-for="tag in tags" :key="tag.id" class="grid min-h-11 grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-3 border-b border-gray-100 px-3 last:border-b-0 dark:border-dark-700">
            <span class="truncate text-sm text-gray-800 dark:text-gray-100">{{ tag.name }}</span>
            <label class="flex min-h-11 cursor-pointer items-center gap-2 text-xs text-emerald-700 dark:text-emerald-300">
              <input :checked="tagAddIDs.includes(tag.id)" type="checkbox" :data-test="`bulk-taxonomy-tag-add-${tag.id}`" class="h-4 w-4 rounded text-emerald-600" @change="toggleTag('add', tag.id)" />
              <span>{{ t('admin.accounts.bulkTaxonomy.addTag') }}</span>
            </label>
            <label class="flex min-h-11 cursor-pointer items-center gap-2 text-xs text-red-600 dark:text-red-300">
              <input :checked="tagRemoveIDs.includes(tag.id)" type="checkbox" :data-test="`bulk-taxonomy-tag-remove-${tag.id}`" class="h-4 w-4 rounded text-red-600" @change="toggleTag('remove', tag.id)" />
              <span>{{ t('admin.accounts.bulkTaxonomy.removeTag') }}</span>
            </label>
          </div>
        </div>
        <p v-else class="rounded-md border border-dashed border-gray-200 px-3 py-6 text-center text-sm text-gray-400 dark:border-dark-700">{{ t('admin.accounts.noTags') }}</p>
      </div>
    </div>

    <template #footer>
      <button type="button" class="btn btn-secondary" :disabled="saving" @click="emit('close')">{{ t('common.cancel') }}</button>
      <button type="button" data-test="bulk-taxonomy-submit" class="btn btn-primary" :disabled="saving || !hasEffectiveChange" @click="submit">
        {{ saving ? t('common.saving') : t('admin.accounts.bulkTaxonomy.submit') }}
      </button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { AccountListFilters } from '@/api/admin/accounts'
import type { AccountJob } from '@/api/admin/accountJobs'
import { useAppStore } from '@/stores/app'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { AccountBulkTaxonomyRequest, AccountManagementFolder, AccountManagementTag } from '@/types'

export type AccountBulkTaxonomyTarget =
  | { mode: 'selected'; accountIds: number[]; count: number }
  | { mode: 'filtered'; filters: AccountListFilters; count: number }

const props = defineProps<{
  show: boolean
  target: AccountBulkTaxonomyTarget | null
  folders: AccountManagementFolder[]
  tags: AccountManagementTag[]
}>()
const emit = defineEmits<{
  close: []
  updated: [job: AccountJob]
  stale: []
}>()
const { t } = useI18n()
const appStore = useAppStore()
const folderAction = ref<'' | 'set' | 'clear'>('')
const folderID = ref('')
const tagAddIDs = ref<number[]>([])
const tagRemoveIDs = ref<number[]>([])
const saving = ref(false)
const folderOptions = computed(() => [
  { value: '', label: t('admin.accounts.bulkTaxonomy.keepFolder') },
  { value: 'set', label: t('admin.accounts.bulkTaxonomy.moveFolder') },
  { value: 'clear', label: t('admin.accounts.bulkTaxonomy.clearFolder') }
])
const targetLabel = computed(() => props.target?.mode === 'filtered'
  ? t('admin.accounts.bulkTaxonomy.filteredTarget', { count: props.target.count })
  : t('admin.accounts.bulkTaxonomy.selectedTarget', { count: props.target?.count || 0 }))
const hasEffectiveChange = computed(() => (
  (folderAction.value === 'set' && Boolean(folderID.value)) ||
  folderAction.value === 'clear' ||
  tagAddIDs.value.length > 0 ||
  tagRemoveIDs.value.length > 0
))
const operationSummary = computed(() => {
  const actions: string[] = []
  if (folderAction.value === 'set') {
    const folder = props.folders.find((item) => String(item.id) === folderID.value)
    if (folder) actions.push(t('admin.accounts.bulkTaxonomy.summaryMove', { name: folder.name }))
  } else if (folderAction.value === 'clear') actions.push(t('admin.accounts.bulkTaxonomy.summaryClear'))
  if (tagAddIDs.value.length) actions.push(t('admin.accounts.bulkTaxonomy.summaryAddTags', { count: tagAddIDs.value.length }))
  if (tagRemoveIDs.value.length) actions.push(t('admin.accounts.bulkTaxonomy.summaryRemoveTags', { count: tagRemoveIDs.value.length }))
  return actions.length ? actions.join(' / ') : t('admin.accounts.bulkTaxonomy.noChanges')
})

watch(() => props.show, (open) => {
  if (!open) return
  folderAction.value = ''
  folderID.value = ''
  tagAddIDs.value = []
  tagRemoveIDs.value = []
})

const toggleTag = (mode: 'add' | 'remove', id: number) => {
  const own = mode === 'add' ? tagAddIDs : tagRemoveIDs
  const opposite = mode === 'add' ? tagRemoveIDs : tagAddIDs
  own.value = own.value.includes(id) ? own.value.filter((value) => value !== id) : [...own.value, id]
  opposite.value = opposite.value.filter((value) => value !== id)
}

const submit = async () => {
  if (!props.target || !hasEffectiveChange.value || saving.value) return
  const payload: AccountBulkTaxonomyRequest = {
    folder_action: folderAction.value || undefined,
    folder_id: folderAction.value === 'set' ? Number(folderID.value) : undefined,
    tag_add_ids: tagAddIDs.value,
    tag_remove_ids: tagRemoveIDs.value
  }
  if (props.target.mode === 'selected') payload.account_ids = props.target.accountIds
  else {
    payload.filters = props.target.filters
    payload.expected_match_count = props.target.count
  }
  saving.value = true
  try {
    const job = await adminAPI.accounts.bulkUpdateTaxonomy(payload)
    emit('updated', job)
  } catch (error: any) {
    if (error?.response?.status === 409) {
      appStore.showError(t('admin.accounts.bulkTaxonomy.targetChanged'))
      emit('stale')
    } else {
      appStore.showError(error?.message || t('common.operationFailed'))
    }
  } finally {
    saving.value = false
  }
}
</script>
