<template>
  <Teleport to="body">
    <div v-if="store.drawerOpen" class="fixed inset-0 z-50 flex justify-end" @keydown.esc="store.closeDrawer">
      <button
        type="button"
        class="absolute inset-0 bg-black/30"
        :aria-label="t('common.close')"
        @click="store.closeDrawer"
      />
      <aside
        role="dialog"
        aria-modal="true"
        :aria-label="t('admin.accountTasks.drawerTitle')"
        class="relative flex h-full w-full max-w-md flex-col border-l border-gray-200 bg-white shadow-xl dark:border-dark-700 dark:bg-dark-900"
      >
        <header class="flex items-start justify-between gap-3 border-b border-gray-200 px-4 py-3 dark:border-dark-700">
          <div class="min-w-0">
            <h2 class="break-words text-base font-semibold text-gray-900 dark:text-white">
              {{ t('admin.accountTasks.drawerTitle') }}
            </h2>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-300">
              {{ t('admin.accountTasks.description') }}
            </p>
          </div>
          <button
            type="button"
            data-test="task-refresh"
            class="btn-ghost btn-icon shrink-0"
            :aria-label="t('common.refresh')"
            :disabled="store.loadingJobs || store.loadingCurrent"
            @click="store.refreshDrawer"
          >
            <Icon name="refresh" size="sm" />
          </button>
          <button type="button" class="btn-ghost btn-icon shrink-0" :aria-label="t('common.close')" @click="store.closeDrawer">
            <Icon name="x" size="sm" />
          </button>
        </header>

        <div class="min-h-0 flex-1 overflow-y-auto p-4">
          <div v-if="store.recentJobs.length" class="space-y-2">
            <button
              v-for="job in store.recentJobs.slice(0, 8)"
              :key="job.id"
              type="button"
              class="w-full rounded-md border px-3 py-2 text-left transition-colors"
              :class="store.currentJob?.id === job.id
                ? 'border-primary-400 bg-primary-50 dark:border-primary-600 dark:bg-primary-900/20'
                : 'border-gray-200 hover:bg-gray-50 dark:border-dark-700 dark:hover:bg-dark-800'"
              :aria-pressed="store.currentJob?.id === job.id"
              @click="store.openJob(job.id)"
            >
              <div class="flex items-center justify-between gap-2">
                <span class="truncate text-sm font-medium text-gray-900 dark:text-white">{{ kindLabel(job.kind) }}</span>
                <span class="shrink-0 text-xs text-gray-500 dark:text-dark-300">#{{ job.id }}</span>
              </div>
              <div class="mt-1 flex items-center justify-between gap-2 text-xs">
                <span :class="statusClass(job.status)">{{ statusLabel(job.status) }}</span>
                <span class="tabular-nums text-gray-500 dark:text-dark-300">{{ job.processed_count }}/{{ job.target_count }}</span>
              </div>
            </button>
          </div>
          <p v-else class="py-10 text-center text-sm text-gray-500 dark:text-dark-300">
            {{ t('admin.accountTasks.noTasks') }}
          </p>

          <section v-if="store.currentJob" class="mt-5 border-t border-gray-200 pt-4 dark:border-dark-700">
            <div class="flex items-center justify-between gap-3">
              <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
                {{ t('admin.accountTasks.details') }} #{{ store.currentJob.id }}
              </h3>
              <span class="text-xs" :class="statusClass(store.currentJob.status)">
                {{ statusLabel(store.currentJob.status) }}
              </span>
            </div>
            <div class="mt-3 h-2 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700">
              <div class="h-full bg-primary-500 transition-[width]" :style="{ width: `${progressPercent}%` }" />
            </div>
            <div
              class="sr-only"
              role="progressbar"
              :aria-label="t('admin.accountTasks.jobProgress', { id: store.currentJob.id })"
              aria-valuemin="0"
              aria-valuemax="100"
              :aria-valuenow="progressPercent"
            />
            <p class="mt-1 text-xs tabular-nums text-gray-500 dark:text-dark-300">
              {{ t('admin.accountTasks.progress', { processed: store.currentJob.processed_count, total: store.currentJob.target_count }) }}
            </p>
            <p class="mt-1 text-xs tabular-nums text-gray-500 dark:text-dark-300" data-test="task-result-summary">
              {{ t('admin.accountTasks.resultSummary', {
                succeeded: store.currentJob.succeeded_count,
                failed: store.currentJob.failed_count,
                canceled: store.currentJob.canceled_count
              }) }}
            </p>
            <p class="mt-1 text-xs tabular-nums text-gray-500 dark:text-dark-300" data-test="task-progress-metrics">
              {{ t('admin.accountTasks.progressMetrics', { elapsed: elapsedLabel, rate: rateLabel, eta: etaLabel }) }}
            </p>
            <p
              v-if="store.currentJob.error_message"
              class="mt-2 break-words text-xs text-red-600 dark:text-red-300"
            >
              {{ store.currentJob.error_message }}
            </p>

            <div class="mt-3 flex flex-wrap gap-2">
              <button
                v-if="canCancel"
                type="button"
                data-test="task-cancel"
                class="btn btn-secondary btn-sm"
                @click="cancelCurrent"
              >
                {{ t('admin.accountTasks.cancel') }}
              </button>
              <button
                v-if="canRetry"
                type="button"
                data-test="task-retry"
                class="btn btn-primary btn-sm"
                @click="retryCurrent"
              >
                {{ t('admin.accountTasks.retryFailed') }}
              </button>
            </div>

            <div v-if="duplicateReview" class="mt-5 rounded-md border border-gray-200 p-3 dark:border-dark-700">
              <h4 class="text-sm font-semibold text-gray-900 dark:text-white">
                {{ t('admin.accountTasks.duplicate.title') }}
              </h4>
              <p class="mt-1 text-xs text-gray-500 dark:text-dark-300">
                {{ t('admin.accountTasks.duplicate.selectSurvivor') }}
              </p>
              <div class="mt-3 space-y-2">
                <label
                  v-for="account in duplicateReview.accounts"
                  :key="account.account_id"
                  class="flex cursor-pointer items-start gap-3 rounded-md border border-gray-200 p-3 dark:border-dark-700"
                >
                  <input
                    v-model="survivorID"
                    type="radio"
                    name="duplicate-survivor"
                    :value="account.account_id"
                    :data-test="`duplicate-survivor-${account.account_id}`"
                    class="mt-0.5 h-4 w-4 text-primary-600"
                  />
                  <span class="min-w-0 flex-1">
                    <span class="block truncate text-sm font-medium text-gray-900 dark:text-white">{{ account.name }}</span>
                    <span class="mt-1 block text-xs text-gray-500 dark:text-dark-300">
                      {{ t('admin.accountTasks.duplicate.summary', {
                        id: account.account_id,
                        groups: account.group_count,
                        tags: account.tag_count,
                        score: account.configuration_score
                      }) }}
                    </span>
                  </span>
                </label>
              </div>
              <button
                type="button"
                data-test="duplicate-merge-submit"
                class="btn btn-danger mt-3 w-full"
                :disabled="!survivorID || merging"
                @click="submitDuplicateMerge"
              >
                {{ merging ? t('common.saving') : t('admin.accountTasks.duplicate.merge') }}
              </button>
            </div>

            <div v-if="store.items.length" class="mt-5">
              <h4 class="mb-2 text-sm font-semibold text-gray-900 dark:text-white">
                {{ t('admin.accountTasks.results') }}
              </h4>
              <div class="divide-y divide-gray-100 rounded-md border border-gray-200 dark:divide-dark-700 dark:border-dark-700">
                <div v-for="item in store.items" :key="item.id" class="px-3 py-2 text-xs">
                  <div class="flex items-center justify-between gap-2">
                    <span class="text-gray-700 dark:text-dark-200">{{ itemLabel(item) }}</span>
                    <span :class="statusClass(item.status)">{{ statusLabel(item.status) }}</span>
                  </div>
                  <p v-if="item.error_message" class="mt-1 break-words text-red-600 dark:text-red-300">{{ item.error_message }}</p>
                </div>
              </div>
              <div v-if="store.itemPage.total > store.itemPage.pageSize" class="mt-2 flex justify-end gap-2">
                <button type="button" class="btn btn-secondary btn-sm" :disabled="store.itemPage.page <= 1" @click="changeItemPage(-1)">
                  {{ t('admin.accountTasks.previousPage') }}
                </button>
                <button type="button" class="btn btn-secondary btn-sm" :disabled="store.itemPage.page * store.itemPage.pageSize >= store.itemPage.total" @click="changeItemPage(1)">
                  {{ t('admin.accountTasks.nextPage') }}
                </button>
              </div>
            </div>
          </section>
        </div>

        <footer class="border-t border-gray-200 p-3 dark:border-dark-700">
          <RouterLink to="/admin/tasks" class="btn btn-secondary w-full" @click="store.closeDrawer">
            {{ t('admin.accountTasks.viewAll') }}
          </RouterLink>
        </footer>
      </aside>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { isTerminalAccountJob, useAccountJobsStore } from '@/stores/accountJobs'
import { useAppStore } from '@/stores/app'
import type {
  AccountJobItem,
  AccountJobStatus,
  DuplicateReviewAccount,
  DuplicateReviewMetadata,
} from '@/api/admin/accountJobs'

const { t } = useI18n()
const store = useAccountJobsStore()
const appStore = useAppStore()
const survivorID = ref<number | null>(null)
const merging = ref(false)

function safeDuplicateReview(item: AccountJobItem): DuplicateReviewMetadata | null {
  if (item.status !== 'succeeded' || !item.metadata || typeof item.metadata !== 'object') return null
  const hash = item.metadata.confirmation_hash
  const rawAccounts = item.metadata.accounts
  if (typeof hash !== 'string' || hash.length === 0 || !Array.isArray(rawAccounts)) return null
  const accounts: DuplicateReviewAccount[] = []
  for (const candidate of rawAccounts) {
    if (!candidate || typeof candidate !== 'object') return null
    const value = candidate as Record<string, unknown>
    if (
      !Number.isInteger(value.account_id)
      || Number(value.account_id) <= 0
      || typeof value.name !== 'string'
      || !Number.isFinite(value.group_count)
      || !Number.isFinite(value.tag_count)
      || !Number.isFinite(value.configuration_score)
    ) return null
    accounts.push({
      account_id: Number(value.account_id),
      name: value.name,
      group_count: Number(value.group_count),
      tag_count: Number(value.tag_count),
      configuration_score: Number(value.configuration_score),
    })
  }
  if (accounts.length < 2 || accounts.length > 100) return null
  return { confirmation_hash: hash, accounts }
}

const duplicateReview = computed(() => {
  if (store.currentJob?.kind !== 'account_duplicate_review' || store.currentJob.status !== 'succeeded') return null
  for (const item of store.items) {
    const result = safeDuplicateReview(item)
    if (result) return result
  }
  return null
})

watch(() => store.currentJob?.id, () => {
  survivorID.value = null
})

const progressPercent = computed(() => {
  const job = store.currentJob
  if (!job || job.target_count <= 0) return 0
  return Math.min(100, Math.round((job.processed_count / job.target_count) * 100))
})
const elapsedSeconds = computed(() => {
  const job = store.currentJob
  if (!job) return 0
  const started = new Date(job.started_at || job.created_at).getTime()
  // Metrics describe the last explicitly refreshed snapshot, not a stale live estimate.
  const finished = new Date(job.finished_at || job.updated_at).getTime()
  if (!Number.isFinite(started) || !Number.isFinite(finished) || finished <= started) return 0
  return Math.max(0, Math.floor((finished - started) / 1_000))
})
const formatDuration = (seconds: number): string => {
  if (!Number.isFinite(seconds) || seconds <= 0) return '0s'
  const whole = Math.max(0, Math.round(seconds))
  const hours = Math.floor(whole / 3_600)
  const minutes = Math.floor((whole % 3_600) / 60)
  const remaining = whole % 60
  if (hours > 0) return `${hours}h ${minutes}m`
  if (minutes > 0) return `${minutes}m ${remaining}s`
  return `${remaining}s`
}
const processingRate = computed(() => {
  const job = store.currentJob
  if (!job || elapsedSeconds.value <= 0 || job.processed_count <= 0) return 0
  return job.processed_count / elapsedSeconds.value
})
const elapsedLabel = computed(() => formatDuration(elapsedSeconds.value))
const rateLabel = computed(() => processingRate.value > 0 ? processingRate.value.toFixed(1) : '0.0')
const etaLabel = computed(() => {
  const job = store.currentJob
  if (!job || isTerminalAccountJob(job)) return t('admin.accountTasks.etaDone')
  const remaining = Math.max(0, job.target_count - job.processed_count)
  if (remaining === 0) return '0s'
  if (processingRate.value <= 0) return '--'
  return formatDuration(remaining / processingRate.value)
})
const canCancel = computed(() => {
  const job = store.currentJob
  return Boolean(job && (job.status === 'pending' || job.status === 'running') && !job.cancel_requested_at)
})
const canRetry = computed(() => {
  const job = store.currentJob
  return Boolean(job && job.failed_count > 0 && (job.status === 'failed' || job.status === 'partially_succeeded'))
})

function kindLabel(kind: string): string {
  const key = `admin.accountTasks.kinds.${kind}`
  return String(t(key))
}

function statusLabel(status: AccountJobStatus | AccountJobItem['status']): string {
  return String(t(`admin.accountTasks.statuses.${status}`))
}

function statusClass(status: AccountJobStatus | AccountJobItem['status']): string {
  if (status === 'succeeded') return 'text-emerald-600 dark:text-emerald-300'
  if (status === 'failed') return 'text-red-600 dark:text-red-300'
  if (status === 'partially_succeeded' || status === 'canceled') return 'text-amber-600 dark:text-amber-300'
  return 'text-primary-600 dark:text-primary-300'
}

function itemLabel(item: AccountJobItem): string {
  if (item.target_account_id) return String(t('admin.accountTasks.account', { id: item.target_account_id }))
  return String(t('admin.accountTasks.item', { ordinal: item.ordinal }))
}

async function cancelCurrent(): Promise<void> {
  if (!store.currentJob) return
  try {
    await store.cancelJob(store.currentJob.id)
  } catch {
    appStore.showError(t('admin.accountTasks.actionFailed'))
  }
}

async function retryCurrent(): Promise<void> {
  if (!store.currentJob) return
  try {
    await store.retryJob(store.currentJob.id)
  } catch {
    appStore.showError(t('admin.accountTasks.actionFailed'))
  }
}

async function submitDuplicateMerge(): Promise<void> {
  const review = duplicateReview.value
  if (!review || !survivorID.value || merging.value) return
  merging.value = true
  try {
    await store.mergeDuplicates({
      survivor_account_id: survivorID.value,
      loser_account_ids: review.accounts
        .map((account) => account.account_id)
        .filter((accountID) => accountID !== survivorID.value),
      confirmation_hash: review.confirmation_hash,
    })
  } catch {
    appStore.showError(t('admin.accountTasks.duplicate.mergeFailed'))
  } finally {
    merging.value = false
  }
}

async function changeItemPage(offset: number): Promise<void> {
  if (!store.currentJob) return
  try {
    await store.loadCurrent(store.currentJob.id, {
      page: store.itemPage.page + offset,
      page_size: store.itemPage.pageSize,
    })
  } catch {
    appStore.showError(t('admin.accountTasks.loadFailed'))
  }
}
</script>
