<template>
  <AppLayout>
    <TablePageLayout class="capability-page" :class="{ 'capability-overview': tab === 'overview' }" :content-framed="!['overview', 'preview'].includes(tab)">
      <template #filters>
        <div class="capability-toolbar space-y-2">
          <div class="pb-2"><h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('admin.accountCapabilities.title') }}</h1><p class="mt-1 text-sm leading-relaxed text-gray-500 dark:text-dark-300">{{ t('admin.accountCapabilities.description') }}</p></div>
          <details v-if="tab !== 'overview'" class="capability-safety">
            <summary>{{ t('admin.accountCapabilities.safetySummary') }}</summary>
            <p class="mt-2 leading-relaxed">{{ t('admin.accountCapabilities.safetyNotice') }}</p>
          </details>
          <div class="flex flex-wrap items-center justify-between gap-3">
            <div role="tablist" :aria-label="t('admin.accountCapabilities.title')" class="flex max-w-full flex-wrap gap-1 rounded-xl bg-gray-100 p-1 dark:bg-dark-800">
              <button v-for="value in tabs" :key="value" type="button" role="tab" :aria-selected="tab === value" :disabled="managerLocked && value !== 'overview'"
                :data-test="`capability-tab-${value}`" :class="['capability-tab', { 'capability-tab-active': tab === value }]"
                @click="changeTab(value)">{{ t(`admin.accountCapabilities.tabs.${value}`) }}</button>
            </div>
            <button type="button" class="btn btn-secondary" :disabled="loading || busy || managerLocked" data-test="capability-refresh" @click="refresh()">
              <Icon name="refresh" size="sm" />{{ t('common.refresh') }}
            </button>
          </div>
          <details class="capability-source-details" :open="tab !== 'overview' || !sourceFolderNames">
          <summary class="cursor-pointer text-xs text-gray-600 dark:text-dark-300">{{ t('admin.accountCapabilities.sourceFolders') }}：{{ sourceFolderNames || '—' }} <span v-if="resolvedScopeAccounts.length">· {{ t('admin.accountCapabilities.manager.accountCount', { count: resolvedScopeAccounts.length }) }}</span><span class="ml-2 text-primary-600 dark:text-primary-300">{{ t('admin.accountCapabilities.manager.adjustSources') }}</span></summary>
          <fieldset :disabled="busy || managerLocked" class="capability-scope mt-2">
            <legend class="sr-only">{{ t('admin.accountCapabilities.sourceFolders') }}</legend>
            <span aria-hidden="true" class="text-xs font-medium text-gray-500 dark:text-dark-300">{{ t('admin.accountCapabilities.sourceFolders') }}</span>
            <label v-for="folder in folders" :key="folder.id" class="capability-folder">
              <input v-model="folderIDs" type="checkbox" :value="folder.id" :data-test="`capability-folder-${folder.id}`" @change="scopeChanged" />
              <span>{{ folder.name }}</span><span class="text-gray-400">{{ folder.account_count }}</span>
            </label>
            <span v-if="!folders.length" class="text-sm text-gray-500">{{ t('admin.accountCapabilities.noFolders') }}</span>
            <details v-if="resolvedScopeAccounts.length" class="capability-scope-info">
              <summary>{{ t('admin.accountCapabilities.scopeSummary', { count: resolvedScopeAccounts.length }) }}</summary>
              <p class="mt-2 leading-relaxed">{{ t('admin.accountCapabilities.frozenScopeHint', { count: resolvedScopeAccounts.length }) }}</p>
            </details>
          </fieldset>
          </details>
          <div v-if="accountIDs.length || groupIDs.length" class="flex flex-wrap gap-3 text-xs text-gray-500">
            <span v-if="accountIDs.length">{{ t('admin.accountCapabilities.manager.selectedAccounts', { count: accountIDs.length }) }} <button type="button" class="ml-1 text-primary-600" :disabled="busy || managerLocked" @click="clearAccountScope">{{ t('admin.accountCapabilities.manager.clearAccountScope') }}</button></span>
            <span v-if="groupIDs.length">{{ t('admin.accountCapabilities.manager.selectedGroups', { count: groupIDs.length }) }} <button type="button" class="ml-1 text-primary-600" :disabled="busy || managerLocked" @click="clearGroupScope">{{ t('admin.accountCapabilities.manager.clearGroupScope') }}</button></span>
          </div>
          <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-300">{{ error }}</p>

          <div v-if="tab === 'inventory'" class="flex flex-wrap items-end justify-between gap-2">
            <form class="flex flex-wrap items-center gap-2" @submit.prevent="loadCandidates(1)">
              <input v-model="search" class="input capability-search" :placeholder="t('admin.accountCapabilities.search')" :aria-label="t('admin.accountCapabilities.search')" />
              <select v-model="candidateStatus" class="input capability-status-filter" :aria-label="t('common.status')" @change="loadCandidates(1)">
                <option value="">{{ t('common.all') }}</option>
                <option value="alive">{{ t('admin.accountCapabilities.states.alive') }}</option>
                <option value="failed">{{ t('admin.accountCapabilities.states.failed') }}</option>
                <option value="untested">{{ t('admin.accountCapabilities.untested') }}</option>
                <option value="published">{{ t('admin.accountCapabilities.published') }}</option>
              </select>
              <button type="submit" class="btn btn-secondary" :disabled="loading">{{ t('common.search') }}</button>
            </form>
            <div class="capability-actions flex flex-wrap gap-2">
              <button type="button" class="btn btn-secondary" :disabled="busy || loading || !resolvedScopeAccounts.length" data-test="capability-discover" @click="discover">
                <Icon name="refresh" size="sm" />{{ t('admin.accountCapabilities.discover') }}
              </button>
              <button type="button" class="btn btn-primary" :disabled="busy || !selectedCandidates.length" data-test="capability-probe" @click="prepareCandidateProbe">
                {{ t('admin.accountCapabilities.testSelected', { count: selectedCandidates.length }) }}
              </button>
              <button type="button" class="btn btn-secondary" :disabled="busy || !publishableCandidates.length" data-test="capability-prepare-preview" @click="preparePublication">
                {{ t('admin.accountCapabilities.preparePreview', { count: publishableCandidates.length }) }}
              </button>
            </div>
            <div class="flex w-full flex-wrap items-center justify-between gap-2 text-xs text-gray-500 dark:text-dark-300">
              <details class="capability-view-note"><summary>{{ t('admin.accountCapabilities.mainstreamSummary') }}</summary><p class="mt-2 leading-relaxed">{{ t('admin.accountCapabilities.mainstreamHint') }}</p></details>
              <button v-if="selectedCandidates.length" class="text-primary-600" @click="clearSelection">{{ t('admin.accountCapabilities.clearSelection') }}</button>
              <button type="button" class="text-primary-600" data-test="capability-catalog" @click="openCatalog">{{ t('admin.accountCapabilities.catalog') }}</button>
            </div>
          </div>

          <div v-if="tab === 'runs'" class="flex flex-wrap items-center justify-between gap-3">
            <div class="flex gap-2">
              <select v-model="runKind" class="input w-40" :aria-label="t('admin.accountCapabilities.kind')" @change="refreshRuns(1)">
                <option value="">{{ t('common.all') }}</option>
                <option value="discover">{{ t('admin.accountCapabilities.kinds.discover') }}</option>
                <option value="probe">{{ t('admin.accountCapabilities.kinds.probe') }}</option>
              </select>
              <select v-model="runStatus" class="input w-40" :aria-label="t('common.status')" @change="refreshRuns(1)">
                <option value="">{{ t('common.all') }}</option>
                <option v-for="value in runStatuses" :key="value" :value="value">{{ stateLabel(value) }}</option>
              </select>
            </div>
            <p class="text-xs text-gray-500">{{ t('admin.accountCapabilities.retentionHint') }}</p>
          </div>
        </div>
      </template>

      <template #table>
        <PublicModelManager v-if="sourcesReady && managerLoaded" v-show="tab === 'overview'" ref="managerView" :folder-ids="folderIDs" :account-ids="accountIDs" :group-ids="groupIDs" :folders="folders" :active="tab === 'overview'" :initial-search="initialModelSearch" @resolved="overviewResolved" @locked="managerLocked = $event" @advanced="changeTab('inventory')" />
        <div v-if="tab === 'inventory'" class="capability-inventory-table">
        <DataTable :columns="candidateColumns" :data="candidatePage.items" :loading="loading" row-key="candidate_id"
          selectable :selected-keys="selectedKeys" @update:selected-keys="updateSelection">
          <template #cell-account="{ row }">
            <div class="break-words font-medium">{{ row.account_name }}</div>
            <div class="text-xs text-gray-500">{{ folderName(row.folder_id) }} · #{{ row.account_id }}</div>
          </template>
          <template #cell-model="{ row }">
            <div class="capability-model font-mono text-xs font-medium">{{ row.public_model }}</div>
            <div class="capability-model mt-1 font-mono text-xs text-gray-500">→ {{ row.upstream_model }}</div>
            <details v-if="row.aliases?.length" class="mt-1 text-xs text-gray-500">
              <summary class="cursor-pointer">{{ t('admin.accountCapabilities.aliases') }} ({{ row.aliases.length }})</summary>
              <div class="capability-model">{{ row.aliases.join(', ') }}</div>
            </details>
          </template>
          <template #cell-group="{ row }">
            <div class="break-words text-xs font-medium">{{ row.group_name }}</div>
            <div class="capability-group-meta"><span>{{ t(`admin.accountCapabilities.tiers.${row.tier}`) }}</span><span>{{ protocolLabel(row.protocol) }}</span></div>
          </template>
          <template #cell-evidence="{ row }">
            <div class="capability-evidence-grid">
              <span :class="evidenceClass(row.discovered)">{{ t('admin.accountCapabilities.declared') }}: {{ yesNo(row.discovered) }}</span>
              <span :class="evidenceClass(row.configured)">{{ t('admin.accountCapabilities.configured') }}: {{ yesNo(row.configured) }}</span>
              <span :class="evidenceClass(row.last_success_reusable ?? (row.probe_status === 'alive' && !row.stale))">{{ t('admin.accountCapabilities.tested') }}: {{ (row.last_success_reusable ?? (row.probe_status === 'alive' && !row.stale)) ? stateLabel('alive') : t('admin.accountCapabilities.manager.notPassedYet') }}</span>
              <span :class="evidenceClass(row.published)">{{ t('admin.accountCapabilities.published') }}: {{ yesNo(row.published) }}</span>
            </div>
            <p v-if="row.latest_probe_item_id" class="mt-1 text-xs text-gray-500">{{ t('admin.accountCapabilities.manager.latestCheck') }}：{{ stateLabel(row.probe_status || 'untested') }}</p>
            <p v-if="row.stale" class="mt-1 text-xs text-amber-600">{{ t('admin.accountCapabilities.staleHint') }}</p>
            <details v-if="row.not_publishable_reasons?.length" class="mt-1 text-xs text-amber-600">
              <summary class="cursor-pointer">{{ t('admin.accountCapabilities.notPublishable') }}</summary>
              <p v-for="reason in row.not_publishable_reasons" :key="reason" class="max-w-sm whitespace-normal">{{ reasonLabel(reason) }}</p>
            </details>
            <details v-if="row.warnings?.length" class="mt-1 text-xs text-amber-600">
              <summary class="cursor-pointer">{{ t('common.warning') }} ({{ row.warnings.length }})</summary>
              <p v-for="warning in row.warnings" :key="warning" class="max-w-sm whitespace-normal">{{ reasonLabel(warning) }}</p>
            </details>
          </template>
          <template #empty><div class="px-5 py-8 text-center text-sm text-gray-500">{{ t('admin.accountCapabilities.emptyCandidates') }}</div></template>
        </DataTable>
        </div>

        <DataTable v-else-if="tab === 'runs'" :columns="runColumns" :data="runPage.items" :loading="loading" row-key="id">
          <template #cell-id="{ row }"><span class="font-medium">#{{ row.id }}</span><div class="text-xs text-gray-500">{{ t(`admin.accountCapabilities.kinds.${row.kind}`) }}</div></template>
          <template #cell-status="{ row }"><span :class="statusClass(row.status)">{{ stateLabel(row.status) }}</span></template>
          <template #cell-progress="{ row }">
            <span class="tabular-nums">{{ row.processed_count }}/{{ row.target_count }}</span>
            <div class="text-xs text-gray-500">{{ t('admin.accountCapabilities.runResults', { succeeded: row.succeeded_count, failed: row.failed_count }) }}</div>
          </template>
          <template #cell-request_count="{ row }"><span class="tabular-nums">{{ row.request_count }}</span><p v-if="row.possibly_sent_count" class="text-xs text-amber-700">{{ t('admin.accountCapabilities.possiblySent', { count: row.possibly_sent_count }) }}</p></template>
          <template #cell-created_at="{ row }"><span class="text-xs text-gray-500">{{ formatTime(row.created_at) }}</span></template>
          <template #cell-actions="{ row }">
            <button type="button" class="font-medium text-primary-600" :data-test="`capability-run-${row.id}`" @click="openRun(row.id)">{{ t('admin.accountCapabilities.details') }}</button>
          </template>
        </DataTable>

        <div v-else-if="tab === 'preview'" class="space-y-4 pb-6">
          <section class="card p-5">
            <h2 class="text-base font-semibold">{{ t('admin.accountCapabilities.previewTitle') }}</h2>
            <p class="mt-2 text-sm text-gray-500">{{ t('admin.accountCapabilities.previewHint') }}</p>
            <p class="mt-2 text-sm text-amber-700 dark:text-amber-300">{{ t('admin.accountCapabilities.replacementHint') }}</p>
            <fieldset v-if="draftRows.length" :disabled="busy" class="mt-4 space-y-3">
              <div v-for="(row, index) in draftRows" :key="row.key" class="grid gap-3 rounded-lg border border-gray-200 p-3 dark:border-dark-700 md:grid-cols-3">
                <label><span class="input-label">{{ t('admin.accountCapabilities.publicModel') }}</span><input v-model="row.publicModel" class="input font-mono text-sm" :data-test="`capability-public-model-${index}`" @input="invalidatePreview" /></label>
                <label><span class="input-label">{{ t('admin.accountCapabilities.targetGroup') }}</span>
                  <select v-model="row.groupName" class="input" @change="invalidatePreview"><option v-for="group in publicationGroups" :key="group.name" :value="group.name">{{ group.name }} · ×{{ group.rate_multiplier }}{{ group.id ? '' : ` (${t('admin.accountCapabilities.newGroup')})` }}</option></select>
                </label>
                <label><span class="input-label">{{ t('admin.accountCapabilities.aliases') }}</span><input v-model="row.aliases" class="input font-mono text-sm" :placeholder="t('admin.accountCapabilities.aliasesHint')" @input="invalidatePreview" /></label>
                <p class="break-all text-xs text-gray-500 md:col-span-3">{{ row.accountName }} · {{ row.upstreamModel }} · {{ protocolLabel(row.protocol) }} · {{ t('admin.accountCapabilities.evidenceIDs', { ids: row.evidenceID }) }}</p>
                <button type="button" class="justify-self-start text-xs text-red-600 dark:text-red-300 md:col-span-3" :data-test="`capability-remove-draft-${index}`" @click="removeDraftRow(row.key)">{{ t('admin.accountCapabilities.removeDraftRow') }}</button>
              </div>
            </fieldset>
            <p v-else class="mt-4 text-sm text-gray-500">{{ t('admin.accountCapabilities.emptyDraft') }}</p>
            <details v-if="draftAlternatives.length" class="mt-4 rounded-lg border border-amber-200 p-3 dark:border-amber-800" data-test="capability-draft-alternatives">
              <summary class="cursor-pointer text-sm font-medium text-amber-800 dark:text-amber-300">{{ t('admin.accountCapabilities.omittedAlternatives', { count: draftAlternatives.length }) }}</summary>
              <p class="mt-2 text-xs text-gray-500">{{ t('admin.accountCapabilities.alternativePolicy') }}</p>
              <ul class="mt-2 space-y-1 text-xs text-gray-500"><li v-for="alternative in draftAlternatives" :key="alternative.candidate_id" class="break-all">{{ alternative.account_name }} · {{ alternative.public_model }} → {{ alternative.upstream_model }} · {{ protocolLabel(alternative.protocol) }}</li></ul>
            </details>
            <details class="mt-4 rounded-lg border border-gray-200 p-3 dark:border-dark-700">
              <summary class="cursor-pointer text-sm font-medium">{{ t('admin.accountCapabilities.advancedPreview') }}</summary>
              <label class="mt-3 block"><span class="input-label">{{ t('admin.accountCapabilities.detachIDs') }}</span><input v-model="detachIDsInput" class="input" :disabled="busy" :placeholder="t('admin.accountCapabilities.idsPlaceholder')" @input="invalidatePreview" /></label>
              <p class="mt-2 text-xs text-gray-500">{{ t('admin.accountCapabilities.detachHint') }}</p>
              <p v-if="schedulingEvidenceIDs.length" class="mt-3 text-xs text-gray-500">{{ t('admin.accountCapabilities.schedulingEvidence', { ids: schedulingEvidenceIDs.join(', ') }) }}</p>
            </details>
            <div class="mt-4 flex flex-wrap gap-3">
              <button type="button" class="btn btn-primary" :disabled="busy || !canPreview" data-test="capability-generate-preview" @click="generatePreview">{{ t('admin.accountCapabilities.generatePreview') }}</button>
              <form class="flex gap-2" @submit.prevent="restorePreview">
                <input v-model="restorePreviewID" type="number" min="1" class="input w-36" :aria-label="t('admin.accountCapabilities.changesetID')" :placeholder="t('admin.accountCapabilities.changesetID')" />
                <button type="submit" class="btn btn-secondary" :disabled="busy || !restorePreviewID">{{ t('admin.accountCapabilities.restorePreview') }}</button>
              </form>
            </div>
          </section>
          <section v-if="changeset" class="card space-y-4 p-5" data-test="capability-changeset">
            <div class="flex flex-wrap items-center justify-between gap-3">
              <h2 class="font-semibold">{{ t('admin.accountCapabilities.changesetTitle', { id: changeset.id }) }} · {{ t(`admin.accountCapabilities.changeStates.${changeset.status}`) }}</h2>
              <button type="button" class="btn btn-primary" :disabled="busy || changeset.status === 'applied' || !changeset.changes.length" data-test="capability-apply" @click="applyPreview">
                {{ t(changeset.status === 'applied' ? 'admin.accountCapabilities.applied' : 'admin.accountCapabilities.apply') }}
              </button>
            </div>
            <div class="capability-notice">{{ t('admin.accountCapabilities.applyHint') }}</div>
            <div class="rounded-lg border border-gray-200 p-3 text-sm dark:border-dark-700" data-test="capability-frozen-scope">
              <p>{{ t('admin.accountCapabilities.changesetScope', { folders: changeset.scope.folder_ids.map(folderName).join(', '), count: changeset.scope.account_ids.length }) }}</p>
              <details class="mt-2 text-xs text-gray-500"><summary class="cursor-pointer">{{ t('admin.accountCapabilities.frozenAccountIDs') }}</summary><p class="mt-1 break-all">{{ changeset.scope.account_ids.join(', ') }}</p></details>
              <p v-if="!changesetMatchesCurrentScope" class="mt-2 text-xs text-amber-700 dark:text-amber-300">{{ t('admin.accountCapabilities.scopeMismatch') }}</p>
            </div>
            <ul v-if="changeset.warnings.length" class="list-inside list-disc space-y-1 text-sm text-amber-700 dark:text-amber-300"><li v-for="warning in changeset.warnings" :key="warning">{{ warning }}</li></ul>
            <section v-for="section in changeSections" :key="section.kind" class="space-y-2">
              <h3 class="text-sm font-semibold">{{ t(`admin.accountCapabilities.changeKinds.${section.kind}`) }} ({{ section.changes.length }})</h3>
              <div v-for="(change, index) in section.changes" :key="`${section.kind}-${index}`" class="rounded-lg border border-gray-200 p-3 dark:border-dark-700">
                <p class="break-all text-sm font-medium">{{ change.label }}</p>
                <div class="mt-2 grid gap-3 md:grid-cols-2">
                  <div><span class="text-xs text-gray-500">{{ t('admin.accountCapabilities.before') }}</span><pre class="capability-diff bg-red-50 dark:bg-red-950/20">{{ formatDiff(change.before) }}</pre></div>
                  <div><span class="text-xs text-gray-500">{{ t('admin.accountCapabilities.after') }}</span><pre class="capability-diff bg-emerald-50 dark:bg-emerald-950/20">{{ formatDiff(change.after) }}</pre></div>
                </div>
                <p v-if="change.evidence_ids.length" class="mt-2 text-xs text-gray-500">{{ t('admin.accountCapabilities.evidenceIDs', { ids: change.evidence_ids.join(', ') }) }}</p>
              </div>
            </section>
          </section>
        </div>
      </template>
      <template v-if="['inventory', 'runs'].includes(tab)" #pagination>
        <Pagination v-if="activePage.total" :total="activePage.total" :page="activePage.page" :page-size="activePage.page_size" @update:page="changePage" @update:page-size="changePageSize" />
      </template>
    </TablePageLayout>

    <BaseDialog :show="probeDialog" :title="t('admin.accountCapabilities.testTitle')" width="wide" @close="!busy && (probeDialog = false)">
      <div class="space-y-4">
        <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.accountCapabilities.testHint') }}</p>
        <label><span class="input-label">{{ t('admin.accountCapabilities.profile') }}</span>
          <select v-model="probeProfile" class="input" :disabled="busy" data-test="capability-profile"><option v-if="preserveProbeProfiles" value="original">{{ t('admin.accountCapabilities.profiles.original') }}</option><option value="text">{{ t('admin.accountCapabilities.profiles.text') }}</option><option value="tool_roundtrip" :disabled="hasMetadataTargets">{{ t('admin.accountCapabilities.profiles.tool_roundtrip') }}</option></select>
        </label>
        <p class="rounded-lg bg-primary-50 p-3 text-sm font-medium text-primary-800 dark:bg-primary-950/30 dark:text-primary-200" data-test="capability-dedup-count">{{ t('admin.accountCapabilities.probeCount', { selected: probeTargets.length, count: dedupedTargets.length, requests: plannedRequests }) }}</p>
        <div class="max-h-64 space-y-1 overflow-y-auto text-xs text-gray-500"><p v-for="target in dedupedTargets" :key="JSON.stringify(target)">#{{ target.account_id }} · {{ target.upstream_model }} · {{ protocolLabel(target.protocol) }}</p></div>
        <p class="text-xs text-amber-700 dark:text-amber-300">{{ t('admin.accountCapabilities.noAutoRetry') }}</p>
      </div>
      <template #footer><button class="btn btn-secondary" :disabled="busy" @click="probeDialog = false">{{ t('common.cancel') }}</button><button class="btn btn-primary" :disabled="busy || !dedupedTargets.length" data-test="capability-start-probe" @click="startProbe">{{ t('admin.accountCapabilities.startProbe') }}</button></template>
    </BaseDialog>

    <BaseDialog :show="runDialog" :title="t('admin.accountCapabilities.runTitle', { id: currentRun?.id ?? '' })" width="full" @close="closeRun">
      <div v-if="currentRun" class="space-y-4">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div><span :class="statusClass(currentRun.status)">{{ stateLabel(currentRun.status) }}</span><p class="mt-1 text-sm text-gray-500">{{ t('admin.accountCapabilities.runProgress', { done: currentRun.processed_count, total: currentRun.target_count, requests: currentRun.request_count }) }}</p><p v-if="currentRun.possibly_sent_count" class="text-xs text-amber-700">{{ t('admin.accountCapabilities.possiblySent', { count: currentRun.possibly_sent_count }) }}</p></div>
          <div class="flex flex-wrap gap-2">
            <button v-if="['pending', 'running'].includes(currentRun.status)" class="btn btn-secondary" :disabled="busy" data-test="capability-pause" @click="controlRun('pause')">{{ t('admin.accountCapabilities.pause') }}</button>
            <button v-if="currentRun.status === 'paused'" class="btn btn-primary" :disabled="busy" data-test="capability-resume" @click="controlRun('resume')">{{ t('admin.accountCapabilities.resume') }}</button>
            <button v-if="!['completed', 'canceled'].includes(currentRun.status)" class="btn btn-secondary" :disabled="busy" @click="controlRun('cancel')">{{ t('admin.accountCapabilities.cancelRun') }}</button>
            <button class="btn btn-secondary" :disabled="busy || loadingRun" @click="loadRunDetails">{{ t('common.refresh') }}</button>
          </div>
        </div>
        <p class="text-xs text-gray-500">{{ t('admin.accountCapabilities.pauseHint') }}</p>
        <div class="flex flex-wrap justify-between gap-2">
          <select v-model="itemStatus" class="input w-48" :aria-label="t('common.status')" @change="loadRunItems(1)"><option value="">{{ t('common.all') }}</option><option v-for="value in itemStatuses" :key="value" :value="value">{{ stateLabel(value) }}</option></select>
          <div class="flex flex-wrap gap-2"><button class="btn btn-secondary" :disabled="busy || !retryableItems.length" data-test="capability-retest" @click="prepareRetest">{{ t('admin.accountCapabilities.retest', { count: retryableItems.length }) }}</button><button class="btn btn-secondary" :disabled="busy || !accountFailureItems.length" data-test="capability-scheduling-preview" @click="prepareSchedulingPreview">{{ t('admin.accountCapabilities.schedulingPreview') }}</button></div>
        </div>
        <div class="capability-results-table">
        <DataTable :columns="itemColumns" :data="itemPage.items" :loading="loadingRun" row-key="id" selectable :selected-keys="selectedItemKeys" @update:selected-keys="updateItemSelection">
          <template #cell-account="{ row }"><span>{{ row.account_name }}</span><div class="text-xs text-gray-500">#{{ row.account_id }} · {{ folderName(row.folder_id) }}</div></template>
          <template #cell-model="{ row }"><div class="capability-model font-mono text-xs">{{ row.upstream_model || t('admin.accountCapabilities.kinds.discover') }}</div><div class="capability-model mt-1 text-xs text-gray-500">{{ protocolLabel(row.protocol) }} · {{ profileLabel(row) }}</div></template>
          <template #cell-status="{ row }"><span :class="statusClass(row.status)">{{ stateLabel(row.status) }}</span><p v-if="!isCurrentCapability(row)" class="text-xs text-amber-600">{{ t('admin.accountCapabilities.staleHint') }}</p></template>
          <template #cell-result="{ row }"><div class="capability-result text-xs"><p>{{ resultSummary(row) }}</p><p v-if="row.result?.reason" class="mt-1 text-gray-500">{{ row.result.reason }}</p><p v-if="row.result?.models" class="text-gray-500">{{ t('admin.accountCapabilities.catalogCount', { count: row.result.models.length }) }}</p></div></template>
          <template #cell-requests="{ row }"><span class="tabular-nums">{{ row.request_count }}</span><p v-if="row.request_count_unknown" class="text-xs text-amber-700">{{ t('admin.accountCapabilities.requestCountUnknown') }}</p><p v-if="row.result?.latency_ms != null" class="text-xs text-gray-500">{{ row.result.latency_ms }} ms</p></template>
        </DataTable>
        </div>
        <Pagination v-if="itemPage.total" :total="itemPage.total" :page="itemPage.page" :page-size="itemPage.page_size" @update:page="loadRunItems" @update:page-size="changeItemPageSize" />
      </div>
    </BaseDialog>

    <BaseDialog :show="catalogDialog" :title="t('admin.accountCapabilities.catalog')" width="wide" @close="closeCatalog">
      <div class="space-y-4"><p class="text-sm text-gray-500">{{ t('admin.accountCapabilities.catalogHint') }}</p>
        <input v-model="catalogSearch" class="input" :placeholder="t('admin.accountCapabilities.searchCatalog')" :aria-label="t('admin.accountCapabilities.searchCatalog')" />
        <p v-if="loadingCatalog" class="text-sm text-gray-500">{{ t('common.loading') }}</p>
        <details v-for="item in catalogPage.items" :key="item.id" class="rounded-lg border border-gray-200 p-3 dark:border-dark-700">
          <summary class="cursor-pointer text-sm"><strong>{{ item.account_name }}</strong> · {{ folderName(item.folder_id) }} · {{ resultSummary(item) }} · {{ item.result?.models?.length ?? 0 }}</summary>
          <p v-if="item.result?.reason" class="mt-2 text-xs text-gray-500">{{ item.result.reason }}</p>
          <div class="mt-2 max-h-72 space-y-1 overflow-y-auto"><p v-for="model in catalogModels(item)" :key="model.id" class="break-all font-mono text-xs">{{ model.id }}<span v-if="model.display_name && model.display_name !== model.id" class="text-gray-500"> · {{ model.display_name }}</span></p></div>
        </details>
        <Pagination v-if="catalogPage.total" :total="catalogPage.total" :page="catalogPage.page" :page-size="catalogPage.page_size" @update:page="loadCatalog" @update:page-size="changeCatalogPageSize" />
      </div>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import Icon from '@/components/icons/Icon.vue'
import PublicModelManager from './components/PublicModelManager.vue'
import { listFolders } from '@/api/admin/accounts'
import { getAllIncludingInactive } from '@/api/admin/groups'
import api, { type CapabilityCandidate, type CapabilityChangeset, type CapabilityItem, type CapabilityItemStatus, type CapabilityOverview, type CapabilityPage, type CapabilityPreviewRequest, type CapabilityProbeTarget, type CapabilityProfile, type CapabilityProtocol, type CapabilityPublicationGroup, type CapabilityRun, type CapabilityRunStatus, type CapabilityScopeAccount, type CreateCapabilityRun } from '@/api/admin/accountCapabilities'
import { useAppStore } from '@/stores/app'
import type { AccountManagementFolder, AdminGroup } from '@/types'
import { capabilityCodeLabel, capabilityPublicationTier, deduplicateProbeTargets, isCapabilityRunActive, isCurrentCapability, isPublishableCandidate, makeCapabilityIdempotencyKey, parseCapabilityIDs, selectCapabilityPublicationTargets } from './accountCapabilitiesHelpers'

type Tab = 'overview' | 'inventory' | 'runs' | 'preview'
interface DraftRow { key: string; accountID: number; accountName: string; upstreamModel: string; protocol: CapabilityProtocol; publicModel: string; aliases: string; groupName: string; tier: 'standard' | 'vip'; evidenceID: number }
const { t, te } = useI18n()
const route = useRoute()
const app = useAppStore()
const tabs = computed<Tab[]>(() => ['overview', 'runs', 'inventory', ...(tab.value === 'preview' ? ['preview' as const] : [])])
const requestedTab = String(route.query.tab ?? '')
const tab = ref<Tab>(['inventory', 'runs', 'preview'].includes(requestedTab) ? requestedTab as Tab : 'overview')
const managerLoaded = ref(tab.value === 'overview')
const managerLocked = ref(false)
const managerView = ref<InstanceType<typeof PublicModelManager> | null>(null)
const sourcesReady = ref(false)
const initialModelSearch = String(route.query.model ?? '')
const folders = ref<AccountManagementFolder[]>([])
const groups = ref<AdminGroup[]>([])
const folderIDs = ref<number[]>([])
const accountIDs = ref(parseCapabilityIDs(route.query.account_ids))
const groupIDs = ref(parseCapabilityIDs(route.query.group_ids))
const resolvedScopeAccounts = ref<CapabilityScopeAccount[]>([])
const sourceFolderNames = computed(() => folderIDs.value.map((id) => folders.value.find((folder) => folder.id === id)?.name ?? `#${id}`).join('、'))
const draftScope = ref<{ folder_ids: number[]; account_ids: number[] }>({ folder_ids: [], account_ids: [] })
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const search = ref('')
const candidateStatus = ref('')
const runKind = ref<'' | 'discover' | 'probe'>('')
const runStatus = ref('')
const pageSize = ref(20)
const emptyPage = <T,>(): CapabilityPage<T> => ({ items: [], total: 0, page: 1, page_size: 20 })
const candidatePage = ref(emptyPage<CapabilityCandidate>())
const runPage = ref(emptyPage<CapabilityRun>())
const selections = ref(new Map<string, CapabilityCandidate>())
const selectedKeys = computed(() => [...selections.value.keys()])
const selectedCandidates = computed(() => [...selections.value.values()])
const publishableCandidates = computed(() => selectedCandidates.value.filter(isPublishableCandidate))
const activePage = computed(() => tab.value === 'inventory' ? candidatePage.value : runPage.value)
const probeDialog = ref(false)
const probeTargets = ref<CapabilityProbeTarget[]>([])
const probeProfile = ref<CapabilityProfile | 'original'>('text')
const preserveProbeProfiles = ref(false)
const hasMetadataTargets = computed(() => probeTargets.value.some((item) => ['responses_input_tokens', 'messages_count_tokens'].includes(item.protocol)))
const dedupedTargets = computed(() => deduplicateProbeTargets(probeTargets.value.map((item) => ({ ...item, profile: probeProfile.value === 'original' ? item.profile : probeProfile.value }))))
const plannedRequests = computed(() => dedupedTargets.value.reduce((total, item) => total + (item.profile === 'tool_roundtrip' ? 2 : 1), 0))
const runDialog = ref(false)
const currentRun = ref<CapabilityRun | null>(null)
const itemPage = ref(emptyPage<CapabilityItem>())
const loadingRun = ref(false)
const itemStatus = ref('')
const itemSelections = ref(new Map<number, CapabilityItem>())
const selectedItemKeys = computed(() => [...itemSelections.value.keys()])
const selectedRunItems = computed(() => [...itemSelections.value.values()])
const retryableItems = computed(() => selectedRunItems.value.filter((item) => !!item.upstream_model && isCurrentCapability(item) && !['pending', 'running'].includes(item.status)))
const accountFailureItems = computed(() => selectedRunItems.value.filter((item) => isCurrentCapability(item) && item.result?.account_failure === true && ['credential_invalid', 'account_disabled'].includes(item.result.classification ?? '')))
const catalogDialog = ref(false)
const catalogPage = ref(emptyPage<CapabilityItem>())
const catalogSearch = ref('')
const loadingCatalog = ref(false)
const draftRows = ref<DraftRow[]>([])
const draftAlternatives = ref<CapabilityCandidate[]>([])
const schedulingEvidenceIDs = ref<number[]>([])
const detachIDsInput = ref('')
const restorePreviewID = ref('')
const changeset = ref<CapabilityChangeset | null>(null)
const changesetMatchesCurrentScope = computed(() => {
  if (!changeset.value) return true
  const sameIDs = (left: number[], right: number[]) => left.length === right.length && left.every((id) => right.includes(id))
  if (!sameIDs(changeset.value.scope.folder_ids, folderIDs.value)) return false
  const currentIDs = resolvedScopeAccounts.value.map((account) => account.id)
  return currentIDs.length === 0 || sameIDs(changeset.value.scope.account_ids, currentIDs)
})
const canPreview = computed(() => draftScope.value.account_ids.length > 0 && (draftRows.value.length > 0 || schedulingEvidenceIDs.value.length > 0))
const runStatuses: CapabilityRunStatus[] = ['pending', 'running', 'pausing', 'paused', 'completed', 'canceling', 'canceled']
const itemStatuses: CapabilityItemStatus[] = ['pending', 'running', 'succeeded', 'failed', 'indeterminate', 'stale', 'canceled']
const candidateColumns = computed(() => ['account', 'model', 'group', 'evidence'].map((key) => ({ key, label: t(`admin.accountCapabilities.columns.${key}`) })))
const runColumns = computed(() => ['id', 'status', 'progress', 'request_count', 'created_at', 'actions'].map((key) => ({ key, label: t(`admin.accountCapabilities.columns.${key}`) })))
const itemColumns = computed(() => ['account', 'model', 'status', 'result', 'requests'].map((key) => ({ key, label: t(`admin.accountCapabilities.columns.${key}`) })))
const publicationGroups = computed<CapabilityPublicationGroup[]>(() => {
  const names = ['gpt', 'gpt-vip', 'claude(非逆向渠道)', 'gemini', 'grok(仅4.6)', 'kimi', 'glm', 'deepseek', 'Qwen', 'MiniMax']
  return names.flatMap<CapabilityPublicationGroup>((name) => {
    const group = groups.value.find((item) => item.name === name)
    if (group) return [{ id: group.id, name: group.name, platform: (name === 'claude(非逆向渠道)' ? 'composite' : 'openai') as 'openai' | 'composite', rate_multiplier: group.rate_multiplier, models: [] }]
    if (['Qwen', 'MiniMax'].includes(name)) return [{ name, platform: 'openai' as const, rate_multiplier: 0.3, models: [] }]
    return []
  })
})
const changeSections = computed(() => (['bindings', 'mappings', 'allowlist', 'scheduling', 'routes', 'channel', 'group'] as const)
  .map((kind) => ({ kind, changes: changeset.value?.changes.filter((item) => item.kind === kind) ?? [] })).filter((section) => section.changes.length))

let mounted = false
let readEpoch = 0
let scopeEpoch = 0
let previewEpoch = 0
let runEpoch = 0
let itemEpoch = 0
let catalogEpoch = 0
let pollTimer: ReturnType<typeof setTimeout> | undefined
let readController: AbortController | undefined
let catalogController: AbortController | undefined
const pendingActionKeys = new Map<string, string>()
function folderName(id: number): string { return folders.value.find((folder) => folder.id === id)?.name ?? `#${id}` }
function yesNo(value: boolean): string { return t(value ? 'common.yes' : 'common.no') }
function protocolLabel(value: string): string { return ({ responses: 'Responses', responses_websocket: 'Responses WebSocket', chat_completions: 'Chat Completions', messages: 'Messages', responses_input_tokens: 'Responses input_tokens', messages_count_tokens: 'Messages count_tokens' } as Record<string, string>)[value] ?? value }
function profileLabel(item: CapabilityItem): string { return t(`admin.accountCapabilities.profiles.${['responses_input_tokens', 'messages_count_tokens'].includes(item.protocol) ? 'metadata' : item.profile === 'tool_roundtrip' ? 'tool_roundtrip' : 'text'}`) }
function stateLabel(value: string): string { return capabilityCodeLabel(value, 'states', t, te) }
function reasonLabel(value: string): string { return capabilityCodeLabel(value, 'reasonLabels', t, te) }
function statusClass(value: string): string { return ['succeeded', 'alive', 'completed'].includes(value) ? 'text-emerald-600 dark:text-emerald-300' : ['failed', 'indeterminate', 'stale'].includes(value) ? 'text-amber-700 dark:text-amber-300' : 'text-gray-600 dark:text-gray-300' }
function evidenceClass(value: boolean): string { return `rounded px-1.5 py-0.5 text-[11px] ${value ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/20 dark:text-emerald-300' : 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-dark-300'}` }
function formatTime(value: string): string { const date = new Date(value); return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString() }
function formatDiff(value: unknown): string { return value == null ? '—' : typeof value === 'string' ? value : JSON.stringify(value, null, 2) }
function resultSummary(item: CapabilityItem): string { return [item.result?.classification ? reasonLabel(item.result.classification) : stateLabel(item.result?.status || item.status), item.result?.http_status ? `HTTP ${item.result.http_status}` : '', item.result?.error_code].filter(Boolean).join(' · ') }
function scopeParams() { return { folder_ids: folderIDs.value.join(','), account_ids: accountIDs.value.length ? accountIDs.value.join(',') : undefined, ...(groupIDs.value.length ? { group_ids: groupIDs.value.join(',') } : {}) } }
function overviewResolved(value: CapabilityOverview): void { resolvedScopeAccounts.value = value.accounts; if (!folderIDs.value.length && accountIDs.value.length) folderIDs.value = [...value.scope.folder_ids] }
function clearAccountScope(): void { if (busy.value || managerLocked.value) return; accountIDs.value = []; scopeChanged() }
function clearGroupScope(): void { if (busy.value || managerLocked.value) return; groupIDs.value = []; scopeChanged() }
function clearSelection(): void { selections.value = new Map() }
function updateSelection(keys: Array<string | number>): void {
  const next = new Map(selections.value)
  const wanted = new Set(keys.map(String))
  for (const key of next.keys()) if (!wanted.has(key)) next.delete(key)
  for (const item of candidatePage.value.items) if (wanted.has(item.candidate_id)) next.set(item.candidate_id, item)
  selections.value = next
}
function updateItemSelection(keys: Array<string | number>): void {
  const next = new Map(itemSelections.value)
  const wanted = new Set(keys.map(Number))
  for (const key of next.keys()) if (!wanted.has(key)) next.delete(key)
  for (const item of itemPage.value.items) if (wanted.has(item.id)) next.set(item.id, item)
  itemSelections.value = next
}
function invalidatePreview(): void { previewEpoch++; changeset.value = null }
function scopeChanged(): void {
  scopeEpoch++
  runEpoch++; itemEpoch++; invalidatePreview()
  clearSelection(); itemSelections.value = new Map(); draftRows.value = []; draftAlternatives.value = []; schedulingEvidenceIDs.value = []; resolvedScopeAccounts.value = []; draftScope.value = { folder_ids: [], account_ids: [] }
  currentRun.value = null; runDialog.value = false; closeCatalog(); catalogPage.value = emptyPage()
  candidatePage.value = emptyPage(); runPage.value = emptyPage(); stopPolling()
  if (tab.value === 'overview') return
  void (async () => { if (tab.value !== 'inventory') await loadCandidates(1); await refresh() })()
}
function changeTab(value: Tab): void { if (managerLocked.value && value !== 'overview') return; tab.value = value; error.value = ''; if (value === 'overview') { managerLoaded.value = true; stopPolling(); return }; void refresh() }
function fail(key: 'loadFailed' | 'actionFailed' | 'previewFailed' | 'applyFailed'): void { error.value = t(`admin.accountCapabilities.${key}`) }
function beginRead(): { epoch: number; signal: AbortSignal } {
  readController?.abort(); readController = new AbortController(); loading.value = true
  return { epoch: ++readEpoch, signal: readController.signal }
}
async function loadCandidates(page = candidatePage.value.page): Promise<void> {
  const { epoch, signal } = beginRead()
  if (!folderIDs.value.length) { candidatePage.value = emptyPage(); loading.value = false; return }
  try {
    const data = await api.candidates({ ...scopeParams(), page, page_size: pageSize.value, search: search.value || undefined, status: candidateStatus.value || undefined }, signal)
    if (epoch !== readEpoch || !mounted) return
    candidatePage.value = data
    resolvedScopeAccounts.value = data.accounts
    // Refresh selected evidence from the latest page instead of retaining a
    // stale success badge while configuration has changed on the server.
    for (const item of data.items) if (selections.value.has(item.candidate_id)) selections.value.set(item.candidate_id, item)
    error.value = ''
  } catch { if (!signal.aborted && mounted) fail('loadFailed') }
  finally { if (epoch === readEpoch) loading.value = false }
}
async function loadRuns(page = runPage.value.page): Promise<void> {
  const { epoch, signal } = beginRead()
  if (!folderIDs.value.length) { runPage.value = emptyPage(); loading.value = false; return }
  try {
    const data = await api.listRuns({ ...scopeParams(), page, page_size: pageSize.value, kind: runKind.value || undefined, status: runStatus.value || undefined }, signal)
    if (epoch !== readEpoch || !mounted) return
    runPage.value = data; error.value = ''
  } catch { if (!signal.aborted && mounted) fail('loadFailed') }
  finally { if (epoch === readEpoch) loading.value = false }
}
async function refresh(): Promise<void> {
  stopPolling()
  if (tab.value === 'overview') { await managerView.value?.refresh(); return }
  const trackRuns = hasActiveRun()
  if (tab.value === 'inventory') await loadCandidates()
  else if (tab.value === 'runs') await loadRuns()
  else if (changeset.value) {
    const id = changeset.value.id; const epoch = previewEpoch
    try { const updated = await api.getChangeset(id); if (epoch === previewEpoch && changeset.value?.id === id && mounted) changeset.value = updated } catch { if (epoch === previewEpoch) fail('loadFailed') }
  }
  if (tab.value !== 'runs' && trackRuns) {
    try { runPage.value = await api.listRuns({ ...scopeParams(), page: runPage.value.page, page_size: pageSize.value }) } catch { fail('loadFailed') }
  }
  if (currentRun.value && (runDialog.value || isCapabilityRunActive(currentRun.value.status))) await loadRunDetails()
  // The final request can settle after the first candidate read above. Refresh
  // once more before stopping, so the final success never remains "untested".
  if (tab.value === 'inventory' && trackRuns && !hasActiveRun() && !error.value) await loadCandidates()
  schedulePolling()
}
function stopPolling(): void { if (pollTimer) clearTimeout(pollTimer); pollTimer = undefined }
function hasActiveRun(): boolean { return !!(currentRun.value && isCapabilityRunActive(currentRun.value.status)) || runPage.value.items.some((run) => isCapabilityRunActive(run.status)) }
function schedulePolling(): void {
  stopPolling()
  if (!mounted || document.visibilityState === 'hidden' || !hasActiveRun() || error.value) return
  pollTimer = setTimeout(() => { void refresh() }, 4000)
}
function visibilityChanged(): void { if (document.visibilityState === 'hidden') stopPolling(); else if (hasActiveRun()) void refresh() }
async function refreshRuns(page: number): Promise<void> { await loadRuns(page); schedulePolling() }
function changePage(page: number): void { if (tab.value === 'inventory') void loadCandidates(page); else void refreshRuns(page) }
function changePageSize(size: number): void { pageSize.value = size; changePage(1) }
function actionKey(request: unknown): { signature: string; key: string } {
  const signature = JSON.stringify(request)
  const key = pendingActionKeys.get(signature) ?? makeCapabilityIdempotencyKey()
  pendingActionKeys.set(signature, key)
  return { signature, key }
}
async function createRun(request: CreateCapabilityRun): Promise<void> {
  if (busy.value) return
  busy.value = true; error.value = ''
  const { signature, key } = actionKey(request)
  try {
    const run = await api.createRun(request, key)
    pendingActionKeys.delete(signature); probeDialog.value = false; currentRun.value = run
    tab.value = 'runs'; await loadRuns(1); await openRun(run.id)
    app.showSuccess(t('admin.accountCapabilities.runCreated', { id: run.id, count: run.target_count }))
  } catch { fail('actionFailed') }
  finally { busy.value = false; schedulePolling() }
}
async function discover(): Promise<void> { await createRun({ kind: 'discover', folder_ids: [...folderIDs.value], account_ids: resolvedScopeAccounts.value.map((account) => account.id) }) }
function prepareCandidateProbe(): void {
  probeTargets.value = selectedCandidates.value.filter((item) => !item.stale).map((item) => ({ account_id: item.account_id, upstream_model: item.upstream_model, protocol: item.protocol, profile: 'text', aliases: [...new Set([item.public_model, ...(item.aliases ?? [])])] }))
  preserveProbeProfiles.value = false; probeProfile.value = 'text'; probeDialog.value = true
}
async function startProbe(): Promise<void> {
  await createRun({ kind: 'probe', folder_ids: [...folderIDs.value], account_ids: [...new Set(dedupedTargets.value.map((item) => item.account_id))], items: dedupedTargets.value })
}
async function openRun(id: number): Promise<void> {
  const epoch = ++runEpoch; itemEpoch++; currentRun.value = null
  runDialog.value = true; itemSelections.value = new Map(); itemStatus.value = ''; itemPage.value = emptyPage()
  try { const run = await api.getRun(id); if (epoch !== runEpoch || !mounted) return; currentRun.value = run; await loadRunItems(1) } catch { if (epoch === runEpoch) fail('loadFailed') }
  schedulePolling()
}
function closeRun(): void { runEpoch++; itemEpoch++; runDialog.value = false; itemSelections.value = new Map(); schedulePolling() }
async function loadRunDetails(): Promise<void> {
  if (!currentRun.value || loadingRun.value) return
  const id = currentRun.value.id; const epoch = runEpoch
  try { const run = await api.getRun(id); if (epoch !== runEpoch || currentRun.value?.id !== id || !mounted) return; currentRun.value = run; if (runDialog.value) await loadRunItems() } catch { if (epoch === runEpoch) fail('loadFailed') }
}
async function loadRunItems(page = itemPage.value.page): Promise<void> {
  if (!currentRun.value) return
  const id = currentRun.value.id; const epoch = ++itemEpoch
  loadingRun.value = true
  try {
    const data = await api.listItems(id, { page, page_size: itemPage.value.page_size, status: itemStatus.value || undefined })
    if (epoch !== itemEpoch || currentRun.value?.id !== id || !mounted) return
    itemPage.value = data
    for (const item of data.items) if (itemSelections.value.has(item.id)) itemSelections.value.set(item.id, item)
  } catch { if (epoch === itemEpoch) fail('loadFailed') }
  finally { if (epoch === itemEpoch) loadingRun.value = false }
}
function changeItemPageSize(size: number): void { itemPage.value.page_size = size; void loadRunItems(1) }
async function controlRun(action: 'pause' | 'resume' | 'cancel'): Promise<void> {
  if (!currentRun.value || busy.value) return
  const id = currentRun.value.id; const epoch = runEpoch
  busy.value = true; error.value = ''
  try { const updated = await api.controlRun(id, action); if (epoch !== runEpoch || !mounted || currentRun.value?.id !== id) return; currentRun.value = updated; await loadRuns(); await loadRunItems() } catch { if (epoch === runEpoch) fail('actionFailed') }
  finally { busy.value = false; schedulePolling() }
}
function prepareRetest(): void {
  probeTargets.value = retryableItems.value.map((item) => ({ account_id: item.account_id, upstream_model: item.upstream_model, protocol: item.protocol, profile: item.profile, aliases: item.aliases ?? [] }))
  preserveProbeProfiles.value = true; probeProfile.value = 'original'; runDialog.value = false; probeDialog.value = true
}
function prepareSchedulingPreview(): void {
  schedulingEvidenceIDs.value = accountFailureItems.value.map((item) => item.id)
  draftScope.value = { folder_ids: [...folderIDs.value], account_ids: resolvedScopeAccounts.value.map((account) => account.id) }
  draftRows.value = []; draftAlternatives.value = []; invalidatePreview(); runDialog.value = false; tab.value = 'preview'
}
async function openCatalog(): Promise<void> { catalogDialog.value = true; await loadCatalog(1) }
function closeCatalog(): void { catalogEpoch++; catalogController?.abort(); catalogDialog.value = false; loadingCatalog.value = false }
async function loadCatalog(page = catalogPage.value.page): Promise<void> {
  const epoch = ++catalogEpoch; const scope = scopeEpoch
  catalogController?.abort(); catalogController = new AbortController()
  const signal = catalogController.signal
  loadingCatalog.value = true
  try {
    const data = await api.inventory({ ...scopeParams(), kind: 'discover', page, page_size: catalogPage.value.page_size }, signal)
    if (epoch === catalogEpoch && scope === scopeEpoch && catalogDialog.value && mounted) catalogPage.value = data
  } catch { if (!signal.aborted && epoch === catalogEpoch && scope === scopeEpoch && catalogDialog.value && mounted) fail('loadFailed') }
  finally { if (epoch === catalogEpoch) loadingCatalog.value = false }
}
function changeCatalogPageSize(size: number): void { catalogPage.value.page_size = size; void loadCatalog(1) }
function catalogModels(item: CapabilityItem) { const term = catalogSearch.value.trim().toLowerCase(); return (item.result?.models ?? []).filter((model) => !term || model.id.toLowerCase().includes(term) || model.display_name?.toLowerCase().includes(term)) }
function preparePublication(): void {
  const preferred = selectCapabilityPublicationTargets(publishableCandidates.value)
  const selected = new Set(preferred.selected.map((item) => item.candidate_id))
  draftAlternatives.value = [...new Map(publishableCandidates.value.filter((item) => !selected.has(item.candidate_id)).map((item) => [item.candidate_id, item])).values()]
  draftRows.value = preferred.selected.map((item) => ({ key: item.candidate_id, accountID: item.account_id, accountName: item.account_name, upstreamModel: item.upstream_model, protocol: item.protocol, publicModel: item.public_model, aliases: (item.aliases ?? []).join(', '), groupName: item.group_name, tier: capabilityPublicationTier(item.public_model, item.tier), evidenceID: (item.last_success_item_id ?? item.latest_probe_item_id)! }))
  // Successful route evidence already enables scheduling in the server's
  // preview; this separate list is only for proven account-level failures.
  schedulingEvidenceIDs.value = []
  draftScope.value = { folder_ids: [...folderIDs.value], account_ids: resolvedScopeAccounts.value.map((account) => account.id) }
  invalidatePreview(); tab.value = 'preview'; error.value = ''
}
function removeDraftRow(key: string): void { draftRows.value = draftRows.value.filter((row) => row.key !== key); invalidatePreview() }
function buildPreviewRequest(): CapabilityPreviewRequest {
  const selectedGroups = new Map<string, CapabilityPublicationGroup>()
  for (const row of draftRows.value) {
    const source = publicationGroups.value.find((group) => group.name === row.groupName)
    if (!source || !row.publicModel.trim()) throw new Error('invalid_publication_draft')
    const group = selectedGroups.get(source.name) ?? { ...source, models: [] }
    const aliases = [...new Set(row.aliases.split(/[\n,]/).map((alias) => alias.trim()).filter(Boolean))]
    const model = group.models.find((item) => item.public_model === row.publicModel.trim())
    if (model) { model.evidence_ids = [...new Set([...model.evidence_ids, row.evidenceID])]; model.aliases = [...new Set([...(model.aliases ?? []), ...aliases])] }
    else group.models.push({ public_model: row.publicModel.trim(), aliases, tier: row.tier, evidence_ids: [row.evidenceID] })
    selectedGroups.set(source.name, group)
  }
  if (detachIDsInput.value.trim() && !/^\s*\d+(\s*,\s*\d+)*\s*$/.test(detachIDsInput.value)) throw new Error('invalid_account_ids')
  return { operation: 'merge', scope: { folder_ids: [...draftScope.value.folder_ids], account_ids: [...draftScope.value.account_ids] }, groups: [...selectedGroups.values()], detach_account_ids: parseCapabilityIDs(detachIDsInput.value.replace(/\s/g, '')), scheduling_evidence_ids: [...new Set(schedulingEvidenceIDs.value)] }
}
async function generatePreview(): Promise<void> {
  if (busy.value || !canPreview.value) return
  busy.value = true; error.value = ''; const epoch = ++previewEpoch
  try { const request = buildPreviewRequest(); const { signature, key } = actionKey(request); const result = await api.preview({ ...request, idempotency_key: key }); pendingActionKeys.delete(signature); if (epoch !== previewEpoch || !mounted) return; changeset.value = result; restorePreviewID.value = String(result.id) } catch { if (epoch === previewEpoch) fail('previewFailed') }
  finally { busy.value = false }
}
async function restorePreview(): Promise<void> {
  const id = Number(restorePreviewID.value)
  if (!Number.isSafeInteger(id) || id <= 0 || busy.value) return
  busy.value = true; error.value = ''; const epoch = ++previewEpoch
  try { const result = await api.getChangeset(id); if (epoch === previewEpoch && mounted) changeset.value = result } catch { if (epoch === previewEpoch) fail('loadFailed') }
  finally { busy.value = false }
}
async function applyPreview(): Promise<void> {
  if (!changeset.value || changeset.value.status === 'applied' || busy.value) return
  busy.value = true; error.value = ''; const epoch = previewEpoch
  try { const result = await api.apply(changeset.value.id); if (epoch !== previewEpoch || !mounted) return; changeset.value = result; clearSelection(); app.showSuccess(t('admin.accountCapabilities.applied')); try { groups.value = await getAllIncludingInactive() } catch { fail('loadFailed') } } catch { if (epoch === previewEpoch) fail('applyFailed') }
  finally { busy.value = false }
}

onMounted(async () => {
  mounted = true; document.addEventListener('visibilitychange', visibilityChanged)
  try {
    const [folderData, groupData] = await Promise.all([listFolders(), getAllIncludingInactive()])
    if (!mounted) return
    folders.value = folderData; groups.value = groupData
    const requested = parseCapabilityIDs(route.query.folder_ids)
    folderIDs.value = requested.length ? requested.filter((id) => folderData.some((folder) => folder.id === id)) : accountIDs.value.length ? [] : folderData.filter((folder) => ['dmxapi', '白嫖'].includes(folder.name.trim().toLowerCase())).map((folder) => folder.id)
    sourcesReady.value = true
    if (tab.value === 'overview' && !route.query.changeset_id) return
    if (folderIDs.value.length) runPage.value = await api.listRuns({ ...scopeParams(), page: 1, page_size: pageSize.value })
    const previewID = parseCapabilityIDs(route.query.changeset_id)[0]
    if (previewID) { tab.value = 'preview'; restorePreviewID.value = String(previewID); await restorePreview() }
    else await refresh()
  } catch { fail('loadFailed') }
})
onBeforeUnmount(() => { mounted = false; readEpoch++; previewEpoch++; runEpoch++; itemEpoch++; closeCatalog(); readController?.abort(); stopPolling(); document.removeEventListener('visibilitychange', visibilityChanged) })
</script>

<style scoped>
.capability-page { height: auto; min-height: calc(100vh - 64px - 4rem); gap: 0.75rem; }
.capability-overview :deep(.table-scroll-container-unframed) { height: auto; overflow: visible; }
.capability-overview :deep(.layout-section-scrollable) { display: block; }
.capability-page :deep(.layout-section-scrollable) { min-height: 320px; }
.capability-page :deep(.table-scroll-container) { min-height: 320px; max-height: max(320px, calc(100vh - 300px)); }
.capability-toolbar :deep(.btn) { padding: 0.45rem 0.65rem; font-size: 0.75rem; gap: 0.3rem; }
.capability-toolbar .input { min-height: 2rem; padding-top: 0.4rem; padding-bottom: 0.4rem; font-size: 0.75rem; }
.capability-search { width: 14rem; }
.capability-status-filter { width: 7.5rem; }
.capability-safety { @apply rounded-lg border border-primary-100 bg-primary-50/60 px-3 py-2 text-xs text-primary-800 dark:border-primary-900/50 dark:bg-primary-950/20 dark:text-primary-200; }
.capability-safety summary, .capability-scope-info summary, .capability-view-note summary { cursor: pointer; }
.capability-scope { display: flex; flex-wrap: wrap; align-items: center; gap: 0.375rem; }
.capability-scope-info { @apply text-xs text-gray-500 dark:text-dark-300; }
.capability-scope-info[open], .capability-view-note[open] { flex-basis: 100%; }
.capability-inventory-table { display: flex; flex: 1; min-height: 320px; flex-direction: column; }
.capability-model, .capability-result { min-width: 0; max-width: 100%; white-space: normal; overflow-wrap: anywhere; word-break: break-word; }
.capability-evidence-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 0.25rem; white-space: normal; }
.capability-evidence-grid > span { min-width: 0; line-height: 1.45; }
.capability-group-meta { @apply mt-1 flex flex-wrap gap-x-2 gap-y-0.5 text-gray-500 dark:text-dark-300; font-size: 0.6875rem; line-height: 1.4; overflow-wrap: anywhere; }
.capability-inventory-table :deep(table), .capability-results-table :deep(table) { table-layout: fixed; width: 100%; min-width: 0 !important; }
.capability-inventory-table :deep(th), .capability-inventory-table :deep(td), .capability-results-table :deep(th), .capability-results-table :deep(td) { padding: 0.65rem 0.7rem; white-space: normal; overflow-wrap: anywhere; vertical-align: top; }
.capability-inventory-table :deep(th) { font-size: 0.75rem; }
.capability-inventory-table :deep(th:nth-child(1)), .capability-results-table :deep(th:nth-child(1)) { width: 38px; min-width: 38px; padding-right: 0.35rem; padding-left: 0.35rem; }
.capability-inventory-table :deep(th:nth-child(2)) { width: 16%; }
.capability-inventory-table :deep(th:nth-child(3)) { width: 33%; }
.capability-inventory-table :deep(th:nth-child(4)) { width: 20%; }
.capability-results-table :deep(th:nth-child(2)) { width: 15%; }
.capability-results-table :deep(th:nth-child(3)) { width: 31%; }
.capability-results-table :deep(th:nth-child(4)) { width: 13%; }
.capability-results-table :deep(th:nth-child(6)) { width: 11%; }
@media (max-width: 767px) {
  .capability-page { min-height: 0; }
  .capability-page :deep(.layout-section-scrollable), .capability-page :deep(.table-scroll-container), .capability-inventory-table { min-height: 0; max-height: none; }
  .capability-evidence-grid { min-width: 14rem; }
  .capability-search { width: min(14rem, 100%); }
}
.capability-notice { @apply flex gap-2 rounded-xl border border-primary-100 bg-primary-50/60 p-3 text-xs leading-relaxed text-primary-800 dark:border-primary-900/50 dark:bg-primary-950/20 dark:text-primary-200; }
.capability-tab { @apply rounded-lg px-3 py-2 text-sm font-medium text-gray-500 transition-colors hover:text-gray-900 dark:text-gray-400 dark:hover:text-white; }
.capability-tab-active { @apply bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300; }
.capability-folder { @apply flex cursor-pointer items-center gap-1.5 rounded-lg border border-gray-200 px-2 py-1 text-xs dark:border-dark-600; }
.capability-folder input { @apply rounded border-gray-300 text-primary-600 focus:ring-primary-500 dark:border-dark-500 dark:bg-dark-800; }
.capability-source-details { @apply rounded-lg border border-gray-200 bg-white px-3 py-2 dark:border-dark-700 dark:bg-dark-800; }
.capability-diff { @apply mt-1 max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-lg p-3 font-mono text-xs leading-relaxed; }
</style>
