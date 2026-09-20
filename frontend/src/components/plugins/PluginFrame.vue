<template>
  <div class="relative min-h-32 bg-raised" :style="{ minHeight: `${height}px` }">
    <p v-if="loading" class="p-4 text-sm text-muted">{{ t('admin.plugins.loadingUI') }}</p>
    <p v-if="error" role="alert" class="p-4 text-sm text-red-600">{{ error }}</p>
    <iframe v-if="session" ref="frame" :src="session.url" sandbox="allow-scripts" referrerpolicy="no-referrer"
      class="w-full border-0 bg-white dark:bg-dark-900" :style="{ height: `${height}px` }" :title="title" @load="loaded" />
    <TotpStepUpDialog :controller="stepUp" />
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI, type PluginUISession } from '@/api/admin'
import accountJobsAPI, { type AccountJob } from '@/api/admin/accountJobs'
import { useAppStore } from '@/stores'
import { useAuthStore } from '@/stores/auth'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { usePluginExtensions } from '@/stores/pluginExtensions'
import { callPluginResource, type PluginResourceDescriptor } from './resourceClient'
import { extractApiErrorCode } from '@sub2api/plugin-ui/errors'

const props = withDefaults(defineProps<{ pluginId: number; title: string; permission?: 'admin' | 'user'; context?: Record<string, unknown> }>(), { permission: 'admin', context: () => ({}) })
const emit = defineEmits<{ saved: []; job: [job: AccountJob] }>()
const { t, locale } = useI18n()
const app = useAppStore()
const auth = useAuthStore()
const stepUp = useStepUp()
const registry = usePluginExtensions()
const frame = ref<HTMLIFrameElement | null>(null)
const session = ref<PluginUISession | null>(null)
const loading = ref(false), error = ref(''), height = ref(640)
const pending = new Map<string, number>()
let generation = 0, frameLoaded = false
let resourceCatalog: Promise<PluginResourceDescriptor[]> | null = null
let presentationObserver: MutationObserver | null = null

function clearPending() {
  for (const timer of pending.values()) window.clearTimeout(timer)
  pending.clear()
  resourceCatalog = null
  generation++
}
function loaded() {
  if (frameLoaded) clearPending()
  frameLoaded = true
  loading.value = false
}
function failure(value: unknown): string { return value instanceof Error ? value.message : t('common.error') }

function currentContext() {
  const contribution = registry.items.find(item => item.plugin_id === props.pluginId && item.id === props.context.contribution_id)
  const changed = !!(contribution?.package_sha256 && session.value?.package_sha256 && contribution.package_sha256 !== session.value.package_sha256)
  const styles = getComputedStyle(document.documentElement)
  const tokens: Record<string, string> = {}
  for (let i = 0; i < styles.length; i++) {
    const name = styles[i]!
    const value = styles.getPropertyValue(name).trim()
    if (/^--(?:ui|theme)-[a-z0-9-]+$/.test(name) && value.length <= 512 && !/url\s*\(/i.test(value)) tokens[name] = value
  }
  return { ...props.context, locale: locale?.value || 'zh', theme: document.documentElement.classList.contains('dark') ? 'dark' : 'light',
    available: !changed && (!props.context.contribution_id || !!contribution?.available),
    unavailable_message: changed ? t('admin.plugins.uiVersionChanged') : t('admin.plugins.extensionUnavailable'),
    theme_tokens: tokens, theme_stylesheets: registry.items.filter(item => item.slot === 'theme' && item.available && item.stylesheet_url).map(item => item.stylesheet_url!) }
}

function sendContext() {
  resourceCatalog = null
  if (!session.value || !frame.value?.contentWindow) return
  frame.value.contentWindow.postMessage({ source: 'sub2api-plugin-host', bridge_token: session.value.bridge_token, type: 'extension.context.updated', context: currentContext() }, '*')
}

watch(() => [auth.user?.id, props.pluginId, props.context.account_id, props.context.contribution_id, Array.isArray(props.context.account_ids) ? props.context.account_ids.join(',') : ''], async () => {
  const id = props.pluginId
  clearPending()
  const version = generation
  session.value = null; frameLoaded = false; error.value = ''; loading.value = true
  try {
    const contributionID = typeof props.context.contribution_id === 'string' ? props.context.contribution_id : undefined
    const next = await (props.permission === 'user' ? adminAPI.plugins.createUserUISession(id, contributionID) : adminAPI.plugins.createUISession(id, contributionID))
    if (generation === version) session.value = next
  }
  catch (value) { if (generation === version) { error.value = failure(value); loading.value = false } }
}, { immediate: true })

async function receive(event: MessageEvent) {
  const current = session.value
  if (!current || event.source !== frame.value?.contentWindow || event.origin !== 'null') return
  const message = event.data
  if (!message || message.source !== 'sub2api-plugin-ui' || message.bridge_token !== current.bridge_token) return
  if (message.type === 'sub2api.plugin.ready') { loading.value = false; return }
  if (message.type === 'ui.resize') { const n = Number(message.height); if (Number.isFinite(n)) height.value = Math.max(120, Math.min(1200, Math.round(n))); return }
  if (message.type === 'ui.notify') {
    const text = typeof message.message === 'string' ? message.message.slice(0, 500) : ''
    if (text) { if (message.level === 'error') app.showError(text); else if (message.level === 'success') app.showSuccess(text); else if (message.level === 'warning') app.showWarning(text); else app.showInfo(text) }
    return
  }
  if (!['config.load', 'config.save', 'config.test', 'plugin.status', 'extension.context', 'extension.invoke', 'extension.job.submit', 'extension.job.get', 'extension.resource'].includes(message.type)) return
  const requestID = typeof message.request_id === 'string' ? message.request_id.trim() : ''
  if (!requestID || requestID.length > 128 || pending.has(requestID) || pending.size >= 32) return
  pending.set(requestID, window.setTimeout(() => pending.delete(requestID), 30000))
  const version = generation, id = props.pluginId
  const actorID = auth.user?.id
  const accountID = typeof props.context.account_id === 'number' ? props.context.account_id : undefined
  const reply = (payload: Record<string, unknown>) => {
    const timer = pending.get(requestID)
    if (generation !== version || timer === undefined || session.value !== current || !frame.value?.contentWindow) return
    window.clearTimeout(timer); pending.delete(requestID)
    frame.value.contentWindow.postMessage({ source: 'sub2api-plugin-host', bridge_token: current.bridge_token, type: `${message.type}.result`, request_id: requestID, ...payload }, '*')
  }
  try {
    if (current.permission === 'user' && !['extension.context', 'extension.resource'].includes(message.type)) throw new Error(t('admin.plugins.bridgeRejected'))
    switch (message.type) {
      case 'extension.context': reply({ ok: true, context: currentContext() }); break
      case 'extension.resource': {
        if (typeof message.operation !== 'string' || !message.input || typeof message.input !== 'object' || Array.isArray(message.input)) throw new Error(t('admin.plugins.bridgeRejected'))
        resourceCatalog ||= adminAPI.plugins.resources(id, current.permission || 'admin')
        const descriptor = (await resourceCatalog).find(item => item.name === message.operation)
        if (!descriptor) throw new Error(t('admin.plugins.bridgeRejected'))
        const execute = () => callPluginResource(id, current.package_sha256 || '', descriptor, message.input)
        const result = current.permission === 'user' ? await execute() : await stepUp.run(execute)
        reply({ ok: true, result }); break
      }
      case 'config.load': reply({ ok: true, config: await adminAPI.plugins.getConfig(id) }); break
      case 'config.save': {
        if (!message.config || typeof message.config !== 'object' || Array.isArray(message.config)) throw new Error(t('admin.plugins.bridgeRejected'))
        const config = await stepUp.run(() => adminAPI.plugins.saveConfig(id, message.config))
        reply({ ok: true, config }); if (generation === version) emit('saved'); break
      }
      case 'config.test': { const result = await stepUp.run(() => adminAPI.plugins.test(id)); reply({ ok: result.success, result }); break }
      case 'plugin.status': reply({ ok: true, result: await adminAPI.plugins.status(id) }); break
      case 'extension.invoke': {
        if (typeof message.operation !== 'string' || !message.payload || typeof message.payload !== 'object' || Array.isArray(message.payload)) throw new Error(t('admin.plugins.bridgeRejected'))
        const result = await adminAPI.plugins.invokeAdmin(id, message.operation, accountID, message.payload)
        reply({ ok: !result.code, result: result.payload, error: result.message || result.code }); break
      }
      case 'extension.job.submit': {
        if (typeof message.operation !== 'string' || !Array.isArray(message.items) || !message.items.length || message.items.length > 3200) throw new Error(t('admin.plugins.bridgeRejected'))
        const selected = accountID !== undefined ? [accountID] : Array.isArray(props.context.account_ids) ? props.context.account_ids : null
        if (selected && message.items.some((item: { account_id?: unknown }) => !selected.includes(item.account_id))) throw new Error(t('admin.plugins.bridgeRejected'))
        const job = await adminAPI.plugins.submitJob(id, message.operation, message.items, typeof message.operation_key === 'string' ? message.operation_key : undefined)
        const { useAccountJobsStore } = await import('@/stores/accountJobs')
        if (auth.user?.id === actorID) useAccountJobsStore().track(job, { open: false })
        reply({ ok: true, job }); if (generation === version && auth.user?.id === actorID) emit('job', job); break
      }
      case 'extension.job.get': {
        if (!Number.isSafeInteger(message.job_id) || message.job_id <= 0) throw new Error(t('admin.plugins.bridgeRejected'))
        const job = await accountJobsAPI.get(message.job_id)
        if (job.metadata.plugin_id !== id) throw new Error(t('admin.plugins.bridgeRejected'))
        reply({ ok: true, job }); break
      }
    }
  } catch (value) {
    const status = typeof value === 'object' && value !== null && 'status' in value ? value.status : undefined
    reply({ ok: false, error: isStepUpCancelled(value) ? t('common.cancel') : failure(value), code: extractApiErrorCode(value), status })
  }
}

watch(() => [locale?.value, registry.items.map(item => `${item.plugin_id}:${item.id}:${item.available}:${item.package_sha256}:${item.stylesheet_url}`).join('|')], sendContext)
onMounted(() => {
  window.addEventListener('message', receive)
  if (!registry.loaded) void registry.refresh()
  presentationObserver = new MutationObserver(sendContext)
  presentationObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class', 'style'] })
})
onBeforeUnmount(() => { clearPending(); presentationObserver?.disconnect(); window.removeEventListener('message', receive) })
</script>
