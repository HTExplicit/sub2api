<template>
  <Teleport to="body">
    <Transition name="fade">
      <div v-if="open" class="fixed inset-0 z-[10000] bg-black/35" aria-hidden="true" @click="emit('close')" />
    </Transition>
    <Transition
      enter-active-class="transition-transform duration-200 ease-out"
      enter-from-class="translate-x-full"
      leave-active-class="transition-transform duration-150 ease-in"
      leave-to-class="translate-x-full"
    >
      <aside
        v-if="open"
        class="fixed inset-y-0 right-0 z-[10001] flex w-full max-w-2xl flex-col border-l border-gray-200 bg-white shadow-2xl dark:border-dark-700 dark:bg-dark-900"
        role="dialog"
        aria-modal="true"
        :aria-label="t('admin.systemPrompts.advanced.title')"
        data-test="system-prompt-advanced-drawer"
        @keydown.esc="emit('close')"
      >
        <header class="flex items-center gap-3 border-b border-gray-200 px-4 py-4 dark:border-dark-700 sm:px-5">
          <div class="min-w-0 flex-1">
            <h2 class="truncate text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.systemPrompts.advanced.title') }}</h2>
            <p class="mt-0.5 truncate text-xs text-gray-500 dark:text-dark-300">{{ sourceTemplateDisplayName || t('admin.systemPrompts.title') }}</p>
          </div>
          <button type="button" class="icon-button" :title="t('admin.systemPrompts.common.close')" @click="emit('close')">
            <Icon name="x" size="sm" />
          </button>
        </header>

        <div class="min-h-0 flex-1 overflow-y-auto">
          <section class="border-b border-gray-100 px-4 py-4 dark:border-dark-700 sm:px-5">
            <div class="mb-3 flex items-center justify-between gap-3">
              <h3 class="text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-dark-300">{{ t('admin.systemPrompts.advanced.runtime') }}</h3>
              <button type="button" class="btn btn-primary btn-sm" :disabled="savingRuntime || !runtimeDirty" @click="emit('save-runtime')">
                <Icon name="check" size="xs" class="mr-1" />{{ savingRuntime ? t('admin.systemPrompts.common.saving') : t('admin.systemPrompts.actions.saveRuntime') }}
              </button>
            </div>
            <div class="divide-y divide-gray-100 border-y border-gray-100 dark:divide-dark-700 dark:border-dark-700">
              <label class="flex items-center justify-between gap-4 py-3">
                <span class="text-sm text-gray-800 dark:text-gray-100">{{ t('admin.systemPrompts.runtime.enabled') }}</span>
                <Toggle :model-value="runtimeDraft.enabled" :aria-label="t('admin.systemPrompts.runtime.enabled')" @update:model-value="emit('runtime-change', { enabled: $event })" />
              </label>
              <label class="flex items-center justify-between gap-4 py-3">
                <span class="text-sm text-gray-800 dark:text-gray-100">{{ t('admin.systemPrompts.runtime.expose') }}</span>
                <Toggle :model-value="runtimeDraft.expose_server_prompt" :aria-label="t('admin.systemPrompts.runtime.expose')" @update:model-value="emit('runtime-change', { expose_server_prompt: $event })" />
              </label>
              <label class="flex items-center justify-between gap-4 py-3">
                <span class="text-sm text-gray-800 dark:text-gray-100">{{ t('admin.systemPrompts.runtime.compact') }}</span>
                <Toggle :model-value="runtimeDraft.compact_enabled" :aria-label="t('admin.systemPrompts.runtime.compact')" @update:model-value="emit('runtime-change', { compact_enabled: $event })" />
              </label>
            </div>
            <div v-if="runtime" class="mt-3 flex flex-wrap items-center gap-3 text-xs text-gray-500 dark:text-dark-400">
              <span>{{ t('admin.systemPrompts.advanced.revision') }} {{ runtime.revision }}</span>
              <span v-if="runtime.degraded" class="badge badge-warning">{{ t('admin.systemPrompts.runtime.degraded') }}</span>
            </div>
          </section>

          <section class="border-b border-gray-100 px-4 py-4 dark:border-dark-700 sm:px-5">
            <div class="mb-3 flex flex-wrap items-center justify-between gap-3">
              <div class="flex items-center gap-2">
                <h3 class="text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-dark-300">{{ t('admin.systemPrompts.advanced.skill') }}</h3>
                <span class="badge badge-info">{{ t('admin.systemPrompts.skillRegistry.modelGang') }}</span>
                <span v-if="skillLoading" class="text-xs text-gray-400">{{ t('admin.systemPrompts.common.loading') }}</span>
              </div>
              <button v-if="skillRegistry" type="button" data-test="system-prompt-skill-sync" class="btn btn-secondary btn-sm" :disabled="skillSyncing" @click="syncSkill">
                <Icon name="refresh" size="xs" class="mr-1" :class="skillSyncing ? 'animate-spin' : ''" />{{ t('admin.systemPrompts.skillRegistry.syncCandidate') }}
              </button>
            </div>

            <label class="mb-3 block text-xs text-gray-600 dark:text-dark-300">
              <span class="mb-1 block font-medium">{{ t('admin.systemPrompts.skillRegistry.promptCapture') }}</span>
              <input
                data-test="system-prompt-skill-prompt-capture"
                type="file"
                accept="text/plain,.txt"
                class="block w-full text-xs file:mr-3 file:border-0 file:bg-gray-100 file:px-3 file:py-2 file:text-xs dark:file:bg-dark-800 dark:file:text-dark-100"
                @change="selectPromptCapture"
              />
            </label>

            <div v-if="!skillRegistry && !skillLoading" class="text-sm text-gray-500 dark:text-dark-400">{{ t('admin.systemPrompts.advanced.skillUnavailable') }}</div>
            <div v-else-if="skillRegistry" class="space-y-3">
              <dl class="grid gap-x-4 gap-y-2 border-y border-gray-100 py-3 text-xs sm:grid-cols-[9rem_minmax(0,1fr)] dark:border-dark-700" data-test="system-prompt-skill-source">
                <dt class="text-gray-500 dark:text-dark-400">{{ t('admin.systemPrompts.skillRegistry.upstreamRoot') }}</dt>
                <dd class="break-all font-mono text-gray-800 dark:text-dark-100">{{ skillRegistry.source.upstream_root }}</dd>
                <dt class="text-gray-500 dark:text-dark-400">{{ t('admin.systemPrompts.skillRegistry.publicRoot') }}</dt>
                <dd class="break-all font-mono text-gray-800 dark:text-dark-100">{{ skillRegistry.source.public_root }}</dd>
              </dl>

              <div class="flex flex-wrap items-center gap-2 text-sm text-gray-800 dark:text-dark-100">
                <span :class="skillRegistry.runtime.active ? 'badge-success' : 'badge-warning'" class="badge">{{ skillRegistry.runtime.active ? t('admin.systemPrompts.skillRegistry.active') : t('admin.systemPrompts.skillRegistry.noActive') }}</span>
                <span class="text-xs text-gray-500 dark:text-dark-400">{{ t('admin.systemPrompts.advanced.revision') }} {{ skillRegistry.runtime.revision }}</span>
              </div>

              <dl v-if="skillRegistry.runtime.active" class="grid gap-x-4 gap-y-2 border-y border-gray-100 py-3 text-xs sm:grid-cols-[9rem_minmax(0,1fr)] dark:border-dark-700" data-test="system-prompt-skill-active">
                <dt class="text-gray-500 dark:text-dark-400">{{ t('admin.systemPrompts.skillRegistry.rawTree') }}</dt>
                <dd class="break-all font-mono text-gray-800 dark:text-dark-100">{{ skillRegistry.runtime.active.raw_tree_sha256 }}</dd>
                <dt class="text-gray-500 dark:text-dark-400">{{ t('admin.systemPrompts.skillRegistry.effectiveTree') }}</dt>
                <dd class="break-all font-mono text-gray-800 dark:text-dark-100">{{ skillRegistry.runtime.active.effective_tree_sha256 }}</dd>
                <template v-if="skillRegistry.runtime.active_prompt">
                  <dt class="text-gray-500 dark:text-dark-400">{{ t('admin.systemPrompts.skillRegistry.rawPrompt') }}</dt>
                  <dd class="break-all font-mono text-gray-800 dark:text-dark-100">{{ skillRegistry.runtime.active_prompt.raw_sha256 }}</dd>
                  <dt class="text-gray-500 dark:text-dark-400">{{ t('admin.systemPrompts.skillRegistry.effectivePrompt') }}</dt>
                  <dd class="break-all font-mono text-gray-800 dark:text-dark-100">{{ skillRegistry.runtime.active_prompt.effective_sha256 }}</dd>
                </template>
                <dt class="text-gray-500 dark:text-dark-400">{{ t('admin.systemPrompts.skillRegistry.files') }}</dt>
                <dd class="text-gray-800 dark:text-dark-100">{{ skillRegistry.runtime.active.file_count }} · {{ formatBytes(skillRegistry.runtime.active.effective_total_bytes) }}</dd>
                <div v-if="skillRegistry.runtime.degraded" class="text-amber-700 sm:col-span-2 dark:text-amber-300">{{ skillRegistry.runtime.degraded_reason || t('admin.systemPrompts.runtime.degraded') }}</div>
              </dl>

              <div v-if="skillSyncJob" class="border-l-2 border-primary-500 px-3 py-2 text-xs text-gray-600 dark:text-dark-300" data-test="system-prompt-skill-job">
                {{ t(`admin.systemPrompts.skillRegistry.status.${skillSyncJob.status}`) }}
                <span v-if="skillSyncJob.prompt_capture_provided"> · {{ t('admin.systemPrompts.skillRegistry.captureIncluded') }}</span>
                <span v-if="skillSyncJob.error_code"> · {{ skillSyncJob.error_code }}</span>
              </div>

              <div v-if="skillCandidate" class="border border-amber-200 bg-amber-50 px-3 py-3 text-xs text-amber-900 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-100" data-test="system-prompt-skill-candidate">
                <div class="flex flex-wrap items-start justify-between gap-3">
                  <dl class="grid min-w-0 flex-1 gap-1">
	                    <div>{{ t('admin.systemPrompts.skillRegistry.rawTree') }} <span class="break-all font-mono">{{ skillCandidate.raw_tree_sha256 }}</span></div>
                    <div>{{ t('admin.systemPrompts.skillRegistry.effectiveTree') }} <span class="break-all font-mono">{{ skillCandidate.effective_tree_sha256 }}</span></div>
	                    <div>{{ t('admin.systemPrompts.skillRegistry.rawPrompt') }} <span class="break-all font-mono">{{ skillCandidate.prompt.raw_sha256 }}</span></div>
                    <div>{{ t('admin.systemPrompts.skillRegistry.effectivePrompt') }} <span class="break-all font-mono">{{ skillCandidate.prompt.effective_sha256 }}</span></div>
                    <div>{{ t('admin.systemPrompts.skillRegistry.changes') }} +{{ skillCandidate.added_files }} / ~{{ skillCandidate.modified_files }} / -{{ skillCandidate.deleted_files }} · {{ t('admin.systemPrompts.skillRegistry.scripts') }} {{ skillCandidate.script_changes }}</div>
	                    <div>{{ t('admin.systemPrompts.skillRegistry.fetchedAt') }} <span class="font-mono">{{ skillCandidate.fetched_at }}</span> · {{ t('admin.systemPrompts.skillRegistry.operator') }} {{ skillCandidate.created_by || '-' }}</div>
                  </dl>
                  <button type="button" data-test="system-prompt-skill-publish-candidate" class="btn btn-primary btn-sm" :disabled="publishingSkill || !skillCandidate.verified" @click="requestSkillPublication(skillCandidate.id, false)">{{ t('admin.systemPrompts.skillRegistry.publishCandidate') }}</button>
                </div>
                <details class="mt-3 border-t border-amber-200 pt-3 dark:border-amber-800" open>
                  <summary class="cursor-pointer font-medium">{{ t('admin.systemPrompts.skillRegistry.promptDiff') }}</summary>
                  <pre data-test="system-prompt-skill-prompt-diff" class="mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-words bg-white/70 p-2 font-mono text-[11px] dark:bg-dark-950/50">{{ skillCandidate.prompt.diff }}</pre>
                </details>
                <details v-if="skillCandidate.file_changes.length" class="mt-3 border-t border-amber-200 pt-3 dark:border-amber-800">
                  <summary class="cursor-pointer font-medium">{{ t('admin.systemPrompts.skillRegistry.fileDiff') }} ({{ skillCandidate.file_changes.length }})</summary>
                  <div class="mt-2 max-h-56 overflow-auto divide-y divide-amber-200 font-mono text-[11px] dark:divide-amber-800">
                    <div v-for="change in skillCandidate.file_changes" :key="`${change.change}:${change.path}`" class="grid grid-cols-[4.5rem_minmax(0,1fr)] gap-2 py-1.5">
                      <span>{{ change.change }}</span><span class="break-all">{{ change.path }}</span>
                    </div>
                  </div>
                </details>
              </div>

              <div v-if="pendingSkillAction" class="flex flex-wrap items-center justify-between gap-3 border-y border-gray-200 py-3 text-xs text-gray-700 dark:border-dark-700 dark:text-dark-200" data-test="system-prompt-skill-confirm">
                <span>{{ pendingSkillAction.rollback ? t('admin.systemPrompts.confirm.skillRollbackMessage') : t('admin.systemPrompts.confirm.skillPublishMessage') }}</span>
                <div class="flex gap-2">
                  <button type="button" class="btn btn-secondary btn-sm" @click="pendingSkillAction = null">{{ t('admin.systemPrompts.common.cancel') }}</button>
                  <button type="button" data-test="system-prompt-skill-confirm-action" class="btn btn-primary btn-sm" :disabled="publishingSkill" @click="confirmSkillPublication">{{ t('admin.systemPrompts.actions.confirm') }}</button>
                </div>
              </div>

              <div v-if="skillRegistry.versions.length" class="overflow-x-auto border-t border-gray-100 pt-3 dark:border-dark-700">
                <table class="min-w-full text-left text-xs">
	                  <thead class="text-gray-500 dark:text-dark-400"><tr><th class="py-2 pr-3">{{ t('admin.systemPrompts.skillRegistry.effectiveTree') }}</th><th class="py-2 pr-3">{{ t('admin.systemPrompts.skillRegistry.files') }}</th><th class="py-2 pr-3">{{ t('admin.systemPrompts.skillRegistry.fetchedAt') }}</th><th class="py-2 pr-3">{{ t('admin.systemPrompts.history.status') }}</th><th class="py-2 text-right">{{ t('admin.systemPrompts.history.actions') }}</th></tr></thead>
                  <tbody class="divide-y divide-gray-100 dark:divide-dark-800">
                    <tr v-for="item in skillRegistry.versions" :key="item.id">
                      <td class="py-2 pr-3 font-mono">{{ shortHash(item.effective_tree_sha256) }}</td>
                      <td class="py-2 pr-3">{{ item.file_count }}</td>
	                      <td class="whitespace-nowrap py-2 pr-3 font-mono">{{ item.fetched_at }}</td>
                      <td class="py-2 pr-3">{{ item.id === skillRegistry.runtime.active?.id ? t('admin.systemPrompts.history.active') : t('admin.systemPrompts.history.candidate') }}</td>
                      <td class="py-2 text-right"><button type="button" class="btn btn-secondary btn-sm" :title="t('admin.systemPrompts.actions.rollback')" :disabled="publishingSkill || item.id === skillRegistry.runtime.active?.id" @click="requestSkillPublication(item.id, true)"><Icon name="refresh" size="xs" /></button></td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </div>
          </section>

          <section v-if="sourceTemplate?.managed_source !== 'remote_skill_registry'" class="px-4 py-4 sm:px-5">
            <div class="mb-3 flex items-center justify-between gap-3">
              <h3 class="text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-dark-300">{{ t('admin.systemPrompts.advanced.source') }}</h3>
              <button v-if="sourceTemplate?.managed_source" type="button" data-test="system-prompt-source-sync" class="btn btn-secondary btn-sm" :disabled="sourceSyncing" @click="emit('sync-source')">
                <Icon name="sync" size="xs" class="mr-1" :class="sourceSyncing ? 'animate-spin' : ''" />{{ t('admin.systemPrompts.source.sync') }}
              </button>
            </div>
            <div v-if="sourceTemplate?.managed_source" class="space-y-2 text-xs text-gray-600 dark:text-dark-300">
              <div class="font-mono">{{ sourceVersion?.source_repository || sourceTemplate.managed_source }}</div>
              <div v-if="sourceVersion?.source_version">{{ sourceVersion.source_version }} · {{ sourceVersion.source_artifact }}</div>
              <div v-if="sourceVersion?.source_commit" class="font-mono">{{ sourceVersion.source_commit }}</div>
              <div v-if="sourceSyncStatus" data-test="system-prompt-source-status" class="text-primary-700 dark:text-primary-300">{{ t(`admin.systemPrompts.source.status.${sourceSyncStatus}`) }}</div>
              <div v-if="sourceCandidate" data-test="system-prompt-source-candidate" class="border-l-2 border-amber-500 px-3 py-2 text-amber-700 dark:text-amber-300">{{ t('admin.systemPrompts.source.candidatePending') }} · v{{ sourceCandidate.version }}</div>
            </div>
            <div v-else class="text-sm text-gray-500 dark:text-dark-400">{{ t('admin.systemPrompts.source.inline') }}</div>
          </section>
        </div>
      </aside>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import Toggle from '@/components/common/Toggle.vue'
import type {
  ManagedSourceSyncStatus,
  ManagedSourceSyncVersion,
  RemoteSkillBundleVersionDetail,
  RemoteSkillRegistryResponse,
  RemoteSkillSyncJob,
  SystemPromptRuntime,
  SystemPromptTemplate,
  SystemPromptVersion,
} from '@/api/admin/systemPrompts'

const props = defineProps<{
  open: boolean
  runtime: SystemPromptRuntime | null
  runtimeDraft: { enabled: boolean; expose_server_prompt: boolean; compact_enabled: boolean }
  runtimeDirty: boolean
  savingRuntime: boolean
  skillRegistry: RemoteSkillRegistryResponse | null
  skillLoading: boolean
  skillSyncJob: RemoteSkillSyncJob | null
  skillCandidate: RemoteSkillBundleVersionDetail | null
  skillSyncing: boolean
  publishingSkill: boolean
  sourceTemplate: SystemPromptTemplate | null
  sourceTemplateDisplayName: string
  sourceVersion: SystemPromptVersion | null
  sourceSyncStatus: ManagedSourceSyncStatus | null
  sourceCandidate: ManagedSourceSyncVersion | null
  sourceSyncing: boolean
}>()

const emit = defineEmits<{
  (event: 'close'): void
  (event: 'runtime-change', value: Partial<typeof props.runtimeDraft>): void
  (event: 'save-runtime'): void
  (event: 'sync-skill', promptCapture?: File): void
  (event: 'publish-skill', id: number, rollback: boolean): void
  (event: 'sync-source'): void
}>()

const { t } = useI18n()
const promptCapture = ref<File | null>(null)
const pendingSkillAction = ref<{ id: number; rollback: boolean } | null>(null)

function selectPromptCapture(event: Event) {
  promptCapture.value = (event.target as HTMLInputElement).files?.[0] || null
}

function syncSkill() {
  emit('sync-skill', promptCapture.value || undefined)
}

function requestSkillPublication(id: number, rollback: boolean) {
  pendingSkillAction.value = { id, rollback }
}

function confirmSkillPublication() {
  if (!pendingSkillAction.value) return
  emit('publish-skill', pendingSkillAction.value.id, pendingSkillAction.value.rollback)
  pendingSkillAction.value = null
}

function shortHash(value: string) {
  return value ? `${value.slice(0, 8)}...${value.slice(-6)}` : '-'
}

function formatBytes(value: number) {
  if (value < 1024) return `${value} B`
  return `${(value / 1024).toFixed(1)} KiB`
}

function handleEscape(event: KeyboardEvent) {
  if (event.key === 'Escape' && props.open) emit('close')
}

onMounted(() => document.addEventListener('keydown', handleEscape))
onUnmounted(() => document.removeEventListener('keydown', handleEscape))
</script>
