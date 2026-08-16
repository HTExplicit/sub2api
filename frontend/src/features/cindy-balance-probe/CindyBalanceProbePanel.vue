<template>
  <section class="mb-4 overflow-hidden rounded-md border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800" data-test="cindy-probe-panel">
    <header class="flex flex-wrap items-start justify-between gap-3 border-b border-gray-200 px-4 py-3 dark:border-dark-700">
      <div>
        <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.accounts.cindyProbe.title') }}</h2>
        <p class="mt-1 text-xs text-gray-500 dark:text-dark-300">{{ t('admin.accounts.cindyProbe.description') }}</p>
      </div>
      <div class="flex items-center gap-2">
        <span v-if="activeJob && !expanded" class="rounded px-2 py-1 text-xs font-medium" :class="jobStatusClass(activeJob.status)">
          {{ jobStatusLabel(activeJob.status) }}
        </span>
        <button type="button" class="icon-button" :title="t('common.refresh')" :disabled="loading.jobs" @click="loadJobs">
          <Icon name="refresh" size="sm" :class="loading.jobs ? 'animate-spin' : ''" />
        </button>
        <button type="button" class="icon-button" :title="expanded ? t('admin.accounts.cindyProbe.collapse') : t('admin.accounts.cindyProbe.expand')" data-test="cindy-probe-toggle" @click="expanded = !expanded">
          <Icon :name="expanded ? 'chevronUp' : 'chevronDown'" size="sm" />
        </button>
      </div>
    </header>

    <div v-if="expanded" class="grid gap-5 p-4 xl:grid-cols-[minmax(300px,0.9fr)_minmax(0,1.6fr)]">
      <div class="min-w-0 space-y-4">
        <div>
          <div class="mb-2 text-xs font-medium text-gray-600 dark:text-dark-300">{{ t('admin.accounts.cindyProbe.scope') }}</div>
          <div class="grid grid-cols-3 rounded-md bg-gray-100 p-1 dark:bg-dark-900" role="group">
            <button
              v-for="option in scopeOptions"
              :key="option.value"
              type="button"
              class="min-h-9 rounded px-2 text-xs font-medium transition-colors"
              :class="scopeMode === option.value ? activeSegmentClass : inactiveSegmentClass"
              :disabled="loading.preview || loading.create"
              :data-test="`cindy-probe-scope-${option.value}`"
              @click="scopeMode = option.value"
            >
              {{ option.label }}
            </button>
          </div>
          <p v-if="scopeMode === 'selected'" class="mt-1.5 text-xs text-gray-500 dark:text-dark-300">
            {{ t('admin.accounts.cindyProbe.selectedCount', { count: selectedIds.length }) }}
          </p>
        </div>

        <div>
          <div class="mb-2 flex items-center justify-between gap-3">
            <label for="cindy-probe-rate" class="text-xs font-medium text-gray-600 dark:text-dark-300">
              {{ t('admin.accounts.cindyProbe.rate') }}
            </label>
            <span class="text-sm font-semibold tabular-nums text-gray-900 dark:text-white">{{ normalizedRate.toFixed(1) }} RPS</span>
          </div>
          <input
            id="cindy-probe-rate"
            v-model.number="rateRPS"
            type="range"
            min="0.1"
            max="1"
            step="0.1"
            class="w-full accent-primary-600"
            :disabled="loading.preview || loading.create"
            data-test="cindy-probe-rate"
          />
          <div class="mt-1 flex justify-between text-[11px] text-gray-400"><span>0.1</span><span>1.0</span></div>
        </div>

        <button
          type="button"
          class="btn btn-primary w-full"
          :disabled="loading.preview || loading.create || (scopeMode === 'selected' && selectedIds.length === 0)"
          data-test="cindy-probe-preview"
          @click="previewJob"
        >
          <Icon name="eye" size="sm" />
          {{ loading.preview ? t('admin.accounts.cindyProbe.previewing') : t('admin.accounts.cindyProbe.preview') }}
        </button>

        <div v-if="preview" class="rounded-md border border-primary-200 bg-primary-50/60 p-3 dark:border-primary-800/60 dark:bg-primary-900/10" data-test="cindy-probe-preview-result">
          <div class="grid grid-cols-3 gap-2 text-center">
            <div v-for="metric in previewMetrics" :key="metric.label" class="min-w-0">
              <div class="text-base font-semibold tabular-nums text-gray-900 dark:text-white">{{ metric.value }}</div>
              <div class="truncate text-[11px] text-gray-500 dark:text-dark-300">{{ metric.label }}</div>
            </div>
          </div>
          <div class="mt-3 space-y-1 border-t border-primary-200 pt-3 text-xs text-gray-600 dark:border-primary-800/60 dark:text-dark-300">
            <div class="flex justify-between gap-3">
              <span>{{ t('admin.accounts.cindyProbe.calls') }}</span>
              <span class="font-medium tabular-nums text-gray-900 dark:text-white">{{ preview.minimum_calls }} - {{ preview.maximum_calls }}</span>
            </div>
            <div class="flex justify-between gap-3">
              <span>{{ t('admin.accounts.cindyProbe.eta') }}</span>
              <span class="font-medium tabular-nums text-gray-900 dark:text-white">{{ formatDurationRange(preview.minimum_eta_seconds, preview.maximum_eta_seconds) }}</span>
            </div>
          </div>
          <button
            type="button"
            class="btn btn-primary mt-3 w-full"
            :disabled="loading.create || preview.candidate_count === 0"
            data-test="cindy-probe-create"
            @click="createJob"
          >
            <Icon name="play" size="sm" />
            {{ loading.create ? t('admin.accounts.cindyProbe.creating') : t('admin.accounts.cindyProbe.confirmCreate') }}
          </button>
        </div>
      </div>

      <div class="min-w-0 border-t border-gray-200 pt-4 dark:border-dark-700 xl:border-l xl:border-t-0 xl:pl-5 xl:pt-0">
        <div class="flex flex-wrap items-end justify-between gap-3">
          <div class="min-w-[220px] flex-1">
            <label for="cindy-probe-job" class="input-label">{{ t('admin.accounts.cindyProbe.currentAndRecent') }}</label>
            <select id="cindy-probe-job" v-model.number="selectedJobID" class="input" :disabled="jobs.length === 0" data-test="cindy-probe-job-select">
              <option :value="0">{{ t('admin.accounts.cindyProbe.noJobs') }}</option>
              <option v-for="job in jobs" :key="job.id" :value="job.id">
                #{{ job.id }} · {{ jobStatusLabel(job.status) }} · {{ formatDate(job.created_at) }}
              </option>
            </select>
          </div>
          <span v-if="activeJob" class="rounded px-2 py-1 text-xs font-medium" :class="jobStatusClass(activeJob.status)" data-test="cindy-probe-job-status">
            {{ jobStatusLabel(activeJob.status) }}
          </span>
        </div>

        <div v-if="activeJob" class="mt-4 space-y-4" data-test="cindy-probe-job-detail">
          <div>
            <div class="mb-1.5 flex items-center justify-between text-xs text-gray-500 dark:text-dark-300">
              <span>{{ t('admin.accounts.cindyProbe.progress') }}</span>
              <span class="tabular-nums">{{ completedCount }} / {{ activeJob.candidate_count }}</span>
            </div>
            <div class="h-2 overflow-hidden rounded bg-gray-100 dark:bg-dark-900">
              <div class="h-full bg-primary-500 transition-[width]" :style="{ width: `${progressPercent}%` }" />
            </div>
          </div>

          <div class="grid grid-cols-2 gap-2 sm:grid-cols-4 lg:grid-cols-7" data-test="cindy-probe-counts">
            <div v-for="metric in jobCountMetrics" :key="metric.key" class="rounded border border-gray-200 px-2 py-2 text-center dark:border-dark-700">
              <div class="text-sm font-semibold tabular-nums text-gray-900 dark:text-white">{{ metric.value }}</div>
              <div class="mt-0.5 truncate text-[11px] text-gray-500 dark:text-dark-300">{{ metric.label }}</div>
            </div>
          </div>

          <div class="flex flex-wrap items-end gap-2 border-y border-gray-200 py-3 dark:border-dark-700">
            <div class="w-40">
              <label for="cindy-probe-job-rate" class="input-label">{{ t('admin.accounts.cindyProbe.jobRate') }}</label>
              <input id="cindy-probe-job-rate" v-model.number="jobRateRPS" type="number" min="0.1" max="1" step="0.1" class="input" :disabled="!canChangeRate || loading.action" data-test="cindy-probe-job-rate" />
            </div>
            <button type="button" class="btn btn-secondary" :disabled="!canChangeRate || loading.action" data-test="cindy-probe-save-rate" @click="saveJobRate">
              {{ t('common.save') }}
            </button>
            <button v-if="canPause" type="button" class="btn btn-secondary" :disabled="loading.action" data-test="cindy-probe-pause" @click="pauseJob">
              <Icon name="ban" size="sm" /> {{ t('admin.accounts.cindyProbe.pause') }}
            </button>
            <button v-if="canResume" type="button" class="btn btn-secondary" :disabled="loading.action" data-test="cindy-probe-resume" @click="resumeJob">
              <Icon name="play" size="sm" /> {{ t('admin.accounts.cindyProbe.resume') }}
            </button>
            <button v-if="canCancel" type="button" class="btn btn-danger" :disabled="loading.action" data-test="cindy-probe-cancel" @click="cancelJob">
              <Icon name="x" size="sm" /> {{ t('admin.accounts.cindyProbe.cancel') }}
            </button>
          </div>

          <div class="min-w-0">
            <div class="mb-2 flex flex-wrap items-end justify-between gap-2">
              <h3 class="text-xs font-semibold uppercase text-gray-500 dark:text-dark-300">{{ t('admin.accounts.cindyProbe.items') }}</h3>
              <select v-model="itemState" class="input h-9 w-44 py-1 text-xs" data-test="cindy-probe-item-state">
                <option value="">{{ t('admin.accounts.cindyProbe.allStates') }}</option>
                <option v-for="state in itemStateOptions" :key="state" :value="state">{{ itemStateLabel(state) }}</option>
              </select>
            </div>
            <div class="overflow-x-auto rounded-md border border-gray-200 dark:border-dark-700">
              <table class="min-w-full divide-y divide-gray-200 text-xs dark:divide-dark-700">
                <thead class="bg-gray-50 text-left text-gray-500 dark:bg-dark-900 dark:text-dark-300">
                  <tr>
                    <th class="px-3 py-2 font-medium">{{ t('admin.accounts.cindyProbe.account') }}</th>
                    <th class="px-3 py-2 font-medium">{{ t('admin.accounts.cindyProbe.state') }}</th>
                    <th class="px-3 py-2 font-medium">Luna</th>
                    <th class="px-3 py-2 font-medium">Terra</th>
                    <th class="whitespace-nowrap px-3 py-2 font-medium">{{ t('admin.accounts.cindyProbe.checkedAt') }}</th>
                    <th class="px-3 py-2 text-right font-medium">{{ t('admin.accounts.cindyProbe.requests') }}</th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-gray-100 bg-white text-gray-700 dark:divide-dark-700 dark:bg-dark-800 dark:text-gray-200">
                  <tr v-for="item in itemPage.items" :key="item.id">
                    <td class="px-3 py-2 tabular-nums">#{{ item.account_id }}</td>
                    <td class="px-3 py-2">{{ itemStateLabel(item.state) }}</td>
                    <td class="px-3 py-2">{{ item.luna_outcome || '-' }}</td>
                    <td class="px-3 py-2">{{ item.terra_outcome || '-' }}</td>
                    <td class="whitespace-nowrap px-3 py-2" data-test="cindy-probe-item-checked-at">{{ formatDate(item.finished_at || item.terra_at || item.luna_at || '') }}</td>
                    <td class="px-3 py-2 text-right tabular-nums">{{ item.request_count }}</td>
                  </tr>
                  <tr v-if="!loading.items && itemPage.items.length === 0">
                    <td colspan="6" class="px-3 py-6 text-center text-gray-400">{{ t('admin.accounts.cindyProbe.noItems') }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
            <div class="mt-2 flex items-center justify-between gap-3 text-xs text-gray-500 dark:text-dark-300">
              <span>{{ t('admin.accounts.cindyProbe.itemTotal', { count: itemPage.total }) }}</span>
              <div class="flex items-center gap-2">
                <button type="button" class="btn btn-secondary h-8 px-2" :disabled="itemPage.page <= 1 || loading.items" data-test="cindy-probe-items-prev" @click="itemPageNumber -= 1">
                  <Icon name="chevronLeft" size="xs" />
                </button>
                <span class="min-w-16 text-center tabular-nums">{{ itemPage.page }} / {{ itemTotalPages }}</span>
                <button type="button" class="btn btn-secondary h-8 px-2" :disabled="itemPage.page >= itemTotalPages || loading.items" data-test="cindy-probe-items-next" @click="itemPageNumber += 1">
                  <Icon name="chevronRight" size="xs" />
                </button>
              </div>
            </div>
          </div>
        </div>

        <div v-else class="mt-4 rounded-md border border-dashed border-gray-200 px-4 py-8 text-center text-sm text-gray-400 dark:border-dark-700">
          {{ loading.jobs ? t('common.loading') : t('admin.accounts.cindyProbe.noJobs') }}
        </div>
      </div>
    </div>

    <ConfirmDialog
      :show="showCancelConfirm"
      :title="t('admin.accounts.cindyProbe.cancelTitle')"
      :message="t('admin.accounts.cindyProbe.cancelMessage')"
      :confirm-text="t('admin.accounts.cindyProbe.cancelConfirm')"
      danger
      @confirm="confirmCancelJob"
      @cancel="showCancelConfirm = false"
    />
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import {
  cindyBalanceProbeAPI,
  canonicalizeCindyBalanceProbeScope,
  type CindyBalanceProbeFilters,
  type CindyBalanceProbeItemPage,
  type CindyBalanceProbeJob,
  type CindyBalanceProbePreview,
  type CindyBalanceProbePreviewRequest,
  type CindyBalanceProbeScopeMode,
} from '@/api/admin/cindyBalanceProbe'

const props = withDefaults(defineProps<{
  selectedIds: number[]
  filters: CindyBalanceProbeFilters
  initiallyExpanded?: boolean
}>(), {
  initiallyExpanded: false,
})

const { t } = useI18n()
const appStore = useAppStore()
const scopeMode = ref<CindyBalanceProbeScopeMode>('all')
const expanded = ref(props.initiallyExpanded)
const rateRPS = ref(0.5)
const preview = ref<CindyBalanceProbePreview | null>(null)
const jobs = ref<CindyBalanceProbeJob[]>([])
const selectedJobID = ref(0)
const jobRateRPS = ref(0.5)
const itemState = ref('')
const itemPageNumber = ref(1)
const showCancelConfirm = ref(false)
const itemPageSize = 20
const itemPage = reactive<CindyBalanceProbeItemPage>({ items: [], total: 0, page: 1, page_size: itemPageSize })
const loading = reactive({ preview: false, create: false, jobs: false, items: false, action: false })
let pollTimer: ReturnType<typeof setInterval> | null = null
let itemsRequestSequence = 0
let itemsLoadQueued = false

const activeSegmentClass = 'bg-white text-gray-950 shadow-sm dark:bg-dark-700 dark:text-white'
const inactiveSegmentClass = 'text-gray-500 hover:text-gray-900 dark:text-dark-300 dark:hover:text-white'
const scopeOptions = computed(() => [
  { value: 'all' as const, label: t('admin.accounts.cindyProbe.scopeAll') },
  { value: 'filter' as const, label: t('admin.accounts.cindyProbe.scopeFilter') },
  { value: 'selected' as const, label: t('admin.accounts.cindyProbe.scopeSelected') },
])
const normalizedRate = computed(() => Math.min(1, Math.max(0.1, Number(rateRPS.value) || 0.5)))
const activeJob = computed(() => jobs.value.find((job) => job.id === selectedJobID.value) || null)
const activeStatuses = new Set(['queued', 'running', 'paused', 'paused_upstream', 'cancel_requested'])
const canPause = computed(() => ['queued', 'running'].includes(activeJob.value?.status || ''))
const canResume = computed(() => ['paused', 'paused_upstream'].includes(activeJob.value?.status || ''))
const canCancel = computed(() => activeJob.value != null && activeStatuses.has(activeJob.value.status) && activeJob.value.status !== 'cancel_requested')
const canChangeRate = computed(() => activeJob.value != null && activeStatuses.has(activeJob.value.status))
const completedCount = computed(() => {
  const counts = activeJob.value?.counts
  if (!counts) return 0
  return counts.healthy + counts.recovered + counts.exhausted + counts.inconclusive + counts.skipped
})
const progressPercent = computed(() => {
  const total = activeJob.value?.candidate_count || 0
  return total > 0 ? Math.min(100, Math.round((completedCount.value / total) * 100)) : 0
})
const itemTotalPages = computed(() => Math.max(1, Math.ceil(itemPage.total / itemPage.page_size)))
const previewMetrics = computed(() => preview.value ? [
  { label: t('admin.accounts.cindyProbe.candidates'), value: preview.value.candidate_count },
  { label: t('admin.accounts.cindyProbe.marked'), value: preview.value.marked_count },
  { label: t('admin.accounts.cindyProbe.unmarked'), value: preview.value.unmarked_count },
] : [])
const jobCountMetrics = computed(() => {
  const counts = activeJob.value?.counts
  return [
    { key: 'pending', label: t('admin.accounts.cindyProbe.counts.pending'), value: counts?.pending ?? 0 },
    { key: 'running', label: t('admin.accounts.cindyProbe.counts.running'), value: counts?.running ?? 0 },
    { key: 'healthy', label: t('admin.accounts.cindyProbe.counts.healthy'), value: counts?.healthy ?? 0 },
    { key: 'recovered', label: t('admin.accounts.cindyProbe.counts.recovered'), value: counts?.recovered ?? 0 },
    { key: 'exhausted', label: t('admin.accounts.cindyProbe.counts.exhausted'), value: counts?.exhausted ?? 0 },
    { key: 'inconclusive', label: t('admin.accounts.cindyProbe.counts.inconclusive'), value: counts?.inconclusive ?? 0 },
    { key: 'skipped', label: t('admin.accounts.cindyProbe.counts.skipped'), value: counts?.skipped ?? 0 },
  ]
})
const itemStateOptions = [
  'pending', 'luna_running', 'luna_exact', 'terra_running', 'healthy', 'recovered',
  'still_exhausted', 'exhausted', 'already_marked', 'inconclusive', 'confirmation_expired',
  'skipped_stale', 'unknown_after_crash', 'canceled',
]

function compactFilters(filters: CindyBalanceProbeFilters): CindyBalanceProbeFilters {
  return Object.fromEntries(Object.entries(filters).filter(([, value]) => {
    if (value == null || value === '') return false
    if (Array.isArray(value)) return value.length > 0
    if (typeof value === 'boolean') return value
    return true
  })) as CindyBalanceProbeFilters
}

function buildPreviewRequest(): CindyBalanceProbePreviewRequest {
  const scope = scopeMode.value === 'selected'
    ? { mode: 'selected' as const, account_ids: [...new Set(props.selectedIds)] }
    : scopeMode.value === 'filter'
      ? { mode: 'filter' as const, filters: compactFilters(props.filters) }
      : { mode: 'all' as const }
  return { scope, rate_rps: normalizedRate.value }
}

async function previewJob(): Promise<void> {
  loading.preview = true
  preview.value = null
  try {
    const result = await cindyBalanceProbeAPI.preview(buildPreviewRequest())
    preview.value = result
    rateRPS.value = result.rate_rps
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.accounts.cindyProbe.previewFailed')))
  } finally {
    loading.preview = false
  }
}

async function createJob(): Promise<void> {
  if (!preview.value) return
  loading.create = true
  try {
    const job = await cindyBalanceProbeAPI.create({
      scope: canonicalizeCindyBalanceProbeScope(preview.value.scope),
      rate_rps: preview.value.rate_rps,
      expected_count: preview.value.candidate_count,
      candidate_fingerprint: preview.value.candidate_fingerprint,
    })
    mergeJob(job)
    selectedJobID.value = job.id
    preview.value = null
    appStore.showSuccess(t('admin.accounts.cindyProbe.created'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.accounts.cindyProbe.createFailed')))
  } finally {
    loading.create = false
  }
}

async function loadJobs(): Promise<void> {
  loading.jobs = true
  try {
    const result = await cindyBalanceProbeAPI.list(10)
    jobs.value = Array.isArray(result.items) ? result.items : []
    if (!jobs.value.some((job) => job.id === selectedJobID.value)) {
      selectedJobID.value = jobs.value.find((job) => activeStatuses.has(job.status))?.id || jobs.value[0]?.id || 0
    }
    if (jobs.value.some((job) => activeStatuses.has(job.status))) expanded.value = true
    if (selectedJobID.value) queueItemsLoad()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.accounts.cindyProbe.loadFailed')))
  } finally {
    loading.jobs = false
  }
}

async function refreshActiveJob(): Promise<void> {
  const jobID = selectedJobID.value
  if (!jobID || !activeJob.value || !activeStatuses.has(activeJob.value.status)) return
  try {
    const job = await cindyBalanceProbeAPI.get(jobID)
    if (selectedJobID.value !== jobID) return
    mergeJob(job)
    await loadItems()
  } catch {
    // The manual refresh path surfaces errors; polling remains quiet.
  }
}

async function loadItems(): Promise<void> {
  const requestSequence = ++itemsRequestSequence
  const jobID = selectedJobID.value
  if (!jobID) {
    Object.assign(itemPage, { items: [], total: 0, page: 1, page_size: itemPageSize })
    loading.items = false
    return
  }
  loading.items = true
  try {
    const result = await cindyBalanceProbeAPI.listItems(jobID, {
      state: itemState.value || undefined,
      page: itemPageNumber.value,
      page_size: itemPageSize,
    })
    if (requestSequence === itemsRequestSequence && selectedJobID.value === jobID) Object.assign(itemPage, result)
  } catch (error) {
    if (requestSequence === itemsRequestSequence) {
      appStore.showError(extractApiErrorMessage(error, t('admin.accounts.cindyProbe.itemsFailed')))
    }
  } finally {
    if (requestSequence === itemsRequestSequence) loading.items = false
  }
}

function queueItemsLoad(): void {
  if (itemsLoadQueued) return
  itemsLoadQueued = true
  void Promise.resolve().then(async () => {
    itemsLoadQueued = false
    await loadItems()
  })
}

function mergeJob(job: CindyBalanceProbeJob): void {
  const index = jobs.value.findIndex((candidate) => candidate.id === job.id)
  if (index >= 0) jobs.value.splice(index, 1, job)
  else jobs.value.unshift(job)
  jobs.value = [...jobs.value].sort((a, b) => b.id - a.id).slice(0, 10)
}

async function mutateJob(action: () => Promise<CindyBalanceProbeJob>, successKey: string): Promise<void> {
  loading.action = true
  try {
    mergeJob(await action())
    appStore.showSuccess(t(successKey))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.accounts.cindyProbe.actionFailed')))
  } finally {
    loading.action = false
  }
}

async function saveJobRate(): Promise<void> {
  const jobID = selectedJobID.value
  const rate = Math.min(1, Math.max(0.1, Number(jobRateRPS.value) || 0))
  if (!jobID || rate < 0.1) return
  await mutateJob(() => cindyBalanceProbeAPI.setRate(jobID, rate), 'admin.accounts.cindyProbe.rateSaved')
}

async function pauseJob(): Promise<void> {
  if (selectedJobID.value) await mutateJob(() => cindyBalanceProbeAPI.pause(selectedJobID.value), 'admin.accounts.cindyProbe.paused')
}

async function resumeJob(): Promise<void> {
  if (selectedJobID.value) await mutateJob(() => cindyBalanceProbeAPI.resume(selectedJobID.value), 'admin.accounts.cindyProbe.resumed')
}

async function cancelJob(): Promise<void> {
  if (selectedJobID.value) showCancelConfirm.value = true
}

async function confirmCancelJob(): Promise<void> {
  const jobID = selectedJobID.value
  showCancelConfirm.value = false
  if (jobID) await mutateJob(() => cindyBalanceProbeAPI.cancel(jobID), 'admin.accounts.cindyProbe.cancelRequested')
}

function jobStatusLabel(status: string): string {
  return t(`admin.accounts.cindyProbe.status.${status}`)
}

function itemStateLabel(state: string): string {
  return t(`admin.accounts.cindyProbe.itemState.${state}`)
}

function jobStatusClass(status: string): string {
  if (status === 'completed') return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300'
  if (['completed_with_issues', 'paused_upstream'].includes(status)) return 'bg-amber-50 text-amber-700 dark:bg-amber-900/20 dark:text-amber-300'
  if (['canceled', 'cancel_requested'].includes(status)) return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-dark-300'
  return 'bg-primary-50 text-primary-700 dark:bg-primary-900/20 dark:text-primary-300'
}

function formatDuration(seconds: number): string {
  const safeSeconds = Math.max(0, Math.round(Number(seconds) || 0))
  if (safeSeconds < 60) return t('admin.accounts.cindyProbe.seconds', { count: safeSeconds })
  const minutes = Math.ceil(safeSeconds / 60)
  if (minutes < 60) return t('admin.accounts.cindyProbe.minutes', { count: minutes })
  return t('admin.accounts.cindyProbe.hours', { count: Math.ceil(minutes / 60) })
}

function formatDurationRange(minimum: number, maximum: number): string {
  return minimum === maximum ? formatDuration(minimum) : `${formatDuration(minimum)} - ${formatDuration(maximum)}`
}

function formatDate(value: string): string {
  const timestamp = new Date(value).getTime()
  return Number.isFinite(timestamp) ? new Date(timestamp).toLocaleString() : '-'
}

watch([scopeMode, rateRPS, () => props.selectedIds, () => props.filters], () => {
  preview.value = null
}, { deep: true })

watch(activeJob, (job) => {
  jobRateRPS.value = job?.rate_rps || 0.5
})

watch(selectedJobID, () => {
  itemPageNumber.value = 1
  itemState.value = ''
  queueItemsLoad()
})

watch(itemState, () => {
  itemPageNumber.value = 1
  queueItemsLoad()
})

watch(itemPageNumber, () => {
  queueItemsLoad()
})

onMounted(() => {
  void loadJobs()
  pollTimer = setInterval(() => void refreshActiveJob(), 3000)
})

onBeforeUnmount(() => {
  if (pollTimer) clearInterval(pollTimer)
})
</script>
