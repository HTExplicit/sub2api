<template>
  <article class="min-w-0 overflow-hidden rounded-xl border border-line bg-white shadow-sm dark:bg-dark-900" :data-test="`pelican-result-${result.id}`">
    <button type="button" class="relative block aspect-[3/2] w-full overflow-hidden bg-gray-50 text-left focus-visible:ring-2 focus-visible:ring-primary-500 dark:bg-dark-800" :disabled="expired" :aria-label="`${text('查看详情', 'View details')} ${title}`" @click="$emit('open', result)">
      <PelicanPreview v-if="result.preview_url && !expired" :preview-url="result.preview_url" :result-id="result.id" :title="title" />
      <span v-else class="flex h-full flex-col items-center justify-center gap-2 px-5 text-center text-sm text-muted"><span class="text-2xl" aria-hidden="true">{{ unfinished ? '◌' : result.evaluation?.test_items.includes('pelican') === false ? '21' : '↯' }}</span>{{ placeholder }}</span>
      <span class="absolute bottom-3 right-3 rounded-md border border-white/70 bg-white/95 px-2.5 py-1.5 text-xs text-gray-700 shadow-sm dark:border-line dark:bg-dark-900/95 dark:text-gray-100">{{ text('查看详情 ↗', 'View details ↗') }}</span>
    </button>
    <div class="space-y-3 p-4">
      <div class="flex items-center justify-between gap-2"><time class="text-xs tabular-nums text-muted">{{ date }}</time><span class="rounded px-2 py-1 text-xs" :class="pelicanTone(verdict)">{{ verdictLabel }}</span></div>
      <PelicanEvaluationBadges :evaluation="result.evaluation" :status="result.status" />
      <div class="space-y-1.5 text-xs leading-relaxed text-muted">
        <p class="break-words">{{ text('账号', 'Account') }}：{{ result.account_name }} #{{ result.account_id }}</p>
        <p v-if="result.evaluation?.scene">{{ text('场景', 'Scene') }}：{{ result.evaluation.scene }}</p>
        <p class="break-words">{{ result.model_id }} · {{ result.effort || t('admin.pelicanTests.normalDefault') }} · {{ (result.duration_ms / 1000).toFixed(1) }}s</p>
        <p data-test="pelican-repeat-label">{{ repeatLabel }}</p>
        <p>{{ text('手动检测', 'Manual test') }}<template v-if="result.evaluation?.nonce && result.evaluation?.checks && 'nonce' in result.evaluation.checks"> · {{ text('校验码', 'Verification code') }} {{ result.evaluation.checks.nonce ? text('一致', 'Matched') : text('未通过', 'Not matched') }}</template></p>
      </div>
      <div class="flex flex-wrap items-center gap-2 border-t border-line pt-3 text-xs">
        <label class="mr-auto flex items-center gap-1.5"><input type="checkbox" :checked="selected" :disabled="(selectionFull || expired) && !selected" @change="$emit('select', result)" />{{ text('比较', 'Compare') }}</label>
        <button type="button" class="rounded px-1 py-1.5 hover:bg-gray-50 dark:hover:bg-dark-800" :disabled="expired || !result.has_preview" @click="$emit('enlarge', result)">{{ text('放大', 'Enlarge') }}</button>
        <button type="button" class="rounded px-1 py-1.5 hover:bg-gray-50 dark:hover:bg-dark-800" :disabled="busy || expired || unfinished" data-test="pelican-retest-result" @click="$emit('retest', result)">{{ text('重测', 'Retest') }}</button>
        <button type="button" class="rounded px-1 py-1.5 text-red-700 dark:text-red-300" :disabled="busy || expired || !canDelete || unfinished" data-test="pelican-delete-result" @click="$emit('delete', result)">{{ text('删除', 'Delete') }}</button>
      </div>
    </div>
  </article>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PelicanResultSummary } from '@/api/admin/pelicanTests'
import PelicanPreview from './PelicanPreview.vue'
import PelicanEvaluationBadges from './PelicanEvaluationBadges.vue'
import { pelicanLabel, pelicanTone, pelicanRepeatLabel } from '@/utils/pelicanDisplay'
const props = defineProps<{ result: PelicanResultSummary; selected: boolean; selectionFull: boolean; now: number; canDelete?: boolean; busy?: boolean }>()
defineEmits<{ select: [PelicanResultSummary]; open: [PelicanResultSummary]; enlarge: [PelicanResultSummary]; retest: [PelicanResultSummary]; delete: [PelicanResultSummary] }>()
const { t, locale } = useI18n()
const repeatLabel = computed(() => pelicanRepeatLabel(props.result, locale.value))
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const expired = computed(() => Date.parse(props.result.expires_at) <= props.now)
const unfinished = computed(() => ['pending', 'running'].includes(props.result.status))
const title = computed(() => `${props.result.account_name} · ${props.result.model_id}`)
const date = computed(() => props.result.started_at ? new Date(props.result.started_at).toLocaleString(locale.value, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }) : '—')
const verdict = computed(() => expired.value || props.result.interrupted ? 'incomplete' : unfinished.value ? props.result.phase || props.result.status : props.result.evaluation?.verdict || props.result.status)
const verdictLabel = computed(() => {
  if (expired.value) return text('记录已过期', 'Expired')
  if (props.result.interrupted) return text('服务中断', 'Interrupted')
  const e = props.result.evaluation
  if (verdict.value === 'passed' && e?.test_items.length === 2) return text('双项通过', 'Both passed')
  if (e && ['invalid', 'error'].includes(verdict.value)) {
    const stage = e.generation === verdict.value ? text('生成校验', 'Generation') : e.candy === verdict.value ? text('糖果', 'Candy') : text('视觉审核', 'Visual review')
    return `${stage}${locale.value.startsWith('zh') ? '' : ' · '}${pelicanLabel(verdict.value, locale.value)}`
  }
  return pelicanLabel(verdict.value, locale.value)
})
const placeholder = computed(() => expired.value ? text('记录已过期', 'Expired') : unfinished.value ? pelicanLabel(verdict.value, locale.value) : props.result.evaluation?.test_items.includes('pelican') === false ? text('糖果推理检测', 'Candy reasoning test') : text('本轮未生成可展示动画', 'No animation available'))
</script>
