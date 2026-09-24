<template>
  <section class="space-y-3" aria-labelledby="observability-settings-title" data-testid="observability-settings">
    <div>
      <h3 id="observability-settings-title" class="text-base font-semibold text-gray-900 dark:text-white">
        {{ t('admin.settings.observability.title') }}
      </h3>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.settings.observability.description') }}</p>
    </div>
    <p v-if="loadError" class="text-sm text-red-600 dark:text-red-400">{{ t('admin.settings.observability.loadFailed') }}</p>
    <template v-else-if="form">
      <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
        <input v-model="form.telemetry_enabled" type="checkbox" data-testid="observability-telemetry" />
        {{ t('admin.settings.observability.telemetryEnabled') }}
      </label>
      <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
        <input v-model="form.theme_enabled" type="checkbox" data-testid="observability-theme" />
        {{ t('admin.settings.observability.themeEnabled') }}
      </label>
      <div class="flex items-center gap-3">
        <button type="button" class="btn btn-secondary" :disabled="saving" data-testid="observability-save" @click="save">
          {{ t('admin.settings.observability.save') }}
        </button>
        <span v-if="status" class="text-sm text-gray-500 dark:text-gray-400" role="status">{{ status }}</span>
      </div>
    </template>
    <TotpStepUpDialog :controller="stepUp" />
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getObservabilitySettings, updateObservabilitySettings, type ObservabilitySettings } from '@/api/admin/settings'
import { useAppStore } from '@/stores'
import { applyFlatTheme } from '@/utils/flatTheme'
import { useStepUp, isStepUpCancelled } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'

const { t } = useI18n()
const appStore = useAppStore()
const stepUp = useStepUp()
const form = ref<ObservabilitySettings | null>(null)
const loadError = ref(false)
const saving = ref(false)
const status = ref('')

async function save() {
  if (!form.value) return
  saving.value = true
  status.value = ''
  const submitted = { ...form.value }
  try {
    form.value = await stepUp.run(() => updateObservabilitySettings(submitted))
    applyFlatTheme(form.value.theme_enabled)
    status.value = t('admin.settings.observability.saved')
    void appStore.fetchPublicSettings(true)
  } catch (error) {
    if (!isStepUpCancelled(error)) status.value = t('admin.settings.observability.saveFailed')
  } finally {
    saving.value = false
  }
}

onMounted(async () => {
  try {
    form.value = await getObservabilitySettings()
  } catch {
    loadError.value = true
  }
})
</script>
