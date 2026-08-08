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
        class="fixed inset-y-0 right-0 z-[10001] flex w-full max-w-xl flex-col border-l border-gray-200 bg-white shadow-2xl dark:border-dark-700 dark:bg-dark-900"
        role="dialog"
        aria-modal="true"
        :aria-label="t('admin.systemPrompts.advanced.title')"
        data-test="system-prompt-advanced-drawer"
        @keydown.esc="emit('close')"
      >
        <header class="flex items-center gap-3 border-b border-gray-200 px-4 py-4 dark:border-dark-700 sm:px-5">
          <div class="min-w-0 flex-1">
            <h2 class="truncate text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.systemPrompts.advanced.title') }}</h2>
            <p class="mt-0.5 truncate text-xs text-gray-500 dark:text-dark-300">{{ sourceTemplate?.name || t('admin.systemPrompts.title') }}</p>
          </div>
          <button type="button" class="icon-button" :title="t('common.close')" @click="emit('close')">
            <Icon name="x" size="sm" />
          </button>
        </header>

        <div class="min-h-0 flex-1 overflow-y-auto">
          <section class="border-b border-gray-100 px-4 py-4 dark:border-dark-700 sm:px-5">
            <div class="mb-3 flex items-center justify-between gap-3">
              <h3 class="text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-dark-300">{{ t('admin.systemPrompts.advanced.runtime') }}</h3>
              <button type="button" class="btn btn-primary btn-sm" :disabled="savingRuntime || !runtimeDirty" @click="emit('save-runtime')">
                <Icon name="check" size="xs" class="mr-1" />{{ savingRuntime ? t('common.saving') : t('admin.systemPrompts.actions.saveRuntime') }}
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
            <div class="mb-3 flex items-center justify-between gap-3">
              <div class="flex items-center gap-2">
                <h3 class="text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-dark-300">{{ t('admin.systemPrompts.advanced.skill') }}</h3>
                <span v-if="skillLoading" class="text-xs text-gray-400">{{ t('common.loading') }}</span>
              </div>
              <button v-if="skillRegistry" type="button" data-test="system-prompt-skill-sync" class="btn btn-secondary btn-sm" :disabled="skillSyncing" @click="emit('sync-skill')">
                <Icon name="refresh" size="xs" class="mr-1" :class="skillSyncing ? 'animate-spin' : ''" />{{ t('admin.systemPrompts.skillRegistry.sync') }}
              </button>
            </div>

            <div v-if="!skillRegistry && !skillLoading" class="text-sm text-gray-500 dark:text-dark-400">{{ t('admin.systemPrompts.advanced.skillUnavailable') }}</div>
            <div v-else-if="skillRegistry" class="space-y-3">
              <div class="flex flex-wrap items-center gap-2 text-sm text-gray-800 dark:text-dark-100">
                <span :class="skillRegistry.runtime.active ? 'badge-success' : 'badge-warning'" class="badge">{{ skillRegistry.runtime.active ? t('admin.systemPrompts.skillRegistry.active') : t('admin.systemPrompts.skillRegistry.noActive') }}</span>
                <span class="text-xs text-gray-500 dark:text-dark-400">{{ t('admin.systemPrompts.advanced.revision') }} {{ skillRegistry.runtime.revision }}</span>
              </div>
              <div v-if="skillRegistry.runtime.active" class="text-xs text-gray-500 dark:text-dark-400">
                {{ t('admin.systemPrompts.skillRegistry.commit') }} <span class="font-mono">{{ shortHash(skillRegistry.runtime.active.source_commit) }}</span>
              </div>
              <div v-if="skillSyncJob" class="border-l-2 border-primary-500 px-3 py-2 text-xs text-gray-600 dark:text-dark-300" data-test="system-prompt-skill-job">
                {{ t(`admin.systemPrompts.skillRegistry.status.${skillSyncJob.status}`) }}<span v-if="skillSyncJob.error_code"> · {{ skillSyncJob.error_code }}</span>
              </div>
              <div v-if="skillCandidate" class="flex flex-wrap items-center justify-between gap-3 border border-amber-200 bg-amber-50 px-3 py-3 text-xs text-amber-800 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-200" data-test="system-prompt-skill-candidate">
                <span>{{ t('admin.systemPrompts.skillRegistry.candidate') }}</span>
                <button type="button" class="btn btn-primary btn-sm" :disabled="publishingSkill || !skillCandidate.verified" @click="emit('publish-skill', skillCandidate.id, false)">{{ t('admin.systemPrompts.skillRegistry.publishCandidate') }}</button>
              </div>
              <div class="border-t border-gray-100 pt-3 dark:border-dark-700">
                <div class="mb-2 flex items-center justify-between gap-2">
                  <span class="text-xs font-medium text-gray-700 dark:text-dark-200">{{ t('admin.systemPrompts.skillRegistry.clientInstall') }}</span>
                  <select v-model="installPlatform" class="input h-8 w-auto py-1 text-xs" :aria-label="t('admin.systemPrompts.advanced.installPlatform')">
                    <option value="powershell">PowerShell 7</option>
                    <option value="python">Python 3</option>
                  </select>
                </div>
                <div class="grid grid-cols-2 gap-2">
                  <button type="button" class="btn btn-secondary btn-sm" data-test="system-prompt-copy-acquire" @click="emit('copy-install', selectedInstaller?.acquire_command || '', 'acquire')">
                    <Icon name="copy" size="xs" class="mr-1" />{{ t('admin.systemPrompts.advanced.copyAcquire') }}
                  </button>
                  <button type="button" class="btn btn-secondary btn-sm" data-test="system-prompt-copy-execute" @click="emit('copy-install', selectedInstaller?.execute_command || '', 'execute')">
                    <Icon name="copy" size="xs" class="mr-1" />{{ t('admin.systemPrompts.advanced.copyExecute') }}
                  </button>
                </div>
              </div>
              <div v-if="skillRegistry.versions.length" class="overflow-x-auto border-t border-gray-100 pt-3 dark:border-dark-700">
                <table class="min-w-full text-left text-xs">
                  <thead class="text-gray-500 dark:text-dark-400"><tr><th class="py-2 pr-3">{{ t('admin.systemPrompts.history.version') }}</th><th class="py-2 pr-3">{{ t('admin.systemPrompts.history.status') }}</th><th class="py-2 text-right">{{ t('admin.systemPrompts.history.actions') }}</th></tr></thead>
                  <tbody class="divide-y divide-gray-100 dark:divide-dark-800">
                    <tr v-for="item in skillRegistry.versions" :key="item.id">
                      <td class="py-2 pr-3 font-mono">{{ shortHash(item.source_commit) }}</td>
                      <td class="py-2 pr-3">{{ item.id === skillRegistry.runtime.active?.id ? t('admin.systemPrompts.history.active') : t('admin.systemPrompts.history.candidate') }}</td>
                      <td class="py-2 text-right"><button type="button" class="btn btn-secondary btn-sm" :disabled="publishingSkill || item.id === skillRegistry.runtime.active?.id" @click="emit('publish-skill', item.id, true)"><Icon name="refresh" size="xs" /></button></td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </div>
          </section>

          <section class="px-4 py-4 sm:px-5">
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
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import Toggle from '@/components/common/Toggle.vue'
import type {
  ManagedSourceSyncStatus,
  ManagedSourceSyncVersion,
  RemoteSkillBundleVersionDetail,
  RemoteSkillClientInstaller,
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
  sourceVersion: SystemPromptVersion | null
  sourceSyncStatus: ManagedSourceSyncStatus | null
  sourceCandidate: ManagedSourceSyncVersion | null
  sourceSyncing: boolean
}>()

const emit = defineEmits<{
  (event: 'close'): void
  (event: 'runtime-change', value: Partial<typeof props.runtimeDraft>): void
  (event: 'save-runtime'): void
  (event: 'sync-skill'): void
  (event: 'publish-skill', id: number, rollback: boolean): void
  (event: 'copy-install', command: string, stage: 'acquire' | 'execute'): void
  (event: 'sync-source'): void
}>()

const { t } = useI18n()
const installPlatform = ref<'powershell' | 'python'>('powershell')
const selectedInstaller = computed<RemoteSkillClientInstaller | undefined>(() => props.skillRegistry?.client_install?.[installPlatform.value])

function shortHash(value: string) {
  return value ? `${value.slice(0, 8)}…${value.slice(-6)}` : '—'
}

function handleEscape(event: KeyboardEvent) {
  if (event.key === 'Escape' && props.open) emit('close')
}

onMounted(() => document.addEventListener('keydown', handleEscape))
onUnmounted(() => document.removeEventListener('keydown', handleEscape))
</script>
