<template>
  <div class="min-w-0 text-xs" data-test="cindy-probe-summary">
    <div v-if="showLabel" class="mb-1 text-[10px] font-medium uppercase text-gray-400">
      {{ t('admin.accounts.cindyProbe.recent') }}
    </div>
    <div v-if="hasRecord" class="min-w-0">
      <div class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
        <span class="shrink-0 font-mono text-gray-500 dark:text-dark-300" data-test="cindy-probe-summary-job">
          {{ jobLabel }}
        </span>
        <span
          class="max-w-full break-words rounded px-1.5 py-0.5 text-[10px] font-medium leading-4"
          :class="outcomeClass"
          data-test="cindy-probe-summary-outcome"
        >
          {{ outcomeLabel }}
        </span>
      </div>
      <div
        class="mt-1 max-w-full truncate text-[11px] text-gray-500 dark:text-dark-300"
        :title="checkedAtLabel === emptyValue ? '' : checkedAtLabel"
        data-test="cindy-probe-summary-time"
      >
        {{ checkedAtLabel }}
      </div>
    </div>
    <span v-else class="text-gray-400 dark:text-dark-500" data-test="cindy-probe-summary-empty">{{ emptyValue }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { formatDateTime } from '@/utils/format'
import type { Account } from '@/types'

const props = withDefaults(defineProps<{
  account: Account
  showLabel?: boolean
}>(), {
  showLabel: false,
})

const { t } = useI18n()
const emptyValue = '--'

const translatedOutcomes = new Set([
  'pending',
  'luna_running',
  'luna_exact',
  'terra_running',
  'healthy',
  'recovered',
  'still_exhausted',
  'exhausted',
  'already_marked',
  'inconclusive',
  'confirmation_expired',
  'skipped_stale',
  'unknown_after_crash',
  'canceled',
])

const normalizedOutcome = computed(() => String(props.account.cindy_balance_probe_outcome || '').trim())
const hasRecord = computed(() =>
  props.account.cindy_balance_probe_job_id != null ||
  normalizedOutcome.value !== '' ||
  Boolean(props.account.cindy_balance_probe_checked_at),
)
const jobLabel = computed(() => props.account.cindy_balance_probe_job_id == null
  ? emptyValue
  : `#${props.account.cindy_balance_probe_job_id}`,
)
const outcomeLabel = computed(() => {
  if (!normalizedOutcome.value) return emptyValue
  return translatedOutcomes.has(normalizedOutcome.value)
    ? t(`admin.accounts.cindyProbe.itemState.${normalizedOutcome.value}`)
    : normalizedOutcome.value
})
const checkedAtLabel = computed(() => {
  if (!props.account.cindy_balance_probe_checked_at) return emptyValue
  return formatDateTime(props.account.cindy_balance_probe_checked_at) || emptyValue
})
const outcomeClass = computed(() => {
  if (['healthy', 'recovered'].includes(normalizedOutcome.value)) {
    return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300'
  }
  if (['luna_exact', 'still_exhausted', 'exhausted'].includes(normalizedOutcome.value)) {
    return 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-300'
  }
  if (['inconclusive', 'confirmation_expired', 'unknown_after_crash'].includes(normalizedOutcome.value)) {
    return 'bg-amber-50 text-amber-700 dark:bg-amber-900/20 dark:text-amber-300'
  }
  if (['luna_running', 'terra_running'].includes(normalizedOutcome.value)) {
    return 'bg-primary-50 text-primary-700 dark:bg-primary-900/20 dark:text-primary-300'
  }
  return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-dark-300'
})
</script>
