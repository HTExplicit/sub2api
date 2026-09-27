<template>
  <section class="space-y-4" aria-labelledby="retired-plugins-title" data-testid="retired-plugins">
    <div class="border-b border-gray-200 pb-3 dark:border-dark-700">
      <h2 id="retired-plugins-title" class="text-base font-semibold text-gray-900 dark:text-white">
        {{ t('admin.plugins.retired.title') }}
      </h2>
      <p class="mt-1 max-w-3xl text-sm text-gray-500 dark:text-gray-400">
        {{ t('admin.plugins.retired.description') }}
      </p>
    </div>

    <p v-if="loading" class="text-sm text-gray-500">{{ t('common.loading') }}</p>
    <p v-else-if="loadError" class="whitespace-pre-wrap break-words text-sm text-red-600 dark:text-red-400">
      {{ t('admin.plugins.retired.loadFailed') }}: {{ loadError }}
    </p>
    <template v-else-if="view">
      <p v-if="!view.installations.length && !view.receipt" class="text-sm text-gray-500">
        {{ t('admin.plugins.retired.empty') }}
      </p>
      <div class="grid grid-cols-1 gap-4 xl:grid-cols-2">
        <article
          v-for="plugin in view.installations"
          :key="plugin.id"
          class="card overflow-hidden border border-gray-200 dark:border-dark-700"
          data-testid="retired-plugin"
        >
          <div class="border-b border-gray-100 p-5 dark:border-dark-700">
            <div class="flex flex-wrap items-center gap-2">
              <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ plugin.name || plugin.plugin_key }}</h3>
              <span class="font-mono text-xs text-gray-500">v{{ plugin.version }}</span>
              <span class="rounded bg-gray-100 px-2 py-0.5 text-xs text-gray-600 dark:bg-dark-700 dark:text-gray-300">#{{ plugin.id }}</span>
            </div>
            <p v-if="plugin.description" class="mt-2 text-sm text-gray-600 dark:text-gray-300">{{ plugin.description }}</p>
          </div>
          <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 p-5 text-xs">
            <dt class="text-gray-500">{{ t('admin.plugins.retired.key') }}</dt>
            <dd class="break-all font-mono text-gray-800 dark:text-gray-200">{{ plugin.plugin_key }}<span v-if="plugin.author"> · {{ plugin.author }}</span></dd>
            <dt class="text-gray-500">{{ t('admin.plugins.retired.state') }}</dt>
            <dd class="font-mono text-gray-800 dark:text-gray-200">{{ plugin.state }}</dd>
            <template v-if="plugin.last_error">
              <dt class="text-gray-500">{{ t('admin.plugins.retired.lastError') }}</dt>
              <dd class="whitespace-pre-wrap break-words text-red-600 dark:text-red-400">{{ plugin.last_error }}</dd>
            </template>
            <dt class="text-gray-500">{{ t('admin.plugins.retired.installedAt') }}</dt>
            <dd class="text-gray-800 dark:text-gray-200">{{ formatDateTime(plugin.installed_at) }}<span v-if="plugin.installed_by"> · #{{ plugin.installed_by }}</span></dd>
            <template v-if="plugin.enabled_at">
              <dt class="text-gray-500">{{ t('admin.plugins.retired.enabledAt') }}</dt>
              <dd class="text-gray-800 dark:text-gray-200">{{ formatDateTime(plugin.enabled_at) }}</dd>
            </template>
            <dt class="text-gray-500">{{ t('admin.plugins.retired.updatedAt') }}</dt>
            <dd class="text-gray-800 dark:text-gray-200">{{ formatDateTime(plugin.updated_at) }}</dd>
            <dt class="text-gray-500">{{ t('admin.plugins.retired.signature') }}</dt>
            <dd class="font-mono text-gray-800 dark:text-gray-200">{{ plugin.signature_status || '-' }}</dd>
            <dt class="text-gray-500">{{ t('admin.plugins.retired.binarySHA256') }}</dt>
            <dd class="break-all font-mono text-gray-800 dark:text-gray-200">{{ plugin.binary_sha256 || '-' }}</dd>
            <dt class="text-gray-500">{{ t('admin.plugins.retired.paths') }}</dt>
            <dd class="break-all font-mono text-gray-800 dark:text-gray-200">
              <span v-for="path in [plugin.artifact_path, plugin.install_path, plugin.binary_path].filter(Boolean)" :key="path" class="block">{{ path }}</span>
              <span v-if="![plugin.artifact_path, plugin.install_path, plugin.binary_path].some(Boolean)">-</span>
            </dd>
            <dt class="text-gray-500">{{ t('admin.plugins.retired.bindings') }}</dt>
            <dd class="font-mono text-gray-800 dark:text-gray-200">
              <span v-for="binding in plugin.bindings" :key="binding.id" class="block break-all">
                {{ binding.capability }} · {{ binding.platform }}/{{ binding.account_type }} · {{ binding.enabled ? 'enabled' : 'disabled' }} · {{ binding.rollout_percent }}%
              </span>
              <span v-if="!plugin.bindings.length">-</span>
            </dd>
          </dl>
          <div class="space-y-3 border-t border-gray-100 p-5 dark:border-dark-700">
            <div>
              <p class="text-xs font-medium uppercase text-gray-500">{{ t('admin.plugins.retired.config') }}</p>
              <p v-if="plugin.config_error" class="mt-1 whitespace-pre-wrap break-words text-xs text-red-600 dark:text-red-400">
                {{ t('admin.plugins.retired.configError') }}: {{ plugin.config_error }}
              </p>
              <pre v-else class="mt-1 max-h-72 overflow-auto whitespace-pre-wrap break-all rounded bg-gray-50 p-3 font-mono text-xs text-gray-700 dark:bg-dark-900 dark:text-gray-300">{{ configText(plugin) }}</pre>
            </div>
            <div>
              <p class="text-xs font-medium uppercase text-gray-500">{{ t('admin.plugins.retired.manifest') }}</p>
              <pre class="mt-1 max-h-72 overflow-auto whitespace-pre-wrap break-all rounded bg-gray-50 p-3 font-mono text-xs text-gray-700 dark:bg-dark-900 dark:text-gray-300">{{ JSON.stringify(plugin.manifest, null, 2) }}</pre>
            </div>
          </div>
        </article>
      </div>
      <div v-if="view.receipt" class="card border border-gray-200 p-5 dark:border-dark-700" data-testid="retired-plugins-receipt">
        <p class="text-xs font-medium uppercase text-gray-500">{{ t('admin.plugins.retired.receipt') }}</p>
        <p class="mt-1 text-xs text-gray-600 dark:text-gray-300">
          {{ t('admin.plugins.retired.retiredAt') }}: {{ formatDateTime(view.receipt.retired_at) }}
        </p>
        <pre class="mt-2 max-h-96 overflow-auto whitespace-pre-wrap break-all rounded bg-gray-50 p-3 font-mono text-xs text-gray-700 dark:bg-dark-900 dark:text-gray-300">{{ JSON.stringify(view.receipt, null, 2) }}</pre>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { RetiredPluginInstallation, RetiredPluginsView } from '@/api/admin/plugins'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTime } from '@/utils/format'

const { t } = useI18n()
const view = ref<RetiredPluginsView | null>(null)
const loading = ref(false)
const loadError = ref('')

function configText(plugin: RetiredPluginInstallation): string {
  if (plugin.config !== undefined && plugin.config !== null) return JSON.stringify(plugin.config, null, 2)
  return plugin.config_text || '-'
}

onMounted(async () => {
  loading.value = true
  try {
    view.value = await adminAPI.plugins.listRetired()
  } catch (error) {
    loadError.value = extractApiErrorMessage(error, t('common.unknownError'))
  } finally {
    loading.value = false
  }
})
</script>
