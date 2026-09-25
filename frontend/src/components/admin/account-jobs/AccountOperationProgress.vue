<template>
  <div v-if="job" class="space-y-5" data-test="operation-progress">
    <div class="flex items-start justify-between gap-4">
      <div class="flex min-w-0 items-center gap-3">
        <span class="flex h-10 w-10 shrink-0 items-center justify-center border border-line bg-raised" :class="statusClass(job.status)">
          <Icon :name="terminal ? (job.failed_count ? 'exclamationTriangle' : 'checkCircle') : 'refresh'" size="md" :class="!terminal && 'animate-spin'" />
        </span>
        <div>
          <p class="text-base font-semibold text-ink" aria-live="polite">{{ job.cancel_requested_at && !terminal ? t('admin.accountTasks.stopping') : statusLabel(job.status) }}</p>
          <p class="mt-1 text-xs text-muted">{{ t('admin.accountTasks.progress', { processed: job.processed_count, total: job.target_count }) }}</p>
        </div>
      </div>
      <span class="shrink-0 text-2xl font-semibold tabular-nums text-ink">{{ progress }}<span class="ml-0.5 text-xs font-normal text-muted">%</span></span>
    </div>
    <div class="h-1 overflow-hidden bg-surface" role="progressbar" :aria-label="t('admin.accountTasks.progressLabel')" :aria-valuenow="progress" aria-valuemin="0" aria-valuemax="100">
      <div class="h-full bg-primary-500 transition-[width] duration-300" :style="{ width: `${progress}%` }" />
    </div>
    <div class="grid grid-cols-3 divide-x divide-line border-y border-line py-3 text-center text-xs">
      <div><span class="block text-lg font-semibold tabular-nums text-primary-600 dark:text-primary-400">{{ job.succeeded_count }}</span><span class="text-muted">{{ t('admin.accountTasks.statuses.succeeded') }}</span></div>
      <div><span class="block text-lg font-semibold tabular-nums" :class="job.failed_count ? 'text-red-600 dark:text-red-400' : 'text-ink'">{{ job.failed_count }}</span><span class="text-muted">{{ t('admin.accountTasks.statuses.failed') }}</span></div>
      <div><span class="block text-lg font-semibold tabular-nums text-ink">{{ job.canceled_count }}</span><span class="text-muted">{{ t('admin.accountTasks.statuses.canceled') }}</span></div>
    </div>
    <p v-if="store.connectionLost" role="status" class="border-l-2 border-amber-500 bg-raised px-3 py-2 text-sm text-amber-700 dark:text-amber-300">{{ t('admin.accountTasks.reconnecting') }}</p>
    <p v-if="error || job.error_message" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error || job.error_message }}</p>

    <section v-if="duplicateReview" class="space-y-3">
      <p class="text-sm text-muted">{{ t('admin.accountTasks.duplicate.selectSurvivor') }}</p>
      <label v-for="account in duplicateReview.accounts" :key="account.account_id" class="flex cursor-pointer items-center gap-3 border border-line p-3 transition-colors hover:bg-raised" :class="survivorID === account.account_id && 'border-primary-500 bg-raised'">
        <input v-model="survivorID" type="radio" name="duplicate-survivor" :value="account.account_id" :data-test="`duplicate-survivor-${account.account_id}`" />
        <span class="min-w-0"><span class="block truncate text-sm font-medium text-ink">{{ account.name }}</span><span class="mt-1 block text-xs text-muted">{{ t('admin.accountTasks.duplicate.summary', { id: account.account_id, groups: account.group_count, tags: account.tag_count, score: account.configuration_score }) }}</span></span>
      </label>
      <p v-if="confirmMerge" class="text-sm text-red-600">{{ t('admin.accountTasks.duplicate.confirmMerge') }}</p>
      <button type="button" data-test="duplicate-merge-submit" class="btn btn-danger" :disabled="!survivorID || busy" @click="merge">{{ t(confirmMerge ? 'admin.accountTasks.duplicate.merge' : 'admin.accountTasks.duplicate.reviewMerge') }}</button>
    </section>

    <section v-else class="space-y-3">
      <div class="flex items-center justify-between gap-3 border-b border-line">
        <div class="flex gap-5 text-sm" role="group" :aria-label="t('admin.accountTasks.results')">
          <button v-for="filter in ['', 'failed']" :key="filter" type="button" class="border-b-2 px-0.5 pb-2 transition-colors" :class="store.itemFilter === filter ? 'border-primary-500 font-medium text-ink' : 'border-transparent text-muted hover:text-ink'" :aria-pressed="store.itemFilter === filter" @click="setFilter(filter)">{{ t(filter ? 'admin.accountTasks.failedOnly' : 'admin.accountTasks.allResults') }}</button>
        </div>
        <button class="btn-ghost btn-icon" :disabled="store.loadingCurrent" :aria-label="t('common.refresh')" @click="refresh"><Icon name="refresh" size="sm" /></button>
      </div>
      <div class="max-h-[38vh] divide-y divide-line overflow-y-auto" :aria-busy="store.loadingCurrent">
        <div v-for="item in store.items" :key="item.id" class="py-3 first:pt-0">
          <div class="flex items-start justify-between gap-3">
            <div class="min-w-0">
              <p class="truncate text-sm font-medium text-ink">{{ itemLabel(item) }}</p>
              <p v-if="item.metadata.model_id" class="mt-1 break-all text-xs text-muted">{{ item.metadata.model_id }}</p>
              <p v-else-if="typeof item.metadata.label === 'string'" class="mt-1 break-all text-xs text-muted">{{ item.metadata.label }}</p>
            </div>
            <span class="shrink-0 text-xs" :class="statusClass(item.status)">{{ statusLabel(item.status) }}</span>
          </div>
          <p v-if="typeof item.metadata.message === 'string'" class="mt-2 break-words text-xs" :class="item.status === 'failed' ? 'text-red-600 dark:text-red-400' : 'text-muted'">{{ item.metadata.message }}</p>
          <p v-else-if="item.error_message" class="mt-2 break-words text-xs text-red-600 dark:text-red-400">{{ item.error_message }}</p>
          <p v-if="job.kind === 'account_batch_test' && item.error_code" class="mt-1 text-xs text-muted">{{ item.error_code }}</p>
          <p v-if="item.metadata.output_limited" class="mt-1 text-xs text-muted">{{ t('admin.accounts.batchTest.outputLimited') }}</p>
          <p v-if="typeof item.metadata.recovery_status === 'string'" class="mt-1 text-xs" :class="item.metadata.recovery_status === 'warning' ? 'text-amber-600' : 'text-muted'">{{ t(`admin.accounts.batchTest.recovery.${item.metadata.recovery_status}`) }}</p>
          <dl v-if="resultFacts(item).length" class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted">
            <div v-for="(fact, index) in resultFacts(item)" :key="index" class="flex gap-1">
              <dt>{{ fact.label[locale || 'zh'] || fact.label.zh || fact.label.en }}</dt>
              <dd>{{ fact.timestamp ? formatDateTime(fact.value) : fact.value }}</dd>
            </div>
          </dl>
          <p v-if="typeof item.metadata.latency_ms === 'number'" class="mt-1 text-xs tabular-nums text-muted">{{ item.metadata.latency_ms }} ms</p>
        </div>
        <p v-if="!store.items.length" class="py-5 text-center text-sm text-muted">{{ t(store.loadingCurrent ? 'common.loading' : store.itemFilter ? 'admin.accountTasks.noFailures' : 'admin.accountTasks.awaitingResults') }}</p>
      </div>
      <div v-if="store.itemPage.total > store.itemPage.pageSize" class="flex items-center justify-between text-xs text-muted">
        <span>{{ store.itemPage.page }} / {{ Math.ceil(store.itemPage.total / store.itemPage.pageSize) }}</span>
        <div class="flex gap-2"><button class="btn btn-secondary btn-sm" :disabled="store.loadingCurrent || store.itemPage.page <= 1" @click="changePage(-1)">{{ t('admin.accountTasks.previousPage') }}</button><button class="btn btn-secondary btn-sm" :disabled="store.loadingCurrent || store.itemPage.page * store.itemPage.pageSize >= store.itemPage.total" @click="changePage(1)">{{ t('admin.accountTasks.nextPage') }}</button></div>
      </div>
    </section>
    <div class="flex flex-wrap items-center justify-between gap-3 border-t border-line pt-4">
      <span class="text-xs text-muted">{{ t(terminal ? 'admin.accountTasks.savedInHistory' : 'admin.accountTasks.continuesInBackground') }}</span>
      <div class="flex gap-2">
        <button v-if="!terminal" class="btn btn-secondary btn-sm" :disabled="busy || !!job.cancel_requested_at" @click="stop">{{ t(job.cancel_requested_at ? 'admin.accountTasks.stopping' : 'admin.accountTasks.cancel') }}</button>
        <button v-if="job.retry_eligible && !retryExpired" class="btn btn-secondary btn-sm" :disabled="busy" @click="retry">{{ t('admin.accountTasks.retryFailed') }}</button>
        <span v-if="job.retry_unavailable_reason === 'payload_expired'" class="text-xs text-muted">{{ t('admin.accountTasks.retryExpired') }}</span>
        <button class="btn btn-primary btn-sm" @click="emit('close')">{{ t(terminal ? 'common.close' : 'admin.accountTasks.minimize') }}</button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { isTerminalAccountJob, useAccountJobsStore } from '@/stores/accountJobs'
import { list as listAccounts } from '@/api/admin/accounts'
import type { AccountJobItem, DuplicateReviewMetadata } from '@/api/admin/accountJobs'
import { formatDateTime } from '@/utils/format'
const emit = defineEmits<{ close: [] }>()
const { t, locale } = useI18n()
const store = useAccountJobsStore()
const job = computed(() => store.currentJob)
const terminal = computed(() => !!job.value && isTerminalAccountJob(job.value))
const progress = computed(() => job.value?.target_count ? Math.min(100, Math.round(job.value.processed_count / job.value.target_count * 100)) : 0)
const busy = ref(false), error = ref(''), retryExpired = ref(false), survivorID = ref<number | null>(null), confirmMerge = ref(false)
const names = ref<Record<number, string>>({})
type ResultFact = { label: Record<string, string>; value: string; timestamp?: boolean }
function resultFacts(item: AccountJobItem): ResultFact[] {
  return Array.isArray(item.metadata.facts) ? item.metadata.facts.filter((fact): fact is ResultFact => !!fact && typeof fact === 'object' && typeof fact.value === 'string' && !!fact.label && typeof fact.label === 'object') : []
}
let nameVersion = 0
watch(() => job.value?.id, () => { error.value = ''; retryExpired.value = false; survivorID.value = null; confirmMerge.value = false })
watch(survivorID, () => { confirmMerge.value = false })
watch(() => store.items.map(item => item.target_account_id).filter(Boolean).join(','), async ids => {
  const version = ++nameVersion
  if (!ids) return
  try {
    const page = await listAccounts(1, 100, { account_ids: ids, lite: '1', include_scheduler_score: '0' })
    if (version === nameVersion) for (const account of page.items) names.value[account.id] = account.name
  } catch { /* Deleted accounts still have their durable account ID in results. */ }
}, { immediate: true })
const duplicateReview = computed<DuplicateReviewMetadata | null>(() => {
  if (job.value?.kind !== 'account_duplicate_review' || job.value.status !== 'succeeded') return null
  for (const item of store.items) {
    const metadata = item.metadata
    if (item.status !== 'succeeded' || typeof metadata.confirmation_hash !== 'string' || !metadata.confirmation_hash || !Array.isArray(metadata.accounts)) continue
    const accounts = metadata.accounts
    if (accounts.length < 2 || accounts.length > 100 || !accounts.every(a => a && Number.isSafeInteger(a.account_id) && a.account_id > 0 && typeof a.name === 'string' && Number.isFinite(a.group_count) && Number.isFinite(a.tag_count) && Number.isFinite(a.configuration_score))) continue
    return { confirmation_hash: metadata.confirmation_hash, accounts }
  }
  return null
})
function statusLabel(status: string) { return t(`admin.accountTasks.statuses.${status}`) }
function statusClass(status: string) {
  return status === 'failed' ? 'text-red-600 dark:text-red-400' : ['partially_succeeded', 'canceled'].includes(status) ? 'text-amber-600 dark:text-amber-400' : 'text-primary-600 dark:text-primary-400'
}
function itemLabel(item: AccountJobItem) { return (typeof item.metadata.name === 'string' && item.metadata.name) || (item.target_account_id && names.value[item.target_account_id]) || (item.target_account_id ? t('admin.accountTasks.account', { id: item.target_account_id }) : t('admin.accountTasks.item', { ordinal: item.ordinal })) }
async function action(callback: () => Promise<unknown>) {
  if (busy.value) return
  busy.value = true; error.value = ''
  try { await callback() }
  catch (e) {
    const code = String((e as { code?: string })?.code || '').toLowerCase()
    retryExpired.value = code.includes('payload_expired')
    error.value = t(retryExpired.value ? 'admin.accountTasks.retryExpired' : 'admin.accountTasks.actionFailed')
  } finally { busy.value = false }
}
async function stop() { if (job.value) await action(() => store.cancelJob(job.value!.id)) }
async function retry() { if (job.value) await action(() => store.retryJob(job.value!.id)) }
async function merge() {
  if (!duplicateReview.value || !survivorID.value) return
  if (!confirmMerge.value) { confirmMerge.value = true; return }
  const review = duplicateReview.value
  await action(() => store.mergeDuplicates({ survivor_account_id: survivorID.value!, loser_account_ids: review.accounts.map(a => a.account_id).filter(id => id !== survivorID.value), confirmation_hash: review.confirmation_hash }))
}
async function setFilter(status: string) { if (job.value) await action(() => store.loadCurrent(job.value!.id, { page: 1, status })) }
async function changePage(offset: number) { if (job.value) await action(() => store.loadCurrent(job.value!.id, { page: store.itemPage.page + offset })) }
async function refresh() { if (job.value) await action(() => store.loadCurrent(job.value!.id)) }
</script>
