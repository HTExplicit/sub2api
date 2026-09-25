<template>
  <AppLayout>
    <div class="mx-auto max-w-[1500px] space-y-4 px-1">
      <header class="flex flex-wrap items-center justify-between gap-3 border-b border-line pb-4">
        <div><h1 class="text-xl font-semibold">{{ t('admin.systemPrompts.title') }}</h1><p v-if="baseline" class="mt-1 text-xs text-muted">{{ text('revision') }} {{ baseline.revision }}<span v-if="dirty"> · {{ text('unsaved') }}</span></p></div>
        <div class="flex flex-wrap items-center gap-3">
          <label v-if="draft" class="flex items-center gap-2 text-sm" data-test="system-prompt-global-toggle"><input v-model="draft.enabled" type="checkbox" />{{ t('admin.systemPrompts.runtime.enabled') }}</label>
          <button type="button" class="btn btn-secondary btn-sm" data-test="system-prompt-refresh" :disabled="loading || saving" @click="load">{{ text('reload') }}</button>
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
      <PromptRulesManager v-if="draft" v-model="draft" :selected-id="selectedID" :dirty-ids="dirtyIDs" :invalid-reason="selectedInvalidReason" @select="selectedID = $event" @add="add()" @remove="remove" @duplicate="duplicatePrompt" @history="openHistory" />
    </div>
    <section v-if="historyOpen" class="mx-auto mt-4 max-w-[1500px] space-y-3 border border-line p-4" data-test="prompt-history">
      <div class="flex items-center justify-between"><h2 class="text-sm font-semibold">{{ selectedRule?.name }} · {{ text('history') }}</h2><button type="button" class="btn btn-secondary btn-sm" @click="historyOpen = false">{{ t('admin.systemPrompts.common.close') }}</button></div>
      <p class="text-xs text-muted">{{ text('historyHint') }}</p>
      <p v-if="historyLoading" class="text-xs text-muted">{{ t('admin.systemPrompts.common.loading') }}</p>
      <article v-for="version in history" :key="version.id" class="space-y-2 border-t border-line pt-3 text-xs">
        <p>{{ formatDate(version.created_at) }}<span v-if="selectedRule?.version_id === version.id" class="ml-2 badge badge-info">{{ text('referenced') }}</span></p>
        <pre class="max-h-60 overflow-auto whitespace-pre-wrap">{{ historyBody(version) }}</pre>
        <button v-if="version.restorable" type="button" class="btn btn-secondary btn-sm" :disabled="selectedBodyDirty || saving || selectedRule?.version_id === version.id" :data-test="`restore-version-${version.id}`" @click="restoreVersion(version)">{{ text('applyVersion') }}</button>
        <p v-else class="text-muted">{{ text('archivedHistory') }}</p>
      </article>
      <p v-if="selectedBodyDirty" class="text-xs text-amber-700">{{ text('historyDraftHint') }}</p>
    </section>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import PromptRulesManager from '@/components/admin/systemPrompt/PromptRulesManager.vue'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { extractApiErrorCode, extractApiErrorMessage } from '@/utils/apiError'
import { useSystemPromptConfigDraft } from '@/composables/useSystemPromptConfigDraft'
import { legalPromptModelScope, legalPromptPosition, rulesAPI, type PromptRule, type PromptHistoryVersion } from '@/api/admin/systemPromptRules'

const { t, locale } = useI18n()
const text = (key: string) => t(`admin.systemPrompts.rules.${key}`)
const notifications = useAppStore()
const auth = useAuthStore()
const sessionKey = () => `system-prompts-v2:${auth.user?.id || 'admin'}`
const { baseline, draft, dirty, pendingRemote, selectedID, selectedRule, selectedContent, contentEdits, ruleDirty, receive, keepDraft, saved, request, add, remove, restore, switchSession } = useSystemPromptConfigDraft(sessionKey())
const loading = ref(false), saving = ref(false), error = ref(''), historyOpen = ref(false), historyLoading = ref(false)
const history = ref<PromptHistoryVersion[]>([])
let disposed = false, loadGeneration = 0, historyGeneration = 0, sessionGeneration = 0
const dirtyIDs = computed(() => (draft.value?.policy.rules || []).filter(rule => ruleDirty(rule.id)).map(rule => rule.id))
const selectedBodyDirty = computed(() => !!selectedID.value && !!contentEdits.value[selectedID.value])
function invalidReason(rule: PromptRule) {
  const content = draft.value?.contents[rule.id]
  if (!rule.name.trim() || !content || !content.body.trim() || content.body.includes('\u0000') || new TextEncoder().encode(content.body).length > 64 * 1024) return 'invalidBody'
  if (!rule.platforms.length) return 'platformRequired'
  if (rule.role === 'system' && rule.platforms.some(platform => ['openai', 'cindy'].includes(platform)) && rule.account_types?.some(type => ['oauth', 'setup-token'].includes(type))) return 'incompatible'
  const capabilities = draft.value?.capabilities || []
  if (!legalPromptPosition(rule.role, rule.position, rule.platforms, capabilities, rule.account_types)) return 'incompatible'
  if (!legalPromptModelScope(rule, capabilities)) return 'conversationModelRequired'
  return ''
}
const invalid = computed(() => draft.value?.policy.rules.some(rule => !!invalidReason(rule)) || false)
const selectedInvalidReason = computed(() => selectedRule.value ? invalidReason(selectedRule.value) : '')
function fail(value: unknown) { error.value = extractApiErrorCode(value) === 'system_prompt_revision_conflict' ? text('conflict') : extractApiErrorMessage(value) || text('error') }
async function load() {
  if (loading.value || saving.value) return
  const generation = ++loadGeneration, session = sessionGeneration
  loading.value = true; error.value = ''
  try { const next = await rulesAPI.config(); if (!disposed && generation === loadGeneration && session === sessionGeneration) receive(next) }
  catch (value) { if (!disposed && generation === loadGeneration && session === sessionGeneration) fail(value) }
  finally { if (!disposed && generation === loadGeneration && session === sessionGeneration) loading.value = false }
}
async function save() {
  if (!draft.value || saving.value || loading.value || invalid.value) return
  const payload = request()
  if (!payload) return
  const sent = JSON.parse(JSON.stringify(draft.value)), session = sessionGeneration
  saving.value = true; error.value = ''
  try {
    const next = await rulesAPI.saveConfig(payload)
    if (disposed || session !== sessionGeneration) return
    saved(sent, next); notifications.showSuccess(text('saved'))
    if (historyOpen.value) void loadHistory()
  } catch (value) {
    if (disposed || session !== sessionGeneration) return
    fail(value)
    if (extractApiErrorCode(value) === 'system_prompt_revision_conflict') {
      try { const next = await rulesAPI.config(); if (!disposed && session === sessionGeneration) receive(next) } catch { /* Keep the unsaved draft and its revision. */ }
    }
  } finally { if (!disposed && session === sessionGeneration) saving.value = false }
}
function duplicatePrompt() {
  if (!selectedRule.value || !selectedContent.value || selectedContent.value.composition_mode !== 'inline') return
  add(`${selectedRule.value.name} (${text('copy')})`, selectedContent.value.body)
}
function formatDate(value: string) {
  try { return new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value)) } catch { return value }
}
function historyBody(version: PromptHistoryVersion) {
  if (version.composition_mode !== 'anthropic_system_blocks') return version.body
  try { const parsed = JSON.parse(version.body); return [...(Array.isArray(parsed) ? parsed : parsed.blocks || []).map((block: { text?: string } | null) => block?.text || ''), parsed.expansion_prompt || ''].join('\n\n') } catch { return version.body }
}
async function loadHistory() {
  const id = selectedID.value, generation = ++historyGeneration, session = sessionGeneration
  history.value = []; historyLoading.value = false
  if (!selectedRule.value?.template_id || !id) return
  historyLoading.value = true
  try { const next = await rulesAPI.history(id); if (!disposed && generation === historyGeneration && session === sessionGeneration && id === selectedID.value) history.value = next }
  catch (value) { if (!disposed && generation === historyGeneration && session === sessionGeneration) fail(value) }
  finally { if (!disposed && generation === historyGeneration && session === sessionGeneration) historyLoading.value = false }
}
function openHistory() { historyOpen.value = true; void loadHistory() }
function restoreVersion(version: PromptHistoryVersion) { if (selectedBodyDirty.value || saving.value || !version.restorable) return; restore(version); historyOpen.value = false }
watch(selectedID, () => { historyGeneration++; history.value = []; if (historyOpen.value) void loadHistory() })
watch(() => auth.user?.id, () => {
  sessionGeneration++; loadGeneration++; historyGeneration++
  loading.value = false; saving.value = false; historyOpen.value = false; history.value = []; error.value = ''
  switchSession(sessionKey()); void load()
}, { flush: 'sync' })
onMounted(() => void load())
onBeforeUnmount(() => { disposed = true; loadGeneration++; historyGeneration++; sessionGeneration++ })
</script>
