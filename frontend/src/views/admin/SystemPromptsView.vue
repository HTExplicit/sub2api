<template>
  <AppLayout>
    <div class="mx-auto max-w-[1500px] space-y-4 px-1">
      <header class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 pb-4 dark:border-dark-700">
        <div class="flex min-w-0 items-center gap-3">
          <span class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-primary-50 text-primary-600 dark:bg-primary-900/30 dark:text-primary-300">
            <Icon name="document" size="md" />
          </span>
          <h1 class="truncate text-xl font-semibold text-gray-900 dark:text-white">{{ t('admin.systemPrompts.title') }}</h1>
        </div>
        <div class="flex items-center gap-2">
          <label class="flex items-center gap-2 text-sm text-gray-600 dark:text-dark-300" data-test="system-prompt-global-toggle">
            <span>{{ runtimeDraft.enabled ? t('admin.systemPrompts.runtime.active') : t('admin.systemPrompts.runtime.disabled') }}</span>
            <Toggle :model-value="runtimeDraft.enabled" :aria-label="t('admin.systemPrompts.runtime.enabled')" @update:model-value="toggleGlobalEnabled" />
          </label>
          <button type="button" class="icon-button" data-test="system-prompt-refresh" :title="t('admin.systemPrompts.common.refresh')" :disabled="loading" @click="loadAll()">
            <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
          </button>
          <button type="button" class="icon-button" data-test="system-prompt-open-advanced" :title="t('admin.systemPrompts.advanced.title')" @click="openAdvanced">
            <Icon name="cog" size="sm" />
          </button>
        </div>
      </header>

      <div v-if="conflict" data-test="system-prompt-conflict" class="flex items-center justify-between gap-3 border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-200">
        <span class="flex min-w-0 items-center gap-2"><Icon name="exclamationTriangle" size="sm" />{{ t('admin.systemPrompts.errors.conflict') }}</span>
        <button type="button" class="btn btn-secondary btn-sm shrink-0" @click="reloadAfterConflict">{{ t('admin.systemPrompts.actions.reload') }}</button>
      </div>

      <div v-if="loading && !runtime" class="flex min-h-[320px] items-center justify-center">
        <div class="h-7 w-7 animate-spin rounded-full border-b-2 border-primary-600"></div>
      </div>

      <template v-else>
        <div class="flex min-w-0 items-center gap-2 xl:hidden">
          <label class="sr-only" for="system-prompt-mobile-template">{{ t('admin.systemPrompts.templates.title') }}</label>
          <select id="system-prompt-mobile-template" :value="selectedId ?? ''" data-test="system-prompt-mobile-template" class="input min-w-0 flex-1" @change="selectTemplateFromMobile">
            <option v-for="template in templates" :key="template.id" :value="template.id">{{ templateDisplayName(template) }}</option>
          </select>
          <button type="button" class="icon-button shrink-0" data-test="system-prompt-mobile-create" :title="t('admin.systemPrompts.actions.create')" @click="openCreate">
            <Icon name="plus" size="sm" />
          </button>
        </div>

        <div class="grid min-w-0 gap-4 xl:grid-cols-[250px_minmax(0,1fr)]">
          <aside class="hidden min-w-0 border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900 xl:block">
            <div class="flex items-center justify-between border-b border-gray-200 px-3 py-3 dark:border-dark-700">
              <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.systemPrompts.templates.title') }}</h2>
              <button type="button" class="icon-button" :title="t('admin.systemPrompts.actions.create')" @click="openCreate">
                <Icon name="plus" size="sm" />
              </button>
            </div>
            <div class="max-h-[calc(100vh-240px)] overflow-y-auto p-2">
              <button
                v-for="template in templates"
                :key="template.id"
                type="button"
                class="mb-1 flex w-full min-w-0 items-center gap-2 border-l-2 px-3 py-3 text-left transition-colors"
                :class="selectedId === template.id ? 'border-primary-500 bg-primary-50 dark:bg-primary-900/20' : 'border-transparent hover:bg-gray-50 dark:hover:bg-dark-800'"
                :data-test="`system-prompt-template-${template.id}`"
                @click="selectTemplate(template.id)"
              >
                <Icon name="document" size="sm" class="shrink-0 text-gray-400" />
                <span class="min-w-0 flex-1 truncate text-sm font-medium text-gray-900 dark:text-white" :title="templateDisplayName(template)">{{ templateDisplayName(template) }}</span>
                <span v-if="runtimeTemplateId === template.id" class="badge badge-success shrink-0">{{ t('admin.systemPrompts.history.active') }}</span>
              </button>
              <div v-if="!templates.length" class="px-3 py-8 text-center text-sm text-gray-500 dark:text-dark-400">{{ t('admin.systemPrompts.templates.empty') }}</div>
            </div>
          </aside>

          <section v-if="detail" class="min-w-0">
            <header class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 pb-3 dark:border-dark-700">
              <div class="flex min-w-0 items-center gap-2">
                <h2 class="truncate text-lg font-semibold text-gray-900 dark:text-white" :title="templateDisplayName(detail.template)">{{ templateDisplayName(detail.template) }}</h2>
                <span v-if="runtimeTemplateId === detail.template.id" class="badge badge-success shrink-0">{{ t('admin.systemPrompts.editor.activeTemplate') }}</span>
              </div>
              <div class="relative flex items-center gap-1">
                <button v-if="!isRemoteSkillManaged" type="button" class="icon-button" data-test="system-prompt-edit-metadata" :title="t('admin.systemPrompts.actions.editMetadata')" @click="openMetadata">
                  <Icon name="edit" size="sm" />
                </button>
                <button v-if="!isRemoteSkillManaged" type="button" class="icon-button" data-test="system-prompt-template-menu" :title="t('admin.systemPrompts.actions.more')" @click="templateMenuOpen = !templateMenuOpen">
                  <Icon name="more" size="sm" />
                </button>
                <div v-if="templateMenuOpen" class="absolute right-0 top-10 z-10 w-40 border border-gray-200 bg-white py-1 shadow-lg dark:border-dark-700 dark:bg-dark-800">
                  <button type="button" class="block w-full px-3 py-2 text-left text-sm text-gray-700 hover:bg-gray-50 dark:text-dark-200 dark:hover:bg-dark-700" @click="openMetadata">{{ t('admin.systemPrompts.actions.editMetadata') }}</button>
                  <button type="button" class="block w-full px-3 py-2 text-left text-sm text-gray-700 hover:bg-gray-50 dark:text-dark-200 dark:hover:bg-dark-700" @click="openDuplicate">{{ t('admin.systemPrompts.actions.duplicate') }}</button>
                  <button type="button" class="block w-full px-3 py-2 text-left text-sm text-red-600 hover:bg-red-50 dark:text-red-300 dark:hover:bg-red-900/20" @click="openConfirm({ kind: 'delete' })">{{ t('admin.systemPrompts.actions.delete') }}</button>
                </div>
              </div>
            </header>

            <div class="flex items-center justify-between gap-3 border-b border-gray-200 dark:border-dark-700" role="tablist">
              <div class="flex items-center gap-1">
                <button type="button" role="tab" data-test="system-prompt-tab-editor" :aria-selected="activeTab === 'editor'" class="border-b-2 px-3 py-2 text-sm font-medium" :class="activeTab === 'editor' ? 'border-primary-500 text-primary-600 dark:text-primary-300' : 'border-transparent text-gray-500 dark:text-dark-400'" @click="activeTab = 'editor'">{{ t('admin.systemPrompts.tabs.editor') }}</button>
                <button type="button" role="tab" data-test="system-prompt-tab-history" :aria-selected="activeTab === 'history'" class="border-b-2 px-3 py-2 text-sm font-medium" :class="activeTab === 'history' ? 'border-primary-500 text-primary-600 dark:text-primary-300' : 'border-transparent text-gray-500 dark:text-dark-400'" @click="activeTab = 'history'">{{ t('admin.systemPrompts.tabs.history') }}</button>
              </div>
              <span v-if="selectedVersion" class="shrink-0 px-2 text-xs text-gray-500 dark:text-dark-400">v{{ selectedVersion.version }}<span v-if="editorDirty" class="ml-2 badge badge-warning">{{ t('admin.systemPrompts.editor.unsaved') }}</span></span>
            </div>

            <div v-if="activeTab === 'editor'" class="space-y-3 pt-4">
              <textarea v-model="body" data-test="system-prompt-body" class="min-h-[430px] w-full resize-y border border-gray-200 bg-white p-4 font-mono text-[13px] leading-6 text-gray-900 outline-none transition-colors focus:border-primary-500 focus:ring-2 focus:ring-primary-500/20 disabled:cursor-not-allowed disabled:bg-gray-50 dark:border-dark-700 dark:bg-dark-900 dark:text-gray-100 dark:disabled:bg-dark-950" spellcheck="false" :disabled="isRemoteSkillManaged" :aria-label="t('admin.systemPrompts.editor.body')"></textarea>
              <div class="flex flex-wrap items-center justify-between gap-3">
                <input v-model="note" type="text" class="input w-full text-sm sm:min-w-0 sm:flex-1" :disabled="isRemoteSkillManaged" :placeholder="t('admin.systemPrompts.editor.notePlaceholder')" :aria-label="t('admin.systemPrompts.editor.note')" />
                <div v-if="!isRemoteSkillManaged" class="flex w-full items-center justify-end gap-2 sm:w-auto sm:shrink-0">
                  <button type="button" data-test="system-prompt-save-version" class="btn btn-primary btn-sm" :disabled="savingVersion || !editorDirty" @click="saveVersion">
                    <Icon name="check" size="sm" class="mr-1" />{{ savingVersion ? t('admin.systemPrompts.common.saving') : t('admin.systemPrompts.actions.saveVersion') }}
                  </button>
                  <button type="button" data-test="system-prompt-set-current" class="btn btn-secondary btn-sm" :disabled="!selectedVersion || selectedVersion.id === runtimeVersionId || editorDirty || publishingPrompt" @click="openConfirm({ kind: 'publish', versionId: selectedVersion?.id })">
                    <Icon name="upload" size="sm" class="mr-1" />{{ publishingPrompt ? t('admin.systemPrompts.common.saving') : t('admin.systemPrompts.actions.setCurrent') }}
                  </button>
                </div>
              </div>
            </div>

            <div v-else class="overflow-hidden border border-gray-200 dark:border-dark-700">
              <div class="overflow-x-auto">
                <table class="min-w-full text-left text-sm">
                  <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-800 dark:text-dark-400"><tr><th class="px-3 py-3">{{ t('admin.systemPrompts.history.version') }}</th><th class="px-3 py-3">{{ t('admin.systemPrompts.history.status') }}</th><th class="px-3 py-3">{{ t('admin.systemPrompts.history.created') }}</th><th class="px-3 py-3 text-right">{{ t('admin.systemPrompts.history.actions') }}</th></tr></thead>
                  <tbody class="divide-y divide-gray-100 dark:divide-dark-800">
                    <tr v-for="version in detail.versions" :key="version.id" class="hover:bg-gray-50 dark:hover:bg-dark-800/60">
                      <td class="whitespace-nowrap px-3 py-3"><button type="button" class="font-semibold text-primary-600 dark:text-primary-300" @click="selectVersion(version)">v{{ version.version }}</button></td>
                      <td class="whitespace-nowrap px-3 py-3"><span :class="version.id === runtimeVersionId ? 'badge-success' : 'badge-gray'" class="badge">{{ version.id === runtimeVersionId ? t('admin.systemPrompts.history.active') : t('admin.systemPrompts.history.candidate') }}</span></td>
                      <td class="whitespace-nowrap px-3 py-3 text-gray-500 dark:text-dark-400">{{ formatDate(version.created_at) }}</td>
                      <td class="whitespace-nowrap px-3 py-3 text-right">
                        <button type="button" class="icon-button mr-1" :title="t('admin.systemPrompts.actions.details')" @click="versionDetails = version"><Icon name="infoCircle" size="sm" /></button>
                        <button v-if="!isRemoteSkillManaged" type="button" class="btn btn-secondary btn-sm" :disabled="version.id === runtimeVersionId || publishingPrompt || editorDirty" @click="openConfirm({ kind: 'rollback', versionId: version.id })"><Icon name="refresh" size="xs" class="mr-1" />{{ t('admin.systemPrompts.actions.rollback') }}</button>
                      </td>
                    </tr>
                    <tr v-if="!detail.versions.length"><td colspan="4" class="px-3 py-10 text-center text-sm text-gray-500 dark:text-dark-400">{{ t('admin.systemPrompts.history.empty') }}</td></tr>
                  </tbody>
                </table>
              </div>
            </div>
          </section>

          <div v-else class="border border-dashed border-gray-300 px-5 py-16 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-dark-400">{{ t('admin.systemPrompts.templates.select') }}</div>
        </div>
      </template>
    </div>

    <SystemPromptAdvancedDrawer
      :open="advancedOpen"
      :runtime="runtime"
      :runtime-draft="runtimeDraft"
      :runtime-dirty="runtimeDirty"
      :saving-runtime="savingRuntime"
      :skill-registry="skillRegistry"
      :skill-loading="skillLoading"
      :skill-sync-job="skillSyncJob"
      :skill-candidate="skillCandidate"
      :skill-syncing="skillSyncInProgress"
      :publishing-skill="publishingSkill"
      :source-template="selectedTemplate"
      :source-template-display-name="selectedTemplate ? templateDisplayName(selectedTemplate) : ''"
      :source-version="selectedVersion"
      :source-sync-status="sourceSyncStatus"
      :source-candidate="sourceCandidate"
      :source-syncing="sourceSyncing"
      @close="advancedOpen = false"
      @runtime-change="updateRuntimeDraft"
      @save-runtime="saveRuntime"
      @sync-skill="startSkillSync"
      @publish-skill="publishSkillBundle"
      @sync-source="syncManagedSource"
    />

    <BaseDialog :show="showMetadataDialog" :title="t('admin.systemPrompts.dialogs.metadataTitle')" width="normal" @close="showMetadataDialog = false">
      <form class="space-y-4" @submit.prevent="saveMetadata">
        <div><label class="input-label">{{ t('admin.systemPrompts.editor.name') }}</label><input v-model.trim="metaName" class="input" required /></div>
        <div><label class="input-label">{{ t('admin.systemPrompts.editor.description') }}</label><textarea v-model="metaDescription" rows="3" class="input resize-y"></textarea></div>
        <div class="flex justify-end gap-2 border-t border-gray-200 pt-4 dark:border-dark-700"><button type="button" class="btn btn-secondary" @click="showMetadataDialog = false">{{ t('admin.systemPrompts.common.cancel') }}</button><button type="submit" class="btn btn-primary" :disabled="savingMetadata">{{ savingMetadata ? t('admin.systemPrompts.common.saving') : t('admin.systemPrompts.actions.saveMetadata') }}</button></div>
      </form>
    </BaseDialog>

    <BaseDialog :show="showCreateDialog" :title="t('admin.systemPrompts.dialogs.createTitle')" width="wide" @close="showCreateDialog = false">
      <form class="space-y-4" @submit.prevent="createTemplate">
        <div class="grid gap-4 sm:grid-cols-2"><div><label class="input-label">{{ t('admin.systemPrompts.dialogs.slug') }}</label><input v-model.trim="createForm.slug" class="input" required /></div><div><label class="input-label">{{ t('admin.systemPrompts.dialogs.name') }}</label><input v-model.trim="createForm.name" class="input" required /></div></div>
        <div><label class="input-label">{{ t('admin.systemPrompts.dialogs.description') }}</label><textarea v-model="createForm.description" rows="2" class="input resize-y"></textarea></div>
        <div><label class="input-label">{{ t('admin.systemPrompts.dialogs.body') }}</label><textarea v-model="createForm.body" rows="12" class="input resize-y font-mono text-xs" spellcheck="false" required></textarea></div>
        <div><label class="input-label">{{ t('admin.systemPrompts.dialogs.note') }}</label><input v-model="createForm.note" class="input" /></div>
        <div class="flex justify-end gap-2 border-t border-gray-200 pt-4 dark:border-dark-700"><button type="button" class="btn btn-secondary" @click="showCreateDialog = false">{{ t('admin.systemPrompts.common.cancel') }}</button><button type="submit" class="btn btn-primary" :disabled="creating"><Icon name="plus" size="sm" class="mr-1" />{{ creating ? t('admin.systemPrompts.common.saving') : t('admin.systemPrompts.actions.create') }}</button></div>
      </form>
    </BaseDialog>

    <BaseDialog v-if="versionDetails" :show="!!versionDetails" :title="`v${versionDetails.version}`" width="wide" @close="versionDetails = null">
      <div class="space-y-3 text-sm text-gray-600 dark:text-dark-300">
        <div v-if="versionDetails.source_repository" class="space-y-1"><div>{{ t('admin.systemPrompts.source.repository') }}: <span class="font-mono">{{ versionDetails.source_repository }}</span></div><div>{{ t('admin.systemPrompts.source.version') }}: {{ versionDetails.source_version }}</div><div>{{ t('admin.systemPrompts.source.commit') }}: <span class="break-all font-mono">{{ versionDetails.source_commit }}</span></div><div>{{ t('admin.systemPrompts.source.artifact') }}: <span class="break-all font-mono">{{ versionDetails.source_artifact }}</span></div><div class="break-all font-mono">{{ versionDetails.source_artifact_sha256 }}</div><div class="break-all font-mono">{{ versionDetails.source_license_sha256 }}</div></div>
        <div v-else>{{ t('admin.systemPrompts.source.inline') }}</div>
      </div>
    </BaseDialog>

    <BaseDialog :show="showDuplicateDialog" :title="t('admin.systemPrompts.dialogs.duplicateTitle')" width="normal" @close="showDuplicateDialog = false">
      <form class="space-y-4" @submit.prevent="duplicateTemplate">
        <div><label class="input-label">{{ t('admin.systemPrompts.dialogs.slug') }}</label><input v-model.trim="duplicateForm.slug" class="input" required /></div>
        <div><label class="input-label">{{ t('admin.systemPrompts.dialogs.name') }}</label><input v-model.trim="duplicateForm.name" class="input" required /></div>
        <div class="flex justify-end gap-2 border-t border-gray-200 pt-4 dark:border-dark-700"><button type="button" class="btn btn-secondary" @click="showDuplicateDialog = false">{{ t('admin.systemPrompts.common.cancel') }}</button><button type="submit" class="btn btn-primary" :disabled="duplicating">{{ duplicating ? t('admin.systemPrompts.common.saving') : t('admin.systemPrompts.actions.duplicate') }}</button></div>
      </form>
    </BaseDialog>

    <ConfirmDialog
      :show="!!confirmState"
      :title="confirmTitle"
      :message="confirmMessage"
      :danger="confirmState?.kind === 'delete'"
      @confirm="confirmAction"
      @cancel="confirmState = null"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import Toggle from '@/components/common/Toggle.vue'
import SystemPromptAdvancedDrawer from '@/components/admin/systemPrompt/SystemPromptAdvancedDrawer.vue'
import { useAppStore } from '@/stores'
import systemPromptsAPI, {
  type ManagedSourceSyncStatus,
  type ManagedSourceSyncVersion,
  type RemoteSkillBundleVersionDetail,
  type RemoteSkillRegistryResponse,
  type RemoteSkillSyncJob,
  type SystemPromptCompositionMode,
  type SystemPromptRuntime,
  type SystemPromptTemplate,
  type SystemPromptVersion,
  type SystemPromptDetailResponse,
} from '@/api/admin/systemPrompts'
import { extractApiErrorCode, extractApiErrorMessage } from '@/utils/apiError'

const { t, locale } = useI18n()
const appStore = useAppStore()

type Tab = 'editor' | 'history'
type ConfirmAction = { kind: 'publish' | 'rollback' | 'delete'; versionId?: number }

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
const metaName = ref('')
const metaDescription = ref('')
const activeTab = ref<Tab>('editor')
const loading = ref(false)
const savingVersion = ref(false)
const savingMetadata = ref(false)
const savingRuntime = ref(false)
const publishingPrompt = ref(false)
const creating = ref(false)
const duplicating = ref(false)
const conflict = ref(false)
const templateMenuOpen = ref(false)
const advancedOpen = ref(false)
const showMetadataDialog = ref(false)
const showCreateDialog = ref(false)
const showDuplicateDialog = ref(false)
const versionDetails = ref<SystemPromptVersion | null>(null)
const confirmState = ref<ConfirmAction | null>(null)

const runtimeDraft = reactive({ enabled: false, expose_server_prompt: false, compact_enabled: false })
const createForm = reactive({ slug: '', name: '', description: '', body: '', note: '', composition_mode: 'inline' as SystemPromptCompositionMode, bundle_id: '', bundle_manifest_sha256: '' })
const duplicateForm = reactive({ slug: '', name: '' })

const skillRegistry = ref<RemoteSkillRegistryResponse | null>(null)
const skillLoading = ref(false)
const skillSyncJob = ref<RemoteSkillSyncJob | null>(null)
const skillCandidate = ref<RemoteSkillBundleVersionDetail | null>(null)
const publishingSkill = ref(false)
const sourceSyncing = ref(false)
const sourceSyncStatus = ref<ManagedSourceSyncStatus | null>(null)
const sourceCandidate = ref<ManagedSourceSyncVersion | null>(null)
let skillSyncTimer: ReturnType<typeof setTimeout> | null = null

const selectedTemplate = computed(() => detail.value?.template ?? null)
const isRemoteSkillManaged = computed(() => selectedTemplate.value?.managed_source === 'remote_skill_registry')
const selectedVersion = computed(() => detail.value?.versions.find(item => item.id === selectedVersionId.value) ?? null)
const latestVersion = computed(() => detail.value?.versions[0] ?? null)
const runtimeTemplateId = computed(() => runtime.value?.template_id ?? 0)
const runtimeVersionId = computed(() => runtime.value?.version_id ?? 0)
const editorDirty = computed(() => {
  const version = selectedVersion.value
  return !!version && (body.value !== version.body || note.value !== version.note)
})
const runtimeDirty = computed(() => !!runtime.value && (runtimeDraft.enabled !== runtime.value.enabled || runtimeDraft.expose_server_prompt !== runtime.value.expose_server_prompt || runtimeDraft.compact_enabled !== runtime.value.compact_enabled))
const skillSyncInProgress = computed(() => skillSyncJob.value?.status === 'queued' || skillSyncJob.value?.status === 'running')

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

function formatDate(value: string) {
  if (!value) return '—'
  try { return new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value)) } catch { return value }
}

function setRuntimeDraft(value: SystemPromptRuntime) {
  runtimeDraft.enabled = value.enabled
  runtimeDraft.expose_server_prompt = value.expose_server_prompt
  runtimeDraft.compact_enabled = value.compact_enabled
}

function templateDisplayName(template: SystemPromptTemplate) {
  if (template.slug === 'codexrip_reverse_skill') return t('admin.systemPrompts.templates.codexripReverseSkill')
  if (template.slug === 'gpt_5_6_instruct') return t('admin.systemPrompts.templates.gpt56Instruct')
  return template.name
}

function applyVersionToEditor(version: SystemPromptVersion) {
  selectedVersionId.value = version.id
  body.value = version.body
  note.value = version.note
  compositionMode.value = version.composition_mode
  bundleId.value = version.bundle_id || ''
  bundleManifestSHA256.value = version.bundle_manifest_sha256 || ''
}

function isConflictError(error: unknown) {
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
    const result = await systemPromptsAPI.list()
    templates.value = result.templates
    runtime.value = result.runtime
    setRuntimeDraft(result.runtime)
    const nextId = preferredId && result.templates.some(item => item.id === preferredId) ? preferredId : result.templates[0]?.id ?? null
    selectedId.value = nextId
    if (nextId) await loadDetail(nextId)
    else detail.value = null
    conflict.value = false
  } catch (error) { handleError(error, t('admin.systemPrompts.errors.load')) } finally { loading.value = false }
}

async function loadDetail(id: number) {
  try {
    const result = await systemPromptsAPI.get(id)
    runtime.value = result.runtime
    setRuntimeDraft(result.runtime)
    detail.value = result
    selectedId.value = id
    metaName.value = result.template.name
    metaDescription.value = result.template.description
    const active = runtime.value?.template_id === id ? result.versions.find(item => item.id === runtime.value?.version_id) : undefined
    const version = active || result.versions[0]
    if (version) applyVersionToEditor(version)
    else { selectedVersionId.value = null; body.value = ''; note.value = ''; compositionMode.value = 'inline'; bundleId.value = ''; bundleManifestSHA256.value = '' }
    sourceSyncStatus.value = null
    sourceCandidate.value = null
    activeTab.value = 'editor'
  } catch (error) { handleError(error, t('admin.systemPrompts.errors.loadDetail')) }
}

async function selectTemplate(id: number) {
  if (id === selectedId.value) return
  if (editorDirty.value) { appStore.showWarning(t('admin.systemPrompts.errors.unsavedSelection')); return }
  await loadDetail(id)
}

async function selectTemplateFromMobile(event: Event) {
  const id = Number((event.target as HTMLSelectElement).value)
  if (id > 0) await selectTemplate(id)
}

function selectVersion(version: SystemPromptVersion) {
  if (version.id === selectedVersionId.value) { activeTab.value = 'editor'; return }
  if (editorDirty.value) { appStore.showWarning(t('admin.systemPrompts.errors.unsavedSelection')); return }
  applyVersionToEditor(version)
  activeTab.value = 'editor'
}

async function saveVersion() {
  if (!detail.value || !runtime.value || !editorDirty.value) return
  const bytes = new TextEncoder().encode(body.value).length
  if (!body.value.trim() || body.value.includes('\u0000') || bytes > 64 * 1024) { appStore.showError(t('admin.systemPrompts.errors.invalidBody')); return }
  savingVersion.value = true
  try {
    const version = await systemPromptsAPI.saveDraft(detail.value.template.id, { body: body.value, note: note.value, composition_mode: compositionMode.value, bundle_id: compositionMode.value === 'inline' ? '' : bundleId.value, bundle_manifest_sha256: '', expected_latest_version: latestVersion.value?.version ?? 0, expected_revision: runtime.value.revision })
    detail.value.versions = [version, ...detail.value.versions]
    applyVersionToEditor(version)
    appStore.showSuccess(t('admin.systemPrompts.messages.versionSaved'))
  } catch (error) { handleError(error, t('admin.systemPrompts.errors.saveVersion')) } finally { savingVersion.value = false }
}

function updateRuntimeDraft(value: Partial<typeof runtimeDraft>) {
  if (value.enabled !== undefined) runtimeDraft.enabled = value.enabled
  if (value.expose_server_prompt !== undefined) runtimeDraft.expose_server_prompt = value.expose_server_prompt
  if (value.compact_enabled !== undefined) runtimeDraft.compact_enabled = value.compact_enabled
}

async function toggleGlobalEnabled(value: boolean) {
  updateRuntimeDraft({ enabled: value })
  await saveRuntime()
}

async function saveRuntime() {
  if (!runtime.value || !runtimeDirty.value) return
  savingRuntime.value = true
  try {
    runtime.value = await systemPromptsAPI.updateRuntime({ expected_revision: runtime.value.revision, enabled: runtimeDraft.enabled, expose_server_prompt: runtimeDraft.expose_server_prompt, compact_enabled: runtimeDraft.compact_enabled })
    setRuntimeDraft(runtime.value)
    appStore.showSuccess(t('admin.systemPrompts.messages.runtimeSaved'))
  } catch (error) { setRuntimeDraft(runtime.value); handleError(error, t('admin.systemPrompts.errors.saveRuntime')) } finally { savingRuntime.value = false }
}

function openAdvanced() {
  advancedOpen.value = true
  void loadSkillRegistry()
}

async function loadSkillRegistry() {
  if (skillRegistry.value || skillLoading.value) return
  skillLoading.value = true
  try { skillRegistry.value = await systemPromptsAPI.getSkillRegistry() } catch (error) { handleError(error, t('admin.systemPrompts.errors.skillLoad')) } finally { skillLoading.value = false }
}

function clearSkillSyncTimer() {
  if (skillSyncTimer !== null) { clearTimeout(skillSyncTimer); skillSyncTimer = null }
}

function scheduleSkillSyncPoll() { clearSkillSyncTimer(); skillSyncTimer = setTimeout(() => void pollSkillSync(), 1200) }

async function pollSkillSync() {
  if (!skillSyncJob.value) return
  try {
    skillSyncJob.value = await systemPromptsAPI.getSkillSync(skillSyncJob.value.id)
    if (skillSyncInProgress.value) { scheduleSkillSyncPoll(); return }
    if (skillSyncJob.value.status === 'succeeded' && skillSyncJob.value.candidate_bundle_version_id) {
      const [candidate, registry] = await Promise.all([systemPromptsAPI.getSkillVersion(skillSyncJob.value.candidate_bundle_version_id), systemPromptsAPI.getSkillRegistry()])
      skillCandidate.value = candidate
      skillRegistry.value = registry
      appStore.showSuccess(t('admin.systemPrompts.messages.skillCandidateReady'))
    } else if (skillSyncJob.value.status === 'failed') appStore.showError(t('admin.systemPrompts.errors.skillSync'))
  } catch (error) { handleError(error, t('admin.systemPrompts.errors.skillSync')) }
}

async function startSkillSync(promptCapture?: File) {
  if (!skillRegistry.value || skillSyncInProgress.value) return
  skillCandidate.value = null
  try { skillSyncJob.value = await systemPromptsAPI.startSkillSync(skillRegistry.value.runtime.revision, promptCapture); scheduleSkillSyncPoll() } catch (error) { handleError(error, t('admin.systemPrompts.errors.skillSync')) }
}

async function publishSkillBundle(versionId: number, rollback: boolean) {
  if (!skillRegistry.value) return
  publishingSkill.value = true
  try {
    await systemPromptsAPI.publishSkillVersion(versionId, skillRegistry.value.runtime.revision, rollback)
    skillRegistry.value = await systemPromptsAPI.getSkillRegistry()
    skillCandidate.value = null
    appStore.showSuccess(rollback ? t('admin.systemPrompts.messages.skillRolledBack') : t('admin.systemPrompts.messages.skillPublished'))
  } catch (error) { handleError(error, rollback ? t('admin.systemPrompts.errors.skillRollback') : t('admin.systemPrompts.errors.skillPublish')) } finally { publishingSkill.value = false }
}

async function syncManagedSource() {
  if (!detail.value || !runtime.value || !selectedTemplate.value?.managed_source) return
  sourceSyncing.value = true
  try {
    const result = await systemPromptsAPI.syncManagedSource(detail.value.template.id, { expected_latest_version: latestVersion.value?.version ?? 0, expected_revision: runtime.value.revision })
    if (result.status === 'candidate_created') await loadDetail(detail.value.template.id)
    sourceSyncStatus.value = result.status
    sourceCandidate.value = result.version || null
    if (result.status === 'candidate_created') appStore.showSuccess(t('admin.systemPrompts.messages.sourceCandidateCreated'))
    else appStore.showSuccess(t(`admin.systemPrompts.source.status.${result.status}`))
  } catch (error) { handleError(error, t('admin.systemPrompts.errors.sourceSync')) } finally { sourceSyncing.value = false }
}

function openMetadata() { templateMenuOpen.value = false; showMetadataDialog.value = true }

async function saveMetadata() {
  if (!detail.value || !runtime.value) return
  savingMetadata.value = true
  try {
    const template = await systemPromptsAPI.updateMetadata(detail.value.template.id, { name: metaName.value, description: metaDescription.value, expected_revision: runtime.value.revision })
    detail.value.template = template
    templates.value = templates.value.map(item => item.id === template.id ? template : item)
    showMetadataDialog.value = false
    appStore.showSuccess(t('admin.systemPrompts.messages.metadataSaved'))
  } catch (error) { handleError(error, t('admin.systemPrompts.errors.saveMetadata')) } finally { savingMetadata.value = false }
}

function openCreate() {
  Object.assign(createForm, { slug: '', name: '', description: '', body: '', note: '', composition_mode: 'inline', bundle_id: '', bundle_manifest_sha256: '' })
  showCreateDialog.value = true
}

async function createTemplate() {
  if (!runtime.value) return
  creating.value = true
  try { const result = await systemPromptsAPI.create({ ...createForm, expected_revision: runtime.value.revision }); showCreateDialog.value = false; await loadAll(result.template.id); appStore.showSuccess(t('admin.systemPrompts.messages.created')) } catch (error) { handleError(error, t('admin.systemPrompts.errors.create')) } finally { creating.value = false }
}

function openDuplicate() {
  templateMenuOpen.value = false
  if (!selectedTemplate.value) return
  Object.assign(duplicateForm, { slug: `${selectedTemplate.value.slug}-copy`, name: `${selectedTemplate.value.name} (${t('admin.systemPrompts.dialogs.copySuffix')})` })
  showDuplicateDialog.value = true
}

async function duplicateTemplate() {
  if (!selectedTemplate.value || !runtime.value) return
  duplicating.value = true
  try { const result = await systemPromptsAPI.duplicate(selectedTemplate.value.id, { ...duplicateForm, expected_revision: runtime.value.revision }); showDuplicateDialog.value = false; await loadAll(result.template.id); appStore.showSuccess(t('admin.systemPrompts.messages.duplicated')) } catch (error) { handleError(error, t('admin.systemPrompts.errors.duplicate')) } finally { duplicating.value = false }
}

function openConfirm(action: ConfirmAction) {
  templateMenuOpen.value = false
  if (action.kind !== 'delete' && editorDirty.value) { appStore.showWarning(t('admin.systemPrompts.errors.saveBeforePublish')); return }
  confirmState.value = action
}

async function confirmAction() {
  const action = confirmState.value
  confirmState.value = null
  if (!action) return
  if (action.kind === 'delete') { await deleteTemplate(); return }
  if (!runtime.value || !selectedTemplate.value || !action.versionId) return
  publishingPrompt.value = true
  try {
    runtime.value = await systemPromptsAPI.publish(selectedTemplate.value.id, action.versionId, runtime.value.revision, action.kind === 'rollback')
    setRuntimeDraft(runtime.value)
    await loadDetail(selectedTemplate.value.id)
    appStore.showSuccess(action.kind === 'rollback' ? t('admin.systemPrompts.messages.rolledBack') : t('admin.systemPrompts.messages.published'))
  } catch (error) { handleError(error, action.kind === 'rollback' ? t('admin.systemPrompts.errors.rollback') : t('admin.systemPrompts.errors.publish')) } finally { publishingPrompt.value = false }
}

async function deleteTemplate() {
  if (!selectedTemplate.value || !runtime.value) return
  try { await systemPromptsAPI.remove(selectedTemplate.value.id, runtime.value.revision); await loadAll(null); appStore.showSuccess(t('admin.systemPrompts.messages.deleted')) } catch (error) { handleError(error, t('admin.systemPrompts.errors.delete')) }
}

async function reloadAfterConflict() { await loadAll(selectedId.value); conflict.value = false }

onMounted(() => void loadAll())
onBeforeUnmount(clearSkillSyncTimer)
</script>
