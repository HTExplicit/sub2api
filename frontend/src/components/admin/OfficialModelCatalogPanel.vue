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
    <p v-if="loadError" class="whitespace-pre-wrap break-words text-sm text-red-600 dark:text-red-400">{{ t('admin.settings.officialModelCatalog.loadFailed') }}: {{ loadError }}</p>
    <template v-else-if="loaded">
      <p class="text-xs text-gray-500 dark:text-gray-400">
        {{ t('admin.settings.officialModelCatalog.count', { count: matches.length }) }}
      </p>
      <ul
        class="official-model-catalog-list max-h-[70vh] divide-y divide-gray-100 overflow-y-auto rounded-md border border-gray-200 px-3 dark:divide-dark-700 dark:border-dark-700"
        tabindex="0"
        aria-labelledby="official-model-catalog-title"
        data-testid="official-model-catalog-list"
      >
        <li v-for="entry in matches" :key="`${entry.provider}/${entry.product}/${entry.model_id}`" class="py-2" data-testid="official-model-catalog-entry">
          <p class="text-sm font-medium text-gray-900 dark:text-white">{{ entry.model_id }}</p>
          <p class="text-sm text-gray-600 dark:text-gray-300">{{ entry.provider }} / {{ entry.product }}</p>
          <dl class="mt-1 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-0.5 text-xs text-gray-500 dark:text-gray-400">
            <template v-for="field in entryFields(entry)" :key="field.label">
              <dt>{{ field.label }}</dt>
              <dd class="whitespace-pre-wrap break-words text-gray-700 dark:text-gray-300">
                <template v-if="field.links">
                  <a v-for="url in field.links" :key="url" :href="url" target="_blank" rel="noopener noreferrer" class="block break-all text-primary-600 hover:underline dark:text-primary-400">{{ url }}</a>
                </template>
                <template v-else>{{ field.value }}</template>
              </dd>
            </template>
          </dl>
        </li>
      </ul>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getOfficialModelCapacityCatalog, type OfficialModelCapacityEntry } from '@/api/admin/settings'
import { extractApiErrorMessage } from '@/utils/apiError'

// Every field the catalog returns is shown, including the rules that limit
// where an entry applies.
type CatalogEntry = OfficialModelCapacityEntry & {
  capacity_basis?: string
  observed_at?: string
  source_urls?: string[]
  original_text?: string
  normalization_basis?: string
  match_hosts?: string[]
  match_account_modes?: string[]
}
type CatalogField = { label: string; value?: string; links?: string[] }

const { t, locale } = useI18n()
const entries = ref<CatalogEntry[]>([])
const loaded = ref(false)
const loadError = ref('')
const query = ref('')

const matches = computed(() => {
  const needle = query.value.trim().toLowerCase()
  if (!needle) return entries.value
  return entries.value.filter(entry =>
    [entry.model_id, entry.provider, entry.product, ...(entry.aliases ?? [])].some(value => value?.toLowerCase().includes(needle)))
})

function formatTokens(value?: number): string {
  return value ? value.toLocaleString(locale.value) : '—'
}

function entryFields(entry: CatalogEntry): CatalogField[] {
  const label = (key: string) => t(`admin.settings.officialModelCatalog.${key}`)
  const fields: CatalogField[] = []
  const text = (key: string, value?: string) => { if (value) fields.push({ label: label(key), value }) }
  const list = (key: string, values?: string[]) => { if (values?.length) fields.push({ label: label(key), value: values.join(', ') }) }
  const tokens = (key: string, value?: number) => { if (value) fields.push({ label: label(key), value: formatTokens(value) }) }
  list('aliases', entry.aliases)
  tokens('contextWindow', entry.context_window)
  tokens('maxContextWindow', entry.max_context_window)
  tokens('maxInputTokens', entry.max_input_tokens)
  tokens('maxOutput', entry.max_output_tokens)
  text('capacityBasis', entry.capacity_basis)
  text('observedAt', entry.observed_at)
  text('conditions', entry.conditions)
  text('normalizationBasis', entry.normalization_basis)
  text('originalText', entry.original_text)
  list('matchHosts', entry.match_hosts)
  list('matchAccountModes', entry.match_account_modes)
  if (entry.reference) {
    const reference = entry.reference
    fields.push({
      label: label('reference'),
      value: [
        `${reference.product} ${reference.release}`,
        `${label('contextWindow')}: ${formatTokens(reference.context_window)}`,
        `${label('subscriptionMaximum')}: ${formatTokens(reference.max_context_window)}`,
        `${label('verified')}: ${reference.verified_at}`,
        reference.source_url,
      ].join('\n'),
    })
  }
  text('verified', entry.verified_at)
  const links = [entry.source_url, ...(entry.source_urls ?? [])].filter((url, index, all) => !!url && all.indexOf(url) === index)
  if (links.length) fields.push({ label: label('sources'), links })
  return fields
}

onMounted(async () => {
  try {
    entries.value = (await getOfficialModelCapacityCatalog()).entries as CatalogEntry[]
    loaded.value = true
  } catch (error) {
    loadError.value = extractApiErrorMessage(error, t('common.unknownError'))
  }
})
</script>

<style scoped>
/* The list scrolls inside its own box, so its scrollbar stays visible (the global thumb shows only on hover). */
.official-model-catalog-list {
  scrollbar-width: thin;
  scrollbar-color: rgb(var(--ui-control-line, 156 163 175)) transparent;
}
</style>
