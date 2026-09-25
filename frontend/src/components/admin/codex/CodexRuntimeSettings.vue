<template>
  <section class="space-y-4" aria-live="polite">
    <h2 class="text-xl font-semibold text-ink">{{ text('Codex 路由设置', 'Codex route settings') }}</h2>
    <p class="text-sm text-muted">{{ text('路由材料有效、响应完整、模型声明一致是不同证据。STATE 长度仅供观测，不能证明账号套餐或模型质量；本页操作不包含质量验证。', 'Valid routing material, a complete response and a matching model declaration are separate observations. STATE length proves neither account plan nor model quality; operations on this page do not test quality.') }}</p>
    <p v-if="loading" class="text-sm text-muted">{{ t('common.loading') }}</p>
    <fieldset v-else :disabled="!config" class="space-y-4 disabled:opacity-60">
      <label class="flex items-center gap-2">
        <input v-model="enabled" data-test="codex-enabled" type="checkbox" />
        {{ text('启用 Codex 路由采集与验证', 'Enable Codex route acquisition and verification') }}
      </label>
      <label class="flex items-center gap-2">
        <input v-model="compression" data-test="codex-compression" type="checkbox" />
        {{ text('压缩 Codex Responses 请求体', 'Compress Codex Responses requests') }}
      </label>
      <label class="block space-y-2">
        <span>{{ text('采集代理', 'Acquisition proxy') }}</span>
        <textarea ref="proxyInput" v-model="address" data-test="codex-proxy" class="input" rows="4" autocomplete="off" spellcheck="false" />
      </label>
      <div class="flex flex-wrap items-end gap-3">
        <label class="space-y-2">
          <span class="block">{{ text('未写协议时使用', 'Default for addresses without a protocol') }}</span>
          <select v-model="protocol" data-test="codex-protocol" class="input">
            <option v-for="scheme in protocols" :key="scheme" :value="scheme">{{ scheme }}</option>
          </select>
        </label>
        <button type="button" data-test="codex-parse-proxy" :disabled="busy" class="btn btn-secondary" @click="parseProxy">{{ text('识别代理', 'Recognize proxies') }}</button>
        <button type="button" data-test="codex-test-proxy" :disabled="busy" class="btn btn-secondary" @click="testProxy">{{ text('测试连接', 'Test connection') }}</button>
        <button type="button" class="btn btn-secondary" @click="copyProxy">{{ text('复制', 'Copy') }}</button>
        <button type="button" data-test="codex-clear-proxy" :disabled="busy" class="btn btn-secondary" @click="clearProxy">{{ text('清除代理并停用路由采集', 'Clear proxy and stop route acquisition') }}</button>
      </div>
      <p class="text-sm text-muted">{{ text('可粘贴 URI、四段式、分列或标签记录；原文的明确协议优先。多条或有歧义时，请选一个全局采集代理。引号可保留分隔符及字段两端空白。', 'Paste URLs, four-field, column or labelled records. Explicit protocols take precedence. Choose one global acquisition proxy when several records or interpretations are recognized. Quote fields to preserve delimiters and surrounding spaces.') }}</p>
      <fieldset v-if="parsed?.candidates.length" data-test="codex-proxy-candidates" class="space-y-2 border border-line p-3">
        <legend>{{ text('选择采集代理', 'Choose acquisition proxy') }}</legend>
        <label v-for="(candidate, index) in parsed.candidates" :key="candidate.selection_id" class="flex items-start gap-2 text-sm">
          <input v-model="selection" type="radio" name="codex-proxy-selection" :value="candidate.selection_id" :disabled="busy" />
          <span>{{ index + 1 }}. {{ candidate.protocol }} · {{ candidate.host }} · {{ candidate.port }}
            <span v-if="candidate.username_masked"> · {{ text('用户名', 'Username') }} {{ candidate.username_masked }}</span>
            · {{ text('来源行', 'Source line') }} {{ candidate.source_line }}, {{ text('列', 'column') }} {{ candidate.source_column }} · {{ candidate.format }}</span>
        </label>
      </fieldset>
      <ul v-if="parsed?.issues.length" data-test="codex-proxy-issues" class="space-y-1 text-sm text-red-600" role="alert">
        <li v-for="(issue, index) in parsed.issues" :key="index">{{ text('行', 'Line') }} {{ issue.line }}, {{ text('列', 'column') }} {{ issue.column }} · {{ issue.field }}: {{ issue.message }}</li>
      </ul>
      <p class="text-sm text-muted">{{ text('连接测试验证代理与证书；成功连接不代表 Codex 路由已验证。', 'Connection tests verify the proxy and certificate; they do not verify the Codex route.') }}</p>
      <button type="button" data-test="codex-save" :disabled="busy" class="btn btn-primary" @click="save">{{ text('保存设置', 'Save settings') }}</button>
    </fieldset>
    <div v-if="message || proxyResult" role="status" class="space-y-2 border border-line p-3 text-sm" :class="failure ? 'text-red-600' : 'text-ink'">
      <p v-if="message">{{ message }}</p>
      <template v-if="proxyResult">
        <p>{{ proxyResult.network_reachable && ['proxy_reachable', 'target_http_status'].includes(proxyResult.code)
          ? text(`代理链路已连接，目标返回 HTTP ${proxyResult.http_status}；本次未发送账号凭据。`, `Proxy connected; target returned HTTP ${proxyResult.http_status}. No account credential was sent.`)
          : text('代理连接或证书验证失败。', 'Proxy connection or certificate verification failed.') }}</p>
        <p v-for="stage in proxyResult.stages || []" :key="stage.name">{{ stage.name }}: {{ stage.success ? '✓' : '✗' }} {{ stage.message || '' }}</p>
        <p v-if="proxyResult.certificate_fingerprint">{{ text('证书指纹', 'Certificate fingerprint') }}: {{ proxyResult.certificate_fingerprint }}</p>
      </template>
    </div>
    <TotpStepUpDialog :controller="stepUp" />
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { codexRuntimeAPI, type CodexRuntimeConfig } from '@/api/admin/codexRuntime'
import { codexTicketsAPI, type ProxyParseResult, type ProxyTestResult } from '@/api/admin/codexTickets'
import { useAuthStore } from '@/stores/auth'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t, locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('en') ? en : zh
const auth = useAuthStore()
const stepUp = useStepUp()
const config = ref<CodexRuntimeConfig | null>(null)
const loading = ref(true), busy = ref(false), failure = ref(false)
const enabled = ref(false), compression = ref(true), address = ref(''), protocol = ref('http')
const protocols = ['http', 'https', 'socks5', 'socks5h']
const proxyInput = ref<HTMLTextAreaElement | null>(null)
const message = ref(''), proxyResult = ref<ProxyTestResult | null>(null)
const parsed = ref<ProxyParseResult | null>(null), selection = ref('')
let generation = 0
let draft = 0, operation = 0

function invalidateDraft() {
  draft++; operation++; busy.value = false
  parsed.value = null; selection.value = ''; resetResult()
}
function scope() {
  const current = generation, revision = draft, request = ++operation, actor = auth.user?.id
  return () => current === generation && revision === draft && request === operation && actor === auth.user?.id
}

function resetResult() { message.value = ''; failure.value = false; proxyResult.value = null }
async function load() {
  const current = ++generation
  config.value = null; address.value = ''; loading.value = true; busy.value = false; resetResult()
  try {
    const value = await codexRuntimeAPI.getConfig()
    if (current !== generation) return
    config.value = value
    enabled.value = !!value.enabled
    compression.value = value.request_zstd !== false
    address.value = value.proxy_url || ''
    protocol.value = protocols.includes(String(value.proxy_protocol)) ? String(value.proxy_protocol)
      : /^(https?|socks5h?):\/\//.exec(address.value)?.[1] || 'http'
  } catch (error) {
    if (current === generation) { failure.value = true; message.value = extractApiErrorMessage(error, t('common.error')) }
  } finally { if (current === generation) loading.value = false }
}
async function run(action: (current: () => boolean) => Promise<void>) {
  if (busy.value || !config.value) return
  const current = scope()
  busy.value = true; resetResult()
  try { await action(current) } catch (error) {
    if (current() && !isStepUpCancelled(error)) { failure.value = true; message.value = extractApiErrorMessage(error, t('common.error')) }
  } finally { if (current()) busy.value = false }
}
async function recognize(current: () => boolean) {
  if (parsed.value) return true
  const value = await codexTicketsAPI.parseProxy(address.value, protocol.value)
  if (!current()) return false
  parsed.value = value; selection.value = value.selection_id || ''
  return true
}
async function requireSelection(current: () => boolean, allowEmpty: boolean) {
  if (allowEmpty && !address.value.trim()) return true
  if (!await recognize(current)) return false
  if (!selection.value) {
    throw new Error(parsed.value?.candidates.length
      ? text('请选择一个已识别的代理，再保存或测试。', 'Select one recognized proxy before saving or testing.')
      : text('未识别到有效代理，请按字段提示修改原文。', 'No valid proxy was recognized. Edit the input using the field issues.'))
  }
  return true
}
function parseProxy() { return run(async current => { await recognize(current) }) }
async function persist(value: CodexRuntimeConfig, current: () => boolean) {
  const saved = await stepUp.run(() => {
    if (!current()) throw new Error(t('common.operationFailed'))
    return codexRuntimeAPI.saveConfig(value)
  })
  if (!current()) return false
  config.value = saved
  return true
}
function save() {
  return run(async current => {
    if (!await requireSelection(current, true)) return
    if (await persist({ ...config.value!, enabled: enabled.value, proxy_url: address.value, proxy_protocol: protocol.value, proxy_selection_id: selection.value || undefined, request_zstd: compression.value }, current)) {
      address.value = config.value?.proxy_url || ''
      message.value = text('设置已保存', 'Settings saved')
    }
  })
}
function clearProxy() {
  return run(async current => {
    if (await persist({ ...config.value!, proxy_url: '', proxy_selection_id: undefined, enabled: false }, current)) {
      address.value = ''; enabled.value = false
      message.value = text('已清除并关闭', 'Cleared and disabled')
    }
  })
}
function testProxy() {
  return run(async current => {
    if (!await requireSelection(current, false)) return
    const value = address.value, selectedProtocol = protocol.value
    const selected = selection.value
    const tested = await stepUp.run(() => {
      if (!current()) throw new Error(t('common.operationFailed'))
      return codexTicketsAPI.testProxy(value, selectedProtocol, selected)
    })
    if (current()) proxyResult.value = tested
  })
}
function copyProxy() {
  resetResult(); proxyInput.value?.focus(); proxyInput.value?.select()
  try {
    if (!document.execCommand('copy')) throw new Error(text('复制失败，请手动复制。', 'Copy failed; copy the selected text manually.'))
    message.value = text('已复制', 'Copied')
  } catch (error) { failure.value = true; message.value = extractApiErrorMessage(error, t('common.error')) }
}
watch([address, protocol], invalidateDraft, { flush: 'sync' })
watch([enabled, compression], () => { draft++; operation++; busy.value = false }, { flush: 'sync' })
watch(selection, () => { proxyResult.value = null; message.value = ''; failure.value = false }, { flush: 'sync' })
watch(() => auth.user?.id, () => { void load() }, { flush: 'sync' })
onMounted(() => { void load() })
onBeforeUnmount(() => { generation++; config.value = null; address.value = '' })
</script>
