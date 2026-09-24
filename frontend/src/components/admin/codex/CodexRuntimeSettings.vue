<template>
  <section class="space-y-4" aria-live="polite">
    <h2 class="text-xl font-semibold text-ink">{{ text('Codex 路由设置', 'Codex route settings') }}</h2>
    <p class="text-sm text-muted">{{ text('路由材料有效、响应完整、模型声明一致是不同证据。STATE 长度仅供观测，不能证明账号套餐或模型质量；本页操作不包含质量验证。', 'Valid routing material, a complete response and a matching model declaration are separate observations. STATE length proves neither account plan nor model quality; operations on this page do not test quality.') }}</p>
    <p v-if="loading" class="text-sm text-muted">{{ t('common.loading') }}</p>
    <fieldset v-else :disabled="busy || !config" class="space-y-4 disabled:opacity-60">
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
          <span class="block">{{ text('协议', 'Protocol') }}</span>
          <select v-model="protocol" data-test="codex-protocol" class="input">
            <option v-for="scheme in protocols" :key="scheme" :value="scheme">{{ scheme }}</option>
          </select>
        </label>
        <button type="button" data-test="codex-test-proxy" class="btn btn-secondary" @click="testProxy">{{ text('测试连接', 'Test connection') }}</button>
        <button type="button" class="btn btn-secondary" @click="copyProxy">{{ text('复制', 'Copy') }}</button>
        <button type="button" data-test="codex-clear-proxy" class="btn btn-secondary" @click="clearProxy">{{ text('清除代理并停用路由采集', 'Clear proxy and stop route acquisition') }}</button>
      </div>
      <p class="text-sm text-muted">{{ text('连接测试验证代理与证书；成功连接不代表 Codex 路由已验证。', 'Connection tests verify the proxy and certificate; they do not verify the Codex route.') }}</p>
      <button type="button" data-test="codex-save" class="btn btn-primary" @click="save">{{ text('保存设置', 'Save settings') }}</button>
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
import { codexTicketsAPI, type ProxyTestResult } from '@/api/admin/codexTickets'
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
let generation = 0

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
async function run(action: () => Promise<void>) {
  if (busy.value || !config.value) return
  const current = generation
  busy.value = true; resetResult()
  try { await action() } catch (error) {
    if (current === generation && !isStepUpCancelled(error)) { failure.value = true; message.value = extractApiErrorMessage(error, t('common.error')) }
  } finally { if (current === generation) busy.value = false }
}
async function persist(value: CodexRuntimeConfig) {
  const current = generation, actor = auth.user?.id
  const saved = await stepUp.run(() => {
    if (current !== generation || auth.user?.id !== actor) throw new Error(t('common.operationFailed'))
    return codexRuntimeAPI.saveConfig(value)
  })
  if (current !== generation || auth.user?.id !== actor) return false
  config.value = saved
  return true
}
function save() {
  return run(async () => {
    if (await persist({ ...config.value!, enabled: enabled.value, proxy_url: address.value, proxy_protocol: protocol.value, request_zstd: compression.value })) {
      address.value = config.value?.proxy_url || ''
      message.value = text('设置已保存', 'Settings saved')
    }
  })
}
function clearProxy() {
  return run(async () => {
    if (await persist({ ...config.value!, proxy_url: '', enabled: false })) {
      address.value = ''; enabled.value = false
      message.value = text('已清除并关闭', 'Cleared and disabled')
    }
  })
}
function testProxy() {
  return run(async () => {
    const current = generation, actor = auth.user?.id
    const value = address.value, selectedProtocol = protocol.value
    const tested = await stepUp.run(() => {
      if (current !== generation || auth.user?.id !== actor) throw new Error(t('common.operationFailed'))
      return codexTicketsAPI.testProxy(value, selectedProtocol)
    })
    if (current === generation) proxyResult.value = tested
  })
}
function copyProxy() {
  resetResult(); proxyInput.value?.focus(); proxyInput.value?.select()
  try {
    if (!document.execCommand('copy')) throw new Error(text('复制失败，请手动复制。', 'Copy failed; copy the selected text manually.'))
    message.value = text('已复制', 'Copied')
  } catch (error) { failure.value = true; message.value = extractApiErrorMessage(error, t('common.error')) }
}
watch(() => auth.user?.id, () => { void load() })
onMounted(() => { void load() })
onBeforeUnmount(() => { generation++; config.value = null; address.value = '' })
</script>
