<template>
  <AppLayout>
    <div class="mx-auto w-full max-w-[1920px] space-y-5" data-ui="pelican-tests">
      <header class="flex flex-wrap items-center justify-between gap-3 border-b border-line pb-4">
        <div><p class="text-[10px] tracking-[.22em] text-muted">PELICAN WATCH / MODEL OBSERVATORY</p><h1 class="mt-2 text-2xl font-semibold">{{ text('鹈鹕测试', 'Pelican tests') }}</h1><p class="mt-2 text-sm text-muted">{{ text('让每一次输出，都有迹可循。', 'Every output, an observation to keep.') }}</p></div>
        <div class="flex gap-2"><button type="button" class="btn btn-secondary" :disabled="loading" data-test="pelican-refresh" @click="refresh">{{ text('刷新记录', 'Refresh') }}</button><button type="button" class="btn btn-primary" :disabled="busy" data-test="pelican-new" @click="draft = undefined; composerOpen = true">{{ text('新建测试', 'New test') }}</button></div>
      </header>
      <pre v-if="error" role="alert" class="pelican-error">{{ error }}</pre>
      <div v-if="pending" class="space-y-2 rounded-lg border border-amber-300 p-3 text-sm" role="status"><p>{{ text('上次提交尚未确认，可查询任务或重试同一提交。', 'The last submission is unconfirmed. Check it or retry the same submission.') }}</p><div class="flex gap-2"><button type="button" class="btn btn-secondary btn-sm" :disabled="submitting" @click="recoverPending">{{ text('查询任务', 'Check task') }}</button><button type="button" class="btn btn-secondary btn-sm" :disabled="submitting" @click="sendPending">{{ text('重试同一提交', 'Retry same submission') }}</button></div></div>
      <section class="space-y-3 rounded-xl border border-line bg-white p-4 dark:bg-dark-900" :aria-label="text('画廊筛选', 'Gallery filters')">
        <div class="flex flex-wrap items-end gap-3">
          <label class="min-w-52 flex-1 text-xs text-muted">{{ text('任务', 'Task') }}<select v-model="taskId" class="input mt-1 w-full" data-test="pelican-task-select" @change="selectTask"><option value="">{{ text('最近 24 小时全部作品', 'All works from the last 24 hours') }}</option><option v-for="task in history.items" :key="task.id" :value="task.id">{{ formatTime(task.created_at) }} · {{ label(task.status) }} · {{ task.total }}</option></select></label>
          <div class="flex items-center gap-2 text-xs"><button type="button" class="btn btn-ghost btn-sm" :disabled="historyPage <= 1" @click="historyPage--; loadHistory()">←</button><span>{{ text('任务页', 'Task page') }} {{ historyPage }}</span><button type="button" class="btn btn-ghost btn-sm" :disabled="historyPage * 12 >= history.total" @click="historyPage++; loadHistory()">→</button></div>
        </div>
        <div class="grid grid-cols-2 gap-3 xl:grid-cols-6">
          <label class="text-xs text-muted">{{ text('账号 ID', 'Account ID') }}<input v-model="filters.account" type="number" min="1" class="input mt-1 w-full" /></label>
          <label class="text-xs text-muted">{{ text('模型', 'Model') }}<input v-model="filters.model" class="input mt-1 w-full" /></label>
          <label class="text-xs text-muted">{{ text('思考强度', 'Reasoning') }}<select v-model="filters.effort" class="input mt-1 w-full"><option value="">{{ text('全部', 'All') }}</option><option v-for="effort in efforts" :key="effort" :value="effort">{{ effort }}</option></select></label>
          <label class="text-xs text-muted">{{ text('场景', 'Scene') }}<select v-model="filters.scene" class="input mt-1 w-full"><option value="">{{ text('全部场景', 'All scenes') }}</option><option v-for="scene in PELICAN_SCENES" :key="scene">{{ scene }}</option></select></label>
          <label class="text-xs text-muted">{{ text('检测判定', 'Verdict') }}<select v-model="filters.verdict" class="input mt-1 w-full" data-test="pelican-verdict-filter"><option value="">{{ text('全部', 'All') }}</option><option v-for="state in ['passed', 'invalid', 'error', 'uncertain', 'not_reviewed']" :key="state" :value="state">{{ label(state) }}</option></select></label>
          <label class="text-xs text-muted">{{ text('执行状态', 'Execution') }}<select v-model="filters.status" class="input mt-1 w-full" data-test="pelican-result-filter"><option value="">{{ text('全部', 'All') }}</option><option v-for="state in ['pending', 'running', 'complete', 'failed', 'incomplete', 'cancelled', 'skipped', 'no_preview']" :key="state" :value="state">{{ state === 'no_preview' ? text('无法预览', 'No preview') : label(state) }}</option></select></label>
        </div>
      </section>
      <section v-if="activeTask" class="space-y-3 rounded-xl border border-line p-4" data-test="pelican-active-task">
        <div class="flex flex-wrap items-center justify-between gap-3"><div><strong>{{ label(activeTask.status) }} · {{ text('已处理', 'Processed') }} {{ activeTask.completed }} / {{ activeTask.total }}</strong><p class="mt-1 break-all text-xs text-muted">{{ activeTask.id }}</p></div><div class="flex flex-wrap gap-2"><button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="copyTask">{{ text('复制参数', 'Copy parameters') }}</button><button type="button" class="btn btn-secondary btn-sm" :disabled="busy || unfinished" data-test="pelican-retest-task" @click="retestTask(false)">{{ text('整批重测', 'Retest task') }}</button><button type="button" class="btn btn-secondary btn-sm" :disabled="busy || unfinished || !failedCount" data-test="pelican-retry-failed" @click="retestTask(true)">{{ text('重试失败项', 'Retry failures') }} ({{ failedCount }})</button><button v-if="unfinished" type="button" class="btn btn-secondary btn-sm" :disabled="stopping" data-test="pelican-test-stop" @click="stopTask">{{ text('停止任务', 'Stop task') }}</button><button type="button" class="btn btn-ghost btn-sm text-red-700 dark:text-red-300" :disabled="busy || unfinished" data-test="pelican-delete-task" @click="deleteTarget = { taskId: activeTask.id }">{{ text('删除任务', 'Delete task') }}</button></div></div>
        <p v-if="unfinished" role="status" class="text-sm text-muted" data-test="pelican-observer-state">{{ connection === 'retrying' ? text('观察连接中断，正在重连；后台任务继续。', 'Reconnecting; the background task continues.') : text('后台任务运行中，离开页面不会取消。', 'Running in the background; leaving this page does not cancel it.') }}</p>
        <pre v-if="activeTask.error" class="pelican-error">{{ activeTask.error }}</pre>
      </section>
      <section v-if="comparison.length" class="sticky top-2 z-10 flex flex-wrap items-center gap-3 rounded-xl border border-line bg-white/95 p-3 shadow-sm dark:bg-dark-900/95" data-test="pelican-comparison-tray"><strong class="text-sm">{{ text('跨任务比较', 'Compare across tasks') }} {{ comparison.length }} / 4</strong><button v-for="item in comparison" :key="item.id" type="button" class="max-w-48 truncate rounded border border-line px-2 py-1 text-xs" :aria-label="`${text('移出比较', 'Remove from comparison')} ${item.account_name}`" @click="toggleCompare(item)">{{ item.account_name }} · {{ item.model_id }} ×</button><button type="button" class="btn btn-primary btn-sm" :disabled="comparison.length < 2 || detailLoading" data-test="pelican-compare" @click="openComparison">{{ text('并排查看', 'View side by side') }}</button><button v-if="selectedFailures.length" type="button" class="btn btn-secondary btn-sm" :disabled="busy" data-test="pelican-retry-selected" @click="retestItems(selectedFailures)">{{ text('重测选中失败项', 'Retest selected failures') }}</button></section>
      <section :aria-busy="loading" class="space-y-4">
        <div class="flex items-center justify-between"><h2 class="text-base font-semibold">{{ text('鹈鹕骑行画廊', 'Pelican riding gallery') }}</h2><span class="text-xs text-muted">{{ results.total }} {{ text('条记录', 'observations') }} · {{ loading ? text('读取中…', 'Loading…') : text('每页 24 条', '24 per page') }}</span></div>
        <div class="grid min-w-0 grid-cols-1 gap-5 md:grid-cols-2 2xl:grid-cols-4" data-test="pelican-results"><PelicanResultCard v-for="result in results.items" :key="result.id" :result="result" :now="now" :selected="comparison.some(item => item.id === result.id)" :selection-full="comparison.length >= 4" :can-delete="!(unfinished && result.task_id === activeTask?.id)" :busy="busy" @select="toggleCompare" @open="openDetail" @enlarge="openDetail" @retest="item => retestItems([item])" @delete="item => deleteTarget = { taskId: item.task_id, result: item }" /></div>
        <p v-if="!loading && !results.items.length" class="rounded-xl border border-dashed border-line p-14 text-center text-sm text-muted">{{ text('还没有符合条件的检测记录。发起一次测试，让第一只鹈鹕出发。', 'No matching observations. Start a test to send the first pelican on its way.') }}</p>
        <Pagination v-if="results.total" v-model:page="page" :total="results.total" :page-size="24" :show-page-size-selector="false" />
      </section>
      <p class="text-xs leading-relaxed text-muted">{{ text('每次检测都是一份独立观察。视觉审核可能误判，结果不证明上游模型身份。', 'Each test is an independent observation. Visual review can be mistaken; results do not prove model identity.') }}</p>
      <BaseDialog :show="composerOpen" :title="text('新建鹈鹕测试', 'New Pelican test')" width="extra-wide" @close="composerOpen = false"><PelicanTaskComposer v-if="composerOpen" :initial="draft" :submitting="submitting" @submit="startTask" /></BaseDialog>
      <BaseDialog :show="!!detail || detailLoading && !comparisonOpen" :title="text('检测详情', 'Observation details')" width="extra-wide" @close="closeDetail"><PelicanWorkDetail v-if="detail" :result="detail" /><p v-else class="p-6 text-muted">{{ text('读取详情…', 'Loading details…') }}</p></BaseDialog>
      <BaseDialog :show="comparisonOpen" :title="text('作品比较', 'Compare works')" width="extra-wide" @close="comparisonOpen = false"><div class="grid gap-5 md:grid-cols-2"><section v-for="item in comparisonDetails" :key="item.id" class="min-w-0"><PelicanWorkDetail :result="item" /></section></div></BaseDialog>
      <BaseDialog :show="!!deleteTarget" :title="text('删除检测记录', 'Delete observation')" width="narrow" @close="!deleting && (deleteTarget = null)"><p data-test="pelican-delete-message">{{ deleteTarget?.result ? text('删除这张作品及其检测资料？', 'Delete this work and its observation data?') : text('删除整个任务及全部作品？', 'Delete this task and all its works?') }}</p><template #footer><div class="flex justify-end gap-2"><button type="button" class="btn btn-secondary" :disabled="deleting" @click="deleteTarget = null">{{ text('取消', 'Cancel') }}</button><button type="button" class="btn bg-red-600 text-white" :disabled="deleting" data-test="pelican-confirm-delete" @click="confirmDelete">{{ text('删除', 'Delete') }}</button></div></template></BaseDialog>
    </div>
  </AppLayout>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import PelicanTaskComposer from '@/components/admin/codex/PelicanTaskComposer.vue'
import PelicanResultCard from '@/components/admin/codex/PelicanResultCard.vue'
import PelicanWorkDetail from '@/components/admin/codex/PelicanWorkDetail.vue'
import { pelicanTestsAPI, PELICAN_SCENES, createPelicanClientTaskId, type PelicanTaskSnapshot, type PelicanTestRequest, type PelicanRetestRequest, type PelicanResultSummary, type PelicanResultPage, type PelicanTestResult, type PelicanTestHistory } from '@/api/admin/pelicanTests'
import { pelicanFailed, pelicanLabel } from '@/utils/pelicanDisplay'
import { extractApiErrorMessage } from '@/utils/apiError'
const { locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const label = (value: string) => pelicanLabel(value, locale.value)
const formatTime = (value: string) => new Date(value).toLocaleString()
const lifecycle = new AbortController()
const efforts = ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max', 'ultra']
const filters = reactive({ account: '', model: '', effort: '', scene: '', verdict: '', status: '' })
const results = shallowRef<PelicanResultPage>({ items: [], total: 0, page: 1, page_size: 24 })
const history = shallowRef<PelicanTestHistory>({ items: [], total: 0, page: 1, size: 12 })
const activeTask = shallowRef<PelicanTaskSnapshot | null>(null)
const taskId = ref(''), page = ref(1), historyPage = ref(1), error = ref(''), loading = ref(false), connection = ref('idle'), stopping = ref(false)
const composerOpen = ref(false), draft = shallowRef<Omit<PelicanTestRequest, 'client_task_id'>>()
const submitting = ref(false), copying = ref(false), deleting = ref(false), detailLoading = ref(false), now = ref(Date.now()), serverOffset = ref(0), clockKnown = ref(false)
const detail = shallowRef<PelicanTestResult | null>(null), deleteTarget = shallowRef<{ taskId: string; result?: PelicanResultSummary } | null>(null)
const comparison = shallowRef<PelicanResultSummary[]>([]), comparisonDetails = shallowRef<PelicanTestResult[]>([]), comparisonOpen = ref(false)
type Pending = { kind: 'task'; request: PelicanTestRequest } | { kind: 'retest'; taskId: string; request: PelicanRetestRequest }
function readPending(): Pending | null { try { return JSON.parse(sessionStorage.getItem('pelican.pending.v2') || 'null') } catch { return null } }
const pending = shallowRef<Pending | null>(readPending())
function storePending(value: Pending | null) { pending.value = value; if (value) sessionStorage.setItem('pelican.pending.v2', JSON.stringify(value)); else sessionStorage.removeItem('pelican.pending.v2') }
const busy = computed(() => submitting.value || copying.value || deleting.value || !!pending.value)
const unfinished = computed(() => !!activeTask.value && ['pending', 'running'].includes(activeTask.value.status))
const failedCount = computed(() => activeTask.value?.failed_count ?? (activeTask.value?.counts.failed || 0) + (activeTask.value?.counts.incomplete || 0) + (activeTask.value?.counts.skipped || 0))
const selectedFailures = computed(() => comparison.value.filter(pelicanFailed))
let galleryController: AbortController | undefined, observer: AbortController | undefined, clock: ReturnType<typeof setInterval> | undefined, debounce: ReturnType<typeof setTimeout> | undefined
let selection = 0, content = 0
function apiStatus(value: unknown) { return value && typeof value === 'object' && 'status' in value ? Number(value.status) : 0 }
function report(value: unknown) { if (!lifecycle.signal.aborted) error.value = extractApiErrorMessage(value, text('操作失败，请查看原因。', 'Operation failed; review the error.')) }
async function loadHistory() { try { history.value = await pelicanTestsAPI.listTasks(historyPage.value, lifecycle.signal) } catch (value) { report(value) } }
async function loadGallery() {
  galleryController?.abort(); const controller = new AbortController(); galleryController = controller; loading.value = true
  try {
    const value = await pelicanTestsAPI.gallery({ page: page.value, page_size: 24, task_id: taskId.value || undefined, account_id: Number(filters.account) || undefined, model: filters.model.trim() || undefined, effort: filters.effort || undefined, scene: filters.scene || undefined, verdict: filters.verdict || undefined, status: filters.status || undefined }, controller.signal)
    if (!controller.signal.aborted && !lifecycle.signal.aborted) results.value = value
  } catch (value) { if (!controller.signal.aborted) report(value) }
  finally { if (galleryController === controller) loading.value = false }
}
async function refresh() { error.value = ''; await Promise.all([loadHistory(), loadGallery()]) }
async function selectTask() {
  const version = ++selection; observer?.abort(); activeTask.value = null; connection.value = 'idle'; stopping.value = false; page.value = 1
  if (taskId.value) {
    try { const value = await pelicanTestsAPI.getTask(taskId.value, lifecycle.signal); if (version !== selection || lifecycle.signal.aborted) return; activeTask.value = value; if (unfinished.value) void observe(value.id) }
    catch (value) { report(value) }
  }
  await loadGallery()
}
async function observe(id: string) {
  observer?.abort(); const controller = new AbortController(); observer = controller
  let delay = 1000
  while (!controller.signal.aborted && taskId.value === id) {
    try {
      await pelicanTestsAPI.observeTask(id, event => { if (controller.signal.aborted) return; activeTask.value = event.task; connection.value = 'connected'; delay = 1000; void loadGallery(); if (event.type === 'task_complete') { stopping.value = false; void loadHistory() } }, controller.signal)
      return
    } catch {
      if (controller.signal.aborted) return
      connection.value = 'retrying'
      try { const task = await pelicanTestsAPI.getTask(id, controller.signal); if (controller.signal.aborted) return; activeTask.value = task; if (!['pending', 'running'].includes(task.status)) { await refresh(); return } }
      catch (value) { if ([401, 403, 404, 410].includes(apiStatus(value))) { report(value); return } }
      await new Promise<void>(resolve => { const finish = () => { clearTimeout(timer); controller.signal.removeEventListener('abort', finish); resolve() }; const timer = setTimeout(finish, delay); controller.signal.addEventListener('abort', finish, { once: true }) })
      delay = Math.min(delay * 2, 30000)
    }
  }
}
function clientId() { if (!clockKnown.value) throw new Error(text('服务器时间尚未读取，请稍后重试。', 'Server time is not available yet.')); return createPelicanClientTaskId(serverOffset.value) }
async function accepted(task: PelicanTaskSnapshot) { storePending(null); composerOpen.value = false; taskId.value = task.id; historyPage.value = 1; await Promise.all([selectTask(), loadHistory()]) }
async function sendPending() {
  const submission = pending.value; if (!submission || submitting.value) return false
  submitting.value = true; error.value = ''
  try { const task = submission.kind === 'task' ? await pelicanTestsAPI.startTask(submission.request, lifecycle.signal) : await pelicanTestsAPI.retestTask(submission.taskId, submission.request, lifecycle.signal); if (!lifecycle.signal.aborted) await accepted(task); return true }
  catch (value) { if (apiStatus(value) >= 400 && apiStatus(value) < 500) storePending(null); report(value); return false }
  finally { submitting.value = false }
}
async function startTask(parameters: Omit<PelicanTestRequest, 'client_task_id'>) { if (busy.value) return; try { storePending({ kind: 'task', request: { ...parameters, client_task_id: clientId() } }); await sendPending() } catch (value) { report(value) } }
async function recoverPending() { if (!pending.value || submitting.value) return; try { await accepted(await pelicanTestsAPI.getTask(pending.value.request.client_task_id, lifecycle.signal)) } catch (value) { if (apiStatus(value) === 410) storePending(null); report(value) } }
async function retestTask(failedOnly: boolean) { if (!activeTask.value || busy.value || unfinished.value) return; try { storePending({ kind: 'retest', taskId: activeTask.value.id, request: { client_task_id: clientId(), failed_only: failedOnly } }); await sendPending() } catch (value) { report(value) } }
async function retestItems(items: PelicanResultSummary[]) {
  if (busy.value || !items.length) return
  copying.value = true
  try { const groups = new Map<string, string[]>(); for (const item of items) groups.set(item.task_id, [...(groups.get(item.task_id) || []), item.id]); for (const [id, resultIDs] of groups) { storePending({ kind: 'retest', taskId: id, request: { client_task_id: clientId(), result_ids: resultIDs } }); if (!await sendPending()) break } }
  catch (value) { report(value) } finally { copying.value = false }
}
async function copyTask() {
  const task = activeTask.value; if (!task || busy.value) return; copying.value = true
  try { const items: PelicanResultSummary[] = []; for (let next = 1; ; next++) { const rows = await pelicanTestsAPI.getResults(task.id, { page: next, page_size: 50 }, lifecycle.signal); items.push(...rows.items); if (next * 50 >= rows.total) break } draft.value = { generation_timeout_seconds: task.generation_timeout_seconds || 600, test_items: task.test_config?.test_items?.length ? task.test_config.test_items : ['pelican'], scene: task.test_config?.scene || '', targets: items.map(item => ({ account_id: item.account_id, model_id: item.model_id, effort: item.effort, scene: item.evaluation?.scene })) }; composerOpen.value = true }
  catch (value) { report(value) } finally { copying.value = false }
}
async function stopTask() { if (!activeTask.value || stopping.value) return; stopping.value = true; try { activeTask.value = await pelicanTestsAPI.cancelTask(activeTask.value.id) } catch (value) { stopping.value = false; report(value) } }
function toggleCompare(item: PelicanResultSummary) { if (comparison.value.some(row => row.id === item.id)) comparison.value = comparison.value.filter(row => row.id !== item.id); else if (comparison.value.length < 4) comparison.value = [...comparison.value, item] }
function closeDetail() { content++; detail.value = null; detailLoading.value = false }
async function openDetail(item: PelicanResultSummary) { const version = ++content; detailLoading.value = true; try { const value = await pelicanTestsAPI.getResult(item.task_id, item.id, lifecycle.signal); if (version === content && !lifecycle.signal.aborted) detail.value = value } catch (value) { report(value) } finally { if (version === content) detailLoading.value = false } }
async function openComparison() { const version = ++content; comparisonOpen.value = true; detailLoading.value = true; try { const values = await Promise.all(comparison.value.map(item => pelicanTestsAPI.getResult(item.task_id, item.id, lifecycle.signal))); if (version === content && !lifecycle.signal.aborted) comparisonDetails.value = values } catch (value) { report(value) } finally { detailLoading.value = false } }
async function confirmDelete() {
  const target = deleteTarget.value; if (!target || deleting.value) return; deleting.value = true
  try { if (target.result) await pelicanTestsAPI.deleteResult(target.taskId, target.result.id); else await pelicanTestsAPI.deleteTask(target.taskId)
    content++; const affected = (item: { id: string; task_id?: string }) => target.result ? item.id === target.result.id : item.task_id === target.taskId
    comparison.value = comparison.value.filter(item => !affected(item)); comparisonDetails.value = comparisonDetails.value.filter(item => !affected(item)); if (detail.value && affected(detail.value)) detail.value = null; if (comparisonDetails.value.length < 2) comparisonOpen.value = false
    if (taskId.value === target.taskId) { try { activeTask.value = await pelicanTestsAPI.getTask(target.taskId, lifecycle.signal) } catch (value) { if ([404, 410].includes(apiStatus(value))) { observer?.abort(); taskId.value = ''; activeTask.value = null } else throw value } }
    deleteTarget.value = null; await refresh(); const last = Math.max(1, Math.ceil(results.value.total / 24)); if (page.value > last) page.value = last
  } catch (value) { report(value) } finally { deleting.value = false }
}
watch(page, () => { void loadGallery() })
watch(filters, () => { if (debounce) clearTimeout(debounce); debounce = setTimeout(() => { if (page.value !== 1) page.value = 1; else void loadGallery() }, 250) })
onMounted(() => { void refresh(); void pelicanTestsAPI.getOptions(lifecycle.signal).then(options => { serverOffset.value = Date.parse(options.generated_at) - Date.now(); clockKnown.value = Number.isFinite(serverOffset.value) }).catch(report); clock = setInterval(() => { now.value = Date.now() + serverOffset.value }, 1000); if (pending.value) void recoverPending() })
onBeforeUnmount(() => { lifecycle.abort(); galleryController?.abort(); observer?.abort(); if (clock) clearInterval(clock); if (debounce) clearTimeout(debounce) })
</script>
<style scoped>
.pelican-error { white-space: pre-wrap; overflow-wrap: anywhere; font-size: .75rem; line-height: 1.7; @apply rounded-lg border border-red-200 bg-red-50 p-3 text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-200; }
</style>
