<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.dataImportTitle')"
    width="wide"
    close-on-click-outside
    @close="handleClose"
  >
    <form id="account-import-job-form" class="space-y-4" @submit.prevent="handleSubmit">
      <p class="text-sm text-gray-600 dark:text-dark-300">{{ t('admin.accounts.dataImportHint') }}</p>
      <div>
        <label class="input-label">{{ t('admin.accounts.dataImportFile') }}</label>
        <div
          class="flex items-center justify-between gap-3 rounded-md border border-dashed px-4 py-4 transition-colors"
          :class="dragActive
            ? 'border-primary-400 bg-primary-50/70 dark:border-primary-500 dark:bg-primary-900/20'
            : 'border-gray-300 bg-gray-50 dark:border-dark-600 dark:bg-dark-800'"
          @dragenter.prevent="handleDragEnter"
          @dragover.prevent
          @dragleave.prevent="handleDragLeave"
          @drop.prevent="handleDrop"
        >
          <div class="min-w-0">
            <div class="truncate text-sm font-medium text-gray-700 dark:text-dark-200" :title="fileListTitle">
              {{ selectedFilesLabel || t('admin.accounts.dataImportSelectFile') }}
            </div>
            <div class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">Sub2 JSON v1 / v2 (.json)</div>
          </div>
          <button type="button" class="btn btn-secondary shrink-0" :disabled="busy" @click="openFilePicker">
            {{ t('common.chooseFile') }}
          </button>
        </div>
        <input
          ref="fileInput"
          type="file"
          class="hidden"
          accept="application/json,.json"
          multiple
          @change="handleFileChange"
        />
      </div>

      <div v-if="payload" class="rounded-md border border-gray-200 px-3 py-2 text-sm dark:border-dark-700">
        {{ t('admin.accounts.dataImportLocalSummary', {
          accounts: payload.accounts.length,
          proxies: payload.proxies.length
        }) }}
      </div>

      <div v-if="payload" class="space-y-1">
        <label class="input-label" for="account-import-target-group">{{ t('admin.accounts.dataImportTargetGroup') }}</label>
        <select
          id="account-import-target-group"
          v-model="targetGroupID"
          class="input w-full"
          :disabled="busy"
          data-test="import-target-group"
        >
          <option value="">{{ t('admin.accounts.dataImportTargetGroupPlaceholder') }}</option>
          <option v-for="group in importGroups" :key="group.id" :value="String(group.id)">
            {{ group.name }} · {{ t(`admin.groups.platforms.${group.platform}`) }}
          </option>
        </select>
        <p class="input-hint">{{ t('admin.accounts.dataImportTargetGroupHint') }}</p>
      </div>

      <details v-if="payload" class="rounded-md border border-gray-200 dark:border-dark-700">
        <summary class="cursor-pointer px-3 py-2.5 text-sm font-medium text-gray-800 dark:text-gray-100">
          {{ t('admin.accounts.importUniformSettings') }}
        </summary>
        <div class="border-t border-gray-100 px-3 py-3 dark:border-dark-700">
          <AccountImportSettingsEditor
            v-model="uniformDraft"
            mode="uniform"
            uid="uniform"
            :folders="folders"
            :tags="tags"
            :groups="groups"
            :proxies="proxies"
          />
        </div>
      </details>

      <div v-if="preview" class="space-y-3 rounded-md border border-gray-200 px-3 py-3 dark:border-dark-700" data-test="import-preview">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <span class="text-sm font-semibold text-gray-800 dark:text-gray-100">{{ t('admin.accounts.dataImportPreviewItems') }}</span>
          <span class="text-xs text-gray-500 dark:text-dark-400">
            {{ t('admin.accounts.dataImportPreviewSummary', { create: preview.create_count, update: preview.update_count, reject: preview.reject_count }) }}
          </span>
        </div>
        <div class="max-h-52 max-w-full overflow-x-auto overflow-y-auto rounded border border-gray-100 dark:border-dark-700">
          <table class="min-w-full text-left text-xs">
            <thead class="sticky top-0 bg-gray-50 text-gray-500 dark:bg-dark-800 dark:text-dark-300">
              <tr>
                <th class="px-2 py-1.5">#</th>
                <th class="px-2 py-1.5">{{ t('admin.accounts.accountName') }}</th>
                <th class="px-2 py-1.5">{{ t('admin.accounts.dataImportPreviewAction') }}</th>
                <th class="px-2 py-1.5">{{ t('admin.accounts.status') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="item in preview.items" :key="item.index" class="border-t border-gray-100 dark:border-dark-700">
                <td class="px-2 py-1.5 text-gray-500">{{ item.index + 1 }}</td>
                <td class="max-w-[16rem] truncate px-2 py-1.5">{{ redactedItemLabel(item.index) }}</td>
                <td class="px-2 py-1.5">{{ item.action }}</td>
                <td class="max-w-[22rem] truncate px-2 py-1.5 text-gray-500" :title="item.message || item.error || ''">{{ item.message || item.error || item.code || '--' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </form>

    <template #footer>
      <button type="button" class="btn btn-secondary" :disabled="busy" @click="handleClose">
        {{ t('common.cancel') }}
      </button>
      <button
        type="button"
        data-test="preview-import"
        class="btn btn-secondary"
        :disabled="busy || !payload"
        @click="handlePreview"
      >
        {{ previewLoading ? t('admin.accounts.dataImportPreviewing') : t('admin.accounts.dataImportPreview') }}
      </button>
      <button
        type="submit"
        form="account-import-job-form"
        data-test="submit-import-job"
        class="btn btn-primary"
        :disabled="busy || !payload || !preview"
      >
        {{ busy ? t('admin.accounts.dataImporting') : t('admin.accounts.dataImportSubmitJob') }}
      </button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import AccountImportSettingsEditor, { type AccountImportSettingsDraft } from './AccountImportSettingsEditor.vue'
import { adminAPI } from '@/api/admin'
import type { AccountImportPreview } from '@/api/admin/accounts'
import { useAppStore } from '@/stores/app'
import type { AccountJob } from '@/api/admin/accountJobs'
import type {
  AccountManagementFolder,
  AccountManagementTag,
  AdminDataImportUniformSettings,
  AdminDataPayload,
  AdminGroup,
  Proxy,
} from '@/types'

const props = withDefaults(defineProps<{
  show: boolean
  folders?: AccountManagementFolder[]
  tags?: AccountManagementTag[]
  groups?: AdminGroup[]
  proxies?: Proxy[]
}>(), {
  folders: () => [],
  tags: () => [],
  groups: () => [],
  proxies: () => [],
})

const emit = defineEmits<{
  close: []
  imported: [job: AccountJob]
}>()

const { t } = useI18n()
const appStore = useAppStore()
const busy = ref(false)
const files = ref<File[]>([])
const payload = ref<AdminDataPayload | null>(null)
const uniformDraft = ref(makeSettingsDraft())
const targetGroupID = ref('')
const preview = ref<AccountImportPreview | null>(null)
const previewLoading = ref(false)
const dragDepth = ref(0)
const fileInput = ref<HTMLInputElement | null>(null)
let selectionGeneration = 0

const dragActive = computed(() => dragDepth.value > 0)
const selectedFilesLabel = computed(() => {
  if (!files.value.length) return ''
  if (files.value.length === 1) return files.value[0]?.name || ''
  return t('admin.accounts.selectedCount', { count: files.value.length })
})
const fileListTitle = computed(() => files.value.map((file) => file.name).join(', '))
const importGroups = computed(() => props.groups.filter((group) =>
  group.platform === 'cindy' &&
  (!group.wire_platform || group.wire_platform === 'openai') &&
  (!group.provider_profile || group.provider_profile === 'cindy_laxa_v1')
))

const redactedItemLabel = (index: number) => `#${index + 1}`

function makeSettingsDraft(): AccountImportSettingsDraft {
  return {
    enabled: {
      name: false,
      namePrefix: false,
      nameSuffix: false,
      notes: false,
      folder: false,
      tags: false,
      groups: false,
      proxy: false,
      concurrency: false,
      priority: false,
      rateMultiplier: false,
      status: false,
      schedulable: false,
    },
    name: '',
    namePrefix: '',
    nameSuffix: '',
    notesMode: 'append',
    notesValue: '',
    folder: '',
    tagsText: '',
    groupIDs: [],
    proxyID: '0',
    concurrency: 1,
    priority: 0,
    rateMultiplier: 1,
    status: 'active',
    schedulable: true,
  }
}

function reset(): void {
  selectionGeneration += 1
  busy.value = false
  files.value = []
  payload.value = null
  targetGroupID.value = ''
  preview.value = null
  previewLoading.value = false
  uniformDraft.value = makeSettingsDraft()
  dragDepth.value = 0
  if (fileInput.value) fileInput.value.value = ''
}

watch(() => props.show, (open) => {
  if (open) reset()
})

function handleClose(): void {
  if (!busy.value) emit('close')
}

function openFilePicker(): void {
  fileInput.value?.click()
}

async function handleFileChange(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  await setSelectedFiles(input.files)
  input.value = ''
}

function isJsonFile(file: File): boolean {
  return file.name.toLowerCase().endsWith('.json') || file.type === 'application/json'
}

async function setSelectedFiles(source: FileList | File[] | null | undefined): Promise<void> {
  if (busy.value) return
  const requestGeneration = ++selectionGeneration
  const incoming = Array.from(source || [])
  const accepted = incoming.filter(isJsonFile)
  if (!accepted.length) {
    files.value = []
    payload.value = null
    appStore.showError(t('admin.accounts.dataImportSelectFile'))
    return
  }
  if (accepted.length !== incoming.length) {
    appStore.showWarning(t('admin.accounts.dataImportIgnoredFiles', { count: incoming.length - accepted.length }))
  }
  try {
    const parsed = await parseFiles(accepted)
    if (requestGeneration !== selectionGeneration) return
    files.value = accepted
    payload.value = parsed
    preview.value = null
  } catch (error) {
    if (requestGeneration !== selectionGeneration) return
    files.value = []
    payload.value = null
    appStore.showError(error instanceof Error ? error.message : t('admin.accounts.dataImportFailed'))
  }
}

function handleDragEnter(): void {
  if (!busy.value) dragDepth.value += 1
}

function handleDragLeave(): void {
  dragDepth.value = Math.max(0, dragDepth.value - 1)
}

function handleDrop(event: DragEvent): void {
  dragDepth.value = 0
  void setSelectedFiles(event.dataTransfer?.files)
}

async function readFileAsText(file: File): Promise<string> {
  if (typeof file.text === 'function') return file.text()
  if (typeof file.arrayBuffer === 'function') return new TextDecoder().decode(await file.arrayBuffer())
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result || ''))
    reader.onerror = () => reject(reader.error || new Error('Failed to read file'))
    reader.readAsText(file)
  })
}

function isValidDataPayload(value: unknown): value is AdminDataPayload {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false
  const candidate = value as Partial<AdminDataPayload>
  if (candidate.type && !['sub2api-data', 'sub2api-bundle'].includes(candidate.type)) return false
  if (candidate.version && ![1, 2].includes(candidate.version)) return false
  return Array.isArray(candidate.accounts) && Array.isArray(candidate.proxies)
}

async function parseFiles(selectedFiles: File[]): Promise<AdminDataPayload> {
  const parsedPayloads: AdminDataPayload[] = []
  for (const file of selectedFiles) {
    let parsed: unknown
    try {
      parsed = JSON.parse(await readFileAsText(file))
    } catch {
      throw new Error(t('admin.accounts.dataImportParseFailedFile', { name: file.name }))
    }
    if (!isValidDataPayload(parsed)) {
      throw new Error(t('admin.accounts.dataImportInvalidFile', { name: file.name }))
    }
    parsedPayloads.push(parsed)
  }
  if (parsedPayloads.length === 1) return parsedPayloads[0]!
  return {
    type: 'sub2api-data',
    version: Math.max(1, ...parsedPayloads.map((item) => item.version || 1)),
    exported_at: new Date().toISOString(),
    proxies: parsedPayloads.flatMap((item) => item.proxies),
    accounts: parsedPayloads.flatMap((item) => item.accounts),
    skipped_shadows: parsedPayloads.reduce((sum, item) => sum + Number(item.skipped_shadows || 0), 0),
  }
}

function parseTags(value: string): string[] {
  return Array.from(new Set(value.split(',').map((item) => item.trim()).filter(Boolean)))
}

function buildUniformSettings(draft: AccountImportSettingsDraft): AdminDataImportUniformSettings {
  const settings: AdminDataImportUniformSettings = {}
  if (draft.enabled.namePrefix) settings.name_prefix = draft.namePrefix
  if (draft.enabled.nameSuffix) settings.name_suffix = draft.nameSuffix
  if (draft.enabled.notes) settings.notes = { mode: draft.notesMode, value: draft.notesValue }
  if (draft.enabled.folder) settings.management_folder = draft.folder.trim()
  if (draft.enabled.tags) settings.tags = parseTags(draft.tagsText)
  if (draft.enabled.groups) settings.group_ids = [...draft.groupIDs]
  if (draft.enabled.proxy) settings.proxy_id = Number(draft.proxyID || 0)
  if (draft.enabled.concurrency) settings.concurrency = Number(draft.concurrency)
  if (draft.enabled.priority) settings.priority = Number(draft.priority)
  if (draft.enabled.rateMultiplier) settings.rate_multiplier = Number(draft.rateMultiplier)
  if (draft.enabled.status) settings.status = draft.status
  if (draft.enabled.schedulable) settings.schedulable = draft.schedulable
  return settings
}

function buildImportRequest() {
  return {
    data: payload.value!,
    skip_default_group_bind: true,
    uniform_settings: buildUniformSettings(uniformDraft.value),
    ...(targetGroupID.value ? { target_group_id: Number(targetGroupID.value) } : {}),
  }
}

async function handlePreview(): Promise<void> {
  if (!payload.value || previewLoading.value) return
  previewLoading.value = true
  try {
    preview.value = await adminAPI.accounts.previewImportData(buildImportRequest())
  } catch (error: any) {
    preview.value = null
    if (error?.response?.status === 409) {
      appStore.showWarning(t('admin.accounts.dataImportStalePreview'))
    } else {
      appStore.showError(error instanceof Error ? error.message : t('admin.accounts.dataImportFailed'))
    }
  } finally {
    previewLoading.value = false
  }
}

async function handleSubmit(): Promise<void> {
  if (!payload.value || !preview.value || busy.value) {
    if (payload.value && !preview.value) appStore.showInfo(t('admin.accounts.dataImportPreviewRequired'))
    return
  }
  busy.value = true
  try {
    const job = await adminAPI.accounts.importData(buildImportRequest())
    emit('imported', job)
    emit('close')
  } catch (error: any) {
    if (error?.response?.status === 409) {
      preview.value = null
      appStore.showWarning(t('admin.accounts.dataImportStalePreview'))
      await handlePreview()
    } else {
      appStore.showError(t('admin.accounts.dataImportFailed'))
    }
  } finally {
    busy.value = false
  }
}

watch([uniformDraft, targetGroupID], () => {
  preview.value = null
}, { deep: true })
</script>
