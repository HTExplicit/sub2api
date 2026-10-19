<template>
  <article class="min-w-0 overflow-hidden rounded-xl border bg-white transition-shadow dark:bg-dark-900" :class="selected ? 'border-primary-500 ring-2 ring-primary-500/20' : 'border-line hover:shadow-md'" :data-test="`pelican-result-${result.id}`">
    <button type="button" class="relative block aspect-[4/3] w-full overflow-hidden bg-gray-50 text-left focus-visible:ring-2 focus-visible:ring-primary-500 dark:bg-dark-800" :disabled="expired" :aria-label="`${text('查看详情', 'View details')} ${title}`" @click="$emit('open', result)">
      <PelicanPreview v-if="result.preview_url && !expired" :preview-url="result.preview_url" :result-id="result.id" :title="title" />
      <span v-else class="flex h-full flex-col items-center justify-center gap-3 px-5 text-center text-sm text-muted"><span v-if="unfinished" class="h-7 w-7 animate-spin rounded-full border-2 border-line border-t-primary-600 motion-reduce:animate-none" aria-hidden="true" /><span v-else class="text-3xl font-light" aria-hidden="true">{{ result.evaluation?.test_items.includes('pelican') === false ? '21' : '—' }}</span>{{ placeholder }}</span>
      <span class="absolute left-3 top-3 rounded-md border border-line bg-white/95 px-2.5 py-1.5 text-xs font-medium dark:bg-dark-900/95" :class="pelicanTone(verdict)">{{ verdictLabel }}</span>
      <span class="absolute bottom-3 right-3 rounded-md border border-line bg-white/95 px-2.5 py-1.5 text-xs text-ink dark:bg-dark-900/95">{{ text('查看作品 ↗', 'Open work ↗') }}</span>
    </button>
    <div class="space-y-3 p-4">
      <div><h3 class="break-words text-sm font-semibold">{{ result.model_id }}</h3><p class="mt-1 break-words text-xs text-muted">{{ result.account_name }} <span class="opacity-80">#{{ result.account_id }}</span></p></div>
      <PelicanEvaluationBadges :evaluation="result.evaluation" :status="result.status" />
      <div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted"><span>{{ result.effort || t('admin.pelicanTests.normalDefault') }}</span><span class="tabular-nums">{{ ((result.duration_ms || 0) / 1000).toFixed(1) }}s</span><span data-test="pelican-repeat-label">{{ repeatLabel }}</span></div>
      <p v-if="unfinished" role="status" class="text-xs text-primary-700 dark:text-primary-300">{{ text('执行阶段', 'Execution stage') }} · {{ pelicanLabel(result.phase || result.status, locale) }}</p>
      <p v-if="result.evaluation?.scene" class="text-xs text-muted">{{ text('历史场景', 'Legacy scene') }} · {{ result.evaluation.scene }}</p>
      <div class="flex items-center justify-between gap-2 border-t border-line pt-3"><label class="flex cursor-pointer items-center gap-2 text-xs"><input type="checkbox" :checked="selected" :aria-label="`${text('比较', 'Compare')} ${title} ${repeatLabel}`" :disabled="(selectionFull || expired) && !selected" @change="$emit('select', result)" />{{ selected ? text('已选比较', 'Selected') : text('加入比较', 'Compare') }}</label><time class="text-xs tabular-nums text-muted">{{ date }}</time></div>
      <details class="border-t border-line pt-2 text-xs"><summary class="cursor-pointer py-1 text-muted">{{ text('更多操作', 'More actions') }}</summary><div class="mt-2 flex flex-wrap gap-2">
        <button type="button" class="btn btn-secondary btn-sm" :disabled="expired" @click="$emit('open', result)">{{ text('详情与下载', 'Details and downloads') }}</button>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || expired || unfinished" data-test="pelican-retest-result" @click="$emit('retest', result)">{{ text('重测', 'Retest') }}</button>
        <button type="button" class="btn btn-ghost btn-sm text-red-700 dark:text-red-300" :disabled="busy || expired || !canDelete || unfinished" data-test="pelican-delete-result" @click="$emit('delete', result)">{{ text('删除', 'Delete') }}</button>
      </div></details>
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
