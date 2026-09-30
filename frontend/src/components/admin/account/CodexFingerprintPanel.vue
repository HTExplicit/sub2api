<template>
  <section class="space-y-3 border-t border-gray-100 px-4 py-4 dark:border-dark-700 sm:px-5" data-test="codex-fingerprint-panel">
    <div class="flex items-center justify-between gap-3">
      <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ txt('Codex 出站指纹', 'Codex outbound identity') }}</h3>
      <button type="button" class="btn btn-sm" :disabled="loading" @click="load">{{ txt('刷新状态', 'Refresh status') }}</button>
    </div>
    <p class="text-xs text-gray-500">{{ txt('仅查看已记录的请求，不会触发模型调用或采集。', 'Reads recorded requests without starting a model request or acquisition.') }}</p>
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
        <dt>{{ txt('Cookie 上限', 'Cookie ceiling') }}</dt><dd>{{ view.cookie_max_age_seconds }}s · {{ txt('遵循更早的上游到期时间', 'Earlier upstream expiry takes precedence') }}</dd>
      </dl>
      <div class="space-y-2 border-t border-gray-100 pt-3 dark:border-dark-700" data-test="codex-fingerprint-route">
        <h4 class="text-xs font-semibold">{{ txt('路由出口', 'Routing egress') }}</h4>
        <dl class="grid grid-cols-[7rem_minmax(0,1fr)] gap-x-3 gap-y-2 break-words text-xs">
          <dt>{{ txt('路由采集开关', 'Route acquisition') }}</dt><dd>{{ view.routing_enabled ? txt('已开启', 'On') : txt('已关闭', 'Off') }} · fail_closed {{ view.fail_closed ? '✓' : '—' }}</dd>
          <dt>{{ txt('账号代理', 'Account proxy') }}</dt>
          <dd v-if="view.account_proxy">#{{ view.account_proxy.id }} {{ view.account_proxy.name || '' }}<span v-if="view.account_proxy.host"> · {{ view.account_proxy.protocol }}://{{ view.account_proxy.host }}:{{ view.account_proxy.port }}</span><span v-if="view.account_proxy.username"> · {{ txt('用户名', 'Username') }} {{ view.account_proxy.username }}</span><span v-if="view.account_proxy.status"> · {{ view.account_proxy.status }}</span></dd>
          <dd v-else>{{ txt('直连（未绑定代理）', 'Direct (no proxy bound)') }}</dd>
          <dt>{{ txt('采集代理', 'Acquisition proxy') }}</dt><dd class="break-all font-mono">{{ view.harvest_proxy_url || '—' }}</dd>
          <template v-if="view.current_scope">
            <dt>RouteHash</dt><dd class="break-all font-mono">{{ view.current_scope.route_hash }}</dd>
            <dt>ProfileHash</dt><dd class="break-all font-mono">{{ view.current_scope.profile_hash }}</dd>
            <dt>{{ txt('账号主体', 'Credential owner') }}</dt><dd class="break-all font-mono">{{ view.current_scope.identity }}</dd>
          </template>
          <template v-if="view.scope_error"><dt>{{ txt('出口范围错误', 'Scope error') }}</dt><dd class="text-red-600">{{ view.scope_error }}</dd></template>
        </dl>
      </div>
      <div data-ui="dense-btn" class="flex flex-wrap items-center gap-2 text-xs">
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
          <dt>Cookie</dt>
          <dd v-if="view.observed.cookies?.length" data-test="codex-fingerprint-cookies" class="space-y-1">
            <div v-for="(cookie, index) in view.observed.cookies" :key="index" class="break-all font-mono text-[10px]">{{ cookie.name }}={{ cookie.value }}<span v-if="cookie.routing" class="ml-1 font-sans text-gray-500">({{ txt('路由', 'routing') }})</span></div>
          </dd>
          <dd v-else>{{ view.observed.cookie_names.join(', ') || txt('未携带', 'Not sent') }}</dd>
          <dt>STATE</dt>
          <dd data-test="codex-fingerprint-state" class="break-all font-mono text-[10px]">{{ view.observed.state || (view.observed.state_present ? txt(`已发送 ${view.observed.state_length} 字节`, `Sent, ${view.observed.state_length} bytes`) : txt('未携带', 'Not sent')) }}</dd>
          <template v-for="(version, name) in view.observed.cookie_versions" :key="name"><dt>{{ name }} {{ txt('版本摘要', 'version digest') }}</dt><dd class="font-mono">{{ version }}</dd></template>
          <dt>{{ txt('连接租约', 'Connection lease') }}</dt><dd class="break-all font-mono">{{ view.observed.connection_lease_id || '—' }}<span v-if="view.observed.connection_evidence" class="text-gray-500"> · {{ view.observed.connection_evidence }}</span></dd>
          <template v-for="(field, name) in view.observed.identity_fields || {}" :key="name"><dt>{{ name }}</dt><dd class="space-y-1"><span>{{ consistencyLabel(field.consistency) }}</span><div class="break-all font-mono text-[10px]">H {{ field.header_value || '—' }} · B {{ field.body_value || '—' }}</div><div class="break-all font-mono text-[10px] text-gray-500">H {{ field.header_digest || '—' }} · B {{ field.body_digest || '—' }}</div></dd></template>
        </dl>
        <p v-if="view.observed" class="text-xs text-gray-500">{{ txt('H 为最终请求头，B 为请求体身份字段；第二行是对应摘要。缺失或未检查不表示匹配。', 'H is the final request header and B the body identity field; the second line shows their digests. Missing or uninspected does not mean matched.') }}</p>
      </div>
      <div v-for="model in view.models" :key="model.model" class="space-y-1 border-t border-gray-100 pt-3 text-xs dark:border-dark-700" :data-test="`codex-fingerprint-model-${model.model}`">
        <div class="flex flex-wrap justify-between gap-2"><strong>{{ model.model }}</strong><span>{{ model.qualified ? txt('路由已验证', 'Route verified') : phaseLabel(model.phase) }}</span></div>
        <p>{{ txt('连续失败周期', 'Failed cycles') }}: {{ model.failed_cycles }} / 2 · {{ model.enrolled ? txt('已登记按需续获', 'Enrolled for demand renewal') : txt('未登记自动续获', 'Not enrolled') }}<span v-if="model.manual_was_enrolled"> · {{ txt('手动采集前已登记', 'Enrolled before manual acquisition') }}</span></p>
        <p v-if="model.state_error" class="text-red-600">{{ txt('记录无法解析', 'Record cannot be decoded') }}: {{ model.state_error }}</p>
        <p v-if="model.recorded_identity && !model.identity_matches" class="text-amber-700 dark:text-amber-300">{{ txt('该记录属于先前的账号主体', 'This record belongs to a previous credential owner') }}: <span class="break-all font-mono">{{ model.recorded_identity }}</span></p>
        <p v-if="model.last_code">{{ txt('最近结果代码', 'Last result code') }}: <span class="font-mono">{{ model.last_code }}</span></p>
        <p v-if="model.revocation_reason" :data-test="`codex-fingerprint-revocation-${model.model}`" class="break-all text-amber-700 dark:text-amber-300">{{ txt('路由资格已撤销', 'Route qualification withdrawn') }}<span v-if="model.revoked_at">{{ ' ' + model.revoked_at }}</span>: {{ model.revocation_reason }}</p>
        <p v-if="model.last_attempt_at">{{ txt('最近尝试', 'Last attempt') }}: {{ model.last_attempt_at }}<span v-if="model.operation_id"> · {{ txt('操作', 'Operation') }} <span class="break-all font-mono">{{ model.operation_id }}</span></span></p>
        <p v-if="model.next_attempt_at">{{ txt('下次尝试', 'Next attempt') }}: {{ model.next_attempt_at }}</p>
        <p v-if="model.expires_at">{{ txt('有效期至', 'Expires') }}: {{ model.expires_at }}</p>
        <p v-if="model.verified_at">{{ txt('业务出口验证时间', 'Business route verified at') }}: {{ model.verified_at }}</p>
        <dl v-if="model.qualification" class="grid grid-cols-[7rem_minmax(0,1fr)] gap-x-3 gap-y-1 break-words">
          <dt>{{ txt('资格 bundle', 'Qualification bundle') }}</dt><dd class="break-all font-mono">{{ model.qualification.bundle.key }} · rev {{ model.qualification.bundle.revision }}</dd>
          <dt>{{ txt('bundle 到期', 'Bundle expires') }}</dt><dd>{{ model.qualification.bundle.expires_at }}</dd>
          <dt>{{ txt('连接租约', 'Connection lease') }}</dt><dd class="break-all font-mono">{{ model.qualification.scope.connection_lease_id || model.qualification.bundle.connection_lease_id || '—' }}</dd>
          <dt>RouteHash</dt><dd class="break-all font-mono">{{ model.qualification.scope.route_hash }} · {{ model.route_matches_current ? txt('与当前出口一致', 'matches the current egress') : txt('与当前出口不同', 'differs from the current egress') }}</dd>
          <dt>{{ txt('验证时账号代理', 'Account proxy at verification') }}</dt><dd>{{ model.qualification.scope.account_proxy_id ? '#' + model.qualification.scope.account_proxy_id : txt('未记录或直连', 'Not recorded or direct') }}</dd>
        </dl>
        <template v-if="model.last_observation">
          <p>{{ model.last_observation.code }} · HTTP {{ model.last_observation.http_status || '—' }} · {{ txt('返回模型', 'Returned model') }}: {{ model.last_observation.response_model || txt('未知', 'Unknown') }} · {{ txt('完整结束', 'Completed') }}: {{ model.last_observation.completed ? '✓' : '—' }}</p>
          <dl class="grid grid-cols-[7rem_minmax(0,1fr)] gap-x-3 gap-y-1 break-words" :data-test="`codex-fingerprint-observation-${model.model}`">
            <template v-for="row in observationRows(model.last_observation)" :key="row[0]"><dt>{{ row[0] }}</dt><dd class="break-all" :class="row[2] ? 'font-mono' : ''">{{ row[1] }}</dd></template>
          </dl>
          <details v-if="model.last_observation.response_headers?.length || model.last_observation.response_headers_omitted?.length" class="text-gray-500" :data-test="`codex-fingerprint-headers-${model.model}`">
            <summary>{{ txt('上游响应头', 'Upstream response headers') }}</summary>
            <div class="mt-1 max-h-64 space-y-0.5 overflow-auto rounded bg-gray-50 p-2 font-mono dark:bg-dark-800">
              <div v-for="(header, index) in model.last_observation.response_headers || []" :key="index" class="break-all">{{ header.name }}: {{ header.value }}</div>
            </div>
            <p v-if="model.last_observation.response_headers_omitted?.length" class="mt-1">{{ txt('超出 4 KiB 未收录', 'Not kept (over 4 KiB)') }}: {{ model.last_observation.response_headers_omitted.join(', ') }}</p>
          </details>
          <details v-if="model.last_observation.upstream_body" class="text-gray-500">
            <summary>{{ txt('上游响应正文', 'Upstream response body') }}</summary>
            <pre class="mt-1 max-h-64 overflow-auto whitespace-pre-wrap break-all rounded bg-gray-50 p-2 dark:bg-dark-800">{{ model.last_observation.upstream_body }}</pre>
          </details>
        </template>
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
import type { RoutingObservation } from '@/api/admin/codexTickets'
import { extractApiErrorMessage } from '@/utils/apiError'

type RoutingScope = { account_id: number; identity: string; profile_hash: string; route_hash: string; route_evidence: string; connection_lease_id?: string; transport: string; account_proxy_id?: number }
type Qualification = { scope: RoutingScope; bundle: { key: string; revision: number; expires_at: string; connection_lease_id?: string }; model: string; verified_at: string; expires_at: string }
type IdentityField = { header_value?: string; body_value?: string; header_digest?: string; body_digest?: string; consistency: string }
type FingerprintView = {
  configured: { identity_source: string; user_agent: string; originator: string; version: string; fingerprint_mode_effective: string; account_revision: string; profile_source: string; profile_schema: number; ua_override_present?: boolean }
  cookie_max_age_seconds: number
  configured_profile?: { os_type: string; os_version: string; arch: string; terminal: string; sandbox: string }
  routing_enabled?: boolean
  fail_closed?: boolean
  current_scope?: RoutingScope
  scope_error?: string
  account_proxy?: { id: number; name?: string; protocol?: string; host?: string; port?: number; username?: string; status?: string }
  harvest_proxy_url?: string
  observed_error?: string
  observed?: { observed_at: string; ingress: string; transport: string; http_protocol: string; user_agent: string; originator?: string; version?: string; request_encoding: string; tls_implementation: string; tls_version?: string; alpn?: string; state?: string; state_present?: boolean; state_length?: number; cookies?: { name: string; value: string; routing: boolean }[]; cookie_names: string[]; cookie_versions?: Record<string, string>; connection_evidence: string; connection_lease_id?: string; account_proxy_id?: number; identity_fields?: Record<string, IdentityField> }
  models: { model: string; phase: string; enrolled: boolean; failed_cycles: number; qualified: boolean; expires_at?: string; verified_at?: string; next_attempt_at?: string; last_observation?: RoutingObservation; schema?: number; state_revision?: number; state_error?: string; recorded_identity?: string; identity_matches?: boolean; last_attempt_at?: string; operation_id?: string; manual_was_enrolled?: boolean; last_code?: string; revoked_at?: string; revocation_reason?: string; qualification?: Qualification; route_matches_current?: boolean }[]
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
// [label, value, monospace]; only recorded values are listed.
function observationRows(observation: RoutingObservation): [string, string, boolean][] {
  const rows: [string, string | number | undefined, boolean][] = [
    [txt('阶段 / 传输', 'Stage / transport'), [observation.stage, observation.transport].filter(Boolean).join(' / '), false],
    [txt('请求模型', 'Requested model'), [observation.requested_model, observation.reasoning_effort].filter(Boolean).join(' · '), true],
    [txt('模型声明匹配', 'Model declaration match'), observation.model_matched ? '✓' : '—', false],
    [txt('错误原文', 'Error'), observation.error, true],
    [txt('上游错误类型', 'Upstream error type'), observation.upstream_error_type, true],
    [txt('上游错误代码', 'Upstream error code'), observation.upstream_error_code, true],
    [txt('上游错误信息', 'Upstream error message'), observation.upstream_error_message, false],
    [txt('上游错误参数', 'Upstream error param'), observation.upstream_error_param, true],
    [txt('上游请求 ID', 'Upstream request ID'), observation.request_id, true],
    ['CF-Ray', observation.cf_ray, true],
    [txt('Cookie 名称', 'Cookie names'), (observation.cookie_names || []).join(', '), true],
    [txt('已发送路由 Cookie', 'Routing cookie sent'), observation.cookie_sent ? '✓' : '—', false],
    ['STATE', observation.state, true],
    [txt('STATE 长度（仅观测）', 'STATE length (observation only)'), observation.state_length, false],
    [txt('耗时', 'Duration'), observation.duration_ms ? `${observation.duration_ms} ms` : undefined, false],
    [txt('观测时间', 'Observed at'), observation.observed_at, false]
  ]
  return rows.filter((row): row is [string, string | number, boolean] => row[1] !== undefined && row[1] !== '').map(([label, value, mono]) => [label, String(value), mono])
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
