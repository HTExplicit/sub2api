<template>
  <AppLayout>
    <div class="mx-auto max-w-5xl space-y-5 px-1" data-ui="reasoning-recovery">
      <header class="flex flex-wrap items-start justify-between gap-3 border-b border-line pb-4">
        <div class="space-y-1">
          <h1 class="text-xl font-semibold">{{ t('admin.reasoningRecovery.title') }}</h1>
          <p class="text-sm text-muted">{{ t('admin.reasoningRecovery.description') }}</p>
        </div>
        <div class="flex items-center gap-2">
          <button type="button" class="btn btn-secondary btn-sm" data-test="reasoning-recovery-reload" :disabled="loading || saving" @click="load">{{ t('admin.reasoningRecovery.reload') }}</button>
          <button type="button" class="btn btn-primary btn-sm" data-test="reasoning-recovery-save" :disabled="!draft || !dirty || saving || loading" @click="save">{{ t('admin.reasoningRecovery.save') }}</button>
        </div>
      </header>

      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      <p v-if="loading && !draft" class="py-10 text-center text-sm text-muted">{{ t('common.loading') }}</p>

      <section v-if="draft && state">
        <label class="flex items-center gap-3">
          <Toggle v-model="draft.enabled" data-test="reasoning-recovery-enabled" :disabled="saving" />
          <span class="text-sm font-medium">{{ t('admin.reasoningRecovery.enabled') }}</span>
        </label>
        <p class="mt-2 text-xs text-muted">{{ t('admin.reasoningRecovery.enabledHint') }}</p>
        <p class="mt-4 text-xs text-muted">{{ t('admin.reasoningRecovery.scope') }}</p>
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
import { reasoningRecoveryAPI, type ReasoningRecoveryConfig } from '@/api/admin/reasoningRecovery'

const { t } = useI18n()
const appStore = useAppStore()
const state = ref<ReasoningRecoveryConfig | null>(null)
const draft = ref<ReasoningRecoveryConfig | null>(null)
const loading = ref(false)
const saving = ref(false)
const error = ref('')

const dirty = computed(() => !!draft.value && !!state.value && draft.value.enabled !== state.value.enabled)

// The switch shows only what the server answered: an answer without a boolean fails the request, it is never a default.
function receive(value: ReasoningRecoveryConfig) {
  if (typeof value?.enabled !== 'boolean') throw new Error('')
  state.value = { enabled: value.enabled }
  draft.value = { enabled: value.enabled }
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    receive(await reasoningRecoveryAPI.get())
  } catch (value) {
    error.value = extractApiErrorMessage(value) || t('admin.reasoningRecovery.loadFailed')
  } finally {
    loading.value = false
  }
}

async function save() {
  if (!draft.value || !dirty.value || saving.value) return
  saving.value = true
  error.value = ''
  try {
    receive(await reasoningRecoveryAPI.save({ enabled: draft.value.enabled }))
    appStore.showSuccess(t('admin.reasoningRecovery.saved'))
  } catch (value) {
    error.value = extractApiErrorMessage(value) || t('admin.reasoningRecovery.saveFailed')
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>
