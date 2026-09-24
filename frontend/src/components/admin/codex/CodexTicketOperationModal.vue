<template>
  <AccountOperationDialog :show="show" :title="text('Codex 路由采集与验证', 'Codex route acquisition and verification')" :job="job" @close="emit('close')">
    <div class="space-y-4">
      <p class="text-sm text-muted">{{ text('路由材料有效、响应完整、模型声明一致是不同证据。STATE 长度仅供观测，不能证明账号套餐或模型质量；本页操作不包含质量验证。', 'Valid routing material, a complete response and a matching model declaration are separate observations. STATE length proves neither account plan nor model quality; operations on this page do not test quality.') }}</p>
      <p class="text-sm text-muted">{{ text(`已选择 ${targetIDs.length} 个账号。每个账号和模型每周期最多一次采集加一次业务出口复验；有效资格默认跳过。`, `${targetIDs.length} accounts selected. Each account/model cycle has one acquisition and one business-route verification; valid qualifications are skipped.`) }}</p>
      <p v-if="loading" class="text-sm text-muted">{{ t('common.loading') }}</p>
      <fieldset :disabled="loading || submitting || !!job" class="space-y-4 disabled:opacity-60">
        <div class="flex flex-wrap gap-3">
          <label v-for="model in models" :key="model" class="flex items-center gap-2">
            <input v-model="selectedModels" type="checkbox" :value="model" :data-model="model" />{{ model }}
          </label>
        </div>
        <label v-if="targetOperation === 'harvest'" class="flex items-center gap-2">
          <input v-model="force" data-test="codex-force" type="checkbox" />
          {{ text('重新采集并复验仍有效的资格', 'Reacquire and verify a still-valid route') }}
        </label>
        <button type="button" data-test="codex-submit" class="btn btn-primary" :disabled="!selectedModels.length || !targetIDs.length" @click="submit">
          {{ targetOperation === 'stop' ? text('停止续期', 'Stop renewal') : text('开始采集', 'Start acquisition') }}
        </button>
      </fieldset>
      <p v-if="errorMessage" role="alert" class="text-sm text-red-600">{{ errorMessage }}</p>
    </div>
  </AccountOperationDialog>
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { accountJobIdempotencyHeaders, type AccountJob } from '@/api/admin/accountJobs'
import { codexTicketsAPI } from '@/api/admin/codexTickets'
import { useAccountJobsStore } from '@/stores/accountJobs'
import { useAuthStore } from '@/stores/auth'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import AccountOperationDialog from '@/components/admin/account-jobs/AccountOperationDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { isCodexTicketSelection, type CodexTicketOperation } from '@/utils/codexTickets'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = defineProps<{ show: boolean; accountIds: number[]; operation: CodexTicketOperation }>()
const emit = defineEmits<{ close: [] }>()
const { t, locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('en') ? en : zh
const auth = useAuthStore(), jobs = useAccountJobsStore()
const stepUp = useStepUp()
const targetIDs = ref<number[]>([]), targetOperation = ref<CodexTicketOperation>('harvest')
const models = ref<string[]>([]), selectedModels = ref<string[]>([])
const loading = ref(false), submitting = ref(false), force = ref(false), enabled = ref(false)
const errorMessage = ref(''), job = ref<AccountJob | null>(null)
let version = 0
let idempotency = accountJobIdempotencyHeaders('codex_ticket_harvest')

watch(() => props.show, async show => {
  const current = ++version, actor = auth.user?.id
  if (!show) return
  targetIDs.value = [...props.accountIds]
  targetOperation.value = props.operation
  idempotency = accountJobIdempotencyHeaders(props.operation === 'stop' ? 'codex_ticket_stop' : 'codex_ticket_harvest')
  models.value = []; selectedModels.value = []; force.value = false; job.value = null; errorMessage.value = ''; submitting.value = false; enabled.value = false
  loading.value = true
  try {
    const policy = await codexTicketsAPI.policy()
    if (current !== version || auth.user?.id !== actor) return
    enabled.value = policy.enabled
    models.value = [...policy.models]
    selectedModels.value = [...policy.models]
  } catch (error) {
    if (current === version && auth.user?.id === actor) errorMessage.value = extractApiErrorMessage(error, t('common.operationFailed'))
  } finally { if (current === version) loading.value = false }
}, { immediate: true })

async function submit() {
  if (submitting.value || job.value || loading.value || !selectedModels.value.length || !targetIDs.value.length) return
  const current = version, actor = auth.user?.id
  const ids = [...targetIDs.value], selected = [...selectedModels.value], operation = targetOperation.value, forced = force.value
  const key = idempotency
  const ensureCurrent = () => {
    if (!props.show || current !== version || auth.user?.id !== actor) throw new Error(t('common.operationFailed'))
  }
  submitting.value = true; errorMessage.value = ''
  try {
    if (operation === 'harvest' && !enabled.value) throw new Error(text('请先开启票据总开关', 'Enable route acquisition first.'))
    const fresh = await adminAPI.accounts.list(1, 100, { account_ids: ids.join(','), lite: '1', include_scheduler_score: '0' })
    ensureCurrent()
    if (fresh.total !== ids.length || fresh.items.length !== ids.length || !isCodexTicketSelection(ids, fresh.items)) {
      throw new Error(text('所选账号必须全部为非影子的 OpenAI OAuth 或 Setup Token 账号；每次请选择1至100个账号。', 'Select 1–100 OpenAI OAuth or Setup Token accounts; every account must be eligible and must not be a shadow.'))
    }
    const accepted = await stepUp.run(() => {
      ensureCurrent()
      return operation === 'stop' ? codexTicketsAPI.stopJob(ids, selected, key) : codexTicketsAPI.harvest(ids, selected, forced, key)
    })
    if (auth.user?.id !== actor) return
    if (current === version && props.show) job.value = accepted
    else jobs.track(accepted, { open: false })
  } catch (error) {
    if (current === version && auth.user?.id === actor && !isStepUpCancelled(error)) errorMessage.value = extractApiErrorMessage(error, t('common.operationFailed'))
  } finally { if (current === version) submitting.value = false }
}
watch(() => auth.user?.id, () => { version++; models.value = []; selectedModels.value = []; job.value = null })
onBeforeUnmount(() => { version++ })
</script>
