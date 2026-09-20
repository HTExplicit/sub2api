<template>
  <div class="relative bg-raised" :class="inline ? 'min-h-0' : 'min-h-32'" :style="{ minHeight: `${height}px` }">
    <p v-if="loading" class="p-4 text-sm text-muted">{{ t('admin.plugins.loadingUI') }}</p>
    <p v-if="error" role="alert" class="p-4 text-sm text-red-600">{{ error }}</p>
    <iframe v-if="session" ref="frame" :src="session.url" sandbox="allow-scripts allow-forms" referrerpolicy="no-referrer"
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
import { pluginPreferenceEvent, pluginPreferenceKey, readPluginPreference, writeBrowserPreference } from './preferences'
import { getConfiguredTablePageSizeOptions } from '@/utils/tablePreferences'

const props = withDefaults(defineProps<{ pluginId: number; title: string; inline?: boolean; permission?: 'admin' | 'user'; context?: Record<string, unknown> }>(), { inline: false, permission: 'admin', context: () => ({}) })
const emit = defineEmits<{ saved: []; job: [job: AccountJob]; event: [name: string, payload: unknown] }>()
const { t, locale } = useI18n()
const app = useAppStore()
const auth = useAuthStore()
const stepUp = useStepUp()
const registry = usePluginExtensions()
const frame = ref<HTMLIFrameElement | null>(null)
const session = ref<PluginUISession | null>(null)
const loading = ref(false), error = ref(''), height = ref(props.inline ? 64 : 640)
const pending = new Map<string, number>()
const controllers = new Map<string, AbortController>()
const preferenceKeys = new Set<string>()
let generation = 0, frameLoaded = false
let resourceCatalog: Promise<PluginResourceDescriptor[]> | null = null
let presentationObserver: MutationObserver | null = null

function clearPending() {
  for (const timer of pending.values()) window.clearTimeout(timer)
  pending.clear()
  for (const controller of controllers.values()) controller.abort()
  controllers.clear()
  preferenceKeys.clear()
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
  return JSON.parse(JSON.stringify({ ...props.context, actor_id: auth.user?.id, retained_controls: contribution?.retained_controls === true, layout: props.inline ? 'inline' : 'page', locale: locale?.value || 'zh', theme: document.documentElement.classList.contains('dark') ? 'dark' : 'light',
    available: !changed && (!props.context.contribution_id || !!contribution?.available),
    unavailable_message: changed ? t('admin.plugins.uiVersionChanged') : t('admin.plugins.extensionUnavailable'),
    table_page_size_options: getConfiguredTablePageSizeOptions(), theme_tokens: tokens, theme_stylesheets: registry.items.filter(item => item.slot === 'theme' && item.available && item.stylesheet_url).map(item => item.stylesheet_url!) }))
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
  if (message.type === 'extension.cancel') { if (typeof message.target_request_id === 'string') controllers.get(message.target_request_id)?.abort(); return }
  if (message.type === 'sub2api.plugin.ready') { loading.value = false; return }
  if (message.type === 'ui.resize') { const n = Number(message.height); if (Number.isFinite(n)) height.value = Math.max(props.inline ? 0 : 120, Math.min(1200, Math.round(n))); return }
  if (message.type === 'ui.notify') {
    const text = typeof message.message === 'string' ? message.message.slice(0, 500) : ''
    if (text) { if (message.level === 'error') app.showError(text); else if (message.level === 'success') app.showSuccess(text); else if (message.level === 'warning') app.showWarning(text); else app.showInfo(text) }
    return
  }
  if (!['config.load', 'config.save', 'config.test', 'plugin.status', 'extension.context', 'extension.invoke', 'extension.job.submit', 'extension.job.get', 'extension.job.open', 'extension.resource', 'extension.event', 'preference.read', 'preference.write', 'ui.confirm', 'ui.download'].includes(message.type)) return
  const requestID = typeof message.request_id === 'string' ? message.request_id.trim() : ''
  if (!requestID || requestID.length > 128 || pending.has(requestID) || pending.size >= 32) return
  pending.set(requestID, window.setTimeout(() => { controllers.get(requestID)?.abort(); controllers.delete(requestID); pending.delete(requestID) }, 30000))
  const version = generation, id = props.pluginId
  const actorID = auth.user?.id
  const accountID = typeof props.context.account_id === 'number' ? props.context.account_id : undefined
  const reply = (payload: Record<string, unknown>) => {
    const timer = pending.get(requestID)
    if (generation !== version || timer === undefined || session.value !== current || !frame.value?.contentWindow) return
    window.clearTimeout(timer); pending.delete(requestID)
    controllers.delete(requestID)
    frame.value.contentWindow.postMessage({ source: 'sub2api-plugin-host', bridge_token: current.bridge_token, type: `${message.type}.result`, request_id: requestID, ...payload }, '*')
  }
  try {
    if (current.permission === 'user' && !['extension.context', 'extension.resource', 'extension.event', 'preference.read', 'preference.write', 'ui.confirm', 'ui.download'].includes(message.type)) throw new Error(t('admin.plugins.bridgeRejected'))
    switch (message.type) {
      case 'extension.context': reply({ ok: true, context: currentContext() }); break
      case 'ui.confirm': {
        if (typeof message.message !== 'string' || message.message.length > 4000) throw new Error(t('admin.plugins.bridgeRejected'))
        reply({ ok: true, confirmed: window.confirm(message.message) }); break
      }
      case 'ui.download': {
        if (!(message.blob instanceof Blob) || message.blob.size > 20 * 1024 * 1024 || !['image/png', 'image/jpeg', 'image/webp'].includes(message.blob.type) || typeof message.filename !== 'string') throw new Error(t('admin.plugins.bridgeRejected'))
        const url = URL.createObjectURL(message.blob), link = document.createElement('a')
        link.href = url; link.download = Array.from(message.filename as string, character => character.charCodeAt(0) < 32 ? '_' : character).join('').replace(/[\\/:*?"<>|]/g, '_').slice(0, 180)
        document.body.append(link); link.click(); link.remove()
        window.setTimeout(() => URL.revokeObjectURL(url), 1000)
        reply({ ok: true }); break
      }
      case 'extension.event': {
        const contribution = registry.items.find(item => item.plugin_id === id && item.id === props.context.contribution_id)
        if (typeof message.name !== 'string' || !contribution?.events?.includes(message.name)) throw new Error(t('admin.plugins.bridgeRejected'))
        reply({ ok: true }); emit('event', message.name, message.payload); break
      }
      case 'preference.read':
      case 'preference.write': {
        const key = pluginPreferenceKey(location.origin, actorID || 0, current.plugin_key || '', message.key)
        preferenceKeys.add(message.key)
        if (message.type === 'preference.write') {
          if (typeof message.value !== 'string' || message.value.length > 65536) throw new Error(t('admin.plugins.bridgeRejected'))
          writeBrowserPreference(key, message.value)
          reply({ ok: true })
        } else {
          let value = null
          try { value = readPluginPreference(location.origin, actorID!, current.plugin_key!, message.key) } catch { /* Use an empty draft. */ }
          reply({ ok: true, value })
        }
        break
      }
      case 'extension.resource': {
        if (typeof message.operation !== 'string' || !message.input || typeof message.input !== 'object' || Array.isArray(message.input)) throw new Error(t('admin.plugins.bridgeRejected'))
        resourceCatalog ||= adminAPI.plugins.resources(id, current.permission || 'admin')
        const descriptor = (await resourceCatalog).find(item => item.name === message.operation)
        if (!descriptor) throw new Error(t('admin.plugins.bridgeRejected'))
        if (generation !== version || session.value !== current) throw new Error('Plugin view closed')
        const controller = new AbortController()
        controllers.set(requestID, controller)
        const execute = () => callPluginResource(id, current.package_sha256 || '', descriptor, message.input, controller.signal, actorID)
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
      case 'extension.job.open': {
        if (!Number.isSafeInteger(message.job_id) || message.job_id <= 0) throw new Error(t('admin.plugins.bridgeRejected'))
        const job = await accountJobsAPI.get(message.job_id)
        if (job.metadata.plugin_id !== id || auth.user?.id !== actorID) throw new Error(t('admin.plugins.bridgeRejected'))
        reply({ ok: true }); emit('job', job); break
      }
    }
  } catch (value) {
    const status = typeof value === 'object' && value !== null && 'status' in value ? value.status : undefined
    reply({ ok: false, error: isStepUpCancelled(value) ? t('common.cancel') : failure(value), code: extractApiErrorCode(value), status })
  }
}

watch(() => [locale?.value, registry.items.map(item => `${item.plugin_id}:${item.id}:${item.available}:${item.package_sha256}:${item.stylesheet_url}`).join('|')], sendContext)
watch(() => props.context, sendContext, { deep: true })
function preferenceChanged(event: Event) {
  const current = session.value, userID = auth.user?.id
  if (!current?.plugin_key || !userID || !frame.value?.contentWindow) return
  const change = (event as CustomEvent<{ key: string; value: string }>).detail
  if (!change) return
  for (const key of preferenceKeys) if (pluginPreferenceKey(location.origin, userID, current.plugin_key, key) === change.key) {
    frame.value.contentWindow.postMessage({ source: 'sub2api-plugin-host', bridge_token: current.bridge_token, type: 'preference.updated', key, value: change.value }, '*')
  }
}
onMounted(() => {
  window.addEventListener('message', receive)
  window.addEventListener(pluginPreferenceEvent, preferenceChanged)
  if (!registry.loaded) void registry.refresh()
  presentationObserver = new MutationObserver(sendContext)
  presentationObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class', 'style'] })
})
onBeforeUnmount(() => { clearPending(); presentationObserver?.disconnect(); window.removeEventListener('message', receive); window.removeEventListener(pluginPreferenceEvent, preferenceChanged) })
</script>
