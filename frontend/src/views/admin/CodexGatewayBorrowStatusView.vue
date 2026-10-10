<template>
  <component :is="embedded ? 'div' : AppLayout">
    <div class="mx-auto max-w-[1664px] space-y-6 px-1" data-ui="codex-gateway-borrow-status">
      <header v-if="!embedded" class="flex flex-wrap items-start justify-between gap-3 border-b border-line pb-4">
        <div class="min-w-0 space-y-1">
          <h1 class="text-xl font-semibold">{{ t('admin.codexGatewayBorrow.statusTitle') }}</h1>
          <p class="max-w-4xl text-sm text-muted">{{ t('admin.codexGatewayBorrow.statusDescription') }}</p>
        </div>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="loading || refreshing" data-test="borrow-refresh" @click="refreshStatus">
          <Icon name="refresh" size="sm" aria-hidden="true" />{{ t('admin.codexGatewayBorrow.refreshStatus') }}
        </button>
      </header>
      <CodexBorrowNav v-if="!embedded" />
      <pre v-if="statusError" role="alert" class="borrow-error" data-test="borrow-status-error">{{ statusError }}</pre>
      <pre v-if="inventoryError" role="alert" class="borrow-error">{{ inventoryError }}</pre>
      <p v-if="loading" class="py-8 text-center text-sm text-muted">{{ t('common.loading') }}</p>

      <template v-if="status">
        <section class="card space-y-4 p-4 sm:p-5" aria-labelledby="borrow-overview-title">
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div class="space-y-1">
              <h2 id="borrow-overview-title" class="font-semibold">{{ t('admin.codexGatewayBorrow.borrowStatus') }}</h2>
              <p class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.observedAt') }}: {{ formatTime(status.generated_at) }}</p>
            </div>
            <button type="button" class="btn btn-primary btn-sm" :disabled="!canAct" data-test="borrow-prepare" @click="prepare">
              {{ actionBusy === 'prepare' ? t('admin.codexGatewayBorrow.preparationPending') : t('admin.codexGatewayBorrow.prepareRoutes') }}
            </button>
          </div>
          <dl class="grid gap-4 rounded-lg border border-line bg-gray-50 p-4 text-sm dark:bg-dark-900 sm:grid-cols-2 xl:grid-cols-4">
            <div>
              <dt class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.enabled') }}</dt>
              <dd class="mt-1 font-medium" data-test="borrow-enabled-status">{{ status.enabled ? t('admin.codexGatewayBorrow.on') : t('admin.codexGatewayBorrow.off') }}</dd>
            </div>
            <div>
              <dt class="text-xs text-muted">{{ text('线路就绪情况', 'Route readiness') }}</dt>
              <dd class="mt-1 font-medium" data-test="borrow-preparation-status">{{ preparationLabel }}</dd>
            </div>
            <div>
              <dt class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.readyPairs') }}</dt>
              <dd class="mt-1 font-medium" data-test="borrow-ready-count">{{ t('admin.codexGatewayBorrow.pairCount', { ready: readyCount, total: targets.length }) }}</dd>
            </div>
            <div data-test="borrow-candidate">
              <dt class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.currentSource') }}</dt>
              <dd v-if="status.candidate" class="mt-1 space-y-1">
                <p class="break-words font-medium">{{ accountName(status.candidate.source_account_id) }} #{{ status.candidate.source_account_id }}</p>
                <p class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.proxy') }}: {{ proxyLabel(accountById(status.candidate.source_account_id)) }}</p>
                <p v-if="candidateRemaining > 0" class="text-xs" data-test="borrow-candidate-remaining">{{ t('admin.codexGatewayBorrow.remainingSeconds', { seconds: candidateRemaining }) }}</p>
                <p v-else class="text-xs text-amber-800 dark:text-amber-200">{{ t('admin.codexGatewayBorrow.candidateExpired') }}</p>
              </dd>
              <dd v-else class="mt-1 text-muted">{{ t('admin.codexGatewayBorrow.noCandidate') }}</dd>
            </div>
          </dl>
          <div v-if="status.setup?.total" class="rounded-lg border border-line p-3 text-sm" role="status" data-test="borrow-setup-progress">
            <p>{{ text('准备进度', 'Preparation progress') }}: {{ status.setup.completed }} / {{ status.setup.total }} · {{ text('失败', 'Failed') }} {{ status.setup.failed }}</p>
            <p v-if="status.preparing">{{ status.setup.phase === 'source' ? text('正在获取来源路由', 'Acquiring a source route') : `${accountName(status.setup.account_id)} · ${status.setup.model}` }}</p>
            <pre v-if="status.setup.error" class="borrow-error">{{ status.setup.error }}</pre>
          </div>
          <div class="space-y-2 text-sm" data-test="borrow-acquisition">
            <p>{{ t('admin.codexGatewayBorrow.onDemandHint') }}</p>
            <p v-if="status.acquisition?.phase" role="status">{{ acquisitionLabel }} · {{ formatTime(status.acquisition.started_at) }}</p>
            <details v-if="status.acquisition?.phase" class="rounded-lg border border-line p-3">
              <summary class="cursor-pointer">{{ t('admin.codexGatewayBorrow.technicalDetails') }}</summary>
              <dl class="mt-2 grid gap-2 text-xs sm:grid-cols-2">
                <div>{{ text('触发原因', 'Trigger') }}: {{ status.acquisition.trigger }}</div>
                <div>{{ text('完成时间', 'Finished') }}: {{ formatTime(status.acquisition.finished_at) }}</div>
                <div>{{ t('admin.codexGatewayBorrow.retryAfter') }}: {{ formatTime(status.acquisition.retry_after) }}</div>
              </dl>
              <pre v-if="status.acquisition.error" class="borrow-error mt-2">{{ status.acquisition.error }}</pre>
            </details>
          </div>
          <p class="text-sm">{{ t('admin.codexGatewayBorrow.validationMeaning') }}</p>
          <p class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.modelScope') }}</p>

          <div v-if="actionFailure" role="alert" class="space-y-2" data-test="borrow-action-failure">
            <p class="text-sm text-red-700 dark:text-red-300">{{ actionFailure.message }}</p>
            <details class="rounded-lg border border-line p-3">
              <summary class="cursor-pointer text-sm font-medium">{{ t('admin.codexGatewayBorrow.technicalDetails') }}</summary>
              <pre class="borrow-error mt-3" data-test="borrow-action-error">{{ actionFailure.detail }}</pre>
            </details>
          </div>
          <details v-if="status.candidate" class="rounded-lg border border-line p-3" data-test="borrow-candidate-details">
            <summary class="cursor-pointer text-sm font-medium">{{ t('admin.codexGatewayBorrow.technicalDetails') }} · {{ t('admin.codexGatewayBorrow.currentSource') }}</summary>
            <dl class="mt-3 grid gap-3 text-xs sm:grid-cols-2">
              <div><dt class="text-muted">{{ t('admin.codexGatewayBorrow.candidateFingerprint') }}</dt><dd class="mt-1 break-all font-mono">{{ status.candidate.cookie_fingerprint || '—' }}</dd></div>
              <div><dt class="text-muted">{{ t('admin.codexGatewayBorrow.expiresAt') }}</dt><dd class="mt-1">{{ formatTime(status.candidate.expires_at) }}</dd></div>
            </dl>
          </details>
          <details v-if="status.sources.length" class="rounded-lg border border-line p-3" data-test="borrow-source-details">
            <summary class="cursor-pointer text-sm font-medium">{{ t('admin.codexGatewayBorrow.sourceStates') }} ({{ status.sources.length }})</summary>
          <p class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.prepareRoutesHint') }}</p>
          <p class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.pollingHint') }}</p>
            <div v-for="source in status.sources" :key="source.account_id" class="min-w-0 space-y-2 border-t border-line py-3 first:mt-3 last:pb-0">
              <p class="break-words text-sm font-medium">{{ accountName(source.account_id) }} #{{ source.account_id }}</p>
              <p class="break-words text-xs">{{ borrowReasonLabel(source.expires_at && remainingSeconds(source.expires_at) === 0 && source.state === 'ready' ? 'route_expired' : source.reason, t) }}</p>
              <dl class="grid gap-2 text-xs text-muted sm:grid-cols-2">
                <div><dt>{{ t('admin.codexGatewayBorrow.rawState') }}</dt><dd class="mt-1 break-words font-mono">{{ source.state }}</dd></div>
                <div><dt>{{ t('admin.codexGatewayBorrow.rawReason') }}</dt><dd class="mt-1 break-words font-mono">{{ source.reason || '—' }}</dd></div>
                <div><dt>{{ t('admin.codexGatewayBorrow.checkedAt') }}</dt><dd class="mt-1">{{ formatTime(source.checked_at) }}</dd></div>
                <div><dt>{{ t('admin.codexGatewayBorrow.expiresAt') }}</dt><dd class="mt-1">{{ formatTime(source.expires_at) }}</dd></div>
                <div><dt>{{ t('admin.codexGatewayBorrow.proxy') }}</dt><dd class="mt-1 break-words">{{ proxyLabel(accountById(source.account_id)) }}</dd></div>
              </dl>
              <pre v-if="source.error" class="borrow-error" data-test="borrow-source-error">{{ source.error }}</pre>
            </div>
          </details>
        </section>

        <section class="card space-y-4 p-4 sm:p-5" aria-labelledby="borrow-targets-title">
          <h2 id="borrow-targets-title" class="font-semibold">{{ t('admin.codexGatewayBorrow.targetsStatus') }}</h2>
          <p class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.revalidateHint') }}</p>
          <ul v-if="targets.length" class="space-y-3">
            <li v-for="target in targets" :key="pairKey(target.account_id, target.model)" class="grid min-w-0 gap-4 rounded-lg border border-line p-4 lg:grid-cols-[minmax(160px,1fr)_minmax(180px,1.3fr)_minmax(180px,1fr)_minmax(160px,auto)]" :data-test="`borrow-status-${target.account_id}-${target.model}`">
              <div class="min-w-0 space-y-1">
                <p class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.accountModel') }}</p>
                <p class="break-words text-sm font-medium">{{ accountName(target.account_id) }} #{{ target.account_id }}</p>
                <p class="break-words text-xs">{{ target.model }}</p>
                <p class="break-words text-xs text-muted">{{ t('admin.codexGatewayBorrow.proxy') }}: {{ proxyLabel(accountById(target.account_id)) }}</p>
              </div>
              <div class="min-w-0 space-y-2">
                <p class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.validation') }}</p>
                <span class="inline-block rounded px-2 py-1 text-xs font-medium" :class="stateClass(targetState(target))" data-test="borrow-line-state">{{ t(`admin.codexGatewayBorrow.lineStates.${targetState(target)}`) }}</span>
                <p class="break-words text-xs text-muted" data-test="borrow-line-reason">{{ reasonLabel(target) }}</p>
              </div>
              <dl class="min-w-0 space-y-2 text-xs">
                <div><dt class="text-muted">{{ t('admin.codexGatewayBorrow.checkedAt') }}</dt><dd class="mt-1">{{ formatTime(target.checked_at) }}</dd></div>
                <div><dt class="text-muted">{{ text('候选线路到期时间', 'Candidate route expiry') }}</dt><dd class="mt-1">{{ formatTime(target.expires_at) }}</dd></div>
                <div v-if="target.expires_at"><dt class="sr-only">{{ text('候选线路剩余时间', 'Candidate time remaining') }}</dt><dd data-test="borrow-line-remaining">{{ t('admin.codexGatewayBorrow.remainingSeconds', { seconds: remainingSeconds(target.expires_at) }) }}</dd></div>
                <div v-if="!target.cache_valid && target.expires_at"><dt class="sr-only">{{ text('验证说明', 'Validation note') }}</dt><dd class="text-muted">{{ text('候选尚未到期也不代表验证通过。', 'An unexpired candidate does not mean validation passed.') }}</dd></div>
                <div v-if="target.retry_after"><dt class="text-muted">{{ t('admin.codexGatewayBorrow.retryAfter') }}</dt><dd class="mt-1">{{ formatTime(target.retry_after) }}</dd></div>
              </dl>
              <div>
                <button type="button" class="btn btn-secondary btn-sm max-w-full whitespace-normal" :disabled="!canAct" :data-test="`borrow-verify-${target.account_id}-${target.model}`" :aria-label="`${t('admin.codexGatewayBorrow.revalidatePair')} ${accountName(target.account_id)} #${target.account_id} ${target.model}`" @click="verify(target)">
                  {{ actionBusy === pairKey(target.account_id, target.model) ? t('admin.codexGatewayBorrow.validating') : t('admin.codexGatewayBorrow.revalidatePair') }}
                </button>
              </div>
              <details class="min-w-0 rounded-lg border border-line p-3 lg:col-span-4" data-test="borrow-target-details">
                <summary class="cursor-pointer text-sm font-medium">{{ t('admin.codexGatewayBorrow.technicalDetails') }}</summary>
                <dl class="mt-3 grid gap-3 text-xs sm:grid-cols-2">
                  <div><dt class="text-muted">{{ t('admin.codexGatewayBorrow.rawState') }}</dt><dd class="mt-1 break-words font-mono">{{ target.state }}</dd></div>
                  <div><dt class="text-muted">{{ t('admin.codexGatewayBorrow.rawReason') }}</dt><dd class="mt-1 break-words font-mono">{{ target.reason || '—' }}</dd></div>
                  <div v-if="target.acquisition?.started_at"><dt class="text-muted">{{ text('验证开始 / 结束', 'Validation started / finished') }}</dt><dd>{{ formatTime(target.acquisition.started_at) }} / {{ formatTime(target.acquisition.finished_at) }}</dd></div>
                  <div><dt class="text-muted">HTTP</dt><dd class="mt-1">{{ t('admin.codexGatewayBorrow.httpStatuses', { mint: target.mint_status || '—', continuation: target.continue_status || '—' }) }}</dd></div>
                  <div><dt class="text-muted">{{ t('admin.codexGatewayBorrow.stateMinted') }}</dt><dd class="mt-1">{{ target.minted ? t('common.yes') : t('common.no') }}</dd></div>
                  <div><dt class="text-muted">{{ t('admin.codexGatewayBorrow.newTicket') }}</dt><dd class="mt-1">{{ target.new_ticket ? t('common.yes') : t('common.no') }}</dd></div>
                  <div><dt class="text-muted">{{ t('admin.codexGatewayBorrow.reportedModel') }}</dt><dd class="mt-1 break-words">{{ target.reported_model || '—' }}</dd></div>
                  <div v-if="target.request_shape"><dt class="text-muted">{{ text('验证请求形态 / 实际模型', 'Validation shape / actual model') }}</dt><dd class="mt-1 break-words">{{ target.request_shape }} / {{ target.model }}</dd></div>
                  <div v-if="target.request_shape"><dt class="text-muted">{{ text('验证服务档位', 'Validation service tier') }}</dt><dd class="mt-1">{{ target.service_tier || 'default' }}</dd></div>
                  <div v-if="target.request_shape"><dt class="text-muted">{{ text('两轮上游完成', 'Both upstream completions') }}</dt><dd class="mt-1">{{ target.mint_completed ? t('common.yes') : t('common.no') }} / {{ target.continue_completed ? t('common.yes') : t('common.no') }}</dd></div>
                  <div v-if="target.request_shape"><dt class="text-muted">{{ text('两轮 STATE 长度', 'STATE lengths') }}</dt><dd class="mt-1">{{ target.mint_state_length ?? '—' }} / {{ target.continue_state_length ?? '—' }}</dd></div>
                  <div v-if="target.request_shape"><dt class="text-muted">{{ text('上游替换借用路由', 'Upstream replaced borrowed route') }}</dt><dd class="mt-1">{{ target.route_changed ? t('common.yes') : t('common.no') }}</dd></div>
                </dl>
                <p class="mt-3 text-xs text-muted">{{ t('admin.codexGatewayBorrow.stateUnavailable') }}</p>
                <div v-if="target.error" class="mt-3 space-y-1"><p class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.fullError') }}</p><pre class="borrow-error" data-test="borrow-target-error">{{ target.error }}</pre></div>
                <div v-if="accountById(target.account_id)" class="mt-3 space-y-2 text-xs text-muted">
                  <p>{{ accountStateLabel(accountById(target.account_id)) }}</p>
                  <p v-if="accountById(target.account_id)?.parent_account_id" class="break-words">{{ t('admin.codexGatewayBorrow.shadowOf', { id: accountById(target.account_id)?.parent_account_id, name: accountName(accountById(target.account_id)!.parent_account_id!) }) }}</p>
                  <p class="break-words">WS: {{ wsLabel(accountById(target.account_id)!) }} · {{ modelMappingLabel(accountById(target.account_id)!) }}</p>
                  <pre v-if="accountById(target.account_id)?.error_message" class="borrow-error">{{ accountById(target.account_id)?.error_message }}</pre>
                </div>
              </details>
            </li>
          </ul>
          <p v-else class="rounded-lg border border-dashed border-line p-4 text-sm text-muted" data-test="borrow-no-targets">{{ t('admin.codexGatewayBorrow.noConfiguredTargets') }}</p>
        </section>
        <CodexBorrowActivity :status="status" :account-name="accountName" @refresh="refreshStatus" />
      </template>
    </div>
  </component>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import CodexBorrowActivity from '@/components/admin/codex/CodexBorrowActivity.vue'
import CodexBorrowNav from '@/components/admin/CodexBorrowNav.vue'
import { BORROW_MODELS, codexGatewayBorrowAPI, type BorrowTargetStatus, type CodexGatewayBorrowStatus } from '@/api/admin/codexGatewayBorrow'
import { borrowReasonLabel, borrowTargetState, useCodexBorrowClock, useCodexBorrowInventory } from '@/composables/useCodexBorrowUI'
import { extractApiErrorMessage } from '@/utils/apiError'

defineProps<{ embedded?: boolean }>()
const { t, locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('en') ? en : zh
const lifecycle = new AbortController()
const { loadAccounts, accountById, accountName, accountStateLabel, proxyLabel, wsLabel, modelMappingLabel } = useCodexBorrowInventory(lifecycle.signal)
const { now, receiveServerTime, cacheUsable, remainingSeconds } = useCodexBorrowClock()
const status = ref<CodexGatewayBorrowStatus | null>(null)
const loading = ref(true)
const refreshing = ref(false)
const statusError = ref('')
const inventoryError = ref('')
const actionBusy = ref('')
const actionFailure = ref<{ message: string; detail: string } | null>(null)
let pollTimer: ReturnType<typeof setTimeout> | undefined
let statusRequest: Promise<void> | undefined

const targets = computed<BorrowTargetStatus[]>(() => {
  const saved = status.value?.config
  if (!saved) return []
  const models = BORROW_MODELS.filter(model => saved.models.includes(model))
  return [...new Set(saved.target_account_ids)].flatMap(account_id => models.map(model =>
    status.value!.targets.find(target => target.account_id === account_id && target.model === model) || {
      account_id, model, state: 'waiting', reason: 'not_verified', cache_valid: false,
      remaining_seconds: 0, mint_status: 0, continue_status: 0, minted: false, new_ticket: false
    }
  ))
})
const readyCount = computed(() => status.value?.enabled ? targets.value.filter(target => cacheUsable(target)).length : 0)
const candidateRemaining = computed(() => remainingSeconds(status.value?.candidate?.expires_at))
const serverValidating = computed(() => targets.value.some(target => target.state === 'validating' || target.reason === 'validating'))
const canAct = computed(() => !!status.value?.enabled && !loading.value && !actionBusy.value && !status.value.preparing && !serverValidating.value)
const shouldPoll = computed(() => !!status.value?.preparing || !!actionBusy.value || serverValidating.value)
const preparationLabel = computed(() => {
  if (status.value?.preparing || actionBusy.value === 'prepare') return t('admin.codexGatewayBorrow.preparationPending')
  if (actionBusy.value || serverValidating.value) return t('admin.codexGatewayBorrow.validating')
  if (!status.value?.enabled || !targets.value.length) return text('未配置或未启用', 'Not configured or disabled')
  if (readyCount.value === targets.value.length) return text('线路可用', 'Routes ready')
  if (readyCount.value > 0) return text('部分线路可用', 'Some routes ready')
  if (targets.value.some(target => targetState(target) === 'expired')) return t('admin.codexGatewayBorrow.onDemandExpired')
  return text('尚无可用线路，查看下方原因', 'No ready route; see reasons below')
})

const acquisitionLabel = computed(() => {
  const phase = status.value?.acquisition?.phase
  if (phase === 'acquiring') return text('正在获取来源候选', 'Acquiring a source candidate')
  if (phase === 'failed') return text('获取失败；冷却结束后的下一次请求可重试', 'Acquisition failed; the next request after cooldown can retry')
  if (phase === 'cancelled') return text('获取已取消', 'Acquisition cancelled')
  if (candidateRemaining.value <= 0) return t('admin.codexGatewayBorrow.onDemandExpired')
  return text('已取得来源候选；目标是否可用以下方验证为准', 'Source candidate acquired; target readiness depends on validation below')
})

function pairKey(id: number, model: string) { return `${id}:${model}` }
function formatTime(value?: string) {
  if (!value || value.startsWith('0001-01-01')) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}
function targetState(target: BorrowTargetStatus) {
  if (!status.value?.enabled) return 'waiting'
  const state = borrowTargetState(target, now.value)
  if (state !== 'ready' && actionBusy.value === pairKey(target.account_id, target.model)) return 'validating'
  if (state === 'waiting' && (status.value.preparing || actionBusy.value === 'prepare')) return 'validating'
  return state
}
function reasonLabel(target: BorrowTargetStatus) {
  if (!status.value?.enabled) return borrowReasonLabel('disabled', t)
  const state = targetState(target)
  if (state === 'expired') return borrowReasonLabel('route_expired', t)
  if (state === 'validating' && (actionBusy.value || target.state === 'waiting')) return borrowReasonLabel('validating', t)
  return borrowReasonLabel(target.reason, t)
}
function stateClass(state: string) {
  if (state === 'ready') return 'bg-emerald-50 text-emerald-800 dark:bg-emerald-950 dark:text-emerald-200'
  if (state === 'rejected') return 'bg-red-50 text-red-800 dark:bg-red-950 dark:text-red-200'
  if (state === 'expired') return 'bg-amber-50 text-amber-800 dark:bg-amber-950 dark:text-amber-200'
  return 'bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-gray-200'
}
function receiveStatus(value: CodexGatewayBorrowStatus) {
  if (lifecycle.signal.aborted) return
  const current = status.value
  if (current && value.revision < current.revision) return
  if (current && value.revision === current.revision && Date.parse(value.generated_at) < Date.parse(current.generated_at)) return
  status.value = value
  receiveServerTime(value.generated_at)
}
function clearPollTimer() {
  if (pollTimer !== undefined) clearTimeout(pollTimer)
  pollTimer = undefined
}
function schedulePoll() {
  if (lifecycle.signal.aborted || !shouldPoll.value) { clearPollTimer(); return }
  if (pollTimer !== undefined || statusRequest) return
  pollTimer = setTimeout(() => {
    pollTimer = undefined
    void refreshStatus()
  }, 5000)
}
function refreshStatus(): Promise<void> {
  if (statusRequest) return statusRequest
  if (lifecycle.signal.aborted) return Promise.resolve()
  clearPollTimer()
  refreshing.value = true
  statusError.value = ''
  statusRequest = (async () => {
    try { receiveStatus(await codexGatewayBorrowAPI.getStatus(lifecycle.signal)) }
    catch (value) { if (!lifecycle.signal.aborted) statusError.value = extractApiErrorMessage(value, t('admin.codexGatewayBorrow.statusLoadFailed')) }
    finally {
      refreshing.value = false
      statusRequest = undefined
      schedulePoll()
    }
  })()
  return statusRequest
}
async function refreshAfterAction() {
  // A poll started before the operation ended may still contain its old result.
  if (statusRequest) await statusRequest
  if (!lifecycle.signal.aborted) await refreshStatus()
}
async function prepare() {
  if (!canAct.value) return
  actionBusy.value = 'prepare'
  actionFailure.value = null
  try { receiveStatus(await codexGatewayBorrowAPI.prepare(lifecycle.signal)) }
  catch (value) {
    if (!lifecycle.signal.aborted) actionFailure.value = { message: t('admin.codexGatewayBorrow.prepareFailed'), detail: extractApiErrorMessage(value, t('admin.codexGatewayBorrow.prepareFailed')) }
  } finally {
    await refreshAfterAction()
    actionBusy.value = ''
  }
}
async function verify(target: BorrowTargetStatus) {
  if (!canAct.value) return
  actionBusy.value = pairKey(target.account_id, target.model)
  actionFailure.value = null
  try {
    const result = await codexGatewayBorrowAPI.verify(target.account_id, target.model, lifecycle.signal)
    if (!lifecycle.signal.aborted && !result.success) actionFailure.value = {
      message: `${t('admin.codexGatewayBorrow.verifyFailed')}: ${borrowReasonLabel(result.reason, t)}`,
      detail: result.error || result.reason
    }
  } catch (value) {
    if (!lifecycle.signal.aborted) actionFailure.value = { message: t('admin.codexGatewayBorrow.verifyFailed'), detail: extractApiErrorMessage(value, t('admin.codexGatewayBorrow.verifyFailed')) }
  } finally {
    await refreshAfterAction()
    actionBusy.value = ''
  }
}

watch(shouldPoll, schedulePoll)
onMounted(async () => {
  const outcomes = await Promise.allSettled([refreshStatus(), loadAccounts()])
  if (lifecycle.signal.aborted) return
  if (outcomes[1].status === 'rejected') inventoryError.value = extractApiErrorMessage(outcomes[1].reason, t('admin.codexGatewayBorrow.inventoryLoadFailed'))
  loading.value = false
})
onBeforeUnmount(() => { lifecycle.abort(); clearPollTimer() })
defineExpose({ refreshStatus })
</script>

<style scoped>
.borrow-error { max-height: 16rem; overflow: auto; white-space: pre-wrap; overflow-wrap: anywhere; border-radius: 0.5rem; padding: 0.65rem; font-size: 0.75rem; line-height: 1.5; color: rgb(153 27 27); background: rgb(254 242 242); }
:global(.dark) .borrow-error { color: rgb(254 202 202); background: rgb(69 10 10); }
</style>
