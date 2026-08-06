<template>
  <AppLayout>
    <div class="mx-auto max-w-[1600px] space-y-5">
      <header class="flex flex-wrap items-start justify-between gap-4">
        <div class="min-w-0">
          <div class="flex items-center gap-3">
            <span class="flex h-10 w-10 items-center justify-center rounded-xl bg-primary-50 text-primary-600 dark:bg-primary-900/30 dark:text-primary-300">
              <Icon name="document" size="lg" />
            </span>
            <div>
              <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">
                {{ t('admin.systemPrompts.title') }}
              </h1>
              <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">
                {{ t('admin.systemPrompts.description') }}
              </p>
            </div>
          </div>
        </div>
        <button type="button" class="btn btn-secondary" :disabled="loading" :title="t('common.refresh')" @click="loadAll()">
          <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
          <span class="ml-1.5">{{ t('common.refresh') }}</span>
        </button>
      </header>

      <section v-if="runtime" data-test="system-prompt-runtime" class="border-y border-gray-200 bg-white/70 px-4 py-4 dark:border-dark-700 dark:bg-dark-900/60 sm:px-5">
        <div class="flex flex-wrap items-center justify-between gap-4">
          <div class="flex min-w-0 items-center gap-3">
            <span :class="runtime.enabled ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300' : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-dark-300'" class="inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-semibold">
              <span class="h-1.5 w-1.5 rounded-full bg-current"></span>
              {{ runtime.enabled ? t('admin.systemPrompts.runtime.active') : t('admin.systemPrompts.runtime.disabled') }}
            </span>
            <span v-if="runtime.degraded" data-test="system-prompt-degraded" class="inline-flex items-center gap-1.5 rounded-full bg-amber-100 px-2.5 py-1 text-xs font-semibold text-amber-700 dark:bg-amber-900/30 dark:text-amber-300">
              <Icon name="exclamationTriangle" size="xs" />
              {{ t('admin.systemPrompts.runtime.degraded') }}
            </span>
            <span v-if="runtime.composition_mode === 'offline_bundle' && !runtime.bundle_available" data-test="system-prompt-bundle-unavailable" class="inline-flex items-center gap-1.5 rounded-full bg-red-100 px-2.5 py-1 text-xs font-semibold text-red-700 dark:bg-red-900/30 dark:text-red-300">
              <Icon name="exclamationTriangle" size="xs" />
              {{ t('admin.systemPrompts.runtime.bundleUnavailable') }}
            </span>
            <span v-else-if="runtime.composition_mode === 'offline_bundle' && runtime.bundle_degraded" data-test="system-prompt-bundle-degraded" class="inline-flex items-center gap-1.5 rounded-full bg-amber-100 px-2.5 py-1 text-xs font-semibold text-amber-700 dark:bg-amber-900/30 dark:text-amber-300">
              <Icon name="exclamationTriangle" size="xs" />
              {{ t('admin.systemPrompts.runtime.bundleDegraded') }}
            </span>
            <span class="truncate font-mono text-xs text-gray-500 dark:text-dark-400">
              rev {{ runtime.revision }} · v{{ runtime.template_version || '—' }} · {{ runtime.sha256 || '—' }} · {{ formatBytes(runtime.byte_length) }}
            </span>
          </div>
          <div class="flex items-center gap-2">
            <button type="button" class="btn btn-primary btn-sm" :disabled="savingRuntime || !runtimeDirty" @click="saveRuntime">
              <Icon name="check" size="sm" class="mr-1" />
              {{ savingRuntime ? t('common.saving') : t('admin.systemPrompts.actions.saveRuntime') }}
            </button>
          </div>
        </div>
        <div class="mt-4 grid gap-3 md:grid-cols-3">
          <label class="flex min-h-[68px] items-center justify-between gap-4 border border-gray-200 bg-gray-50/60 px-4 py-3 dark:border-dark-700 dark:bg-dark-800/60">
            <span class="min-w-0">
              <span class="block text-sm font-medium text-gray-800 dark:text-gray-100">{{ t('admin.systemPrompts.runtime.enabled') }}</span>
              <span class="mt-0.5 block text-xs text-gray-500 dark:text-dark-400">{{ t('admin.systemPrompts.runtime.enabledHint') }}</span>
            </span>
            <Toggle v-model="runtimeDraft.enabled" :aria-label="t('admin.systemPrompts.runtime.enabled')" />
          </label>
          <label class="flex min-h-[68px] items-center justify-between gap-4 border border-gray-200 bg-gray-50/60 px-4 py-3 dark:border-dark-700 dark:bg-dark-800/60">
            <span class="min-w-0">
              <span class="block text-sm font-medium text-gray-800 dark:text-gray-100">{{ t('admin.systemPrompts.runtime.expose') }}</span>
              <span class="mt-0.5 block text-xs text-gray-500 dark:text-dark-400">{{ t('admin.systemPrompts.runtime.exposeHint') }}</span>
            </span>
            <Toggle v-model="runtimeDraft.expose_server_prompt" :aria-label="t('admin.systemPrompts.runtime.expose')" />
          </label>
          <label class="flex min-h-[68px] items-center justify-between gap-4 border border-gray-200 bg-gray-50/60 px-4 py-3 dark:border-dark-700 dark:bg-dark-800/60">
            <span class="min-w-0">
              <span class="block text-sm font-medium text-gray-800 dark:text-gray-100">{{ t('admin.systemPrompts.runtime.compact') }}</span>
              <span class="mt-0.5 block text-xs text-gray-500 dark:text-dark-400">{{ t('admin.systemPrompts.runtime.compactHint') }}</span>
            </span>
            <Toggle v-model="runtimeDraft.compact_enabled" :aria-label="t('admin.systemPrompts.runtime.compact')" />
          </label>
        </div>
      </section>

      <div v-if="conflict" data-test="system-prompt-conflict" class="flex flex-wrap items-center justify-between gap-3 border border-amber-300 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-200">
        <div class="flex items-center gap-2">
          <Icon name="exclamationTriangle" size="sm" />
          <span>{{ t('admin.systemPrompts.errors.conflict') }}</span>
        </div>
        <button type="button" class="btn btn-secondary btn-sm" @click="reloadAfterConflict">
          {{ t('admin.systemPrompts.actions.reload') }}
        </button>
      </div>

      <div v-if="loading && !runtime" class="flex items-center justify-center py-20">
        <div class="h-8 w-8 animate-spin rounded-full border-b-2 border-primary-600"></div>
      </div>

      <div v-else class="grid min-w-0 gap-5 xl:grid-cols-[280px_minmax(0,1fr)]">
        <aside class="min-w-0 border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900">
          <div class="flex items-center justify-between border-b border-gray-200 px-4 py-3 dark:border-dark-700">
            <div>
              <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.systemPrompts.templates.title') }}</h2>
              <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ templates.length }} {{ t('admin.systemPrompts.templates.count') }}</p>
            </div>
            <button type="button" class="btn btn-primary btn-sm" :title="t('admin.systemPrompts.actions.create')" @click="openCreate">
              <Icon name="plus" size="sm" />
              <span class="sr-only">{{ t('admin.systemPrompts.actions.create') }}</span>
            </button>
          </div>
          <div class="max-h-[calc(100vh-300px)] overflow-y-auto p-2">
            <button
              v-for="template in templates"
              :key="template.id"
              type="button"
              class="mb-1 w-full border-l-2 px-3 py-3 text-left transition-colors"
              :class="selectedId === template.id ? 'border-primary-500 bg-primary-50 dark:bg-primary-900/20' : 'border-transparent hover:bg-gray-50 dark:hover:bg-dark-800'"
              @click="selectTemplate(template.id)"
            >
              <span class="flex min-w-0 items-center gap-2">
                <Icon name="document" size="sm" class="flex-shrink-0 text-gray-400" />
                <span class="min-w-0 flex-1 truncate text-sm font-medium text-gray-900 dark:text-white" :title="template.name">{{ template.name }}</span>
                <span v-if="template.is_seed" class="rounded bg-gray-100 px-1.5 py-0.5 text-[10px] font-semibold uppercase text-gray-500 dark:bg-dark-700 dark:text-dark-300">{{ t('admin.systemPrompts.templates.seed') }}</span>
              </span>
              <span class="mt-1 block truncate pl-6 font-mono text-[11px] text-gray-500 dark:text-dark-400">{{ template.slug }}</span>
            </button>
            <div v-if="!templates.length" class="px-3 py-8 text-center text-sm text-gray-500 dark:text-dark-400">
              {{ t('admin.systemPrompts.templates.empty') }}
            </div>
          </div>
        </aside>

        <section v-if="detail" class="min-w-0 space-y-5">
          <div class="border-b border-gray-200 pb-4 dark:border-dark-700">
            <div class="flex flex-wrap items-start justify-between gap-4">
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-center gap-2">
                  <input v-model="metaName" data-test="system-prompt-name" class="input max-w-xl text-lg font-semibold" :aria-label="t('admin.systemPrompts.editor.name')" />
                  <span v-if="detail.template.is_seed" class="badge badge-gray">{{ t('admin.systemPrompts.templates.seed') }}</span>
                  <span v-if="runtimeTemplateId === detail.template.id" class="badge badge-success">{{ t('admin.systemPrompts.editor.activeTemplate') }}</span>
                </div>
                <div class="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-gray-500 dark:text-dark-400">
                  <span class="font-mono">{{ detail.template.slug }}</span>
                  <span>·</span>
                  <span>{{ t('admin.systemPrompts.editor.updated') }} {{ formatDate(detail.template.updated_at) }}</span>
                </div>
                <textarea v-model="metaDescription" rows="2" class="input mt-3 max-w-3xl resize-y text-sm" :placeholder="t('admin.systemPrompts.editor.descriptionPlaceholder')" :aria-label="t('admin.systemPrompts.editor.description')"></textarea>
              </div>
              <div class="flex flex-wrap items-center justify-end gap-2">
                <button type="button" data-test="system-prompt-save-metadata" class="btn btn-secondary btn-sm" :disabled="!metadataDirty || savingMetadata" @click="saveMetadata">
                  <Icon name="check" size="sm" class="mr-1" />
                  {{ t('admin.systemPrompts.actions.saveMetadata') }}
                </button>
                <button type="button" class="btn btn-secondary btn-sm" :title="t('admin.systemPrompts.actions.duplicate')" @click="openDuplicate">
                  <Icon name="copy" size="sm" />
                  <span class="ml-1">{{ t('admin.systemPrompts.actions.duplicate') }}</span>
                </button>
                <button type="button" class="btn btn-secondary btn-sm text-red-600 hover:text-red-700 dark:text-red-400" :title="t('admin.systemPrompts.actions.delete')" @click="openConfirm({ kind: 'delete' })">
                  <Icon name="trash" size="sm" />
                  <span class="ml-1">{{ t('admin.systemPrompts.actions.delete') }}</span>
                </button>
              </div>
            </div>
          </div>

          <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 dark:border-dark-700">
            <div class="flex items-center gap-1" role="tablist">
              <button v-for="tab in tabs" :key="tab" type="button" role="tab" :data-test="`system-prompt-tab-${tab}`" :aria-selected="activeTab === tab" class="border-b-2 px-3 py-2 text-sm font-medium transition-colors" :class="activeTab === tab ? 'border-primary-500 text-primary-600 dark:text-primary-300' : 'border-transparent text-gray-500 hover:text-gray-800 dark:text-dark-400 dark:hover:text-dark-100'" @click="activeTab = tab">
                {{ t(`admin.systemPrompts.tabs.${tab}`) }}
              </button>
            </div>
            <div class="flex items-center gap-2 pb-2 text-xs text-gray-500 dark:text-dark-400">
              <span v-if="selectedVersion">v{{ selectedVersion.version }}</span>
              <span v-if="editorDirty" class="badge badge-warning">{{ t('admin.systemPrompts.editor.unsaved') }}</span>
            </div>
          </div>

          <div v-if="activeTab === 'editor'" class="space-y-4">
            <section class="grid gap-3 border border-gray-200 bg-gray-50/60 p-4 dark:border-dark-700 dark:bg-dark-800/50 lg:grid-cols-[220px_minmax(0,1fr)]">
              <label class="min-w-0">
                <span class="input-label">{{ t('admin.systemPrompts.bundle.compositionMode') }}</span>
                <select v-model="compositionMode" data-test="system-prompt-composition-mode" class="input" @change="onCompositionModeChange">
                  <option value="inline">{{ t('admin.systemPrompts.bundle.inline') }}</option>
                  <option value="offline_bundle">{{ t('admin.systemPrompts.bundle.offline') }}</option>
                </select>
              </label>
              <div v-if="compositionMode === 'offline_bundle'" class="min-w-0 space-y-2">
                <label class="block min-w-0">
                  <span class="input-label">{{ t('admin.systemPrompts.bundle.bundle') }}</span>
                  <select v-model="bundleId" data-test="system-prompt-bundle-select" class="input" @change="onBundleChange">
                    <option value="" disabled>{{ t('admin.systemPrompts.bundle.select') }}</option>
                    <option v-for="item in bundles" :key="`${item.bundle_id}:${item.manifest_sha256}`" :value="item.bundle_id">
                      {{ item.name || item.bundle_id }} · {{ item.available ? t('admin.systemPrompts.bundle.available') : t('admin.systemPrompts.bundle.unavailable') }}
                    </option>
                  </select>
                </label>
                <div class="flex min-w-0 flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-dark-400">
                  <span :class="selectedBundleUsable ? 'badge badge-success' : 'badge badge-danger'">
                    {{ selectedBundleUsable ? t('admin.systemPrompts.bundle.available') : t('admin.systemPrompts.bundle.unavailable') }}
                  </span>
                  <span v-if="selectedBundleInfo?.degraded" class="badge badge-warning">{{ t('admin.systemPrompts.bundle.degraded') }}</span>
                  <span class="max-w-full truncate font-mono" :title="bundleManifestSHA256">{{ bundleManifestSHA256 || '—' }}</span>
                  <span v-if="selectedBundleInfo">{{ selectedBundleInfo.document_count }} {{ t('admin.systemPrompts.bundle.documents') }} · {{ selectedBundleInfo.route_count }} {{ t('admin.systemPrompts.bundle.routes') }} · {{ formatBytes(selectedBundleInfo.total_bytes) }}</span>
                </div>
              </div>
            </section>
            <div class="flex flex-wrap items-center justify-between gap-3">
              <div class="inline-flex border border-gray-200 bg-gray-50 p-1 dark:border-dark-700 dark:bg-dark-800" role="tablist">
                <button type="button" class="px-3 py-1.5 text-xs font-medium" :class="editorMode === 'raw' ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-600 dark:text-white' : 'text-gray-500 dark:text-dark-300'" @click="editorMode = 'raw'">
                  {{ t('admin.systemPrompts.editor.raw') }}
                </button>
                <button type="button" data-test="system-prompt-markdown-mode" class="px-3 py-1.5 text-xs font-medium" :class="editorMode === 'markdown' ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-600 dark:text-white' : 'text-gray-500 dark:text-dark-300'" @click="editorMode = 'markdown'">
                  {{ t('admin.systemPrompts.editor.markdown') }}
                </button>
              </div>
              <div class="flex items-center gap-2">
                <input v-model="note" type="text" class="input w-56 text-sm" :placeholder="t('admin.systemPrompts.editor.notePlaceholder')" :aria-label="t('admin.systemPrompts.editor.note')" />
                <button type="button" data-test="system-prompt-save-draft" class="btn btn-primary btn-sm" :disabled="savingVersion || !editorDirty" @click="saveDraft">
                  <Icon name="check" size="sm" class="mr-1" />
                  {{ savingVersion ? t('common.saving') : t('admin.systemPrompts.actions.saveDraft') }}
                </button>
              </div>
            </div>
            <div class="grid min-w-0 gap-4" :class="editorMode === 'markdown' ? 'xl:grid-cols-2' : 'grid-cols-1'">
              <textarea v-model="body" data-test="system-prompt-body" class="min-h-[440px] w-full resize-y border border-gray-200 bg-white p-4 font-mono text-[13px] leading-6 text-gray-900 outline-none transition-colors focus:border-primary-500 focus:ring-2 focus:ring-primary-500/20 dark:border-dark-700 dark:bg-dark-900 dark:text-gray-100" spellcheck="false" :aria-label="t('admin.systemPrompts.editor.body')"></textarea>
              <div v-if="editorMode === 'markdown'" data-test="system-prompt-markdown-preview" class="min-h-[440px] overflow-auto border border-gray-200 bg-gray-50/60 p-5 dark:border-dark-700 dark:bg-dark-900/60">
                <div class="prose prose-sm max-w-none dark:prose-invert" v-html="renderedMarkdown"></div>
              </div>
            </div>
            <div class="flex flex-wrap items-center justify-between gap-3 text-xs text-gray-500 dark:text-dark-400">
              <span>{{ t('admin.systemPrompts.editor.bytes') }}: {{ formatBytes(currentByteLength) }} / 64 KiB</span>
              <span class="max-w-full truncate font-mono" :title="currentHash">SHA-256 {{ currentHash || '—' }}</span>
            </div>
            <div v-if="selectedVersion" class="flex flex-wrap items-center justify-between gap-3 border-t border-gray-200 pt-3 text-xs text-gray-500 dark:border-dark-700 dark:text-dark-400">
              <span>{{ t('admin.systemPrompts.editor.versionCreated') }} {{ formatDate(selectedVersion.created_at) }}</span>
              <button type="button" data-test="system-prompt-publish-selected" class="btn btn-secondary btn-sm" :disabled="selectedVersion.id === runtimeVersionId || !runtime || !selectedBundleUsable" @click="openConfirm({ kind: 'publish', versionId: selectedVersion.id })">
                <Icon name="upload" size="sm" class="mr-1" />
                {{ t('admin.systemPrompts.actions.publish') }}
              </button>
            </div>
          </div>

          <div v-else-if="activeTab === 'history'" class="overflow-hidden border border-gray-200 dark:border-dark-700">
            <div class="overflow-x-auto">
              <table class="min-w-full divide-y divide-gray-200 text-left text-sm dark:divide-dark-700">
                <thead class="bg-gray-50 text-xs uppercase tracking-wide text-gray-500 dark:bg-dark-800 dark:text-dark-400">
                  <tr>
                    <th class="px-4 py-3">{{ t('admin.systemPrompts.history.version') }}</th>
                    <th class="px-4 py-3">{{ t('admin.systemPrompts.history.source') }}</th>
                    <th class="px-4 py-3">{{ t('admin.systemPrompts.history.hash') }}</th>
                    <th class="px-4 py-3">{{ t('admin.systemPrompts.history.size') }}</th>
                    <th class="px-4 py-3">{{ t('admin.systemPrompts.history.note') }}</th>
                    <th class="px-4 py-3">{{ t('admin.systemPrompts.history.created') }}</th>
                    <th class="px-4 py-3 text-right">{{ t('admin.systemPrompts.history.actions') }}</th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-gray-100 bg-white dark:divide-dark-800 dark:bg-dark-900">
                  <tr v-for="version in detail.versions" :key="version.id" class="hover:bg-gray-50 dark:hover:bg-dark-800/60">
                    <td class="whitespace-nowrap px-4 py-3">
                      <button type="button" class="font-semibold text-primary-600 hover:text-primary-700 dark:text-primary-300" @click="selectVersion(version)">v{{ version.version }}</button>
                      <span v-if="version.id === runtimeVersionId" class="ml-2 badge badge-success">{{ t('admin.systemPrompts.history.active') }}</span>
                    </td>
                    <td class="max-w-[220px] px-4 py-3 text-xs text-gray-600 dark:text-dark-300">
                      <span class="block font-medium">{{ t(`admin.systemPrompts.bundle.${version.composition_mode === 'offline_bundle' ? 'offline' : 'inline'}`) }}</span>
                      <span v-if="version.bundle_id" class="block truncate font-mono text-[11px] text-gray-500 dark:text-dark-400" :title="version.bundle_manifest_sha256">{{ version.bundle_id }}</span>
                    </td>
                    <td class="max-w-[220px] truncate px-4 py-3 font-mono text-xs text-gray-500 dark:text-dark-400" :title="version.sha256">{{ version.sha256 }}</td>
                    <td class="whitespace-nowrap px-4 py-3 text-gray-600 dark:text-dark-300">{{ formatBytes(version.byte_length) }}</td>
                    <td class="max-w-[240px] truncate px-4 py-3 text-gray-600 dark:text-dark-300" :title="version.note">{{ version.note || '—' }}</td>
                    <td class="whitespace-nowrap px-4 py-3 text-gray-500 dark:text-dark-400">{{ formatDate(version.created_at) }}</td>
                    <td class="whitespace-nowrap px-4 py-3 text-right">
                      <button type="button" class="btn btn-secondary btn-sm mr-1" :disabled="version.id === runtimeVersionId || !isVersionBundleUsable(version)" @click="openConfirm({ kind: 'publish', versionId: version.id })">
                        <Icon name="upload" size="xs" class="mr-1" />{{ t('admin.systemPrompts.actions.publish') }}
                      </button>
                      <button type="button" class="btn btn-secondary btn-sm" :disabled="version.id === runtimeVersionId || !isVersionBundleUsable(version)" @click="openConfirm({ kind: 'rollback', versionId: version.id })">
                        <Icon name="refresh" size="xs" class="mr-1" />{{ t('admin.systemPrompts.actions.rollback') }}
                      </button>
                    </td>
                  </tr>
                  <tr v-if="!detail.versions.length">
                    <td colspan="7" class="px-4 py-10 text-center text-sm text-gray-500 dark:text-dark-400">{{ t('admin.systemPrompts.history.empty') }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>

          <div v-else class="space-y-5">
            <div class="grid gap-5 xl:grid-cols-2">
              <section class="border border-gray-200 dark:border-dark-700">
                <div class="border-b border-gray-200 px-4 py-3 dark:border-dark-700">
                  <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.systemPrompts.preview.mergeTitle') }}</h3>
                </div>
                <div class="space-y-3 p-4">
                  <textarea v-model="previewClientInstructions" rows="5" class="input resize-y font-mono text-xs" :placeholder="t('admin.systemPrompts.preview.clientPlaceholder')" :aria-label="t('admin.systemPrompts.preview.client')"></textarea>
                  <button type="button" class="btn btn-secondary btn-sm" :disabled="previewingMerge" @click="runMergePreview">
                    <Icon name="play" size="sm" class="mr-1" />{{ previewingMerge ? t('common.loading') : t('admin.systemPrompts.actions.previewMerge') }}
                  </button>
                  <pre v-if="mergePreview !== null" class="max-h-[280px] overflow-auto whitespace-pre-wrap border border-gray-200 bg-gray-50 p-3 font-mono text-xs leading-5 text-gray-800 dark:border-dark-700 dark:bg-dark-950 dark:text-dark-200">{{ mergePreview }}</pre>
                </div>
              </section>
              <section class="border border-gray-200 dark:border-dark-700">
                <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 px-4 py-3 dark:border-dark-700">
                  <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.systemPrompts.preview.upstreamTitle') }}</h3>
                  <div class="flex items-center gap-3">
                    <select v-model="previewProtocol" class="input h-8 py-1 text-xs" :aria-label="t('admin.systemPrompts.preview.protocol')">
                      <option value="responses">Responses</option>
                      <option value="chat">Chat</option>
                    </select>
                    <label class="flex items-center gap-2 text-xs text-gray-600 dark:text-dark-300">
                      <Toggle v-model="previewCompact" :aria-label="t('admin.systemPrompts.preview.compact')" />
                      {{ t('admin.systemPrompts.preview.compact') }}
                    </label>
                  </div>
                </div>
                <div class="space-y-3 p-4">
                  <textarea v-model="previewBodyText" rows="8" class="input resize-y font-mono text-xs" spellcheck="false" :aria-label="t('admin.systemPrompts.preview.jsonBody')"></textarea>
                  <button type="button" data-test="system-prompt-preview-upstream" class="btn btn-secondary btn-sm" :disabled="previewingUpstream || !selectedBundleUsable" @click="runUpstreamPreview">
                    <Icon name="play" size="sm" class="mr-1" />{{ previewingUpstream ? t('common.loading') : t('admin.systemPrompts.actions.previewUpstream') }}
                  </button>
                  <pre v-if="upstreamPreview" class="max-h-[360px] overflow-auto whitespace-pre-wrap border border-gray-200 bg-gray-50 p-3 font-mono text-xs leading-5 text-gray-800 dark:border-dark-700 dark:bg-dark-950 dark:text-dark-200">{{ prettyUpstream }}</pre>
                </div>
              </section>
            </div>
            <div v-if="upstreamPreview" class="space-y-2 border border-gray-200 px-4 py-3 text-xs text-gray-500 dark:border-dark-700 dark:text-dark-400">
              <div>
                <span class="font-medium text-gray-700 dark:text-dark-200">{{ t('admin.systemPrompts.preview.application') }}:</span>
                {{ upstreamPreview.application.carrier }} · rev {{ upstreamPreview.application.revision }} · {{ formatBytes(upstreamPreview.application.effective_byte_length || 0) }}
              </div>
              <div data-test="system-prompt-preview-effective-hash" class="break-all font-mono">
                {{ t('admin.systemPrompts.preview.effectiveHash') }}: {{ upstreamPreview.application.effective_sha256 || upstreamPreview.application.sha256 || '—' }}
              </div>
              <div v-if="upstreamPreview.application.bundle_id" class="break-all font-mono">
                {{ upstreamPreview.application.bundle_id }} · {{ upstreamPreview.application.bundle_manifest_sha256 || '—' }}
              </div>
              <div v-if="upstreamPreview.application.route_ids?.length || upstreamPreview.application.document_ids?.length" data-test="system-prompt-preview-routing" class="space-y-1">
                <div>{{ t('admin.systemPrompts.preview.routes') }}: {{ upstreamPreview.application.route_ids?.join(', ') || '—' }}</div>
                <div>{{ t('admin.systemPrompts.preview.documents') }}: {{ upstreamPreview.application.document_ids?.join(', ') || '—' }}</div>
              </div>
              <div v-if="upstreamPreview.application.degraded" class="text-amber-700 dark:text-amber-300">
                {{ t('admin.systemPrompts.bundle.degraded') }}<span v-if="upstreamPreview.application.degraded_reason">: {{ upstreamPreview.application.degraded_reason }}</span>
              </div>
            </div>
          </div>
        </section>

        <div v-else class="border border-dashed border-gray-300 px-6 py-16 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-dark-400">
          {{ t('admin.systemPrompts.templates.select') }}
        </div>
      </div>
    </div>

    <BaseDialog :show="showCreateDialog" :title="t('admin.systemPrompts.dialogs.createTitle')" width="wide" @close="showCreateDialog = false">
      <form class="space-y-4" @submit.prevent="createTemplate">
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="input-label">{{ t('admin.systemPrompts.dialogs.slug') }}</label>
            <input v-model.trim="createForm.slug" class="input" required autocomplete="off" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.systemPrompts.dialogs.name') }}</label>
            <input v-model.trim="createForm.name" class="input" required autocomplete="off" />
          </div>
        </div>
        <div>
          <label class="input-label">{{ t('admin.systemPrompts.dialogs.description') }}</label>
          <textarea v-model="createForm.description" rows="2" class="input resize-y"></textarea>
        </div>
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="input-label">{{ t('admin.systemPrompts.bundle.compositionMode') }}</label>
            <select v-model="createForm.composition_mode" class="input" @change="onCreateCompositionModeChange">
              <option value="inline">{{ t('admin.systemPrompts.bundle.inline') }}</option>
              <option value="offline_bundle">{{ t('admin.systemPrompts.bundle.offline') }}</option>
            </select>
          </div>
          <div v-if="createForm.composition_mode === 'offline_bundle'">
            <label class="input-label">{{ t('admin.systemPrompts.bundle.bundle') }}</label>
            <select v-model="createForm.bundle_id" class="input" required @change="onCreateBundleChange">
              <option value="" disabled>{{ t('admin.systemPrompts.bundle.select') }}</option>
              <option v-for="item in bundles" :key="`create:${item.bundle_id}:${item.manifest_sha256}`" :value="item.bundle_id" :disabled="!item.available">
                {{ item.name || item.bundle_id }} · {{ item.available ? t('admin.systemPrompts.bundle.available') : t('admin.systemPrompts.bundle.unavailable') }}
              </option>
            </select>
          </div>
        </div>
        <div>
          <label class="input-label">{{ t('admin.systemPrompts.dialogs.body') }}</label>
          <textarea v-model="createForm.body" rows="12" class="input resize-y font-mono text-xs" spellcheck="false" required></textarea>
        </div>
        <div>
          <label class="input-label">{{ t('admin.systemPrompts.dialogs.note') }}</label>
          <input v-model="createForm.note" class="input" />
        </div>
        <div class="flex justify-end gap-2 border-t border-gray-200 pt-4 dark:border-dark-700">
          <button type="button" class="btn btn-secondary" @click="showCreateDialog = false">{{ t('common.cancel') }}</button>
          <button type="submit" class="btn btn-primary" :disabled="creating"><Icon name="plus" size="sm" class="mr-1" />{{ creating ? t('common.saving') : t('admin.systemPrompts.actions.create') }}</button>
        </div>
      </form>
    </BaseDialog>

    <BaseDialog :show="showDuplicateDialog" :title="t('admin.systemPrompts.dialogs.duplicateTitle')" width="normal" @close="showDuplicateDialog = false">
      <form class="space-y-4" @submit.prevent="duplicateTemplate">
        <div>
          <label class="input-label">{{ t('admin.systemPrompts.dialogs.slug') }}</label>
          <input v-model.trim="duplicateForm.slug" class="input" required autocomplete="off" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.systemPrompts.dialogs.name') }}</label>
          <input v-model.trim="duplicateForm.name" class="input" required autocomplete="off" />
        </div>
        <div class="flex justify-end gap-2 border-t border-gray-200 pt-4 dark:border-dark-700">
          <button type="button" class="btn btn-secondary" @click="showDuplicateDialog = false">{{ t('common.cancel') }}</button>
          <button type="submit" class="btn btn-primary" :disabled="duplicating"><Icon name="copy" size="sm" class="mr-1" />{{ duplicating ? t('common.saving') : t('admin.systemPrompts.actions.duplicate') }}</button>
        </div>
      </form>
    </BaseDialog>

    <ConfirmDialog
      :show="confirmState !== null"
      :title="confirmTitle"
      :message="confirmMessage"
      :danger="confirmState?.kind === 'delete'"
      :confirm-text="t('common.confirm')"
      @confirm="confirmAction"
      @cancel="confirmState = null"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import DOMPurify from 'dompurify'
import { marked } from 'marked'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import Toggle from '@/components/common/Toggle.vue'
import { useAppStore } from '@/stores'
import systemPromptsAPI, {
  type PreviewUpstreamResponse,
  type SystemPromptBundleDetail,
  type SystemPromptBundleSummary,
  type SystemPromptCompositionMode,
  type SystemPromptRuntime,
  type SystemPromptTemplate,
  type SystemPromptVersion,
  type SystemPromptDetailResponse
} from '@/api/admin/systemPrompts'
import { extractApiErrorCode, extractApiErrorMessage } from '@/utils/apiError'

const { t, locale } = useI18n()
const appStore = useAppStore()

type Tab = 'editor' | 'history' | 'preview'
type EditorMode = 'raw' | 'markdown'
type ConfirmAction = { kind: 'publish' | 'rollback' | 'delete'; versionId?: number }

const tabs: Tab[] = ['editor', 'history', 'preview']
const templates = ref<SystemPromptTemplate[]>([])
const detail = ref<SystemPromptDetailResponse | null>(null)
const runtime = ref<SystemPromptRuntime | null>(null)
const selectedId = ref<number | null>(null)
const selectedVersionId = ref<number | null>(null)
const body = ref('')
const note = ref('')
const compositionMode = ref<SystemPromptCompositionMode>('inline')
const bundleId = ref('')
const bundleManifestSHA256 = ref('')
const bundles = ref<SystemPromptBundleSummary[]>([])
const bundleDetail = ref<SystemPromptBundleDetail | null>(null)
const metaName = ref('')
const metaDescription = ref('')
const editorMode = ref<EditorMode>('raw')
const activeTab = ref<Tab>('editor')
const loading = ref(false)
const savingVersion = ref(false)
const savingMetadata = ref(false)
const savingRuntime = ref(false)
const creating = ref(false)
const duplicating = ref(false)
const conflict = ref(false)

const runtimeDraft = reactive({ enabled: false, expose_server_prompt: false, compact_enabled: false })

const showCreateDialog = ref(false)
const showDuplicateDialog = ref(false)
const createForm = reactive({
  slug: '', name: '', description: '', body: '', note: '',
  composition_mode: 'inline' as SystemPromptCompositionMode,
  bundle_id: '', bundle_manifest_sha256: ''
})
const duplicateForm = reactive({ slug: '', name: '' })
const confirmState = ref<ConfirmAction | null>(null)

const previewClientInstructions = ref('')
const previewProtocol = ref<'responses' | 'chat'>('responses')
const previewCompact = ref(false)
const previewBodyText = ref('{\n  "model": "gpt-4o-mini",\n  "input": [{"role": "user", "content": "preview"}]\n}')
const mergePreview = ref<string | null>(null)
const upstreamPreview = ref<PreviewUpstreamResponse | null>(null)
const previewingMerge = ref(false)
const previewingUpstream = ref(false)

const selectedTemplate = computed(() => detail.value?.template ?? null)
const selectedVersion = computed(() => detail.value?.versions.find(version => version.id === selectedVersionId.value) ?? null)
const latestVersion = computed(() => detail.value?.versions[0] ?? null)
const runtimeTemplateId = computed(() => runtime.value?.template_id ?? 0)
const runtimeVersionId = computed(() => runtime.value?.version_id ?? 0)
const selectedBundle = computed(() => bundles.value.find(item => item.bundle_id === bundleId.value) ?? null)
const selectedBundleInfo = computed<SystemPromptBundleSummary | SystemPromptBundleDetail | null>(() => {
  const loaded = bundleDetail.value
  if (loaded?.bundle_id === bundleId.value && loaded.manifest_sha256 === bundleManifestSHA256.value) return loaded
  return selectedBundle.value
})
const selectedBundleUsable = computed(() => {
  if (compositionMode.value !== 'offline_bundle') return true
  const selected = selectedBundle.value
  return !!selected && selected.available && selected.manifest_sha256 === bundleManifestSHA256.value
})
const metadataDirty = computed(() => {
  const template = selectedTemplate.value
  return !!template && (metaName.value !== template.name || metaDescription.value !== template.description)
})
const editorDirty = computed(() => {
  const version = selectedVersion.value
  return !!version && (
    body.value !== version.body ||
    note.value !== version.note ||
    compositionMode.value !== normalizeCompositionMode(version.composition_mode) ||
    bundleId.value !== (version.bundle_id || '') ||
    bundleManifestSHA256.value !== (version.bundle_manifest_sha256 || '')
  )
})
const runtimeDirty = computed(() => {
  if (!runtime.value) return false
  return runtimeDraft.enabled !== runtime.value.enabled ||
    runtimeDraft.expose_server_prompt !== runtime.value.expose_server_prompt ||
    runtimeDraft.compact_enabled !== runtime.value.compact_enabled
})
const currentByteLength = computed(() => new TextEncoder().encode(body.value).length)
const currentHash = computed(() => editorDirty.value ? '' : selectedVersion.value?.sha256 ?? '')
const renderedMarkdown = computed(() => {
  const parsed = marked.parse(body.value, { async: false })
  const html = typeof parsed === 'string' ? parsed : ''
  return DOMPurify.sanitize(html, {
    ALLOWED_TAGS: ['p', 'br', 'strong', 'em', 'del', 'code', 'pre', 'blockquote', 'ul', 'ol', 'li', 'h1', 'h2', 'h3', 'h4', 'hr', 'a'],
    ALLOWED_ATTR: ['href', 'title', 'class', 'rel', 'target'],
    FORBID_ATTR: ['style', 'onerror', 'onclick']
  })
})
const prettyUpstream = computed(() => upstreamPreview.value ? JSON.stringify(upstreamPreview.value.body, null, 2) : '')
const confirmTitle = computed(() => {
  if (confirmState.value?.kind === 'delete') return t('admin.systemPrompts.confirm.deleteTitle')
  if (confirmState.value?.kind === 'rollback') return t('admin.systemPrompts.confirm.rollbackTitle')
  return t('admin.systemPrompts.confirm.publishTitle')
})
const confirmMessage = computed(() => {
  if (confirmState.value?.kind === 'delete') return t('admin.systemPrompts.confirm.deleteMessage')
  if (confirmState.value?.kind === 'rollback') return t('admin.systemPrompts.confirm.rollbackMessage')
  return t('admin.systemPrompts.confirm.publishMessage')
})

function formatBytes(value: number): string {
  if (!value) return '0 B'
  if (value < 1024) return `${value} B`
  return `${(value / 1024).toFixed(1)} KiB`
}

function formatDate(value: string): string {
  if (!value) return '—'
  try {
    return new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
  } catch {
    return value
  }
}

function setRuntimeDraft(value: SystemPromptRuntime) {
  runtimeDraft.enabled = value.enabled
  runtimeDraft.expose_server_prompt = value.expose_server_prompt
  runtimeDraft.compact_enabled = value.compact_enabled
}

function normalizeCompositionMode(value: string | undefined): SystemPromptCompositionMode {
  return value === 'offline_bundle' ? 'offline_bundle' : 'inline'
}

function isVersionBundleUsable(version: SystemPromptVersion): boolean {
  if (normalizeCompositionMode(version.composition_mode) !== 'offline_bundle') return true
  return bundles.value.some(item =>
    item.bundle_id === version.bundle_id &&
    item.manifest_sha256 === version.bundle_manifest_sha256 &&
    item.available
  )
}

async function loadBundleDetail(id: string) {
  if (!id) {
    bundleDetail.value = null
    return
  }
  try {
    bundleDetail.value = await systemPromptsAPI.getBundle(id)
  } catch (error) {
    bundleDetail.value = null
    handleError(error, t('admin.systemPrompts.errors.loadBundle'))
  }
}

function applyVersionToEditor(version: SystemPromptVersion) {
  selectedVersionId.value = version.id
  body.value = version.body
  note.value = version.note
  compositionMode.value = normalizeCompositionMode(version.composition_mode)
  bundleId.value = version.bundle_id || ''
  bundleManifestSHA256.value = version.bundle_manifest_sha256 || ''
  void loadBundleDetail(compositionMode.value === 'offline_bundle' ? bundleId.value : '')
}

function onCompositionModeChange() {
  if (compositionMode.value === 'inline') {
    bundleId.value = ''
    bundleManifestSHA256.value = ''
    bundleDetail.value = null
    return
  }
  const candidate = selectedBundle.value ?? bundles.value.find(item => item.available) ?? bundles.value[0]
  bundleId.value = candidate?.bundle_id ?? ''
  bundleManifestSHA256.value = candidate?.manifest_sha256 ?? ''
  void loadBundleDetail(bundleId.value)
}

function onBundleChange() {
  bundleManifestSHA256.value = selectedBundle.value?.manifest_sha256 ?? ''
  void loadBundleDetail(bundleId.value)
}

function onCreateCompositionModeChange() {
  if (createForm.composition_mode === 'inline') {
    createForm.bundle_id = ''
    createForm.bundle_manifest_sha256 = ''
    return
  }
  const candidate = bundles.value.find(item => item.available)
  createForm.bundle_id = candidate?.bundle_id ?? ''
  createForm.bundle_manifest_sha256 = candidate?.manifest_sha256 ?? ''
}

function onCreateBundleChange() {
  const selected = bundles.value.find(item => item.bundle_id === createForm.bundle_id)
  createForm.bundle_manifest_sha256 = selected?.manifest_sha256 ?? ''
}

function isConflictError(error: unknown): boolean {
  const status = typeof error === 'object' && error !== null ? (error as { status?: number }).status : undefined
  return status === 409 || extractApiErrorCode(error) === 'system_prompt_revision_conflict'
}

function handleError(error: unknown, fallback: string) {
  if (isConflictError(error)) conflict.value = true
  appStore.showError(extractApiErrorMessage(error, fallback))
}

async function loadAll(preferredId: number | null = selectedId.value) {
  loading.value = true
  try {
    const [result, bundleItems] = await Promise.all([
      systemPromptsAPI.list(),
      systemPromptsAPI.listBundles().catch(error => {
        handleError(error, t('admin.systemPrompts.errors.loadBundle'))
        return [] as SystemPromptBundleSummary[]
      })
    ])
    templates.value = result.templates
    bundles.value = bundleItems
    runtime.value = result.runtime
    setRuntimeDraft(result.runtime)
    const nextId = preferredId && result.templates.some(template => template.id === preferredId)
      ? preferredId
      : result.templates[0]?.id ?? null
    selectedId.value = nextId
    if (nextId) await loadDetail(nextId)
    else detail.value = null
    conflict.value = false
  } catch (error) {
    handleError(error, t('admin.systemPrompts.errors.load'))
  } finally {
    loading.value = false
  }
}

async function loadDetail(id: number) {
  try {
    const result = await systemPromptsAPI.get(id)
    detail.value = result
    selectedId.value = id
    metaName.value = result.template.name
    metaDescription.value = result.template.description
    const active = runtime.value && runtime.value.template_id === id
      ? result.versions.find(version => version.id === runtime.value?.version_id)
      : undefined
    const version = result.versions[0] ?? active
    if (version) {
      applyVersionToEditor(version)
    } else {
      selectedVersionId.value = null
      body.value = ''
      note.value = ''
      compositionMode.value = 'inline'
      bundleId.value = ''
      bundleManifestSHA256.value = ''
      bundleDetail.value = null
    }
    mergePreview.value = null
    upstreamPreview.value = null
  } catch (error) {
    handleError(error, t('admin.systemPrompts.errors.loadDetail'))
  }
}

async function selectTemplate(id: number) {
  if (id === selectedId.value) return
  if (editorDirty.value || metadataDirty.value) {
    appStore.showWarning(t('admin.systemPrompts.errors.unsavedSelection'))
    return
  }
  await loadDetail(id)
}

async function selectVersion(version: SystemPromptVersion) {
  if (version.id === selectedVersionId.value) return
  if (editorDirty.value) {
    appStore.showWarning(t('admin.systemPrompts.errors.unsavedSelection'))
    return
  }
  applyVersionToEditor(version)
  activeTab.value = 'editor'
}

async function saveDraft() {
  if (!detail.value || !runtime.value || !editorDirty.value) return
  const bytes = currentByteLength.value
  if (!body.value.trim() || body.value.includes('\u0000') || bytes > 64 * 1024) {
    appStore.showError(t('admin.systemPrompts.errors.invalidBody'))
    return
  }
  if (!selectedBundleUsable.value) {
    appStore.showError(t('admin.systemPrompts.errors.bundleUnavailable'))
    return
  }
  savingVersion.value = true
  try {
    const version = await systemPromptsAPI.saveDraft(detail.value.template.id, {
      body: body.value,
      note: note.value,
      composition_mode: compositionMode.value,
      bundle_id: compositionMode.value === 'offline_bundle' ? bundleId.value : '',
      bundle_manifest_sha256: compositionMode.value === 'offline_bundle' ? bundleManifestSHA256.value : '',
      expected_latest_version: latestVersion.value?.version ?? 0,
      expected_revision: runtime.value.revision
    })
    detail.value.versions = [version, ...detail.value.versions]
    selectedVersionId.value = version.id
    body.value = version.body
    note.value = version.note
    appStore.showSuccess(t('admin.systemPrompts.messages.draftSaved'))
  } catch (error) {
    handleError(error, t('admin.systemPrompts.errors.saveDraft'))
  } finally {
    savingVersion.value = false
  }
}

async function saveMetadata() {
  if (!detail.value || !runtime.value || !metadataDirty.value) return
  savingMetadata.value = true
  try {
    const template = await systemPromptsAPI.updateMetadata(detail.value.template.id, {
      name: metaName.value,
      description: metaDescription.value,
      expected_revision: runtime.value.revision
    })
    detail.value.template = template
    templates.value = templates.value.map(item => item.id === template.id ? template : item)
    appStore.showSuccess(t('admin.systemPrompts.messages.metadataSaved'))
  } catch (error) {
    handleError(error, t('admin.systemPrompts.errors.saveMetadata'))
  } finally {
    savingMetadata.value = false
  }
}

async function saveRuntime() {
  if (!runtime.value || !runtimeDirty.value) return
  savingRuntime.value = true
  try {
    runtime.value = await systemPromptsAPI.updateRuntime({
      expected_revision: runtime.value.revision,
      enabled: runtimeDraft.enabled,
      expose_server_prompt: runtimeDraft.expose_server_prompt,
      compact_enabled: runtimeDraft.compact_enabled
    })
    setRuntimeDraft(runtime.value)
    appStore.showSuccess(t('admin.systemPrompts.messages.runtimeSaved'))
  } catch (error) {
    handleError(error, t('admin.systemPrompts.errors.saveRuntime'))
  } finally {
    savingRuntime.value = false
  }
}

function openCreate() {
  Object.assign(createForm, {
    slug: '', name: '', description: '', body: '', note: '',
    composition_mode: 'inline', bundle_id: '', bundle_manifest_sha256: ''
  })
  showCreateDialog.value = true
}

async function createTemplate() {
  if (!runtime.value) return
  if (createForm.composition_mode === 'offline_bundle') {
    const selected = bundles.value.find(item => item.bundle_id === createForm.bundle_id)
    if (!selected?.available || selected.manifest_sha256 !== createForm.bundle_manifest_sha256) {
      appStore.showError(t('admin.systemPrompts.errors.bundleUnavailable'))
      return
    }
  }
  creating.value = true
  try {
    const result = await systemPromptsAPI.create({ ...createForm, expected_revision: runtime.value.revision })
    showCreateDialog.value = false
    await loadAll(result.template.id)
    appStore.showSuccess(t('admin.systemPrompts.messages.created'))
  } catch (error) {
    handleError(error, t('admin.systemPrompts.errors.create'))
  } finally {
    creating.value = false
  }
}

function openDuplicate() {
  if (!selectedTemplate.value) return
  Object.assign(duplicateForm, { slug: `${selectedTemplate.value.slug}-copy`, name: `${selectedTemplate.value.name} (${t('admin.systemPrompts.dialogs.copySuffix')})` })
  showDuplicateDialog.value = true
}

async function duplicateTemplate() {
  if (!selectedTemplate.value || !runtime.value) return
  duplicating.value = true
  try {
    const result = await systemPromptsAPI.duplicate(selectedTemplate.value.id, {
      ...duplicateForm,
      expected_revision: runtime.value.revision
    })
    showDuplicateDialog.value = false
    await loadAll(result.template.id)
    appStore.showSuccess(t('admin.systemPrompts.messages.duplicated'))
  } catch (error) {
    handleError(error, t('admin.systemPrompts.errors.duplicate'))
  } finally {
    duplicating.value = false
  }
}

function openConfirm(action: ConfirmAction) {
  if (action.kind === 'publish' && editorDirty.value) {
    appStore.showWarning(t('admin.systemPrompts.errors.saveBeforePublish'))
    return
  }
  confirmState.value = action
}

async function confirmAction() {
  const action = confirmState.value
  confirmState.value = null
  if (!action) return
  if (action.kind === 'delete') {
    await deleteTemplate()
    return
  }
  if (!runtime.value || !selectedTemplate.value || !action.versionId) return
  try {
    runtime.value = await systemPromptsAPI.publish(selectedTemplate.value.id, action.versionId, runtime.value.revision, action.kind === 'rollback')
    setRuntimeDraft(runtime.value)
    await loadDetail(selectedTemplate.value.id)
    appStore.showSuccess(action.kind === 'rollback' ? t('admin.systemPrompts.messages.rolledBack') : t('admin.systemPrompts.messages.published'))
  } catch (error) {
    handleError(error, action.kind === 'rollback' ? t('admin.systemPrompts.errors.rollback') : t('admin.systemPrompts.errors.publish'))
  }
}

async function deleteTemplate() {
  if (!selectedTemplate.value || !runtime.value) return
  try {
    await systemPromptsAPI.remove(selectedTemplate.value.id, runtime.value.revision)
    await loadAll(null)
    appStore.showSuccess(t('admin.systemPrompts.messages.deleted'))
  } catch (error) {
    handleError(error, t('admin.systemPrompts.errors.delete'))
  }
}

async function runMergePreview() {
  previewingMerge.value = true
  try {
    let requestBody: unknown
    try {
      requestBody = JSON.parse(previewBodyText.value)
    } catch {
      requestBody = undefined
    }
    const result = await systemPromptsAPI.previewMerge({
      template_id: !editorDirty.value ? selectedTemplate.value?.id : 0,
      version_id: !editorDirty.value ? selectedVersion.value?.id : 0,
      client_instructions: previewClientInstructions.value,
      server_instructions: body.value,
      composition_mode: compositionMode.value,
      bundle_id: compositionMode.value === 'offline_bundle' ? bundleId.value : '',
      bundle_manifest_sha256: compositionMode.value === 'offline_bundle' ? bundleManifestSHA256.value : '',
      body: requestBody
    })
    mergePreview.value = result.instructions
  } catch (error) {
    handleError(error, t('admin.systemPrompts.errors.preview'))
  } finally {
    previewingMerge.value = false
  }
}

async function runUpstreamPreview() {
  let parsedBody: unknown
  try {
    parsedBody = JSON.parse(previewBodyText.value)
  } catch {
    appStore.showError(t('admin.systemPrompts.errors.invalidJson'))
    return
  }
  previewingUpstream.value = true
  try {
    upstreamPreview.value = await systemPromptsAPI.previewUpstream({
      template_id: !editorDirty.value ? selectedTemplate.value?.id ?? 0 : 0,
      version_id: !editorDirty.value ? selectedVersion.value?.id ?? 0 : 0,
      server_instructions: body.value,
      composition_mode: compositionMode.value,
      bundle_id: compositionMode.value === 'offline_bundle' ? bundleId.value : '',
      bundle_manifest_sha256: compositionMode.value === 'offline_bundle' ? bundleManifestSHA256.value : '',
      protocol: previewProtocol.value,
      compact: previewCompact.value,
      body: parsedBody
    })
  } catch (error) {
    handleError(error, t('admin.systemPrompts.errors.preview'))
  } finally {
    previewingUpstream.value = false
  }
}

async function reloadAfterConflict() {
  await loadAll(selectedId.value)
  conflict.value = false
}

onMounted(() => loadAll())
</script>
