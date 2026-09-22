<template>
  <section class="space-y-3 px-4 py-4">
    <div class="flex items-center justify-between gap-3">
      <h3 class="text-xs font-semibold text-gray-500">{{ t('admin.accounts.classification') }}</h3>
      <button class="btn btn-primary btn-sm" :disabled="saving || !dirty" @click="save">{{ t(saving ? 'common.saving' : 'common.save') }}</button>
    </div>
    <label class="block text-xs text-gray-500">{{ t('admin.accounts.folder') }}
      <select v-model="folder" class="input mt-1 w-full">
        <option value="">{{ t('admin.accounts.folderUncategorized') }}</option>
        <option v-for="item in folders" :key="item.id" :value="String(item.id)">{{ item.name }}</option>
      </select>
    </label>
    <div class="text-xs text-gray-500">{{ t('admin.accounts.tags') }}</div>
    <div class="flex flex-wrap gap-2">
      <label v-for="tag in tags" :key="tag.id" class="flex items-center gap-2 border border-line px-2 py-1 text-sm">
        <input v-model="selectedTags" type="checkbox" :value="tag.id" />{{ tag.name }}
      </label>
    </div>
  </section>
</template>
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useNotifications } from '@sub2api/plugin-ui'
import { accounts, type AccountManagementFolder, type AccountManagementTag } from './api'
const props = defineProps<{ accountId: number; folderId?: number | null; tagIds: number[]; folders: AccountManagementFolder[]; tags: AccountManagementTag[] }>()
const emit = defineEmits<{ changed: [] }>()
const { t } = useI18n()
const notifications = useNotifications()
const folder = ref(''), selectedTags = ref<number[]>([]), saving = ref(false)
watch(() => props.accountId, () => { folder.value = props.folderId ? String(props.folderId) : ''; selectedTags.value = [...props.tagIds] }, { immediate: true })
const dirty = computed(() => folder.value !== (props.folderId ? String(props.folderId) : '') || JSON.stringify([...selectedTags.value].sort((a, b) => a - b)) !== JSON.stringify([...props.tagIds].sort((a, b) => a - b)))
async function save() {
  if (saving.value || !dirty.value) return
  saving.value = true
  try { await accounts.setTaxonomy(props.accountId, folder.value ? Number(folder.value) : null, selectedTags.value); emit('changed'); notifications.showSuccess(t('admin.accounts.taxonomySaved')) }
  catch (error) { notifications.showError(error instanceof Error ? error.message : t('common.operationFailed')) }
  finally { saving.value = false }
}
</script>
