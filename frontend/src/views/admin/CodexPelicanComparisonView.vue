<template>
  <AppLayout>
    <div class="mx-auto w-full max-w-[1920px] space-y-4" data-ui="pelican-tests">
      <header class="flex flex-wrap items-center justify-between gap-3 border-b border-line pb-4">
        <div><h1 class="text-xl font-semibold">{{ t('admin.pelicanTests.title') }}</h1><p class="mt-1 text-sm text-muted">{{ text('同一道题，按任务保存作品，比较完整结果。', 'One fixed prompt, saved tasks and complete works to compare.') }}</p></div>
        <button type="button" class="btn btn-primary" :disabled="submitting || !!pendingRequest" data-test="pelican-new" @click="newTest"><Icon name="plus" size="sm" />{{ text('新建测试', 'New test') }}</button>
      </header>
      <p class="rounded-lg border border-line p-3 text-sm text-muted">{{ text('作品呈现只供观察。模型名称、借用应用和完成状态分别记录，不据此判断真实模型身份或“满血／降智”。', 'Visual works are observations. Model labels, borrowing and completion are recorded separately; they do not prove model identity or intelligence.') }}</p>
      <pre v-if="error" role="alert" class="pelican-error">{{ error }}</pre>
      <div v-if="pendingRequest" class="space-y-2 rounded-lg border border-amber-300 p-3 text-sm" role="status">
        <p>{{ text('上次提交结果尚未确认。查询或重试使用同一个任务标识，避免重复生成。', 'The previous submission is unconfirmed. Checking or retrying uses the same task identity to avoid duplicate generation.') }}</p>
        <div class="flex gap-2"><button type="button" class="btn btn-secondary btn-sm" :disabled="submitting" @click="recoverPending">{{ text('查询任务', 'Check task') }}</button><button type="button" class="btn btn-secondary btn-sm" :disabled="submitting" @click="submitRequest(pendingRequest)">{{ text('重试同一提交', 'Retry same submission') }}</button></div>
      </div>
      <div class="flex gap-2 lg:hidden"><button type="button" class="btn btn-secondary flex-1" :aria-pressed="mobilePane === 'tasks'" @click="mobilePane = 'tasks'">{{ text('任务', 'Tasks') }}</button><button type="button" class="btn btn-secondary flex-1" :aria-pressed="mobilePane === 'works'" @click="mobilePane = 'works'">{{ text('作品与比较', 'Works and comparison') }}</button></div>
      <div class="grid min-w-0 gap-4 lg:grid-cols-[280px_minmax(0,1fr)]">
        <aside class="min-w-0 space-y-3" :class="mobilePane === 'tasks' ? '' : 'hidden lg:block'" :aria-label="text('测试任务', 'Test tasks')">
          <div class="flex items-center justify-between"><h2 class="font-semibold">{{ text('最近 24 小时任务', 'Tasks from the last 24 hours') }}</h2><button type="button" class="btn btn-ghost btn-sm" :disabled="historyLoading" data-test="pelican-refresh" @click="loadHistory">{{ t('admin.codexGatewayBorrow.refreshHistory') }}</button></div>
          <p v-if="historyLoading" class="text-sm text-muted">{{ t('common.loading') }}</p>
          <button v-for="task in history.items" :key="task.id" type="button" class="block w-full rounded-lg border p-3 text-left" :class="activeTask?.id === task.id ? 'border-primary-500 bg-primary-50 dark:bg-primary-950' : 'border-line'" :aria-current="activeTask?.id === task.id ? 'true' : undefined" :data-test="`pelican-task-${task.id}`" @click="selectTask(task.id)">
            <span class="block text-sm font-medium">{{ formatTime(task.created_at) }}</span><span class="mt-1 block text-xs">{{ stateLabel(task.status) }} · {{ text('已处理', 'Processed') }} {{ task.completed }} / {{ task.total }}</span><span class="mt-1 block truncate text-xs text-muted">{{ task.id }}</span>
          </button>
          <p v-if="!historyLoading && !history.items.length" class="rounded-lg border border-dashed border-line p-5 text-sm text-muted">{{ t('admin.codexGatewayBorrow.noHistory') }}</p>
          <div class="flex items-center justify-between text-sm"><button type="button" class="btn btn-secondary btn-sm" :disabled="historyPage <= 1 || historyLoading" @click="historyPage--; loadHistory()">{{ t('pagination.previous') }}</button><span>{{ historyPage }}</span><button type="button" class="btn btn-secondary btn-sm" :disabled="historyPage * 12 >= history.total || historyLoading" @click="historyPage++; loadHistory()">{{ t('pagination.next') }}</button></div>
        </aside>
        <section :aria-label="text('作品与比较', 'Works and comparison')" class="min-w-0 space-y-4" :class="mobilePane === 'works' ? '' : 'hidden lg:block'">
          <section v-if="comparison.length" class="card space-y-3 p-4" data-test="pelican-comparison-tray">
            <div class="flex flex-wrap items-center justify-between gap-2"><h2 class="font-semibold">{{ text('跨任务比较', 'Compare across tasks') }} {{ comparison.length }} / 4</h2><button type="button" class="btn btn-primary btn-sm" :disabled="comparison.length < 2 || comparisonBusy" data-test="pelican-compare" @click="openComparison">{{ text('并排查看', 'View side by side') }}</button></div>
            <ul class="flex flex-wrap gap-2"><li v-for="item in comparison" :key="item.id" class="min-w-0 rounded border border-line px-2 py-1 text-xs"><button type="button" class="max-w-full break-words text-left" :aria-label="`${text('移出比较', 'Remove from comparison')} ${item.account_name} ${item.model_id}`" @click="toggleCompare(item)">{{ item.account_name }} · {{ item.model_id }} ×</button></li></ul>
            <button v-if="selectedFailures.length" type="button" class="btn btn-secondary btn-sm" @click="retrySelected">{{ text('重试选中失败项（新任务）', 'Retry selected failures (new task)') }} ({{ selectedFailures.length }})</button>
          </section>
          <section v-if="activeTask" class="card space-y-4 p-4 sm:p-5" data-test="pelican-active-task">
            <div class="flex flex-wrap items-start justify-between gap-3"><div class="min-w-0"><h2 class="font-semibold">{{ text('任务结果', 'Task results') }}</h2><p class="mt-1 break-all text-xs text-muted">{{ activeTask.id }} · {{ formatTime(activeTask.created_at) }}</p></div><div class="flex flex-wrap gap-2"><button type="button" class="btn btn-secondary btn-sm" :disabled="copying" @click="copyTask(false)">{{ text('复制参数', 'Copy parameters') }}</button><button type="button" class="btn btn-secondary btn-sm" :disabled="copying || !failedCount" data-test="pelican-retry-failed" @click="copyTask(true)">{{ text('重试全部失败项', 'Retry all failures') }} ({{ failedCount }})</button><button v-if="isRunning" type="button" class="btn btn-secondary btn-sm" :disabled="stopping" data-test="pelican-test-stop" @click="stopTask">{{ text('停止任务', 'Stop task') }}</button></div></div>
            <div class="grid grid-cols-2 gap-3 text-sm sm:grid-cols-4" aria-live="polite"><div>{{ text('已完成', 'Completed') }}<strong class="mt-1 block text-xl">{{ activeTask.counts.complete || 0 }}</strong></div><div>{{ text('失败／中断', 'Failed / interrupted') }}<strong class="mt-1 block text-xl">{{ failedCount }}</strong></div><div>{{ text('取消', 'Cancelled') }}<strong class="mt-1 block text-xl">{{ activeTask.counts.cancelled || 0 }}</strong></div><div>{{ text('等待／进行中', 'Pending / running') }}<strong class="mt-1 block text-xl">{{ (activeTask.counts.pending || 0) + (activeTask.counts.running || 0) }}</strong></div></div>
            <p class="text-xs text-muted">{{ text('已处理', 'Processed') }} {{ activeTask.completed }} / {{ activeTask.total }} · {{ text('完成但不可预览', 'Completed without preview') }} {{ activeTask.counts.no_preview || 0 }} · {{ text('每项生成预算', 'Generation budget per item') }} {{ (activeTask.generation_timeout_seconds || 600) / 60 }} {{ t('admin.pelicanTests.minutes') }}</p>
            <p v-if="isRunning" role="status" class="text-sm" data-test="pelican-observer-state">{{ connection === 'retrying' ? text('观察连接中断，正在重连；后台任务继续。', 'Observation disconnected; reconnecting. The background task continues.') : text('后台任务运行中，离开页面不会取消。', 'Running in the background; leaving this page does not cancel it.') }}</p>
            <p v-if="stopping" role="status" class="text-sm">{{ text('已请求停止，正在保存已有结果。', 'Stop requested; preserving existing results.') }}</p>
            <pre v-if="activeTask.error" class="pelican-error">{{ activeTask.error }}</pre>
            <details class="rounded-lg border border-line p-3"><summary class="cursor-pointer text-sm">{{ t('admin.pelicanTests.viewPrompt') }}</summary><p class="mt-2 whitespace-pre-wrap text-sm">{{ activeTask.prompt || PELICAN_PROMPT }}</p></details>
            <div class="grid gap-3 sm:grid-cols-3"><label class="text-sm">{{ text('按账号 ID 筛选', 'Filter by account ID') }}<input v-model="resultAccount" type="number" min="1" class="input mt-1 w-full" /></label><label class="text-sm">{{ text('按模型筛选', 'Filter by model') }}<input v-model="resultModel" class="input mt-1 w-full" /></label><label class="text-sm">{{ text('结果状态', 'Result status') }}<select v-model="resultStatus" class="input mt-1 w-full" data-test="pelican-result-filter"><option value="">{{ text('全部', 'All') }}</option><option v-for="status in ['pending', 'running', 'complete', 'failed', 'incomplete', 'cancelled', 'skipped', 'no_preview']" :key="status" :value="status">{{ status === 'no_preview' ? text('不可预览', 'No preview') : stateLabel(status) }}</option></select></label></div>
          </section>
          <div v-if="activeTask" class="grid min-w-0 gap-4 xl:grid-cols-2" data-test="pelican-results"><PelicanResultCard v-for="result in results.items" :key="result.id" :result="result" :now="now" :selected="comparison.some(item => item.id === result.id)" :selection-full="comparison.length >= 4" @select="toggleCompare" @open="detailResult = $event" @enlarge="enlarged = $event" /></div>
          <p v-if="activeTask && !results.items.length" class="p-6 text-center text-sm text-muted">{{ text('当前筛选没有结果。', 'No results match these filters.') }}</p>
          <Pagination v-if="activeTask && results.total" v-model:page="resultPage" :total="results.total" :page-size="24" :show-page-size-selector="false" />
          <div v-if="!activeTask" class="card flex min-h-80 flex-col items-center justify-center gap-3 p-8 text-center"><h2 class="font-semibold">{{ text('选择任务查看作品，或新建一组测试', 'Select a task or create a new test') }}</h2><p class="max-w-lg text-sm text-muted">{{ text('查看历史、筛选、放大和比较都只读取结果，不会重新调用模型。', 'History, filters, enlargement and comparison only read saved results; they never generate again.') }}</p></div>
        </section>
      </div>
      <BaseDialog :show="composerOpen" :title="text('新建鹈鹕测试', 'New Pelican test')" width="extra-wide" @close="composerOpen = false"><PelicanTaskComposer v-if="composerOpen" :initial="draft" :submitting="submitting" @submit="startTask" /></BaseDialog>
      <BaseDialog :show="comparisonOpen" :title="text('作品比较', 'Compare works')" width="extra-wide" @close="comparisonOpen = false"><div class="grid gap-4 md:grid-cols-2"><section v-for="item in comparisonDetails" :key="item.id" class="min-w-0 space-y-2"><h3 class="break-words text-sm font-semibold">{{ previewTitle(item) }}</h3><div class="aspect-[4/3] min-h-48 rounded-lg border border-line"><BorrowPelicanPreview v-if="item.preview_url" :preview-url="item.preview_url" :title="previewTitle(item)" interactive /><p v-else class="p-5 text-sm text-muted">{{ item.preview_unavailable || stateLabel(item.status) }}</p></div><button type="button" class="btn btn-secondary btn-sm" @click="detailResult = item">{{ t('admin.pelicanTests.details') }}</button><pre v-if="item.error" class="pelican-error">{{ item.error }}</pre></section></div></BaseDialog>
      <BaseDialog :show="!!detailResult" :title="detailResult ? previewTitle(detailResult) : t('admin.pelicanTests.details')" width="extra-wide" @close="detailResult = null">
        <div v-if="detailResult" class="space-y-4 text-sm" data-test="pelican-detail-content">
          <dl class="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div><dt class="text-xs text-muted">{{ t('admin.pelicanTests.account') }}</dt><dd class="break-words">{{ detailResult.account_name }} #{{ detailResult.account_id }} · {{ detailResult.platform || '—' }}</dd></div>
            <div><dt class="text-xs text-muted">{{ t('admin.pelicanTests.model') }}</dt><dd class="break-words">{{ detailResult.model_id }} · {{ detailResult.effort || t('admin.pelicanTests.normalDefault') }}</dd></div>
            <div><dt class="text-xs text-muted">{{ t('admin.pelicanTests.mappedModel') }}</dt><dd class="break-words">{{ detailResult.upstream_model || '—' }}</dd></div>
            <div><dt class="text-xs text-muted">{{ t('admin.pelicanTests.actualCall') }}</dt><dd class="break-all">{{ detailResult.actual_endpoint || '—' }} · {{ detailResult.actual_protocol || '—' }} · {{ detailResult.actual_transport || '—' }} · {{ detailResult.borrow_applied === undefined ? '—' : t(detailResult.borrow_applied ? 'admin.pelicanTests.borrowApplied' : 'admin.pelicanTests.accountRoute') }}</dd></div>
            <div><dt class="text-xs text-muted">{{ t('admin.pelicanTests.phaseDurations') }}</dt><dd>{{ t('admin.pelicanTests.phases.queued') }} {{ formatDuration(detailResult.queue_duration_ms) }} · {{ t('admin.pelicanTests.phases.preparing') }} {{ formatDuration(detailResult.preparation_duration_ms) }} · {{ t('admin.pelicanTests.phases.generating') }} {{ formatDuration(detailResult.generation_duration_ms) }}</dd></div>
            <div><dt class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.duration') }}</dt><dd>{{ formatDuration(detailResult.duration_ms) }} · {{ formatTime(detailResult.started_at) }} · {{ stateLabel(detailResult.status) }}</dd></div>
          </dl>
          <pre v-if="detailResult.error" class="pelican-error" data-test="pelican-result-error">{{ detailResult.error }}</pre>
          <pre v-if="detailResult.preview_unavailable" class="pelican-error">{{ detailResult.preview_unavailable }}</pre>
          <div class="flex flex-wrap gap-2">
            <button type="button" class="btn btn-secondary btn-sm" :disabled="!detailResult.preview_url" @click="enlarged = detailResult">{{ t('admin.codexGatewayBorrow.enlarge') }}</button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="!detailResult.raw_html && !detailResult.html" @click="downloadHtml(detailResult)">{{ t('admin.pelicanTests.downloadHtml') }}</button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="!detailResult.raw_response" @click="downloadResponse(detailResult)">{{ text('下载响应原文', 'Download raw response') }}</button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="!detailResult.raw_answer" @click="downloadAnswer(detailResult)">{{ t('admin.codexGatewayBorrow.downloadAnswer') }}</button>
          </div>
          <details v-if="detailResult.raw_answer"><summary class="cursor-pointer font-medium">{{ t('admin.codexGatewayBorrow.rawAnswer') }}</summary><pre class="pelican-source mt-2" data-test="pelican-raw-answer">{{ detailResult.raw_answer }}</pre></details>
          <details v-if="detailResult.raw_html || detailResult.html"><summary class="cursor-pointer font-medium">{{ t('admin.codexGatewayBorrow.htmlSource') }}</summary><pre class="pelican-source mt-2" data-test="pelican-raw-html">{{ detailResult.raw_html || detailResult.html }}</pre></details>
          <details v-if="detailResult.raw_response"><summary class="cursor-pointer font-medium">{{ t('admin.codexGatewayBorrow.rawResponse') }}</summary><pre class="pelican-source mt-2" data-test="pelican-raw-response">{{ detailResult.raw_response }}</pre></details>
        </div>
      </BaseDialog>
      <BaseDialog :show="!!enlarged" :title="enlarged ? previewTitle(enlarged) : t('admin.codexGatewayBorrow.enlarge')" width="extra-wide" @close="enlarged = null">
        <div v-if="enlarged?.preview_url" class="aspect-[4/3] max-h-[70vh] w-full" data-test="pelican-enlarged-content"><BorrowPelicanPreview :preview-url="enlarged.preview_url" :title="previewTitle(enlarged)" interactive /></div>
      </BaseDialog>

    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import axios from 'axios'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import Icon from '@/components/icons/Icon.vue'
import BorrowPelicanPreview from '@/components/admin/codex/BorrowPelicanPreview.vue'
import PelicanTaskComposer from '@/components/admin/codex/PelicanTaskComposer.vue'
import PelicanResultCard from '@/components/admin/codex/PelicanResultCard.vue'
import { pelicanTestsAPI, PELICAN_PROMPT, createPelicanClientTaskId, type PelicanTaskSnapshot, type PelicanResultSummary, type PelicanTestResult, type PelicanResultPage, type PelicanTestHistory, type PelicanTestRequest } from '@/api/admin/pelicanTests'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t, locale, te } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const lifecycle = new AbortController()
const activeTask = shallowRef<PelicanTaskSnapshot | null>(null), results = shallowRef<PelicanResultPage>({ items: [], total: 0, page: 1, page_size: 24 })
const history = shallowRef<PelicanTestHistory>({ items: [], total: 0, page: 1, size: 12 }), historyPage = ref(1), historyLoading = ref(false)
const resultPage = ref(1), resultAccount = ref(''), resultModel = ref(''), resultStatus = ref('')
const error = ref(''), connection = ref('idle'), stopping = ref(false), copying = ref(false), submitting = ref(false)
const composerOpen = ref(false), draft = shallowRef<Omit<PelicanTestRequest, 'client_task_id'>>(), mobilePane = ref<'tasks' | 'works'>('works')
const comparison = shallowRef<PelicanResultSummary[]>([]), comparisonDetails = shallowRef<PelicanTestResult[]>([]), comparisonOpen = ref(false), comparisonBusy = ref(false)
const detailResult = shallowRef<PelicanTestResult | null>(null), enlarged = shallowRef<PelicanTestResult | null>(null)
const pendingRequest = shallowRef<PelicanTestRequest | null>(readPending()), serverOffset = ref(0), clockKnown = ref(false), now = ref(Date.now())
let resultQuery = '', resultsLoading = false
let observer: AbortController | undefined, resultsController: AbortController | undefined, selectionSequence = 0, historySequence = 0
let clock: ReturnType<typeof setInterval> | undefined, filterTimer: ReturnType<typeof setTimeout> | undefined
const isRunning = computed(() => !!activeTask.value && Date.parse(activeTask.value.expires_at) > now.value && connection.value !== 'unavailable' && ['pending', 'running'].includes(activeTask.value.status))
const failedCount = computed(() => (activeTask.value?.counts.failed || 0) + (activeTask.value?.counts.incomplete || 0) + (activeTask.value?.counts.skipped || 0))
const selectedFailures = computed(() => comparison.value.filter(item => ['failed', 'incomplete', 'skipped'].includes(item.status)))
function readPending(): PelicanTestRequest | null { try { return JSON.parse(sessionStorage.getItem('pelican.pending-task.v1') || 'null') } catch { return null } }
function storePending(value: PelicanTestRequest | null) { pendingRequest.value = value; if (value) sessionStorage.setItem('pelican.pending-task.v1', JSON.stringify(value)); else sessionStorage.removeItem('pelican.pending-task.v1') }
function formatTime(value?: string) { if (!value || value.startsWith('0001-')) return '—'; return new Date(value).toLocaleString() }
function formatDuration(ms?: number) { return `${((ms || 0) / 1000).toFixed(1)} s` }
function stateLabel(value: string) { const key = `admin.codexGatewayBorrow.states.${value}`; return te(key) ? t(key) : value }
function previewTitle(result: PelicanTestResult) { return `${result.account_name} #${result.account_id} · ${result.model_id} · ${result.effort || t('admin.pelicanTests.normalDefault')}` }
function report(value: unknown) { if (!lifecycle.signal.aborted) error.value = extractApiErrorMessage(value, text('操作未完成，请查看原因后重试。', 'The operation did not complete; review the error and retry.')) }
async function loadHistory() {
  const sequence = ++historySequence; historyLoading.value = true
  try { const value = await pelicanTestsAPI.listTasks(historyPage.value, lifecycle.signal); if (sequence === historySequence && !lifecycle.signal.aborted) history.value = value }
  catch (value) { report(value) }
  finally { if (sequence === historySequence) historyLoading.value = false }
}
async function loadResults() {
  const id = activeTask.value?.id; if (!id) return
  const query = JSON.stringify([id, resultPage.value, resultAccount.value, resultModel.value, resultStatus.value])
  if (resultsLoading && resultQuery === query && !resultsController?.signal.aborted) return
  resultQuery = query; resultsLoading = true
  resultsController?.abort(); const controller = new AbortController(); resultsController = controller
  try {
    const value = await pelicanTestsAPI.getResults(id, { page: resultPage.value, page_size: 24, account_id: Number(resultAccount.value) || undefined, model: resultModel.value.trim() || undefined, status: resultStatus.value || undefined }, controller.signal)
    if (!controller.signal.aborted && activeTask.value?.id === id) results.value = value
  } catch (value) { if (!controller.signal.aborted) report(value) }
  finally { if (resultsController === controller) resultsLoading = false }
}
async function selectTask(id: string) {
  const sequence = ++selectionSequence; observer?.abort(); resultsController?.abort()
  connection.value = 'idle'; activeTask.value = null; resultPage.value = 1; resultAccount.value = ''; resultModel.value = ''; resultStatus.value = ''; stopping.value = false; error.value = ''
  await nextTick()
  try {
    const value = await pelicanTestsAPI.getTask(id, lifecycle.signal)
    if (sequence !== selectionSequence || lifecycle.signal.aborted) return
    activeTask.value = value; sessionStorage.setItem('pelican.active-task.v1', value.id); mobilePane.value = 'works'
    await loadResults()
    if (sequence === selectionSequence && activeTask.value?.id === value.id && isRunning.value) void observe(value.id)
  } catch (value) { if (sequence === selectionSequence) report(value) }
}
async function observe(id: string) {
  observer?.abort(); const controller = new AbortController(); observer = controller
  let delay = 1000
  while (!controller.signal.aborted && activeTask.value?.id === id) {
    connection.value = 'connecting'
    try {
      await pelicanTestsAPI.observeTask(id, event => {
        if (controller.signal.aborted || activeTask.value?.id !== id) return
        connection.value = 'connected'; delay = 1000; activeTask.value = event.task
        void loadResults()
        if (event.type === 'task_complete') { stopping.value = false; void loadHistory() }
      }, controller.signal)
      if (!controller.signal.aborted) connection.value = 'idle'
      return
    } catch {
      if (controller.signal.aborted) return
      connection.value = 'retrying'
      try {
        const snapshot = await pelicanTestsAPI.getTask(id, controller.signal)
        if (controller.signal.aborted || activeTask.value?.id !== id) return
        activeTask.value = snapshot
        if (!['pending', 'running'].includes(snapshot.status)) { connection.value = 'idle'; await loadResults(); await loadHistory(); return }
      } catch (value) {
        if (controller.signal.aborted) return
        if (axios.isAxiosError(value) && [401, 403, 404, 410].includes(value.response?.status || 0)) { connection.value = 'unavailable'; report(value); return }
      }
      await new Promise<void>(resolve => {
        const finish = () => { clearTimeout(timer); controller.signal.removeEventListener('abort', finish); resolve() }
        const timer = setTimeout(finish, delay); controller.signal.addEventListener('abort', finish, { once: true })
      })
      delay = Math.min(delay * 2, 30000)
    }
  }
}
function newTest() { draft.value = undefined; composerOpen.value = true; error.value = '' }
async function startTask(parameters: Omit<PelicanTestRequest, 'client_task_id'>) {
  if (!clockKnown.value) { error.value = t('admin.codexGatewayBorrow.clockUnavailable'); return }
  await submitRequest({ ...parameters, client_task_id: createPelicanClientTaskId(serverOffset.value) })
}
async function submitRequest(request: PelicanTestRequest) {
  if (submitting.value) return
  submitting.value = true; error.value = ''; storePending(request)
  try {
    const value = await pelicanTestsAPI.startTask(request, lifecycle.signal)
    if (lifecycle.signal.aborted) return
    storePending(null); composerOpen.value = false; historyPage.value = 1
    await Promise.all([selectTask(value.id), loadHistory()])
  } catch (value) {
    if (axios.isAxiosError(value) && value.response && value.response.status >= 400 && value.response.status < 500) storePending(null)
    report(value)
  } finally { submitting.value = false }
}
async function recoverPending() {
  if (!pendingRequest.value || submitting.value) return
  try { const task = await pelicanTestsAPI.getTask(pendingRequest.value.client_task_id, lifecycle.signal); storePending(null); await selectTask(task.id); await loadHistory() }
  catch (value) { report(value) }
}
async function stopTask() {
  if (!activeTask.value || stopping.value) return
  stopping.value = true
  try { await pelicanTestsAPI.cancelTask(activeTask.value.id) } catch (value) { stopping.value = false; report(value) }
}
function toggleCompare(item: PelicanResultSummary) { if (comparison.value.some(row => row.id === item.id)) comparison.value = comparison.value.filter(row => row.id !== item.id); else if (comparison.value.length < 4) comparison.value = [...comparison.value, item] }
async function openComparison() {
  comparisonBusy.value = true
  try { comparisonDetails.value = await Promise.all(comparison.value.map(item => pelicanTestsAPI.getResult(item.task_id, item.id, lifecycle.signal))); comparisonOpen.value = true }
  catch (value) { report(value) } finally { comparisonBusy.value = false }
}
function retrySelected() { draft.value = { generation_timeout_seconds: activeTask.value?.generation_timeout_seconds || 600, targets: selectedFailures.value.map(item => ({ account_id: item.account_id, model_id: item.model_id, effort: item.effort })) }; composerOpen.value = true }
async function copyTask(failedOnly: boolean) {
  const task = activeTask.value; if (!task) return
  copying.value = true
  try {
    const items: PelicanResultSummary[] = []
    for (let page = 1; !lifecycle.signal.aborted; page++) {
      const result = await pelicanTestsAPI.getResults(task.id, { page, page_size: 50 }, lifecycle.signal)
      items.push(...result.items.filter(item => !failedOnly || ['failed', 'incomplete', 'skipped'].includes(item.status)))
      if (!result.items.length || page * 50 >= result.total) break
    }
    if (!items.length) return
    draft.value = { generation_timeout_seconds: task.generation_timeout_seconds || 600, targets: items.map(item => ({ account_id: item.account_id, model_id: item.model_id, effort: item.effort })) }; composerOpen.value = true
  } catch (value) { report(value) } finally { copying.value = false }
}
function download(content: string, filename: string, type: string) { const url = URL.createObjectURL(new Blob([content], { type })); const link = document.createElement('a'); link.href = url; link.download = filename; link.click(); setTimeout(() => URL.revokeObjectURL(url), 0) }
function downloadHtml(result: PelicanTestResult) { download(result.raw_html || result.html, `${result.id}.html`, 'text/html;charset=utf-8') }
function downloadAnswer(result: PelicanTestResult) { download(result.raw_answer, `${result.id}-answer.txt`, 'text/plain;charset=utf-8') }
function downloadResponse(result: PelicanTestResult) { download(result.raw_response, `${result.id}-response.txt`, 'text/plain;charset=utf-8') }
watch(resultPage, () => { void loadResults() })
watch([resultAccount, resultModel, resultStatus], () => { if (filterTimer) clearTimeout(filterTimer); filterTimer = setTimeout(() => { if (resultPage.value !== 1) resultPage.value = 1; else void loadResults() }, 250) })
onMounted(() => {
  void loadHistory()
  void pelicanTestsAPI.getOptions(lifecycle.signal).then(options => { serverOffset.value = Date.parse(options.generated_at) - Date.now(); clockKnown.value = Number.isFinite(serverOffset.value) }).catch(report)
  clock = setInterval(() => { now.value = Date.now() + serverOffset.value }, 1000)
  if (pendingRequest.value) void recoverPending()
  else { const id = sessionStorage.getItem('pelican.active-task.v1'); if (id) void selectTask(id) }
})
onBeforeUnmount(() => { lifecycle.abort(); observer?.abort(); resultsController?.abort(); if (clock) clearInterval(clock); if (filterTimer) clearTimeout(filterTimer) })
</script>

<style scoped>
.pelican-error, .pelican-source { white-space: pre-wrap; overflow-wrap: anywhere; word-break: break-word; font-size: .75rem; line-height: 1.65; }
.pelican-error { @apply rounded-lg border border-red-200 bg-red-50 p-3 text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-200; }
.pelican-source { @apply max-h-[50vh] overflow-auto rounded-lg border border-line p-3; }
</style>
