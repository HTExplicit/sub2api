<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.manageTaxonomy')"
    width="wide"
    close-on-click-outside
    @close="emit('close')"
  >
    <div class="space-y-4">
      <div class="inline-flex rounded-md bg-gray-100 p-1 dark:bg-dark-800">
        <button
          v-for="item in tabs"
          :key="item.value"
          type="button"
          :disabled="saving"
          class="rounded px-3 py-1.5 text-sm font-medium transition-colors"
          :class="tab === item.value ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white' : 'text-gray-500 dark:text-dark-300'"
          @click="tab = item.value"
        >
          {{ item.label }}
        </button>
      </div>

      <form class="flex gap-2" @submit.prevent="createItem">
        <input
          v-model.trim="newName"
          class="input min-w-0 flex-1"
          :placeholder="tab === 'folders' ? t('admin.accounts.newFolderName') : t('admin.accounts.newTagName')"
          maxlength="100"
        />
        <button type="submit" class="btn btn-primary" :disabled="saving || !newName">
          <Icon name="plus" size="sm" />
          <span>{{ t('common.add') }}</span>
        </button>
      </form>

      <div class="overflow-hidden rounded-md border border-gray-200 dark:border-dark-700">
        <VueDraggable
          :model-value="visibleItems"
          :disabled="saving || editingID !== null"
          handle=".taxonomy-drag-handle"
          :animation="150"
          @start="captureOrder"
          @update:model-value="updateVisibleItems"
          @end="saveOrder"
        >
        <div v-for="item in visibleItems" :key="item.id" :data-test="`taxonomy-item-${item.id}`" class="flex min-h-12 items-center gap-3 border-b border-gray-100 px-3 py-2 last:border-b-0 dark:border-dark-700">
          <button type="button" class="taxonomy-drag-handle icon-button cursor-grab text-gray-400 active:cursor-grabbing" :title="t('admin.accounts.dragToReorder')">
            <Icon name="arrowsUpDown" size="sm" />
          </button>
          <template v-if="editingID === item.id">
            <input v-model.trim="editingName" class="input h-8 min-w-0 flex-1 py-1 text-sm" maxlength="100" />
            <button type="button" class="icon-button" :title="t('common.save')" @click="saveEdit(item)">
              <Icon name="check" size="sm" />
            </button>
            <button type="button" class="icon-button" :title="t('common.cancel')" @click="cancelEdit">
              <Icon name="x" size="sm" />
            </button>
          </template>
          <template v-else>
            <span class="min-w-0 flex-1 truncate text-sm font-medium text-gray-800 dark:text-gray-100">{{ item.name }}</span>
            <span class="text-xs tabular-nums text-gray-400">{{ t('admin.accounts.accountCount', { count: item.account_count }) }}</span>
            <button type="button" class="icon-button" :title="t('common.edit')" @click="startEdit(item)">
              <Icon name="edit" size="sm" />
            </button>
            <button type="button" class="icon-button text-red-500 hover:text-red-600" :title="t('common.delete')" @click="requestDelete(item)">
              <Icon name="trash" size="sm" />
            </button>
          </template>
        </div>
        </VueDraggable>
        <div v-if="visibleItems.length === 0" class="px-4 py-10 text-center text-sm text-gray-400">
          {{ t('common.noData') }}
        </div>
      </div>
    </div>

    <template #footer>
      <button type="button" class="btn btn-secondary" @click="emit('close')">{{ t('common.close') }}</button>
    </template>
  </BaseDialog>

  <ConfirmDialog
    :show="Boolean(pendingDelete)"
    :title="t('common.delete')"
    :message="deleteMessage"
    :confirm-text="t('common.delete')"
    :cancel-text="t('common.cancel')"
    danger
    @confirm="confirmDelete"
    @cancel="pendingDelete = null"
  />
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { VueDraggable } from 'vue-draggable-plus'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import type { AccountManagementFolder, AccountManagementTag } from '@/types'

type TaxonomyItem = AccountManagementFolder | AccountManagementTag

const props = defineProps<{
  show: boolean
  folders: AccountManagementFolder[]
  tags: AccountManagementTag[]
}>()

const emit = defineEmits<{
  close: []
  changed: []
}>()

const { t } = useI18n()
const appStore = useAppStore()
const tab = ref<'folders' | 'tags'>('folders')
const newName = ref('')
const saving = ref(false)
const editingID = ref<number | null>(null)
const editingName = ref('')
const pendingDelete = ref<TaxonomyItem | null>(null)
const orderedFolders = ref<AccountManagementFolder[]>([])
const orderedTags = ref<AccountManagementTag[]>([])
const previousOrder = ref<TaxonomyItem[]>([])

const tabs = computed(() => [
  { value: 'folders' as const, label: t('admin.accounts.folders') },
  { value: 'tags' as const, label: t('admin.accounts.tags') }
])

const visibleItems = computed<TaxonomyItem[]>(() => tab.value === 'folders' ? orderedFolders.value : orderedTags.value)

watch(() => props.folders, (items) => { orderedFolders.value = [...items] }, { immediate: true, deep: true })
watch(() => props.tags, (items) => { orderedTags.value = [...items] }, { immediate: true, deep: true })

const deleteMessage = computed(() => {
  const item = pendingDelete.value
  if (!item) return ''
  if (tab.value === 'folders' && item.account_count > 0) {
    return t('admin.accounts.deleteFolderMoveConfirm', { name: item.name, count: item.account_count })
  }
  return t('admin.accounts.deleteTaxonomyConfirm', { name: item.name })
})

const captureOrder = () => {
  previousOrder.value = [...visibleItems.value]
}

const updateVisibleItems = (items: TaxonomyItem[]) => {
  if (tab.value === 'folders') orderedFolders.value = items as AccountManagementFolder[]
  else orderedTags.value = items as AccountManagementTag[]
}

const saveOrder = async () => {
  const before = [...previousOrder.value]
  const ids = visibleItems.value.map((item) => item.id)
  const orderType = tab.value
  if (!before.length || ids.every((id, index) => before[index]?.id === id)) return
  saving.value = true
  try {
    if (orderType === 'folders') orderedFolders.value = await adminAPI.accounts.reorderFolders(ids)
    else orderedTags.value = await adminAPI.accounts.reorderTags(ids)
    emit('changed')
  } catch (error: any) {
    if (orderType === 'folders') orderedFolders.value = before as AccountManagementFolder[]
    else orderedTags.value = before as AccountManagementTag[]
    appStore.showError(error?.response?.status === 409
      ? t('admin.accounts.taxonomyOrderChanged')
      : error?.message || t('admin.accounts.taxonomyOrderFailed'))
  } finally {
    saving.value = false
    previousOrder.value = []
  }
}

watch(() => props.show, (open) => {
  if (!open) return
  newName.value = ''
  editingID.value = null
  pendingDelete.value = null
})

const createItem = async () => {
  if (!newName.value || saving.value) return
  saving.value = true
  try {
    if (tab.value === 'folders') await adminAPI.accounts.createFolder(newName.value)
    else await adminAPI.accounts.createTag(newName.value)
    newName.value = ''
    emit('changed')
  } catch (error: any) {
    appStore.showError(error?.message || t('common.operationFailed'))
  } finally {
    saving.value = false
  }
}

const startEdit = (item: TaxonomyItem) => {
  editingID.value = item.id
  editingName.value = item.name
}

const cancelEdit = () => {
  editingID.value = null
  editingName.value = ''
}

const saveEdit = async (item: TaxonomyItem) => {
  if (!editingName.value || saving.value) return
  saving.value = true
  try {
    if (tab.value === 'folders') await adminAPI.accounts.updateFolder(item.id, editingName.value, item.sort_order)
    else await adminAPI.accounts.updateTag(item.id, editingName.value, item.sort_order)
    cancelEdit()
    emit('changed')
  } catch (error: any) {
    appStore.showError(error?.message || t('common.operationFailed'))
  } finally {
    saving.value = false
  }
}

const requestDelete = (item: TaxonomyItem) => {
  pendingDelete.value = item
}

const confirmDelete = async () => {
  const item = pendingDelete.value
  if (!item || saving.value) return
  saving.value = true
  try {
    if (tab.value === 'folders') await adminAPI.accounts.deleteFolder(item.id, item.account_count > 0)
    else await adminAPI.accounts.deleteTag(item.id)
    pendingDelete.value = null
    emit('changed')
  } catch (error: any) {
    appStore.showError(error?.message || t('common.operationFailed'))
  } finally {
    saving.value = false
  }
}
</script>
