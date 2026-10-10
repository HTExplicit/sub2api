<template>
  <article ref="element" class="min-w-0 space-y-3 rounded-xl border border-line p-3" :data-test="`pelican-result-${result.id}`">
    <div class="flex items-start justify-between gap-2">
      <div class="min-w-0"><h3 class="break-words text-sm font-semibold">{{ result.account_name }} #{{ result.account_id }}</h3><p class="break-words text-xs text-muted">{{ result.model_id }} · {{ result.effort || t('admin.pelicanTests.normalDefault') }}</p></div>
      <label class="flex shrink-0 items-center gap-1 text-xs"><input type="checkbox" :checked="selected" :disabled="(selectionFull || expired) && !selected" @change="$emit('select', result)" />{{ text('比较', 'Compare') }}</label>
    </div>
    <p class="text-xs font-medium" :class="['failed', 'incomplete'].includes(result.status) ? 'text-red-700 dark:text-red-300' : 'text-muted'">{{ stateLabel }}</p>
    <div class="aspect-[4/3] min-h-48 overflow-hidden rounded-lg border border-line bg-gray-50 dark:bg-dark-900">
      <BorrowPelicanPreview v-if="detail?.preview_url && !expired" :preview-url="detail.preview_url" :title="title" />
      <div v-else class="flex h-full min-h-48 items-center justify-center p-5 text-center text-sm text-muted">{{ loading ? t('common.loading') : error || placeholder }}</div>
    </div>
    <p class="text-xs text-muted">{{ t('admin.pelicanTests.phases.queued') }} {{ duration(result.queue_duration_ms) }} · {{ t('admin.pelicanTests.phases.preparing') }} {{ duration(result.preparation_duration_ms) }} · {{ t('admin.pelicanTests.phases.generating') }} {{ duration(result.generation_duration_ms) }}</p>
    <div class="flex flex-wrap gap-2">
      <button type="button" class="btn btn-secondary btn-sm" :disabled="loading || expired" @click="open(false)">{{ t('admin.pelicanTests.details') }}</button>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="loading || expired || !result.has_preview" @click="open(true)">{{ t('admin.codexGatewayBorrow.enlarge') }}</button>
      <button v-if="error && !expired" type="button" class="btn btn-ghost btn-sm" @click="load">{{ text('重新读取', 'Reload') }}</button>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { pelicanTestsAPI, type PelicanResultSummary, type PelicanTestResult } from '@/api/admin/pelicanTests'
import BorrowPelicanPreview from './BorrowPelicanPreview.vue'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = defineProps<{ result: PelicanResultSummary; selected: boolean; selectionFull: boolean; now: number }>()
const emit = defineEmits<{ select: [PelicanResultSummary]; open: [PelicanTestResult]; enlarge: [PelicanTestResult] }>()
const { t, locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const element = ref<HTMLElement | null>(null), visible = ref(false), loading = ref(false), error = ref('')
const detail = shallowRef<PelicanTestResult | null>(null), lifecycle = new AbortController()
let observer: IntersectionObserver | undefined, pending: Promise<void> | undefined
const expired = computed(() => Date.parse(props.result.expires_at) <= props.now)
const title = computed(() => `${props.result.account_name} #${props.result.account_id} · ${props.result.model_id}`)
const stateLabel = computed(() => expired.value ? text('记录已过期', 'Record expired') : props.result.interrupted ? text('服务中断，未自动重放', 'Interrupted; not replayed') : props.result.phase ? t(`admin.pelicanTests.phases.${props.result.phase}`) : props.result.status === 'complete' && !props.result.has_preview ? text('已完成 · 无法预览', 'Completed · No preview') : t(`admin.codexGatewayBorrow.states.${props.result.status}`))
const placeholder = computed(() => expired.value ? text('记录已过期', 'Record expired') : props.result.has_preview && !detail.value ? t('admin.pelicanTests.previewOnVisible') : props.result.status === 'complete' ? text('没有可预览的 HTML，可查看完整回答。', 'No previewable HTML; open the full response.') : ['pending', 'running'].includes(props.result.status) ? text('等待作品生成', 'Waiting for the work') : text('未生成可预览作品，请查看详情。', 'No previewable work; see details.'))
const duration = (ms?: number) => `${((ms || 0) / 1000).toFixed(1)} s`
function load(): Promise<void> {
  if (pending) return pending
  loading.value = true; error.value = ''
  pending = pelicanTestsAPI.getResult(props.result.task_id, props.result.id, lifecycle.signal).then(result => { if (!lifecycle.signal.aborted) detail.value = result }).catch(value => { if (!lifecycle.signal.aborted) error.value = extractApiErrorMessage(value, text('读取作品失败', 'Could not load the work')) }).finally(() => { loading.value = false; pending = undefined })
  return pending
}
async function open(enlarge: boolean) { if (!detail.value || (detail.value.preview_expires_at && Date.parse(detail.value.preview_expires_at) <= props.now)) await load(); if (detail.value) { if (enlarge) emit('enlarge', detail.value); else emit('open', detail.value) } }
watch(() => [visible.value, props.result.has_preview, expired.value], () => { if (visible.value && props.result.has_preview && !expired.value && !detail.value && !error.value) void load() })
onMounted(() => {
  if (typeof IntersectionObserver === 'undefined') { visible.value = true; return }
  observer = new IntersectionObserver(entries => { if (entries.some(entry => entry.isIntersecting)) { visible.value = true; observer?.disconnect() } }, { rootMargin: '100px' })
  if (element.value) observer.observe(element.value)
})
onBeforeUnmount(() => { lifecycle.abort(); observer?.disconnect() })
</script>
