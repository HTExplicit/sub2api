<template>
  <InfoPopover
    v-if="entry && variant === 'chip'"
    :label="t('admin.accounts.lastUpstreamError.title')"
    width-class="w-[26rem] max-w-[calc(100vw-1rem)]"
    data-test="account-last-upstream-error"
  >
    <template #trigger>
      <span
        class="inline-flex cursor-pointer items-center gap-1 whitespace-nowrap rounded px-1.5 py-0.5 text-xs font-medium"
        :class="toneClass"
      >
        <Icon name="exclamationTriangle" size="xs" :stroke-width="2" />
        {{ chipText }}
      </span>
    </template>
    <div class="space-y-1.5 text-left">
      <div class="font-semibold">{{ t('admin.accounts.lastUpstreamError.title') }}</div>
      <div class="flex flex-wrap gap-x-2 text-gray-300">
        <span v-if="entry.status" class="font-mono">HTTP {{ entry.status }}</span>
        <span v-if="entry.at" class="tabular-nums">{{ formatDateTime(entry.at) }}</span>
        <span v-if="entry.source">{{ t('admin.accounts.lastUpstreamError.source', { source: entry.source }) }}</span>
      </div>
      <pre class="whitespace-pre-wrap break-words font-mono text-[11px] leading-5" data-test="account-last-upstream-error-message">{{ entry.message }}</pre>
      <div class="text-gray-300">{{ t('admin.accounts.lastUpstreamError.note') }}</div>
    </div>
  </InfoPopover>

  <div
    v-else-if="entry"
    class="mt-3 rounded border border-gray-200 bg-gray-50 px-3 py-2 text-xs text-gray-700 dark:border-dark-700 dark:bg-dark-800 dark:text-gray-200"
    data-test="account-last-upstream-error"
  >
    <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
      <span class="font-semibold text-gray-900 dark:text-white">{{ t('admin.accounts.lastUpstreamError.title') }}</span>
      <span v-if="entry.status" class="rounded px-1.5 py-0.5 font-mono font-medium" :class="toneClass">HTTP {{ entry.status }}</span>
      <span v-if="entry.at" class="tabular-nums">{{ formatDateTime(entry.at) }}</span>
      <span v-if="entry.source">{{ t('admin.accounts.lastUpstreamError.source', { source: entry.source }) }}</span>
    </div>
    <p class="mt-1 text-gray-500 dark:text-dark-300">{{ t('admin.accounts.lastUpstreamError.note') }}</p>
    <pre
      tabindex="0"
      class="mt-2 max-h-72 overflow-auto whitespace-pre-wrap break-words rounded bg-white px-2 py-1.5 font-mono text-[11px] leading-5 text-gray-800 dark:bg-dark-900 dark:text-gray-100"
    >{{ entry.message }}</pre>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import InfoPopover from '@/components/common/InfoPopover.vue'
import Icon from '@/components/icons/Icon.vue'
import type { Account } from '@/types'
import { formatDateTime } from '@/utils/format'

// extra.last_upstream_error = { status, message, at, source }: the verbatim upstream error of the last
// 401 / 403 / 429 on an OpenAI API-key or OAuth account. It is only a record; scheduling ignores it.
// "chip" sits next to the status badge in the accounts table (a button: hover, click / tap or keyboard
// opens the full text in a popover), "panel" is the block in the account details drawer.
const props = withDefaults(defineProps<{
  account: Pick<Account, 'extra'>
  variant?: 'chip' | 'panel'
}>(), {
  variant: 'chip'
})

const { t } = useI18n()

type LastUpstreamError = { status: number | null; message: string; at: string; source: string }

const entry = computed<LastUpstreamError | null>(() => {
  const raw = (props.account.extra as Record<string, unknown> | undefined)?.last_upstream_error
  if (!raw || typeof raw !== 'object') return null
  const value = raw as Record<string, unknown>
  const status = Number(value.status)
  const message = typeof value.message === 'string' ? value.message : value.message == null ? '' : JSON.stringify(value.message)
  const normalized: LastUpstreamError = {
    status: Number.isFinite(status) && status > 0 ? status : null,
    message,
    at: typeof value.at === 'string' ? value.at : '',
    source: typeof value.source === 'string' ? value.source : ''
  }
  return normalized.status || normalized.message ? normalized : null
})

const chipText = computed(() => entry.value?.status
  ? t('admin.accounts.lastUpstreamError.chip', { status: entry.value.status })
  : t('admin.accounts.lastUpstreamError.chipNoStatus'))

const toneClass = computed(() => {
  const status = entry.value?.status ?? 0
  if (status === 401 || status === 403) return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400'
  if (status === 429) return 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400'
  return 'bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-gray-300'
})
</script>
