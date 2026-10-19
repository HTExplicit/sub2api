<template>
  <AppLayout>
    <div class="mx-auto w-full max-w-[1920px] space-y-5 pb-5" data-ui="pelican-tests">
      <header class="flex flex-wrap items-end justify-between gap-4">
        <div><p class="text-xs font-medium text-primary-700 dark:text-primary-300">{{ text('模型观察室', 'MODEL OBSERVATORY') }}</p><h1 class="mt-1 text-2xl font-semibold tracking-tight">{{ composerOpen ? text('新建测试', 'New test') : text('鹈鹕测试', 'Pelican tests') }}</h1><p class="mt-2 text-sm text-muted">{{ composerOpen ? text('选择账号，配置模型，一次启动。', 'Choose accounts, configure models, and start.') : text('看作品，比较表现，追踪每一次测试。', 'Explore works, compare results, and follow every test.') }}</p></div>
        <div class="flex gap-2"><button v-if="composerOpen" type="button" class="btn btn-secondary" :disabled="submitting" @click="composerOpen = false">{{ text('返回作品', 'Back to works') }}</button><template v-else><button type="button" class="btn btn-secondary" :disabled="loading" data-test="pelican-refresh" @click="refresh">{{ text('刷新', 'Refresh') }}</button><button type="button" class="btn btn-primary" :disabled="busy" data-test="pelican-new" @click="openComposer">{{ text('新建测试', 'New test') }}</button></template></div>
      </header>
      <pre v-if="error" role="alert" class="pelican-error">{{ error }}</pre>
      <div v-if="pending" class="space-y-2 rounded-xl border border-amber-300 bg-amber-50 p-4 text-sm dark:border-amber-800 dark:bg-amber-950" role="status"><p>{{ text('上次提交尚未确认，可查询任务或重试同一提交。', 'The last submission is unconfirmed. Check it or retry the same submission.') }}</p><div class="flex flex-wrap gap-2"><button type="button" class="btn btn-secondary btn-sm" :disabled="submitting" @click="recoverPending">{{ text('查询任务', 'Check task') }}</button><button type="button" class="btn btn-secondary btn-sm" :disabled="submitting" @click="sendPending">{{ text('重试同一提交', 'Retry same submission') }}</button></div></div>
      <template v-if="composerOpen">
        <p v-if="draftWarning" role="status" class="text-sm text-amber-700 dark:text-amber-300">{{ draftWarning }}</p>
        <PelicanTaskComposer :initial="draft" :submitting="submitting || !!pending" @submit="startTask" @draft="saveDraft" @clear="saveDraft()" />
      </template>
      <template v-else>
        <nav class="flex items-center justify-between gap-3 border-b border-line" :aria-label="text('鹈鹕工作台视图', 'Pelican workbench views')">
          <div class="flex gap-5"><button type="button" class="border-b-2 px-1 py-3 text-sm font-medium" :class="view === 'gallery' ? 'border-primary-600 text-primary-700 dark:text-primary-300' : 'border-transparent text-muted'" :aria-current="view === 'gallery' ? 'page' : undefined" @click="view = 'gallery'">{{ text('作品', 'Works') }}</button><button type="button" class="border-b-2 px-1 py-3 text-sm font-medium" :class="view === 'tasks' ? 'border-primary-600 text-primary-700 dark:text-primary-300' : 'border-transparent text-muted'" :aria-current="view === 'tasks' ? 'page' : undefined" data-test="pelican-tasks-tab" @click="view = 'tasks'">{{ text('任务', 'Tasks') }} <span class="ml-1 text-xs tabular-nums">{{ history.total }}</span></button></div>
          <span class="text-xs text-muted">{{ text('最近 24 小时', 'Last 24 hours') }}</span>
        </nav>
        <section v-if="view === 'tasks'" class="space-y-3" :aria-label="text('测试任务', 'Test tasks')">
          <button v-for="task in history.items" :key="task.id" type="button" class="group flex w-full flex-wrap items-center gap-4 rounded-xl border border-line bg-white p-5 text-left transition-colors hover:border-primary-400 dark:bg-dark-900" :data-test="`pelican-task-${task.id}`" @click="chooseTask(task.id)">
            <div class="min-w-0 flex-1"><div class="flex flex-wrap items-center gap-3"><span class="font-medium">{{ formatTime(task.created_at) }}</span><span class="rounded px-2 py-1 text-xs" :class="pelicanTone(task.status)">{{ label(task.status) }}</span></div><p class="mt-2 truncate text-xs text-muted">{{ task.id }}</p></div>
            <div class="w-40 max-w-full"><p class="mb-2 text-xs text-muted">{{ text('已处理', 'Processed') }} {{ task.completed }} / {{ task.total }}</p><div class="h-1.5 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-800"><div class="h-full rounded-full bg-primary-600" :style="{ width: `${task.total ? Math.min(100, task.completed / task.total * 100) : 0}%` }" /></div></div><span class="text-sm text-primary-700 dark:text-primary-300">{{ text('查看作品 →', 'View works →') }}</span>
          </button>
          <div v-if="!history.items.length" class="rounded-xl border border-dashed border-line p-10 text-center text-sm text-muted">{{ text('暂无测试任务。新建测试后，进度会显示在这里。', 'No tasks yet. Start a test to follow its progress here.') }}</div>
          <Pagination v-if="history.total" v-model:page="historyPage" :total="history.total" :page-size="12" :show-page-size-selector="false" @update:page="loadHistory" />
        </section>
        <template v-else>
          <section v-if="activeTask" class="space-y-4 rounded-xl border border-primary-200 bg-primary-50 p-4 dark:border-primary-800 dark:bg-primary-950" data-test="pelican-active-task">
            <div class="flex flex-wrap items-center justify-between gap-3"><div><div class="flex items-center gap-3"><strong>{{ label(activeTask.status) }}</strong><span class="text-sm tabular-nums">{{ text('已处理', 'Processed') }} {{ activeTask.completed }} / {{ activeTask.total }} · {{ progress }}%</span></div><p class="mt-1 text-xs text-muted">{{ formatTime(activeTask.created_at) }}</p></div><button type="button" class="btn btn-ghost btn-sm" @click="chooseTask('')">{{ text('查看全部作品', 'All works') }}</button></div>
            <div role="progressbar" :aria-label="text('任务进度', 'Task progress')" :aria-valuenow="progress" :aria-valuemin="0" :aria-valuemax="100" class="h-2 overflow-hidden rounded-full bg-white dark:bg-dark-800"><div class="h-full rounded-full bg-primary-600 transition-[width] motion-reduce:transition-none" :style="{ width: `${progress}%` }" /></div>
            <div class="flex flex-wrap items-center gap-2"><button v-if="unfinished" type="button" class="btn btn-secondary btn-sm" :disabled="stopping" data-test="pelican-test-stop" @click="stopTask">{{ stopping ? text('正在停止…', 'Stopping…') : text('停止任务', 'Stop task') }}</button><button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="copyTask">{{ text('复制参数', 'Copy parameters') }}</button><button type="button" class="btn btn-secondary btn-sm" :disabled="busy || unfinished || !failedCount" data-test="pelican-retry-failed" @click="retestTask(true)">{{ text('重测失败项', 'Retry failures') }} ({{ failedCount }})</button><button type="button" class="btn btn-ghost btn-sm" :disabled="busy || unfinished" data-test="pelican-retest-task" @click="retestTask(false)">{{ text('整批重测', 'Retest task') }}</button><button type="button" class="btn btn-ghost btn-sm text-red-700 dark:text-red-300" :disabled="busy || unfinished" data-test="pelican-delete-task" @click="deleteTarget = { taskId: activeTask.id }">{{ text('删除任务', 'Delete task') }}</button></div>
            <p v-if="unfinished" role="status" class="text-xs text-muted" data-test="pelican-observer-state">{{ connection === 'retrying' ? text('观察连接中断，正在重连；后台任务继续。', 'Reconnecting; the background task continues.') : text('后台任务运行中，离开页面不会取消。', 'Running in the background; leaving this page does not cancel it.') }}</p><pre v-if="activeTask.error" class="pelican-error">{{ activeTask.error }}</pre>
          </section>
          <section class="space-y-3 rounded-xl border border-line bg-white p-4 dark:bg-dark-900" :aria-label="text('画廊筛选', 'Gallery filters')">
            <div class="grid items-end gap-3 sm:grid-cols-2 lg:grid-cols-[1fr_1fr_1fr_auto]">
              <PelicanAccountFilter v-model="filters.account" />
              <label class="text-xs text-muted">{{ text('模型', 'Model') }}<input v-model="filters.model" type="search" class="input mt-1 w-full" :placeholder="text('搜索模型', 'Search models')" /></label>
              <label class="text-xs text-muted">{{ text('检测判定', 'Verdict') }}<select v-model="filters.verdict" class="input mt-1 w-full" data-test="pelican-verdict-filter"><option value="">{{ text('全部判定', 'All verdicts') }}</option><option v-for="state in ['passed', 'invalid', 'error', 'uncertain', 'not_reviewed']" :key="state" :value="state">{{ label(state) }}</option></select></label>
              <button type="button" class="btn btn-secondary" :aria-expanded="moreFilters" aria-controls="pelican-extra-filters" @click="moreFilters = !moreFilters">{{ text('更多筛选', 'More filters') }} {{ moreFilters ? '−' : '+' }}</button>
            </div>
            <div v-show="moreFilters" id="pelican-extra-filters" class="grid gap-3 border-t border-line pt-3 sm:grid-cols-3">
              <label class="text-xs text-muted">{{ text('思考强度', 'Reasoning') }}<select v-model="filters.effort" class="input mt-1 w-full"><option value="">{{ text('全部', 'All') }}</option><option v-for="effort in efforts" :key="effort" :value="effort">{{ effort }}</option></select></label>
              <label class="text-xs text-muted">{{ text('执行状态', 'Execution') }}<select v-model="filters.status" class="input mt-1 w-full" data-test="pelican-result-filter"><option value="">{{ text('全部', 'All') }}</option><option v-for="state in ['pending', 'running', 'complete', 'failed', 'incomplete', 'cancelled', 'skipped', 'no_preview']" :key="state" :value="state">{{ state === 'no_preview' ? text('无法预览', 'No preview') : label(state) }}</option></select></label>
              <label class="text-xs text-muted">{{ text('历史场景', 'Legacy scene') }}<select v-model="filters.scene" class="input mt-1 w-full"><option value="">{{ text('全部场景', 'All scenes') }}</option><option v-for="scene in PELICAN_SCENES" :key="scene">{{ scene }}</option></select></label>
            </div>
            <div v-if="filterChips.length" class="flex flex-wrap items-center gap-2 border-t border-line pt-3"><button v-for="chip in filterChips" :key="chip.key" type="button" class="max-w-full break-words rounded-md bg-gray-100 px-2.5 py-1.5 text-xs dark:bg-dark-800" :aria-label="`${text('移除筛选', 'Remove filter')}: ${chip.title}`" @click="filters[chip.key] = ''">{{ chip.title }}: {{ ['verdict', 'status'].includes(chip.key) ? label(chip.value) : chip.value }} ×</button><button type="button" class="btn btn-ghost btn-sm" @click="clearFilters">{{ text('清除筛选', 'Clear filters') }}</button></div>
          </section>
          <section :aria-busy="loading" class="space-y-4">
            <div class="flex items-center justify-between gap-3"><h2 class="font-semibold">{{ taskId ? text('本次作品', 'Task works') : text('全部作品', 'All works') }} <span class="ml-2 text-sm font-normal tabular-nums text-muted">{{ results.total }}</span></h2><span class="text-xs text-muted" role="status">{{ loading ? text('读取中…', 'Loading…') : text('每页 24 条', '24 per page') }}</span></div>
            <div class="pelican-gallery-grid" data-test="pelican-results"><PelicanResultCard v-for="result in results.items" :key="result.id" :result="result" :now="now" :selected="comparison.some(item => item.id === result.id)" :selection-full="comparison.length >= 4" :can-delete="!(unfinished && result.task_id === activeTask?.id)" :busy="busy" @select="toggleCompare" @open="openDetail" @enlarge="openDetail" @retest="item => retestItems([item])" @delete="item => deleteTarget = { taskId: item.task_id, result: item }" /></div>
            <div v-if="loading && !results.items.length" class="pelican-gallery-grid" aria-hidden="true"><div v-for="i in 3" :key="i" class="aspect-[4/3] animate-pulse rounded-xl bg-gray-100 motion-reduce:animate-none dark:bg-dark-800" /></div>
            <div v-if="!loading && !results.items.length" class="rounded-xl border border-dashed border-line px-6 py-14 text-center"><p class="font-medium">{{ filterChips.length ? text('没有符合筛选的作品', 'No works match these filters') : text('第一件作品，从一次测试开始', 'Your first work starts with a test') }}</p><p class="mt-2 text-sm text-muted">{{ text('完成的作品会保留在这里，供你查看、比较和重测。', 'Completed works appear here to explore, compare, and retest.') }}</p><button type="button" class="btn btn-secondary mt-5" :disabled="busy" @click="filterChips.length ? clearFilters() : openComposer()">{{ filterChips.length ? text('清除筛选', 'Clear filters') : text('新建测试', 'New test') }}</button></div>
            <Pagination v-if="results.total" v-model:page="page" :total="results.total" :page-size="24" :show-page-size-selector="false" />
          </section>
        </template>
        <p class="text-xs leading-relaxed text-muted">{{ text('每次检测都是一份独立观察。视觉审核可能误判，结果不证明上游模型身份。', 'Each test is an independent observation. Visual review can be mistaken; results do not prove model identity.') }}</p>
        <section v-if="comparison.length" class="sticky bottom-3 z-20 flex flex-wrap items-center gap-3 rounded-xl border border-primary-200 bg-white p-4 shadow-lg dark:border-primary-800 dark:bg-dark-900" data-test="pelican-comparison-tray">
          <strong class="text-sm">{{ text('作品比较', 'Compare works') }} {{ comparison.length }} / 4</strong><div class="flex min-w-0 flex-1 flex-wrap gap-2"><button v-for="item in comparison" :key="item.id" type="button" class="max-w-52 truncate rounded-md border border-line px-2 py-2 text-xs" :aria-label="`${text('移出比较', 'Remove from comparison')} ${item.account_name}`" @click="toggleCompare(item)">{{ item.account_name }} · {{ item.model_id }} ×</button></div><button type="button" class="btn btn-ghost btn-sm" @click="comparison = []">{{ text('清空', 'Clear') }}</button><button v-if="selectedFailures.length" type="button" class="btn btn-secondary btn-sm" :disabled="busy" data-test="pelican-retry-selected" @click="retestItems(selectedFailures)">{{ text('重测选中失败项', 'Retest selected failures') }}</button><button type="button" class="btn btn-primary btn-sm" :disabled="comparison.length < 2 || detailLoading" data-test="pelican-compare" @click="openComparison">{{ text('并排比较', 'Compare side by side') }}</button>
        </section>
      </template>
      <BaseDialog :show="!!detail || detailLoading && !comparisonOpen" :title="text('作品详情', 'Work details')" width="full" @close="closeDetail"><PelicanWorkDetail v-if="detail" :key="detail.id" :result="detail" /><p v-else class="p-6 text-muted">{{ text('读取详情…', 'Loading details…') }}</p></BaseDialog>
      <BaseDialog :show="comparisonOpen" :title="text('作品比较', 'Compare works')" width="full" @close="closeComparison"><p v-if="detailLoading" role="status" class="p-4 text-muted">{{ text('正在读取作品…', 'Loading works…') }}</p><div class="grid items-start gap-5 md:grid-cols-2" :class="comparisonDetails.length > 2 ? '2xl:grid-cols-4' : ''"><section v-for="item in comparisonDetails" :key="item.id" class="min-w-0"><PelicanWorkDetail :result="item" compact /></section></div></BaseDialog>
      <BaseDialog :show="!!deleteTarget" :title="text('删除检测记录', 'Delete observation')" width="narrow" @close="!deleting && (deleteTarget = null)"><p data-test="pelican-delete-message">{{ deleteTarget?.result ? text('删除这张作品及其检测资料？', 'Delete this work and its observation data?') : text('删除整个任务及全部作品？', 'Delete this task and all its works?') }}</p><template #footer><div class="flex justify-end gap-2"><button type="button" class="btn btn-secondary" :disabled="deleting" @click="deleteTarget = null">{{ text('取消', 'Cancel') }}</button><button type="button" class="btn bg-red-600 text-white" :disabled="deleting" data-test="pelican-confirm-delete" @click="confirmDelete">{{ text('删除', 'Delete') }}</button></div></template></BaseDialog>
    </div>
  </AppLayout>
</template>
<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import PelicanAccountFilter from '@/components/admin/codex/PelicanAccountFilter.vue'
import { readPelicanDraft, savePelicanDraft, type PelicanDraft } from '@/utils/pelicanDraft'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import PelicanTaskComposer from '@/components/admin/codex/PelicanTaskComposer.vue'
import PelicanResultCard from '@/components/admin/codex/PelicanResultCard.vue'
import PelicanWorkDetail from '@/components/admin/codex/PelicanWorkDetail.vue'
import { pelicanTestsAPI, PELICAN_SCENES, createPelicanClientTaskId, type PelicanTaskSnapshot, type PelicanTestRequest, type PelicanRetestRequest, type PelicanResultSummary, type PelicanResultPage, type PelicanTestResult, type PelicanTestHistory } from '@/api/admin/pelicanTests'
import { pelicanFailed, pelicanLabel, pelicanTone } from '@/utils/pelicanDisplay'
import { extractApiErrorMessage } from '@/utils/apiError'
const { locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const label = (value: string) => pelicanLabel(value, locale.value)
const formatTime = (value: string) => new Date(value).toLocaleString()
const lifecycle = new AbortController()
const route = useRoute(), router = useRouter()
const queryString = (key: string) => typeof route.query[key] === 'string' ? route.query[key] as string : ''
const queryPage = (key: string) => { const value = Number(queryString(key)); return Number.isSafeInteger(value) && value > 0 ? value : 1 }
const view = ref(['tasks', 'new'].includes(queryString('view')) ? queryString('view') : 'gallery')
const moreFilters = ref(false), draftWarning = ref('')
const efforts = ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max', 'ultra']
const filters = reactive({ account: queryString('account_id'), model: queryString('model'), effort: queryString('effort'), scene: queryString('scene'), verdict: queryString('verdict'), status: queryString('status') })
const results = shallowRef<PelicanResultPage>({ items: [], total: 0, page: 1, page_size: 24 })
const history = shallowRef<PelicanTestHistory>({ items: [], total: 0, page: 1, size: 12 })
const activeTask = shallowRef<PelicanTaskSnapshot | null>(null)
const taskId = ref(queryString('task')), page = ref(queryPage('page')), historyPage = ref(queryPage('task_page')), error = ref(''), loading = ref(false), connection = ref('idle'), stopping = ref(false)
const composerOpen = computed({ get: () => view.value === 'new', set: value => { view.value = value ? 'new' : 'gallery' } })
const draft = shallowRef<PelicanDraft | undefined>(readPelicanDraft())
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
async function loadGallery(background = false) {
  galleryController?.abort(); const controller = new AbortController(); galleryController = controller; loading.value = !background
  try {
    const value = await pelicanTestsAPI.gallery({ page: page.value, page_size: 24, task_id: taskId.value || undefined, account_id: Number(filters.account) || undefined, model: filters.model.trim() || undefined, effort: filters.effort || undefined, scene: filters.scene || undefined, verdict: filters.verdict || undefined, status: filters.status || undefined }, controller.signal)
    if (!controller.signal.aborted && !lifecycle.signal.aborted) results.value = value
  } catch (value) { if (!controller.signal.aborted) report(value) }
  finally { if (galleryController === controller) loading.value = false }
}
async function refresh() { error.value = ''; await Promise.all([loadHistory(), loadGallery()]) }
async function selectTask(resetPage = true) {
  const version = ++selection; observer?.abort(); activeTask.value = null; connection.value = 'idle'; stopping.value = false; if (resetPage) page.value = 1
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
      await pelicanTestsAPI.observeTask(id, event => { if (controller.signal.aborted) return; activeTask.value = event.task; connection.value = 'connected'; delay = 1000; void loadGallery(true); if (event.type === 'task_complete') { stopping.value = false; void loadHistory() } }, controller.signal)
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
async function accepted(task: PelicanTaskSnapshot) { if (pending.value?.kind === 'task') { savePelicanDraft(); draft.value = undefined } storePending(null); composerOpen.value = false; taskId.value = task.id; historyPage.value = 1; await Promise.all([selectTask(), loadHistory()]) }
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
  try { const items: PelicanResultSummary[] = []; for (let next = 1; ; next++) { const rows = await pelicanTestsAPI.getResults(task.id, { page: next, page_size: 50 }, lifecycle.signal); items.push(...rows.items); if (next * 50 >= rows.total) break } draft.value = { repeat_count: task.test_config?.repeat_count ?? 1, generation_timeout_seconds: task.generation_timeout_seconds || 600, test_items: task.test_config?.test_items?.length ? task.test_config.test_items : ['pelican'], targets: [...new Map(items.map(item => [JSON.stringify([item.account_id, item.model_id]), { account_id: item.account_id, model_id: item.model_id, effort: item.effort }])).values()] }; saveDraft(draft.value); composerOpen.value = true }
  catch (value) { report(value) } finally { copying.value = false }
}
async function stopTask() { if (!activeTask.value || stopping.value) return; stopping.value = true; try { activeTask.value = await pelicanTestsAPI.cancelTask(activeTask.value.id) } catch (value) { stopping.value = false; report(value) } }
function toggleCompare(item: PelicanResultSummary) { if (comparison.value.some(row => row.id === item.id)) comparison.value = comparison.value.filter(row => row.id !== item.id); else if (comparison.value.length < 4) comparison.value = [...comparison.value, item] }
function closeDetail() { content++; detail.value = null; detailLoading.value = false }
async function openDetail(item: PelicanResultSummary) { const version = ++content; detailLoading.value = true; try { const value = await pelicanTestsAPI.getResult(item.task_id, item.id, lifecycle.signal); if (version === content && !lifecycle.signal.aborted) detail.value = value } catch (value) { report(value) } finally { if (version === content) detailLoading.value = false } }
function closeComparison() { content++; comparisonOpen.value = false; detailLoading.value = false }
async function openComparison() { const version = ++content; comparisonOpen.value = true; detailLoading.value = true; comparisonDetails.value = []; try { const values = await Promise.all(comparison.value.map(item => pelicanTestsAPI.getResult(item.task_id, item.id, lifecycle.signal))); if (version === content && !lifecycle.signal.aborted) comparisonDetails.value = values } catch (value) { report(value) } finally { if (version === content) detailLoading.value = false } }
async function confirmDelete() {
  const target = deleteTarget.value; if (!target || deleting.value) return; deleting.value = true
  try { if (target.result) await pelicanTestsAPI.deleteResult(target.taskId, target.result.id); else await pelicanTestsAPI.deleteTask(target.taskId)
    content++; const affected = (item: { id: string; task_id?: string }) => target.result ? item.id === target.result.id : item.task_id === target.taskId
    comparison.value = comparison.value.filter(item => !affected(item)); comparisonDetails.value = comparisonDetails.value.filter(item => !affected(item)); if (detail.value && affected(detail.value)) detail.value = null; if (comparisonDetails.value.length < 2) comparisonOpen.value = false
    if (taskId.value === target.taskId) { try { activeTask.value = await pelicanTestsAPI.getTask(target.taskId, lifecycle.signal) } catch (value) { if ([404, 410].includes(apiStatus(value))) { observer?.abort(); taskId.value = ''; activeTask.value = null } else throw value } }
    deleteTarget.value = null; await refresh(); const last = Math.max(1, Math.ceil(results.value.total / 24)); if (page.value > last) page.value = last
  } catch (value) { report(value) } finally { deleting.value = false }
}
function openComposer() { draft.value = readPelicanDraft(); composerOpen.value = true }
function saveDraft(value?: PelicanDraft) { draftWarning.value = savePelicanDraft(value) ? '' : text('浏览器未能保存草稿，请保持页面打开。', 'The browser could not save the draft. Keep this page open.') }
async function chooseTask(id: string) { taskId.value = id; view.value = 'gallery'; await selectTask() }
const progress = computed(() => activeTask.value?.total ? Math.min(100, Math.round(activeTask.value.completed * 100 / activeTask.value.total)) : 0)
const filterChips = computed(() => Object.entries(filters).filter(([, value]) => value).map(([key, value]) => ({ key: key as keyof typeof filters, value, title: ({ account: text('账号', 'Account'), model: text('模型', 'Model'), effort: text('思考强度', 'Reasoning'), scene: text('历史场景', 'Legacy scene'), verdict: text('判定', 'Verdict'), status: text('执行', 'Execution') })[key as keyof typeof filters] })))
function clearFilters() { Object.assign(filters, { account: '', model: '', effort: '', scene: '', verdict: '', status: '' }) }
let restoringQuery = false
function writeQuery() {
  if (restoringQuery) return
  const query: Record<string, string> = {}
  if (view.value !== 'gallery') query.view = view.value
  if (taskId.value) query.task = taskId.value
  if (page.value > 1) query.page = String(page.value)
  if (historyPage.value > 1) query.task_page = String(historyPage.value)
  for (const [key, value] of Object.entries(filters)) if (value) query[key === 'account' ? 'account_id' : key] = value
  if (JSON.stringify(query) !== JSON.stringify(route.query)) void router.replace({ query })
}
watch(view, (next, previous) => { if (next === 'new' && previous !== 'new') draft.value = readPelicanDraft() })
watch([view, taskId, page, historyPage, filters], writeQuery, { deep: true })
watch(() => route.query, () => {
  const previousTask = taskId.value, previousHistoryPage = historyPage.value
  const previousGallery = JSON.stringify([taskId.value, page.value, filters])
  restoringQuery = true
  view.value = ['tasks', 'new'].includes(queryString('view')) ? queryString('view') : 'gallery'
  taskId.value = queryString('task'); page.value = queryPage('page'); historyPage.value = queryPage('task_page')
  for (const key of Object.keys(filters) as (keyof typeof filters)[]) filters[key] = queryString(key === 'account' ? 'account_id' : key)
  if (taskId.value !== previousTask) void selectTask(false)
  else if (previousGallery !== JSON.stringify([taskId.value, page.value, filters])) void loadGallery()
  if (historyPage.value !== previousHistoryPage) void loadHistory()
  void nextTick(() => { restoringQuery = false })
})
watch(page, () => { if (!restoringQuery) void loadGallery() })
watch(filters, () => { if (restoringQuery) return; if (debounce) clearTimeout(debounce); debounce = setTimeout(() => { if (page.value !== 1) page.value = 1; else void loadGallery() }, 250) })
onMounted(() => { if (taskId.value) { void selectTask(false); void loadHistory() } else void refresh(); void pelicanTestsAPI.getOptions(lifecycle.signal).then(options => { serverOffset.value = Date.parse(options.generated_at) - Date.now(); clockKnown.value = Number.isFinite(serverOffset.value) }).catch(report); clock = setInterval(() => { now.value = Date.now() + serverOffset.value }, 1000); if (pending.value) void recoverPending() })
onBeforeUnmount(() => { lifecycle.abort(); galleryController?.abort(); observer?.abort(); if (clock) clearInterval(clock); if (debounce) clearTimeout(debounce) })
</script>
<style scoped>
.pelican-gallery-grid { display: grid; min-width: 0; grid-template-columns: minmax(0, 1fr); gap: 1.25rem; }
@media (min-width: 768px) { .pelican-gallery-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (min-width: 1280px) { .pelican-gallery-grid { grid-template-columns: repeat(3, minmax(0, 1fr)); } }
@media (min-width: 1920px) { .pelican-gallery-grid { grid-template-columns: repeat(4, minmax(0, 1fr)); } }
.pelican-error { white-space: pre-wrap; overflow-wrap: anywhere; font-size: .75rem; line-height: 1.7; @apply rounded-lg border border-red-200 bg-red-50 p-3 text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-200; }
</style>
