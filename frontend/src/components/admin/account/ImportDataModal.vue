<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.dataImportTitle')"
    width="wide"
    :close-on-click-outside="stage === 'select'"
    @close="handleClose"
  >
    <div class="space-y-4" data-test="account-import-wizard">
      <ol class="grid grid-cols-3 border-b border-gray-200 pb-3 text-xs dark:border-dark-700">
        <li v-for="(step, index) in steps" :key="step.key" class="flex items-center gap-2" :class="stepClass(index)">
          <span class="inline-flex h-6 w-6 items-center justify-center rounded-full border text-xs font-semibold">{{ index + 1 }}</span>
          <span class="truncate">{{ step.label }}</span>
        </li>
      </ol>

      <form v-if="stage === 'select'" id="import-data-preview-form" class="space-y-4" @submit.prevent="handlePreview">
        <p class="text-sm text-gray-600 dark:text-dark-300">{{ t('admin.accounts.dataImportHint') }}</p>
        <div class="rounded-md border border-blue-200 bg-blue-50 px-3 py-2 text-xs text-blue-700 dark:border-blue-800 dark:bg-blue-900/20 dark:text-blue-300">
          {{ t('admin.accounts.dataImportPreviewNoWrite') }}
        </div>
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
            <button type="button" class="btn btn-secondary shrink-0" @click="openFilePicker">
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
      </form>

      <template v-else-if="stage === 'preview' && preview">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <div>
            <div class="text-sm font-semibold text-gray-900 dark:text-white">
              {{ t('admin.accounts.dataImportPreviewSummary', { accounts: preview.accounts.length, proxies: preview.proxies.length }) }}
            </div>
            <div class="mt-0.5 text-xs text-gray-500 dark:text-dark-300">{{ t('admin.accounts.dataImportVersion', { version: preview.version }) }}</div>
          </div>
          <div class="flex items-center gap-2 text-xs">
            <span class="rounded bg-emerald-50 px-2 py-1 text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300">
              {{ t('admin.accounts.importWillCreate', { count: actionCount('create') }) }}
            </span>
            <span class="rounded bg-blue-50 px-2 py-1 text-blue-700 dark:bg-blue-900/20 dark:text-blue-300">
              {{ t('admin.accounts.importWillUpdate', { count: actionCount('update') }) }}
            </span>
            <span class="rounded bg-gray-100 px-2 py-1 text-gray-600 dark:bg-dark-700 dark:text-gray-300">
              {{ t('admin.accounts.importWillSkip', { count: actionCount('skip') }) }}
            </span>
          </div>
        </div>

        <details class="rounded-md border border-gray-200 dark:border-dark-700">
          <summary class="cursor-pointer px-3 py-2.5 text-sm font-medium text-gray-800 dark:text-gray-100">
            {{ t('admin.accounts.importUniformSettings') }}
          </summary>
          <div class="border-t border-gray-100 px-3 py-3 dark:border-dark-700">
            <p class="mb-3 text-xs text-gray-500 dark:text-dark-300">{{ t('admin.accounts.importSettingsPriority') }}</p>
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

        <div class="max-h-[50vh] overflow-y-auto rounded-md border border-gray-200 dark:border-dark-700">
          <div
            v-for="item in preview.accounts"
            :key="item.index"
            class="border-b border-gray-100 last:border-b-0 dark:border-dark-700"
            :data-test="`import-account-${item.index}`"
          >
            <div class="grid grid-cols-[minmax(0,1fr)_8.5rem_auto] items-start gap-3 px-3 py-3">
              <div class="min-w-0">
                <div class="flex min-w-0 flex-wrap items-center gap-1.5">
                  <span class="truncate text-sm font-semibold text-gray-900 dark:text-white">{{ item.name }}</span>
                  <span class="rounded bg-gray-100 px-1.5 py-0.5 text-[10px] text-gray-600 dark:bg-dark-700 dark:text-gray-300">
                    {{ item.platform }} / {{ item.type }}
                  </span>
                  <span v-if="!item.valid" class="rounded bg-red-50 px-1.5 py-0.5 text-[10px] text-red-700 dark:bg-red-900/20 dark:text-red-300">
                    {{ t('admin.accounts.importInvalid') }}
                  </span>
                </div>
                <div class="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-xs text-gray-500 dark:text-dark-300">
                  <span v-if="item.masked_email">{{ item.masked_email }}</span>
                  <span v-if="item.plan">{{ item.plan }}</span>
                  <span v-if="item.management_folder">{{ t('admin.accounts.folder') }}: {{ item.management_folder }}</span>
                  <span v-if="item.tags?.length">{{ t('admin.accounts.tags') }}: {{ item.tags.join(', ') }}</span>
                </div>
                <div v-if="item.strong_identity_matches?.length" class="mt-1.5 text-xs text-amber-700 dark:text-amber-300">
                  {{ t('admin.accounts.importStrongConflict', { count: item.strong_identity_matches.length }) }}
                </div>
                <div v-for="message in item.warnings || []" :key="message" class="mt-1 text-xs text-amber-600 dark:text-amber-300">{{ message }}</div>
                <div v-for="message in item.errors || []" :key="message" class="mt-1 text-xs text-red-600 dark:text-red-300">{{ message }}</div>
              </div>

              <div>
                <select
                  v-model="itemDrafts[item.index].action"
                  class="input h-9 py-1.5 text-sm"
                  :disabled="!item.valid"
                  :data-test="`import-action-${item.index}`"
                  @change="normalizeUpdateTarget(item)"
                >
                  <option value="skip">{{ t('admin.accounts.importActionSkip') }}</option>
                  <option value="update" :disabled="!item.strong_identity_matches?.length">{{ t('admin.accounts.importActionUpdate') }}</option>
                  <option value="create">{{ t('admin.accounts.importActionCreate') }}</option>
                </select>
                <select
                  v-if="itemDrafts[item.index].action === 'update' && (item.strong_identity_matches?.length || 0) > 1"
                  v-model.number="itemDrafts[item.index].existingAccountID"
                  class="input mt-2 h-9 py-1.5 text-xs"
                >
                  <option v-for="match in item.strong_identity_matches" :key="match.account_id" :value="match.account_id">
                    #{{ match.account_id }} {{ match.name }}
                  </option>
                </select>
              </div>

              <button
                type="button"
                class="icon-button"
                :title="t('admin.accounts.importItemOverrides')"
                :aria-expanded="itemDrafts[item.index].expanded"
                @click="itemDrafts[item.index].expanded = !itemDrafts[item.index].expanded"
              >
                <Icon :name="itemDrafts[item.index].expanded ? 'chevronUp' : 'chevronDown'" size="sm" />
              </button>
            </div>

            <div v-if="itemDrafts[item.index].expanded" class="border-t border-gray-100 bg-gray-50/70 px-3 py-3 dark:border-dark-700 dark:bg-dark-800/50">
              <div class="mb-2 text-xs font-semibold uppercase text-gray-500 dark:text-dark-300">{{ t('admin.accounts.importItemOverrides') }}</div>
              <AccountImportSettingsEditor
                v-model="itemDrafts[item.index].settings"
                mode="item"
                :uid="`item-${item.index}`"
                :folders="folders"
                :tags="tags"
                :groups="groups"
                :proxies="proxies"
              />
            </div>
          </div>
        </div>

        <div class="rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-700 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-300">
          {{ t('admin.accounts.dataImportFinalWarning') }}
        </div>
      </template>

      <template v-else-if="stage === 'result' && result">
        <div class="grid grid-cols-2 gap-2 sm:grid-cols-4">
          <div class="rounded-md bg-emerald-50 px-3 py-3 dark:bg-emerald-900/20">
            <div class="text-xs text-emerald-700 dark:text-emerald-300">{{ t('admin.accounts.importCreated') }}</div>
            <div class="mt-1 text-xl font-semibold tabular-nums text-emerald-800 dark:text-emerald-200">{{ result.account_created }}</div>
          </div>
          <div class="rounded-md bg-blue-50 px-3 py-3 dark:bg-blue-900/20">
            <div class="text-xs text-blue-700 dark:text-blue-300">{{ t('admin.accounts.importUpdated') }}</div>
            <div class="mt-1 text-xl font-semibold tabular-nums text-blue-800 dark:text-blue-200">{{ result.account_updated }}</div>
          </div>
          <div class="rounded-md bg-gray-100 px-3 py-3 dark:bg-dark-800">
            <div class="text-xs text-gray-600 dark:text-gray-300">{{ t('admin.accounts.importSkipped') }}</div>
            <div class="mt-1 text-xl font-semibold tabular-nums text-gray-800 dark:text-gray-100">{{ result.account_skipped }}</div>
          </div>
          <div class="rounded-md bg-red-50 px-3 py-3 dark:bg-red-900/20">
            <div class="text-xs text-red-700 dark:text-red-300">{{ t('admin.accounts.importFailed') }}</div>
            <div class="mt-1 text-xl font-semibold tabular-nums text-red-800 dark:text-red-200">{{ result.account_failed }}</div>
          </div>
        </div>

        <div class="max-h-[52vh] overflow-y-auto rounded-md border border-gray-200 dark:border-dark-700">
          <div v-for="item in result.items" :key="item.index" class="flex items-start gap-3 border-b border-gray-100 px-3 py-2.5 last:border-b-0 dark:border-dark-700">
            <span class="mt-0.5 rounded px-1.5 py-0.5 text-[10px] font-semibold" :class="resultActionClass(item.action)">
              {{ resultActionLabel(item.action) }}
            </span>
            <div class="min-w-0 flex-1">
              <div class="truncate text-sm text-gray-800 dark:text-gray-100">{{ item.name }}</div>
              <div v-if="item.account_id" class="mt-0.5 font-mono text-xs text-gray-400">#{{ item.account_id }}</div>
              <div v-if="item.error" class="mt-1 whitespace-pre-wrap text-xs text-red-600 dark:text-red-300">{{ item.error }}</div>
              <div v-for="warning in item.warnings || []" :key="warning" class="mt-1 text-xs text-amber-600 dark:text-amber-300">{{ warning }}</div>
            </div>
          </div>
        </div>
        <p v-if="result.account_ids.length" class="text-xs text-gray-500 dark:text-dark-300">
          {{ t('admin.accounts.importSessionReady', { count: result.account_ids.length }) }}
        </p>
      </template>
    </div>

    <template #footer>
      <div class="flex w-full justify-between gap-3">
        <button v-if="stage === 'preview'" type="button" class="btn btn-secondary" :disabled="busy" @click="backToSelect">
          <Icon name="arrowLeft" size="sm" />
          <span>{{ t('common.back') }}</span>
        </button>
        <span v-else />
        <div class="flex gap-3">
          <button type="button" class="btn btn-secondary" :disabled="busy" @click="handleClose">
            {{ stage === 'result' ? t('common.close') : t('common.cancel') }}
          </button>
          <button
            v-if="stage === 'select'"
            type="submit"
            form="import-data-preview-form"
            class="btn btn-primary"
            :disabled="busy || files.length === 0"
            data-test="preview-import"
          >
            {{ busy ? t('admin.accounts.dataImportPreviewing') : t('admin.accounts.dataImportPreview') }}
          </button>
          <button
            v-else-if="stage === 'preview'"
            type="button"
            class="btn btn-primary"
            :disabled="busy || actionableCount === 0"
            data-test="confirm-import"
            @click="handleImport"
          >
            {{ busy ? t('admin.accounts.dataImporting') : t('admin.accounts.dataImportConfirm') }}
          </button>
        </div>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import AccountImportSettingsEditor, { type AccountImportSettingsDraft } from './AccountImportSettingsEditor.vue'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import type {
  AccountManagementFolder,
  AccountManagementTag,
  AdminDataImportAction,
  AdminDataImportItemDecision,
  AdminDataImportItemOverrides,
  AdminDataImportPreviewAccount,
  AdminDataImportPreviewResult,
  AdminDataImportResult,
  AdminDataImportUniformSettings,
  AdminDataPayload,
  AdminGroup,
  Proxy
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
  proxies: () => []
})

const emit = defineEmits<{
  close: []
  imported: [result: AdminDataImportResult]
}>()

type WizardStage = 'select' | 'preview' | 'result'
type ItemDraft = {
  action: AdminDataImportAction
  existingAccountID?: number
  expanded: boolean
  settings: AccountImportSettingsDraft
}

const { t } = useI18n()
const appStore = useAppStore()
const stage = ref<WizardStage>('select')
const busy = ref(false)
const files = ref<File[]>([])
const payload = ref<AdminDataPayload | null>(null)
const preview = ref<AdminDataImportPreviewResult | null>(null)
const result = ref<AdminDataImportResult | null>(null)
const itemDrafts = ref<Record<number, ItemDraft>>({})
const uniformDraft = ref(makeSettingsDraft())
const dragDepth = ref(0)
const fileInput = ref<HTMLInputElement | null>(null)

const steps = computed(() => [
  { key: 'select', label: t('admin.accounts.importStepFile') },
  { key: 'preview', label: t('admin.accounts.importStepConfigure') },
  { key: 'result', label: t('admin.accounts.importStepResult') }
])
const stageIndex = computed(() => ({ select: 0, preview: 1, result: 2 })[stage.value])
const stepClass = (index: number) => index <= stageIndex.value
  ? 'font-medium text-primary-700 dark:text-primary-300 [&>span:first-child]:border-primary-400 [&>span:first-child]:bg-primary-50 dark:[&>span:first-child]:bg-primary-900/20'
  : 'text-gray-400 [&>span:first-child]:border-gray-200 dark:[&>span:first-child]:border-dark-700'

const dragActive = computed(() => dragDepth.value > 0)
const selectedFilesLabel = computed(() => {
  if (!files.value.length) return ''
  if (files.value.length === 1) return files.value[0]?.name || ''
  return t('admin.accounts.selectedCount', { count: files.value.length })
})
const fileListTitle = computed(() => files.value.map(file => file.name).join(', '))
const actionableCount = computed(() => Object.values(itemDrafts.value).filter(item => item.action !== 'skip').length)

watch(() => props.show, open => {
  if (open) resetWizard()
})

function makeSettingsDraft(): AccountImportSettingsDraft {
  return {
    enabled: {
      name: false, namePrefix: false, nameSuffix: false, notes: false, folder: false,
      tags: false, groups: false, proxy: false, concurrency: false, priority: false,
      rateMultiplier: false, status: false, schedulable: false
    },
    name: '', namePrefix: '', nameSuffix: '', notesMode: 'append', notesValue: '', folder: '',
    tagsText: '', groupIDs: [], proxyID: '0', concurrency: 1, priority: 0,
    rateMultiplier: 1, status: 'active', schedulable: true
  }
}

const resetWizard = () => {
  stage.value = 'select'
  busy.value = false
  files.value = []
  payload.value = null
  preview.value = null
  result.value = null
  itemDrafts.value = {}
  uniformDraft.value = makeSettingsDraft()
  dragDepth.value = 0
  if (fileInput.value) fileInput.value.value = ''
}

const handleClose = () => {
  if (!busy.value) emit('close')
}
const backToSelect = () => {
  if (busy.value) return
  stage.value = 'select'
  preview.value = null
  itemDrafts.value = {}
}
const openFilePicker = () => fileInput.value?.click()
const handleFileChange = (event: Event) => {
  const input = event.target as HTMLInputElement
  setSelectedFiles(input.files)
  input.value = ''
}
const isJsonFile = (file: File) => file.name.toLowerCase().endsWith('.json') || file.type === 'application/json'
const setSelectedFiles = (source: FileList | File[] | null | undefined) => {
  if (busy.value) return
  const incoming = Array.from(source || [])
  const accepted = incoming.filter(isJsonFile)
  if (!accepted.length) {
    appStore.showError(t('admin.accounts.dataImportSelectFile'))
    return
  }
  if (accepted.length !== incoming.length) {
    appStore.showWarning(t('admin.accounts.dataImportIgnoredFiles', { count: incoming.length - accepted.length }))
  }
  files.value = accepted
}
const handleDragEnter = () => { if (!busy.value) dragDepth.value += 1 }
const handleDragLeave = () => { dragDepth.value = Math.max(0, dragDepth.value - 1) }
const handleDrop = (event: DragEvent) => {
  dragDepth.value = 0
  setSelectedFiles(event.dataTransfer?.files)
}

const readFileAsText = async (file: File) => {
  if (typeof file.text === 'function') return file.text()
  if (typeof file.arrayBuffer === 'function') return new TextDecoder().decode(await file.arrayBuffer())
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result || ''))
    reader.onerror = () => reject(reader.error || new Error('Failed to read file'))
    reader.readAsText(file)
  })
}

const isValidDataPayload = (value: unknown): value is AdminDataPayload => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false
  const candidate = value as Partial<AdminDataPayload>
  if (candidate.type && !['sub2api-data', 'sub2api-bundle'].includes(candidate.type)) return false
  if (candidate.version && ![1, 2].includes(candidate.version)) return false
  return Array.isArray(candidate.accounts) && Array.isArray(candidate.proxies)
}

const mergeDataPayloads = (payloads: AdminDataPayload[]): AdminDataPayload => {
  if (payloads.length === 1) return payloads[0]!
  return {
    type: 'sub2api-data',
    version: Math.max(1, ...payloads.map(item => item.version || 1)),
    exported_at: new Date().toISOString(),
    proxies: payloads.flatMap(item => item.proxies),
    accounts: payloads.flatMap(item => item.accounts),
    skipped_shadows: payloads.reduce((sum, item) => sum + Number(item.skipped_shadows || 0), 0)
  }
}

const parseFiles = async () => {
  const parsedPayloads: AdminDataPayload[] = []
  for (const file of files.value) {
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
  return mergeDataPayloads(parsedPayloads)
}

const initializeItemDrafts = (items: AdminDataImportPreviewAccount[]) => {
  const drafts: Record<number, ItemDraft> = {}
  for (const item of items) {
    drafts[item.index] = {
      action: item.valid ? item.default_action : 'skip',
      existingAccountID: item.strong_identity_matches?.length === 1
        ? item.strong_identity_matches[0]?.account_id
        : undefined,
      expanded: false,
      settings: makeSettingsDraft()
    }
  }
  itemDrafts.value = drafts
}

const handlePreview = async () => {
  if (!files.value.length || busy.value) return
  busy.value = true
  try {
    const nextPayload = await parseFiles()
    const nextPreview = await adminAPI.accounts.previewDataImport(nextPayload)
    payload.value = nextPayload
    preview.value = nextPreview
    initializeItemDrafts(nextPreview.accounts)
    stage.value = 'preview'
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.accounts.dataImportFailed'))
  } finally {
    busy.value = false
  }
}

const normalizeUpdateTarget = (item: AdminDataImportPreviewAccount) => {
  const draft = itemDrafts.value[item.index]
  if (!draft || draft.action !== 'update') return
  const matches = item.strong_identity_matches || []
  if (!matches.some(match => match.account_id === draft.existingAccountID)) {
    draft.existingAccountID = matches[0]?.account_id
  }
}

const parseTags = (value: string) => Array.from(new Set(value.split(',').map(item => item.trim()).filter(Boolean)))

const buildSettings = (draft: AccountImportSettingsDraft, mode: 'uniform' | 'item') => {
  const settings: AdminDataImportUniformSettings | AdminDataImportItemOverrides = {}
  if (mode === 'uniform') {
    const uniform = settings as AdminDataImportUniformSettings
    if (draft.enabled.namePrefix) uniform.name_prefix = draft.namePrefix
    if (draft.enabled.nameSuffix) uniform.name_suffix = draft.nameSuffix
  } else if (draft.enabled.name) {
    (settings as AdminDataImportItemOverrides).name = draft.name.trim()
  }
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

const handleImport = async () => {
  if (!payload.value || !preview.value || busy.value || actionableCount.value === 0) return
  busy.value = true
  try {
    const decisions: AdminDataImportItemDecision[] = preview.value.accounts.map(item => {
      const draft = itemDrafts.value[item.index]
      const decision: AdminDataImportItemDecision = {
        index: item.index,
        action: item.valid ? draft.action : 'skip'
      }
      if (decision.action === 'update' && draft.existingAccountID) {
        decision.existing_account_id = draft.existingAccountID
      }
      const overrides = buildSettings(draft.settings, 'item') as AdminDataImportItemOverrides
      if (Object.keys(overrides).length) decision.overrides = overrides
      return decision
    })
    const imported = await adminAPI.accounts.importData({
      data: payload.value,
      skip_default_group_bind: true,
      uniform_settings: buildSettings(uniformDraft.value, 'uniform') as AdminDataImportUniformSettings,
      items: decisions
    })
    result.value = imported
    stage.value = 'result'
    emit('imported', imported)
    if (imported.account_failed || imported.proxy_failed) {
      appStore.showWarning(t('admin.accounts.dataImportCompletedWithErrors', { ...imported }))
    } else {
      appStore.showSuccess(t('admin.accounts.dataImportSuccess', { ...imported }))
    }
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.accounts.dataImportFailed'))
  } finally {
    busy.value = false
  }
}

const actionCount = (action: AdminDataImportAction) => Object.values(itemDrafts.value).filter(item => item.action === action).length
const resultActionLabel = (action: AdminDataImportAction | 'failed') => ({
  create: t('admin.accounts.importCreated'),
  update: t('admin.accounts.importUpdated'),
  skip: t('admin.accounts.importSkipped'),
  failed: t('admin.accounts.importFailed')
})[action]
const resultActionClass = (action: AdminDataImportAction | 'failed') => ({
  create: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300',
  update: 'bg-blue-50 text-blue-700 dark:bg-blue-900/20 dark:text-blue-300',
  skip: 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300',
  failed: 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-300'
})[action]
</script>
