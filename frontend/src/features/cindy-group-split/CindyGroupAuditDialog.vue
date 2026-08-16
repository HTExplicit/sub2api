<template>
  <BaseDialog
    :show="show"
    :title="t('admin.groups.cindyAudit.title')"
    width="extra-wide"
    :close-on-escape="!submitting"
    :show-close-button="!submitting"
    @close="closeDialog"
  >
    <div v-if="!selectedGroup" class="min-w-0 space-y-4" data-test="cindy-group-audit">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <p class="max-w-3xl text-sm text-gray-600 dark:text-dark-300">
          {{ t('admin.groups.cindyAudit.description') }}
        </p>
        <button
          type="button"
          class="icon-button"
          :title="t('common.refresh')"
          :disabled="loadingAudit"
          data-test="cindy-group-audit-refresh"
          @click="loadAudit"
        >
          <Icon name="refresh" size="sm" :class="loadingAudit ? 'animate-spin' : ''" />
        </button>
      </div>

      <div v-if="audit" class="grid grid-cols-1 gap-2 sm:grid-cols-3" data-test="cindy-group-audit-summary">
        <div
          v-for="metric in summaryMetrics"
          :key="metric.key"
          class="rounded-md border border-gray-200 px-3 py-3 dark:border-dark-700"
        >
          <div class="text-lg font-semibold tabular-nums text-gray-900 dark:text-white">
            {{ metric.value }}
          </div>
          <div class="mt-0.5 text-xs text-gray-500 dark:text-dark-300">{{ metric.label }}</div>
        </div>
      </div>

      <div class="overflow-x-auto rounded-md border border-gray-200 dark:border-dark-700">
        <table class="min-w-[760px] w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
          <thead class="bg-gray-50 text-left text-xs text-gray-500 dark:bg-dark-900 dark:text-dark-300">
            <tr>
              <th class="px-3 py-2 font-medium">{{ t('admin.groups.cindyAudit.group') }}</th>
              <th class="px-3 py-2 font-medium">{{ t('admin.groups.cindyAudit.classification') }}</th>
              <th class="px-3 py-2 text-right font-medium">{{ t('admin.groups.cindyAudit.cindyAccounts') }}</th>
              <th class="px-3 py-2 text-right font-medium">{{ t('admin.groups.cindyAudit.ordinaryAccounts') }}</th>
              <th class="px-3 py-2 text-right font-medium">{{ t('admin.groups.cindyAudit.apiKeys') }}</th>
              <th class="px-3 py-2 text-right font-medium">{{ t('admin.groups.cindyAudit.action') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 bg-white text-gray-700 dark:divide-dark-700 dark:bg-dark-800 dark:text-gray-200">
            <tr v-for="group in audit?.groups || []" :key="group.group_id">
              <td class="max-w-64 px-3 py-2">
                <div class="truncate font-medium" :title="group.group_name">{{ group.group_name }}</div>
                <div class="mt-0.5 text-xs text-gray-400">#{{ group.group_id }} · {{ group.status }}</div>
              </td>
              <td class="px-3 py-2">
                <span class="rounded px-2 py-1 text-xs font-medium" :class="classificationClass(group.classification)">
                  {{ classificationLabel(group.classification) }}
                </span>
              </td>
              <td class="px-3 py-2 text-right tabular-nums">{{ group.cindy_account_count }}</td>
              <td class="px-3 py-2 text-right tabular-nums">{{ group.ordinary_account_count }}</td>
              <td class="px-3 py-2 text-right tabular-nums">{{ group.api_key_count }}</td>
              <td class="px-3 py-2 text-right">
                <button
                  v-if="group.classification === 'mixed'"
                  type="button"
                  class="btn btn-secondary h-8 px-3 text-xs"
                  :data-test="`cindy-group-split-${group.group_id}`"
                  @click="openSplitWizard(group)"
                >
                  {{ t('admin.groups.cindyAudit.split') }}
                </button>
                <span v-else class="text-gray-400">-</span>
              </td>
            </tr>
            <tr v-if="!loadingAudit && (audit?.groups.length || 0) === 0">
              <td colspan="6" class="px-4 py-8 text-center text-gray-400">
                {{ t('admin.groups.cindyAudit.empty') }}
              </td>
            </tr>
            <tr v-if="loadingAudit">
              <td colspan="6" class="px-4 py-8 text-center text-gray-400">{{ t('common.loading') }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <div v-else class="min-w-0 space-y-5" data-test="cindy-group-split-wizard">
      <div class="flex flex-wrap items-center gap-3 border-b border-gray-200 pb-3 dark:border-dark-700">
        <button
          type="button"
          class="icon-button"
          :title="t('admin.groups.cindyAudit.backToAudit')"
          :disabled="submitting"
          data-test="cindy-group-split-back"
          @click="leaveSplitWizard"
        >
          <Icon name="arrowLeft" size="sm" />
        </button>
        <div class="min-w-0">
          <h3 class="truncate text-sm font-semibold text-gray-900 dark:text-white" :title="selectedGroup.group_name">
            {{ selectedGroup.group_name }}
          </h3>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-300">
            {{ t('admin.groups.cindyAudit.mixedSummary', {
              cindy: selectedGroup.cindy_account_count,
              ordinary: selectedGroup.ordinary_account_count,
            }) }}
          </p>
        </div>
      </div>

      <div class="grid gap-5 lg:grid-cols-[minmax(280px,0.8fr)_minmax(0,1.2fr)]">
        <div class="min-w-0 space-y-4">
          <fieldset>
            <legend class="input-label">{{ t('admin.groups.cindyAudit.sourceKeeps') }}</legend>
            <div class="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-1">
              <label
                v-for="option in sourceKeepOptions"
                :key="option.value"
                class="flex cursor-pointer items-start gap-3 rounded-md border px-3 py-3 transition-colors"
                :class="sourceKeeps === option.value
                  ? 'border-primary-400 bg-primary-50/60 dark:border-primary-700 dark:bg-primary-900/10'
                  : 'border-gray-200 dark:border-dark-700'"
              >
                <input
                  v-model="sourceKeeps"
                  type="radio"
                  name="cindy-source-keeps"
                  :value="option.value"
                  class="mt-0.5 accent-primary-600"
                  :disabled="submitting"
                  :data-test="`cindy-source-keeps-${option.value}`"
                />
                <span class="min-w-0">
                  <span class="block text-sm font-medium text-gray-900 dark:text-white">{{ option.label }}</span>
                  <span class="mt-0.5 block text-xs text-gray-500 dark:text-dark-300">{{ option.description }}</span>
                </span>
              </label>
            </div>
          </fieldset>

          <div>
            <label for="cindy-split-target-name" class="input-label">{{ t('admin.groups.cindyAudit.targetName') }}</label>
            <input
              id="cindy-split-target-name"
              v-model="targetName"
              type="text"
              class="input"
              maxlength="100"
              :placeholder="t('admin.groups.cindyAudit.targetNamePlaceholder')"
              :disabled="submitting"
              data-test="cindy-group-target-name"
            />
          </div>

          <div class="rounded-md border border-gray-200 bg-gray-50 p-3 text-xs text-gray-600 dark:border-dark-700 dark:bg-dark-900 dark:text-dark-300">
            {{ t('admin.groups.cindyAudit.privacyNote') }}
          </div>
        </div>

        <div class="min-w-0 space-y-3">
          <div class="flex flex-wrap items-end justify-between gap-2">
            <div>
              <h4 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.groups.cindyAudit.keySelection') }}</h4>
              <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-300">{{ t('admin.groups.cindyAudit.keySelectionHint') }}</p>
            </div>
            <span class="text-xs tabular-nums text-gray-500 dark:text-dark-300">
              {{ t('admin.groups.cindyAudit.keysSelected', { selected: selectedKeyIds.length, total: apiKeys.length }) }}
            </span>
          </div>

          <div class="max-h-72 overflow-y-auto rounded-md border border-gray-200 dark:border-dark-700" data-test="cindy-group-api-keys">
            <label
              v-for="apiKey in apiKeys"
              :key="apiKey.id"
              class="flex cursor-pointer items-start gap-3 border-b border-gray-100 px-3 py-2.5 last:border-b-0 hover:bg-gray-50 dark:border-dark-700 dark:hover:bg-dark-900"
            >
              <input
                v-model="selectedKeyIds"
                type="checkbox"
                :value="apiKey.id"
                class="mt-0.5 accent-primary-600"
                :disabled="submitting"
                :data-test="`cindy-group-api-key-${apiKey.id}`"
              />
              <span class="min-w-0 flex-1">
                <span class="block truncate text-sm font-medium text-gray-900 dark:text-white" :title="apiKey.name">
                  {{ apiKey.name || t('admin.groups.cindyAudit.unnamedKey') }}
                </span>
                <span class="mt-0.5 block truncate font-mono text-xs text-gray-500 dark:text-dark-300">
                  {{ maskAPIKey(apiKey.key) }} · {{ apiKey.status }}
                </span>
              </span>
            </label>
            <div v-if="loadingKeys" class="px-3 py-8 text-center text-sm text-gray-400">{{ t('common.loading') }}</div>
            <div v-else-if="apiKeys.length === 0" class="px-3 py-8 text-center text-sm text-gray-400">
              {{ t('admin.groups.cindyAudit.noKeys') }}
            </div>
          </div>
        </div>
      </div>

      <div v-if="driftDetected" class="rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-900/15 dark:text-amber-200" data-test="cindy-group-split-drift">
        {{ t('admin.groups.cindyAudit.drift') }}
      </div>

      <section v-if="preview" class="rounded-md border border-primary-200 bg-primary-50/50 p-4 dark:border-primary-800/60 dark:bg-primary-900/10" data-test="cindy-group-split-preview">
        <div class="mb-3 flex flex-wrap items-center justify-between gap-2">
          <h4 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.groups.cindyAudit.previewTitle') }}</h4>
          <code class="rounded bg-white px-2 py-1 text-xs text-gray-600 dark:bg-dark-800 dark:text-dark-300">
            {{ shortFingerprint(preview.member_fingerprint) }}
          </code>
        </div>
        <dl class="grid grid-cols-1 gap-3 text-sm sm:grid-cols-2 lg:grid-cols-3">
          <div>
            <dt class="text-xs text-gray-500 dark:text-dark-300">{{ t('admin.groups.cindyAudit.sourceClassification') }}</dt>
            <dd class="mt-1 font-medium text-gray-900 dark:text-white">{{ classificationLabel(sourceClassification) }}</dd>
          </div>
          <div>
            <dt class="text-xs text-gray-500 dark:text-dark-300">{{ t('admin.groups.cindyAudit.targetClassification') }}</dt>
            <dd class="mt-1 font-medium text-gray-900 dark:text-white">{{ classificationLabel(preview.target_classification) }}</dd>
          </div>
          <div>
            <dt class="text-xs text-gray-500 dark:text-dark-300">{{ t('admin.groups.cindyAudit.accountsToMove') }}</dt>
            <dd class="mt-1 font-medium tabular-nums text-gray-900 dark:text-white">{{ preview.accounts_to_move }}</dd>
          </div>
          <div>
            <dt class="text-xs text-gray-500 dark:text-dark-300">{{ t('admin.groups.cindyAudit.cindyAccounts') }}</dt>
            <dd class="mt-1 font-medium tabular-nums text-gray-900 dark:text-white">{{ preview.cindy_account_count }}</dd>
          </div>
          <div>
            <dt class="text-xs text-gray-500 dark:text-dark-300">{{ t('admin.groups.cindyAudit.ordinaryAccounts') }}</dt>
            <dd class="mt-1 font-medium tabular-nums text-gray-900 dark:text-white">{{ preview.ordinary_account_count }}</dd>
          </div>
          <div>
            <dt class="text-xs text-gray-500 dark:text-dark-300">{{ t('admin.groups.cindyAudit.keyImpact') }}</dt>
            <dd class="mt-1 font-medium tabular-nums text-gray-900 dark:text-white">
              {{ t('admin.groups.cindyAudit.keyImpactValue', { moved: preview.api_keys_to_rebind, kept: preview.api_keys_remaining }) }}
            </dd>
          </div>
        </dl>
      </section>
    </div>

    <template #footer>
      <div v-if="!selectedGroup" class="flex justify-end">
        <button type="button" class="btn btn-secondary" @click="closeDialog">{{ t('common.close') }}</button>
      </div>
      <div v-else class="flex flex-col-reverse gap-2 sm:flex-row sm:justify-between">
        <button type="button" class="btn btn-secondary" :disabled="submitting" @click="leaveSplitWizard">
          {{ t('admin.groups.cindyAudit.backToAudit') }}
        </button>
        <div class="flex flex-col-reverse gap-2 sm:flex-row">
          <button
            type="button"
            class="btn btn-secondary"
            :disabled="!canPreview || previewing || submitting"
            data-test="cindy-group-split-preview-button"
            @click="requestPreview"
          >
            <Icon name="eye" size="sm" />
            {{ previewing ? t('admin.groups.cindyAudit.previewing') : t('admin.groups.cindyAudit.preview') }}
          </button>
          <button
            type="button"
            class="btn btn-primary"
            :disabled="!preview || previewing || submitting"
            data-test="cindy-group-split-submit"
            @click="commitSplit"
          >
            <Icon name="check" size="sm" />
            {{ submitting ? t('admin.groups.cindyAudit.splitting') : t('admin.groups.cindyAudit.confirmSplit') }}
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
import { adminAPI } from '@/api/admin'
import type { ApiKey } from '@/types'
import type {
  CindyGroupAuditEntry,
  CindyGroupAuditResult,
  CindyGroupClassification,
  CindyGroupSourceKeeps,
  CindyGroupSplitPreview,
  CindyGroupSplitResult,
} from '@/api/admin/groups'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

interface Props {
  show: boolean
}

const props = defineProps<Props>()
const emit = defineEmits<{
  (event: 'close'): void
  (event: 'split', result: CindyGroupSplitResult): void
}>()

const { t } = useI18n()
const appStore = useAppStore()

const audit = ref<CindyGroupAuditResult | null>(null)
const loadingAudit = ref(false)
const selectedGroup = ref<CindyGroupAuditEntry | null>(null)
const sourceKeeps = ref<CindyGroupSourceKeeps>('cindy')
const targetName = ref('')
const apiKeys = ref<ApiKey[]>([])
const selectedKeyIds = ref<number[]>([])
const loadingKeys = ref(false)
const preview = ref<CindyGroupSplitPreview | null>(null)
const previewing = ref(false)
const submitting = ref(false)
const driftDetected = ref(false)
let formRevision = 0
let keyLoadRevision = 0

const summaryMetrics = computed(() => audit.value ? [
  { key: 'pure_cindy', label: t('admin.groups.cindyAudit.summaryPureCindy'), value: audit.value.summary.pure_cindy_groups },
  { key: 'mixed', label: t('admin.groups.cindyAudit.summaryMixed'), value: audit.value.summary.mixed_groups },
  { key: 'no_cindy', label: t('admin.groups.cindyAudit.summaryNoCindy'), value: audit.value.summary.no_cindy_groups },
] : [])

const sourceKeepOptions = computed(() => [
  {
    value: 'cindy' as const,
    label: t('admin.groups.cindyAudit.keepCindy'),
    description: t('admin.groups.cindyAudit.keepCindyHint'),
  },
  {
    value: 'ordinary' as const,
    label: t('admin.groups.cindyAudit.keepOrdinary'),
    description: t('admin.groups.cindyAudit.keepOrdinaryHint'),
  },
])

const canPreview = computed(() => Boolean(selectedGroup.value && targetName.value.trim() && !loadingKeys.value))
const sourceClassification = computed<CindyGroupClassification>(() =>
  sourceKeeps.value === 'cindy' ? 'pure_cindy' : 'no_cindy',
)

function resetSplitWizard(): void {
  selectedGroup.value = null
  sourceKeeps.value = 'cindy'
  targetName.value = ''
  apiKeys.value = []
  selectedKeyIds.value = []
  preview.value = null
  previewing.value = false
  submitting.value = false
  driftDetected.value = false
  formRevision += 1
  keyLoadRevision += 1
}

async function loadAudit(): Promise<void> {
  loadingAudit.value = true
  try {
    audit.value = await adminAPI.groups.auditCindyGroups()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.groups.cindyAudit.loadFailed')))
  } finally {
    loadingAudit.value = false
  }
}

async function openSplitWizard(group: CindyGroupAuditEntry): Promise<void> {
  if (group.classification !== 'mixed') return
  resetSplitWizard()
  selectedGroup.value = group
  const requestRevision = ++keyLoadRevision
  loadingKeys.value = true
  try {
    const loaded: ApiKey[] = []
    let page = 1
    let pages = 1
    do {
      const response = await adminAPI.groups.getGroupApiKeys(group.group_id, page, 100)
      if (requestRevision !== keyLoadRevision || selectedGroup.value?.group_id !== group.group_id) return
      loaded.push(...response.items)
      pages = Math.max(1, response.pages || Math.ceil(response.total / Math.max(1, response.page_size)))
      page += 1
    } while (page <= pages)
    apiKeys.value = loaded
    selectedKeyIds.value = []
  } catch (error) {
    if (requestRevision === keyLoadRevision) {
      appStore.showError(extractApiErrorMessage(error, t('admin.groups.cindyAudit.keysLoadFailed')))
    }
  } finally {
    if (requestRevision === keyLoadRevision) loadingKeys.value = false
  }
}

function leaveSplitWizard(): void {
  if (!submitting.value) resetSplitWizard()
}

function closeDialog(): void {
  if (submitting.value) return
  resetSplitWizard()
  emit('close')
}

async function requestPreview(): Promise<void> {
  const group = selectedGroup.value
  if (!group || !canPreview.value) return
  const revision = formRevision
  previewing.value = true
  driftDetected.value = false
  preview.value = null
  try {
    const result = await adminAPI.groups.previewCindyGroupSplit(group.group_id, {
      source_keeps: sourceKeeps.value,
      target_name: targetName.value.trim(),
      api_key_ids: [...selectedKeyIds.value],
    })
    if (revision === formRevision && selectedGroup.value?.group_id === group.group_id) {
      preview.value = result
    }
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.groups.cindyAudit.previewFailed')))
  } finally {
    previewing.value = false
  }
}

async function commitSplit(): Promise<void> {
  const group = selectedGroup.value
  const currentPreview = preview.value
  if (!group || !currentPreview) return
  submitting.value = true
  try {
    const result = await adminAPI.groups.splitCindyGroup(group.group_id, {
      source_keeps: sourceKeeps.value,
      target_name: targetName.value.trim(),
      api_key_ids: [...selectedKeyIds.value],
      member_fingerprint: currentPreview.member_fingerprint,
    })
    appStore.showSuccess(t('admin.groups.cindyAudit.splitSuccess'))
    resetSplitWizard()
    await loadAudit()
    emit('split', result)
  } catch (error) {
    if (isConflict(error)) {
      preview.value = null
      driftDetected.value = true
      appStore.showError(t('admin.groups.cindyAudit.drift'))
    } else {
      appStore.showError(extractApiErrorMessage(error, t('admin.groups.cindyAudit.splitFailed')))
    }
  } finally {
    submitting.value = false
  }
}

function isConflict(error: unknown): boolean {
  if (!error || typeof error !== 'object') return false
  const candidate = error as {
    status?: number
    code?: string | number
    reason?: string
    response?: { status?: number; data?: { code?: string | number } }
  }
  const code = String(candidate.reason ?? candidate.code ?? candidate.response?.data?.code ?? '')
  return candidate.status === 409 || candidate.response?.status === 409 || code === 'CINDY_GROUP_SPLIT_DRIFT'
}

function classificationLabel(value: CindyGroupClassification): string {
  return t(`admin.groups.cindyAudit.classifications.${value}`)
}

function classificationClass(value: CindyGroupClassification): string {
  if (value === 'mixed') return 'bg-amber-50 text-amber-700 dark:bg-amber-900/20 dark:text-amber-300'
  if (value === 'pure_cindy') return 'bg-primary-50 text-primary-700 dark:bg-primary-900/20 dark:text-primary-300'
  return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-dark-300'
}

function maskAPIKey(value: string): string {
  const safe = String(value || '')
  if (!safe) return '****'
  const prefix = safe.slice(0, Math.min(3, safe.length))
  const suffix = safe.slice(-4)
  return `${prefix}****${suffix}`
}

function shortFingerprint(value: string): string {
  if (value.length <= 20) return value
  return `${value.slice(0, 10)}...${value.slice(-6)}`
}

watch(
  [sourceKeeps, targetName, selectedKeyIds],
  () => {
    formRevision += 1
    preview.value = null
    driftDetected.value = false
  },
  { deep: true },
)

watch(
  () => props.show,
  (show) => {
    if (show) {
      resetSplitWizard()
      void loadAudit()
    } else {
      resetSplitWizard()
    }
  },
  { immediate: true },
)
</script>
