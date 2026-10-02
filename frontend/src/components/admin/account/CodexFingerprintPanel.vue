<template>
  <section class="space-y-3 border-t border-gray-100 px-4 py-4 dark:border-dark-700 sm:px-5" data-test="codex-fingerprint-panel">
    <div class="flex items-center justify-between gap-3">
      <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ txt('Codex 出站指纹', 'Codex outbound identity') }}</h3>
      <button type="button" class="btn btn-sm" :disabled="loading" @click="load">{{ txt('刷新状态', 'Refresh status') }}</button>
    </div>
    <p class="text-xs text-gray-500">{{ txt('仅查看已记录的请求，不会触发模型调用。', 'Reads recorded requests without starting a model request.') }}</p>
    <p v-if="error" role="status" data-test="codex-fingerprint-error" class="break-words text-xs text-amber-700 dark:text-amber-300">{{ error }}</p>
    <template v-if="view">
      <dl class="grid grid-cols-[7rem_minmax(0,1fr)] gap-x-3 gap-y-2 break-words text-xs">
        <dt>{{ txt('配置来源', 'Configured source') }}</dt><dd>{{ view.configured.identity_source }}</dd>
        <dt>{{ txt('手动 UA 覆盖', 'Explicit UA override') }}</dt><dd>{{ view.configured.ua_override_present ? txt('已配置', 'Configured') : txt('未配置', 'Not configured') }}</dd>
        <dt>{{ txt('应用配置来源', 'Profile source') }}</dt><dd>{{ view.configured.profile_source }} (v{{ view.configured.profile_schema }})</dd>
        <template v-if="view.configured_profile"><dt>{{ txt('系统 / 架构', 'OS / architecture') }}</dt><dd>{{ view.configured_profile.os_type }} {{ view.configured_profile.os_version }} / {{ view.configured_profile.arch }}</dd><dt>{{ txt('终端 / 沙箱', 'Terminal / sandbox') }}</dt><dd>{{ view.configured_profile.terminal }} / {{ view.configured_profile.sandbox }}</dd></template>
        <dt>User-Agent</dt><dd class="font-mono">{{ view.configured.user_agent }}</dd>
        <dt>Originator / version</dt><dd>{{ view.configured.originator }} / {{ view.configured.version }}</dd>
        <dt>{{ txt('身份收敛', 'Identity mode') }}</dt><dd>{{ view.configured.fingerprint_mode_effective }}</dd>
      </dl>
      <div data-ui="dense-btn" class="flex flex-wrap items-center gap-2 text-xs">
        <label>{{ txt('选择应用指纹', 'Application profile') }}
          <select v-model="profileChoice" class="input input-sm ml-2">
            <option value="keep_legacy">{{ txt('保留当前配置', 'Keep current profile') }}</option>
            <option value="captured_windows_cli">{{ txt('官方 Windows CLI 参照 0.156.0', 'Official Windows CLI reference 0.156.0') }}</option>
          </select>
        </label>
        <button type="button" class="btn btn-sm" :disabled="loading || profileChoice === 'keep_legacy'" @click="selectProfile">{{ txt('应用选择', 'Apply selection') }}</button>
        <p>{{ txt('选择参照保留设备种子和手动 UA 覆盖。', 'Selection preserves the device seed and explicit UA override.') }}</p>
      </div>
      <div class="space-y-2 border-t border-gray-100 pt-3 dark:border-dark-700">
        <h4 class="text-xs font-semibold">{{ txt('最近实际出站', 'Last observed outbound request') }}</h4>
        <p v-if="view.observed_error" class="text-xs text-red-600">{{ view.observed_error }}</p>
        <p v-if="!view.observed" class="text-xs text-gray-500">{{ txt('暂无最终请求观测；配置值不代表已发送。', 'No final request observation. Configuration does not prove transmission.') }}</p>
        <dl v-else class="grid grid-cols-[7rem_minmax(0,1fr)] gap-x-3 gap-y-2 break-words text-xs">
          <dt>{{ txt('观测时间', 'Observed at') }}</dt><dd>{{ view.observed.observed_at }}</dd>
          <dt>{{ txt('入站 → 上游', 'Ingress → upstream') }}</dt><dd>{{ view.observed.ingress }} → {{ view.observed.transport }} ({{ view.observed.http_protocol }})</dd>
          <dt>User-Agent</dt><dd class="font-mono">{{ view.observed.user_agent }}</dd>
          <dt>Originator / version</dt><dd>{{ view.observed.originator || '—' }} / {{ view.observed.version || '—' }}</dd>
          <dt>{{ txt('请求压缩', 'Request encoding') }}</dt><dd>{{ view.observed.request_encoding || txt('无', 'None') }}</dd>
          <dt>TLS / ALPN</dt><dd>{{ view.observed.tls_implementation }} · {{ view.observed.tls_version || txt('未知', 'Unknown') }} · {{ view.observed.alpn || '—' }}</dd>
          <dt>JA3 / JA4</dt><dd>{{ txt('未测得，不能用预设代替', 'Not captured; a configured profile is not a measurement') }}</dd>
          <dt>{{ txt('账号代理', 'Account proxy') }}</dt><dd>{{ view.observed.account_proxy_id ? '#' + view.observed.account_proxy_id : txt('直连', 'Direct') }}</dd>
          <dt>STATE</dt>
          <dd data-test="codex-fingerprint-state" class="break-all font-mono text-[10px]">{{ view.observed.state || (view.observed.state_present ? txt(`已发送 ${view.observed.state_length} 字节`, `Sent, ${view.observed.state_length} bytes`) : txt('未携带', 'Not sent')) }}</dd>
          <template v-for="(field, name) in view.observed.identity_fields || {}" :key="name"><dt>{{ name }}</dt><dd class="space-y-1"><span>{{ consistencyLabel(field.consistency) }}</span><div class="break-all font-mono text-[10px]">H {{ field.header_value || '—' }} · B {{ field.body_value || '—' }}</div><div class="break-all font-mono text-[10px] text-gray-500">H {{ field.header_digest || '—' }} · B {{ field.body_digest || '—' }}</div></dd></template>
        </dl>
        <p v-if="view.observed" class="text-xs text-gray-500">{{ txt('H 为最终请求头，B 为请求体身份字段；第二行是对应摘要。缺失或未检查不表示匹配。', 'H is the final request header and B the body identity field; the second line shows their digests. Missing or uninspected does not mean matched.') }}</p>
      </div>
      <details class="text-xs text-gray-500">
        <summary>{{ txt('官方客户端参照与限制', 'Official client reference and limits') }}</summary>
        <p class="mt-2">{{ txt('参照来自本地隔离的官方 CLI HTTP / WS / TLS 抓取，不包含真实模型请求。当前账号配置不等于该捕获设备；生产 Go TLS 与参照是不同实现，尚未证明一致。', 'Reference captured from official CLI HTTP / WS / TLS on an isolated loopback fixture with no real model request. The configured account is not the captured device. Production Go TLS is a different implementation; equality is unverified.') }}</p>
        <p class="mt-2 break-all font-mono">{{ view.captured_reference.client_version }} · {{ view.captured_reference.user_agent }}</p>
        <dl class="mt-2 grid grid-cols-[7rem_minmax(0,1fr)] gap-2 break-all">
          <dt>{{ txt('采样时间', 'Captured at') }}</dt><dd>{{ view.captured_reference.captured_at || txt('未知', 'Unknown') }}</dd>
          <dt>{{ txt('二进制 SHA256', 'Binary SHA256') }}</dt><dd class="font-mono">{{ view.captured_reference.binary_sha256 || txt('未知', 'Unknown') }}</dd>
          <dt>{{ txt('采样范围', 'Capture scope') }}</dt><dd>{{ view.captured_reference.transport_scope || txt('未知', 'Unknown') }}</dd>
        </dl>
        <details v-if="view.captured_reference.responses?.length" class="mt-2">
          <summary>{{ txt('查看 ClientHello / ALPN 原始参照', 'Inspect captured ClientHello / ALPN reference') }}</summary>
          <div v-for="(sample, index) in view.captured_reference.responses" :key="index" class="mt-2">
            <p>{{ sample.method === 'GET' ? 'WebSocket' : 'HTTP' }} · {{ txt('本地受控端点', 'Controlled loopback endpoint') }}</p>
            <pre class="mt-1 max-h-64 overflow-auto whitespace-pre-wrap break-all rounded bg-gray-50 p-2 dark:bg-dark-800">{{ JSON.stringify(sample.transport, null, 2) }}</pre>
          </div>
        </details>
      </details>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { apiClient } from '@/api/client'
import { extractApiErrorMessage } from '@/utils/apiError'

type IdentityField = { header_value?: string; body_value?: string; header_digest?: string; body_digest?: string; consistency: string }
type FingerprintView = {
  configured: { identity_source: string; user_agent: string; originator: string; version: string; fingerprint_mode_effective: string; account_revision: string; profile_source: string; profile_schema: number; ua_override_present?: boolean }
  configured_profile?: { os_type: string; os_version: string; arch: string; terminal: string; sandbox: string }
  observed_error?: string
  observed?: { observed_at: string; ingress: string; transport: string; http_protocol: string; user_agent: string; originator?: string; version?: string; request_encoding: string; tls_implementation: string; tls_version?: string; alpn?: string; state?: string; state_present?: boolean; state_length?: number; account_proxy_id?: number; identity_fields?: Record<string, IdentityField> }
  captured_reference: { client_version: string; user_agent: string; captured_at?: string; binary_sha256?: string; transport_scope?: string; responses?: { method: string; transport: Record<string, unknown> }[] }
}
const props = defineProps<{ accountId: number }>()
const { locale } = useI18n()
const english = computed(() => locale.value.startsWith('en'))
const txt = (zh: string, en: string) => english.value ? en : zh
const view = ref<FingerprintView | null>(null)
const loading = ref(false)
const error = ref('')
const profileChoice = ref('keep_legacy')
let generation = 0
function consistencyLabel(value: string) {
  const values: Record<string, [string, string]> = { match: ['头体一致', 'Header/body match'], mismatch: ['头体不一致', 'Header/body mismatch'], missing: ['缺少对应字段', 'Corresponding field missing'], uninspected: ['未检查', 'Uninspected'] }
  return values[value] ? txt(...values[value]) : txt('未知', 'Unknown')
}
async function load() {
  const current = ++generation
  loading.value = true; error.value = ''
  try {
    const { data } = await apiClient.get<FingerprintView>(`/admin/accounts/${props.accountId}/codex-fingerprint`)
    if (current === generation) view.value = data
  } catch (cause) {
    if (current === generation) error.value = extractApiErrorMessage(cause, txt('暂时无法读取指纹状态。', 'Fingerprint status is unavailable.'))
  } finally { if (current === generation) loading.value = false }
}
async function selectProfile() {
  if (!view.value || profileChoice.value !== 'captured_windows_cli') return
  const current = generation
  loading.value = true; error.value = ''
  try {
    await apiClient.put(`/admin/accounts/${props.accountId}/codex-fingerprint/profile`, { profile: profileChoice.value, expected_revision: view.value.configured.account_revision })
    if (current === generation) { profileChoice.value = 'keep_legacy'; await load() }
  } catch (cause) { if (current === generation) error.value = extractApiErrorMessage(cause, txt('应用失败，账号可能已更新，请刷新后再试。', 'Could not apply; the account may have changed. Refresh before retrying.')) }
  finally { if (current === generation) loading.value = false }
}
watch(() => props.accountId, () => { view.value = null; void load() }, { immediate: true })
onBeforeUnmount(() => { generation++ })
</script>
