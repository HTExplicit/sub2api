<template>
  <section class="space-y-4" aria-live="polite" data-ui="codex-runtime">
    <h2 class="text-xl font-semibold text-ink">{{ text('Codex 运行设置', 'Codex runtime settings') }}</h2>
    <p class="text-sm text-muted">{{ text('开启后，OAuth 账号发往 ChatGPT Codex 后端的流式 /responses 请求体按官方 Codex 客户端的方式以 zstd 压缩发送（', 'When enabled, streaming /responses request bodies that OAuth accounts send to the ChatGPT Codex backend are zstd-compressed (') }}<span class="whitespace-nowrap">Content-Encoding: zstd</span>{{ text('）；compact、models 等其他请求保持明文。关闭后全部以明文发送。', '), as the official Codex client does; compact, models and other requests stay uncompressed. When disabled, every request is sent uncompressed.') }}</p>
    <p v-if="loading" class="text-sm text-muted">{{ t('common.loading') }}</p>
    <fieldset v-else :disabled="!loaded" class="space-y-4 disabled:opacity-60">
      <label class="flex items-center gap-2">
        <input v-model="compression" data-test="codex-compression" type="checkbox" />
        {{ text('压缩 Codex Responses 请求体', 'Compress Codex Responses requests') }}
      </label>
      <button type="button" data-test="codex-save" :disabled="busy" class="btn btn-primary" @click="save">{{ text('保存设置', 'Save settings') }}</button>
    </fieldset>
    <div v-if="message" role="status" class="space-y-2 border border-line p-3 text-sm" :class="failure ? 'text-red-600' : 'text-ink'">
      <p>{{ message }}</p>
    </div>
    <TotpStepUpDialog :controller="stepUp" />
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { codexRuntimeAPI } from '@/api/admin/codexRuntime'
import { useAuthStore } from '@/stores/auth'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t, locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('en') ? en : zh
const auth = useAuthStore()
const stepUp = useStepUp()
const loading = ref(true), loaded = ref(false), busy = ref(false), failure = ref(false)
const compression = ref(true)
const message = ref('')
let generation = 0

function resetResult() { message.value = ''; failure.value = false }
async function load() {
  const current = ++generation
  loaded.value = false; loading.value = true; busy.value = false; resetResult()
  try {
    const settings = await codexRuntimeAPI.getSettings()
    if (current !== generation) return
    compression.value = settings.request_zstd
    loaded.value = true
  } catch (error) {
    if (current === generation) { failure.value = true; message.value = extractApiErrorMessage(error, t('common.error')) }
  } finally { if (current === generation) loading.value = false }
}
async function save() {
  if (busy.value || !loaded.value) return
  const current = generation, sent = compression.value
  busy.value = true; resetResult()
  try {
    await stepUp.run(() => codexRuntimeAPI.updateSettings({ request_zstd: sent }))
    // A save that finishes after the checkbox changed again is not reported as saved.
    if (current === generation && sent === compression.value) message.value = text('设置已保存', 'Settings saved')
  } catch (error) {
    if (current === generation && !isStepUpCancelled(error)) { failure.value = true; message.value = extractApiErrorMessage(error, t('common.error')) }
  } finally { if (current === generation) busy.value = false }
}
watch(() => auth.user?.id, () => { void load() }, { flush: 'sync' })
onMounted(() => { void load() })
onBeforeUnmount(() => { generation++ })
</script>
