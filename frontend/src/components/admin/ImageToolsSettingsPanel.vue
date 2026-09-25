<template>
  <section class="space-y-3" aria-labelledby="image-tools-settings-title" data-testid="image-tools-settings">
    <div>
      <h3 id="image-tools-settings-title" class="text-base font-semibold text-gray-900 dark:text-white">
        {{ t('admin.settings.imageTools.title') }}
      </h3>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.settings.imageTools.description') }}</p>
    </div>
    <p v-if="loadError" class="text-sm text-red-600 dark:text-red-400">{{ t('admin.settings.imageTools.loadFailed') }}</p>
    <template v-else-if="form">
      <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
        <input v-model="form.studio_enabled" type="checkbox" data-testid="image-tools-studio" />
        {{ t('admin.settings.imageTools.studioEnabled') }}
      </label>
      <div class="flex items-center gap-3">
        <button type="button" class="btn btn-secondary" :disabled="saving" data-testid="image-tools-save" @click="save">
          {{ t('admin.settings.imageTools.save') }}
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
import { getImageToolsSettings, updateImageToolsSettings, type ImageToolsSettings } from '@/api/admin/settings'
import { useAppStore } from '@/stores'
import { useStepUp, isStepUpCancelled } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'

const { t } = useI18n()
const appStore = useAppStore()
const stepUp = useStepUp()
const form = ref<ImageToolsSettings | null>(null)
const loadError = ref(false)
const saving = ref(false)
const status = ref('')

async function save() {
  if (!form.value) return
  saving.value = true
  status.value = ''
  // Only the Image Studio switch exists; never echo other switch fields back.
  const submitted: ImageToolsSettings = { studio_enabled: form.value.studio_enabled }
  try {
    const saved = await stepUp.run(() => updateImageToolsSettings(submitted))
    form.value = { studio_enabled: saved.studio_enabled }
    status.value = t('admin.settings.imageTools.saved')
    // The sidebar and route guard follow the public image_studio_enabled flag.
    void appStore.fetchPublicSettings(true)
  } catch (error) {
    if (!isStepUpCancelled(error)) status.value = t('admin.settings.imageTools.saveFailed')
  } finally {
    saving.value = false
  }
}

onMounted(async () => {
  try {
    const settings = await getImageToolsSettings()
    form.value = { studio_enabled: settings.studio_enabled }
  } catch {
    loadError.value = true
  }
})
</script>
