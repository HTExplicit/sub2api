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
      <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
        <input v-model="form.responses_image_enabled" type="checkbox" data-testid="image-tools-responses" />
        {{ t('admin.settings.imageTools.responsesImageEnabled') }}
      </label>
      <div class="flex items-center gap-3">
        <button type="button" class="btn btn-secondary" :disabled="saving" data-testid="image-tools-save" @click="save">
          {{ t('admin.settings.imageTools.save') }}
        </button>
        <span v-if="status" class="text-sm text-gray-500 dark:text-gray-400" role="status">{{ status }}</span>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getImageToolsSettings, updateImageToolsSettings, type ImageToolsSettings } from '@/api/admin/settings'

const { t } = useI18n()
const form = ref<ImageToolsSettings | null>(null)
const loadError = ref(false)
const saving = ref(false)
const status = ref('')

async function save() {
  if (!form.value) return
  saving.value = true
  status.value = ''
  try {
    form.value = await updateImageToolsSettings({ ...form.value })
    status.value = t('admin.settings.imageTools.saved')
  } catch {
    status.value = t('admin.settings.imageTools.saveFailed')
  } finally {
    saving.value = false
  }
}

onMounted(async () => {
  try {
    form.value = await getImageToolsSettings()
  } catch {
    loadError.value = true
  }
})
</script>
