<template>
  <section class="space-y-3" aria-labelledby="official-model-catalog-title" data-testid="official-model-catalog">
    <div>
      <h3 id="official-model-catalog-title" class="text-base font-semibold text-gray-900 dark:text-white">
        {{ t('admin.settings.officialModelCatalog.title') }}
      </h3>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
        {{ t('admin.settings.officialModelCatalog.description') }}
      </p>
    </div>
    <input
      v-model="query"
      type="search"
      class="input"
      data-testid="official-model-catalog-search"
      :aria-label="t('admin.settings.officialModelCatalog.search')"
      :placeholder="t('admin.settings.officialModelCatalog.search')"
    />
    <p v-if="loadError" class="text-sm text-red-600 dark:text-red-400">{{ t('admin.settings.officialModelCatalog.loadFailed') }}</p>
    <template v-else-if="loaded">
      <p class="text-xs text-gray-500 dark:text-gray-400">
        {{ t('admin.settings.officialModelCatalog.count', { count: matches.length, shown: shown.length }) }}
      </p>
      <ul class="divide-y divide-gray-100 dark:divide-dark-700">
        <li v-for="entry in shown" :key="`${entry.provider}/${entry.product}/${entry.model_id}`" class="py-2">
          <p class="text-sm font-medium text-gray-900 dark:text-white">{{ entry.model_id }}</p>
          <p class="text-sm text-gray-600 dark:text-gray-300">
            {{ entry.provider }} / {{ entry.product }} ·
            {{ t('admin.settings.officialModelCatalog.context') }}: {{ formatTokens(contextOf(entry)) }} ·
            {{ t('admin.settings.officialModelCatalog.maxOutput') }}: {{ formatTokens(entry.max_output_tokens) }}
          </p>
          <p v-if="entry.conditions" class="text-xs text-gray-500 dark:text-gray-400">{{ entry.conditions }}</p>
          <p v-if="entry.reference" class="text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.settings.officialModelCatalog.subscriptionMaximum') }}: {{ formatTokens(entry.reference.max_context_window) }}
          </p>
          <p class="text-xs text-gray-400 dark:text-gray-500">
            {{ t('admin.settings.officialModelCatalog.verified') }}: {{ entry.verified_at }} ·
            <a :href="entry.source_url" target="_blank" rel="noopener noreferrer" class="break-all text-primary-600 hover:underline dark:text-primary-400">{{ entry.source_url }}</a>
          </p>
        </li>
      </ul>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getOfficialModelCapacityCatalog, type OfficialModelCapacityEntry } from '@/api/admin/settings'

const shownLimit = 40
const { t, locale } = useI18n()
const entries = ref<OfficialModelCapacityEntry[]>([])
const loaded = ref(false)
const loadError = ref(false)
const query = ref('')

const matches = computed(() => {
  const needle = query.value.trim().toLowerCase()
  if (!needle) return entries.value
  return entries.value.filter(entry =>
    [entry.model_id, entry.provider, ...(entry.aliases ?? [])].some(value => value.toLowerCase().includes(needle)))
})
const shown = computed(() => matches.value.slice(0, shownLimit))

function contextOf(entry: OfficialModelCapacityEntry): number | undefined {
  return entry.context_window || entry.max_input_tokens || entry.max_context_window
}

function formatTokens(value?: number): string {
  return value ? value.toLocaleString(locale.value) : '—'
}

onMounted(async () => {
  try {
    entries.value = (await getOfficialModelCapacityCatalog()).entries
    loaded.value = true
  } catch {
    loadError.value = true
  }
})
</script>
