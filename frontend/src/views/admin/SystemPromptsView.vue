<template>
  <AppLayout>
    <div class="mx-auto max-w-5xl space-y-5 px-1">
      <header class="flex flex-wrap items-start justify-between gap-3 border-b border-line pb-4">
        <div class="space-y-1">
          <h1 class="text-xl font-semibold">{{ t('admin.systemPrompts.title') }}</h1>
          <p class="text-sm text-muted">{{ t('admin.systemPrompts.description') }}</p>
        </div>
        <div class="flex items-center gap-2">
          <button type="button" class="btn btn-secondary btn-sm" data-test="system-prompts-reload" :disabled="loading || saving" @click="load">{{ t('admin.systemPrompts.reload') }}</button>
          <button type="button" class="btn btn-primary btn-sm" data-test="system-prompts-save" :disabled="!draft || !dirty || !valid || saving || loading" @click="save">{{ t('admin.systemPrompts.save') }}</button>
        </div>
      </header>

      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      <p v-if="loading && !draft" class="py-10 text-center text-sm text-muted">{{ t('common.loading') }}</p>

      <template v-if="draft && state">
        <section class="space-y-4 border-b border-line pb-5">
          <label class="flex items-center gap-3">
            <Toggle v-model="draft.enabled" data-test="system-prompts-enabled" />
            <span class="text-sm font-medium">{{ t('admin.systemPrompts.enabled') }}</span>
            <span class="text-xs text-muted">{{ t('admin.systemPrompts.enabledHint') }}</span>
          </label>
          <label class="block max-w-md space-y-1">
            <span class="block text-sm font-medium">{{ t('admin.systemPrompts.defaultPrompt') }}</span>
            <select v-model="draft.default_prompt_id" class="input" data-test="system-prompts-default">
              <option value="">{{ t('admin.systemPrompts.noDefault') }}</option>
              <option v-for="prompt in draft.prompts" :key="prompt.id" :value="prompt.id">{{ prompt.name || prompt.id }}</option>
            </select>
            <span class="block text-xs text-muted">{{ t('admin.systemPrompts.defaultHint') }}</span>
          </label>
          <p class="text-sm" data-test="system-prompts-usage">{{ t('admin.systemPrompts.usage', { inherit: state.usage.inherit, off: state.usage.off, custom: customTotal }) }}</p>
          <p class="text-xs text-muted">{{ t('admin.systemPrompts.notes') }}</p>
        </section>

        <section class="space-y-4">
          <div class="flex items-center justify-between">
            <h2 class="text-base font-semibold">{{ t('admin.systemPrompts.library') }}</h2>
            <button type="button" class="btn btn-secondary btn-sm" data-test="system-prompts-add" :disabled="draft.prompts.length >= 50" @click="add">{{ t('admin.systemPrompts.add') }}</button>
          </div>
          <p v-if="!draft.prompts.length" class="text-sm text-muted">{{ t('admin.systemPrompts.empty') }}</p>
          <p v-if="!valid" role="status" class="text-sm text-amber-700">{{ t('admin.systemPrompts.invalid') }}</p>
          <article v-for="(prompt, index) in draft.prompts" :key="keys[index]" class="space-y-3 border-t border-line pt-4" :data-test="`system-prompt-${index}`">
            <div class="flex flex-wrap items-end gap-3">
              <label class="min-w-[12rem] flex-1 space-y-1">
                <span class="block text-sm">{{ t('admin.systemPrompts.name') }}</span>
                <input v-model="prompt.name" class="input" maxlength="100" />
              </label>
              <label class="space-y-1">
                <span class="block text-sm">{{ t('admin.systemPrompts.position') }}</span>
                <select v-model="prompt.position" class="input">
                  <option value="prepend">{{ t('admin.systemPrompts.prepend') }}</option>
                  <option value="append">{{ t('admin.systemPrompts.append') }}</option>
                </select>
              </label>
              <label class="space-y-1">
                <span class="block text-sm">{{ t('admin.systemPrompts.role') }}</span>
                <select v-model="prompt.role" class="input">
                  <option value="auto">{{ t('admin.systemPrompts.roleAuto') }}</option>
                  <option value="system">{{ t('admin.systemPrompts.roleSystem') }}</option>
                  <option value="developer">{{ t('admin.systemPrompts.roleDeveloper') }}</option>
                </select>
              </label>
              <button type="button" class="btn btn-danger btn-sm" :disabled="usedBy(prompt) > 0" @click="remove(index)">{{ t('admin.systemPrompts.remove') }}</button>
            </div>
            <label class="block space-y-1">
              <span class="block text-sm">{{ t('admin.systemPrompts.body') }}</span>
              <textarea v-model="prompt.body" class="input font-mono text-xs" rows="8" spellcheck="false" />
            </label>
            <p class="flex flex-wrap gap-3 text-xs text-muted">
              <span :class="{ 'text-red-600': bodyBytes(prompt.body) > systemPromptMaxBodyBytes }">{{ t('admin.systemPrompts.bytes', { used: bodyBytes(prompt.body), max: systemPromptMaxBodyBytes }) }}</span>
              <span v-if="usedBy(prompt) > 0">{{ t('admin.systemPrompts.usedBy', { count: usedBy(prompt) }) }}</span>
            </p>
          </article>
        </section>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Toggle from '@/components/common/Toggle.vue'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import {
  systemPromptMaxBodyBytes,
  systemPromptsAPI,
  type SystemPrompt,
  type SystemPromptConfig,
  type SystemPromptState
} from '@/api/admin/systemPrompts'

const { t } = useI18n()
const appStore = useAppStore()
const state = ref<SystemPromptState | null>(null)
const draft = ref<SystemPromptConfig | null>(null)
const keys = ref<number[]>([])
const loading = ref(false)
const saving = ref(false)
const error = ref('')
let nextKey = 0

const encoder = new TextEncoder()
const bodyBytes = (body: string) => encoder.encode(body).length
const usedBy = (prompt: SystemPrompt) => (prompt.id && state.value?.usage.custom[prompt.id]) || 0
const customTotal = computed(() => Object.values(state.value?.usage.custom || {}).reduce((sum, count) => sum + count, 0))
const snapshot = (config: SystemPromptConfig) => JSON.stringify(config)
const dirty = computed(() => !!draft.value && !!state.value && snapshot(draft.value) !== snapshot(toConfig(state.value)))
const valid = computed(() => !!draft.value && draft.value.prompts.every(prompt =>
  prompt.name.trim() !== '' && prompt.body.trim() !== '' && bodyBytes(prompt.body) <= systemPromptMaxBodyBytes))

function toConfig(value: SystemPromptConfig): SystemPromptConfig {
  return {
    enabled: value.enabled,
    default_prompt_id: value.default_prompt_id || '',
    prompts: (value.prompts || []).map(prompt => ({ ...prompt }))
  }
}

function receive(value: SystemPromptState) {
  state.value = value
  draft.value = toConfig(value)
  keys.value = draft.value.prompts.map(() => nextKey++)
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    receive(await systemPromptsAPI.get())
  } catch (value) {
    error.value = extractApiErrorMessage(value) || t('admin.systemPrompts.loadFailed')
  } finally {
    loading.value = false
  }
}

async function save() {
  if (!draft.value || !valid.value || saving.value) return
  saving.value = true
  error.value = ''
  try {
    receive(await systemPromptsAPI.save(draft.value))
    appStore.showSuccess(t('admin.systemPrompts.saved'))
  } catch (value) {
    error.value = extractApiErrorMessage(value) || t('admin.systemPrompts.loadFailed')
  } finally {
    saving.value = false
  }
}

function newPromptID() {
  const bytes = crypto.getRandomValues(new Uint8Array(6))
  return `p-${Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('')}`
}

function add() {
  if (!draft.value) return
  draft.value.prompts.push({ id: newPromptID(), name: '', body: '', position: 'append', role: 'auto' })
  keys.value.push(nextKey++)
}

function remove(index: number) {
  if (!draft.value) return
  const [removed] = draft.value.prompts.splice(index, 1)
  keys.value.splice(index, 1)
  if (removed?.id && draft.value.default_prompt_id === removed.id) draft.value.default_prompt_id = ''
}

onMounted(load)
</script>
