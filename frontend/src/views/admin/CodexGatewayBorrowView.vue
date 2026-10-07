<template>
  <AppLayout>
    <div class="mx-auto max-w-[1664px] space-y-6 px-1" data-ui="codex-gateway-borrow">
      <header class="flex flex-wrap items-start justify-between gap-3 border-b border-line pb-4">
        <div class="min-w-0 space-y-1">
          <h1 class="text-xl font-semibold">{{ t('admin.codexGatewayBorrow.title') }}</h1>
          <p class="max-w-4xl text-sm text-muted">{{ t('admin.codexGatewayBorrow.description') }}</p>
        </div>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="loading || refreshing" data-test="borrow-refresh" @click="refreshStatus">
          <Icon name="refresh" size="sm" aria-hidden="true" />{{ t('admin.codexGatewayBorrow.refreshStatus') }}
        </button>
      </header>
      <pre v-if="error" role="alert" class="borrow-error">{{ error }}</pre>
      <p v-if="loading" class="py-8 text-center text-sm text-muted">{{ t('common.loading') }}</p>

      <section v-if="draft" class="card space-y-4 p-4 sm:p-5" aria-labelledby="borrow-config-title">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <h2 id="borrow-config-title" class="font-semibold">{{ t('admin.codexGatewayBorrow.configuration') }}</h2>
          <button type="button" class="btn btn-primary btn-sm" :disabled="!dirty || !!configError || saving || running || !!actionBusy" data-test="borrow-save" @click="saveConfig">
            {{ saving ? t('common.saving') : t('common.save') }}
          </button>
        </div>
        <label class="flex items-center gap-3">
          <Toggle v-model="draft.enabled" :disabled="saving || running || !!actionBusy" :aria-label="t('admin.codexGatewayBorrow.enabled')" data-test="borrow-enabled" />
          <span class="text-sm font-medium">{{ t('admin.codexGatewayBorrow.enabled') }}</span>
        </label>
        <p class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.saveHint') }}</p>
        <fieldset :disabled="saving || running || !!actionBusy" class="flex flex-wrap items-center gap-4">
          <legend class="mb-2 text-sm font-medium">{{ t('admin.codexGatewayBorrow.models') }}</legend>
          <label v-for="model in BORROW_MODELS" :key="model" class="flex items-center gap-2 text-sm">
            <input v-model="draft.models" type="checkbox" class="checkbox" :value="model" :data-test="`borrow-config-model-${model}`" />{{ model }}
          </label>
        </fieldset>
        <div class="grid gap-4 lg:grid-cols-2">
          <fieldset v-for="role in accountRoles" :key="role.key" :disabled="saving || running || !!actionBusy" class="min-w-0 space-y-2">
            <legend class="text-sm font-medium">{{ t(role.label) }} · {{ draft[role.key].length }}</legend>
            <p class="text-xs text-muted">{{ t(role.hint) }}</p>
            <input v-model="accountSearch[role.key]" type="search" class="input w-full" :aria-label="t('admin.codexGatewayBorrow.searchAccounts')" :placeholder="t('admin.codexGatewayBorrow.searchAccounts')" />
            <div class="max-h-72 overflow-auto rounded-lg border border-line">
              <label v-for="account in filteredAccounts(role.key)" :key="account.id" class="flex cursor-pointer items-start gap-3 border-b border-line px-3 py-2.5 last:border-b-0 hover:bg-gray-50 dark:hover:bg-dark-800">
                <input
                  v-model="draft[role.key]" type="checkbox" class="checkbox mt-1 shrink-0" :value="account.id"
                  :disabled="draft[role.other].includes(account.id)" :data-test="`borrow-${role.key}-${account.id}`"
                />
                <span class="min-w-0 flex-1 space-y-1">
                  <span class="block break-words text-sm font-medium">{{ account.name }} <span class="font-normal text-muted">#{{ account.id }}</span></span>
                  <span class="block text-xs text-muted">{{ accountStateLabel(account) }} · {{ t('admin.codexGatewayBorrow.proxy') }}: {{ proxyLabel(account) }}</span>
                  <span v-if="account.parent_account_id" class="block text-xs text-muted">{{ t('admin.codexGatewayBorrow.shadowOf', { id: account.parent_account_id, name: accountName(account.parent_account_id) }) }}</span>
                  <span class="block break-words text-xs text-muted">WS: {{ wsLabel(account) }} · {{ modelMappingLabel(account) }}</span>
                  <pre v-if="account.error_message" class="borrow-error">{{ account.error_message }}</pre>
                </span>
              </label>
              <p v-if="filteredAccounts(role.key).length === 0" class="p-4 text-sm text-muted">{{ t('admin.codexGatewayBorrow.noAccounts') }}</p>
            </div>
            <div v-if="unknownSelectedAccounts(role.key).length" class="space-y-2 text-xs text-amber-700 dark:text-amber-300"><p>{{ t('admin.codexGatewayBorrow.missingAccounts', { ids: unknownSelectedAccounts(role.key).join(', ') }) }}</p><label v-for="id in unknownSelectedAccounts(role.key)" :key="id" class="flex items-center gap-2"><input v-model="draft[role.key]" type="checkbox" class="checkbox" :value="id" />{{ t('admin.codexGatewayBorrow.unknownAccount') }} #{{ id }}</label></div>
          </fieldset>
        </div>
        <p class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.localInventoryHint') }}</p>
        <p v-if="configError" role="alert" class="text-sm text-red-700 dark:text-red-300">{{ configError }}</p>
      </section>

      <section v-if="status" class="card space-y-4 p-4 sm:p-5" aria-labelledby="borrow-status-title">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div class="space-y-1">
            <h2 id="borrow-status-title" class="font-semibold">{{ t('admin.codexGatewayBorrow.borrowStatus') }}</h2>
            <p class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.observedAt') }}: {{ formatTime(status.generated_at) }} · {{ status.enabled ? t('admin.codexGatewayBorrow.on') : t('admin.codexGatewayBorrow.off') }} <span v-if="status.preparing">· {{ t('admin.codexGatewayBorrow.preparing') }}</span></p>
          </div>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="!status.enabled || !!actionBusy || running || saving || dirty" data-test="borrow-prepare" @click="prepare">
            {{ actionBusy === 'prepare' ? t('admin.codexGatewayBorrow.preparing') : t('admin.codexGatewayBorrow.prepare') }}
          </button>
        </div>
        <p class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.prepareHint') }}</p>
        <dl v-if="status.candidate" class="grid gap-3 rounded-lg border border-line bg-gray-50 p-3 text-sm dark:bg-dark-900 sm:grid-cols-3" data-test="borrow-candidate">
          <div><dt class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.sharedCandidate') }}</dt><dd class="mt-1 break-words">{{ accountName(status.candidate.source_account_id) }} #{{ status.candidate.source_account_id }}</dd></div>
          <div><dt class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.expiresAt') }}</dt><dd class="mt-1">{{ formatTime(status.candidate.expires_at) }} · {{ t('admin.codexGatewayBorrow.remainingSeconds', { seconds: status.candidate.remaining_seconds }) }}</dd></div>
          <div><dt class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.candidateFingerprint') }}</dt><dd class="mt-1 break-all font-mono text-xs">{{ status.candidate.cookie_fingerprint || '—' }}</dd></div>
        </dl>
        <p v-else class="rounded-lg border border-dashed border-line p-3 text-sm text-muted">{{ t('admin.codexGatewayBorrow.noCandidate') }}</p>
        <details v-if="status.sources.length" class="rounded-lg border border-line p-3">
          <summary class="cursor-pointer text-sm font-medium">{{ t('admin.codexGatewayBorrow.sourceStates') }} ({{ status.sources.length }})</summary>
          <div v-for="source in status.sources" :key="source.account_id" class="space-y-1 border-t border-line py-3 first:mt-3 last:pb-0">
            <p class="text-sm">{{ accountName(source.account_id) }} #{{ source.account_id }} · {{ stateLabel(source.state) }}</p>
            <p class="break-words text-xs text-muted">{{ source.reason }} <span v-if="source.expires_at">· {{ formatTime(source.expires_at) }}</span></p>
            <pre v-if="source.error" class="borrow-error">{{ source.error }}</pre>
          </div>
        </details>
        <div v-if="status.targets.length" class="overflow-x-auto rounded-lg border border-line">
          <table class="w-full min-w-[680px] text-left text-sm">
            <thead class="border-b border-line bg-gray-50 text-xs text-muted dark:bg-dark-900"><tr>
              <th class="p-3">{{ t('admin.codexGatewayBorrow.accountModel') }}</th>
              <th class="p-3">{{ t('admin.codexGatewayBorrow.validation') }}</th>
              <th class="p-3">{{ t('admin.codexGatewayBorrow.expiryCooldown') }}</th>
              <th class="p-3">{{ t('admin.codexGatewayBorrow.actions') }}</th>
            </tr></thead>
            <tbody>
              <tr v-for="target in status.targets" :key="pairKey(target.account_id, target.model)" class="border-b border-line align-top last:border-b-0" :data-test="`borrow-status-${target.account_id}-${target.model}`">
                <td class="p-3"><p class="break-words font-medium">{{ accountName(target.account_id) }} #{{ target.account_id }}</p><p class="mt-1 text-xs text-muted">{{ target.model }}</p><p class="mt-1 text-xs text-muted">{{ t('admin.codexGatewayBorrow.proxy') }}: {{ proxyLabel(accountById(target.account_id)) }}</p></td>
                <td class="max-w-lg space-y-1 p-3">
                  <span class="inline-block rounded px-2 py-1 text-xs font-medium" :class="cacheUsable(target) ? 'bg-emerald-50 text-emerald-800 dark:bg-emerald-950 dark:text-emerald-200' : 'bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-gray-200'">{{ stateLabel(target.state) }} · {{ cacheUsable(target) ? t('admin.codexGatewayBorrow.cacheReady') : t('admin.codexGatewayBorrow.cacheMissing') }}</span>
                  <p class="break-words text-xs text-muted">{{ target.reason }}</p>
                  <p class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.httpStatuses', { mint: target.mint_status || '—', continuation: target.continue_status || '—' }) }} · {{ t('admin.codexGatewayBorrow.newTicket') }}: {{ target.new_ticket ? t('common.yes') : t('common.no') }}</p>
                  <p v-if="target.reported_model" class="break-words text-xs text-muted">{{ t('admin.codexGatewayBorrow.reportedModel') }}: {{ target.reported_model }}</p>
                  <pre v-if="target.error" class="borrow-error">{{ target.error }}</pre>
                </td>
                <td class="space-y-1 p-3 text-xs text-muted"><p>{{ t('admin.codexGatewayBorrow.checkedAt') }}: {{ formatTime(target.checked_at) }}</p><p>{{ t('admin.codexGatewayBorrow.expiresAt') }}: {{ formatTime(target.expires_at) }}</p><p v-if="target.retry_after">{{ t('admin.codexGatewayBorrow.retryAfter') }}: {{ formatTime(target.retry_after) }}</p></td>
                <td class="p-3"><button type="button" class="btn btn-secondary btn-sm whitespace-nowrap" :disabled="!status.enabled || !!actionBusy || running || saving || dirty" :data-test="`borrow-verify-${target.account_id}-${target.model}`" @click="verify(target)">{{ actionBusy === pairKey(target.account_id, target.model) ? t('admin.codexGatewayBorrow.validating') : t('admin.codexGatewayBorrow.forceVerify') }}</button></td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-else class="text-sm text-muted">{{ t('admin.codexGatewayBorrow.noTargets') }}</p>
        <pre v-if="verificationError" role="alert" class="borrow-error">{{ verificationError }}</pre>
      </section>

      <section v-if="status" class="card space-y-4 p-4 sm:p-5" aria-labelledby="borrow-pelican-title">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div class="space-y-1"><h2 id="borrow-pelican-title" class="font-semibold">{{ t('admin.codexGatewayBorrow.pelicanTitle') }}</h2><p class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.pelicanHint') }}</p></div>
          <div class="flex flex-wrap gap-2">
            <button v-if="running" type="button" class="btn btn-secondary btn-sm" data-test="borrow-test-stop" @click="stopTests">{{ t('admin.codexGatewayBorrow.stop') }}</button>
            <button type="button" class="btn btn-primary btn-sm" :disabled="!canRunTests" data-test="borrow-test-start" @click="startTests"><Icon name="play" size="sm" aria-hidden="true" />{{ t('admin.codexGatewayBorrow.startTest', { count: testTargets.length }) }}</button>
          </div>
        </div>
        <p v-if="!serverClockKnown" class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.clockUnavailable') }}</p>
        <div class="rounded-lg border border-line bg-gray-50 p-3 dark:bg-dark-900"><label for="borrow-fixed-prompt" class="mb-2 block text-xs font-medium">{{ t('admin.codexGatewayBorrow.fixedPrompt') }}</label><textarea id="borrow-fixed-prompt" :value="PELICAN_BORROW_PROMPT" readonly rows="2" class="input w-full resize-none text-sm" data-test="borrow-fixed-prompt" /></div>
        <fieldset :disabled="running || !!actionBusy || saving" class="grid gap-3 sm:grid-cols-2">
          <legend class="mb-2 text-sm font-medium">{{ t('admin.codexGatewayBorrow.testModels') }}</legend>
          <div v-for="model in savedModels" :key="model" class="flex flex-wrap items-center gap-3 rounded-lg border border-line p-3">
            <label class="flex min-w-0 items-center gap-2 text-sm"><input v-model="selectedTestModels" type="checkbox" :value="model" class="checkbox" :data-test="`borrow-test-model-${model}`" />{{ model }}</label>
            <label class="ml-auto flex items-center gap-2 text-xs text-muted">{{ t('admin.codexGatewayBorrow.effort') }}<select v-model="modelEfforts[model]" class="input w-24 text-sm" :aria-label="`${model} ${t('admin.codexGatewayBorrow.effort')}`"><option v-for="effort in effortOptions(model)" :key="effort" :value="effort">{{ effort }}</option></select></label>
          </div>
        </fieldset>
        <fieldset :disabled="running || !!actionBusy || saving" class="space-y-2">
          <legend class="mb-2 text-sm font-medium">{{ t('admin.codexGatewayBorrow.testAccounts') }} · {{ selectedTestAccountIds.length }}</legend>
          <div class="grid gap-2 md:grid-cols-2 xl:grid-cols-3">
            <label v-for="id in status.config.target_account_ids" :key="id" class="flex cursor-pointer items-start gap-3 rounded-lg border border-line p-3">
              <input v-model="selectedTestAccountIds" type="checkbox" :value="id" class="checkbox mt-1" :data-test="`borrow-test-account-${id}`" />
              <span class="min-w-0 space-y-1"><span class="block break-words text-sm font-medium">{{ accountName(id) }} #{{ id }}</span><span class="block text-xs text-muted">{{ accountStateLabel(accountById(id)) }}</span><span v-for="model in activeTestModels" :key="model" class="block break-words text-xs" :class="cacheUsable(targetStatus(id, model)) ? 'text-emerald-700 dark:text-emerald-300' : 'text-muted'">{{ model }} · {{ cacheUsable(targetStatus(id, model)) ? t('admin.codexGatewayBorrow.cacheReady') : t('admin.codexGatewayBorrow.willSkip') }}</span></span>
            </label>
          </div>
        </fieldset>
        <p class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.executionHint') }}</p>
        <pre v-if="testError" role="alert" class="borrow-error">{{ testError }}</pre>
        <p v-if="testStopped" role="status" class="text-sm text-muted">{{ t('admin.codexGatewayBorrow.stoppedHint') }}</p>

        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-line pb-3">
          <div class="flex gap-2" role="group" :aria-label="t('admin.codexGatewayBorrow.resultViews')">
            <button type="button" class="btn btn-sm" :class="activeTab === 'results' ? 'btn-primary' : 'btn-secondary'" :aria-pressed="activeTab === 'results'" data-test="borrow-tab-results" @click="activeTab = 'results'">{{ t('admin.codexGatewayBorrow.results') }}</button>
            <button type="button" class="btn btn-sm" :class="activeTab === 'history' ? 'btn-primary' : 'btn-secondary'" :aria-pressed="activeTab === 'history'" data-test="borrow-tab-history" @click="activeTab = 'history'">{{ t('admin.codexGatewayBorrow.history') }}</button>
          </div>
          <p v-if="activeTask" class="text-xs text-muted" role="status">{{ activeTask.completed }} / {{ activeTask.total }} · {{ stateLabel(activeTask.status) }} <span v-if="activeTask.replayed">· {{ t('admin.codexGatewayBorrow.replayed') }}</span></p>
        </div>
        <template v-if="activeTab === 'history'">
          <div class="flex flex-wrap items-center justify-between gap-2"><p class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.historyHint') }}</p><button type="button" class="btn btn-secondary btn-sm" :disabled="historyLoading" data-test="borrow-history-refresh" @click="loadHistory(historyPage)">{{ t('admin.codexGatewayBorrow.refreshHistory') }}</button></div>
          <pre v-if="historyError" role="alert" class="borrow-error">{{ historyError }}</pre>
          <p v-if="historyLoading" class="text-sm text-muted">{{ t('common.loading') }}</p>
          <button v-for="task in history.items" :key="task.id" type="button" class="flex w-full flex-wrap items-center justify-between gap-2 rounded-lg border border-line p-3 text-left hover:bg-gray-50 dark:hover:bg-dark-800" :disabled="running || historyLoading" :data-test="`borrow-history-${task.id}`" @click="openHistory(task.id)">
            <span class="min-w-0"><span class="block text-sm font-medium">{{ formatTime(task.created_at) }} · {{ task.completed }} / {{ task.total }} · {{ stateLabel(task.status) }}</span><span class="mt-1 block break-all text-xs text-muted">{{ task.id }} · {{ t('admin.codexGatewayBorrow.expiresAt') }} {{ formatTime(task.expires_at) }}</span></span><span class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.viewResults') }}</span>
          </button>
          <p v-if="!historyLoading && !history.items.length" class="py-6 text-center text-sm text-muted">{{ t('admin.codexGatewayBorrow.noHistory') }}</p>
          <div v-if="history.total > 12" class="flex items-center justify-end gap-3"><button type="button" class="btn btn-secondary btn-sm" :disabled="historyPage <= 1 || historyLoading" @click="loadHistory(historyPage - 1)">{{ t('admin.codexGatewayBorrow.previous') }}</button><span class="text-xs text-muted">{{ historyPage }} / {{ Math.ceil(history.total / 12) }}</span><button type="button" class="btn btn-secondary btn-sm" :disabled="historyPage * 12 >= history.total || historyLoading" @click="loadHistory(historyPage + 1)">{{ t('common.next') }}</button></div>
        </template>
        <template v-else>
          <div v-if="activeTask" class="space-y-1 text-xs text-muted"><p class="break-all">{{ t('admin.codexGatewayBorrow.taskId') }}: {{ activeTask.id }} · {{ formatTime(activeTask.created_at) }}</p><p>{{ t('admin.codexGatewayBorrow.expiresAt') }}: {{ formatTime(activeTask.expires_at) }}</p><pre v-if="activeTask.error" class="borrow-error">{{ activeTask.error }}</pre></div>
          <p v-if="!results.length" class="rounded-lg border border-dashed border-line py-8 text-center text-sm text-muted">{{ t('admin.codexGatewayBorrow.noResults') }}</p>
          <div class="borrow-result-grid" data-test="borrow-result-grid">
            <article v-for="result in results" :key="result.id" class="borrow-result-card overflow-hidden rounded-xl border border-line bg-white dark:bg-dark-800" :data-test="`borrow-result-${result.id}`">
              <header class="space-y-2 border-b border-line px-3 py-2.5">
                <div class="flex items-start justify-between gap-2"><p class="min-w-0 break-words text-sm font-medium">{{ result.account_name || accountName(result.account_id) }} <span class="font-normal text-muted">#{{ result.account_id }}</span></p><span class="shrink-0 rounded px-2 py-0.5 text-xs font-medium" :class="resultStatusClass(result.status)">{{ stateLabel(result.status) }}</span></div>
                <div class="space-y-1 text-xs text-muted"><p class="break-words">{{ result.model_id }} · {{ result.effort || 'high' }}</p><p v-if="result.upstream_model && result.upstream_model !== result.model_id" class="break-words">{{ t('admin.codexGatewayBorrow.reportedModel') }}: {{ result.upstream_model }}</p><p>{{ t('admin.codexGatewayBorrow.duration') }}: {{ formatDuration(result.duration_ms) }} · {{ formatTime(result.started_at) }}</p></div>
              </header>
              <div class="aspect-[4/3] w-full bg-gray-50 dark:bg-dark-900">
                <BorrowPelicanPreview v-if="result.preview_url" :preview-url="result.preview_url" :title="previewTitle(result)" />
                <div v-else class="flex h-full items-center justify-center px-5 text-center text-xs text-muted"><span>{{ result.preview_unavailable || t('admin.codexGatewayBorrow.previewPending') }}</span></div>
              </div>
              <div class="space-y-3 border-t border-line p-3">
                <div class="flex flex-wrap gap-2"><button type="button" class="btn btn-secondary btn-sm" :disabled="!result.preview_url" :aria-label="`${t('admin.codexGatewayBorrow.enlarge')} ${previewTitle(result)}`" @click="enlarged = result">{{ t('admin.codexGatewayBorrow.enlarge') }}</button><button type="button" class="btn btn-secondary btn-sm" :disabled="!result.raw_html && !result.html" @click="downloadHtml(result)"><Icon name="download" size="sm" aria-hidden="true" />HTML</button></div>
                <pre v-if="result.error" class="borrow-error" data-test="borrow-result-error">{{ result.error }}</pre>
                <details v-if="result.raw_answer" class="text-xs"><summary class="cursor-pointer font-medium">{{ t('admin.codexGatewayBorrow.rawAnswer') }}</summary><pre class="borrow-source mt-2">{{ result.raw_answer }}</pre><button type="button" class="btn btn-secondary btn-sm mt-2" @click="downloadAnswer(result)">{{ t('admin.codexGatewayBorrow.downloadAnswer') }}</button></details>
                <details v-if="result.raw_html || result.html" class="text-xs"><summary class="cursor-pointer font-medium">{{ t('admin.codexGatewayBorrow.htmlSource') }}</summary><pre class="borrow-source mt-2">{{ result.raw_html || result.html }}</pre></details>
                <details v-if="result.raw_response" class="text-xs"><summary class="cursor-pointer font-medium">{{ t('admin.codexGatewayBorrow.rawResponse') }}</summary><pre class="borrow-source mt-2">{{ result.raw_response }}</pre></details>
              </div>
            </article>
          </div>
        </template>
      </section>
      <BaseDialog :show="!!enlarged" :title="enlarged ? previewTitle(enlarged) : t('admin.codexGatewayBorrow.enlarge')" width="extra-wide" @close="enlarged = null">
        <div v-if="enlarged?.preview_url" class="aspect-[4/3] max-h-[70vh] w-full"><BorrowPelicanPreview :preview-url="enlarged.preview_url" :title="previewTitle(enlarged)" interactive /></div>
      </BaseDialog>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'
import BorrowPelicanPreview from '@/components/admin/codex/BorrowPelicanPreview.vue'
import { accountsAPI } from '@/api/admin/accounts'
import {
  BORROW_MODELS, PELICAN_BORROW_PROMPT, codexGatewayBorrowAPI, createBorrowClientTaskId,
  type CodexGatewayBorrowConfig, type CodexGatewayBorrowStatus, type BorrowTargetStatus,
  type BorrowTestEvent, type BorrowTestHistory, type BorrowTestRequest, type BorrowTestResult, type BorrowTestTask
} from '@/api/admin/codexGatewayBorrow'
import type { AccountListItem } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { resolveOpenAIWSModeFromExtra } from '@/utils/openaiWsMode'

const { t, te } = useI18n()
const appStore = useAppStore()
const lifecycle = new AbortController()
const accounts = ref<AccountListItem[]>([])
const saved = ref<CodexGatewayBorrowConfig | null>(null)
const draft = ref<CodexGatewayBorrowConfig | null>(null)
const status = ref<CodexGatewayBorrowStatus | null>(null)
const loading = ref(true)
const refreshing = ref(false)
const saving = ref(false)
const error = ref('')
const actionBusy = ref('')
const verificationError = ref('')
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
const serverClockKnown = ref(false)
let testController: AbortController | undefined
let serverOffsetMs = 0

type AccountRole = 'source_account_ids' | 'target_account_ids'
const accountRoles: Array<{ key: AccountRole; other: AccountRole; label: string; hint: string }> = [
  { key: 'source_account_ids', other: 'target_account_ids', label: 'admin.codexGatewayBorrow.sources', hint: 'admin.codexGatewayBorrow.sourcesHint' },
  { key: 'target_account_ids', other: 'source_account_ids', label: 'admin.codexGatewayBorrow.targets', hint: 'admin.codexGatewayBorrow.targetsHint' }
]
const accountSearch = ref<Record<AccountRole, string>>({ source_account_ids: '', target_account_ids: '' })
const accountMap = computed(() => new Map(accounts.value.map(account => [account.id, account])))
const dirty = computed(() => !!draft.value && !!saved.value && JSON.stringify(normalizeConfig(draft.value)) !== JSON.stringify(normalizeConfig(saved.value)))
const configError = computed(() => {
  if (!draft.value) return ''
  if (draft.value.source_account_ids.some(id => draft.value!.target_account_ids.includes(id))) return t('admin.codexGatewayBorrow.disjointRequired')
  if (draft.value.enabled && (!draft.value.source_account_ids.length || !draft.value.target_account_ids.length || !draft.value.models.length)) return t('admin.codexGatewayBorrow.selectionRequired')
  return ''
})
const savedModels = computed(() => BORROW_MODELS.filter(model => status.value?.config.models.includes(model)))
const activeTestModels = computed(() => savedModels.value.filter(model => selectedTestModels.value.includes(model)))
const testTargets = computed(() => [...new Set(selectedTestAccountIds.value)]
  .filter(id => status.value?.config.target_account_ids.includes(id))
  .flatMap(account_id => activeTestModels.value.map(model_id => ({ account_id, model_id, effort: modelEfforts.value[model_id] || 'high' }))))
const canRunTests = computed(() => !!status.value?.enabled && serverClockKnown.value && testTargets.value.length > 0 && !running.value && !saving.value && !actionBusy.value && !dirty.value)
const results = computed(() => activeTask.value?.results || [])

function normalizeConfig(value: CodexGatewayBorrowConfig): CodexGatewayBorrowConfig {
  return { enabled: value.enabled, source_account_ids: [...value.source_account_ids].sort((a, b) => a - b), target_account_ids: [...value.target_account_ids].sort((a, b) => a - b), models: [...value.models].sort() }
}
function receiveConfig(value: CodexGatewayBorrowConfig) {
  if (typeof value?.enabled !== 'boolean' || !Array.isArray(value.source_account_ids) || !Array.isArray(value.target_account_ids) || !Array.isArray(value.models)) throw new Error(t('admin.codexGatewayBorrow.invalidConfig'))
  saved.value = normalizeConfig(value)
  draft.value = normalizeConfig(value)
}
function receiveStatus(value: CodexGatewayBorrowStatus) {
  status.value = value
  const serverTime = Date.parse(value.generated_at)
  serverClockKnown.value = Number.isFinite(serverTime)
  if (serverClockKnown.value) serverOffsetMs = serverTime - Date.now()
}
function accountById(id: number) { return accountMap.value.get(id) }
function accountName(id: number) { return accountById(id)?.name || t('admin.codexGatewayBorrow.unknownAccount') }
function filteredAccounts(role: AccountRole) {
  const search = accountSearch.value[role].trim().toLowerCase()
  return accounts.value.filter(account => !search || `${account.name} ${account.id}`.toLowerCase().includes(search))
}
function unknownSelectedAccounts(role: AccountRole) { return draft.value?.[role].filter(id => !accountMap.value.has(id)) || [] }
function accountStateLabel(account?: AccountListItem) {
  if (!account) return t('admin.codexGatewayBorrow.unknownAccount')
  const parts = [stateLabel(account.status)]
  if (!account.schedulable) parts.push(t('admin.codexGatewayBorrow.paused'))
  if (account.rate_limit_reset_at && Date.parse(account.rate_limit_reset_at) > Date.now() + serverOffsetMs) parts.push(t('admin.codexGatewayBorrow.rateLimited'))
  return parts.join(' · ')
}
function proxyLabel(account?: AccountListItem) {
  if (!account) return '—'
  if (account.proxy) return `${account.proxy.name} #${account.proxy.id}`
  if (account.proxy_id) return `#${account.proxy_id}`
  return t('admin.codexGatewayBorrow.direct')
}
function wsLabel(account: AccountListItem) {
  const extra = account.extra
  const keys = ['openai_oauth_responses_websockets_v2_mode', 'openai_oauth_responses_websockets_v2_enabled', 'responses_websockets_v2_enabled', 'openai_ws_enabled']
  if (!keys.some(key => extra?.[key] !== undefined)) return t('admin.codexGatewayBorrow.wsUnspecified')
  return resolveOpenAIWSModeFromExtra(extra, { modeKey: keys[0], enabledKey: keys[1], fallbackEnabledKeys: keys.slice(2) })
}
function modelMappingLabel(account: AccountListItem) {
  const mapping = account.credentials?.model_mapping
  if (!mapping || typeof mapping !== 'object' || !Object.keys(mapping).length) return t('admin.codexGatewayBorrow.noModelRestriction')
  const entries = Object.entries(mapping).filter((entry): entry is [string, string] => typeof entry[1] === 'string')
  return `${t('admin.codexGatewayBorrow.localMappings')}: ${entries.map(([from, to]) => from === to ? from : `${from} → ${to}`).join(', ')}`
}
function pairKey(id: number, model: string) { return `${id}:${model}` }
function targetStatus(id: number, model: string) { return status.value?.targets.find(target => target.account_id === id && target.model === model) }
function cacheUsable(target?: BorrowTargetStatus) {
  return !!target?.cache_valid && (!target.expires_at || Date.parse(target.expires_at) > Date.now() + serverOffsetMs)
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

async function loadAccounts() {
  const found: AccountListItem[] = []
  for (let page = 1; !lifecycle.signal.aborted; page++) {
    const response = await accountsAPI.list(page, 100, { platform: 'openai', types: 'oauth,setup-token' }, { signal: lifecycle.signal })
    found.push(...response.items.filter(account => account.platform === 'openai' && (account.type === 'oauth' || account.type === 'setup-token')))
    if (!response.items.length || page * 100 >= response.total) break
  }
  if (!lifecycle.signal.aborted) accounts.value = found
}

async function refreshStatus() {
  if (refreshing.value) return
  refreshing.value = true
  try { receiveStatus(await codexGatewayBorrowAPI.getStatus(lifecycle.signal)) }
  catch (value) { if (!lifecycle.signal.aborted) error.value = extractApiErrorMessage(value, t('admin.codexGatewayBorrow.loadFailed')) }
  finally { refreshing.value = false }
}
async function saveConfig() {
  if (!draft.value || !dirty.value || configError.value || saving.value || running.value || actionBusy.value) return
  saving.value = true
  error.value = ''
  try {
    receiveConfig(await codexGatewayBorrowAPI.saveConfig(normalizeConfig(draft.value), lifecycle.signal))
    appStore.showSuccess(t('admin.codexGatewayBorrow.saved'))
    await refreshStatus()
  } catch (value) { if (!lifecycle.signal.aborted) error.value = extractApiErrorMessage(value, t('admin.codexGatewayBorrow.saveFailed')) }
  finally { saving.value = false }
}
async function prepare() {
  if (actionBusy.value || running.value || dirty.value || !status.value?.enabled) return
  actionBusy.value = 'prepare'
  verificationError.value = ''
  try { await codexGatewayBorrowAPI.prepare(lifecycle.signal) }
  catch (value) { if (!lifecycle.signal.aborted) verificationError.value = extractApiErrorMessage(value, t('admin.codexGatewayBorrow.prepareFailed')) }
  finally { actionBusy.value = ''; if (!lifecycle.signal.aborted) await refreshStatus() }
}
async function verify(target: BorrowTargetStatus) {
  if (actionBusy.value || running.value || dirty.value || !status.value?.enabled) return
  actionBusy.value = pairKey(target.account_id, target.model)
  verificationError.value = ''
  try {
    const result = await codexGatewayBorrowAPI.verify(target.account_id, target.model, lifecycle.signal)
    if (!result.success) verificationError.value = result.error || result.reason
  } catch (value) { if (!lifecycle.signal.aborted) verificationError.value = extractApiErrorMessage(value, t('admin.codexGatewayBorrow.verifyFailed')) }
  finally { actionBusy.value = ''; if (!lifecycle.signal.aborted) await refreshStatus() }
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
  const request: BorrowTestRequest = { client_task_id: createBorrowClientTaskId(serverOffsetMs), targets: testTargets.value.map(target => ({ ...target })) }
  testController = new AbortController()
  running.value = true
  testError.value = ''
  testStopped.value = false
  enlarged.value = null
  activeTab.value = 'results'
  activeTask.value = { id: request.client_task_id, client_task_id: request.client_task_id, status: 'pending', created_at: new Date().toISOString(), expires_at: '', total: request.targets.length, completed: 0, results: request.targets.map((target, index) => ({ id: `${request.client_task_id}-${index}`, ...target, account_name: accountName(target.account_id), status: 'pending', raw_answer: '', raw_response: '', raw_html: '', html: '', error: '' })) }
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
  if (activeTask.value) activeTask.value = { ...activeTask.value, status: 'cancelled', results: results.value.map(result => ['pending', 'running'].includes(result.status) ? { ...result, status: 'cancelled' as const } : result) }
}
async function loadHistory(page = 1) {
  if (historyLoading.value) return
  historyLoading.value = true
  historyError.value = ''
  try { history.value = await codexGatewayBorrowAPI.listTests(page, lifecycle.signal); historyPage.value = page }
  catch (value) { if (!lifecycle.signal.aborted) historyError.value = extractApiErrorMessage(value, t('admin.codexGatewayBorrow.historyFailed')) }
  finally { historyLoading.value = false }
}
async function openHistory(id: string) {
  if (running.value || historyLoading.value) return
  historyLoading.value = true
  historyError.value = ''
  try { activeTask.value = await codexGatewayBorrowAPI.getTest(id, lifecycle.signal); activeTab.value = 'results'; testStopped.value = false; testError.value = '' }
  catch (value) { if (!lifecycle.signal.aborted) historyError.value = extractApiErrorMessage(value, t('admin.codexGatewayBorrow.historyFailed')) }
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
    codexGatewayBorrowAPI.getConfig(lifecycle.signal).then(receiveConfig),
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
