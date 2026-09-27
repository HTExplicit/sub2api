<template>
  <section class="space-y-3" aria-live="polite">
    <h2 class="text-lg font-semibold">{{ text('Codex 续接诊断', 'Codex continuation diagnostics') }}</h2>
    <p v-if="failure" class="text-sm text-red-600">{{ failure }}</p>
    <p v-else-if="!records.length" class="text-sm text-muted">{{ text('这条错误没有续接诊断记录。', 'This error has no continuation diagnostic records.') }}</p>
    <div v-for="(record, index) in records" :key="index" class="space-y-3 border border-line p-3 text-sm" data-test="codex-diagnostic-record">
      <h3 class="font-medium">{{ recordTitle(record) }}</h3>
      <p v-if="record.message" class="whitespace-pre-wrap break-words">{{ record.message }}</p>
      <p v-for="line in explain(record.diagnostic || {})" :key="line">{{ line }}</p>

      <div class="space-y-1">
        <h4 class="font-medium">{{ text('上游错误', 'Upstream error') }}</h4>
        <dl class="grid grid-cols-[max-content_minmax(0,1fr)] gap-x-3 gap-y-1">
          <template v-for="row in errorRows(record.diagnostic || {})" :key="row.label">
            <dt class="text-muted">{{ row.label }}</dt>
            <dd class="min-w-0">
              <span class="whitespace-pre-wrap break-all font-mono">{{ signalValue(row.signal) }}</span>
              <span class="ml-2 text-xs text-muted" :title="row.signal?.sha256 || ''">{{ signalMeta(row.signal) }}</span>
            </dd>
          </template>
        </dl>
        <p v-if="hints(record.diagnostic).length" class="text-xs text-muted">{{ text('命中的提示：', 'Matched hints: ') }}{{ hints(record.diagnostic).join(', ') }}</p>
      </div>

      <div class="space-y-1">
        <h4 class="font-medium">{{ text('请求上下文', 'Request context') }}</h4>
        <div class="overflow-x-auto">
          <table class="w-full table-fixed border-collapse text-left">
            <thead>
              <tr class="text-muted">
                <th class="w-52 py-1 pr-3 font-normal">{{ text('字段', 'Field') }}</th>
                <th class="py-1 pr-3 font-normal">{{ text('入口', 'At entry') }}</th>
                <th class="py-1 font-normal">{{ text('实际出站', 'Dispatched') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="field in contextFields" :key="field.key" class="border-t border-line align-top">
                <td class="break-all py-1 pr-3 font-mono text-xs">{{ field.label }}</td>
                <td v-for="side in sides(record.diagnostic)" :key="side.name" class="min-w-0 py-1 pr-3">
                  <pre v-if="field.key === 'instructions' && side.facts[field.key]?.value" class="max-h-48 overflow-auto whitespace-pre-wrap break-words font-mono text-xs">{{ side.facts[field.key]?.value }}</pre>
                  <span v-else class="whitespace-pre-wrap break-all font-mono text-xs">{{ signalValue(side.facts[field.key]) }}</span>
                  <span class="block text-xs text-muted" :title="side.facts[field.key]?.sha256 || ''">{{ signalMeta(side.facts[field.key]) }}</span>
                </td>
              </tr>
              <tr class="border-t border-line align-top">
                <td class="py-1 pr-3 font-mono text-xs">body</td>
                <td v-for="side in sides(record.diagnostic)" :key="side.name" class="py-1 pr-3 text-xs">{{ bodySummary(side.facts) }}</td>
              </tr>
              <tr class="border-t border-line align-top">
                <td class="py-1 pr-3 font-mono text-xs">history</td>
                <td v-for="side in sides(record.diagnostic)" :key="side.name" class="py-1 pr-3 text-xs">{{ historySummary(side.facts.history) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <p v-if="record.diagnostic?.recovery" class="text-xs text-muted">{{ recoverySummary(record.diagnostic.recovery) }}</p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { codexRuntimeAPI } from '@/api/admin/codexRuntime'
import { useAuthStore } from '@/stores/auth'

interface Signal { kind?: string; bytes?: number; characters?: number; sha256?: string; value?: string; truncated?: boolean }
type ContextKey = 'prompt_cache' | 'previous_response' | 'session' | 'conversation' | 'client_metadata_session' | 'instructions'
type ContextFacts = Partial<Record<ContextKey, Signal>> & {
  body_bytes?: number
  body_source?: string
  inspection_limited?: boolean
  history?: Record<string, number | boolean | string | undefined>
}
interface UpstreamError {
  error_type?: Signal; error_code?: Signal; error_param?: Signal; message?: Signal
  hints?: string[]; inspection_limited?: boolean
}
interface Recovery { cache_skipped_items?: number; retry_attempted?: boolean; disposition?: string; not_attempted_reason?: string }
interface Diagnostic {
  classification?: string
  incoming?: ContextFacts
  wire?: ContextFacts
  upstream_error?: UpstreamError
  recovery?: Recovery
}
interface DiagnosticRecord {
  account_id: number; account_name?: string; platform?: string; attempt: number
  kind?: string; upstream_status_code?: number; upstream_request_id?: string; message?: string; at_unix_ms?: number
  diagnostic?: Diagnostic
}

const props = defineProps<{ errorId: number }>()
const { locale } = useI18n()
const auth = useAuthStore()
const text = (zh: string, en: string) => locale.value.startsWith('en') ? en : zh
const records = ref<DiagnosticRecord[]>([]), failure = ref('')
const contextFields: Array<{ key: ContextKey; label: string }> = [
  { key: 'prompt_cache', label: 'prompt_cache_key' },
  { key: 'previous_response', label: 'previous_response_id' },
  { key: 'session', label: 'session_id' },
  { key: 'conversation', label: 'conversation_id' },
  { key: 'client_metadata_session', label: 'client_metadata.session_id' },
  { key: 'instructions', label: 'instructions' }
]

function recordTitle(record: DiagnosticRecord): string {
  const parts = [text(`账号 ${record.account_id}`, `Account ${record.account_id}`) + (record.account_name ? ` (${record.account_name})` : '')]
  parts.push(text(`第 ${record.attempt} 次尝试`, `attempt ${record.attempt}`))
  if (record.kind) parts.push(record.kind)
  if (record.upstream_status_code) parts.push(`HTTP ${record.upstream_status_code}`)
  if (record.upstream_request_id) parts.push(record.upstream_request_id)
  if (record.at_unix_ms) parts.push(new Date(record.at_unix_ms).toLocaleString())
  return parts.join(' · ')
}
function errorRows(diagnostic: Diagnostic): Array<{ label: string; signal?: Signal }> {
  const error = diagnostic.upstream_error || {}
  return [
    { label: text('类型', 'Type'), signal: error.error_type },
    { label: text('代码', 'Code'), signal: error.error_code },
    { label: text('参数', 'Param'), signal: error.error_param },
    { label: text('消息', 'Message'), signal: error.message }
  ]
}
function hints(diagnostic?: Diagnostic): string[] {
  return diagnostic?.upstream_error?.hints || []
}
function sides(diagnostic?: Diagnostic): Array<{ name: string; facts: ContextFacts }> {
  return [{ name: 'incoming', facts: diagnostic?.incoming || {} }, { name: 'wire', facts: diagnostic?.wire || {} }]
}
function signalValue(signal?: Signal): string {
  if (!signal || signal.kind === 'missing') return text('（无）', '(missing)')
  if (signal.kind === 'uninspected') return text('（未检查）', '(not inspected)')
  if (signal.kind === 'null') return 'null'
  return signal.value ?? ''
}
function signalMeta(signal?: Signal): string {
  if (!signal || ['missing', 'uninspected', 'null'].includes(signal.kind || '')) return ''
  const parts = [signal.kind || '', text(`${signal.bytes ?? 0} 字节`, `${signal.bytes ?? 0} bytes`)]
  if (signal.sha256) parts.push(`sha256 ${signal.sha256.slice(0, 12)}…`)
  if (signal.truncated) parts.push(text('已截断，指纹对应完整值', 'truncated; fingerprint covers the full value'))
  return parts.filter(Boolean).join(' · ')
}
function bodySummary(facts: ContextFacts): string {
  const parts = [text(`${facts.body_bytes ?? 0} 字节`, `${facts.body_bytes ?? 0} bytes`)]
  if (facts.body_source) parts.push(facts.body_source)
  if (facts.inspection_limited) parts.push(text('检查受限', 'inspection limited'))
  return parts.join(' · ')
}
function historySummary(history?: ContextFacts['history']): string {
  if (!history) return ''
  return Object.entries(history)
    .filter(([, value]) => value !== undefined && value !== 0 && value !== false && value !== '')
    .map(([key, value]) => (typeof value === 'string' && value.length > 16 ? `${key}=${value.slice(0, 12)}…` : `${key}=${value}`))
    .join(' · ') || text('无历史项', 'no history items')
}
function recoverySummary(recovery: Recovery): string {
  const parts = [`disposition=${recovery.disposition || ''}`, `retry_attempted=${!!recovery.retry_attempted}`]
  if (recovery.not_attempted_reason) parts.push(`not_attempted_reason=${recovery.not_attempted_reason}`)
  if (recovery.cache_skipped_items) parts.push(`cache_skipped_items=${recovery.cache_skipped_items}`)
  return text('推理签名恢复：', 'Reasoning recovery: ') + parts.join(' · ')
}

const known = (value?: Signal) => !!value && !['missing', 'uninspected', 'null', 'unknown'].includes(value.kind || '')
const changed = (before?: Signal, after?: Signal) => known(before) && known(after) && before?.sha256 && after?.sha256 && before.sha256 !== after.sha256
function explain(diagnostic: Diagnostic): string[] {
  const lines: string[] = [], incoming = diagnostic.incoming || {}, wire = diagnostic.wire || {}, history = wire.history || {}, error = diagnostic.upstream_error || {}, recovery = diagnostic.recovery || {}
  const code = error.error_code?.value || diagnostic.classification
  if (['invalid_encrypted_content', 'thinking_signature_invalid'].includes(code || '')) lines.push(text('上游拒绝了推理状态签名；这条记录本身不能证明账号失效或模型质量变化。', 'The upstream rejected a reasoning signature. This record alone does not establish account failure or model quality.'))
  else if (code === 'previous_response_not_found') lines.push(text('上游未找到引用的响应。检查此前响应与本次请求是否使用相同来源和账号。', 'The referenced response was not found. Check that both requests used the same source and account.'))
  if (known(wire.previous_response) || known(wire.conversation)) lines.push(text('请求依赖服务端保存的上下文；本地无法证明它包含完整历史。', 'The request depends on server-held context; local records cannot establish complete history.'))
  if (history.missing_call_ids || history.missing_output_ids || history.duplicate_call_ids || history.duplicate_output_ids || history.unmatched_outputs || history.unpaired_calls) lines.push(text('工具调用与结果的关联不完整或不唯一，不能据此安全重写历史。', 'Tool calls and results are incomplete or ambiguous; the history cannot be safely rewritten from these facts.'))
  if (changed(incoming.instructions, wire.instructions)) lines.push(text('入口与实际出站的指令不同，对照下表的原文。', 'Instructions differ between entry and dispatch; compare the values below.'))
  if (changed(incoming.session, wire.session) || changed(incoming.previous_response, wire.previous_response)) lines.push(text('入口与实际出站的会话或前序响应引用发生了变化。', 'Session or previous-response references changed between entry and dispatch.'))
  if (incoming.inspection_limited || wire.inspection_limited || history.scan_limited || error.inspection_limited) lines.push(text('本次结构检查受大小或可读性限制；未检查部分不能当作不存在。', 'Structural inspection was limited by size or readability; uninspected fields are not evidence of absence.'))
  if (recovery.retry_attempted) lines.push(text('宿主已执行一次受限恢复请求；恢复是否成功须查看后续结果。', 'The host dispatched one bounded recovery request; check the subsequent result for its outcome.'))
  const reasons: Record<string, [string, string]> = {
    disabled: ['恢复能力已关闭或当前账号不在启用范围。', 'Recovery is disabled or outside the enabled account scope.'],
    policy_unavailable: ['恢复能力不可用，未启用替代执行路径。', 'Recovery was unavailable.'],
    semantic_output_committed: ['已向客户端发送业务输出，宿主禁止重放请求。', 'Semantic output was already committed; the host prohibits replay.'],
    source_changed: ['请求来源已变化，宿主禁止跨来源恢复。', 'The source changed; the host prohibits recovery across sources.'],
    request_cancelled: ['请求已取消，未继续恢复。', 'The request was canceled before further recovery.']
  }
  const reason = reasons[recovery.not_attempted_reason || '']
  if (reason) lines.push(text(...reason))
  if (!lines.length) lines.push(text('现有结构事实不足以确定续接失败原因；请对照上游错误原文。', 'Available structural facts do not establish the cause; compare the upstream error text.'))
  return lines
}
let sequence = 0, controller: AbortController | undefined
watch([() => props.errorId, () => auth.user?.id], async ([errorId]) => {
  const current = ++sequence
  controller?.abort(); controller = new AbortController()
  records.value = []; failure.value = ''
  if (!Number.isSafeInteger(errorId) || !errorId || errorId <= 0) return
  try {
    const value = await codexRuntimeAPI.diagnostics(errorId, controller.signal)
    if (current === sequence) records.value = Array.isArray(value) ? value as DiagnosticRecord[] : []
  } catch (error) {
    if (current === sequence) failure.value = text('无法读取诊断记录：', 'Unable to read diagnostics: ') + (error instanceof Error ? error.message : String(error))
  }
}, { immediate: true })
onBeforeUnmount(() => { sequence++; controller?.abort() })
</script>
