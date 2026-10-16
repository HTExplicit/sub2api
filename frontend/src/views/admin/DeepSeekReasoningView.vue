<template>
  <AppLayout>
    <div class="mx-auto max-w-3xl space-y-5 px-1" data-ui="deepseek-reasoning">
      <header class="flex flex-wrap items-start justify-between gap-3 border-b border-line pb-4">
        <div class="space-y-1">
          <h1 class="text-xl font-semibold">{{ t('admin.deepseekReasoning.title') }}</h1>
          <p class="text-sm text-muted">{{ t('admin.deepseekReasoning.description') }}</p>
        </div>
        <div class="flex items-center gap-2">
          <span v-if="dirty" role="status" class="text-xs text-muted">{{ t('admin.deepseekReasoning.unsaved') }}</span>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="loading || saving" @click="load">{{ t('common.refresh') }}</button>
          <button type="button" class="btn btn-primary btn-sm" data-test="save" :disabled="!dirty || loading || saving" @click="save">
            {{ saving ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </header>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      <p v-if="loading && !draft" class="py-10 text-center text-sm text-muted">{{ t('common.loading') }}</p>
      <section v-if="draft" class="space-y-5" :aria-busy="loading || saving">
        <label class="flex items-center gap-3">
          <Toggle v-model="draft.enabled" :disabled="loading || saving" :aria-label="t('admin.deepseekReasoning.enabled')" data-test="enabled" />
          <span class="text-sm font-medium">{{ t('admin.deepseekReasoning.enabled') }}</span>
        </label>
        <label class="block max-w-sm space-y-2">
          <span class="block text-sm font-medium">{{ t('admin.deepseekReasoning.effort') }}</span>
          <select v-model="draft.effort" class="input" data-test="effort" :disabled="loading || saving">
            <option value="low">Low</option>
            <option value="high">High</option>
            <option value="max">Max</option>
          </select>
        </label>
        <p class="text-sm text-muted">{{ t('admin.deepseekReasoning.rules') }}</p>
        <p class="text-sm text-muted">{{ t('admin.deepseekReasoning.scope') }}</p>
      </section>
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
import { deepseekReasoningAPI, type DeepSeekReasoningConfig } from '@/api/admin/deepseekReasoning'

const { t } = useI18n()
const appStore = useAppStore()
const state = ref<DeepSeekReasoningConfig | null>(null)
const draft = ref<DeepSeekReasoningConfig | null>(null)
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const dirty = computed(() => !!draft.value && !!state.value &&
  (draft.value.enabled !== state.value.enabled || draft.value.effort !== state.value.effort))

function receive(config: DeepSeekReasoningConfig) {
  state.value = { ...config }
  draft.value = { ...config }
}

async function load() {
  loading.value = true
  error.value = ''
  try { receive(await deepseekReasoningAPI.get()) }
  catch (value) { error.value = extractApiErrorMessage(value) || t('admin.deepseekReasoning.loadFailed') }
  finally { loading.value = false }
}

async function save() {
  if (!draft.value || !dirty.value || saving.value) return
  saving.value = true
  error.value = ''
  try {
    receive(await deepseekReasoningAPI.save({ ...draft.value }))
    appStore.showSuccess(t('admin.deepseekReasoning.saved'))
  } catch (value) { error.value = extractApiErrorMessage(value) || t('admin.deepseekReasoning.saveFailed') }
  finally { saving.value = false }
}

onMounted(load)
</script>
