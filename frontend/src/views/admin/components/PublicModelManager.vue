<template>
  <div class="public-manager space-y-5 pb-5" data-test="public-model-manager">
    <div v-if="error" role="alert" class="manager-error">
      <p>{{ error }}</p>
      <button v-if="!assistantOpen" type="button" class="mt-2 font-medium underline" @click="refreshOverview">{{ t('common.refresh') }}</button>
    </div>

    <section v-if="overview" class="grid grid-cols-2 gap-3 xl:grid-cols-4" :aria-label="mt('summary')">
      <div class="manager-stat"><span>{{ mt('publishedModels') }}</span><strong data-test="manager-published-count">{{ overview.totals.published_model_count }}</strong><small>{{ mt('publishedCountHint') }}</small></div>
      <div class="manager-stat"><span>{{ mt('verifiedAccounts') }}</span><strong data-test="manager-verified-count">{{ overview.totals.verified_account_count }}</strong><small>{{ mt('verifiedCountHint') }}</small></div>
      <div class="manager-stat"><span>{{ mt('readyAccounts') }}</span><strong>{{ overview.totals.routing_ready_account_count }}</strong><small>{{ mt('readyCountHint') }}</small></div>
      <div class="manager-stat"><span>{{ mt('needsAttention') }}</span><strong class="text-amber-600 dark:text-amber-300">{{ overview.totals.attention_count }}</strong><small>{{ mt('attentionCountHint') }}</small></div>
    </section>

    <section class="manager-intro">
      <div class="min-w-0 flex-1">
        <h2 class="font-semibold text-gray-900 dark:text-white">{{ mt('gettingStarted') }}</h2>
        <p class="mt-1 text-sm leading-relaxed text-gray-600 dark:text-dark-300">{{ mt('gettingStartedHint') }}</p>
        <p class="mt-2 text-xs text-gray-500 dark:text-dark-300">{{ mt('readOnlyHint') }}</p>
      </div>
      <button type="button" class="btn btn-primary shrink-0" :disabled="loading || busy || !overview?.accounts.length" data-test="manager-organize" @click="organizeCurrentScope">{{ run ? mt('continueAssistant') : selectedGroup ? mt('organizeGroup') : mt('organizeAll') }}</button>
    </section>

    <section v-if="run && !assistantOpen" class="manager-run-banner" role="status">
      <div><p class="font-medium">{{ activeRun ? mt('checkInProgress') : mt('checkFinished') }}</p><p class="mt-1 text-xs">{{ mt('runProgress', { done: run.processed_count, total: run.target_count, requests: run.request_count }) }}</p></div>
      <button type="button" class="btn btn-secondary" @click="assistantOpen = true">{{ mt('continueAssistant') }}</button>
    </section>

    <div class="flex flex-wrap items-center justify-between gap-3">
      <div class="flex min-w-0 flex-wrap items-center gap-2">
        <button v-if="selectedGroup" type="button" class="manager-link" data-test="manager-back-groups" @click="selectedGroupKey = ''">{{ mt('backToGroups') }}</button>
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ selectedGroup?.name ?? mt('yourGroups') }}</h2>
        <span v-if="selectedGroup" class="text-xs text-gray-500">×{{ selectedGroup.rate_multiplier }}</span>
      </div>
      <label class="relative min-w-0 sm:w-72"><span class="sr-only">{{ mt('searchModels') }}</span><input v-model="search" type="search" class="input w-full" data-test="manager-model-search" :placeholder="mt('searchModels')" /></label>
    </div>

    <div v-if="loading && !overview" class="card p-10 text-center text-sm text-gray-500" role="status">{{ t('common.loading') }}</div>
    <div v-else-if="!folderIds.length && !accountIds.length" class="manager-empty"><h3>{{ mt('chooseSources') }}</h3><p>{{ mt('chooseSourcesHint') }}</p></div>
    <div v-else-if="overview && !overview.accounts.length" class="manager-empty"><h3>{{ mt('noAccounts') }}</h3><p>{{ mt('noAccountsHint') }}</p><RouterLink to="/admin/accounts" class="manager-link">{{ mt('openAccounts') }}</RouterLink></div>
    <div v-else-if="overview && !overview.groups.length" class="manager-empty"><h3>{{ mt('noModels') }}</h3><p>{{ mt('noModelsHint') }}</p><button type="button" class="btn btn-secondary" @click="emit('advanced')">{{ mt('openAdvanced') }}</button></div>
    <div v-else-if="!selectedGroup && !search.trim()" class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3" data-test="manager-group-grid">
      <button v-for="group in overview?.groups ?? []" :key="groupKey(group)" type="button" class="manager-group-card" :data-group="group.name" @click="selectedGroupKey = groupKey(group)">
        <div class="flex items-start justify-between gap-3"><h3 class="min-w-0 break-words font-semibold text-gray-900 dark:text-white">{{ group.name }}</h3><span :class="['manager-badge', group.attention_count ? 'manager-badge-amber' : 'manager-badge-green']">{{ group.attention_count ? mt('attentionItems', { count: group.attention_count }) : mt('noGaps') }}</span></div>
        <p class="mt-4 text-sm text-gray-700 dark:text-dark-200">{{ mt('groupPublished', { count: group.published_model_count }) }}</p>
        <p class="mt-1 text-xs text-gray-500 dark:text-dark-300">{{ mt('groupAccounts', { passed: group.verified_account_count, ready: group.routing_ready_account_count }) }}</p>
        <div class="mt-4 flex items-center justify-between gap-2 text-xs"><span class="text-gray-500">{{ sourceNames }}</span><span class="font-medium text-primary-600 dark:text-primary-300">{{ mt('viewModels') }} →</span></div>
      </button>
    </div>

    <div v-else-if="displayGroups.length" class="space-y-5">
      <section v-for="group in displayGroups" :key="groupKey(group)" class="space-y-3">
        <h3 v-if="!selectedGroup" class="font-medium text-gray-700 dark:text-dark-200">{{ group.name }}</h3>
        <article v-for="model in matchingModels(group)" :key="model.public_model" class="manager-model-card" data-test="public-model-row" :data-model="model.public_model">
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div class="min-w-0"><h3 class="break-words font-semibold text-gray-900 dark:text-white">{{ model.public_model }}</h3><span v-if="model.needs_name_confirmation" class="manager-badge manager-badge-amber mt-2">{{ mt('nameNeedsReview') }}</span></div>
            <div class="flex flex-wrap items-center gap-3">
              <RouterLink v-if="model.action === 'set_price'" to="/admin/channels/pricing" class="btn btn-secondary">{{ mt('setPrice') }}</RouterLink>
              <button v-else-if="['add', 'check_untested'].includes(model.action)" type="button" class="btn btn-secondary" :disabled="busy || !!run" @click="openAssistant(group, model)">{{ model.action === 'add' ? mt('reviewRecommendation') : mt('checkUntested') }}</button>
              <button type="button" class="manager-link text-sm" @click="details = { group, model }">{{ model.action === 'review_name' ? mt('reviewName') : mt('viewAccounts') }}</button>
            </div>
          </div>
          <dl class="manager-model-facts">
            <div><dt>{{ mt('publication') }}</dt><dd :class="model.published ? 'text-emerald-700 dark:text-emerald-300' : 'text-gray-600 dark:text-dark-300'">{{ model.published ? mt('isPublished') : mt('notPublished') }}</dd></div>
            <div><dt>{{ mt('readyNow') }}</dt><dd>{{ mt('accountCount', { count: model.routing_ready_account_count }) }}</dd></div>
            <div><dt>{{ mt('latestCheck') }}</dt><dd :class="checkClass(model.last_check_status)">{{ checkLabel(model.last_check_status) }}</dd><small v-if="model.last_checked_at">{{ formatTime(model.last_checked_at) }}</small></div>
          </dl>
          <div class="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-gray-100 pt-3 dark:border-dark-700">
            <p class="text-xs text-gray-600 dark:text-dark-300">{{ mt('modelEvidence', { passed: model.verified_account_count, untested: model.untested_account_count }) }}</p>
            <p :class="['text-xs leading-relaxed', model.action === 'view' ? 'text-gray-500' : 'text-amber-700 dark:text-amber-300']">{{ modelHint(model) }}</p>
          </div>
        </article>
      </section>
    </div>
    <div v-else-if="overview?.groups.length" class="manager-empty"><h3>{{ mt('noSearchResults') }}</h3><p>{{ mt('noSearchResultsHint') }}</p><button type="button" class="manager-link" @click="search = ''">{{ mt('clearSearch') }}</button></div>

    <p v-if="overview?.groups.length" class="text-xs leading-relaxed text-gray-500 dark:text-dark-300">{{ mt('countsHint') }}</p>

    <BaseDialog :show="assistantOpen" :title="mt('assistantTitle')" width="wide" :close-on-escape="!busy" :show-close-button="!busy" @close="closeAssistant">
      <div class="space-y-5">
        <ol class="manager-steps" :aria-label="mt('assistantSteps')"><li v-for="(value, index) in steps" :key="value" :class="{ 'manager-step-active': step === value, 'manager-step-done': steps.indexOf(step) > index }" :aria-current="step === value ? 'step' : undefined"><span>{{ index + 1 }}</span>{{ mt(`steps.${value}`) }}</li></ol>
        <p class="text-xs text-gray-500">{{ mt('frozenSources', { sources: frozenSourceNames, count: plan?.scope.account_ids.length ?? planRequest?.scope.account_ids?.length ?? 0 }) }}</p>
        <p v-if="error" role="alert" class="manager-error">{{ error }}</p>
        <div v-if="busy && !plan" class="py-8 text-center text-sm text-gray-500" role="status">{{ mt('readingSavedResults') }}</div>

        <template v-if="plan && step === 'discover'">
          <div class="rounded-xl bg-primary-50 p-4 dark:bg-primary-950/30"><h3 class="font-semibold text-primary-900 dark:text-primary-100">{{ mt('existingResultsReady', { count: plan.impact.reused_success_count }) }}</h3><p class="mt-1 text-sm leading-relaxed text-primary-800 dark:text-primary-200">{{ mt('reuseHint') }}</p></div>
          <section v-if="hasUntested" class="space-y-3" data-test="manager-probe-plan">
            <h3 class="font-semibold">{{ mt('untestedPlan', { count: plan.probe_request?.items?.length ?? 0, requests: plan.maximum_request_count }) }}</h3>
            <p class="text-sm leading-relaxed text-gray-600 dark:text-dark-300">{{ mt('probeBoundary') }}</p>
            <ul class="max-h-48 space-y-2 overflow-auto rounded-lg border border-gray-200 p-3 text-sm dark:border-dark-700"><li v-for="target in probeSummary" :key="target.model" class="flex flex-wrap justify-between gap-1"><span class="break-words font-medium">{{ target.model }}</span><span class="text-gray-500">{{ mt('accountCount', { count: target.accounts.length }) }}</span></li></ul>
            <details class="manager-details"><summary>{{ mt('technicalTargets') }}</summary><p v-for="target in plan.probe_request?.items ?? []" :key="JSON.stringify(target)" class="mt-2 break-all text-xs">{{ accountName(target.account_id) }} · {{ target.upstream_model }} · {{ protocolLabel(target.protocol) }}</p></details>
          </section>
          <p v-else class="text-sm text-gray-600 dark:text-dark-300" data-test="manager-no-new-checks">{{ mt('nothingUntested') }}</p>
          <p class="text-xs text-gray-500">{{ mt('preserveNotice') }}</p>
        </template>

        <section v-if="step === 'check'" class="space-y-4" aria-live="polite">
          <template v-if="run">
            <div class="flex flex-wrap items-center justify-between gap-3"><h3 class="font-semibold">{{ stateLabel(run.status) }}</h3><span class="text-xs text-gray-500">{{ mt('actualRequests', { count: run.request_count }) }}</span></div>
            <progress class="manager-progress" :value="run.processed_count" :max="run.target_count || 1" :aria-label="mt('checkProgress')" />
            <p class="text-sm">{{ mt('runProgress', { done: run.processed_count, total: run.target_count, requests: run.request_count }) }}</p>
            <p class="text-sm text-gray-600 dark:text-dark-300">{{ mt('runResults', { passed: run.succeeded_count, failed: run.failed_count }) }}</p>
            <p v-if="run.possibly_sent_count" class="text-xs text-amber-700 dark:text-amber-300">{{ t('admin.accountCapabilities.possiblySent', { count: run.possibly_sent_count }) }}</p>
            <p class="text-xs leading-relaxed text-gray-500">{{ mt('runHint') }}</p>
            <div class="flex flex-wrap gap-2">
              <button v-if="['pending', 'running'].includes(run.status)" type="button" class="btn btn-secondary" :disabled="busy" @click="controlRun('pause')">{{ t('admin.accountCapabilities.pause') }}</button>
              <button v-if="run.status === 'paused'" type="button" class="btn btn-secondary" :disabled="busy" @click="controlRun('resume')">{{ t('admin.accountCapabilities.resume') }}</button>
              <button v-if="!['completed', 'canceled'].includes(run.status)" type="button" class="btn btn-secondary" :disabled="busy" @click="controlRun('cancel')">{{ t('admin.accountCapabilities.cancelRun') }}</button>
              <button type="button" class="manager-link text-sm" :disabled="busy" @click="refreshRun">{{ t('common.refresh') }}</button>
            </div>
          </template>
          <p v-else class="text-sm text-gray-500">{{ mt('startingChecks') }}</p>
        </section>

        <template v-if="plan && step === 'review'">
          <div v-if="changeset?.status === 'applied'" class="manager-success" role="status" data-test="manager-applied"><h3 class="font-semibold">{{ mt('appliedTitle') }}</h3><p class="mt-1 text-sm">{{ mt('appliedHint') }}</p></div>
          <p v-else class="text-sm leading-relaxed text-gray-600 dark:text-dark-300">{{ mt('reviewHint') }}</p>
          <div class="grid grid-cols-2 gap-3 sm:grid-cols-4" data-test="manager-impact"><div class="manager-impact-stat"><strong>{{ plan.impact.added_models.length }}</strong><span>{{ mt('addingModels') }}</span></div><div class="manager-impact-stat"><strong>{{ addedAccountCount }}</strong><span>{{ mt('addingAccounts') }}</span></div><div class="manager-impact-stat"><strong>{{ plan.impact.retained_models.length }}</strong><span>{{ mt('retainingModels') }}</span></div><div class="manager-impact-stat"><strong>{{ plan.impact.removed_models.length + plan.impact.removed_accounts.length }}</strong><span>{{ mt('explicitRemovals') }}</span></div></div>
          <section v-if="plan.impact.added_models.length" class="manager-impact-section"><h3>{{ mt('newPublicModels') }}</h3><ul><li v-for="model in plan.impact.added_models" :key="impactModelKey(model)"><span>{{ model.public_model }}</span><small>{{ model.group_name }}</small></li></ul></section>
          <section v-if="addedAccountRows.length" class="manager-impact-section"><h3>{{ mt('newAccountRoutes') }}</h3><ul><li v-for="row in addedAccountRows" :key="row.key"><div><span>{{ row.public_model }}</span><small class="ml-2">{{ row.group_name }}</small></div><p class="mt-1 text-xs text-gray-500">{{ row.names.join('、') }}</p></li></ul></section>
          <section v-if="addedRouteRows.length" class="manager-impact-section" data-test="manager-added-routes"><h3>{{ mt('addingRoutes', { count: plan.impact.added_routes?.length ?? 0 }) }}</h3><p class="mt-1 text-xs text-gray-500">{{ mt('addingRoutesHint') }}</p><ul><li v-for="row in addedRouteRows" :key="row.key"><div><span>{{ row.public_model }}</span><small class="ml-2">{{ row.group_name }}</small></div><p class="mt-1 text-xs text-gray-500">{{ row.names.join('、') }}</p></li></ul></section>
          <section class="rounded-xl bg-gray-50 p-4 text-sm dark:bg-dark-800"><h3 class="font-medium">{{ mt('whatStays') }}</h3><p class="mt-1 leading-relaxed text-gray-600 dark:text-dark-300">{{ mt('whatStaysHint') }}</p><details v-if="plan.impact.retained_models.length" class="manager-details mt-2"><summary>{{ mt('retainedList', { count: plan.impact.retained_models.length }) }}</summary><p v-for="model in plan.impact.retained_models" :key="impactModelKey(model)" class="mt-1 text-xs">{{ model.group_name }} · {{ model.public_model }}</p></details></section>
          <p v-if="schedulingChanges.length" class="manager-caution">{{ mt('schedulingNotice', { count: schedulingChanges.length }) }}</p>
          <p v-if="unsafePlan" class="manager-error" role="alert">{{ mt('unexpectedRemoval') }}</p>
          <p v-if="!changeset?.changes.length && !busy" class="text-sm text-gray-600 dark:text-dark-300" data-test="manager-no-changes">{{ mt('noChanges') }}</p>
          <details v-if="plan.exclusions.length" class="manager-details" data-test="manager-exclusions"><summary>{{ mt('notAdded', { count: plan.exclusions.length }) }}</summary><p class="mt-2 text-xs text-gray-500">{{ mt('notAddedHint') }}</p><ul class="mt-3 max-h-56 space-y-3 overflow-auto"><li v-for="(item, index) in plan.exclusions" :key="index" class="text-xs"><p class="font-medium">{{ accountName(item.account_id) }} · {{ item.public_model }}</p><p class="mt-1 text-gray-600 dark:text-dark-300">{{ reasonLabel(item.reason) }}</p><RouterLink v-if="item.reason.includes('pricing')" to="/admin/channels/pricing" class="manager-link mt-1 inline-block">{{ mt('setPrice') }}</RouterLink></li></ul></details>
          <details class="manager-details" data-test="manager-technical-preview"><summary>{{ mt('technicalPreview') }}</summary><p v-if="changeset" class="mt-2 text-xs">{{ t('admin.accountCapabilities.changesetTitle', { id: changeset.id }) }}</p><ul v-if="allWarnings.length" class="mt-2 list-inside list-disc space-y-1 text-xs"><li v-for="warning in allWarnings" :key="warning">{{ reasonLabel(warning) }}</li></ul><div v-for="(change, index) in changeset?.changes ?? []" :key="index" class="mt-3 border-t border-gray-200 pt-3 dark:border-dark-700"><h4 class="text-xs font-medium">{{ change.label }}</h4><div class="mt-2 grid gap-2 sm:grid-cols-2"><div><span class="text-xs text-gray-500">{{ t('admin.accountCapabilities.before') }}</span><pre class="manager-json">{{ formatDiff(change.before) }}</pre></div><div><span class="text-xs text-gray-500">{{ t('admin.accountCapabilities.after') }}</span><pre class="manager-json">{{ formatDiff(change.after) }}</pre></div></div></div></details>
        </template>
      </div>
      <template #footer>
        <div class="flex w-full flex-wrap items-center justify-between gap-3">
          <button type="button" class="btn btn-secondary" :disabled="busy" @click="closeAssistant">{{ changeset?.status === 'applied' ? mt('backToOverview') : t('common.close') }}</button>
          <div v-if="step === 'discover'" class="flex flex-wrap gap-2"><button v-if="plan?.preview_request" type="button" :class="['btn', hasUntested ? 'btn-secondary' : 'btn-primary']" :disabled="busy" data-test="manager-review-existing" @click="prepareReview(false)">{{ mt('useExistingResults') }}</button><button v-if="hasUntested" type="button" class="btn btn-primary" :disabled="busy" data-test="manager-start-checks" @click="startChecks">{{ mt('startChecks', { count: plan?.maximum_request_count ?? 0 }) }}</button></div>
          <button v-if="step === 'check' && run && !activeRun && run.status !== 'paused'" type="button" class="btn btn-primary" :disabled="busy" data-test="manager-checks-next" @click="prepareReview(true)">{{ mt('continueToReview') }}</button>
          <button v-if="step === 'review' && changeset && changeset.status !== 'applied'" type="button" class="btn btn-primary" :disabled="busy || !changeset.changes.length || unsafePlan" data-test="manager-confirm-apply" @click="applyRecommendation">{{ mt('confirmApply') }}</button>
        </div>
      </template>
    </BaseDialog>

    <BaseDialog :show="!!details" :title="details?.model.public_model ?? ''" width="wide" @close="details = null">
      <div v-if="details" class="space-y-4">
        <p class="text-sm leading-relaxed text-gray-600 dark:text-dark-300">{{ modelHint(details.model) }}</p>
        <p class="text-xs text-gray-500">{{ mt('detailFacts', { passed: details.model.verified_account_count, ready: details.model.routing_ready_account_count, untested: details.model.untested_account_count }) }}</p>
        <div v-if="details.model.needs_name_confirmation" class="manager-caution"><p>{{ mt('nameReviewHint') }}</p><RouterLink :to="{ path: '/admin/accounts', query: { search: details.model.public_model } }" class="manager-link mt-2 inline-block">{{ mt('openAccounts') }}</RouterLink></div>
        <div v-if="!details.model.pricing_known && details.model.verified_account_count" class="manager-caution"><p>{{ mt('waitingPrice') }}</p><RouterLink to="/admin/channels/pricing" class="manager-link mt-2 inline-block">{{ mt('setPrice') }}</RouterLink></div>
        <p v-if="!detailAccounts.length" class="text-sm text-gray-500">{{ mt('noScopedRoutes') }}</p>
        <article v-for="account in detailAccounts" :key="account.id" class="rounded-xl border border-gray-200 p-4 dark:border-dark-700" data-test="manager-account-detail">
          <div class="flex flex-wrap items-center justify-between gap-2"><h3 class="font-medium">{{ account.name }}</h3><span :class="['manager-badge', account.passed ? 'manager-badge-green' : 'manager-badge-amber']">{{ account.passed ? mt('basicPassed') : account.attempted ? mt('notPassedYet') : mt('notCheckedYet') }}</span></div>
          <p class="mt-2 text-xs text-gray-500">{{ folderName(account.folderId) }} · {{ account.ready ? mt('canReceiveNow') : mt('cannotReceiveNow') }}</p>
          <details class="manager-details mt-3"><summary>{{ mt('accountTechnicalDetails') }}</summary><p class="mt-2 text-xs text-gray-500">#{{ account.id }}</p><div v-for="candidate in account.candidates" :key="candidate.candidate_id" class="mt-3 text-xs"><p class="break-all font-mono">{{ candidate.upstream_model }}</p><p class="mt-1 text-gray-500">{{ protocolLabel(candidate.protocol) }} · {{ candidate.discovered ? mt('catalogClaim') : mt('savedHint') }}</p><p v-if="candidate.last_success_at" class="mt-1 text-emerald-700 dark:text-emerald-300">{{ mt('lastSuccess', { time: formatTime(candidate.last_success_at) }) }}</p><p v-if="candidate.latest_attempt" class="mt-1">{{ mt('latestAttempt', { status: checkLabel(candidate.latest_attempt.status), time: formatTime(candidate.latest_attempt.checked_at) }) }}</p><p v-for="reason in candidate.not_publishable_reasons ?? []" :key="reason" class="mt-1 text-amber-700 dark:text-amber-300">{{ reasonLabel(reason) }}</p></div></details>
        </article>
        <details v-if="details.model.reasons.length" class="manager-details"><summary>{{ mt('moreReasons') }}</summary><p v-for="reason in details.model.reasons" :key="reason" class="mt-2 text-xs">{{ reasonLabel(reason) }}</p></details>
      </div>
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import BaseDialog from '@/components/common/BaseDialog.vue'
import api, { type CapabilityCandidate, type CapabilityChangeset, type CapabilityGroupOverview, type CapabilityModelOverview, type CapabilityOverview, type CapabilityPlan, type CapabilityPlanModel, type CapabilityPlanRequest, type CapabilityRun } from '@/api/admin/accountCapabilities'
import type { AccountManagementFolder } from '@/types'
import { capabilityCodeLabel, isCapabilityRunActive, makeCapabilityIdempotencyKey } from '../accountCapabilitiesHelpers'

const props = withDefaults(defineProps<{ folderIds: number[]; accountIds: number[]; groupIds?: number[]; folders: AccountManagementFolder[]; active?: boolean; initialSearch?: string }>(), { groupIds: () => [], active: true, initialSearch: '' })
const emit = defineEmits<{ (event: 'resolved', value: CapabilityOverview): void; (event: 'locked', value: boolean): void; (event: 'advanced'): void }>()
const { t, te } = useI18n()
const mt = (key: string, values?: Record<string, string | number>) => values ? t(`admin.accountCapabilities.manager.${key}`, values) : t(`admin.accountCapabilities.manager.${key}`)
const overview = ref<CapabilityOverview | null>(null)
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const search = ref(props.initialSearch)
const selectedGroupKey = ref('')
const selectedGroup = computed(() => overview.value?.groups.find((group) => groupKey(group) === selectedGroupKey.value))
const displayGroups = computed(() => (selectedGroup.value ? [selectedGroup.value] : overview.value?.groups ?? []).filter((group) => matchingModels(group).length))
const details = ref<{ group: CapabilityGroupOverview; model: CapabilityModelOverview } | null>(null)
const assistantOpen = ref(false)
const steps = ['discover', 'check', 'review'] as const
const step = ref<(typeof steps)[number]>('discover')
const plan = ref<CapabilityPlan | null>(null)
const planRequest = ref<CapabilityPlanRequest | null>(null)
const changeset = ref<CapabilityChangeset | null>(null)
const run = ref<CapabilityRun | null>(null)
const activeRun = computed(() => !!run.value && isCapabilityRunActive(run.value.status))
const hasUntested = computed(() => !!plan.value?.probe_request?.items?.length)
const unsafePlan = computed(() => !!plan.value && ((!!plan.value.preview_request?.operation && plan.value.preview_request.operation !== 'merge') || plan.value.impact.removed_models.length > 0 || plan.value.impact.removed_accounts.length > 0))
const schedulingChanges = computed(() => changeset.value?.changes.filter((change) => change.kind === 'scheduling') ?? [])
const allWarnings = computed(() => [...new Set([...(plan.value?.warnings ?? []), ...(changeset.value?.warnings ?? [])])])
const addedAccountCount = computed(() => new Set(plan.value?.impact.added_accounts.map((item) => item.account_id)).size)
const addedAccountRows = computed(() => {
  const rows = new Map<string, { key: string; public_model: string; group_name?: string; names: string[] }>()
  for (const item of plan.value?.impact.added_accounts ?? []) {
    const key = impactModelKey(item)
    const row = rows.get(key) ?? { key, public_model: item.public_model, group_name: item.group_name, names: [] }
    if (!row.names.includes(item.account_name)) row.names.push(item.account_name)
    rows.set(key, row)
  }
  return [...rows.values()]
})
const addedRouteRows = computed(() => {
  const rows = new Map<string, { key: string; public_model: string; group_name?: string; names: string[] }>()
  for (const item of plan.value?.impact.added_routes ?? []) {
    const key = impactModelKey(item)
    const row = rows.get(key) ?? { key, public_model: item.public_model, group_name: item.group_name, names: [] }
    if (!row.names.includes(item.account_name)) row.names.push(item.account_name)
    rows.set(key, row)
  }
  return [...rows.values()]
})
const probeSummary = computed(() => {
  const rows = new Map<string, Set<number>>()
  for (const item of plan.value?.probe_request?.items ?? []) {
    const accounts = rows.get(item.upstream_model) ?? new Set<number>()
    accounts.add(item.account_id)
    rows.set(item.upstream_model, accounts)
  }
  return [...rows].map(([model, accounts]) => ({ model, accounts: [...accounts] }))
})
const detailAccounts = computed(() => {
  const accounts = new Map<number, { id: number; name: string; folderId: number; passed: boolean; ready: boolean; attempted: boolean; candidates: CapabilityCandidate[] }>()
  for (const item of details.value?.model.candidates ?? []) {
    const account = accounts.get(item.account_id) ?? { id: item.account_id, name: item.account_name, folderId: item.folder_id, passed: false, ready: false, attempted: false, candidates: [] }
    account.passed ||= item.last_success_reusable === true
    account.ready ||= item.routing_ready === true
    account.attempted ||= !!item.latest_attempt || !!item.latest_probe_item_id
    account.candidates.push(item)
    accounts.set(item.account_id, account)
  }
  return [...accounts.values()].sort((left, right) => Number(right.passed) - Number(left.passed) || left.name.localeCompare(right.name))
})
const sourceNames = computed(() => (overview.value?.scope.folder_ids ?? props.folderIds).map(folderName).join('、'))
const frozenSourceNames = computed(() => (plan.value?.scope.folder_ids ?? planRequest.value?.scope.folder_ids ?? []).map(folderName).join('、'))
let mounted = false
let overviewEpoch = 0
let assistantEpoch = 0
let overviewController: AbortController | undefined
let planController: AbortController | undefined
let pollTimer: ReturnType<typeof setTimeout> | undefined
let polling = false
let pollStopped = false
let canonicalScopeKey = ''
const actionKeys = new Map<string, string>()
const completedRuns = new Set<number>()

function groupKey(group: CapabilityGroupOverview): string { return JSON.stringify([group.id, group.name]) }
function impactModelKey(model: CapabilityPlanModel): string { return JSON.stringify([model.group_id, model.group_name, model.public_model]) }
function folderName(id: number): string { return props.folders.find((folder) => folder.id === id)?.name ?? mt('sourceFolder') }
function accountName(id: number): string { return overview.value?.accounts.find((account) => account.id === id)?.name ?? mt('sourceAccount') }
function matchingModels(group: CapabilityGroupOverview): CapabilityModelOverview[] { const query = search.value.trim().toLowerCase(); return group.models.filter((model) => !query || [model.public_model, ...(model.aliases ?? []), ...(model.candidates ?? []).map((candidate) => candidate.upstream_model)].some((name) => name.toLowerCase().includes(query))) }
function formatTime(value?: string | null): string { if (!value) return '—'; const date = new Date(value); return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString() }
function formatDiff(value: unknown): string { return value == null ? '—' : typeof value === 'string' ? value : JSON.stringify(value, null, 2) }
function protocolLabel(value: string): string { return ({ responses: 'Responses', responses_websocket: 'Responses WebSocket', chat_completions: 'Chat Completions', messages: 'Messages', responses_input_tokens: 'Responses input_tokens', messages_count_tokens: 'Messages count_tokens' } as Record<string, string>)[value] ?? value }
function stateLabel(value: string): string { return capabilityCodeLabel(value, 'states', t, te) }
function checkLabel(value?: string): string { if (!value || value === 'untested') return mt('notCheckedYet'); if (value === 'model_unavailable') return mt('modelUnavailable'); return stateLabel(value) }
function checkClass(value: string): string { return ['alive', 'succeeded', 'completed'].includes(value) ? 'text-emerald-700 dark:text-emerald-300' : value && value !== 'untested' ? 'text-amber-700 dark:text-amber-300' : 'text-gray-500' }
function reasonLabel(value: string): string { return capabilityCodeLabel(value, 'reasonLabels', t, te) }
function modelHint(model: CapabilityModelOverview): string {
  if (model.needs_name_confirmation) return mt('nameReviewHint')
  if (!model.pricing_known && model.verified_account_count) return mt('waitingPrice')
  if (model.temporary_failure_account_count) return mt(model.verified_account_count ? 'temporaryWithSuccess' : 'temporaryWithoutSuccess')
  if (model.action === 'add') return mt('readyToAdd')
  if (model.untested_account_count) return mt('hasUntestedHint')
  if (model.published && !model.routing_ready_account_count) return mt('publishedNotReady')
  if (model.published) return mt('publishedHint')
  if (model.reasons.length) return reasonLabel(model.reasons[0]!)
  return mt('noReusableSuccess')
}
function fail(key: string): void { error.value = mt(key) }
function responseStatus(value: unknown): number | undefined {
  if (!value || typeof value !== 'object' || !('response' in value)) return undefined
  const response = value.response
  return response && typeof response === 'object' && 'status' in response && typeof response.status === 'number' ? response.status : undefined
}
function operationKey(request: unknown): { signature: string; key: string } { const signature = JSON.stringify(request); const key = actionKeys.get(signature) ?? makeCapabilityIdempotencyKey(); actionKeys.set(signature, key); return { signature, key } }
function stopPolling(): void { if (pollTimer) clearTimeout(pollTimer); pollTimer = undefined }
function schedulePolling(): void { stopPolling(); if (mounted && props.active && document.visibilityState !== 'hidden' && activeRun.value && !pollStopped) pollTimer = setTimeout(() => { void refreshRun() }, 4000) }
function visibilityChanged(): void { if (document.visibilityState === 'hidden') stopPolling(); else if (activeRun.value && props.active && !pollStopped) void refreshRun() }

async function refreshOverview(): Promise<void> {
  overviewController?.abort(); overviewController = new AbortController()
  const signal = overviewController.signal; const epoch = ++overviewEpoch
  if (!props.folderIds.length && !props.accountIds.length) { overview.value = null; loading.value = false; return }
  loading.value = true
  try {
    const data = await api.overview({ folder_ids: props.folderIds.length ? props.folderIds.join(',') : undefined, account_ids: props.accountIds.length ? props.accountIds.join(',') : undefined, group_ids: props.groupIds.length ? props.groupIds.join(',') : undefined }, signal)
    if (!mounted || epoch !== overviewEpoch) return
    overview.value = data
    if (!selectedGroupKey.value && props.groupIds.length === 1 && data.groups.length === 1) selectedGroupKey.value = groupKey(data.groups[0]!)
    // Account-only deep links resolve their folders on the server. Hydrating
    // those checkboxes must not discard the just-loaded overview or fetch twice.
    canonicalScopeKey = [data.scope.folder_ids.join(','), props.accountIds.join(','), props.groupIds.join(',')].join('|')
    emit('resolved', data)
    if (!assistantOpen.value) error.value = ''
  } catch (failure) { if (!signal.aborted && mounted && epoch === overviewEpoch) fail(responseStatus(failure) === 400 ? 'scopeUnavailable' : 'readFailed') }
  finally { if (epoch === overviewEpoch) loading.value = false }
}
function organizeCurrentScope(): void { if (run.value) { assistantOpen.value = true; return }; void openAssistant(selectedGroup.value) }
async function openAssistant(group?: CapabilityGroupOverview, model?: CapabilityModelOverview): Promise<void> {
  if (busy.value || !overview.value?.accounts.length) return
  const scope = { folder_ids: [...overview.value.scope.folder_ids], account_ids: [...overview.value.scope.account_ids] }
  planRequest.value = { scope, mainstream_only: true, ...(model && group ? { models: [{ group_id: group.id ?? undefined, group_name: group.name, public_model: model.public_model }] } : group?.id ? { group_ids: [group.id] } : group ? { models: group.models.map((item) => ({ group_name: group.name, public_model: item.public_model })) } : props.groupIds.length ? { group_ids: [...props.groupIds] } : {}) }
  plan.value = null; changeset.value = null; run.value = null; step.value = 'discover'; error.value = ''; assistantOpen.value = true
  const epoch = ++assistantEpoch; busy.value = true
  try { await readPlan(epoch) } catch { if (epoch === assistantEpoch) fail('planFailed') }
  finally { if (epoch === assistantEpoch) busy.value = false }
}
async function readPlan(epoch: number): Promise<boolean> {
  if (!planRequest.value) return false
  planController?.abort(); planController = new AbortController()
  const data = await api.plan(planRequest.value, planController.signal)
  if (epoch !== assistantEpoch || !mounted) return false
  plan.value = data
  return true
}
function closeAssistant(): void {
  if (busy.value) return
  assistantOpen.value = false; error.value = ''
  if (!run.value || (!activeRun.value && run.value.status !== 'paused')) { assistantEpoch++; planController?.abort(); plan.value = null; changeset.value = null; run.value = null }
}
async function startChecks(): Promise<void> {
  if (busy.value || !plan.value?.probe_request || !hasUntested.value) return
  busy.value = true; error.value = ''; pollStopped = false
  const epoch = assistantEpoch
  const request = plan.value.probe_request
  const { signature, key } = operationKey(request)
  try {
    const created = await api.createRun(request, key)
    actionKeys.delete(signature)
    if (!mounted || epoch !== assistantEpoch) return
    run.value = created; step.value = 'check'
  } catch { if (epoch === assistantEpoch) fail('startFailed') }
  finally { if (epoch === assistantEpoch) busy.value = false }
  if (run.value && !activeRun.value && run.value.status !== 'paused') await finishChecks()
  schedulePolling()
}
async function refreshRun(): Promise<void> {
  if (!run.value || polling) return
  const id = run.value.id; const epoch = assistantEpoch
  polling = true; pollStopped = false
  try {
    const updated = await api.getRun(id)
    if (!mounted || epoch !== assistantEpoch || run.value?.id !== id) return
    run.value = updated; error.value = ''
    if (!isCapabilityRunActive(updated.status) && updated.status !== 'paused') await finishChecks()
  } catch { if (epoch === assistantEpoch) { fail('checkReadFailed'); pollStopped = true } }
  finally { polling = false; schedulePolling() }
}
async function finishChecks(): Promise<void> {
  if (!run.value || completedRuns.has(run.value.id)) return
  completedRuns.add(run.value.id)
  await refreshOverview()
  await prepareReview(true)
}
async function controlRun(action: 'pause' | 'resume' | 'cancel'): Promise<void> {
  if (busy.value || !run.value) return
  const epoch = assistantEpoch; const id = run.value.id
  busy.value = true; error.value = ''
  try { const updated = await api.controlRun(id, action); if (mounted && epoch === assistantEpoch) { run.value = updated; pollStopped = false } }
  catch { if (epoch === assistantEpoch) fail('checkControlFailed') }
  finally { if (epoch === assistantEpoch) busy.value = false; schedulePolling() }
  if (run.value && !activeRun.value && run.value.status !== 'paused') await finishChecks()
}
async function prepareReview(refreshPlan: boolean): Promise<void> {
  if (busy.value || !planRequest.value) return
  const epoch = assistantEpoch; busy.value = true; error.value = ''
  try {
    if (refreshPlan && !(await readPlan(epoch))) return
    if (!plan.value || epoch !== assistantEpoch) return
    changeset.value = null
    if (plan.value.preview_request) {
      const request = { ...plan.value.preview_request, operation: 'merge' as const }
      const { signature, key } = operationKey(request)
      const preview = await api.preview({ ...request, idempotency_key: request.idempotency_key || key })
      actionKeys.delete(signature)
      if (!mounted || epoch !== assistantEpoch) return
      changeset.value = preview
    }
    step.value = 'review'
  } catch { if (epoch === assistantEpoch) fail('previewFailed') }
  finally { if (epoch === assistantEpoch) busy.value = false }
}
async function applyRecommendation(): Promise<void> {
  if (busy.value || !changeset.value || changeset.value.status === 'applied' || unsafePlan.value || !changeset.value.changes.length) return
  busy.value = true; error.value = ''; const epoch = assistantEpoch; const id = changeset.value.id
  try {
    const applied = await api.apply(id)
    if (!mounted || epoch !== assistantEpoch) return
    changeset.value = applied
    await refreshOverview()
  } catch { if (epoch === assistantEpoch) fail('applyFailed') }
  finally { if (epoch === assistantEpoch) busy.value = false }
}

watch(() => [props.folderIds.join(','), props.accountIds.join(','), props.groupIds.join(',')], (parts) => {
  if (overview.value && parts.join('|') === canonicalScopeKey) { canonicalScopeKey = ''; return }
  canonicalScopeKey = ''
  assistantEpoch++; planController?.abort(); stopPolling(); plan.value = null; planRequest.value = null; changeset.value = null; run.value = null; assistantOpen.value = false; details.value = null; overview.value = null; selectedGroupKey.value = ''; error.value = ''; busy.value = false
  if (mounted) void refreshOverview()
})
watch(() => props.active, (active) => { if (!active) stopPolling(); else if (activeRun.value && !pollStopped) void refreshRun() })
watch(() => busy.value || assistantOpen.value || activeRun.value || run.value?.status === 'paused', (locked) => emit('locked', locked), { immediate: true })
onMounted(() => { mounted = true; document.addEventListener('visibilitychange', visibilityChanged); void refreshOverview() })
onBeforeUnmount(() => { mounted = false; overviewEpoch++; assistantEpoch++; overviewController?.abort(); planController?.abort(); stopPolling(); emit('locked', false); document.removeEventListener('visibilitychange', visibilityChanged) })
defineExpose({ refresh: refreshOverview })
</script>

<style scoped>
.public-manager { @apply min-w-0; }
.manager-stat { @apply flex min-w-0 flex-col gap-1 rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800; }
.manager-stat > span { @apply text-xs text-gray-600 dark:text-dark-300; }
.manager-stat > strong { @apply text-2xl font-semibold tabular-nums; }
.manager-stat > small { @apply text-xs leading-relaxed text-gray-500 dark:text-dark-400; }
.manager-intro { @apply flex flex-wrap items-center gap-4 rounded-xl border border-primary-100 bg-primary-50/60 p-4 dark:border-primary-900/50 dark:bg-primary-950/20; }
.manager-group-card { @apply min-w-0 rounded-xl border border-gray-200 bg-white p-5 text-left transition-colors hover:border-primary-400 hover:bg-primary-50/30 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:border-dark-700 dark:bg-dark-800 dark:hover:border-primary-600 dark:hover:bg-dark-700; }
.manager-badge { @apply inline-flex shrink-0 items-center rounded-full px-2.5 py-1 text-xs font-medium; }
.manager-badge-green { @apply bg-emerald-50 text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-300; }
.manager-badge-amber { @apply bg-amber-50 text-amber-700 dark:bg-amber-950/30 dark:text-amber-300; }
.manager-model-card { @apply min-w-0 rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800 sm:p-5; }
.manager-model-facts { @apply mt-4 grid grid-cols-1 gap-3 rounded-lg bg-gray-50 p-3 dark:bg-dark-900/50 sm:grid-cols-3; }
.manager-model-facts dt { @apply text-xs text-gray-500 dark:text-dark-300; }
.manager-model-facts dd { @apply mt-1 text-sm font-medium; }
.manager-model-facts small { @apply mt-1 block text-xs text-gray-500; }
.manager-link { @apply font-medium text-primary-600 hover:text-primary-700 focus-visible:rounded focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-primary-300 dark:hover:text-primary-200; }
.manager-empty { @apply flex flex-col items-center gap-3 rounded-xl border border-dashed border-gray-300 bg-white p-8 text-center dark:border-dark-600 dark:bg-dark-800; }
.manager-empty h3 { @apply text-base font-semibold; }
.manager-empty p { @apply max-w-lg text-sm leading-relaxed text-gray-500 dark:text-dark-300; }
.manager-error { @apply rounded-lg border border-red-200 bg-red-50 p-3 text-sm leading-relaxed text-red-700 dark:border-red-900 dark:bg-red-950/30 dark:text-red-300; }
.manager-caution { @apply rounded-lg border border-amber-200 bg-amber-50 p-3 text-xs leading-relaxed text-amber-800 dark:border-amber-900 dark:bg-amber-950/30 dark:text-amber-300; }
.manager-success { @apply rounded-xl border border-emerald-200 bg-emerald-50 p-4 text-emerald-800 dark:border-emerald-900 dark:bg-emerald-950/30 dark:text-emerald-200; }
.manager-run-banner { @apply flex flex-wrap items-center justify-between gap-3 rounded-xl border border-primary-200 bg-primary-50 p-4 text-primary-800 dark:border-primary-900 dark:bg-primary-950/30 dark:text-primary-200; }
.manager-steps { @apply grid grid-cols-3 gap-2 border-b border-gray-200 pb-4 dark:border-dark-700; }
.manager-steps li { @apply flex min-w-0 flex-col gap-2 text-xs font-medium text-gray-400 sm:flex-row sm:items-center; }
.manager-steps li > span { @apply flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-gray-100 text-xs dark:bg-dark-700; }
.manager-steps .manager-step-active { @apply text-primary-700 dark:text-primary-300; }
.manager-steps .manager-step-active > span { @apply bg-primary-600 text-white; }
.manager-steps .manager-step-done { @apply text-emerald-700 dark:text-emerald-300; }
.manager-steps .manager-step-done > span { @apply bg-emerald-100 text-emerald-700 dark:bg-emerald-950/50 dark:text-emerald-300; }
.manager-details { @apply rounded-lg border border-gray-200 p-3 text-gray-600 dark:border-dark-700 dark:text-dark-300; }
.manager-details > summary { @apply cursor-pointer text-xs font-medium; }
.manager-progress { @apply h-2 w-full overflow-hidden rounded-full; accent-color: theme('colors.primary.600'); }
.manager-impact-stat { @apply flex flex-col gap-1 rounded-lg bg-gray-50 p-3 dark:bg-dark-800; }
.manager-impact-stat > strong { @apply text-xl font-semibold tabular-nums; }
.manager-impact-stat > span { @apply text-xs text-gray-500 dark:text-dark-300; }
.manager-impact-section > h3 { @apply text-sm font-semibold; }
.manager-impact-section > ul { @apply mt-2 max-h-52 space-y-2 overflow-auto rounded-lg border border-gray-200 p-3 dark:border-dark-700; }
.manager-impact-section li { @apply break-words text-sm; }
.manager-impact-section li > small { @apply ml-2 text-xs text-gray-500; }
.manager-json { @apply mt-1 max-h-48 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-900; }
</style>
