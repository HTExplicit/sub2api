<template>
  <BaseDialog :show="show" :title="t(`accountView.${capturedKind}Title`)" width="normal" @close="emit('close')">
    <p class="text-sm text-muted">{{ t('accountView.wholeDomain') }}</p>
    <p v-if="preview" class="mt-3 text-sm" data-test="cindy-cleanup-preview">{{ t(`accountView.${capturedKind}Confirm`, { count: preview.count }) }}</p>
    <p v-if="error" role="alert" class="mt-3 text-sm text-red-600">{{ error }}</p>
    <p v-if="conflict" role="status" class="mt-3 text-sm text-amber-700">{{ t('accountView.changed') }}</p>
    <div v-if="resultID" class="mt-3 flex items-center gap-2" data-test="cindy-cleanup-result">
      <span class="font-mono text-sm">#{{ resultID }}</span>
      <button type="button" class="btn btn-secondary" @click="openResult">{{ t('accountView.result') }}</button>
    </div>
    <template #footer>
      <button type="button" class="btn btn-secondary" @click="emit('close')">{{ t('common.cancel') }}</button>
      <button type="button" class="btn btn-secondary" :disabled="!available || loading" data-test="cindy-cleanup-repreview" @click="refreshPreview">{{ t('accountView.refreshPreview') }}</button>
      <button type="button" class="btn btn-danger" :disabled="!available || loading || !validPreview || !preview?.count || !!resultID" data-test="cindy-cleanup-submit" @click="submit">
        {{ loading ? t('common.submitting') : t('common.delete') }}
      </button>
    </template>
  </BaseDialog>
</template>
<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { useCindyDraft } from './nativeState'
import { useAccountJobsStore } from '@/stores/accountJobs'
import zh from './locales/zh'
import en from './locales/en'
import { cindyCleanupAPI, type CindyCleanupKind, type CindyCleanupPreview } from './api'

const props = defineProps<{ show: boolean; kind: CindyCleanupKind; available: boolean }>()
const emit = defineEmits<{ close: []; submitted: [] }>()
const { t } = useI18n({ useScope: 'local', messages: { zh, en } })
const jobs = useAccountJobsStore()
const draft = useCindyDraft('account-cleanup-draft')
const capturedKind = ref<CindyCleanupKind>(props.kind)
const preview = ref<CindyCleanupPreview | null>(null)
const validPreview = ref(false)
const conflict = ref(false)
const error = ref('')
const loading = ref(false)
const resultID = ref<number>()
let operationKey = '', revision = 0, disposed = false
let initializedKind: CindyCleanupKind | undefined
let controller: AbortController | undefined
const makeKey = () => `cindy-cleanup-${globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(36).slice(2)}`}`
function saveDraft() {
  // Restored preview data never authorizes a deletion; a fresh preview is mandatory.
  draft.value = JSON.stringify({ version: 1, kind: capturedKind.value, preview: preview.value, result_id: resultID.value, conflict: conflict.value })
}
function message(value: unknown) { return value instanceof Error ? value.message : t('common.operationFailed') }
function status(value: unknown) {
  if (!value || typeof value !== 'object') return undefined
  return (value as { status?: number; response?: { status?: number } }).status || (value as { response?: { status?: number } }).response?.status
}
async function refreshPreview() {
  if (!props.available || loading.value) return
  const current = ++revision
  controller?.abort(); controller = new AbortController()
  loading.value = true; validPreview.value = false; error.value = ''
  // A conflict starts a new explicit submission identity, never an automatic retry.
  if (conflict.value || resultID.value) { operationKey = makeKey(); resultID.value = undefined }
  try {
    const next = await cindyCleanupAPI.preview(capturedKind.value, operationKey, controller.signal)
    if (disposed || current !== revision) return
    if (!Number.isSafeInteger(next.count) || next.count < 0 || typeof next.fingerprint !== 'string' || !next.fingerprint) throw new Error(t('accountView.invalidPreview'))
    preview.value = { count: next.count, fingerprint: next.fingerprint }
    conflict.value = false; validPreview.value = true; saveDraft()
  } catch (value) { if (!disposed && current === revision) error.value = message(value) }
  finally { if (current === revision) loading.value = false }
}
async function submit() {
  if (!props.available || loading.value || !validPreview.value || !preview.value?.count || resultID.value) return
  const capturedPreview = { ...preview.value }, current = revision
  loading.value = true; error.value = ''
  try {
    const job = await cindyCleanupAPI.submit(capturedKind.value, capturedPreview, operationKey)
    if (disposed || current !== revision) return
    if (!Number.isSafeInteger(job.id) || job.id <= 0) throw new Error(t('common.operationFailed'))
    resultID.value = job.id; validPreview.value = false; saveDraft()
    emit('submitted')
    await jobs.openJob(job.id)
  } catch (value) {
    if (disposed || current !== revision) return
    if (status(value) === 409) { conflict.value = true; validPreview.value = false; saveDraft() }
    error.value = message(value)
  } finally { if (current === revision) loading.value = false }
}
function openResult() { if (resultID.value) void jobs.openJob(resultID.value) }
watch(() => [props.show, props.kind] as const, ([show, kind]) => {
  if (!show) return
  if (initializedKind === kind) return
  initializedKind = kind
  capturedKind.value = kind
  operationKey = makeKey(); validPreview.value = false; conflict.value = false; resultID.value = undefined; error.value = ''
  preview.value = null
  try {
    const saved = JSON.parse(draft.value || '{}')
    if (saved.version === 1 && saved.kind === capturedKind.value) {
      if (saved.preview && Number.isSafeInteger(saved.preview.count) && saved.preview.count >= 0 && typeof saved.preview.fingerprint === 'string') preview.value = { count: saved.preview.count, fingerprint: saved.preview.fingerprint }
      if (Number.isSafeInteger(saved.result_id) && saved.result_id > 0) resultID.value = saved.result_id
      conflict.value = saved.conflict === true
    }
  } catch { /* Ignore an invalid presentation draft. */ }
  if (!resultID.value) void refreshPreview()
}, { immediate: true })
watch(() => props.available, available => {
  if (!available) { revision++; validPreview.value = false; loading.value = false; controller?.abort() }
})
onBeforeUnmount(() => { disposed = true; revision++; controller?.abort() })
</script>
