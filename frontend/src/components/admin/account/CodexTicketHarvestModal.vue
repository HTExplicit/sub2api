<template>
  <AccountOperationDialog :show="show" :job="operationJob" :title="t('admin.accounts.tickets.title')" width="wide" @close="emit('close')">
    <div class="space-y-5">
      <p class="text-sm leading-relaxed text-muted">{{ t('admin.accounts.tickets.description') }}</p>
      <p v-if="!enabled && !loading" class="border-l-2 border-amber-500 bg-raised px-3 py-2 text-sm text-amber-700 dark:text-amber-300">{{ t('admin.accounts.tickets.disabled') }}</p>
      <div class="grid gap-2 sm:grid-cols-2">
        <label v-for="model in models" :key="model" class="flex cursor-pointer items-center gap-3 border p-3 transition-colors" :class="selected.includes(model) ? 'border-primary-500 bg-raised' : 'border-line'">
          <input v-model="selected" type="checkbox" :value="model" :disabled="busy" class="h-4 w-4 accent-primary-600" />
          <span class="text-sm font-medium text-ink">{{ model }}</span>
        </label>
      </div>
      <label class="flex items-center gap-2 text-xs text-muted"><input v-model="force" type="checkbox" :disabled="busy" />{{ t('admin.accounts.tickets.force') }}</label>
      <div class="max-h-[42vh] space-y-4 overflow-y-auto">
        <section v-for="row in rows" :key="row.id" class="border border-line">
          <div class="flex items-center justify-between gap-2 border-b border-line bg-raised px-4 py-3">
            <strong class="truncate text-sm text-ink">{{ row.account?.name || '#' + row.id }}</strong>
            <span v-if="row.account" class="text-xs text-muted">{{ row.account.type }}</span>
          </div>
          <p v-if="!row.account" class="p-4 text-sm text-muted">{{ row.error || t('common.loading') }}</p>
          <p v-else-if="!eligible(row.account)" class="p-4 text-sm text-amber-600">{{ t('admin.accounts.tickets.ineligible') }}</p>
          <div v-else class="divide-y divide-line px-4">
            <div v-for="model in selected" :key="model" class="space-y-2 py-3">
              <div class="flex flex-wrap items-center justify-between gap-2">
                <span class="text-sm font-medium text-ink">{{ model }}</span>
                <span class="text-xs" :class="ticket(row.account, model)?.ready ? 'text-primary-600 dark:text-primary-400' : 'text-muted'">{{ status(row.account, model) }}</span>
              </div>
              <dl class="grid gap-x-4 gap-y-1 text-xs text-muted sm:grid-cols-2">
                <div>{{ t('admin.accounts.tickets.expires') }} · {{ time(ticket(row.account, model)?.expires_at) }}</div>
                <div>{{ t('admin.accounts.tickets.next') }} · {{ time(ticket(row.account, model)?.next_attempt_at) }}</div>
                <div>{{ t('admin.accounts.tickets.last') }} · {{ time(ticket(row.account, model)?.last_attempt_at) }}</div>
              </dl>
              <p v-if="ticket(row.account, model)?.last_result?.message" class="text-xs leading-relaxed text-muted">{{ ticket(row.account, model)?.last_result?.message }}</p>
              <button v-if="!['idle', 'stopped'].includes(ticket(row.account, model)?.renewal_state || 'idle')" type="button" class="text-xs text-primary-600 hover:underline dark:text-primary-400" :disabled="busy" @click="stop(row.account, model)">{{ t('admin.accounts.tickets.stop') }}</button>
            </div>
          </div>
        </section>
      </div>
      <p class="text-xs text-muted">{{ t('admin.accountTasks.accountUnchanged') }}</p>
      <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
    </div>
    <template #footer>
      <button class="btn btn-secondary" :disabled="busy" @click="emit('close')">{{ t('common.close') }}</button>
      <button class="btn btn-primary" :disabled="!canSubmit" @click="submit">{{ t(busy ? 'common.submitting' : 'admin.accounts.tickets.start') }}</button>
    </template>
  </AccountOperationDialog>
</template>
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AccountOperationDialog from '@/components/admin/account-jobs/AccountOperationDialog.vue'
import { canManageCodexTickets } from '@/utils/codexTicketEligibility'
import { getById } from '@/api/admin/accounts'
import { codexTicketsAPI } from '@/api/admin/codexTickets'
import { accountJobIdempotencyHeaders, type AccountJob } from '@/api/admin/accountJobs'
import type { Account } from '@/types'
const props = defineProps<{ show: boolean; accountIds: number[] }>()
const emit = defineEmits<{ close: []; submitted: [job: AccountJob] }>()
const { t } = useI18n()
const operationJob = ref<AccountJob | null>(null)
const rows = ref<Array<{ id: number; account?: Account; error?: string }>>([])
const models = ref<string[]>([]), selected = ref<string[]>([])
const enabled = ref(false), busy = ref(false), loading = ref(false), force = ref(false), error = ref('')
let version = 0
let submission = accountJobIdempotencyHeaders('codex_ticket_harvest')
const eligible = canManageCodexTickets
const canSubmit = computed(() => !busy.value && !loading.value && enabled.value && selected.value.length > 0 && rows.value.length > 0 && rows.value.length <= 100 && rows.value.every(r => eligible(r.account)))
const ticket = (a: Account, model: string) => a.codex_turn_tickets?.find(value => value.model === model)
const time = (value?: string) => value ? new Date(value).toLocaleString() : '—'
function status(a: Account, model: string) {
  const value = a.codex_turn_tickets?.find(ticket => ticket.model === model)
  const phase = value?.renewal_state || 'idle'
  return `${value?.ready ? t('admin.accounts.tickets.valid') + ' · ' : ''}${t('admin.accounts.tickets.states.' + phase)}`
}
watch([selected, force], () => { submission = accountJobIdempotencyHeaders('codex_ticket_harvest') }, { deep: true })
watch(() => props.show, async show => {
  const v = ++version
  if (!show) return
  operationJob.value = null; enabled.value = false
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
  if (!canSubmit.value) return
  busy.value = true; error.value = ''
  try {
    const ids = rows.value.map(row => row.id)
    for (let i = 0; i < ids.length; i += 5) {
      const verified = await Promise.all(ids.slice(i, i + 5).map(async id => {
        try { return await getById(id) }
        catch { return null }
      }))
      if (verified.some(account => !eligible(account))) {
        error.value = t('admin.accountTasks.selectionChanged')
        return
      }
    }
    operationJob.value = await codexTicketsAPI.harvest(ids, selected.value, force.value, submission)
    emit('submitted', operationJob.value)
  }
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
