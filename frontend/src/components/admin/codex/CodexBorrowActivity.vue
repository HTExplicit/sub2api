<template>
  <section class="space-y-5 rounded-lg border border-line p-4 sm:p-5" aria-labelledby="codex-activity-title">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h2 id="codex-activity-title" class="font-semibold text-ink">{{ text('最近实际请求', 'Recent actual requests') }}</h2>
      <RouterLink to="/admin/pelican-tests" class="text-sm text-primary-700 underline dark:text-primary-300">{{ text('打开鹈鹕测试', 'Open Pelican tests') }}</RouterLink>
    </div>
    <p class="text-sm text-muted">{{ text('线路验证通过不代表业务已经使用。这里记录实际发送和上游完成情况；诊断请求单独标记。', 'A passed route check does not mean a business request used it. This records actual dispatch and completion; diagnostics are labelled separately.') }}</p>
    <p v-if="!status.recent_usage?.length" class="rounded-lg bg-surface p-4 text-sm text-muted" data-test="borrow-no-usage">{{ text('还没有实际发送记录。可运行下方对照，或在正常使用后刷新状态。', 'No dispatch has been observed. Run a comparison below, or refresh after normal use.') }}</p>
    <div v-else class="overflow-x-auto">
      <table class="w-full text-left text-sm">
        <thead class="border-b border-line text-muted"><tr><th class="p-2">{{ text('账号与模型', 'Account / model') }}</th><th class="p-2">{{ text('实际使用', 'Actual use') }}</th><th class="p-2">{{ text('上游结果', 'Upstream result') }}</th><th class="p-2">{{ text('时间与次数', 'Time / counts') }}</th></tr></thead>
        <tbody><tr v-for="row in status.recent_usage" :key="`${row.account_id}:${row.model}:${row.transport}:${row.origin}`" class="border-b border-line align-top">
          <td class="p-2"><span class="block">{{ accountName(row.account_id) }}</span><span class="text-xs text-muted">{{ row.model }} · {{ row.transport.toUpperCase() }} · {{ row.origin === 'business' ? text('业务', 'Business') : text('诊断', 'Diagnostic') }}</span></td>
          <td class="p-2">{{ row.applied ? text('已使用借用', 'Borrow applied') : text('未使用借用', 'Borrow not applied') }}</td>
          <td class="p-2">{{ outcome(row.outcome) }}<details class="mt-1 text-xs text-muted"><summary class="cursor-pointer">{{ text('请求详情', 'Request details') }}</summary><p class="break-all">{{ row.request_id || '—' }}</p><p>{{ row.reported_model || '—' }}</p><p>{{ row.reason }}</p></details></td>
          <td class="p-2"><time>{{ time(row.started_at) }}</time><p class="text-xs text-muted">{{ text('发送', 'Sent') }} {{ row.count }} · {{ text('借用', 'Borrowed') }} {{ row.applied_count }}</p></td>
        </tr></tbody>
      </table>
      <p class="mt-2 text-xs text-muted">{{ text('当前配置的进程内统计，开始于', 'In-process counters for the current configuration, since') }} {{ time(status.observed_since) }}</p>
    </div>
    <div class="space-y-3 border-t border-line pt-4">
      <h3 class="font-medium">{{ text('验证实际请求', 'Verify actual requests') }}</h3>
      <p class="text-sm text-muted">{{ text('用同一个账号对照普通路径和借用路径。每次最多 8 个短请求，包含准备与验证，会消耗上游额度；不会修改全局设置。', 'Compare ordinary and borrowed paths on the same account. Up to 8 short requests including preparation consume upstream quota; global settings are unchanged.') }}</p>
      <div class="flex flex-wrap items-end gap-3">
        <label class="min-w-0 flex-1 text-sm">{{ text('账号与模型', 'Account / model') }}<select v-model="selection" class="input mt-1 w-full" :disabled="running" data-test="diagnose-selection"><option v-for="pair in pairs" :key="pair.key" :value="pair.key">{{ accountName(pair.account_id) }} · {{ pair.model }}</option></select></label>
        <label class="text-sm">{{ text('连接方式', 'Transport') }}<select v-model="transport" class="input mt-1 w-full" :disabled="running"><option value="http">HTTP / SSE</option><option value="ws">WebSocket</option></select></label>
        <button class="btn btn-primary" type="button" :disabled="running || !status.enabled || !selected" data-test="diagnose-start" @click="diagnose">{{ text('运行一次对照', 'Run comparison') }}</button>
        <button v-if="running" class="btn btn-secondary" type="button" @click="cancel">{{ text('停止', 'Stop') }}</button>
        <button v-if="events.length" class="btn btn-secondary" type="button" @click="download">{{ text('下载结果', 'Download results') }}</button>
      </div>
      <p v-if="running" role="status" class="text-sm">{{ phase === 'ordinary' ? text('正在检查普通路径…', 'Checking ordinary path…') : text('正在准备并检查借用路径…', 'Preparing and checking borrowed path…') }} {{ text('已发送', 'Requests sent') }} {{ requests }} / 8</p>
      <pre v-if="error" role="alert" class="max-h-48 overflow-auto whitespace-pre-wrap break-words text-sm text-red-700 dark:text-red-300">{{ error }}</pre>
      <article v-for="(result, index) in results" :key="index" class="space-y-2 rounded-lg border border-line p-3 text-sm" data-test="diagnose-result">
        <p class="font-medium">{{ result.mode === 'ordinary' ? text('普通路径', 'Ordinary path') : text('借用路径', 'Borrowed path') }} · {{ text('第', 'Turn') }} {{ result.turn }} {{ result.completed ? text('轮：已完成', ': completed') : text('轮：未完成', ': not completed') }}</p>
        <p>{{ result.dispatched === false ? text('准备阶段结束，未发出对照请求', 'Stopped before dispatching this comparison') : result.applied ? text('实际已使用借用', 'Borrow was applied') : text('未使用借用', 'Borrow was not applied') }} · {{ result.reported_model || '—' }} · {{ result.duration_ms }} ms</p>
        <p class="whitespace-pre-wrap break-words">{{ result.answer || '—' }}</p>
        <pre v-if="result.error" class="whitespace-pre-wrap break-words text-red-700 dark:text-red-300">{{ result.error }}</pre>
        <details><summary class="cursor-pointer text-muted">{{ text('完整响应', 'Complete response') }}</summary><p v-if="result.read_error" class="mt-2 text-xs text-muted">{{ text('流读取结束信息（完成终态优先）', 'Read cleanup detail (a completed terminal takes precedence)') }}: {{ result.read_error }}</p><pre class="mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-words text-xs">{{ result.raw_response }}</pre></details>
      </article>
      <p class="text-xs text-muted">{{ text('这些结果用于确认链路和本次回答，不证明模型的真实身份或智力水平。WS 仅在账号已经启用时可用。', 'These results establish transport behaviour and this answer, not physical model identity or intelligence. WS requires an already enabled account.') }}</p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { codexGatewayBorrowAPI, type CodexBorrowDiagnosticEvent, type CodexGatewayBorrowStatus } from '@/api/admin/codexGatewayBorrow'
const props = defineProps<{ status: CodexGatewayBorrowStatus; accountName: (id: number) => string }>()
const emit = defineEmits<{ refresh: [] }>()
const { locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('en') ? en : zh
const pairs = computed(() => props.status.config.target_account_ids.flatMap(account_id => props.status.config.models.map(model => ({ account_id, model, key: `${account_id}/${model}` }))))
const selection = ref(''), transport = ref<'http' | 'ws'>('http'), running = ref(false), error = ref(''), phase = ref(''), requests = ref(0)
const events = ref<CodexBorrowDiagnosticEvent[]>([])
const selected = computed(() => pairs.value.find(pair => pair.key === selection.value))
const results = computed(() => events.value.flatMap(event => event.result ? [event.result] : []))
watch(pairs, value => { if (!value.some(pair => pair.key === selection.value)) selection.value = value[0]?.key || '' }, { immediate: true })
let controller: AbortController | undefined
let disposed = false
function time(value?: string) { return value && !value.startsWith('0001') ? new Date(value).toLocaleString() : '—' }
function outcome(value: string) { return ({ unobserved: text('终态信息未完整记录', 'Terminal evidence not fully recorded'), sent: text('已发送，等待终态', 'Sent, awaiting completion'), completed: text('已完成', 'Completed'), incomplete: text('未收到完整终态', 'No complete terminal event'), upstream_error: text('上游返回错误', 'Upstream error'), transport_error: text('连接失败或中断', 'Connection failed or interrupted') } as Record<string, string>)[value] || value }
function cancel() { controller?.abort() }
async function diagnose() {
  if (!selected.value || running.value) return
  controller = new AbortController(); running.value = true; error.value = ''; events.value = []; requests.value = 0; phase.value = 'ordinary'
  try {
    await codexGatewayBorrowAPI.diagnose({ account_id: selected.value.account_id, model: selected.value.model, transport: transport.value }, event => {
      events.value.push(event); requests.value = event.requests
      if (event.mode) phase.value = event.mode
    }, controller.signal)
  } catch (value) { if (!disposed) error.value = controller.signal.aborted ? text('已停止。已发出的请求可能已消耗额度。', 'Stopped. Dispatched requests may have consumed quota.') : value instanceof Error ? value.message : String(value) }
  finally { running.value = false; if (!disposed) emit('refresh') }
}
function download() {
  const url = URL.createObjectURL(new Blob([JSON.stringify(events.value, null, 2)], { type: 'application/json' }))
  const link = document.createElement('a'); link.href = url; link.download = 'codex-diagnostic.json'; link.click(); URL.revokeObjectURL(url)
}
onBeforeUnmount(() => { disposed = true; cancel() })
</script>
