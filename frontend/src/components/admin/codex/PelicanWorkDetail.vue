<template>
  <div class="min-w-0 space-y-4 text-sm" data-test="pelican-detail-content">
    <div v-if="result.preview_url" class="aspect-[3/2] max-h-[60vh] w-full overflow-hidden rounded-lg border border-line"><PelicanPreview :preview-url="result.preview_url" :title="title" interactive /></div>
    <PelicanEvaluationBadges :evaluation="result.evaluation" />
    <dl class="grid gap-3 sm:grid-cols-2">
      <div><dt class="text-xs text-muted">{{ text('账号与模型', 'Account and model') }}</dt><dd class="break-words">{{ title }} · {{ result.effort || text('模型默认', 'Model default') }}</dd></div>
      <div><dt class="text-xs text-muted">{{ text('耗时', 'Duration') }}</dt><dd>{{ ((result.duration_ms || 0) / 1000).toFixed(1) }}s · {{ result.started_at ? new Date(result.started_at).toLocaleString() : '—' }}</dd></div>
      <template v-if="result.evaluation?.test_items.includes('pelican')"><div>{{ text('场景', 'Scene') }}：{{ result.evaluation.scene }}</div><div>{{ text('校验码', 'Verification code') }}：{{ result.evaluation.nonce }}</div></template>
      <div class="break-all sm:col-span-2">{{ result.upstream_model }} · {{ result.actual_endpoint }} · {{ result.actual_protocol }} · {{ result.actual_transport }}</div>
    </dl>
    <pre v-if="result.error" class="detail-error" data-test="pelican-result-error">{{ result.error }}</pre>
    <div v-if="result.evaluation?.checks" class="flex flex-wrap gap-3 text-xs"><span v-for="(passed, key) in result.evaluation.checks" :key="key">{{ passed ? '✓' : '×' }} {{ checkName(String(key)) }}</span></div>
    <section v-if="result.evaluation?.review_checks || result.review_image" class="space-y-3 rounded-lg border border-line p-3">
      <h3 class="font-medium">{{ text('多帧视觉审核', 'Multi-frame visual review') }}</h3>
      <img v-if="result.review_image" :src="`data:image/png;base64,${result.review_image}`" :alt="text('按时间顺序排列的四帧审核截图', 'Four review frames in chronological order')" class="w-full rounded" />
      <p v-if="result.evaluation?.frame_times" class="text-xs text-muted">{{ result.evaluation.frame_times.map(value => `${value.toFixed(3)}s`).join(' · ') }}</p>
      <ul class="grid gap-2 sm:grid-cols-2"><li v-for="(passed, key) in result.evaluation?.review_checks" :key="key">{{ checkName(String(key)) }}：{{ passed === null ? text('证据不足', 'Inconclusive') : passed ? text('通过', 'Passed') : text('未通过', 'Not passed') }}</li></ul>
    </section>
    <div class="flex flex-wrap gap-2"><button v-if="source" type="button" class="btn btn-secondary btn-sm" @click="copySource">{{ text('复制源码', 'Copy source') }}</button><button v-if="source" type="button" class="btn btn-secondary btn-sm" @click="download(source, result.raw_svg ? 'svg' : 'html')">{{ result.raw_svg ? text('下载 SVG', 'Download SVG') : text('下载 HTML', 'Download HTML') }}</button><button type="button" class="btn btn-secondary btn-sm" @click="download(result.raw_answer, 'txt')">{{ text('下载回答', 'Download answer') }}</button><span role="status" class="text-xs text-muted">{{ copyMessage }}</span></div>
    <section v-for="(stage, key) in result.evidence" :key="key" class="space-y-2 rounded-lg border border-line p-3">
      <h3 class="font-medium">{{ label(String(key)) }} · {{ (stage.duration_ms / 1000).toFixed(1) }}s</h3>
      <pre v-if="stage.error" class="detail-error">{{ decodePelicanEvidence(stage.error) }}</pre>
      <details><summary>{{ text('实际提示词', 'Actual prompt') }}</summary><pre>{{ stage.prompt }}</pre></details>
      <details :open="key === 'candy'"><summary>{{ text('模型回答', 'Model answer') }}</summary><pre>{{ decodePelicanEvidence(stage.raw_answer) }}</pre></details>
      <details><summary>{{ text('完整上游响应与调用资料', 'Full upstream response and invocation') }}</summary><pre>{{ stage.invocation }}</pre><pre>{{ decodePelicanEvidence(stage.raw_response) }}</pre></details>
    </section>
    <details v-if="source"><summary>{{ text('作品源码', 'Work source') }}</summary><pre data-test="pelican-raw-html">{{ source }}</pre></details>
    <details><summary>{{ text('生成回答原文', 'Raw generation answer') }}</summary><pre data-test="pelican-raw-answer">{{ result.raw_answer }}</pre></details>
    <details><summary>{{ text('生成响应原文', 'Raw generation response') }}</summary><pre data-test="pelican-raw-response">{{ result.raw_response }}</pre></details>
  </div>
</template>
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PelicanTestResult } from '@/api/admin/pelicanTests'
import { decodePelicanEvidence, pelicanLabel } from '@/utils/pelicanDisplay'
import PelicanPreview from './PelicanPreview.vue'
import PelicanEvaluationBadges from './PelicanEvaluationBadges.vue'
const props = defineProps<{ result: PelicanTestResult }>()
const { locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const label = (value: string) => pelicanLabel(value, locale.value)
const source = computed(() => props.result.raw_svg || props.result.raw_html || props.result.html)
const title = computed(() => `${props.result.account_name} #${props.result.account_id} · ${props.result.model_id}`)
const copyMessage = ref('')
const names: Record<string, [string, string]> = { svg: ['SVG 格式', 'SVG format'], animation: ['动画声明', 'Animation'], nonce: ['校验码', 'Verification code'], pelican: ['鹈鹕形态', 'Pelican anatomy'], bicycle: ['自行车结构', 'Bicycle structure'], riding: ['骑乘关系', 'Riding position'], motion: ['协调运动', 'Coordinated motion'] }
function checkName(key: string) { const pair = names[key]; return pair ? text(...pair) : key }
async function copySource() { try { await navigator.clipboard.writeText(source.value); copyMessage.value = text('已复制', 'Copied') } catch { copyMessage.value = text('复制失败，请下载源码', 'Copy failed; download the source') } }
function download(content: string, ext: string) { const url = URL.createObjectURL(new Blob([content], { type: ext === 'svg' ? 'image/svg+xml' : 'text/plain;charset=utf-8' })); const link = document.createElement('a'); link.href = url; link.download = `${props.result.id}.${ext}`; link.click(); setTimeout(() => URL.revokeObjectURL(url), 0) }
</script>
<style scoped>
pre { white-space: pre-wrap; overflow-wrap: anywhere; max-height: 50vh; overflow: auto; font-size: .75rem; line-height: 1.7; }
summary { cursor: pointer; padding: .4rem 0; }
.detail-error { @apply rounded-lg border border-red-200 bg-red-50 p-3 text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-200; }
</style>
