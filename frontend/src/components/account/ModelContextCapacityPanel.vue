<template>
  <section
    data-testid="model-context-capacity-panel"
    class="rounded-lg border border-gray-200 dark:border-dark-600"
    :aria-busy="loading || syncing"
  >
    <div class="border-b border-gray-200 px-3 py-3 dark:border-dark-600">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <h3 class="text-sm font-medium text-gray-900 dark:text-white">{{ t(`${key}.title`) }}</h3>
        <button
          v-if="canSync"
          type="button"
          data-testid="context-capacity-sync"
          :disabled="syncing || syncDisabled"
          class="btn btn-secondary btn-sm text-xs disabled:cursor-not-allowed disabled:opacity-50"
          @click="requestSync"
        >{{ t(syncing ? 'admin.accounts.syncUpstreamModelsLoading' : 'admin.accounts.syncUpstreamModels') }}</button>
      </div>
      <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t(`${key}.description`) }}</p>
      <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t(`${key}.units`) }}</p>
      <p v-if="canSync && syncDisabled && syncDisabledReason" class="mt-1 text-xs text-amber-700 dark:text-amber-300">
        {{ syncDisabledReason }}
      </p>
    </div>
    <p v-if="loading" role="status" class="px-3 py-2 text-xs text-gray-500 dark:text-gray-400">
      {{ t(`${key}.loading`) }}
    </p>
    <p v-if="error" role="status" class="px-3 py-2 text-xs text-amber-700 dark:text-amber-300">{{ error }}</p>
    <p v-if="!rows.length && !loading && !error" class="px-3 py-4 text-sm text-gray-500 dark:text-gray-400">
      {{ t(`${key}.empty`) }}
    </p>
    <div class="max-h-[32rem] divide-y divide-gray-200 overflow-y-auto dark:divide-dark-600">
      <article
        v-for="row in rows"
        :key="row.upstream_model_id"
        data-testid="context-capacity-row"
        :data-model-id="row.upstream_model_id"
        class="space-y-3 px-3 py-3"
      >
        <div class="flex flex-wrap items-start justify-between gap-2">
          <div class="min-w-0 flex-1">
            <p class="break-all text-sm font-medium text-gray-900 dark:text-white">{{ row.upstream_model_id }}</p>
            <p v-if="row.aliases.length" class="mt-1 break-all text-xs text-gray-500 dark:text-gray-400">
              {{ t(`${key}.aliases`) }}: {{ row.aliases.join(', ') }}
            </p>
          </div>
          <div class="text-right text-xs">
            <div class="text-gray-500 dark:text-gray-400">{{ t(`${key}.effective`) }}</div>
            <div data-testid="context-capacity-effective" class="mt-0.5 font-semibold text-gray-900 dark:text-white">
              {{ formatContextCapacity(preview(row).value) }}
              <span class="font-normal text-gray-500 dark:text-gray-400"> · {{ sourceLabel(preview(row).source) }}</span>
            </div>
            <p v-if="preview(row).value" class="mt-0.5 text-gray-500 dark:text-gray-400">
              {{ t(`${key}.exactTokens`, { value: preview(row).value }) }}
            </p>
            <p class="mt-0.5 text-gray-500 dark:text-gray-400">{{ basisLabel(previewBasis(row)) }}</p>
          </div>
        </div>

        <div class="grid gap-3 text-xs sm:grid-cols-3">
          <div class="min-w-0">
            <h4 class="font-medium text-gray-700 dark:text-gray-300">{{ t(`${key}.upstream`) }}</h4>
            <template v-if="row.upstream">
              <dl class="mt-1 space-y-0.5 text-gray-500 dark:text-gray-400">
                <div v-for="field in capacityFields(row.upstream)" :key="field.key" class="flex flex-wrap justify-between gap-x-2">
                  <dt>{{ t(`${key}.${field.key}`) }}</dt><dd>{{ formatContextCapacity(field.value) }}</dd>
                </div>
              </dl>
              <p v-if="!capacityFields(row.upstream).length" class="mt-1 text-gray-500 dark:text-gray-400">{{ t(`${key}.unknown`) }}</p>
              <p v-if="row.upstream.capacity_basis" class="mt-1 text-gray-500 dark:text-gray-400">{{ basisLabel(row.upstream.capacity_basis) }}</p>
              <p v-if="row.upstream.observed_at" class="mt-1 break-words text-gray-500 dark:text-gray-400">
                {{ t(`${key}.observedAt`) }}: <time :datetime="row.upstream.observed_at">{{ row.upstream.observed_at }}</time>
              </p>
            </template>
            <p v-else class="mt-1 text-gray-500 dark:text-gray-400">{{ t(`${key}.unknown`) }}</p>
          </div>

          <div class="min-w-0">
            <h4 class="font-medium text-gray-700 dark:text-gray-300">{{ t(`${key}.official`) }}</h4>
            <template v-if="row.official">
              <dl class="mt-1 space-y-0.5 text-gray-500 dark:text-gray-400">
                <div v-for="field in capacityFields(row.official)" :key="field.key" class="flex flex-wrap justify-between gap-x-2">
                  <dt>{{ t(`${key}.${field.key}`) }}</dt><dd>{{ formatContextCapacity(field.value) }}</dd>
                </div>
              </dl>
              <p v-if="row.official.capacity_basis" class="mt-1 text-gray-500 dark:text-gray-400">{{ basisLabel(row.official.capacity_basis) }}</p>
              <p class="mt-1 text-gray-500 dark:text-gray-400">{{ row.official.provider }} · {{ row.official.product }}</p>
              <details class="mt-1 text-gray-500 dark:text-gray-400">
                <summary class="cursor-pointer text-primary-600 dark:text-primary-400">{{ t(`${key}.officialEvidence`) }}</summary>
                <p class="mt-1 break-words">{{ row.official.original_text }}</p>
                <p v-if="row.official.normalization_basis" class="mt-1 break-words">
                  {{ t(`${key}.normalization`) }}: {{ row.official.normalization_basis }}
                </p>
                <p v-if="row.official.conditions" class="mt-1 break-words">{{ row.official.conditions }}</p>
                <p class="mt-1">{{ t(`${key}.verifiedAt`) }}: <time :datetime="row.official.verified_at">{{ row.official.verified_at }}</time></p>
                <a
                  v-for="(url, index) in officialLinks(row)"
                  :key="url"
                  :href="url"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="mt-1 block break-all text-primary-600 hover:underline dark:text-primary-400"
                >{{ t(`${key}.officialSource`) }}{{ index ? ` ${index + 1}` : '' }}</a>
              </details>
            </template>
            <p v-else class="mt-1 text-gray-500 dark:text-gray-400">{{ t(`${key}.notMatched`) }}</p>
          </div>

          <div class="min-w-0">
            <label class="block">
              <span class="font-medium text-gray-700 dark:text-gray-300">{{ t(`${key}.custom`) }}</span>
              <input
                v-if="row.editable"
                :value="draftValue(row)"
                :placeholder="formatContextCapacity(row.automatic_context_window)"
                :aria-label="`${t(`${key}.custom`)} ${row.upstream_model_id}`"
                :aria-invalid="!inputValid(row)"
                data-testid="context-capacity-input"
                type="text"
                inputmode="text"
                autocomplete="off"
                spellcheck="false"
                maxlength="80"
                class="input mt-1 w-full text-sm"
                :class="{ 'border-red-500 dark:border-red-500': !inputValid(row) }"
                @input="updateDraft(row, ($event.target as HTMLInputElement).value)"
              />
            </label>
            <template v-if="row.editable">
              <p v-if="!inputValid(row)" role="alert" class="mt-1 text-red-600 dark:text-red-400">{{ t(`${key}.invalid`) }}</p>
              <p v-else class="mt-1 text-gray-500 dark:text-gray-400">{{ t(`${key}.clearHint`) }}</p>
              <button
                v-if="draftValue(row)"
                type="button"
                data-testid="context-capacity-clear"
                class="mt-1 text-primary-600 hover:underline dark:text-primary-400"
                @click="updateDraft(row, '')"
              >{{ t(`${key}.clear`) }}</button>
            </template>
            <p v-else data-testid="context-capacity-readonly" class="mt-1 text-gray-500 dark:text-gray-400">{{ t(`${key}.readonly`) }}</p>
          </div>
        </div>

        <p
          v-if="exceedsUpstream(row)"
          data-testid="context-capacity-difference"
          class="rounded bg-amber-50 px-2 py-1.5 text-xs text-amber-800 dark:bg-amber-900/20 dark:text-amber-300"
        >{{ t(`${key}.exceedsUpstream`) }}</p>
        <p v-if="row.reason" class="break-words text-xs text-gray-500 dark:text-gray-400">{{ row.reason }}</p>
      </article>
    </div>
  </section>
</template>

<script setup lang="ts">
import { watchEffect } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ModelContextCapacityRow } from '@/api/admin/accounts'
import {
  areContextCapacityDraftsValid,
  formatContextCapacity,
  parseContextCapacityInput
} from '@/utils/modelContextCapacity'

const props = withDefaults(defineProps<{
  rows: ModelContextCapacityRow[]
  modelValue: Record<string, string>
  loading?: boolean
  error?: string
  canSync?: boolean
  syncing?: boolean
  syncDisabled?: boolean
  syncDisabledReason?: string
}>(), { loading: false, error: '', canSync: false, syncing: false, syncDisabled: false, syncDisabledReason: '' })

const emit = defineEmits<{
  'update:modelValue': [value: Record<string, string>]
  validity: [valid: boolean]
  sync: []
}>()

const { t } = useI18n()
const key = 'admin.accounts.contextCapacity'

watchEffect(() => emit('validity', areContextCapacityDraftsValid(props.modelValue)))

function requestSync(): void {
  if (props.canSync && !props.syncing && !props.syncDisabled) emit('sync')
}

function hasDraft(row: ModelContextCapacityRow): boolean {
  return Object.prototype.hasOwnProperty.call(props.modelValue, row.upstream_model_id)
}

function draftValue(row: ModelContextCapacityRow): string {
  return hasDraft(row)
    ? props.modelValue[row.upstream_model_id]
    : row.custom_context_window ? formatContextCapacity(row.custom_context_window) : ''
}

function inputValid(row: ModelContextCapacityRow): boolean {
  return !row.editable || parseContextCapacityInput(draftValue(row)).valid
}

function updateDraft(row: ModelContextCapacityRow, value: string): void {
  if (!row.editable) return
  emit('update:modelValue', { ...props.modelValue, [row.upstream_model_id]: value })
}

function preview(row: ModelContextCapacityRow): { value: number | null; source: string } {
  if (row.editable && hasDraft(row)) {
    const parsed = parseContextCapacityInput(draftValue(row))
    if (!parsed.valid) return { value: null, source: 'invalid' }
    if (parsed.value !== null) return { value: parsed.value, source: 'custom' }
    return { value: row.automatic_context_window, source: row.automatic_source }
  }
  return { value: row.effective_context_window, source: row.effective_source }
}

function previewBasis(row: ModelContextCapacityRow): string {
  const source = preview(row).source
  if (source === 'official') return row.official?.capacity_basis || 'total_context'
  if (source === 'upstream') return row.upstream?.capacity_basis || 'total_context'
  if (source === 'custom' || source === 'default') return 'total_context'
  return source === 'invalid' ? '' : row.capacity_basis
}

function sourceLabel(source: string): string {
  return ['custom', 'official', 'upstream', 'default', 'protected', 'invalid'].includes(source)
    ? t(`${key}.sources.${source}`)
    : t(`${key}.unknown`)
}

function basisLabel(basis: string): string {
  if (!basis) return ''
  if (basis === 'context_window') basis = 'total_context'
  return ['total_context', 'input_limit', 'max_context_window'].includes(basis)
    ? t(`${key}.basis.${basis}`)
    : basis
}

type CapacityValues = {
  context_window?: number
  max_context_window?: number
  max_input_tokens?: number
  max_output_tokens?: number
}

function capacityFields(capacity: CapacityValues): { key: keyof CapacityValues; value: number }[] {
  const fields: (keyof CapacityValues)[] = ['context_window', 'max_context_window', 'max_input_tokens', 'max_output_tokens']
  return fields.flatMap((field) => {
    const value = capacity[field]
    return value && Number.isSafeInteger(value) && value > 0 ? [{ key: field, value }] : []
  })
}

function exceedsUpstream(row: ModelContextCapacityRow): boolean {
  const upstream = row.upstream?.context_window || row.upstream?.max_context_window || row.upstream?.max_input_tokens
  const effective = preview(row).value
  return !!(upstream && effective && effective > upstream)
}

function officialLinks(row: ModelContextCapacityRow): string[] {
  const urls = [row.official?.source_url, ...(row.official?.source_urls ?? [])]
  return [...new Set(urls.filter((url): url is string => {
    if (!url) return false
    try {
      return ['https:', 'http:'].includes(new URL(url).protocol)
    } catch {
      return false
    }
  }))]
}
</script>
