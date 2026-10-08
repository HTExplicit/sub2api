<template>
  <AppLayout>
    <div class="mx-auto w-full max-w-[1920px] space-y-4" data-ui="pelican-tests">
      <header class="flex flex-wrap items-start justify-between gap-3 border-b border-line pb-4">
        <div class="min-w-0 space-y-1">
          <h1 class="text-xl font-semibold">{{ t('admin.pelicanTests.title') }}</h1>
          <p class="max-w-4xl text-sm text-muted">{{ t('admin.pelicanTests.description') }}</p>
        </div>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="loading || refreshing || running" data-test="pelican-refresh" @click="refreshInventory">
          <Icon name="refresh" size="sm" aria-hidden="true" />{{ t('admin.pelicanTests.refreshLocal') }}
        </button>
      </header>
      <pre v-if="error" role="alert" class="pelican-error">{{ error }}</pre>
      <p v-if="loading" class="py-4 text-center text-sm text-muted">{{ t('common.loading') }}</p>

      <section class="card space-y-3 p-3 sm:p-4" aria-labelledby="pelican-selection-title" :aria-busy="loading || optionsLoading > 0">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div class="space-y-1">
            <h2 id="pelican-selection-title" class="font-semibold">{{ t('admin.pelicanTests.chooseAccounts') }} · {{ selectedAccountIds.length }}</h2>
            <p class="text-xs text-muted">{{ t('admin.pelicanTests.localCandidatesHint') }}</p>
          </div>
          <div class="flex flex-wrap items-center gap-2">
            <label for="pelican-budget" class="flex items-center gap-2 text-xs">
              {{ t('admin.pelicanTests.generationBudget') }}
              <input id="pelican-budget" v-model.number="budgetMinutes" type="number" min="1" max="30" step="1" class="input w-20" :disabled="running" data-test="pelican-budget" />
              {{ t('admin.pelicanTests.minutes') }}
            </label>
            <button v-if="running" type="button" class="btn btn-secondary btn-sm" data-test="pelican-test-stop" @click="stopTests">{{ t('admin.codexGatewayBorrow.stop') }}</button>
            <button type="button" class="btn btn-primary btn-sm" :disabled="!canRunTests" data-test="pelican-test-start" @click="startTests">
              <Icon name="play" size="sm" aria-hidden="true" />{{ t('admin.codexGatewayBorrow.startTest', { count: testTargets.length }) }}
            </button>
          </div>
        </div>
        <p v-if="!budgetValid" role="alert" class="text-xs text-red-700 dark:text-red-300">{{ t('admin.pelicanTests.budgetBoundary') }}</p>
        <p v-if="!serverClockKnown" class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.clockUnavailable') }}</p>
        <details class="rounded-lg border border-line p-3" data-test="pelican-prompt-details">
          <summary class="cursor-pointer text-sm font-medium">{{ t('admin.pelicanTests.viewPrompt') }}</summary>
          <label for="pelican-fixed-prompt" class="mb-2 mt-3 block text-xs font-medium">{{ t('admin.codexGatewayBorrow.fixedPrompt') }}</label>
          <textarea id="pelican-fixed-prompt" :value="PELICAN_PROMPT" readonly rows="3" class="input w-full resize-none text-sm" data-test="pelican-fixed-prompt" />
        </details>
        <fieldset :disabled="running || loading || refreshing" class="space-y-3">
          <legend class="sr-only">{{ t('admin.pelicanTests.chooseAccounts') }}</legend>
          <div class="flex flex-wrap items-end gap-2">
            <label class="min-w-40 flex-1 space-y-1 text-xs">
              <span class="block">{{ t('admin.pelicanTests.searchAccounts') }}</span>
              <input v-model="search" type="search" class="input w-full" :placeholder="t('admin.pelicanTests.searchAccounts')" data-test="pelican-account-search" />
            </label>
            <label class="space-y-1 text-xs">
              <span class="block">{{ t('admin.pelicanTests.platform') }}</span>
              <select v-model="platformFilter" class="input min-w-32" data-test="pelican-platform-filter">
                <option value="">{{ t('admin.pelicanTests.allPlatforms') }}</option>
                <option v-for="platform in platforms" :key="platform" :value="platform">{{ platform }}</option>
              </select>
            </label>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="!filteredAccounts.length" data-test="pelican-select-filtered" @click="selectFiltered">{{ t('admin.pelicanTests.selectFiltered', { count: filteredAccounts.length }) }}</button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="!selectedAccountIds.length" data-test="pelican-clear-selection" @click="selectedAccountIds = []">{{ t('admin.pelicanTests.clearSelection') }}</button>
          </div>
          <div class="pelican-account-list grid gap-2 lg:grid-cols-2" data-test="pelican-account-list">
            <div v-for="account in filteredAccounts" :key="account.id" class="min-w-0 rounded-lg border border-line p-3">
              <label class="flex cursor-pointer items-start gap-2 text-sm">
                <input v-model="selectedAccountIds" type="checkbox" :value="account.id" class="checkbox mt-1 shrink-0" :data-test="`pelican-account-${account.id}`" />
                <span class="min-w-0 flex-1">
                  <span class="block break-words font-medium">{{ account.name }} <span class="text-muted">#{{ account.id }}</span></span>
                  <span class="mt-1 block text-xs text-muted">{{ account.platform }} · {{ account.type }} · {{ accountStateLabel(account) }}</span>
                </span>
              </label>
              <pre v-if="account.error_message" class="pelican-account-error mt-2">{{ account.error_message }}</pre>
              <div v-if="selectedAccountIds.includes(account.id)" class="mt-3 space-y-2 border-t border-line pt-3" :data-test="`pelican-account-models-${account.id}`">
                <p v-if="!accountOptions[account.id]" class="text-xs text-muted">{{ t('common.loading') }}</p>
                <p v-if="accountOptions[account.id]?.capability_reason" class="break-words text-xs text-red-700 dark:text-red-300">{{ accountOptions[account.id].capability_reason }}</p>
                <div v-for="(selection, index) in accountModels[account.id] || []" :key="selection.key" class="flex flex-wrap items-end gap-2">
                  <label class="min-w-36 flex-1 space-y-1 text-xs">
                    <span class="block">{{ t('admin.pelicanTests.model') }}</span>
                    <input :value="selection.model_id" :list="`pelican-model-candidates-${account.id}`" class="input w-full" :placeholder="t('admin.pelicanTests.manualModel')" :data-test="`pelican-model-${account.id}-${index}`" @input="changeModel(account.id, index, ($event.target as HTMLInputElement).value)" />
                  </label>
                  <label class="space-y-1 text-xs">
                    <span class="block">{{ t('admin.codexGatewayBorrow.effort') }}</span>
                    <select v-model="selection.effort" class="input w-28" :data-test="`pelican-effort-${account.id}-${index}`">
                      <option value="">{{ t('admin.pelicanTests.normalDefault') }}</option>
                      <option v-for="effort in effortOptions(account.id, selection.model_id)" :key="effort" :value="effort">{{ effort }}</option>
                    </select>
                  </label>
                  <button v-if="(accountModels[account.id]?.length || 0) > 1" type="button" class="btn btn-secondary btn-sm" :aria-label="t('admin.pelicanTests.removeModel')" @click="accountModels[account.id].splice(index, 1)"><Icon name="x" size="sm" aria-hidden="true" /></button>
                  <p v-if="modelOption(account.id, selection.model_id)?.capability_reason" class="w-full break-words text-xs text-red-700 dark:text-red-300">{{ modelOption(account.id, selection.model_id)?.capability_reason }}</p>
                </div>
                <datalist :id="`pelican-model-candidates-${account.id}`">
                  <option v-for="model in accountOptions[account.id]?.models || []" :key="model.id" :value="model.id">{{ model.display_name }}</option>
                </datalist>
                <button v-if="accountOptions[account.id]" type="button" class="btn btn-secondary btn-sm" :data-test="`pelican-add-model-${account.id}`" @click="addModel(account.id)">{{ t('admin.pelicanTests.addModel') }}</button>
              </div>
            </div>
          </div>
          <p v-if="!loading && !filteredAccounts.length" class="rounded-lg border border-dashed border-line p-4 text-sm text-muted">{{ t('admin.pelicanTests.noAccounts') }}</p>
        </fieldset>
        <p class="text-xs text-muted">{{ t('admin.pelicanTests.executionHint', { count: options?.max_concurrency || 10 }) }}</p>
      </section>

      <section class="card space-y-3 p-3 sm:p-4" :aria-label="t('admin.codexGatewayBorrow.resultViews')">
        <pre v-if="testError" role="alert" class="pelican-error">{{ testError }}</pre>
        <p v-if="testStopped" role="status" class="text-sm text-muted">{{ t('admin.codexGatewayBorrow.stoppedHint') }}</p>
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-line pb-3">
          <div class="flex flex-wrap gap-2" role="tablist" :aria-label="t('admin.codexGatewayBorrow.resultViews')">
            <button id="pelican-results-tab" type="button" role="tab" class="btn btn-sm" :class="activeTab === 'results' ? 'btn-primary' : 'btn-secondary'" :aria-selected="activeTab === 'results'" :tabindex="activeTab === 'results' ? 0 : -1" aria-controls="pelican-results-panel" data-test="pelican-tab-results" @click="activeTab = 'results'" @keydown="changeTabWithKeyboard">{{ t('admin.pelicanTests.results') }}</button>
            <button id="pelican-history-tab" type="button" role="tab" class="btn btn-sm" :class="activeTab === 'history' ? 'btn-primary' : 'btn-secondary'" :aria-selected="activeTab === 'history'" :tabindex="activeTab === 'history' ? 0 : -1" aria-controls="pelican-history-panel" data-test="pelican-tab-history" @click="activeTab = 'history'" @keydown="changeTabWithKeyboard">{{ t('admin.pelicanTests.history') }}</button>
          </div>
          <label v-if="activeTab === 'results'" class="flex items-center gap-2 text-xs">
            {{ t('admin.pelicanTests.cardSize') }}
            <select v-model="cardSize" class="input" data-test="pelican-card-size" @change="persistCardSize">
              <option v-for="size in cardSizes" :key="size" :value="size">{{ t(`admin.pelicanTests.sizes.${size}`) }}</option>
            </select>
          </label>
          <p v-if="activeTask" class="text-xs text-muted" role="status" data-test="pelican-progress">{{ activeTask.completed }} / {{ activeTask.total }} · {{ stateLabel(activeTask.status) }} <span v-if="activeTask.replayed">· {{ t('admin.codexGatewayBorrow.replayed') }}</span></p>
        </div>
        <div v-if="activeTab === 'history'" id="pelican-history-panel" role="tabpanel" aria-labelledby="pelican-history-tab" class="space-y-3">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <p class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.historyHint') }}</p>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="historyLoading" data-test="pelican-history-refresh" @click="loadHistory(historyPage)">{{ t('admin.codexGatewayBorrow.refreshHistory') }}</button>
          </div>
          <pre v-if="historyError" role="alert" class="pelican-error">{{ historyError }}</pre>
          <p v-if="historyLoading" class="text-sm text-muted">{{ t('common.loading') }}</p>
          <button v-for="task in history.items" :key="task.id" type="button" class="flex w-full flex-wrap items-center justify-between gap-2 rounded-lg border border-line p-3 text-left hover:bg-gray-50 dark:hover:bg-dark-800" :disabled="running || historyLoading" :data-test="`pelican-history-${task.id}`" @click="openHistory(task.id)">
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
        <div v-else id="pelican-results-panel" role="tabpanel" aria-labelledby="pelican-results-tab" class="space-y-3">
          <div v-if="activeTask" class="space-y-1 text-xs text-muted">
            <p class="break-all">{{ t('admin.codexGatewayBorrow.taskId') }}: {{ activeTask.id }} · {{ formatTime(activeTask.created_at) }} · {{ t('admin.codexGatewayBorrow.expiresAt') }}: {{ formatTime(activeTask.expires_at) }}</p>
            <pre v-if="activeTask.error" class="pelican-error">{{ activeTask.error }}</pre>
          </div>
          <p v-if="!results.length" class="rounded-lg border border-dashed border-line py-8 text-center text-sm text-muted">{{ t('admin.codexGatewayBorrow.noResults') }}</p>
          <div class="pelican-result-grid" :data-size="cardSize" data-test="pelican-result-grid">
            <article v-for="result in results" :key="result.id" class="pelican-result-card overflow-hidden rounded-lg border border-line bg-white dark:bg-dark-800" :data-test="`pelican-result-${result.id}`">
              <header class="space-y-1 px-2 py-2 text-xs">
                <div class="flex items-center justify-between gap-1">
                  <p class="min-w-0 truncate font-medium" :title="`${result.account_name || accountName(result.account_id)} #${result.account_id}`">{{ result.account_name || accountName(result.account_id) }}</p>
                  <span class="shrink-0 rounded px-1 py-0.5 text-[11px] font-medium" :class="resultStatusClass(result.status)">{{ result.phase && ['pending', 'running'].includes(result.status) ? t(`admin.pelicanTests.phases.${result.phase}`) : stateLabel(result.status) }}</span>
                </div>
                <p class="truncate text-muted" :title="result.model_id">{{ result.platform || accountById(result.account_id)?.platform || '—' }} · {{ result.model_id }}</p>
                <p class="truncate text-muted">{{ result.effort || t('admin.pelicanTests.normalDefault') }} · {{ formatDuration(result.generation_duration_ms ?? result.duration_ms) }}</p>
              </header>
              <div :ref="element => setPreviewElement(result.id, element)" class="aspect-[4/3] w-full bg-gray-50 dark:bg-dark-900" :data-preview-result="result.id">
                <BorrowPelicanPreview v-if="result.preview_url && visiblePreviews.has(result.id)" :preview-url="result.preview_url" :title="previewTitle(result)" />
                <div v-else class="flex h-full items-center justify-center px-2 text-center text-xs text-muted"><span>{{ result.preview_url ? t('admin.pelicanTests.previewOnVisible') : previewPlaceholder(result) }}</span></div>
              </div>
              <div class="flex items-center gap-1 border-t border-line p-1.5">
                <button type="button" class="btn btn-secondary btn-sm flex-1" :data-test="`pelican-details-${result.id}`" @click="detailResult = result">{{ t('admin.pelicanTests.details') }}</button>
                <button type="button" class="btn btn-secondary btn-sm" :disabled="!result.preview_url" :aria-label="`${t('admin.codexGatewayBorrow.enlarge')} ${previewTitle(result)}`" :data-test="`pelican-enlarge-${result.id}`" @click="enlarged = result"><Icon name="externalLink" size="sm" aria-hidden="true" /></button>
                <button type="button" class="btn btn-secondary btn-sm" :disabled="!result.raw_html && !result.html" :aria-label="t('admin.pelicanTests.downloadHtml')" @click="downloadHtml(result)"><Icon name="download" size="sm" aria-hidden="true" /></button>
              </div>
            </article>
          </div>
        </div>
      </section>
      <BaseDialog :show="!!detailResult" :title="detailResult ? previewTitle(detailResult) : t('admin.pelicanTests.details')" width="extra-wide" @close="detailResult = null">
        <div v-if="detailResult" class="space-y-4 text-sm" data-test="pelican-detail-content">
          <dl class="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div><dt class="text-xs text-muted">{{ t('admin.pelicanTests.account') }}</dt><dd class="break-words">{{ detailResult.account_name || accountName(detailResult.account_id) }} #{{ detailResult.account_id }} · {{ detailResult.platform || accountById(detailResult.account_id)?.platform || '—' }}</dd></div>
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
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch, type ComponentPublicInstance } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import BorrowPelicanPreview from '@/components/admin/codex/BorrowPelicanPreview.vue'
import { accountsAPI } from '@/api/admin/accounts'
import {
  PELICAN_PROMPT, pelicanTestsAPI, createPelicanClientTaskId,
  type PelicanOptions, type PelicanAccountOption, type PelicanTestEvent,
  type PelicanTestHistory, type PelicanTestRequest, type PelicanTestResult, type PelicanTestTask
} from '@/api/admin/pelicanTests'
import type { AccountListItem } from '@/types'
import { extractApiErrorMessage } from '@/utils/apiError'

interface ModelSelection { key: number; model_id: string; effort: string }
const { t, te } = useI18n()
const lifecycle = new AbortController()
const accounts = ref<AccountListItem[]>([])
const options = ref<PelicanOptions | null>(null)
const accountOptions = ref<Record<number, PelicanAccountOption>>({})
const accountModels = ref<Record<number, ModelSelection[]>>({})
const selectedAccountIds = ref<number[]>([])
const search = ref('')
const platformFilter = ref('')
const budgetMinutes = ref(10)
const serverClockKnown = ref(false)
const serverOffsetMs = ref(0)
const loading = ref(true)
const refreshing = ref(false)
const optionsLoading = ref(0)
const error = ref('')
const running = ref(false)
const testError = ref('')
const testStopped = ref(false)
const activeTask = ref<PelicanTestTask | null>(null)
const detailResult = ref<PelicanTestResult | null>(null)
const enlarged = ref<PelicanTestResult | null>(null)
const history = ref<PelicanTestHistory>({ items: [], total: 0, page: 1, size: 12 })
const historyPage = ref(1)
const historyLoading = ref(false)
const historyError = ref('')
const activeTab = ref<'results' | 'history'>('results')
const cardSizes = ['compact', 'standard', 'large'] as const
type CardSize = typeof cardSizes[number]
const cardSize = ref<CardSize>(readCardSize())
const visiblePreviews = ref(new Set<string>())
const previewElements = new Map<string, HTMLElement>()
const pendingOptions = new Set<number>()
let previewObserver: IntersectionObserver | undefined
let testController: AbortController | undefined
let nextSelectionKey = 0

const accountMap = computed(() => new Map(accounts.value.map(account => [account.id, account])))
const platforms = computed(() => [...new Set(accounts.value.map(account => account.platform))].sort())
const filteredAccounts = computed(() => {
  const term = search.value.trim().toLowerCase()
  return accounts.value.filter(account => (!platformFilter.value || account.platform === platformFilter.value)
    && (!term || account.name.toLowerCase().includes(term) || String(account.id).includes(term)))
})
const budgetValid = computed(() => Number.isInteger(budgetMinutes.value) && budgetMinutes.value >= 1 && budgetMinutes.value <= 30)
const testTargets = computed(() => [...new Set(selectedAccountIds.value)].flatMap(account_id => {
  const seen = new Set<string>()
  return (accountModels.value[account_id] || []).filter(selection => {
    const model = selection.model_id.trim()
    if (!model || seen.has(model)) return false
    seen.add(model)
    return true
  }).map(selection => ({ account_id, model_id: selection.model_id.trim(), effort: selection.effort }))
}))
const canRunTests = computed(() => serverClockKnown.value && budgetValid.value && testTargets.value.length > 0
  && selectedAccountIds.value.every(id => (accountModels.value[id] || []).some(selection => selection.model_id.trim()))
  && !running.value && !loading.value && !refreshing.value && optionsLoading.value === 0 && !historyLoading.value)
const results = computed(() => activeTask.value?.results || [])

function readCardSize(): CardSize {
  try { const saved = localStorage.getItem('pelican_card_size'); if (cardSizes.includes(saved as CardSize)) return saved as CardSize } catch { /* Storage may be unavailable. */ }
  return 'standard'
}
function persistCardSize() { try { localStorage.setItem('pelican_card_size', cardSize.value) } catch { /* The current view still changes. */ } }
function accountById(id: number) { return accountMap.value.get(id) }
function accountName(id: number) { return accountById(id)?.name || accountOptions.value[id]?.name || t('admin.codexGatewayBorrow.unknownAccount') }
function stateLabel(state: string) { const key = `admin.codexGatewayBorrow.states.${state}`; return te(key) ? t(key) : state }
function accountStateLabel(account: AccountListItem) {
  const parts = [stateLabel(account.status)]
  if (account.schedulable === false) parts.push(t('admin.codexGatewayBorrow.paused'))
  const now = Date.now() + serverOffsetMs.value
  if (account.rate_limit_reset_at && Date.parse(account.rate_limit_reset_at) > now) parts.push(t('admin.codexGatewayBorrow.rateLimited'))
  if (account.temp_unschedulable_until && Date.parse(account.temp_unschedulable_until) > now) parts.push(t('admin.pelicanTests.temporarilyUnavailable'))
  if (account.overload_until && Date.parse(account.overload_until) > now) parts.push(t('admin.pelicanTests.overloaded'))
  return parts.join(' · ')
}
function modelOption(id: number, model: string) { return accountOptions.value[id]?.models.find(candidate => candidate.id === model.trim()) }
function effortOptions(id: number, model: string) { return modelOption(id, model)?.reasoning_efforts || [] }
function defaultEffort(id: number, model: string) { return effortOptions(id, model).includes('high') ? 'high' : '' }
function changeModel(id: number, index: number, model: string) {
  const selection = accountModels.value[id]?.[index]
  if (selection) { selection.model_id = model; selection.effort = defaultEffort(id, model) }
}
function addModel(id: number) {
  const choices = accountOptions.value[id]
  const current = accountModels.value[id] || []
  const model = choices?.models.find(candidate => !current.some(selected => selected.model_id === candidate.id))?.id
    || (current.length ? '' : choices?.default_model_id || choices?.models[0]?.id || '')
  accountModels.value[id] = [...current, { key: ++nextSelectionKey, model_id: model, effort: defaultEffort(id, model) }]
}
function selectFiltered() { selectedAccountIds.value = [...new Set([...selectedAccountIds.value, ...filteredAccounts.value.map(account => account.id)])] }
function receiveOptions(value: PelicanOptions) {
  if (lifecycle.signal.aborted) return
  options.value = value
  const timestamp = Date.parse(value.generated_at)
  serverClockKnown.value = Number.isFinite(timestamp)
  if (serverClockKnown.value) serverOffsetMs.value = timestamp - Date.now()
  for (const account of value.accounts) {
    accountOptions.value[account.id] = account
    if (!accountModels.value[account.id]) addModel(account.id)
  }
}
async function loadAccountOptions(ids: number[]) {
  const missing = ids.filter(id => !accountOptions.value[id] && !pendingOptions.has(id))
  if (!missing.length || lifecycle.signal.aborted) return
  missing.forEach(id => pendingOptions.add(id))
  optionsLoading.value++
  try { receiveOptions(await pelicanTestsAPI.getAccountOptions(missing, lifecycle.signal)) }
  catch (value) { if (!lifecycle.signal.aborted) error.value = extractApiErrorMessage(value, t('admin.codexGatewayBorrow.loadFailed')) }
  finally { missing.forEach(id => pendingOptions.delete(id)); optionsLoading.value-- }
}
watch(selectedAccountIds, ids => { void loadAccountOptions(ids) }, { deep: true })
async function loadInventory() {
  const found: AccountListItem[] = []
  for (let page = 1; !lifecycle.signal.aborted; page++) {
    const response = await accountsAPI.list(page, 100, undefined, { signal: lifecycle.signal })
    found.push(...response.items)
    if (!response.items.length || page * 100 >= response.total) break
  }
  if (!lifecycle.signal.aborted) accounts.value = found
}
async function refreshInventory() {
  if (refreshing.value || loading.value || running.value) return
  refreshing.value = true
  error.value = ''
  const outcomes = await Promise.allSettled([loadInventory(), pelicanTestsAPI.getOptions(lifecycle.signal).then(receiveOptions),
    selectedAccountIds.value.length ? pelicanTestsAPI.getAccountOptions(selectedAccountIds.value, lifecycle.signal).then(receiveOptions) : Promise.resolve()])
  reportLoadErrors(outcomes)
  refreshing.value = false
}
function reportLoadErrors(outcomes: PromiseSettledResult<unknown>[]) {
  if (lifecycle.signal.aborted) return
  const failures = outcomes.filter((outcome): outcome is PromiseRejectedResult => outcome.status === 'rejected')
  if (failures.length) error.value = failures.map(outcome => extractApiErrorMessage(outcome.reason, t('admin.codexGatewayBorrow.loadFailed'))).join('\n')
}
function formatTime(value?: string) { if (!value || value.startsWith('0001-01-01')) return '—'; const date = new Date(value); return Number.isNaN(date.getTime()) ? value : date.toLocaleString() }
function formatDuration(ms?: number) { return typeof ms === 'number' ? `${(ms / 1000).toFixed(1)} s` : '—' }
function resultStatusClass(value: string) {
  if (value === 'complete') return 'bg-emerald-50 text-emerald-800 dark:bg-emerald-950 dark:text-emerald-200'
  if (value === 'failed' || value === 'incomplete') return 'bg-red-50 text-red-800 dark:bg-red-950 dark:text-red-200'
  return 'bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-gray-200'
}
function previewTitle(result: PelicanTestResult) { return `${result.account_name || accountName(result.account_id)} #${result.account_id} · ${result.model_id} · ${result.effort || t('admin.pelicanTests.normalDefault')}` }
function previewPlaceholder(result: PelicanTestResult) {
  if (['pending', 'running'].includes(result.status)) return t('admin.codexGatewayBorrow.previewPending')
  return t(`admin.codexGatewayBorrow.${result.status === 'complete' ? 'previewMissingHtml' : 'previewNotGenerated'}`)
}
function setPreviewElement(id: string, element: Element | ComponentPublicInstance | null) {
  const previous = previewElements.get(id)
  if (previous === element) return
  if (previous) previewObserver?.unobserve(previous)
  if (element instanceof HTMLElement) {
    previewElements.set(id, element)
    if (previewObserver) previewObserver.observe(element)
    else if (typeof IntersectionObserver === 'undefined') visiblePreviews.value.add(id)
  } else { previewElements.delete(id); visiblePreviews.value.delete(id) }
}
async function changeTabWithKeyboard(event: KeyboardEvent) {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
  event.preventDefault()
  activeTab.value = event.key === 'Home' ? 'results' : event.key === 'End' ? 'history' : activeTab.value === 'results' ? 'history' : 'results'
  await nextTick()
  document.getElementById(`pelican-${activeTab.value}-tab`)?.focus()
}
function receiveTestEvent(event: PelicanTestEvent) {
  if (testController?.signal.aborted || lifecycle.signal.aborted) return
  if (event.type === 'task_start' || event.type === 'task_complete') activeTask.value = event.task
  else if (event.type === 'result_started' || event.type === 'result_complete' || event.type === 'result_phase') {
    if (!activeTask.value || activeTask.value.id !== event.task_id) return
    const next = [...(activeTask.value.results || [])]
    const index = next.findIndex(result => result.id === event.result.id)
    const result = event.type === 'result_phase' ? { ...event.result, phase: event.phase } : event.result
    if (index >= 0) next[index] = result
    else next.push(result)
    activeTask.value = { ...activeTask.value, results: next, completed: next.filter(value => !['pending', 'running'].includes(value.status)).length }
  }
  syncResultDialogs()
}
function syncResultDialogs() {
  if (detailResult.value) detailResult.value = results.value.find(result => result.id === detailResult.value?.id) || detailResult.value
  if (enlarged.value) enlarged.value = results.value.find(result => result.id === enlarged.value?.id) || enlarged.value
}
async function startTests() {
  if (!canRunTests.value) return
  const request: PelicanTestRequest = { client_task_id: createPelicanClientTaskId(serverOffsetMs.value), generation_timeout_seconds: budgetMinutes.value * 60, targets: testTargets.value.map(target => ({ ...target })) }
  testController = new AbortController()
  running.value = true
  testError.value = ''
  testStopped.value = false
  detailResult.value = null
  enlarged.value = null
  activeTab.value = 'results'
  activeTask.value = { id: request.client_task_id, client_task_id: request.client_task_id, status: 'pending', created_at: new Date(Date.now() + serverOffsetMs.value).toISOString(), expires_at: '', generation_timeout_seconds: request.generation_timeout_seconds, total: request.targets.length, completed: 0, results: request.targets.map((target, index) => ({ id: `${request.client_task_id}-${index}`, ...target, account_name: accountName(target.account_id), platform: accountById(target.account_id)?.platform, phase: 'queued', status: 'pending', raw_answer: '', raw_response: '', raw_html: '', html: '', error: '' })) }
  try { await pelicanTestsAPI.streamTests(request, receiveTestEvent, testController.signal) }
  catch (value) {
    if (!testController.signal.aborted && !lifecycle.signal.aborted) {
      testError.value = extractApiErrorMessage(value, t('admin.codexGatewayBorrow.testFailed'))
      if (activeTask.value) activeTask.value = { ...activeTask.value, status: 'incomplete', results: results.value.map(result => ['pending', 'running'].includes(result.status) ? { ...result, status: 'incomplete' as const } : result) }
    }
  } finally { running.value = false; if (!lifecycle.signal.aborted) await loadHistory(1) }
}
function stopTests() {
  testController?.abort()
  testStopped.value = true
  if (activeTask.value) {
    const next = results.value.map(result => ['pending', 'running'].includes(result.status) ? { ...result, status: 'cancelled' as const } : result)
    activeTask.value = { ...activeTask.value, status: 'cancelled', results: next, completed: next.length }
    syncResultDialogs()
  }
}
async function loadHistory(page = 1) {
  if (historyLoading.value || lifecycle.signal.aborted) return
  historyLoading.value = true
  historyError.value = ''
  try { const value = await pelicanTestsAPI.listTests(page, lifecycle.signal); if (!lifecycle.signal.aborted) { history.value = value; historyPage.value = page } }
  catch (value) { if (!lifecycle.signal.aborted) historyError.value = extractApiErrorMessage(value, t('admin.codexGatewayBorrow.historyFailed')) }
  finally { historyLoading.value = false }
}
async function openHistory(id: string) {
  if (running.value || historyLoading.value || lifecycle.signal.aborted) return
  const previousTask = activeTask.value
  historyLoading.value = true
  historyError.value = ''
  try {
    const task = await pelicanTestsAPI.getTest(id, lifecycle.signal)
    if (!lifecycle.signal.aborted && !running.value && activeTask.value === previousTask) { activeTask.value = task; activeTab.value = 'results'; testStopped.value = false; testError.value = ''; detailResult.value = null; enlarged.value = null }
  } catch (value) { if (!lifecycle.signal.aborted) historyError.value = extractApiErrorMessage(value, t('admin.codexGatewayBorrow.historyFailed')) }
  finally { historyLoading.value = false }
}
function downloadText(text: string, name: string, type: string) {
  const url = URL.createObjectURL(new Blob([text], { type }))
  const link = document.createElement('a')
  link.href = url; link.download = name; link.click()
  setTimeout(() => URL.revokeObjectURL(url), 0)
}
function downloadHtml(result: PelicanTestResult) { downloadText(result.raw_html || result.html, `pelican-${result.account_id}-${result.model_id}-${result.id}.html`, 'text/html;charset=utf-8') }
function downloadAnswer(result: PelicanTestResult) { downloadText(result.raw_answer, `pelican-${result.account_id}-${result.model_id}-${result.id}.txt`, 'text/plain;charset=utf-8') }

onMounted(async () => {
  if (typeof IntersectionObserver !== 'undefined') {
    previewObserver = new IntersectionObserver(entries => {
      for (const entry of entries) {
        const id = (entry.target as HTMLElement).dataset.previewResult
        if (id) { if (entry.isIntersecting) visiblePreviews.value.add(id); else visiblePreviews.value.delete(id) }
      }
    }, { rootMargin: '0px', threshold: 0 })
    previewElements.forEach(element => previewObserver?.observe(element))
  }
  const outcomes = await Promise.allSettled([pelicanTestsAPI.getOptions(lifecycle.signal).then(receiveOptions), loadInventory(), loadHistory()])
  reportLoadErrors(outcomes)
  if (!lifecycle.signal.aborted) loading.value = false
})
onBeforeUnmount(() => { lifecycle.abort(); testController?.abort(); previewObserver?.disconnect(); previewElements.clear() })
</script>

<style scoped>
.pelican-account-list { max-height: 28rem; overflow: auto; align-items: start; }
.pelican-account-error { max-height: 4.5rem; overflow: auto; white-space: pre-wrap; overflow-wrap: anywhere; font-size: 0.75rem; line-height: 1.5; color: rgb(153 27 27); }
.pelican-result-grid { --pelican-card-width: 200px; display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, var(--pelican-card-width)), 1fr)); gap: 8px; align-items: start; }
.pelican-result-grid[data-size="compact"] { --pelican-card-width: 160px; }
.pelican-result-grid[data-size="large"] { --pelican-card-width: 280px; }
.pelican-result-card { width: 100%; min-width: 0; }
@media (max-width: 639px) { .pelican-result-grid[data-size="compact"] { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
.pelican-error { max-height: 24rem; overflow: auto; white-space: pre-wrap; overflow-wrap: anywhere; border-radius: 0.5rem; padding: 0.65rem; font-size: 0.75rem; line-height: 1.5; color: rgb(153 27 27); background: rgb(254 242 242); }
:global(.dark) .pelican-error, :global(.dark) .pelican-account-error { color: rgb(254 202 202); }
:global(.dark) .pelican-error { background: rgb(69 10 10); }
.pelican-source { max-height: 24rem; overflow: auto; white-space: pre-wrap; overflow-wrap: anywhere; border-radius: 0.375rem; padding: 0.75rem; font-size: 0.75rem; color: rgb(229 231 235); background: rgb(3 7 18); line-height: 1.5; }
</style>
