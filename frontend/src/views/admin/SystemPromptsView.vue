<template>
  <AppLayout>
    <div class="mx-auto max-w-[1500px] space-y-4 px-1">
      <header class="flex flex-wrap items-center justify-between gap-3 border-b border-line pb-4">
        <div><h1 class="text-xl font-semibold">{{ t('admin.systemPrompts.title') }}</h1><p v-if="baseline" class="mt-1 text-xs text-muted">{{ text('revision') }} {{ baseline.revision }}<span v-if="dirty"> · {{ text('unsaved') }}</span></p></div>
        <div class="flex flex-wrap items-center gap-3">
          <label v-if="draft" class="flex items-center gap-2 text-sm" data-test="system-prompt-global-toggle"><input v-model="draft.enabled" type="checkbox" />{{ t('admin.systemPrompts.runtime.enabled') }}</label>
          <button type="button" class="btn btn-secondary btn-sm" data-test="system-prompt-refresh" :disabled="loading || saving" @click="load">{{ text('reload') }}</button>
          <button type="button" class="btn btn-secondary btn-sm" data-test="open-prompt-advanced" :disabled="!draft" @click="openAdvanced">{{ text('advanced') }}</button>
          <button type="button" class="btn btn-primary btn-sm" data-test="save-rules" :disabled="!dirty || saving || loading || invalid || !!pendingRemote" @click="save">{{ saving ? t('admin.systemPrompts.common.saving') : text('save') }}</button>
        </div>
      </header>
      <p v-if="error" role="alert" class="border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-900 dark:bg-red-900/20">{{ error }}</p>
      <section v-if="pendingRemote" data-test="system-prompt-conflict" class="space-y-3 border border-amber-300 bg-amber-50 p-3 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-900/20">
        <p>{{ text('conflict') }}</p><p>{{ text('conflictMergeHint') }}</p>
        <details><summary class="cursor-pointer">{{ text('serverChanges') }} · {{ text('revision') }} {{ pendingRemote.revision }}</summary><div class="mt-2 max-h-80 space-y-3 overflow-auto"><article v-for="rule in pendingRemote.policy.rules" :key="rule.id" class="space-y-1 border-t border-amber-200 pt-2"><strong>{{ rule.name }}</strong><p class="text-xs">{{ text(rule.role) }} · {{ text(rule.position) }} · {{ rule.platforms.join(', ') }} · {{ rule.enabled ? text('enabled') : text('rule_disabled') }}<span v-if="pendingRemote.policy.default_rule_ids.includes(rule.id)"> · {{ text('default') }}</span></p><p v-if="rule.models.length" class="text-xs">{{ rule.models.join(', ') }}</p><pre class="max-h-36 overflow-auto whitespace-pre-wrap text-xs">{{ pendingRemote.contents[rule.id]?.body }}</pre></article></div></details>
        <button type="button" class="btn btn-secondary btn-sm" data-test="keep-prompt-draft" @click="keepDraft">{{ text('keepDraft') }}</button>
      </section>
      <p v-if="invalid && draft" role="status" class="text-sm text-amber-700">{{ text('invalid') }}</p>
      <p v-if="loading && !draft" class="p-12 text-center text-sm text-muted">{{ t('admin.systemPrompts.common.loading') }}</p>
      <PromptRulesManager v-if="draft" v-model="draft" :selected-id="selectedID" :dirty-ids="dirtyIDs" :content-edits="contentEdits" :invalid="invalid" :invalid-reason="selectedInvalidReason" :busy="saving" @select="selectedID = $event" @add="add()" @remove="remove" @duplicate="duplicatePrompt" @advanced="openAdvanced" />
    </div>
    <SystemPromptAdvancedDrawer
      :open="advancedOpen" :runtime="baseline ? { revision: baseline.revision } : null"
      :runtime-draft="draft || { enabled: false, expose_server_prompt: false, compact_enabled: false }"
      :runtime-dirty="dirty && !invalid && !pendingRemote && !loading" :saving-runtime="saving" :skill-registry="skillRegistry" :skill-loading="skillLoading"
      :skill-sync-job="skillSyncJob" :skill-candidate="skillCandidate" :skill-syncing="skillSyncing" :publishing-skill="publishingSkill"
      :skill-target-rule-names="skillTargets.map(rule => rule.name)" :skill-publish-blocked="dirty || !!pendingRemote"
      :source-template="detail?.template || null" :source-template-display-name="selectedRule?.name || ''" :source-version="selectedVersion"
      :source-sync-status="sourceSyncStatus" :source-candidate="sourceCandidate" :source-syncing="sourceSyncing"
      @close="advancedOpen = false" @runtime-change="updateRuntimeDraft" @save-runtime="save" @sync-skill="startSkillSync" @publish-skill="publishSkillBundle" @sync-source="syncManagedSource"
    >
      <template #history>
        <section class="space-y-3 border-b border-line px-4 py-4 sm:px-5" data-test="prompt-history">
          <h3 class="text-sm font-semibold">{{ text('history') }}</h3>
          <p class="text-xs text-muted">{{ text('historyHint') }}</p>
          <div v-if="skillTargets.length || templates.some(template => template.managed_source === 'remote_skill_registry')" class="space-y-2 border border-line p-3"><p class="text-xs text-muted">{{ text('subscribeSkillHint') }}</p><button type="button" class="btn btn-secondary btn-sm" data-test="subscribe-current-skill" :disabled="subscribingSkill || !skillSubscriptionAvailable" @click="subscribeCurrentSkill">{{ text('subscribeCurrentSkill') }}</button></div>
          <label class="block text-sm">{{ text('template') }}<select :value="detail?.template.id || 0" class="input mt-1 w-full" data-test="history-template" @change="loadDetail(Number(($event.target as HTMLSelectElement).value))"><option :value="0">—</option><option v-for="template in templates" :key="template.id" :value="template.id">{{ template.name }}</option></select></label>
          <p v-if="historyLoading" class="text-xs text-muted">{{ t('admin.systemPrompts.common.loading') }}</p>
          <div v-for="version in detail?.versions || []" :key="version.id" class="space-y-2 border-t border-line pt-3 text-xs">
            <div class="flex flex-wrap items-center gap-2"><strong>v{{ version.version }}</strong><span>{{ formatDate(version.created_at) }}</span><span v-if="selectedRule?.version_id === version.id" class="badge badge-info">{{ text('referenced') }}</span></div>
            <details><summary class="cursor-pointer text-muted">{{ text('versionDetails') }}</summary><pre class="mt-2 max-h-44 overflow-auto whitespace-pre-wrap">{{ version.body }}</pre><dl class="mt-2 space-y-1 break-all"><div>SHA-256: {{ version.sha256 }}</div><div v-if="version.source_commit">{{ version.source_repository }} · {{ version.source_commit }}</div><div v-if="version.note">{{ version.note }}</div></dl></details>
            <div class="flex flex-wrap gap-2"><button type="button" class="btn btn-secondary btn-sm" :disabled="!selectedRule || selectedRule.version_id === version.id || selectedBodyDirty || version.composition_mode === 'codex_skill_hybrid' || detail?.template.managed_source === 'remote_skill_registry'" :data-test="`restore-version-${version.id}`" @click="restoreVersion(version)">{{ text('applyVersion') }}</button><button type="button" class="btn btn-secondary btn-sm" @click="add(`${detail?.template.name || ''} (${text('copy')})`, version.body)">{{ text('copyIndependent') }}</button></div>
          </div>
          <p v-if="selectedBodyDirty" class="text-xs text-amber-700">{{ text('historyDraftHint') }}</p>
          <p v-if="detail?.template.managed_source === 'remote_skill_registry'" class="text-xs text-muted">{{ text('skillHistoryHint') }}</p>
        </section>
      </template>
    </SystemPromptAdvancedDrawer>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import PromptRulesManager from '@/components/admin/systemPrompt/PromptRulesManager.vue'
import SystemPromptAdvancedDrawer from '@/components/admin/systemPrompt/SystemPromptAdvancedDrawer.vue'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { extractApiErrorCode, extractApiErrorMessage } from '@/utils/apiError'
import { useSystemPromptConfigDraft } from '@/composables/useSystemPromptConfigDraft'
import { legalPromptModelScope, legalPromptPosition, rulesAPI, type PromptRule } from '@/api/admin/systemPromptRules'
import systemPromptsAPI, { type SystemPromptTemplate, type SystemPromptDetailResponse, type SystemPromptVersion, type RemoteSkillRegistryResponse, type RemoteSkillBundleVersionDetail, type RemoteSkillSyncJob, type ManagedSourceSyncStatus, type ManagedSourceSyncVersion } from '@/api/admin/systemPrompts'

const { t, locale } = useI18n()
const text = (key: string) => t(`admin.systemPrompts.rules.${key}`)
const notifications = useAppStore()
const auth = useAuthStore()
const { baseline, draft, dirty, pendingRemote, selectedID, selectedRule, selectedContent, contentEdits, ruleDirty, receive, keepDraft, saved, request, add, remove, useVersion } = useSystemPromptConfigDraft(`system-prompts-v2:${auth.user?.id || 'admin'}`)
const loading = ref(false), saving = ref(false), error = ref(''), advancedOpen = ref(false)
const templates = ref<SystemPromptTemplate[]>([]), detail = ref<SystemPromptDetailResponse | null>(null), historyLoading = ref(false)
const skillRegistry = ref<RemoteSkillRegistryResponse | null>(null), skillLoading = ref(false), skillCandidate = ref<RemoteSkillBundleVersionDetail | null>(null), skillSyncJob = ref<RemoteSkillSyncJob | null>(null), startingSkillSync = ref(false), publishingSkill = ref(false), subscribingSkill = ref(false)
const sourceSyncStatus = ref<ManagedSourceSyncStatus | null>(null), sourceCandidate = ref<ManagedSourceSyncVersion | null>(null), sourceSyncing = ref(false)
let disposed = false, loadGeneration = 0, detailGeneration = 0
let skillTimer: ReturnType<typeof setTimeout> | undefined
const dirtyIDs = computed(() => (draft.value?.policy.rules || []).filter(rule => ruleDirty(rule.id)).map(rule => rule.id))
const selectedBodyDirty = computed(() => !!selectedID.value && !!contentEdits.value[selectedID.value])
const selectedVersion = computed(() => detail.value?.versions.find(version => version.id === selectedRule.value?.version_id) || null)
const skillSyncing = computed(() => startingSkillSync.value || skillSyncJob.value?.status === 'queued' || skillSyncJob.value?.status === 'running')
const skillTargets = computed(() => (baseline.value?.policy.rules || []).filter(rule => baseline.value?.contents[rule.id]?.composition_mode === 'codex_skill_hybrid'))
const skillSubscriptionAvailable = computed(() => !!skillRegistry.value?.runtime.active && (!skillTargets.value[0] || baseline.value?.contents[skillTargets.value[0].id]?.available !== false))
function invalidReason(rule: PromptRule) {
  const content = draft.value?.contents[rule.id]
  if (!rule.name.trim() || !content || (!content.managed && (!content.body.trim() || content.body.includes('\u0000') || new TextEncoder().encode(content.body).length > 64 * 1024))) return 'invalidBody'
  if (!rule.platforms.length) return 'platformRequired'
  if (rule.role === 'system' && rule.platforms.some(platform => ['openai', 'cindy'].includes(platform)) && rule.account_types?.some(type => ['oauth', 'setup-token'].includes(type))) return 'incompatible'
  const capabilities = draft.value?.capabilities || []
  if (!legalPromptPosition(rule.role, rule.position, rule.platforms, capabilities, rule.account_types)) return 'incompatible'
  if (!legalPromptModelScope(rule, capabilities)) return 'conversationModelRequired'
  return ''
}
const invalid = computed(() => draft.value?.policy.rules.some(rule => !!invalidReason(rule)) || false)
const selectedInvalidReason = computed(() => selectedRule.value ? invalidReason(selectedRule.value) : '')
function fail(value: unknown) {
  error.value = extractApiErrorCode(value) === 'system_prompt_revision_conflict' ? text('conflict') : extractApiErrorMessage(value) || text('error')
}
async function load() {
  if (loading.value || saving.value) return
  const generation = ++loadGeneration
  loading.value = true; error.value = ''
  try { const next = await rulesAPI.config(); if (!disposed && generation === loadGeneration) receive(next) }
  catch (value) { if (!disposed && generation === loadGeneration) fail(value) }
  finally { if (!disposed && generation === loadGeneration) loading.value = false }
}
async function save() {
  if (!draft.value || saving.value || loading.value || invalid.value) return
  const payload = request()
  if (!payload) return
  const sent = JSON.parse(JSON.stringify(draft.value))
  saving.value = true; error.value = ''
  try {
    const next = await rulesAPI.saveConfig(payload)
    if (disposed) return
    saved(sent, next)
    notifications.showSuccess(text('saved'))
    if (advancedOpen.value) void loadAdvanced()
  } catch (value) {
    if (disposed) return
    fail(value)
    if (extractApiErrorCode(value) === 'system_prompt_revision_conflict') {
      try { const next = await rulesAPI.config(); if (!disposed) receive(next) } catch { /* The draft and its original revision remain intact. */ }
    }
  } finally { if (!disposed) saving.value = false }
}
function duplicatePrompt() {
  if (selectedRule.value && selectedContent.value && selectedContent.value.available !== false) add(`${selectedRule.value.name} (${text('copy')})`, selectedContent.value.body)
}
function updateRuntimeDraft(value: Partial<{ enabled: boolean; expose_server_prompt: boolean; compact_enabled: boolean }>) {
  if (draft.value) Object.assign(draft.value, value)
}
function formatDate(value: string) {
  if (!value) return '—'
  try { return new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value)) } catch { return value }
}
async function loadDetail(id: number) {
  const generation = ++detailGeneration
  detail.value = null
  if (!id) return
  historyLoading.value = true
  try { const next = await systemPromptsAPI.get(id); if (!disposed && generation === detailGeneration) detail.value = next }
  catch (value) { if (!disposed && generation === detailGeneration) fail(value) }
  finally { if (!disposed && generation === detailGeneration) historyLoading.value = false }
}
async function loadAdvanced() {
  const templateID = selectedRule.value?.template_id || 0
  const results = await Promise.allSettled([systemPromptsAPI.list(), loadDetail(templateID)])
  if (disposed) return
  if (results[0].status === 'fulfilled') templates.value = results[0].value.templates
  else fail(results[0].reason)
}
async function openAdvanced() {
  advancedOpen.value = true
  await Promise.allSettled([loadAdvanced(), loadSkillRegistry()])
}
function restoreVersion(version: SystemPromptVersion) {
  if (!selectedRule.value || !detail.value || selectedBodyDirty.value || version.composition_mode === 'codex_skill_hybrid' || detail.value.template.managed_source === 'remote_skill_registry') return
  useVersion(selectedRule.value, { body: version.body, composition_mode: version.composition_mode, managed: !!detail.value.template.managed_source || version.composition_mode !== 'inline', template_id: version.template_id, version_id: version.id })
  advancedOpen.value = false
}
async function subscribeCurrentSkill() {
  if (!baseline.value || !skillRegistry.value?.runtime.active || !skillSubscriptionAvailable.value || subscribingSkill.value) return
  subscribingSkill.value = true
  try {
    const existing = skillTargets.value[0]
    let content = existing ? baseline.value.contents[existing.id] : undefined
    if (!content) {
      const source = templates.value.find(template => template.managed_source === 'remote_skill_registry')
      if (!source) return
      const activeID = skillRegistry.value.runtime.active.id
      const [sourceDetail, active] = await Promise.all([systemPromptsAPI.get(source.id), systemPromptsAPI.getSkillVersion(activeID)])
      const version = sourceDetail.versions.filter(version => version.composition_mode === 'codex_skill_hybrid').sort((left, right) => right.version - left.version)[0]
      if (!version || active.id !== activeID || typeof active.prompt?.effective_body !== 'string') throw new Error(t('admin.systemPrompts.editor.managedBodyUnavailable'))
      content = { body: active.prompt.effective_body, composition_mode: 'codex_skill_hybrid', managed: true, template_id: source.id, version_id: version.id }
    }
    if (disposed) return
    add(text('skillPromptName'))
    if (selectedRule.value) useVersion(selectedRule.value, content)
    advancedOpen.value = false
  } catch (value) { if (!disposed) fail(value) }
  finally { if (!disposed) subscribingSkill.value = false }
}
async function loadSkillRegistry() {
  if (skillLoading.value) return
  skillLoading.value = true
  try { const next = await systemPromptsAPI.getSkillRegistry(); if (!disposed) skillRegistry.value = next }
  catch (value) { if (!disposed) fail(value) }
  finally { if (!disposed) skillLoading.value = false }
}
async function pollSkillSync() {
  if (disposed || !skillSyncJob.value) return
  try {
    const job = await systemPromptsAPI.getSkillSync(skillSyncJob.value.id)
    if (disposed) return
    skillSyncJob.value = job
    if (job.status === 'queued' || job.status === 'running') skillTimer = setTimeout(() => void pollSkillSync(), 1200)
    else if (job.status === 'succeeded' && job.candidate_bundle_version_id) {
      const [candidate, registry] = await Promise.all([systemPromptsAPI.getSkillVersion(job.candidate_bundle_version_id), systemPromptsAPI.getSkillRegistry()])
      if (!disposed) { skillCandidate.value = candidate; skillRegistry.value = registry }
    } else if (job.status === 'failed') error.value = t('admin.systemPrompts.errors.skillSync')
  } catch (value) { if (!disposed) fail(value) }
}
async function startSkillSync(promptCapture?: File) {
  if (!skillRegistry.value || skillSyncing.value) return
  startingSkillSync.value = true
  try {
    const job = await systemPromptsAPI.startSkillSync(skillRegistry.value.runtime.revision, promptCapture)
    if (disposed) return
    skillSyncJob.value = job; skillCandidate.value = null
    skillTimer = setTimeout(() => void pollSkillSync(), 1200)
  } catch (value) { if (!disposed) fail(value) }
  finally { if (!disposed) startingSkillSync.value = false }
}
async function publishSkillBundle(id: number, rollback: boolean) {
  if (!skillRegistry.value || !baseline.value || dirty.value || pendingRemote.value || publishingSkill.value) return
  publishingSkill.value = true
  try {
    await systemPromptsAPI.publishSkillVersion(id, skillRegistry.value.runtime.revision, rollback, { target_rule_ids: skillTargets.value.map(rule => rule.id), expected_config_revision: baseline.value.revision })
    if (disposed) return
    skillCandidate.value = null
    await Promise.all([load(), loadSkillRegistry()])
    notifications.showSuccess(t(rollback ? 'admin.systemPrompts.messages.skillRolledBack' : 'admin.systemPrompts.messages.skillPublished'))
  } catch (value) { if (!disposed) fail(value) }
  finally { if (!disposed) publishingSkill.value = false }
}
async function syncManagedSource() {
  if (!detail.value || !baseline.value || !detail.value.template.managed_source || sourceSyncing.value) return
  const templateID = detail.value.template.id
  sourceSyncing.value = true
  try {
    const next = await systemPromptsAPI.syncManagedSource(templateID, { expected_latest_version: detail.value.versions[0]?.version || 0, expected_revision: baseline.value.revision })
    if (disposed) return
    sourceSyncStatus.value = next.status; sourceCandidate.value = next.version || null
    await Promise.all([load(), loadDetail(templateID)])
  } catch (value) { if (!disposed) fail(value) }
  finally { if (!disposed) sourceSyncing.value = false }
}
watch(selectedID, () => {
  sourceSyncStatus.value = null; sourceCandidate.value = null
  if (advancedOpen.value) void loadDetail(selectedRule.value?.template_id || 0)
})
onMounted(() => void load())
onBeforeUnmount(() => { disposed = true; loadGeneration++; detailGeneration++; if (skillTimer) clearTimeout(skillTimer) })
</script>
