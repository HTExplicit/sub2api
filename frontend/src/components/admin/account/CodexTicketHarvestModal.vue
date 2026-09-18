<template>
  <BaseDialog :show="show" :title="t('admin.accounts.tickets.title')" width="wide" @close="emit('close')">
    <p class="mb-3 text-sm text-gray-500">{{ t('admin.accounts.tickets.description') }}</p>
    <p v-if="!enabled" class="mb-3 text-amber-600">{{ t('admin.accounts.tickets.disabled') }}</p>
    <div class="mb-3 flex gap-4">
      <label v-for="model in models" :key="model"><input v-model="selected" type="checkbox" :value="model" :disabled="busy" /> {{ model }}</label>
    </div>
    <label class="block mb-3"><input v-model="force" type="checkbox" :disabled="busy" /> {{ t('admin.accounts.tickets.force') }}</label>
    <div v-for="row in rows" :key="row.id" class="border-b py-2 text-sm">
      <strong>{{ row.account?.name || `#${row.id}` }}</strong>
      <span v-if="!row.account"> · {{ row.error || t('common.loading') }}</span>
      <span v-else-if="!eligible(row.account)" class="text-amber-600"> · {{ t('admin.accounts.tickets.ineligible') }}</span>
      <template v-else>
        <div v-for="model in selected" :key="model" class="flex justify-between gap-2">
          <span>{{ model }} · {{ status(row.account, model) }}</span>
          <button type="button" class="text-primary-600" :disabled="busy" @click="stop(row.account, model)">{{ t('admin.accounts.tickets.stop') }}</button>
        </div>
      </template>
    </div>
    <p v-if="error" role="alert" class="mt-3 text-red-600">{{ error }}</p>
    <template #footer>
      <button class="btn btn-secondary" @click="emit('close')">{{ t('common.close') }}</button>
      <button class="btn btn-primary" :disabled="busy || loading || !enabled || !selected.length || !eligibleIDs.length || rows.length > 100" @click="submit">{{ t('admin.accounts.tickets.start') }}</button>
    </template>
  </BaseDialog>
</template>
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { getById } from '@/api/admin/accounts'
import { codexTicketsAPI } from '@/api/admin/codexTickets'
import { accountJobIdempotencyHeaders, type AccountJob } from '@/api/admin/accountJobs'
import type { Account } from '@/types'
const props = defineProps<{ show: boolean; accountIds: number[] }>()
const emit = defineEmits<{ close: []; submitted: [job: AccountJob] }>()
const { t } = useI18n()
const rows = ref<Array<{ id: number; account?: Account; error?: string }>>([])
const models = ref<string[]>([]), selected = ref<string[]>([])
const enabled = ref(false), busy = ref(false), loading = ref(false), force = ref(false), error = ref('')
let version = 0
let submission = accountJobIdempotencyHeaders('codex_ticket_harvest')
const eligible = (a: Account) => a.platform === 'openai' && ['oauth', 'setup-token'].includes(a.type) && a.parent_account_id == null && a.status === 'active'
const eligibleIDs = computed(() => rows.value.filter(r => r.account && eligible(r.account)).map(r => r.id))
function status(a: Account, model: string) {
  const value = a.codex_turn_tickets?.find(ticket => ticket.model === model)
  const phase = value?.renewal_state || 'idle'
  return `${value?.ready ? t('admin.accounts.tickets.valid') + ' · ' : ''}${t('admin.accounts.tickets.states.' + phase)}`
}
watch([selected, force], () => { submission = accountJobIdempotencyHeaders('codex_ticket_harvest') }, { deep: true })
watch(() => props.show, async show => {
  const v = ++version
  if (!show) return
  error.value = ''; force.value = false; loading.value = true
  rows.value = [...new Set(props.accountIds)].map(id => ({ id }))
  submission = accountJobIdempotencyHeaders('codex_ticket_harvest')
  if (rows.value.length > 100) { error.value = t('admin.accounts.tickets.limit'); loading.value = false; return }
  try {
    const policy = await codexTicketsAPI.policy()
    if (v !== version) return
    enabled.value = policy.enabled; models.value = policy.models; selected.value = [...policy.models]
    // Bound admin reads as well as server-side harvesting.
    for (let i = 0; i < rows.value.length && v === version; i += 5) {
      await Promise.all(rows.value.slice(i, i + 5).map(async row => {
        try { const account = await getById(row.id); if (v === version) row.account = account }
        catch { row.error = t('admin.accounts.tickets.loadFailed') }
      }))
    }
  } catch { error.value = t('admin.accounts.tickets.loadFailed') }
  finally { if (v === version) loading.value = false }
}, { immediate: true })
async function submit() {
  if (busy.value) return
  busy.value = true; error.value = ''
  try { emit('submitted', await codexTicketsAPI.harvest(eligibleIDs.value, selected.value, force.value, submission)); emit('close') }
  catch { error.value = t('admin.accounts.tickets.submitFailed') }
  finally { busy.value = false }
}
async function stop(a: Account, model: string) {
  busy.value = true
  try {
    await codexTicketsAPI.stop(a.id, [model])
    const row = rows.value.find(r => r.id === a.id)
    if (row) row.account = await getById(a.id)
  } catch { error.value = t('admin.accounts.tickets.submitFailed') }
  finally { busy.value = false }
}
</script>
