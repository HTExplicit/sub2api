<template>
  <BaseDialog :show="show" :title="t('admin.accounts.batchTest.title')" width="extra-wide" @close="handleClose">
    <div class="space-y-4">
      <div class="flex flex-wrap items-end gap-3">
        <label class="min-w-64 flex-1">
          <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-200">{{ t('admin.accounts.batchTest.model') }}</span>
          <select v-model="selectedModel" class="input w-full" data-testid="batch-test-model" :disabled="running">
            <option value="">{{ t('admin.accounts.batchTest.autoModel') }}</option>
            <option v-for="option in modelOptions" :key="option.id" :value="option.id">
              {{ option.id }} ({{ option.supported }}/{{ rows.length }})
            </option>
          </select>
        </label>
        <div class="text-sm text-gray-500 dark:text-gray-400">
          {{ loadingModels && selectedModel ? t('admin.accounts.batchTest.loadingModels') : t('admin.accounts.batchTest.summary', { supported: supportedCount, skipped: skippedCount }) }}
        </div>
        <label class="w-36">
          <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-200">{{ t('admin.accounts.batchTest.resultFilter') }}</span>
          <select v-model="resultFilter" class="input w-full" :aria-label="t('admin.accounts.batchTest.resultFilter')">
            <option value="all">{{ t('admin.accounts.batchTest.filter.all') }}</option>
            <option value="success">{{ t('admin.accounts.batchTest.filter.success') }}</option>
            <option value="failed">{{ t('admin.accounts.batchTest.filter.failed') }}</option>
          </select>
        </label>
        <button class="btn btn-primary" data-testid="batch-test-start" :disabled="running || (!!selectedModel && loadingModels) || supportedCount === 0" @click="startTest">
          {{ running ? t('admin.accounts.batchTest.testing') : t('admin.accounts.batchTest.start') }}
        </button>
        <button v-if="running" class="btn btn-secondary" data-testid="batch-test-stop" @click="stopTest">
          {{ t('admin.accounts.batchTest.stop') }}
        </button>
        <button v-else-if="failedCount" class="btn btn-secondary" data-testid="batch-test-retry-failed" @click="retryFailed">
          {{ t('admin.accounts.batchTest.retryFailed') }} ({{ failedCount }})
        </button>
        <button v-if="snapshotAvailable" class="btn" data-testid="batch-test-view-snapshot" :disabled="running" @click="restoreSnapshot">
          {{ t('admin.accounts.batchTest.viewSnapshot') }}
        </button>
      </div>

      <div v-if="running || completedCount" class="text-sm text-gray-600 dark:text-gray-300">
        {{ t('admin.accounts.batchTest.progress', { completed: completedCount, total: runTotal }) }}
      </div>

      <div class="max-h-[55vh] overflow-auto rounded-lg border border-gray-200 dark:border-dark-600">
        <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-600">
          <thead class="bg-gray-50 dark:bg-dark-700">
            <tr>
              <th class="px-3 py-2 text-left">{{ t('admin.accounts.batchTest.account') }}</th>
              <th class="px-3 py-2 text-left">{{ t('admin.accounts.batchTest.modelStatus') }}</th>
              <th class="px-3 py-2 text-left">{{ t('admin.accounts.batchTest.result') }}</th>
              <th class="px-3 py-2 text-right">{{ t('admin.accounts.batchTest.firstByte') }}</th>
              <th class="px-3 py-2 text-right">{{ t('admin.accounts.batchTest.totalLatency') }}</th>
              <th class="px-3 py-2 text-left">{{ t('admin.accounts.batchTest.error') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <tr v-for="row in visibleRows" :key="row.id">
              <td class="px-3 py-2">
                <div class="font-medium text-gray-900 dark:text-gray-100">{{ row.name }}</div>
                <div class="text-xs text-gray-500">{{ row.platform }} #{{ row.id }}</div>
              </td>
              <td class="px-3 py-2">
                <span :class="!selectedModel || row.supportedModels === null ? 'text-gray-500' : row.supportedModels.includes(selectedModel) ? 'text-green-600' : 'text-amber-600'">
                  {{ modelSupportLabel(row) }}
                </span>
                <div class="mt-1">{{ row.requestedModel || selectedModel || '-' }}</div>
                <div v-if="row.upstreamModel" :class="row.upstreamModel === (row.requestedModel || selectedModel) ? 'text-green-600 dark:text-green-400' : 'text-red-600 dark:text-red-400'">
                  ↳ {{ t('admin.accounts.batchTest.upstreamResponse') }}: {{ row.upstreamModel }}
                  <span v-if="row.upstreamModel !== (row.requestedModel || selectedModel)">（{{ t('admin.accounts.batchTest.modelMismatch') }}）</span>
                </div>
              </td>
              <td class="px-3 py-2">
                <span :class="resultClass(row.status)">{{ resultLabel(row.status) }}</span>
              </td>
              <td class="px-3 py-2 text-right">{{ formatLatency(row.firstByteLatencyMs) }}</td>
              <td class="px-3 py-2 text-right">{{ formatLatency(row.latencyMs) }}</td>
              <td class="max-w-md px-3 py-2 text-red-600">
                <div class="max-h-24 overflow-y-auto whitespace-pre-wrap break-all">{{ row.error || '-' }}</div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { adminAPI } from '@/api/admin'
import type { Account } from '@/types'
import type { BatchTestAccountEvent } from '@/api/admin/accounts'

type KnownAccount = Pick<Account, 'id' | 'name' | 'platform'>
const props = defineProps<{ show: boolean; accountIds: number[]; accounts: KnownAccount[] }>()
const emit = defineEmits<{ (event: 'close'): void }>()
const { t } = useI18n()

type Row = {
  id: number
  name: string
  platform: string
  supportedModels: string[] | null
  status: 'checking' | 'ready' | 'skipped' | 'testing' | 'success' | 'failed'
  firstByteLatencyMs: number
  latencyMs: number
  error: string
  requestedModel: string
  upstreamModel: string
}

const rows = ref<Row[]>([])
// '' asks the server to choose each account's default test model.
const selectedModel = ref('')
const loadingModels = ref(false)
const running = ref(false)
const completedCount = ref(0)
const runTotal = ref(0)
const resultFilter = ref<'all' | 'success' | 'failed'>('all')
const snapshotAvailable = ref(false)
const SNAPSHOT_KEY = 'sub2api:batch-account-test-snapshot'
const CATALOG_CONCURRENCY = 8
let loadVersion = 0
let controller: AbortController | null = null

type Snapshot = { model: string; savedAt: string; rows: Row[] }
const validStatuses = new Set<Row['status']>(['checking', 'ready', 'skipped', 'testing', 'success', 'failed'])
const readSnapshot = (): Snapshot | null => {
  try {
    const parsed = JSON.parse(localStorage.getItem(SNAPSHOT_KEY) || 'null') as Partial<Snapshot> | null
    if (!parsed || typeof parsed.model !== 'string' || !Array.isArray(parsed.rows) || !parsed.rows.length) return null
    if (!parsed.rows.every(row => row && typeof row.id === 'number' && typeof row.name === 'string' && typeof row.platform === 'string' && (row.supportedModels === null || Array.isArray(row.supportedModels)) && validStatuses.has(row.status))) return null
    return parsed as Snapshot
  } catch {
    return null
  }
}

const modelOptions = computed(() => {
  const counts = new Map<string, number>()
  for (const row of rows.value) {
    for (const model of row.supportedModels || []) counts.set(model, (counts.get(model) || 0) + 1)
  }
  return [...counts.entries()].map(([id, supported]) => ({ id, supported })).sort((a, b) => a.id.localeCompare(b.id))
})
const testableRows = computed(() => selectedModel.value ? rows.value.filter(row => row.supportedModels?.includes(selectedModel.value)) : rows.value)
const supportedCount = computed(() => testableRows.value.length)
const skippedCount = computed(() => rows.value.length - supportedCount.value)
const failedCount = computed(() => rows.value.filter(row => row.status === 'failed').length)
const visibleRows = computed(() => {
  const filtered = resultFilter.value === 'all'
    ? rows.value
    : rows.value.filter(row => row.status === resultFilter.value)
  return [...filtered].sort((a, b) => {
    const rank = (status: Row['status']) => status === 'success' ? 0 : status === 'failed' ? 1 : 2
    const rankDiff = rank(a.status) - rank(b.status)
    if (rankDiff !== 0 || a.status !== 'success' || b.status !== 'success') return rankDiff
    if (a.firstByteLatencyMs === 0) return b.firstByteLatencyMs === 0 ? 0 : 1
    if (b.firstByteLatencyMs === 0) return -1
    return a.firstByteLatencyMs - b.firstByteLatencyMs
  })
})

const loadModels = async () => {
  const version = ++loadVersion
  loadingModels.value = true
  selectedModel.value = ''
  resultFilter.value = 'all'
  completedCount.value = 0
  runTotal.value = 0
  const known = new Map<number, KnownAccount>(props.accounts.map(account => [account.id, account]))
  rows.value = props.accountIds.map(id => {
    const account = known.get(id)
    return { id, name: account?.name || `Account ${id}`, platform: account?.platform || '-', supportedModels: null, status: 'checking', firstByteLatencyMs: 0, latencyMs: 0, error: '', requestedModel: '', upstreamModel: '' }
  })
  // Catalog reads only serve an explicit model choice; keep them bounded.
  const queue = [...rows.value]
  const loadRow = async (row: Row) => {
    try {
      if (!known.has(row.id)) known.set(row.id, await adminAPI.accounts.getById(row.id))
      const account = known.get(row.id)
      if (account) { row.name = account.name; row.platform = account.platform }
      row.supportedModels = (await adminAPI.accounts.getAvailableModels(row.id)).map(model => model.id)
      if (row.status === 'checking') row.status = 'ready'
    } catch (error) {
      if (row.status === 'checking') { row.status = 'skipped'; row.error = String(error) }
    }
  }
  await Promise.all(Array.from({ length: Math.min(CATALOG_CONCURRENCY, queue.length) }, async () => {
    for (let row = queue.shift(); row; row = queue.shift()) {
      if (version !== loadVersion) return
      await loadRow(row)
    }
  }))
  if (version !== loadVersion || !props.show) return
  loadingModels.value = false
}

const handleEvent = (event: BatchTestAccountEvent) => {
  if (event.type === 'batch_start') {
    runTotal.value = event.total || runTotal.value
    return
  }
  if (event.type === 'account_started' && event.account_id) {
    const row = rows.value.find(item => item.id === event.account_id)
    if (row) { row.status = 'testing'; row.requestedModel = event.model_id || '' }
  }
  if (event.type !== 'account_result' || !event.account_id) return
  const row = rows.value.find(item => item.id === event.account_id)
  if (!row) return
  row.status = event.status === 'success' ? 'success' : 'failed'
  row.requestedModel = event.model_id || selectedModel.value
  row.upstreamModel = event.upstream_model || ''
  row.firstByteLatencyMs = event.first_byte_latency_ms || 0
  row.latencyMs = event.latency_ms || 0
  row.error = event.error || ''
  completedCount.value = event.completed || completedCount.value + 1
  persistSnapshot()
}

const runTest = async (ids: number[]) => {
  const runIds = new Set(ids)
  rows.value.forEach(row => {
    if (!runIds.has(row.id)) return
    Object.assign(row, { status: 'ready', error: '', requestedModel: '', upstreamModel: '', firstByteLatencyMs: 0, latencyMs: 0 })
  })
  completedCount.value = 0
  runTotal.value = ids.length
  running.value = true
  controller = new AbortController()
  const signal = controller.signal
  try {
    await adminAPI.accounts.batchTestAccounts(ids, selectedModel.value, handleEvent, signal)
  } catch (error) {
    const message = signal.aborted ? t('admin.accounts.batchTest.stopped') : String(error)
    rows.value.filter(row => runIds.has(row.id) && (row.status === 'testing' || row.status === 'ready')).forEach(row => { row.status = 'failed'; row.error = message })
  } finally {
    controller = null
    running.value = false
    persistSnapshot()
  }
}

const startTest = async () => {
  const ids = testableRows.value.map(row => row.id)
  rows.value.forEach(row => { if (!ids.includes(row.id)) row.status = 'skipped' })
  await runTest(ids)
}

// Retry re-submits the failed accounts with the current model choice.
const retryFailed = async () => {
  await runTest(rows.value.filter(row => row.status === 'failed').map(row => row.id))
}

// Closing the stream stops the run on the server.
const stopTest = () => { controller?.abort() }

const persistSnapshot = () => {
  if (running.value || !completedCount.value) return
  try {
    localStorage.setItem(SNAPSHOT_KEY, JSON.stringify({ model: selectedModel.value, savedAt: new Date().toISOString(), rows: rows.value }))
    snapshotAvailable.value = true
  } catch {
    snapshotAvailable.value = false
  }
}

const restoreSnapshot = () => {
  const snapshot = readSnapshot()
  if (!snapshot) { snapshotAvailable.value = false; return }
  rows.value = snapshot.rows
  selectedModel.value = snapshot.model
  completedCount.value = rows.value.filter(row => row.status === 'success' || row.status === 'failed').length
  runTotal.value = completedCount.value
  resultFilter.value = 'all'
  running.value = false
}

const handleClose = () => { if (!running.value) emit('close') }
const modelSupportLabel = (row: Row) => !selectedModel.value ? t('admin.accounts.batchTest.automatic') : row.supportedModels === null ? (row.status === 'skipped' ? t('admin.accounts.batchTest.status.skipped') : t('admin.accounts.batchTest.checking')) : row.supportedModels.includes(selectedModel.value) ? t('admin.accounts.batchTest.supported') : t('admin.accounts.batchTest.unsupported')
const resultLabel = (status: Row['status']) => t(`admin.accounts.batchTest.status.${status}`)
const resultClass = (status: Row['status']) => status === 'success' ? 'text-green-600 dark:text-green-400' : status === 'failed' ? 'text-red-600 dark:text-red-400' : 'text-gray-600 dark:text-gray-300'
const formatLatency = (value: number) => value > 0 ? `${value} ms` : '-'

watch(() => props.show, value => {
  if (value) {
    snapshotAvailable.value = !!readSnapshot()
    void loadModels()
  }
})
</script>
