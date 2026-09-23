<template>
  <section class="space-y-3 border-t border-gray-100 px-4 py-4 dark:border-dark-700 sm:px-5" data-test="codex-fingerprint-panel">
    <div class="flex items-center justify-between gap-3">
      <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ txt('Codex 出站指纹', 'Codex outbound identity') }}</h3>
      <button type="button" class="btn btn-sm" :disabled="loading" @click="load">{{ txt('刷新状态', 'Refresh status') }}</button>
    </div>
    <p class="text-xs text-gray-500">{{ txt('仅查看已记录的请求，不会触发模型调用或采集。', 'Reads recorded requests without starting a model request or acquisition.') }}</p>
    <p v-if="error" role="status" class="text-xs text-amber-700 dark:text-amber-300">{{ error }}</p>
    <template v-if="view">
      <dl class="grid grid-cols-[7rem_minmax(0,1fr)] gap-x-3 gap-y-2 break-words text-xs">
        <dt>{{ txt('配置来源', 'Configured source') }}</dt><dd>{{ view.configured.identity_source }}</dd>
        <dt>{{ txt('手动 UA 覆盖', 'Explicit UA override') }}</dt><dd>{{ view.configured.ua_override_present ? txt('已配置', 'Configured') : txt('未配置', 'Not configured') }}</dd>
        <dt>{{ txt('应用配置来源', 'Profile source') }}</dt><dd>{{ view.configured.profile_source }} (v{{ view.configured.profile_schema }})</dd>
        <template v-if="view.configured_profile"><dt>{{ txt('系统 / 架构', 'OS / architecture') }}</dt><dd>{{ view.configured_profile.os_type }} {{ view.configured_profile.os_version }} / {{ view.configured_profile.arch }}</dd><dt>{{ txt('终端 / 沙箱', 'Terminal / sandbox') }}</dt><dd>{{ view.configured_profile.terminal }} / {{ view.configured_profile.sandbox }}</dd></template>
        <dt>User-Agent</dt><dd class="font-mono">{{ view.configured.user_agent }}</dd>
        <dt>Originator / version</dt><dd>{{ view.configured.originator }} / {{ view.configured.version }}</dd>
        <dt>{{ txt('身份收敛', 'Identity mode') }}</dt><dd>{{ view.configured.fingerprint_mode_effective }}</dd>
        <dt>{{ txt('Cookie 上限', 'Cookie ceiling') }}</dt><dd>{{ view.cookie_max_age_seconds }}s · {{ txt('遵循更早的上游到期时间', 'Earlier upstream expiry takes precedence') }}</dd>
      </dl>
      <div class="flex flex-wrap items-center gap-2 text-xs">
        <label>{{ txt('选择应用指纹', 'Application profile') }}
          <select v-model="profileChoice" class="input input-sm ml-2">
            <option value="keep_legacy">{{ txt('保留当前配置', 'Keep current profile') }}</option>
            <option value="captured_windows_cli">{{ txt('官方 Windows CLI 参照 0.156.0', 'Official Windows CLI reference 0.156.0') }}</option>
          </select>
        </label>
        <button type="button" class="btn btn-sm" :disabled="loading || profileChoice === 'keep_legacy'" @click="selectProfile">{{ txt('应用选择', 'Apply selection') }}</button>
        <p>{{ txt('选择参照保留设备种子和手动 UA 覆盖；已有 Cookie 路由资格需要重新验证。', 'Selection preserves the device seed and explicit UA override. Existing Cookie route qualifications need revalidation.') }}</p>
      </div>
      <div class="space-y-2 border-t border-gray-100 pt-3 dark:border-dark-700">
        <h4 class="text-xs font-semibold">{{ txt('最近实际出站', 'Last observed outbound request') }}</h4>
        <p v-if="!view.observed" class="text-xs text-gray-500">{{ txt('暂无最终请求观测；配置值不代表已发送。', 'No final request observation. Configuration does not prove transmission.') }}</p>
        <dl v-else class="grid grid-cols-[7rem_minmax(0,1fr)] gap-x-3 gap-y-2 break-words text-xs">
          <dt>{{ txt('观测时间', 'Observed at') }}</dt><dd>{{ view.observed.observed_at }}</dd>
          <dt>{{ txt('入站 → 上游', 'Ingress → upstream') }}</dt><dd>{{ view.observed.ingress }} → {{ view.observed.transport }} ({{ view.observed.http_protocol }})</dd>
          <dt>User-Agent</dt><dd class="font-mono">{{ view.observed.user_agent }}</dd>
          <dt>Originator / version</dt><dd>{{ view.observed.originator || '—' }} / {{ view.observed.version || '—' }}</dd>
          <dt>{{ txt('请求压缩', 'Request encoding') }}</dt><dd>{{ view.observed.request_encoding || txt('无', 'None') }}</dd>
          <dt>TLS / ALPN</dt><dd>{{ view.observed.tls_implementation }} · {{ view.observed.tls_version || txt('未知', 'Unknown') }} · {{ view.observed.alpn || '—' }}</dd>
          <dt>JA3 / JA4</dt><dd>{{ txt('未测得，不能用预设代替', 'Not captured; a configured profile is not a measurement') }}</dd>
          <dt>Cookie</dt><dd>{{ view.observed.cookie_names.join(', ') || txt('未携带', 'Not sent') }}</dd>
          <template v-for="(version, name) in view.observed.cookie_versions" :key="name"><dt>{{ name }} {{ txt('版本摘要', 'version digest') }}</dt><dd class="font-mono">{{ version }}</dd></template>
          <dt>{{ txt('连接证据', 'Connection evidence') }}</dt><dd class="font-mono">{{ view.observed.connection_evidence || '—' }}</dd>
          <template v-for="(field, name) in view.observed.identity_fields || {}" :key="name"><dt>{{ name }}</dt><dd class="space-y-1"><span>{{ consistencyLabel(field.consistency) }}</span><div class="break-all font-mono text-[10px]">H {{ field.header_digest || '—' }} · B {{ field.body_digest || '—' }}</div></dd></template>
        </dl>
        <p v-if="view.observed" class="text-xs text-gray-500">{{ txt('H 为最终请求头摘要，B 为请求体身份摘要。缺失或未检查不表示匹配；摘要用于比对稳定性，不展示原标识。', 'H is the final header digest; B is the body identity digest. Missing or uninspected does not mean matched. Digests compare stability without revealing raw identifiers.') }}</p>
      </div>
      <div v-for="model in view.models" :key="model.model" class="space-y-1 border-t border-gray-100 pt-3 text-xs dark:border-dark-700">
        <div class="flex flex-wrap justify-between gap-2"><strong>{{ model.model }}</strong><span>{{ model.qualified ? txt('路由已验证', 'Route verified') : phaseLabel(model.phase) }}</span></div>
        <p>{{ txt('连续失败周期', 'Failed cycles') }}: {{ model.failed_cycles }} / 2 · {{ model.enrolled ? txt('已登记按需续获', 'Enrolled for demand renewal') : txt('未登记自动续获', 'Not enrolled') }}</p>
        <p v-if="model.expires_at">{{ txt('有效期至', 'Expires') }}: {{ model.expires_at }}</p>
        <p v-if="model.verified_at">{{ txt('业务出口验证时间', 'Business route verified at') }}: {{ model.verified_at }}</p>
        <p v-if="model.last_observation">{{ model.last_observation.code }} · HTTP {{ model.last_observation.http_status || '—' }} · {{ txt('返回模型', 'Returned model') }}: {{ model.last_observation.response_model || txt('未知', 'Unknown') }} · {{ txt('完整结束', 'Completed') }}: {{ model.last_observation.completed ? '✓' : '—' }}</p>
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

type Observation = { code: string; http_status?: number; response_model?: string; completed: boolean }
type FingerprintView = {
  configured: { identity_source: string; user_agent: string; originator: string; version: string; fingerprint_mode_effective: string; account_revision: string; profile_source: string; profile_schema: number; ua_override_present?: boolean }
  cookie_max_age_seconds: number
  configured_profile?: { os_type: string; os_version: string; arch: string; terminal: string; sandbox: string }
  observed?: { observed_at: string; ingress: string; transport: string; http_protocol: string; user_agent: string; originator?: string; version?: string; request_encoding: string; tls_implementation: string; tls_version?: string; alpn?: string; cookie_names: string[]; cookie_versions?: Record<string, string>; connection_evidence: string; identity_fields?: Record<string, { header_digest?: string; body_digest?: string; consistency: string }> }
  models: { model: string; phase: string; enrolled: boolean; failed_cycles: number; qualified: boolean; expires_at?: string; verified_at?: string; last_observation?: Observation }[]
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
function phaseLabel(phase: string) {
  const labels: Record<string, [string, string]> = { ready: ['等待有效观测', 'Awaiting valid observation'], stopped: ['已停止', 'Stopped'], retry: ['等待有界重试', 'Awaiting bounded retry'], needs_cookie_verification: ['需要业务出口复验', 'Business route verification needed'], idle: ['尚未采集', 'Not acquired'] }
  const label = labels[phase]
  return label ? txt(...label) : phase
}
async function load() {
  const current = ++generation
  loading.value = true; error.value = ''
  try {
    const { data } = await apiClient.get<FingerprintView>(`/admin/accounts/${props.accountId}/codex-fingerprint`)
    if (current === generation) view.value = data
  } catch {
    if (current === generation) error.value = txt('暂时无法读取指纹状态。', 'Fingerprint status is unavailable.')
  } finally { if (current === generation) loading.value = false }
}
async function selectProfile() {
  if (!view.value || profileChoice.value !== 'captured_windows_cli') return
  const current = generation
  loading.value = true; error.value = ''
  try {
    await apiClient.put(`/admin/accounts/${props.accountId}/codex-fingerprint/profile`, { profile: profileChoice.value, expected_revision: view.value.configured.account_revision })
    if (current === generation) { profileChoice.value = 'keep_legacy'; await load() }
  } catch { if (current === generation) error.value = txt('应用失败，账号可能已更新，请刷新后再试。', 'Could not apply; the account may have changed. Refresh before retrying.') }
  finally { if (current === generation) loading.value = false }
}
watch(() => props.accountId, () => { view.value = null; void load() }, { immediate: true })
onBeforeUnmount(() => { generation++ })
</script>
