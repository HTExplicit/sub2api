<template>
  <AppLayout>
    <div class="mx-auto max-w-[1664px] space-y-6 px-1" data-ui="codex-pelican-comparison">
      <header class="space-y-4 border-b border-line pb-4">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div class="min-w-0 space-y-1">
            <h1 class="text-xl font-semibold">{{ t('admin.codexPelicanComparison.title') }}</h1>
            <p class="max-w-4xl text-sm text-muted">{{ t('admin.codexPelicanComparison.description') }}</p>
            <RouterLink to="/admin/codex-gateway-borrow/status" class="inline-block text-sm text-primary-600 underline dark:text-primary-400" data-test="pelican-status-link">{{ t('admin.codexPelicanComparison.viewStatus') }}</RouterLink>
          </div>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="loading || refreshing" data-test="borrow-refresh" @click="refreshStatus">
            <Icon name="refresh" size="sm" aria-hidden="true" />{{ t('admin.codexPelicanComparison.refreshAvailability') }}
          </button>
        </div>
        <CodexBorrowNav />
      </header>
      <pre v-if="error" role="alert" class="borrow-error">{{ error }}</pre>
      <p v-if="loading" class="py-4 text-center text-sm text-muted">{{ t('common.loading') }}</p>

      <section v-if="status" class="card space-y-4 p-4 sm:p-5" aria-labelledby="pelican-selection-title">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div class="space-y-1">
            <h2 id="pelican-selection-title" class="font-semibold">{{ t('admin.codexGatewayBorrow.testAccounts') }}</h2>
            <p class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.pelicanHint') }}</p>
          </div>
          <div class="flex flex-wrap gap-2">
            <button v-if="running" type="button" class="btn btn-secondary btn-sm" data-test="borrow-test-stop" @click="stopTests">{{ t('admin.codexGatewayBorrow.stop') }}</button>
            <button type="button" class="btn btn-primary btn-sm" :disabled="!canRunTests" data-test="borrow-test-start" @click="startTests">
              <Icon name="play" size="sm" aria-hidden="true" />{{ t('admin.codexGatewayBorrow.startTest', { count: testTargets.length }) }}
            </button>
          </div>
        </div>
        <p v-if="!status.enabled" class="text-sm text-muted" data-test="pelican-disabled-hint">{{ t('admin.codexPelicanComparison.disabledHint') }}</p>
        <p v-if="!serverClockKnown" class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.clockUnavailable') }}</p>
        <details class="rounded-lg border border-line p-3" data-test="pelican-prompt-details">
          <summary class="cursor-pointer text-sm font-medium">{{ t('admin.codexPelicanComparison.viewPrompt') }}</summary>
          <label for="borrow-fixed-prompt" class="mb-2 mt-3 block text-xs font-medium">{{ t('admin.codexGatewayBorrow.fixedPrompt') }}</label>
          <textarea id="borrow-fixed-prompt" :value="PELICAN_BORROW_PROMPT" readonly rows="3" class="input w-full resize-none text-sm" data-test="borrow-fixed-prompt" />
        </details>
        <fieldset :disabled="running || loading" class="grid gap-3 sm:grid-cols-2">
          <legend class="mb-2 text-sm font-medium">{{ t('admin.codexGatewayBorrow.testModels') }}</legend>
          <div v-for="model in savedModels" :key="model" class="flex flex-wrap items-center gap-3 rounded-lg border border-line p-3">
            <label class="flex min-w-0 items-center gap-2 text-sm">
              <input v-model="selectedTestModels" type="checkbox" :value="model" class="checkbox" :data-test="`borrow-test-model-${model}`" />{{ model }}
            </label>
            <label class="ml-auto flex items-center gap-2 text-xs text-muted">
              {{ t('admin.codexGatewayBorrow.effort') }}
              <select v-model="modelEfforts[model]" class="input w-24 text-sm" :aria-label="`${model} ${t('admin.codexGatewayBorrow.effort')}`" :data-test="`borrow-effort-${model}`">
                <option v-for="effort in effortOptions(model)" :key="effort" :value="effort">{{ effort }}</option>
              </select>
            </label>
          </div>
        </fieldset>
        <fieldset :disabled="running || loading" class="space-y-2">
          <legend class="mb-2 text-sm font-medium">{{ t('admin.codexGatewayBorrow.testAccounts') }} · {{ selectedTestAccountIds.length }}</legend>
          <div class="grid gap-2 md:grid-cols-2 xl:grid-cols-3">
            <label v-for="id in status.config.target_account_ids" :key="id" class="flex cursor-pointer items-start gap-3 rounded-lg border border-line p-3">
              <input v-model="selectedTestAccountIds" type="checkbox" :value="id" class="checkbox mt-1 shrink-0" :data-test="`borrow-test-account-${id}`" />
              <span class="min-w-0 flex-1 space-y-2">
                <span class="block break-words text-sm font-medium">{{ accountName(id) }} #{{ id }}</span>
                <span class="block text-xs text-muted">{{ accountStateLabel(accountById(id)) }} · {{ t('admin.codexGatewayBorrow.proxy') }}: {{ proxyLabel(accountById(id)) }}</span>
                <span v-for="model in savedModels" :key="model" class="block space-y-1 border-t border-line pt-2" :data-test="`pelican-qualification-${id}-${model}`">
                  <span class="block break-words text-xs font-medium" :class="cacheUsable(targetStatus(id, model)) ? 'text-emerald-700 dark:text-emerald-300' : 'text-muted'">
                    {{ model }} · {{ routeStateLabel(id, model) }}<span v-if="!cacheUsable(targetStatus(id, model))"> · {{ t('admin.codexGatewayBorrow.willSkip') }}</span>
                  </span>
                  <span class="block break-words text-xs text-muted">{{ routeReason(id, model) }}</span>
                </span>
              </span>
            </label>
          </div>
          <p v-if="!status.config.target_account_ids.length" class="rounded-lg border border-dashed border-line p-4 text-sm text-muted">{{ t('admin.codexPelicanComparison.noTargets') }}</p>
        </fieldset>
        <p class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.executionHint') }}</p>
      </section>

      <section class="card space-y-4 p-4 sm:p-5" :aria-label="t('admin.codexGatewayBorrow.resultViews')">
        <pre v-if="testError" role="alert" class="borrow-error">{{ testError }}</pre>
        <p v-if="testStopped" role="status" class="text-sm text-muted">{{ t('admin.codexGatewayBorrow.stoppedHint') }}</p>
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-line pb-3">
          <div class="flex flex-wrap gap-2" role="tablist" :aria-label="t('admin.codexGatewayBorrow.resultViews')">
            <button id="pelican-results-tab" type="button" role="tab" class="btn btn-sm" :class="activeTab === 'results' ? 'btn-primary' : 'btn-secondary'" :aria-selected="activeTab === 'results'" :tabindex="activeTab === 'results' ? 0 : -1" aria-controls="pelican-results-panel" data-test="borrow-tab-results" @click="activeTab = 'results'" @keydown="changeTabWithKeyboard">{{ t('admin.codexPelicanComparison.results') }}</button>
            <button id="pelican-history-tab" type="button" role="tab" class="btn btn-sm" :class="activeTab === 'history' ? 'btn-primary' : 'btn-secondary'" :aria-selected="activeTab === 'history'" :tabindex="activeTab === 'history' ? 0 : -1" aria-controls="pelican-history-panel" data-test="borrow-tab-history" @click="activeTab = 'history'" @keydown="changeTabWithKeyboard">{{ t('admin.codexPelicanComparison.history') }}</button>
          </div>
          <p v-if="activeTask" class="text-xs text-muted" role="status" data-test="pelican-progress">{{ activeTask.completed }} / {{ activeTask.total }} · {{ stateLabel(activeTask.status) }} <span v-if="activeTask.replayed">· {{ t('admin.codexGatewayBorrow.replayed') }}</span></p>
        </div>
        <div v-if="activeTab === 'history'" id="pelican-history-panel" role="tabpanel" aria-labelledby="pelican-history-tab" class="space-y-4">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <p class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.historyHint') }}</p>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="historyLoading" data-test="borrow-history-refresh" @click="loadHistory(historyPage)">{{ t('admin.codexGatewayBorrow.refreshHistory') }}</button>
          </div>
          <pre v-if="historyError" role="alert" class="borrow-error">{{ historyError }}</pre>
          <p v-if="historyLoading" class="text-sm text-muted">{{ t('common.loading') }}</p>
          <button v-for="task in history.items" :key="task.id" type="button" class="flex w-full flex-wrap items-center justify-between gap-2 rounded-lg border border-line p-3 text-left hover:bg-gray-50 dark:hover:bg-dark-800" :disabled="running || historyLoading" :data-test="`borrow-history-${task.id}`" @click="openHistory(task.id)">
            <span class="min-w-0">
              <span class="block text-sm font-medium">{{ formatTime(task.created_at) }} · {{ task.completed }} / {{ task.total }} · {{ stateLabel(task.status) }}</span>
              <span class="mt-1 block break-all text-xs text-muted">{{ task.id }} · {{ t('admin.codexGatewayBorrow.expiresAt') }} {{ formatTime(task.expires_at) }}</span>
            </span>
            <span class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.viewResults') }}</span>
          </button>
          <p v-if="!historyLoading && !history.items.length" class="py-6 text-center text-sm text-muted">{{ t('admin.codexGatewayBorrow.noHistory') }}</p>
          <div v-if="history.total > 12" class="flex items-center justify-end gap-3">
            <button type="button" class="btn btn-secondary btn-sm" :disabled="historyPage <= 1 || historyLoading" @click="loadHistory(historyPage - 1)">{{ t('admin.codexGatewayBorrow.previous') }}</button>
            <span class="text-xs text-muted">{{ historyPage }} / {{ Math.ceil(history.total / 12) }}</span>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="historyPage * 12 >= history.total || historyLoading" @click="loadHistory(historyPage + 1)">{{ t('common.next') }}</button>
          </div>
        </div>
        <div v-else id="pelican-results-panel" role="tabpanel" aria-labelledby="pelican-results-tab" class="space-y-4">
          <div v-if="activeTask" class="space-y-1 text-xs text-muted">
            <p class="break-all">{{ t('admin.codexGatewayBorrow.taskId') }}: {{ activeTask.id }} · {{ formatTime(activeTask.created_at) }}</p>
            <p>{{ t('admin.codexGatewayBorrow.expiresAt') }}: {{ formatTime(activeTask.expires_at) }}</p>
            <pre v-if="activeTask.error" class="borrow-error">{{ activeTask.error }}</pre>
          </div>
          <p v-if="!results.length" class="rounded-lg border border-dashed border-line py-8 text-center text-sm text-muted">{{ t('admin.codexGatewayBorrow.noResults') }}</p>
          <div class="borrow-result-grid" data-test="borrow-result-grid">
            <article v-for="result in results" :key="result.id" class="borrow-result-card overflow-hidden rounded-xl border border-line bg-white dark:bg-dark-800" :data-test="`borrow-result-${result.id}`">
              <header class="space-y-2 border-b border-line px-3 py-2.5">
                <div class="flex items-start justify-between gap-2">
                  <p class="min-w-0 break-words text-sm font-medium">{{ result.account_name || accountName(result.account_id) }} <span class="font-normal text-muted">#{{ result.account_id }}</span></p>
                  <span class="shrink-0 rounded px-2 py-0.5 text-xs font-medium" :class="resultStatusClass(result.status)">{{ stateLabel(result.status) }}</span>
                </div>
                <div class="space-y-1 text-xs text-muted">
                  <p class="break-words">{{ result.model_id }} · {{ result.effort || 'high' }}</p>
                  <p v-if="result.upstream_model && result.upstream_model !== result.model_id" class="break-words">{{ t('admin.codexGatewayBorrow.reportedModel') }}: {{ result.upstream_model }}</p>
                  <p>{{ t('admin.codexGatewayBorrow.duration') }}: {{ formatDuration(result.duration_ms) }} · {{ formatTime(result.started_at) }}</p>
                </div>
              </header>
              <div class="aspect-[4/3] w-full bg-gray-50 dark:bg-dark-900">
                <BorrowPelicanPreview v-if="result.preview_url" :preview-url="result.preview_url" :title="previewTitle(result)" />
                <div v-else class="flex h-full items-center justify-center px-5 text-center text-xs text-muted"><span>{{ result.preview_unavailable || t('admin.codexGatewayBorrow.previewPending') }}</span></div>
              </div>
              <div class="space-y-3 border-t border-line p-3">
                <div class="flex flex-wrap gap-2">
                  <button type="button" class="btn btn-secondary btn-sm" :disabled="!result.preview_url" :aria-label="`${t('admin.codexGatewayBorrow.enlarge')} ${previewTitle(result)}`" :data-test="`borrow-enlarge-${result.id}`" @click="enlarged = result">{{ t('admin.codexGatewayBorrow.enlarge') }}</button>
                  <button type="button" class="btn btn-secondary btn-sm" :disabled="!result.raw_html && !result.html" @click="downloadHtml(result)"><Icon name="download" size="sm" aria-hidden="true" />HTML</button>
                </div>
                <pre v-if="result.error" class="borrow-error" data-test="borrow-result-error">{{ result.error }}</pre>
                <details v-if="result.raw_answer" class="text-xs">
                  <summary class="cursor-pointer font-medium">{{ t('admin.codexGatewayBorrow.rawAnswer') }}</summary><pre class="borrow-source mt-2">{{ result.raw_answer }}</pre>
                  <button type="button" class="btn btn-secondary btn-sm mt-2" @click="downloadAnswer(result)">{{ t('admin.codexGatewayBorrow.downloadAnswer') }}</button>
                </details>
                <details v-if="result.raw_html || result.html" class="text-xs"><summary class="cursor-pointer font-medium">{{ t('admin.codexGatewayBorrow.htmlSource') }}</summary><pre class="borrow-source mt-2">{{ result.raw_html || result.html }}</pre></details>
                <details v-if="result.raw_response" class="text-xs"><summary class="cursor-pointer font-medium">{{ t('admin.codexGatewayBorrow.rawResponse') }}</summary><pre class="borrow-source mt-2">{{ result.raw_response }}</pre></details>
              </div>
            </article>
          </div>
        </div>
      </section>
      <BaseDialog :show="!!enlarged" :title="enlarged ? previewTitle(enlarged) : t('admin.codexGatewayBorrow.enlarge')" width="extra-wide" @close="enlarged = null">
        <div v-if="enlarged?.preview_url" class="aspect-[4/3] max-h-[70vh] w-full"><BorrowPelicanPreview :preview-url="enlarged.preview_url" :title="previewTitle(enlarged)" interactive /></div>
      </BaseDialog>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import CodexBorrowNav from '@/components/admin/CodexBorrowNav.vue'
import BorrowPelicanPreview from '@/components/admin/codex/BorrowPelicanPreview.vue'
import {
  BORROW_MODELS, PELICAN_BORROW_PROMPT, codexGatewayBorrowAPI, createBorrowClientTaskId,
  type CodexGatewayBorrowStatus, type BorrowTestEvent, type BorrowTestHistory,
  type BorrowTestRequest, type BorrowTestResult, type BorrowTestTask
} from '@/api/admin/codexGatewayBorrow'
import { borrowReasonLabel, borrowTargetState, useCodexBorrowClock, useCodexBorrowInventory } from '@/composables/useCodexBorrowUI'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t, te } = useI18n()
const lifecycle = new AbortController()
const { loadAccounts, accountById, accountName, accountStateLabel, proxyLabel } = useCodexBorrowInventory(lifecycle.signal)
const { now, serverClockKnown, serverOffsetMs, receiveServerTime, cacheUsable } = useCodexBorrowClock()
const status = ref<CodexGatewayBorrowStatus | null>(null)
const loading = ref(true)
const refreshing = ref(false)
const error = ref('')
const running = ref(false)
const testError = ref('')
const testStopped = ref(false)
const selectedTestAccountIds = ref<number[]>([])
const selectedTestModels = ref<string[]>([...BORROW_MODELS])
const modelEfforts = ref<Record<string, string>>({ 'gpt-6-astra': 'high', 'gpt-6.1-sol': 'high' })
const activeTask = ref<BorrowTestTask | null>(null)
const enlarged = ref<BorrowTestResult | null>(null)
const history = ref<BorrowTestHistory>({ items: [], total: 0, page: 1, size: 12 })
const historyPage = ref(1)
const historyLoading = ref(false)
const historyError = ref('')
const activeTab = ref<'results' | 'history'>('results')
let testController: AbortController | undefined

const savedModels = computed(() => BORROW_MODELS.filter(model => status.value?.config.models.includes(model)))
const activeTestModels = computed(() => savedModels.value.filter(model => selectedTestModels.value.includes(model)))
const testTargets = computed(() => [...new Set(selectedTestAccountIds.value)]
  .filter(id => status.value?.config.target_account_ids.includes(id))
  .flatMap(account_id => activeTestModels.value.map(model_id => ({ account_id, model_id, effort: modelEfforts.value[model_id] || 'high' }))))
const canRunTests = computed(() => !!status.value?.enabled && serverClockKnown.value && testTargets.value.length > 0 && !running.value && !loading.value && !refreshing.value && !historyLoading.value)
const results = computed(() => activeTask.value?.results || [])

function receiveStatus(value: CodexGatewayBorrowStatus) {
  if (lifecycle.signal.aborted) return
  status.value = value
  receiveServerTime(value.generated_at)
  for (const model of BORROW_MODELS) {
    const options = effortOptions(model)
    if (!options.includes(modelEfforts.value[model])) modelEfforts.value[model] = options.includes('high') ? 'high' : options[0]
  }
}
function targetStatus(id: number, model: string) { return status.value?.targets.find(target => target.account_id === id && target.model === model) }
function routeStateLabel(id: number, model: string) { return t(`admin.codexGatewayBorrow.lineStates.${borrowTargetState(targetStatus(id, model), now.value)}`) }
function routeReason(id: number, model: string) {
  const target = targetStatus(id, model)
  const reason = borrowTargetState(target, now.value) === 'expired' ? 'route_expired' : target?.reason || 'no_matched_cache'
  return borrowReasonLabel(reason, t)
}
function effortOptions(model: string) { return status.value?.model_efforts?.[model]?.length ? status.value.model_efforts[model] : ['high'] }
function stateLabel(state: string) { const key = `admin.codexGatewayBorrow.states.${state}`; return te(key) ? t(key) : state }
function formatTime(value?: string) { if (!value || value.startsWith('0001-01-01')) return '—'; const date = new Date(value); return Number.isNaN(date.getTime()) ? value : date.toLocaleString() }
function formatDuration(ms?: number) { return typeof ms === 'number' ? `${(ms / 1000).toFixed(1)} s` : '—' }
function resultStatusClass(value: string) {
  if (value === 'complete') return 'bg-emerald-50 text-emerald-800 dark:bg-emerald-950 dark:text-emerald-200'
  if (value === 'failed' || value === 'incomplete') return 'bg-red-50 text-red-800 dark:bg-red-950 dark:text-red-200'
  return 'bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-gray-200'
}
function previewTitle(result: BorrowTestResult) { return `${result.account_name || accountName(result.account_id)} #${result.account_id} · ${result.model_id} · ${result.effort}` }
async function changeTabWithKeyboard(event: KeyboardEvent) {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
  event.preventDefault()
  activeTab.value = event.key === 'Home' ? 'results' : event.key === 'End' ? 'history' : activeTab.value === 'results' ? 'history' : 'results'
  await nextTick()
  document.getElementById(`pelican-${activeTab.value}-tab`)?.focus()
}
async function refreshStatus() {
  if (refreshing.value || loading.value) return
  refreshing.value = true
  error.value = ''
  try { receiveStatus(await codexGatewayBorrowAPI.getStatus(lifecycle.signal)) }
  catch (value) { if (!lifecycle.signal.aborted) error.value = extractApiErrorMessage(value, t('admin.codexGatewayBorrow.loadFailed')) }
  finally { refreshing.value = false }
}
function receiveTestEvent(event: BorrowTestEvent) {
  if (testController?.signal.aborted || lifecycle.signal.aborted) return
  if (event.type === 'task_start' || event.type === 'task_complete') {
    activeTask.value = event.task
  } else if (event.type === 'result_started' || event.type === 'result_complete') {
    if (!activeTask.value || activeTask.value.id !== event.task_id) return
    const next = [...(activeTask.value.results || [])]
    const index = next.findIndex(result => result.id === event.result.id)
    if (index >= 0) next[index] = event.result
    else next.push(event.result)
    activeTask.value = { ...activeTask.value, results: next, completed: next.filter(result => !['pending', 'running'].includes(result.status)).length }
  }
}
async function startTests() {
  if (!canRunTests.value) return
  const request: BorrowTestRequest = { client_task_id: createBorrowClientTaskId(serverOffsetMs.value), targets: testTargets.value.map(target => ({ ...target })) }
  testController = new AbortController()
  running.value = true
  testError.value = ''
  testStopped.value = false
  enlarged.value = null
  activeTab.value = 'results'
  activeTask.value = { id: request.client_task_id, client_task_id: request.client_task_id, status: 'pending', created_at: new Date(now.value).toISOString(), expires_at: '', total: request.targets.length, completed: 0, results: request.targets.map((target, index) => ({ id: `${request.client_task_id}-${index}`, ...target, account_name: accountName(target.account_id), status: 'pending', raw_answer: '', raw_response: '', raw_html: '', html: '', error: '' })) }
  try { await codexGatewayBorrowAPI.streamTests(request, receiveTestEvent, testController.signal) }
  catch (value) {
    if (!testController.signal.aborted && !lifecycle.signal.aborted) {
      testError.value = extractApiErrorMessage(value, t('admin.codexGatewayBorrow.testFailed'))
      if (activeTask.value) activeTask.value = { ...activeTask.value, status: 'incomplete', results: results.value.map(result => ['pending', 'running'].includes(result.status) ? { ...result, status: 'incomplete' as const } : result) }
    }
  } finally {
    running.value = false
    if (!lifecycle.signal.aborted) await loadHistory(1)
  }
}
function stopTests() {
  testController?.abort()
  testStopped.value = true
  if (activeTask.value) {
    const next = results.value.map(result => ['pending', 'running'].includes(result.status) ? { ...result, status: 'cancelled' as const } : result)
    activeTask.value = { ...activeTask.value, status: 'cancelled', results: next, completed: next.length }
  }
}
async function loadHistory(page = 1) {
  if (historyLoading.value || lifecycle.signal.aborted) return
  historyLoading.value = true
  historyError.value = ''
  try {
    const value = await codexGatewayBorrowAPI.listTests(page, lifecycle.signal)
    if (!lifecycle.signal.aborted) { history.value = value; historyPage.value = page }
  } catch (value) { if (!lifecycle.signal.aborted) historyError.value = extractApiErrorMessage(value, t('admin.codexGatewayBorrow.historyFailed')) }
  finally { historyLoading.value = false }
}
async function openHistory(id: string) {
  if (running.value || historyLoading.value || lifecycle.signal.aborted) return
  const previousTask = activeTask.value
  historyLoading.value = true
  historyError.value = ''
  try {
    const task = await codexGatewayBorrowAPI.getTest(id, lifecycle.signal)
    if (!lifecycle.signal.aborted && !running.value && activeTask.value === previousTask) { activeTask.value = task; activeTab.value = 'results'; testStopped.value = false; testError.value = ''; enlarged.value = null }
  } catch (value) { if (!lifecycle.signal.aborted) historyError.value = extractApiErrorMessage(value, t('admin.codexGatewayBorrow.historyFailed')) }
  finally { historyLoading.value = false }
}
function downloadText(text: string, name: string, type: string) {
  const url = URL.createObjectURL(new Blob([text], { type }))
  const link = document.createElement('a')
  link.href = url; link.download = name; link.click()
  setTimeout(() => URL.revokeObjectURL(url), 0)
}
function downloadHtml(result: BorrowTestResult) { downloadText(result.raw_html || result.html, `pelican-${result.account_id}-${result.model_id}-${result.id}.html`, 'text/html;charset=utf-8') }
function downloadAnswer(result: BorrowTestResult) { downloadText(result.raw_answer, `pelican-${result.account_id}-${result.model_id}-${result.id}.txt`, 'text/plain;charset=utf-8') }

onMounted(async () => {
  const outcomes = await Promise.allSettled([
    codexGatewayBorrowAPI.getStatus(lifecycle.signal).then(receiveStatus),
    loadAccounts(), loadHistory()
  ])
  if (!lifecycle.signal.aborted) {
    const failures = outcomes.filter((outcome): outcome is PromiseRejectedResult => outcome.status === 'rejected')
    if (failures.length) error.value = failures.map(outcome => extractApiErrorMessage(outcome.reason, t('admin.codexGatewayBorrow.loadFailed'))).join('\n')
    loading.value = false
  }
})
onBeforeUnmount(() => { lifecycle.abort(); testController?.abort() })
</script>

<style scoped>
.borrow-result-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 280px), 1fr)); gap: 1rem; align-items: start; }
.borrow-result-card { width: 100%; max-width: 360px; min-width: 0; }
.borrow-error { max-height: 16rem; overflow: auto; white-space: pre-wrap; overflow-wrap: anywhere; border-radius: 0.5rem; padding: 0.65rem; font-size: 0.75rem; line-height: 1.5; color: rgb(153 27 27); background: rgb(254 242 242); }
:global(.dark) .borrow-error { color: rgb(254 202 202); background: rgb(69 10 10); }
.borrow-source { max-height: 16rem; overflow: auto; white-space: pre-wrap; overflow-wrap: anywhere; border-radius: 0.375rem; padding: 0.75rem; color: rgb(229 231 235); background: rgb(3 7 18); line-height: 1.5; }
</style>
