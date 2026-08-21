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
    </form>

    <template #footer>
      <button type="button" class="btn btn-secondary" :disabled="busy" @click="handleClose">
        {{ t('common.cancel') }}
      </button>
      <button
        type="submit"
        form="account-import-job-form"
        data-test="submit-import-job"
        class="btn btn-primary"
        :disabled="busy || !payload"
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

async function handleSubmit(): Promise<void> {
  if (!payload.value || busy.value) return
  busy.value = true
  try {
    const job = await adminAPI.accounts.importData({
      data: payload.value,
      skip_default_group_bind: true,
      uniform_settings: buildUniformSettings(uniformDraft.value),
    })
    emit('imported', job)
    emit('close')
  } catch {
    appStore.showError(t('admin.accounts.dataImportFailed'))
  } finally {
    busy.value = false
  }
}
</script>
