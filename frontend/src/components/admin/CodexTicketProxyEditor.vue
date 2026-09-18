<template>
  <div class="space-y-2 mt-3">
    <textarea id="codex-ticket-harvest-proxy" :value="modelValue" class="input w-full font-mono text-sm" rows="4" autocomplete="off" spellcheck="false"
      :aria-label="t('admin.accounts.ticketProxy.address')" @input="input(($event.target as HTMLTextAreaElement).value)" @blur="normalize" />
    <div class="flex flex-wrap gap-2 items-center">
      <label>{{ t('admin.accounts.ticketProxy.protocol') }}
        <select v-model="protocol" class="input" @change="changeProtocol"><option>http</option><option>https</option><option>socks5</option><option>socks5h</option></select>
      </label>
      <button type="button" class="btn btn-secondary" :disabled="busy || !modelValue.trim()" @click="test">{{ t('admin.accounts.ticketProxy.test') }}</button>
      <button type="button" class="btn btn-secondary" :disabled="!modelValue" @click="copy">{{ t('common.copy') }}</button>
      <button type="button" class="btn btn-secondary" @click="emit('clear')">{{ t('admin.accounts.ticketProxy.clear') }}</button>
    </div>
    <p class="text-xs text-gray-500">{{ t('admin.accounts.ticketProxy.hint') }}</p>
    <p v-if="error" role="alert" class="text-red-600 text-sm">{{ error }}</p>
    <div v-if="result" role="status" class="space-y-1 border p-3 text-sm">
      <p :class="result.success ? 'text-emerald-600' : 'text-amber-600'">{{ result.message }}</p>
      <p v-if="result.failure_detail" class="break-words text-red-600">{{ result.failure_detail }}</p>
      <p v-for="(stage, index) in result.stages" :key="index">{{ t('admin.accounts.ticketProxy.stages.' + stage.name) }} · {{ stage.success ? '✓' : '✗' }} {{ stage.message }} {{ stage.duration_ms }} ms</p>
      <p v-if="result.certificate_trust">{{ t('admin.accounts.ticketProxy.trust') }}: {{ result.certificate_trust }} {{ result.certificate_fingerprint }}</p>
      <p v-if="result.protocol_suggestion">{{ result.protocol_suggestion }}</p>
    </div>
  </div>
</template>
<script setup lang="ts">
import { ref, watch, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { codexTicketsAPI, type ProxyTestResult } from '@/api/admin/codexTickets'
import { normalizeCodexTicketProxy } from '@/utils/codexTicketProxy'
const props = defineProps<{ modelValue: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string]; clear: [] }>()
const { t } = useI18n()
const protocol = ref('http'), error = ref(''), busy = ref(false), result = ref<ProxyTestResult | null>(null)
let version = 0
watch(() => props.modelValue, value => {
  version++; result.value = null; error.value = ''
  const scheme = /^(https?|socks5h?):\/\//.exec(value)?.[1]
  if (scheme) protocol.value = scheme
})
function input(value: string) { emit('update:modelValue', value) }
function normalize() {
  try { const value = normalizeCodexTicketProxy(props.modelValue, protocol.value); emit('update:modelValue', value); error.value = ''; return value }
  catch (e) { error.value = t('admin.accounts.ticketProxy.errors.' + (e instanceof Error ? e.message : 'invalid_url')); return null }
}
function changeProtocol() {
  if (props.modelValue.includes('://')) emit('update:modelValue', props.modelValue.replace(/^[a-z0-9]+:\/\//i, protocol.value + '://'))
}
async function copy() {
  try { await navigator.clipboard.writeText(props.modelValue) } catch { error.value = t('admin.accounts.ticketProxy.copyFailed') }
}
async function test() {
  const draft = normalize()
  if (!draft || busy.value) return
  await nextTick()
  const v = version; busy.value = true; result.value = null
  try { const value = await codexTicketsAPI.testProxy(draft); if (v === version) result.value = value }
  catch { if (v === version) error.value = t('admin.accounts.ticketProxy.testFailed') }
  finally { busy.value = false }
}
</script>
