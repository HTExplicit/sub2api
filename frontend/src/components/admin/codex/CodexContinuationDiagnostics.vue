<template>
  <section class="space-y-3" aria-live="polite">
    <h2 class="text-lg font-semibold">{{ text('Codex 续接诊断', 'Codex continuation diagnostics') }}</h2>
    <p v-if="failure" class="text-sm text-red-600">{{ failure }}</p>
    <p v-else-if="!records.length" class="text-sm text-muted">{{ text('当前授权范围内没有可用的续接结构记录。', 'No continuation records are available in the current scope.') }}</p>
    <div v-for="(record, index) in records" :key="index" class="space-y-2 border border-line p-3 text-sm">
      <h3 class="font-medium">{{ text(`账号 ${record.account_id} · 第 ${record.attempt} 次尝试`, `Account ${record.account_id} · attempt ${record.attempt}`) }}</h3>
      <p v-for="line in explain(record.diagnostic || {})" :key="line">{{ line }}</p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { codexRuntimeAPI } from '@/api/admin/codexRuntime'
import { useAuthStore } from '@/stores/auth'

interface Signal { kind?: string; sha256?: string; value?: string }
interface ContextFacts {
  instructions?: Signal; session?: Signal; previous_response?: Signal; conversation?: Signal
  inspection_limited?: boolean
  history?: Record<string, number | boolean | undefined>
}
interface Diagnostic {
  classification?: string
  incoming?: ContextFacts
  wire?: ContextFacts
  upstream_error?: { error_code?: Signal; inspection_limited?: boolean }
  recovery?: { retry_attempted?: boolean; not_attempted_reason?: string }
}
interface DiagnosticRecord { account_id: number; attempt: number; diagnostic?: Diagnostic }

const props = defineProps<{ errorId: number }>()
const { locale } = useI18n()
const auth = useAuthStore()
const text = (zh: string, en: string) => locale.value.startsWith('en') ? en : zh
const records = ref<DiagnosticRecord[]>([]), failure = ref('')
const known = (value?: Signal) => !!value && !['missing', 'uninspected', 'null', 'unknown'].includes(value.kind || '')
const changed = (before?: Signal, after?: Signal) => known(before) && known(after) && before?.sha256 && after?.sha256 && before.sha256 !== after.sha256
function explain(diagnostic: Diagnostic): string[] {
  const lines: string[] = [], incoming = diagnostic.incoming || {}, wire = diagnostic.wire || {}, history = wire.history || {}, error = diagnostic.upstream_error || {}, recovery = diagnostic.recovery || {}
  const code = error.error_code?.value || diagnostic.classification
  if (['invalid_encrypted_content', 'thinking_signature_invalid'].includes(code || '')) lines.push(text('上游拒绝了推理状态签名；这条记录本身不能证明账号失效或模型质量变化。', 'The upstream rejected a reasoning signature. This record alone does not establish account failure or model quality.'))
  else if (code === 'previous_response_not_found') lines.push(text('上游未找到引用的响应。检查此前响应与本次请求是否使用相同来源和账号。', 'The referenced response was not found. Check that both requests used the same source and account.'))
  if (known(wire.previous_response) || known(wire.conversation)) lines.push(text('请求依赖服务端保存的上下文；本地无法证明它包含完整历史。', 'The request depends on server-held context; local records cannot establish complete history.'))
  if (history.missing_call_ids || history.missing_output_ids || history.duplicate_call_ids || history.duplicate_output_ids || history.unmatched_outputs || history.unpaired_calls) lines.push(text('工具调用与结果的关联不完整或不唯一，不能据此安全重写历史。', 'Tool calls and results are incomplete or ambiguous; the history cannot be safely rewritten from these facts.'))
  if (changed(incoming.instructions, wire.instructions)) lines.push(text('入口与实际出站的指令摘要不同；这里仅记录变化，无法单凭摘要判断原因。', 'Instruction digests differ between entry and dispatch. The digests alone do not explain why.'))
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
  if (!lines.length) lines.push(text('现有结构事实不足以确定续接失败原因。', 'Available structural facts do not establish the cause of continuation failure.'))
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
    if (current === sequence) records.value = Array.isArray(value) ? value.slice(0, 128) : []
  } catch {
    if (current === sequence) failure.value = text('无法读取当前权限下的诊断记录。', 'Unable to read diagnostics in the current scope.')
  }
}, { immediate: true })
onBeforeUnmount(() => { sequence++; controller?.abort() })
</script>
